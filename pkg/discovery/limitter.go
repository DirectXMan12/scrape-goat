package discovery

import (
	"github.com/directxman12/scrape-goat/pkg/collector"
	"github.com/prometheus/prometheus/pkg/labels"
)

// TODO: re-evaluate this logic
var globalMax = int(^uint(0) >> 1)

// SeriesFilter contains the basis for a collector.Processor which limits the
// number of series allowed to be ingested.
type SeriesFilter struct {
	// maxSeriesPerFamily controls the maximum number of series per metric name.
	maxSeriesPerFamily int
	// maxSeries controls the overall maximum number of allowed series.
	maxSeries int
	// maxFamilies controls the overall maximum number of allowed metrics.
	maxFamilies int
}

// NewSeriesFilter constructs a SeriesFilter capable of limitting the number of
// of series, metrics, and series per metric.  Passing a value of zero or less defaults
// to the maximum integer size.
func NewSeriesFilter(maxSeriesPerFamily, maxSeries, maxFamilies int) *SeriesFilter {
	if maxSeriesPerFamily <= 0 {
		maxSeriesPerFamily = globalMax
	}
	if maxSeries <= 0 {
		maxSeries = globalMax
	}
	if maxFamilies <= 0 {
		maxFamilies = globalMax
	}

	return &SeriesFilter{
		maxSeriesPerFamily: maxSeriesPerFamily,
		maxSeries: maxSeries,
		maxFamilies: maxFamilies,
	}
}

// seriesFilterProcessor is a collecotr.Processor which limits the number of metrics
// that are allowed to be ingested.
type seriesFilterProcessor struct {
	*SeriesFilter

	currSeriesInFamily map[string]int
	currSeries int

	next collector.Processor
}

// NewSeriesFilterProcessor returns a collector.Processor based on the rules defined in
// the given SeriesFilter.
func NewSeriesFilterProcessor(seriesFilter *SeriesFilter, next collector.Processor) collector.Processor {
	return &seriesFilterProcessor{
		SeriesFilter: seriesFilter,
		next: next,
		currSeriesInFamily: map[string]int{},
	}
}

func (s *seriesFilterProcessor) Add(name string, labels labels.Labels, timestamp int64, value float64) collector.ParseHint {
	// fast returns
	if s.currSeries >= s.maxSeries {
		return collector.HintSkipRestOfSeries
	}

	seriesInFamily, familyAlreadyPresent := s.currSeriesInFamily[name]

	if !familyAlreadyPresent && len(s.currSeriesInFamily) >= s.maxFamilies {
		return collector.HintSkipRestOfFamilies
	}

	if seriesInFamily >= s.maxSeriesPerFamily {
		return collector.HintSkipRestOfSeriesInFamily
	}

	// NB: we can't assume that metrics are necessarily ordered
	s.currSeries++
	s.currSeriesInFamily[name] = seriesInFamily + 1

	hint := s.next.Add(name, labels, timestamp, value)

	// always return the next filter's hint, if it suggested something
	// besides just "continue"
	if hint != collector.HintContinue {
		return hint
	}

	// hint at what the collector should do next, before it does it
	if s.currSeries >= s.maxSeries {
		return collector.HintSkipRestOfSeries
	}

	if len(s.currSeriesInFamily) >= s.maxFamilies {
		return collector.HintSkipRestOfFamilies
	}

	if seriesInFamily + 1 >= s.maxSeriesPerFamily {
		return collector.HintSkipRestOfSeriesInFamily
	}

	return collector.HintContinue
}
