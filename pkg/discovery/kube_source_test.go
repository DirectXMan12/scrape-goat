package discovery

import (
	"time"
	"sort"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/directxman12/scrape-goat/pkg/config"
)

// really?  we don't have one of these?
type fakeSharedInformer struct {
	handlers []cache.ResourceEventHandler
}

func (i *fakeSharedInformer) AddEventHandler(handler cache.ResourceEventHandler) {
	i.handlers = append(i.handlers, handler)
}

func (i *fakeSharedInformer) AddEventHandlerWithResyncPeriod(handler cache.ResourceEventHandler, resyncPeriod time.Duration) {}
func (i *fakeSharedInformer) GetStore() cache.Store { return nil }
func (i *fakeSharedInformer) GetController() cache.Controller { return nil }
func (i *fakeSharedInformer) Run(stopCh <-chan struct{}) {}
func (i *fakeSharedInformer) HasSynced() bool { return true }
func (i *fakeSharedInformer) LastSyncResourceVersion() string { return "" }

func (i *fakeSharedInformer) FireAdd(obj interface{}) {
	for _, handler := range i.handlers {
		handler.OnAdd(obj)
	}
}

func (i *fakeSharedInformer) FireUpdate(oldObj, newObj interface{}) {
	for _, handler := range i.handlers {
		handler.OnUpdate(oldObj, newObj)
	}
}

func (i *fakeSharedInformer) FireDelete(obj interface{}) {
	for _, handler := range i.handlers {
		handler.OnDelete(obj)
	}
}

type fakeTargetHandler struct {
	// targets maps ScrapeTarget keys to the associated discovery groups
	targets map[string][]string
}

func (h *fakeTargetHandler) OnAdd(target ScrapeTarget, discoveryGroups ...string) {
	sort.Strings(discoveryGroups)
	h.targets[target.Key()] = discoveryGroups
}

func (h *fakeTargetHandler) OnDelete(target ScrapeTarget) {
	delete(h.targets, target.Key())
}

var _ = Describe("Kubernetes Discovery Source", func() {
	var (
		source        *KubeDiscovery
		targetHandler *fakeTargetHandler

		nsInformer        *fakeSharedInformer
		endpointsInformer *fakeSharedInformer
		podInformer       *fakeSharedInformer
		serviceInformer   *fakeSharedInformer
	)

	// TODO: move some of these into a common test utils location
	conditionSets := map[string][]config.ScrapeCondition{
		"infra": []config.ScrapeCondition{
			{
				On: config.ResourceNamespace,
				When: &config.ScrapeConditionWhen{Name: "openshift-infra"},
			},
			{From: config.ResourcePod},
			{From: config.ResourceService},
			{From: config.ResourceEndpoints},
		},
		"trusted": []config.ScrapeCondition{
			{
				On: config.ResourceNamespace,
				When: &config.ScrapeConditionWhen{
					Annotations: map[string]string{
						"openshift.io/trust-metrics": "true",
					},
				},
			},
			{
				From: config.ResourcePod,
				When: &config.ScrapeConditionWhen{
					Annotations: map[string]string{
						"openshift.io/metrics-provider": "true",
					},
				},
			},
		},
		"general": []config.ScrapeCondition{
			{
				From: config.ResourcePod,
				When: &config.ScrapeConditionWhen{
					Annotations: map[string]string{
						"openshift.io/has-metrics": "true",
					},
				},
			},
		},
		"other-ns": []config.ScrapeCondition{
			{
				On: config.ResourceNamespace,
				When: &config.ScrapeConditionWhen{
					Annotations: map[string]string{"openshift.io/other-metrics": "true"},
				},
			},
			{From: config.ResourcePod},
		},
		"other-pod": []config.ScrapeCondition{
			{
				From: config.ResourcePod,
				When: &config.ScrapeConditionWhen{
					Annotations: map[string]string{"openshift.io/other-pod": "true"},
				},
			},
		},
	}

	JustBeforeEach(func() {
		targetHandler = &fakeTargetHandler{
			targets: map[string][]string{},
		}
		source = NewKubeDiscovery(targetHandler)

		nsInformer = &fakeSharedInformer{}
		endpointsInformer = &fakeSharedInformer{}
		podInformer = &fakeSharedInformer{}
		serviceInformer = &fakeSharedInformer{}

		builder := NewConditionFilterBuilder(source, source)
		for ident, conds := range conditionSets {
			_, err := builder.ConditionToFilters(ident, conds...)
			Expect(err).NotTo(HaveOccurred())
		}

		source.Run(nsInformer, podInformer, endpointsInformer, serviceInformer)

		// populate some basic namespaces
		nsInformer.FireAdd(&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: "openshift-infra"},
		})
		nsInformer.FireAdd(&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "trusted-components",
				Annotations: map[string]string{"openshift.io/trust-metrics": "true"},
			},
		})
		nsInformer.FireAdd(&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: "some-project"},
		})
	})

	// TODO: test on missing NS?

	It("should add and delete scrape targets that match a set of conditions", func() {
		By("creating a new target when the underlying object is seen")
		podInformer.FireAdd(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "some-pod1",
				Namespace: "openshift-infra",
				Annotations: map[string]string{"openshift.io/has-metrics": "true"},
			},
			Status: corev1.PodStatus{PodIP: "1.2.3.4"},
		})

		// TODO: make this not rely on knowlege of the key format
		Expect(targetHandler.targets).To(HaveKeyWithValue("Pod/openshift-infra/some-pod1", []string{"general", "infra"}))

		By("deleting the target when the underlying object is deleted")
		podInformer.FireDelete(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "some-pod1",
				Namespace: "openshift-infra",
				Annotations: map[string]string{"openshift.io/has-metrics": "true"},
			},
			Status: corev1.PodStatus{PodIP: "1.2.3.4"},
		})
	})

	It("should add and delete scrape targets as they are updated to match conditions", func() {
		By("not creating a new scrape target when an object doesn't match any conditions")
		podInformer.FireAdd(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "some-pod2",
				Namespace: "some-project",
			},
			Status: corev1.PodStatus{PodIP: "1.2.3.4"},
		})
		Expect(targetHandler.targets).To(BeEmpty())

		By("creating a new scrape target when an object is updated to match a condition")
		podInformer.FireUpdate(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "some-pod2",
				Namespace: "some-project",
			},
			Status: corev1.PodStatus{PodIP: "1.2.3.4"},
		}, &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "some-pod2",
				Namespace: "some-project",
				Annotations: map[string]string{"openshift.io/has-metrics": "true"},
			},
			Status: corev1.PodStatus{PodIP: "1.2.3.4"},
		})
		Expect(targetHandler.targets).To(HaveKeyWithValue("Pod/some-project/some-pod2", []string{"general"}))

		By("not doing anything if an unrelated update is recieved")
		// TODO: how do we implement this without recording actions?

		By("deleting the old scrape target and creating a new one if the object switches condition sets")
		podInformer.FireUpdate(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "some-pod2",
				Namespace: "some-project",
				Annotations: map[string]string{"openshift.io/has-metrics": "true"},
			},
			Status: corev1.PodStatus{PodIP: "1.2.3.4"},
		}, &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "some-pod2",
				Namespace: "some-project",
				Annotations: map[string]string{"openshift.io/other-pod": "true"},
			},
			Status: corev1.PodStatus{PodIP: "1.2.3.4"},
		})
		Expect(targetHandler.targets).To(HaveKeyWithValue("Pod/some-project/some-pod2", []string{"other-pod"}))

		By("deleting the scrape target when the object no longer matches any conditions")
		podInformer.FireUpdate(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "some-pod2",
				Namespace: "some-project",
				Annotations: map[string]string{"openshift.io/other-pod": "true"},
			},
			Status: corev1.PodStatus{PodIP: "1.2.3.4"},
		}, &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "some-pod2",
				Namespace: "some-project",
			},
			Status: corev1.PodStatus{PodIP: "1.2.3.4"},
		})
		Expect(targetHandler.targets).To(BeEmpty())
	})

	It("should add and delete scrape targets as their namespaces are updated to match conditions", func() {
		By("not creating a new scrape target when an object doesn't match any conditions")
		podInformer.FireAdd(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "some-pod3",
				Namespace: "other-trusted",
				Annotations: map[string]string{
					"openshift.io/metrics-provider": "true",
				},
			},
			Status: corev1.PodStatus{PodIP: "1.2.3.4"},
		})
		nsInformer.FireAdd(&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: "other-trusted"},
		})
		Expect(targetHandler.targets).To(BeEmpty())

		By("creating a new scrape target when the namespace is updated to match a condition")
		nsInformer.FireUpdate(&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: "other-trusted"},
		}, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "other-trusted",
				Annotations: map[string]string{"openshift.io/trust-metrics": "true"},
			},
		})
		Expect(targetHandler.targets).To(HaveKeyWithValue("Pod/other-trusted/some-pod3", []string{"trusted"}))

		By("not doing anything if an unrelated update is recieved")
		// TODO: how do we handle this without recording actions

		By("deleting the old scrape target and creating a new one if the namespace switches condition sets")
		nsInformer.FireUpdate(&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "other-trusted",
				Annotations: map[string]string{"openshift.io/trust-metrics": "true"},
			},
		}, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "other-trusted",
				Annotations: map[string]string{"openshift.io/other-metrics": "true"},
			},
		})
		Expect(targetHandler.targets).To(HaveKeyWithValue("Pod/other-trusted/some-pod3", []string{"other-ns"}))

		By("deleting the scrape target when the namespace no longer matches any conditions")
		nsInformer.FireUpdate(&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "other-trusted",
				Annotations: map[string]string{"openshift.io/trust-metrics": "true"},
			},
		}, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "other-trusted",
			},
		})
		Expect(targetHandler.targets).To(BeEmpty())
	})

})
