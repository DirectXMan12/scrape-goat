package discovery

import (
	"github.com/directxman12/scrape-goat/pkg/config"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1 "k8s.io/api/core/v1"
)

type fakeFilterInfo struct {
	Filter
	ident string
}

type fakeNamespaceConditionLookup struct {
	filters map[string]Filter
	namespaces map[string]*corev1.Namespace
}

func (l *fakeNamespaceConditionLookup) NamespaceMatchesCondition(namespace string, conditionIdentity string) bool {
	filter, ok := l.filters[conditionIdentity]
	if !ok {
		return false
	}

	ns, ok := l.namespaces[namespace]
	if !ok {
		return false
	}

	return filter.Filter(ns)
}
func (l *fakeNamespaceConditionLookup) RegisterNamespaceCondition(filter Filter, conditionIdentity string) {
	l.filters[conditionIdentity] = filter
}

type fakeFilterRegister struct {
	filters map[schema.GroupKind][]fakeFilterInfo
}
func(r *fakeFilterRegister) RegisterFilter(gk schema.GroupKind, filter Filter, ident string) {
	r.filters[gk] = append(r.filters[gk], fakeFilterInfo{filter, ident})
}

var _ = Describe("ScrapeTarget Filters", func() {
	plainTarget1 := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name: "plain-obj2",
			Namespace: "some-ns",
		},
	}
	plainTarget2 := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name: "plain-obj1",
			Namespace: "other-ns",
		},
	}
	labeledTarget1 := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name: "labeled-obj1",
			Namespace: "some-ns",
			Labels: map[string]string{
				"lbl1": "val1-1",
				"lbl2": "val1-2",
			},
		},
	}
	altLabeledTarget1 := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name: "labeled-obj1a",
			Namespace: "some-ns",
			Labels: map[string]string{
				"lbl1": "val1-1",
				"lbl2": "val1-2",
			},
		},
	}
	labeledTarget2 := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name: "labeled-obj2",
			Namespace: "some-ns",
			Labels: map[string]string{
				"lbl1": "val2-1",
				"lbl2": "val2-2",
			},
		},
	}
	annotatedTarget1 := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name: "annotated-obj1",
			Namespace: "some-ns",
			Annotations: map[string]string{
				"annotation-1": "aval1-1",
				"annotation-2": "aval1-2",
				"annotation-3": "avalx-3",
			},
		},
	}
	annotatedTarget2 := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name: "annotated-obj2",
			Namespace: "some-ns",
			Annotations: map[string]string{
				"annotation-1": "aval2-1",
				"annotation-2": "aval2-2",
				"annotation-3": "avalx-3",  // deliberately the same
			},
		},
	}

	allTargets := []runtime.Object{
		plainTarget1,
		plainTarget2,
		labeledTarget1,
		altLabeledTarget1,
		labeledTarget2,
		annotatedTarget1,
		annotatedTarget2,
	}

	var (
		builder *ConditionFilterBuilder
		lookup  *fakeNamespaceConditionLookup
		reg     *fakeFilterRegister
	)

	JustBeforeEach(func() {
		lookup = &fakeNamespaceConditionLookup{
			filters: map[string]Filter{},
			namespaces: map[string]*corev1.Namespace{
				"some-ns": &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: "some-ns",
						Annotations: map[string]string{
							"x-scrape-me": "true",
						},
					},
				},
				"other-ns": &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{Name: "other-ns"},
				},
			},
		}
		reg = &fakeFilterRegister{
			filters: map[schema.GroupKind][]fakeFilterInfo{},
		}
		builder = NewConditionFilterBuilder(lookup, reg)
	})

	It("should allow all scrape targets of a given type through if no conditions are set", func() {
		filter, err := builder.ConditionToFilters("some-cond", config.ScrapeCondition{
			From: config.ResourceEndpoints,
		})
		Expect(err).NotTo(HaveOccurred())

		for _, target := range allTargets {
			Expect(filter.Filter(target)).To(BeTrue())
		}
	})

	It("should filter on object names, if a name is set", func() {
		filter, err := builder.ConditionToFilters("some-cond", config.ScrapeCondition{
			From: config.ResourceEndpoints,
			When: &config.ScrapeConditionWhen{Name: plainTarget1.Name},
		})
		Expect(err).NotTo(HaveOccurred())

		By("admitting objects with matching names")
		Expect(filter.Filter(plainTarget1)).To(BeTrue())

		By("rejecting objects whose names don't match")
		Expect(filter.Filter(plainTarget2)).To(BeFalse())
	})

	It("should only admit objects with matching labels, if a label selector is set", func() {
		filter, err := builder.ConditionToFilters("some-cond", config.ScrapeCondition{
			From: config.ResourceEndpoints,
			When: &config.ScrapeConditionWhen{
				Labels: &metav1.LabelSelector{MatchLabels: labeledTarget1.Labels},
			},
		})
		Expect(err).NotTo(HaveOccurred())

		By("admitting objects matching the selector")
		Expect(filter.Filter(labeledTarget1)).To(BeTrue())

		By("rejecting objects not matching the selector")
		Expect(filter.Filter(labeledTarget2)).To(BeFalse())

	})

	It("should only admit objects whose annotation values match those set", func() {
		filter, err := builder.ConditionToFilters("some-cond", config.ScrapeCondition{
			From: config.ResourceEndpoints,
			When: &config.ScrapeConditionWhen{
				Annotations: map[string]string{
					"annotation-1": "aval1-1",
					"annotation-3": "avalx-3",
				},
			},
		})
		Expect(err).NotTo(HaveOccurred())

		By("admitting objects who have all annotations listed with the appropriate values")
		Expect(filter.Filter(annotatedTarget1)).To(BeTrue())

		By("rejecting objects who lack one or more annotations listed, or whose annotations don't have the appropriate values")
		Expect(filter.Filter(annotatedTarget2)).To(BeFalse())

	})

	It("should filter on multiple metadata facets at once", func() {
		filter, err := builder.ConditionToFilters("some-cond", config.ScrapeCondition{
			From: config.ResourceEndpoints,
			When: &config.ScrapeConditionWhen{
				Name: labeledTarget1.Name,
				Labels: &metav1.LabelSelector{MatchLabels: labeledTarget1.Labels},
			},
		})
		Expect(err).NotTo(HaveOccurred())

		By("admitting objects which match all metadata criteria")
		Expect(filter.Filter(labeledTarget1)).To(BeTrue())

		By("rejecting objects which fail at least one of the metadata criteria")
		Expect(filter.Filter(altLabeledTarget1)).To(BeFalse())
	})

	It("should be able to filter on a particular namespace, or namespace metadata", func() {
		filter, err := builder.ConditionToFilters("some-cond", config.ScrapeCondition{
			On: config.ResourceNamespace,
			When: &config.ScrapeConditionWhen{
				Annotations: map[string]string{"x-scrape-me": "true"},
			},
		}, config.ScrapeCondition{From: config.ResourceEndpoints})
		Expect(err).NotTo(HaveOccurred())

		By("admitting objects whose associated namespace has the appropriate metadata")
		Expect(filter.Filter(plainTarget1)).To(BeTrue())

		By("rejecting objects whose associated namespace lacks the appropriate metadata")
		Expect(filter.Filter(plainTarget2)).To(BeFalse())
	})

	// NB: registering is tested as part of the discovery source test mechanisms
});

