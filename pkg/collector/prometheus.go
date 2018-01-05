package collector

import (
	"fmt"
	"unsafe"
	"io"
	"context"
	"net/http"
	"compress/gzip"
	"bufio"
	"bytes"
	"time"

	"github.com/prometheus/prometheus/pkg/pool"
	"github.com/prometheus/prometheus/pkg/textparse"

	// TODO: can this be taken from common/model/expformat?
	"github.com/prometheus/prometheus/pkg/labels"
	"github.com/prometheus/prometheus/pkg/timestamp"
)

// much of this is loosely inspired by the Prometheus scrape logic,
// which doesn't quite do what we want, and/or is private

// cacheEntry represents a parsed set of series information,
// plus the last cache iteration at which we saw that information.
type cacheEntry struct {
	name string
	labels labels.Labels

	// lastSeen tracks when we last saw this series, so
	// that we can manually GC unused cache entries
	lastSeen uint64
}

// scrapeCache allows quick lookup of previously parsed series information
// via hashing the unparsed string.
type scrapeCache struct {
	entries map[string]*cacheEntry

	// lastIteration is the count of updates
	// to the cache, used to GC old cache entries.
	lastIteration uint64
}

func newScrapeCache() *scrapeCache {
	return &scrapeCache{
		entries: map[string]*cacheEntry{},
	}
}

// finishIteration GCs old cache entries and increments our
// current iteration.
func (c *scrapeCache) finishIteration() {
	for rawVal, entry := range c.entries {
		if c.lastIteration - entry.lastSeen > 2 {
			delete(c.entries, rawVal)
		}
	}

	c.lastIteration++
}

// get attempts to fetch preparsed series info from the cache.
// If present, it marks the entry as recently seen, preventing GC.
func (c *scrapeCache) get(raw string) (*cacheEntry, bool) {
	entry, present := c.entries[raw]
	if !present {
		return nil, false
	}

	entry.lastSeen = c.lastIteration
	return entry, true
}

// set adds a cache entry mapping the given raw metric string to the
// given name and label set
func (c *scrapeCache) set(raw string, name string, lbls labels.Labels) *cacheEntry {
	entry := &cacheEntry{
		name: name,
		labels: lbls,
		lastSeen: c.lastIteration,
	}
	c.entries[raw] = entry
	return entry
}

// scraperProvider provides scrapers that scrape from certain endpoints.
type scraperProvider struct {
	ctx context.Context

	buffers *pool.BytesPool

	// override for testing without HTTP connections
	newRawScraper func() RawScraper
}

func NewScraperProvider(baseCtx context.Context, client *http.Client) ScraperProvider {
	return NewScraperProviderFromRaw(baseCtx, func() RawScraper { return newRawScraper(client) })
}

func NewScraperProviderFromRaw(baseCtx context.Context, newRaw func() RawScraper) ScraperProvider {
	return &scraperProvider{
		ctx: baseCtx,
		buffers: pool.NewBytesPool(163, 100e6, 3),
		newRawScraper: newRaw,
	}
}

func (p *scraperProvider) ScraperFor(baseRequest *http.Request, scrapeTimeout time.Duration, processor Processor) Scraper {
	return &scraper{
		buffers: p.buffers,
		lastScrapeSize: 0, // we initialize it on first run

		rawScraper: p.newRawScraper(),
		ctx: p.ctx,
		scrapeTimeout: scrapeTimeout,
		baseRequest: baseRequest,

		cache: newScrapeCache(),

		processor: processor,
	}
}

// scraper knows how to ingest metrics and forward them to a
// Processor for further processing.
type scraper struct {
	// buffers allows us to reuse an already-allocated bytes
	// of the appropriate size, based on our last known size.
	buffers *pool.BytesPool

	// lastScrapeSize is our last-known size of the scrape
	// (so we can estimate what size buffer we need)
	lastScrapeSize int

	rawScraper RawScraper
	ctx context.Context
	scrapeTimeout time.Duration
	baseRequest *http.Request

	// cache allows us to skip text parsing if we already have the metric
	// name and labels cached
	cache *scrapeCache

	processor Processor
}

// NB: Prometheus 2.0 dropped support for the protobuf exposition format
// It's convinient for us, so we may wish to support it in the future,
// but for now, just deal with the text format.

// scrape pulls raw content from the rawScraper, parsing it into
// an ingestible form and forwarding it to the processor.
func (s *scraper) Scrape() error {
	var bufferSlice []byte
	if s.lastScrapeSize != 0 {
		// acquire a buffer -- we later need to release it back into the pool
		bufferSlice = s.buffers.Get(s.lastScrapeSize)
	} else {
		bufferSlice = make([]byte, 0, 16000)
	}
	buf := bytes.NewBuffer(bufferSlice)

	scrapeCtx, cancel := context.WithTimeout(s.ctx, s.scrapeTimeout)

	defaultTS := timestamp.FromTime(time.Now())
	err := s.rawScraper.ScrapeRawData(s.baseRequest, scrapeCtx, buf)
	cancel()

	// TODO
	if err != nil {
		return err
	}

	bufferSlice = buf.Bytes()

	// make sure to release the buffer slice later
	defer s.buffers.Put(bufferSlice)

	// don't overwrite our last size if this size was
	// zero, since we probably still want to keep around
	// the non-zero size.
	if len(bufferSlice) > 0 {
		s.lastScrapeSize = len(bufferSlice)
	}

	return s.append(bufferSlice, defaultTS)
}

// yoloString converts a byte buffer to a "short-lived" string
// avoiding an extra allocation.
func yoloString(b []byte) string {
	return *((*string)(unsafe.Pointer(&b)))
}

// append actually converts a full buffer to series one at a time,
// forwarding them to the Processor.
func (s *scraper) append(buf []byte, defaultTimestamp int64) error {
	// TODO: can we actually use the reader directly?
	parser := textparse.New(buf)

	// TODO: make this at start scrape time
	skipSeries := false
	var lastName *string

ParseLoop:
	for parser.Next() {
		timestamp := defaultTimestamp
		metricBytes, parsedTimestamp, value := parser.At()
		if parsedTimestamp != nil {
			timestamp = *parsedTimestamp
		}

		// TODO: what are droped metrics

		cacheEntry, present := s.cache.get(yoloString(metricBytes))
		if !present {
			var labelSet labels.Labels
			metric := parser.Metric(&labelSet)
			// the first label should be __name__
			if labelSet[0].Name != labels.MetricName {
				return fmt.Errorf("expect %s as first label, but got %s", labels.MetricName, labelSet[0].Name)
			}
			name := labelSet[0].Value
			labelSet = labelSet[1:]
			cacheEntry = s.cache.set(metric, name, labelSet)
		}
		if skipSeries {
			if cacheEntry.name == *lastName {
				continue
			}

			skipSeries = false
			lastName = nil
		}

		continueHint := s.processor.Add(cacheEntry.name, cacheEntry.labels, timestamp, value)
		switch continueHint {
		case HintSkipRestOfSeriesInFamily:
			// skip all series with this metric name
			skipSeries = true
			lastName = &cacheEntry.name
		case HintSkipRestOfSeries:
			// skip all the rest of the metrics
			break ParseLoop
		}

		s.cache.finishIteration()
	}

	if err := parser.Err(); err != nil {
		return fmt.Errorf("error parsing metrics: %v", err)
	}

	return nil
}

const (
	version = "0.1.0-alpha.1"
	acceptHeader = `text/plain;version=0.0.4;q=1,*/*;q=0.1`
)

var (
	// TODO: user agent trickery?
	userAgentHeader = fmt.Sprintf("PrometheusScrapeProxy/%s (OpenShift Scrape Goat, like Prometheus/2.0, baaaaaah)", version)
)

// RawScraper knows how to get raw data from some source
// (can be implemented for testing purposes).
type RawScraper interface {
	// ScrapeRawData scrapes raw metrics data from the given request to the given writer.
	ScrapeRawData(targetReq *http.Request, ctx context.Context, w io.Writer) error
}

// rawHTTPScraper knows how to connect to some metrics endpoint,
// pulling text-formatted metric data and ungziping it if necessary.
type rawHTTPScraper struct {
	client *http.Client

	gunzipper *gzip.Reader
	gunzipBuffer *bufio.Reader
}

func newRawScraper(client *http.Client) RawScraper {
	return &rawHTTPScraper{
		client: client,
	}
}

func (s *rawHTTPScraper) ScrapeRawData(targetReq *http.Request, ctx context.Context, w io.Writer) error {
	targetReq.Header.Set("Accept", acceptHeader)
	targetReq.Header.Set("Accept-Encoding", "gzip")
	targetReq.Header.Set("User-Agent", userAgentHeader)

	req := targetReq.WithContext(ctx)
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("metrics endpoint returned non-%v status %q", http.StatusOK, resp.Status)
	}

	if resp.Header.Get("Content-Encoding") != "gzip" {
		_, err := io.Copy(w, resp.Body)
		return err
	}

	// TODO: cap this at a certain size so hostile users can't overwhelm us?
	if s.gunzipper == nil {
		s.gunzipBuffer = bufio.NewReader(resp.Body)
		s.gunzipper, err = gzip.NewReader(s.gunzipBuffer)
		if err != nil {
			return err
		}
	} else {
		s.gunzipBuffer.Reset(resp.Body)
		s.gunzipper.Reset(s.gunzipBuffer)
	}

	_, err = io.Copy(w, s.gunzipper)
	s.gunzipper.Close()
	return err
}
