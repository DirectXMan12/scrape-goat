package discovery

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/labels"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Filter knows how to check to see if a runtime.Object
// matches some condition
type Filter interface {
	// Filter checks to see if the given object matches this filter.
	Filter(runtime.Object) bool
}

// indirectNamespaceFilter matches any object whose namespace matches
// according to the given function.
type indirectNamespaceFilter struct {
	namespaceMatches func(namespace string) bool
}

func (f *indirectNamespaceFilter) Filter(obj runtime.Object) bool {
	meta, hasMeta := obj.(metav1.ObjectMetaAccessor)
	if !hasMeta {
		utilruntime.HandleError(fmt.Errorf("object type %T has no metadata"))
		return false
	}

	ns := meta.GetObjectMeta().GetNamespace()
	return f.namespaceMatches(ns)
}

// conditionFilter filter which matches name, labels, and annotations.
type conditionFilter struct {
	selector labels.Selector
	annotations map[string]string
	name string
}

func (f *conditionFilter) Filter(obj runtime.Object) bool {
	metaAccessor, hasMeta := obj.(metav1.ObjectMetaAccessor)
	if !hasMeta {
		utilruntime.HandleError(fmt.Errorf("object type %T has no metadata"))
		return false
	}

	meta := metaAccessor.GetObjectMeta()

	if f.name != "" && meta.GetName() != f.name {
		return false
	}

	if f.selector != nil && !f.selector.Matches(labels.Set(meta.GetLabels())) {
		return false
	}

	if f.annotations != nil {
		actualAnnotations := meta.GetAnnotations()
		for k, v := range f.annotations {
			if actualV, ok := actualAnnotations[k]; !ok || actualV != v {
				return false
			}
		}
	}

	return true
}

type AllowAllFilter struct{}
func (_ *AllowAllFilter) Filter(_ runtime.Object) bool {
	return true
}
