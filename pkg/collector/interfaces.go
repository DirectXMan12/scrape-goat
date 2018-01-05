package collector

import (
	"time"
	"net/http"

	// TODO: can this be taken from common/model/expformat?
	"github.com/prometheus/prometheus/pkg/labels"
)

// ParseHint represents a hint from Processors to other processors
// in the chain, or to the source collector, about how to continue
// collecting.
type ParseHint uint64

var (
	HintContinue                 ParseHint = 0
	HintSkipRestOfSamples        ParseHint = 1
	HintSkipRestOfSeriesInFamily ParseHint = 2
	HintSkipRestOfFamilies       ParseHint = 3
	HintSkipRestOfSeries         ParseHint = 4
)

// Processor represents something that can ingest metric samples.
type Processor interface {
	// Add ingests a metric sample.  It may use a return code to
	// hint to the parser that certain further classes of metrics
	// may be dropped, but should not rely on the parser always
	// fully complying with the hint.
	Add(name string, labels labels.Labels, timestamp int64, value float64) ParseHint
}

// Scraper knows how to scrape some endpoint for metrics.
type Scraper interface {
	// Scrape scrapes metrics, passing them on to a configured Processor, or similar.
	Scrape() error
}

// ScrapeProvider knows how to create Scrapers given a base request, a timeout, and a Processor.
type ScraperProvider interface {
	// ScraperFor creates a new Scraper that pulls from the given URL,
	// dumping metrics into the given Processor.
	ScraperFor(baseRequest *http.Request, scrapeTimeout time.Duration, processor Processor) Scraper
}
