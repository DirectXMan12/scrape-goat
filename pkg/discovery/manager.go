package discovery

import (
	"time"
	"fmt"
	"sync"

	"github.com/golang/glog"
	"k8s.io/client-go/tools/cache"
	"github.com/prometheus/prometheus/pkg/labels"

	"github.com/directxman12/scrape-goat/pkg/collector"
	"github.com/directxman12/scrape-goat/pkg/export"
)

const (
	indexDiscoveryGroup = "discoveryGroup"
	metricsBufferSize = 1000
)

type groupedTarget struct {
	discoveryGroup string
	ScrapeTarget
}

func indexGroupedTargetByDiscoveryGroup(raw interface{}) ([]string, error) {
	obj, ok := raw.(*groupedTarget)
	if !ok {
		return nil, fmt.Errorf("non-scrape-target %v object passed to the indexing func", raw)
	}

	return []string{obj.discoveryGroup}, nil
}

func groupedTargetKeyFunc(raw interface{}) (string, error) {
	obj, ok := raw.(*groupedTarget)
	if !ok {
		return "", fmt.Errorf("non-scrape-target %v object passed to the indexing func", raw)
	}
	return obj.Key(), nil
}

// Runner knows how to gather metrics for one named discovery/filtering/relabeling
// configuration.
type Runner interface {
	// RunOnce immediately collects metrics from all sources for this configuration.
	RunOnce() error

	// RunUntil periodically collects metrics from all sources for this configuration,
	// until the given channel is closed.
	RunUntil(interval time.Duration, stopCh <-chan struct{}) error

	export.SampleProvider
}

// configRunner runs collection for some individual set of configuration.
type configRunner struct {
	relabeler Relabeler
	limiter *SeriesFilter
	reqBuilder ReqBuilder
	scraperProv collector.ScraperProvider

	timeout time.Duration

	targetCache cache.Indexer

	identity string

	sampleMu sync.RWMutex
	lastSamples []*export.Sample
}

func (r *configRunner) RunOnce() error {
	targets, err := r.targetCache.ByIndex(indexDiscoveryGroup, r.identity)
	if err != nil {
		return fmt.Errorf("error listing discovery targets for collector %q: %v", r.identity, err)
	}

	sampleCh := make(chan *export.Sample, 100)

	wg := &sync.WaitGroup{}
	wg.Add(len(targets))
	for _, target := range targets {
		go func(tgt ScrapeTarget) {
			r.processTarget(tgt, sampleCh)
			wg.Done()
		}(target.(ScrapeTarget))
	}

	// wait until this round has metrics collected to start the next one
	// TODO: revisit this decision (should bad actors hold up the process at all?)
	go func() {
		wg.Wait()
		close(sampleCh)
	}()

	var res []*export.Sample
	for sample := range sampleCh {
		res = append(res, sample)
	}

	r.sampleMu.Lock()
	defer r.sampleMu.Unlock()
	r.lastSamples = res

	return nil
}

func (r *configRunner) RunUntil(interval time.Duration, stopCh <-chan struct{}) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
RunLoop:
	for {
		select {
		case tickTime := <-ticker.C:
			glog.V(6).Infof("tick for collector %q at %s", r.identity, tickTime.String())

			if err := r.RunOnce(); err != nil {
				glog.Errorf("[manager] %v", err)
				continue RunLoop
			}

			nowTime := time.Now()
			glog.V(6).Infof("end collection for collector %q at %s (took %s)", r.identity, nowTime.String(), nowTime.Sub(tickTime).String())
		case <-stopCh:
			break RunLoop
		}
	}

	glog.Infof("[manager] collector %s stopped", r.identity)
	return nil
}

func (r *configRunner) Samples(sink chan<- *export.Sample) {
	r.sampleMu.RLock()
	defer r.sampleMu.RUnlock()

	for _, sample := range r.lastSamples {
		sink <- sample
	}
}

type channelSinkProcessor chan<- *export.Sample
func (p channelSinkProcessor) Add(name string, lbls labels.Labels, timestamp int64, value float64) collector.ParseHint {
	p <- export.NewSample(name, lbls, timestamp, value)
	return collector.HintContinue
}


// processTarget takes a set of discovery targets and collects metrics from them,
// processing the metrics for re-export
func (r *configRunner) processTarget(target ScrapeTarget, sampleChan chan<- *export.Sample) {
	// TODO: recreating these each time could create a lot of garbage really quickly
	processorChain := NewSeriesFilterProcessor(r.limiter,
		NewRelabelingProcessor(r.relabeler, target, channelSinkProcessor(sampleChan)))

	for _, host := range target.Hostnames() {
		// TODO: can/should we try to cache these to avoid re-constructing the URL every time?
		targetReq, err := r.reqBuilder.RequestFor(host, target)
		if err != nil {
			glog.Errorf("error processing discovered target %s for collector %q: %v", target.Key(), r.identity, err)
			continue
		}
		scraper := r.scraperProv.ScraperFor(targetReq, r.timeout, processorChain)
		// TODO: make these separate goroutines?
		if glog.V(10) {
			glog.Infof("[discovery] scraping endpoint %s for config %q: %s", targetReq.URL.String(), r.identity, reqToCurlCommand(targetReq))
		}
		if err := scraper.Scrape(); err != nil {
			// TODO: report this better?
			glog.Errorf("error processing discovered target %s for collector %q: %v", targetReq.URL.String(), r.identity, err)
		}
	}
}

// TODO: allow extra label injection for group identity (similar to Prometheus "job" label)?

// DiscoveryManager runs discovery and processing, managing groups of configRunners
// for each known configuration.  It receives processing events from some discovery
// source.
type DiscoveryManager struct {
	// discoveryTargets manages the known set of scrape targets
	discoveryTargets cache.Indexer

	// metricsDump is where metrics collectors send collected
	// metrics for grouping
	metricsDump chan *export.Sample

	scraperProv collector.ScraperProvider
}

// NewDiscoveryManager constructs a new discovery manager which scrapes using
// the given provider of Scrapers.  The discovery manager can be registered
// as a TargetHandler to receive new ScrapeTargets.
func NewDiscoveryManager(scraperProv collector.ScraperProvider) *DiscoveryManager {
	return &DiscoveryManager{
		discoveryTargets: cache.NewIndexer(groupedTargetKeyFunc, cache.Indexers{
			indexDiscoveryGroup: indexGroupedTargetByDiscoveryGroup,
		}),
		metricsDump: make(chan *export.Sample, 100),
		scraperProv: scraperProv,
	}
}

// MetricsChannel returns the channel that metrics consumers can use to read metrics
// from this manager.  If the channel is ever closed, consumers should request a new
// one using this method.
func (m *DiscoveryManager) MetricsChannel() <-chan *export.Sample {
	return m.metricsDump
}

// RunnerForConfig produces a Runner to run collection for scrape targets associated with the
// given name, feeding them first through the given limiter, and then the relabeler.  Collection is
// no longer than the given limit for each endpoint.
func (m *DiscoveryManager) RunnerForConfig(name string, relabeler Relabeler, limiter *SeriesFilter, reqBuilder ReqBuilder, perEndpointTimeout time.Duration) Runner {
	return &configRunner{
		relabeler: relabeler,
		limiter: limiter,
		timeout: perEndpointTimeout,
		identity: name,
		scraperProv: m.scraperProv,
		targetCache: m.discoveryTargets,
		reqBuilder: reqBuilder,
	}
}

func (m *DiscoveryManager) OnAdd(target ScrapeTarget, discoveryGroups ...string) {
	if len(discoveryGroups) < 1 {
		glog.Errorf("target %v did not belong to any configurations, ignoring", target)
		return
	}

	discoveryGroup := discoveryGroups[0]
	if len(discoveryGroups) > 1 {
		glog.Errorf("target %v belonged to more than one configuration, using the first (%q)", target, discoveryGroup)
	}

	grpTarget := &groupedTarget{
		discoveryGroup: discoveryGroup,
		ScrapeTarget: target,
	}

	glog.V(6).Infof("discovered target %q for rule %v", target.Key(), discoveryGroup)
	if err := m.discoveryTargets.Add(grpTarget); err != nil {
		glog.Errorf("error adding scrape target %v to cache: %v", target, err)
	}
}

func (m *DiscoveryManager) OnDelete(target ScrapeTarget) {
	grpTarget := &groupedTarget{
		ScrapeTarget: target,
	}

	glog.V(6).Infof("removed target %q", target.Key())
	if err := m.discoveryTargets.Delete(grpTarget); err != nil {
		glog.Errorf("error deleting scrape target %v from cache: %v", target, err)
	}
}
