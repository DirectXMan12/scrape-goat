package discovery

import (
	"fmt"
	"regexp"
	"strings"

	// TODO: can this be taken from common/model/expformat?
	"github.com/prometheus/prometheus/pkg/labels"

	"github.com/directxman12/scrape-goat/pkg/config"
	"github.com/directxman12/scrape-goat/pkg/collector"
)

// Relabel knows how to relable Prometheus metrics.
type Relabeler interface {
	// Relabel modifies the given labels according to some logic,
	// returning the result.  It may modify the input.
	Relabel(target ScrapeTarget, lbls labels.Labels) labels.Labels
}

// fromFunc transforms an input pair of labels,
// and returns whether or not it was successful
type fromFunc func(ScrapeTarget, *labels.Label) bool

// createSourceAppender creates a fromFunc to append new labels based on the given
// SourceLabel config.  It returns the processing function if the source config had an
// appender component, or nil otherwise.
func createSourceAppender(cfg *config.SourceLabel, target string) (fromFunc, error) {
	if cfg.From != "" {
		parts := strings.Split(cfg.From, ":")
		return func(scrapeTarget ScrapeTarget, input *labels.Label) bool {
			val := scrapeTarget.SourceValue(parts...)
			input.Value = val
			input.Name = target
			// TODO: return false on error?
			return true
		}, nil
	}

	if cfg.Const != "" {
		return func(_ ScrapeTarget, input *labels.Label) bool {
			input.Value = cfg.Const
			input.Name = target
			return true
		}, nil
	}

	return nil, nil
}

// createSourceProcessor creates a fromFunc to process existing labels based on the given
// SourceLabel config.  It returns the processing function if the source config had a processor
// component, or nil otherwise.
func createSourceProcessor(cfg *config.SourceLabel, target string) (fromFunc, error) {
	if cfg.Matches != "" {
		re, err := regexp.Compile(cfg.Matches)
		if err != nil {
			return nil, err
		}
		return func(_ ScrapeTarget, input *labels.Label) bool {
			matches := re.FindStringSubmatchIndex(input.Name)
			if matches == nil {
				return false
			}
			res := string(re.ExpandString([]byte{}, target, input.Name, matches))
			input.Name = res
			return true
		}, nil
	}

	if cfg.Name != "" {
		return func(_ ScrapeTarget, input *labels.Label) bool {
			if input.Name == cfg.Name {
				return true
			}

			return false
		}, nil
	}

	return nil, nil
}

type relabeler struct {
	// processors filter or manipulate existing label pairs
	processors []fromFunc

	// appenders generate new label pairs
	appenders  []fromFunc
}

func (r *relabeler) Relabel(target ScrapeTarget, input labels.Labels) labels.Labels {
	output := input[:0]

InputLoop:
	for i := range input {
		// take reference to the actual item in the slice, not the iteration variable
		lblPair := &input[i]
		for _, from := range r.processors {
			if matched := from(target, lblPair); matched {
				// NB: we're overwriting the input as we go along, which is fine,
				// *as long as we never have more than one append*
				output = append(output, *lblPair)
				continue InputLoop
			}
		}

		// we didn't have any matches, so skip this by not appending it
	}

	// TODO: figure out a way to allow calculating these once per target ahead of time
	for _, from := range r.appenders {
		newPair := &labels.Label{}
		if ok := from(target, newPair); ok {
			output = append(output, *newPair)
		}
	}

	return output
}

type passThroughRelabeler struct{}

func (r *passThroughRelabeler) Relabel(_ ScrapeTarget, lbls labels.Labels) labels.Labels {
	return lbls
}

func NewRelabelerFromConfig(cfgs []*config.RelabelConfig) (Relabeler, error) {
	if len(cfgs) == 0 {
		return &passThroughRelabeler{}, nil
	}

	// TODO: handle actions
	processorFuncs := []fromFunc{}
	appenderFuncs := []fromFunc{}
	for _, cfg := range cfgs {
		procFunc, err := createSourceProcessor(&cfg.Source, cfg.Target)
		if err != nil {
			return nil, err
		}
		appFunc, err := createSourceAppender(&cfg.Source, cfg.Target)
		if err != nil {
			return nil, err
		}

		if appFunc == nil && procFunc == nil {
			return nil, fmt.Errorf("no source config specified for relabeling config %v", cfg)
		}

		if procFunc != nil {
			processorFuncs = append(processorFuncs, procFunc)
		}

		if appFunc != nil {
			appenderFuncs = append(appenderFuncs, appFunc)
		}
	}

	return &relabeler{
		processors: processorFuncs,
		appenders: appenderFuncs,
	}, nil
}

// relabelingProcessor is a collector.Processor that relabels series as it goes along.
type relabelingProcessor struct {
	Relabeler
	target ScrapeTarget
	next collector.Processor
}

func (p relabelingProcessor) Add(name string, labels labels.Labels, timestamp int64, value float64) collector.ParseHint {
	labels = p.Relabel(p.target, labels)
	return p.next.Add(name, labels, timestamp, value)
}

// NewRelabelingProcessor returns a collector.Processor that relabels
// based on the given Relabeler and ScrapeTarget.
func NewRelabelingProcessor(relabeler Relabeler, target ScrapeTarget, next collector.Processor) collector.Processor {
	return &relabelingProcessor{
		Relabeler: relabeler,
		target: target,
		next: next,
	}
}
