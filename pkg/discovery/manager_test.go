package discovery

import (
	"time"
	"math/rand"
	"net/http"
	"sync"
	"context"
	"bytes"
	"io"

	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/prometheus/prometheus/pkg/labels"
	"github.com/prometheus/common/model"

	"github.com/directxman12/scrape-goat/pkg/config"
	"github.com/directxman12/scrape-goat/pkg/collector"
	"github.com/directxman12/scrape-goat/pkg/export"
)

type fakeReqBuilder struct {}
func (b *fakeReqBuilder) RequestFor(host string, target ScrapeTarget) (*http.Request, error) {
	return &http.Request{}, nil
}

func randString(r *rand.Rand, maxSize int) string {
	start := int('a')
	end := int('z')
	rng := end - start

	size := r.Intn(maxSize-1)+1
	runes := make([]rune, size)
	for i := range runes {
		runes[i] = rune(start + r.Intn(rng))
	}

	return string(runes)
}

type fakeRawScraper struct {
	maxSeriesPer int
	data [][]byte
}

func preloadScraper(maxSeriesPer, sources int) *fakeRawScraper {
	rng := rand.New(rand.NewSource(1))

	data := make([][]byte, sources)
	for i := range data {
		numSeries := rng.Intn(maxSeriesPer-1)+1
		buff := new(bytes.Buffer)
		for i := 0; i < numSeries; i++ {
			name := randString(rng, 10)
			numLabels := rng.Intn(10)
			lbls := make(labels.Labels, numLabels)
			for i := range lbls {
				lbls[i] = labels.Label{
					Name: randString(rng, 10),
					Value: randString(rng, 20),
				}
			}

			ts := rng.Int63()
			val := rng.Float64()

			sample := export.NewSample(name, lbls, ts, val)
			// TODO: check for error
			if _, err := sample.WriteAsText(buff); err != nil {
				panic(fmt.Sprintf("error preloading test data: %v", err))
			}
			buff.WriteByte('\n')
		}
		data[i] = buff.Bytes()
	}
	return &fakeRawScraper{
		maxSeriesPer: maxSeriesPer,
		data: data,
	}
}

func (s *fakeRawScraper) ScrapeRawData(req *http.Request, ctx context.Context, w io.Writer) error {
	url := req.URL
	ind := int(url.Host[0]) + int(url.Host[1]) << 8 + int(url.Host[2]) << 16
	if ind < 0 || ind >= len(s.data) {
		return fmt.Errorf("invalid hostname (must be index into preloaded data")
	}
	_, err := w.Write(s.data[ind])
	return err
}

func (s *fakeRawScraper) newRaw() collector.RawScraper {
	return s
}

func intPtr(base int) *int {
	return &base
}

var _ = Describe("Discovery and Collection Integration", func() {
	configSets := []struct{
		name string
		timing config.TimingConfig
		limits config.LimitsConfig
		relabeling []*config.RelabelConfig
	}{
		{
			name: "trusted",
			timing: config.TimingConfig{
				ScrapeInterval: model.Duration(10*time.Second),
				ScrapeTimeout: model.Duration(5*time.Second),
			},
			limits: config.LimitsConfig{
				MaxFamilies: intPtr(1),
				MaxSeriesPerFamily: intPtr(0),
				MaxSeries: intPtr(0),
			},
			relabeling: []*config.RelabelConfig{
				{
					Source: config.SourceLabel{From: "metadata:namespace"},
					Target: "namespace",
				},
			},
		},
		{
			name: "infra",
			timing: config.TimingConfig{
				ScrapeInterval: model.Duration(10*time.Second),
				ScrapeTimeout: model.Duration(5*time.Second),
			},
			limits: config.LimitsConfig{
				MaxFamilies: intPtr(1),
				MaxSeriesPerFamily: intPtr(0),
				MaxSeries: intPtr(0),
			},
			relabeling: []*config.RelabelConfig{},
		},
		{
			name: "general",
			timing: config.TimingConfig{
				ScrapeInterval: model.Duration(10*time.Second),
				ScrapeTimeout: model.Duration(5*time.Second),
			},
			limits: config.LimitsConfig{
				MaxSeries: intPtr(10),
				MaxFamilies: intPtr(1),
				MaxSeriesPerFamily: intPtr(0),
			},
			relabeling: []*config.RelabelConfig{
				{
					Source: config.SourceLabel{From: "metadata:namespace"},
					Target: "namespace",
				},
				{
					Source: config.SourceLabel{From: "metadata:name"},
					Target: "name",
				},
				{
					Source: config.SourceLabel{Const: "user-provided"},
					Target: "metric_kind",
				},
				{
					Source: config.SourceLabel{Matches: ".*"},
					Target: "user-${0}",
				},
			},
		},
	}

	// preload before running measurements, and don't reload before each
	var fakeRawScraper *fakeRawScraper
	maxSourcesPerConfig := 10000
	fakeRawScraper = preloadScraper(1000, maxSourcesPerConfig)
	urlBuilder := &fakeReqBuilder{}

	Measure("it should handle lots of discovery endpoints effectively", func(b Benchmarker) {
		Skip("this is expensive")
		prov := collector.NewScraperProviderFromRaw(context.TODO(), fakeRawScraper.newRaw)
		mgr := NewDiscoveryManager(prov)

		sampleCount := 0
		b.Time("runtime", func() {

			wg := &sync.WaitGroup{}
			// use our own rand to avoid locking overhead
			rng := rand.New(rand.NewSource(time.Now().UnixNano()))
			samplesChan := make(chan *export.Sample, 100)
			for _, cfg := range configSets {
				relabeler, err := NewRelabelerFromConfig(cfg.relabeling)
				Expect(err).ToNot(HaveOccurred())

				limiter := NewSeriesFilter(*cfg.limits.MaxSeriesPerFamily, *cfg.limits.MaxSeries, *cfg.limits.MaxFamilies)
				runner := mgr.RunnerForConfig(cfg.name, relabeler, limiter, urlBuilder, time.Duration(cfg.timing.ScrapeTimeout))
				for i := 0; i < maxSourcesPerConfig; i++ {
					target := &fakeScrapeTarget{
						metadata: map[string]string{
							"metadata:name": randString(rng, 63),
							"metadata:namespace": randString(rng, 63),
						},
						hostname: string([]byte{byte(i & 255), byte((i >> 8) & 255), byte((i >> 16) & 255)}),
					}
					mgr.OnAdd(target, configSets[i % len(configSets)].name)
				}
				wg.Add(1)
				go func() {
					defer GinkgoRecover()
					defer wg.Done()
					err := runner.RunOnce()
					Expect(err).ToNot(HaveOccurred())
					runner.Samples(samplesChan)
				}()
			}
			wg.Wait()
			close(samplesChan)
			for _ = range samplesChan {
				sampleCount++
			}
			Expect(sampleCount).To(BeNumerically(">", 0))
		})

		b.RecordValue("Sample Count", float64(sampleCount))
	}, 10)
})
