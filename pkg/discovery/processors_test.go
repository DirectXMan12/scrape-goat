package discovery

import (
	"fmt"
	"strings"
	"reflect"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

	"github.com/directxman12/scrape-goat/pkg/collector"
	"github.com/directxman12/scrape-goat/pkg/export"
	"github.com/directxman12/scrape-goat/pkg/config"
	"github.com/prometheus/prometheus/pkg/labels"
)

type fakeProcessor struct {
	results []export.Sample
}

func (f *fakeProcessor) Add(name string, labels labels.Labels, timestamp int64, value float64) collector.ParseHint {
	f.results = append(f.results, *export.NewSample(name, labels, timestamp, value))
	return collector.HintContinue
}

const (
	maxFamilies = 11
	maxSeries = 100
	maxSeriesPerFamily = 9
)

type fakeScrapeTarget struct {
	metadata map[string]string
	hostname string
}

func (t *fakeScrapeTarget) SourceValue(path ...string) string {
	return t.metadata[strings.Join(path, ":")]
}
func (t *fakeScrapeTarget) Hostnames() []string { return []string{t.hostname} }
func (t *fakeScrapeTarget) Key() string { return t.hostname }
func (t *fakeScrapeTarget) Path() string { return "/" }
func (t *fakeScrapeTarget) Scheme() string { return "https" }

func newSingleLabel(name, value string) labels.Labels {
	return labels.Labels{
		labels.Label{
			Name: name,
			Value: value,
		},
	}
}

type haveResultOfMatcher struct {
	expected []export.Sample
	startIndex int
	full bool
}

func (m *haveResultOfMatcher) Match(actualRaw interface{}) (bool, error) {
	actual, ok := actualRaw.(*fakeProcessor)
	if !ok {
		return false, fmt.Errorf("HaveResultOf matcher expects a fakeProcessor")
	}

	// allow starting at end
	startIndex := m.startIndex
	if startIndex < 0 {
		startIndex = len(actual.results) - len(m.expected)
	}

	// if we're going for a full match, make sure there's nothing left over
	if m.full && len(m.expected) + startIndex != len(actual.results) {
		return false, nil
	}

	// make sure we have enough items
	if len(actual.results) < startIndex || len(actual.results[startIndex:]) < len(m.expected) {
		return false, nil
	}

	// check each item
	for i, actualSample := range actual.results[startIndex:] {
		if !reflect.DeepEqual(m.expected[i],  actualSample) {
			return false, nil
		}
	}

	return true, nil
}

func (m *haveResultOfMatcher) FailureMessage(actualRaw interface{}) string {
	actual := actualRaw.(*fakeProcessor)

	// if we're going for a full match, make sure there's nothing left over
	if m.full {
		if m.startIndex > 0 {
			return fmt.Sprintf("Expected\n\t%#v\nto contain only the results\n\t%#v\nstarting at result %v", actual.results, m.expected, m.startIndex)
		} else if m.startIndex == 0 {
			return fmt.Sprintf("Expected\n\t%#v\nto contain only the results\n\t%#v", actual.results, m.expected)
		} else {
			return fmt.Sprintf("Expected\n\t%#v\nto contain only the results\n\t%#v\nat the end", actual.results, m.expected)
		}
	}

	if m.startIndex > 0 {
		return fmt.Sprintf("Expected\n\t%#v\nto contain the results\n\t%#v\nstarting at result %v", actual.results, m.expected, m.startIndex)
	} else if m.startIndex == 0 {
		return fmt.Sprintf("Expected\n\t%#v\nto contain the results\n\t%#v", actual.results, m.expected)
	} else {
		return fmt.Sprintf("Expected\n\t%#v\nto contain the results\n\t%#v\nat the end", actual.results, m.expected)
	}
}

func (m *haveResultOfMatcher) NegatedFailureMessage(actualRaw interface{}) string {
	actual := actualRaw.(*fakeProcessor)

	// if we're going for a full match, make sure there's nothing left over
	if m.full {
		if m.startIndex > 0 {
			return fmt.Sprintf("Expected\n\t%#v\nnot to contain only the results\n\t%#v\nstarting at result %v", actual.results, m.expected, m.startIndex)
		} else {
			return fmt.Sprintf("Expected\n\t%#v\nnot to contain only the results\n\t%#v", actual.results, m.expected)
		}
	}

	if m.startIndex > 0 {
		return fmt.Sprintf("Expected\n\t%#v\nnot to contain the results\n\t%#v\nstarting at result %v", actual.results, m.expected, m.startIndex)
	} else {
		return fmt.Sprintf("Expected\n\t%#v\nnot to contain the results\n\t%#v", actual.results, m.expected)
	}
}

func HaveLastResultsOf(res ...export.Sample) types.GomegaMatcher {
	return &haveResultOfMatcher{
		expected: res,
		full: true,
		startIndex: -1,
	}
}

var _ = Describe("Sample Filters", func() {
	Describe("Limiting Filter", func() {
		It("should limit the max number of series collected", func() {
			seriesFilter := NewSeriesFilter(0, maxSeries, 0)
			nextProc := &fakeProcessor{}
			filter := NewSeriesFilterProcessor(seriesFilter, nextProc)

			By("hinting to continue when adding new series would still be acceptible")
			for i := 0; i < maxSeries-1; i++ {
				hint := filter.Add(fmt.Sprintf("series%v", i), labels.Labels{}, 0, 0.0)
				Expect(hint).To(Equal(collector.HintContinue))
			}

			By("hinting to stop when adding new series would go over the max")
			hint := filter.Add("series_final", labels.Labels{}, 0, 0.0)
			Expect(hint).To(Equal(collector.HintSkipRestOfSeries))

			By("refusing to add any more new series once the limit is reached, and continuing to hint")
			hint = filter.Add("series_extra2", labels.Labels{}, 0, 0.0)
			Expect(hint).To(Equal(collector.HintSkipRestOfSeries))

			Expect(nextProc.results).To(HaveLen(maxSeries))
			Expect(nextProc).To(HaveLastResultsOf(
				*export.NewSample("series_final", labels.Labels{}, 0, 0.0)))
		})

		It("should limit the max number of metric families and series per family collected", func() {
			seriesFilter := NewSeriesFilter(maxSeriesPerFamily, 0, maxFamilies)
			nextProc := &fakeProcessor{}
			filter := NewSeriesFilterProcessor(seriesFilter, nextProc)

			By("hinting to continue when adding new families and series would still be acceptible")
			for i := 0; i < maxFamilies-1; i++ {
				for j := 0; j < maxSeriesPerFamily-1; j++ {
					hint := filter.Add(fmt.Sprintf("family%v", i), newSingleLabel("num", fmt.Sprintf("%v", j)), 0, 0.0)
					Expect(hint).To(Equal(collector.HintContinue))
				}
			}

			By("hinting to skip the rest of the series in this family when no new series in a family would be acceptible")
			hint := filter.Add("family0", newSingleLabel("num", "final"), 0, 0.0)
			Expect(hint).To(Equal(collector.HintSkipRestOfSeriesInFamily))
			Expect(nextProc).To(HaveLastResultsOf(
				*export.NewSample("family0", newSingleLabel("num", "final"), 0, 0.0)))

			By("hinting to stop adding families when adding new families would go over the max")
			hint = filter.Add("family_last", labels.Labels{}, 0, 0.0)
			Expect(hint).To(Equal(collector.HintSkipRestOfFamilies))

			By("refusing to add any more new families once the limit is reached, and continuing to hint")
			hint = filter.Add("family_extra2", labels.Labels{}, 0, 0.0)
			Expect(hint).To(Equal(collector.HintSkipRestOfFamilies))

			Expect(nextProc.results).To(HaveLen((maxFamilies-1)*(maxSeriesPerFamily-1) + 2))
			Expect(nextProc).To(HaveLastResultsOf(
				*export.NewSample("family_last", labels.Labels{}, 0, 0.0)))

			By("continuing to allow additions to any existing families under the limit")
			hint = filter.Add("family1", newSingleLabel("extra", "1"), 0, 0.0)
			Expect(hint).To(Equal(collector.HintSkipRestOfFamilies))
			Expect(nextProc).To(HaveLastResultsOf(
				*export.NewSample("family1", newSingleLabel("extra", "1"), 0, 0.0)))
		})
	})

	Describe("Relabeling Filter", func() {
		It("should allow replacing label values with scrape target metadata", func() {
			fakeTarget := &fakeScrapeTarget{
				metadata: map[string]string{"metadata:namespace": "some-ns"},
			}
			relabeler, err := NewRelabelerFromConfig([]*config.RelabelConfig{
				{
					Source: config.SourceLabel{
						From: "metadata:namespace",
					},
					Target: "ns",
				},
			})
			Expect(err).To(BeNil())

			nextProc := &fakeProcessor{}
			proc := NewRelabelingProcessor(relabeler, fakeTarget, nextProc)

			By("injecting a new label if none is present")
			proc.Add("some_series", newSingleLabel("other", "some-val"), 0, 0.0)
			Expect(nextProc).To(HaveLastResultsOf(
				*export.NewSample("some_series", newSingleLabel("ns", "some-ns"), 0, 0.0)))

			By("replacing an existing label if the label name is already present")
			fakeTarget.metadata["metadata:namespace"] = "other-ns"
			proc.Add("some_series", newSingleLabel("ns", "some-ns"), 0, 0.0)
			Expect(nextProc).To(HaveLastResultsOf(
				*export.NewSample("some_series", newSingleLabel("ns", "other-ns"), 0, 0.0)))
		})

		It("should allow injecting labels with constant values", func() {
			fakeTarget := &fakeScrapeTarget{}
			relabeler, err := NewRelabelerFromConfig([]*config.RelabelConfig{
				{
					Source: config.SourceLabel{
						Const: "some-value",
					},
					Target: "const_lbl",
				},
			})
			Expect(err).To(BeNil())

			nextProc := &fakeProcessor{}
			proc := NewRelabelingProcessor(relabeler, fakeTarget, nextProc)

			By("injecting a new label if none is present")
			proc.Add("some_series", newSingleLabel("other", "some-val"), 0, 0.0)
			Expect(nextProc).To(HaveLastResultsOf(
				*export.NewSample("some_series", newSingleLabel("const_lbl", "some-value"), 0, 0.0)))

			By("replacing an existing label if the label name is already present")
			proc.Add("some_series", newSingleLabel("const_lbl", "other-value"), 0, 0.0)
			Expect(nextProc).To(HaveLastResultsOf(
				*export.NewSample("some_series", newSingleLabel("const_lbl", "some-value"), 0, 0.0)))

		})

		It("should allow taking certain label names and renaming them", func() {
			fakeTarget := &fakeScrapeTarget{}
			relabeler, err := NewRelabelerFromConfig([]*config.RelabelConfig{
				{
					Source: config.SourceLabel{
						Matches: ".*",
					},
					Target: "user-${0}",
				},
			})
			Expect(err).To(BeNil())

			nextProc := &fakeProcessor{}
			proc := NewRelabelingProcessor(relabeler, fakeTarget, nextProc)
			Expect(err).To(BeNil())


			initialLabels := labels.Labels{
				{"label1", "val1"}, {"label2", "val2"},
			}

			proc.Add("some_series", initialLabels, 0, 0.0)
			Expect(nextProc).To(HaveLastResultsOf(
				*export.NewSample("some_series",
					labels.Labels{{"user-label1", "val1"}, {"user-label2", "val2"}}, 0, 0.0)))
		})

		It("should check rules in order to allow specific rules and catch-alls", func() {
			fakeTarget := &fakeScrapeTarget{}
			relabeler, err := NewRelabelerFromConfig([]*config.RelabelConfig{
				{
					Source: config.SourceLabel{
						Matches: "cheddar_(.*)",
					},
					Target: "cheese_${1}",
				},
				{
					Source: config.SourceLabel{
						Matches: ".*",
					},
					Target: "user_${0}",
				},
			})
			Expect(err).To(BeNil())

			nextProc := &fakeProcessor{}
			proc := NewRelabelingProcessor(relabeler, fakeTarget, nextProc)
			Expect(err).To(BeNil())


			initialLabels := labels.Labels{
				{"cheddar_sharp", "val1"}, {"crackers", "val2"},
			}

			proc.Add("some_series", initialLabels, 0, 0.0)
			Expect(nextProc).To(HaveLastResultsOf(
				*export.NewSample("some_series",
					labels.Labels{{"cheese_sharp", "val1"}, {"user_crackers", "val2"}}, 0, 0.0)))
		})
	})
})
