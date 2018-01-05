package discovery

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/directxman12/scrape-goat/pkg/config"
)

// FilterRegister registers filters on different discoverable
// types.  It can be used to inform the base discovery tools to
// filter on ingestion.  Any object must pass at least one filter
// (including global filters) to be considered for discovery.
type FilterRegister interface {
	RegisterFilter(groupKind schema.GroupKind, filter Filter, conditionIdentity string)
}

// NamespaceConditionLookup knows how to register different conditions on namespaces,
// and then look up if a named namespace matches some condition.  This is useful since
// you need to cross-reference namespace names with the actual namespace object to
// check if they meet the given conditions.
type NamespaceConditionLookup interface {
	NamespaceMatchesCondition(namespace string, conditionIdentity string) bool
	RegisterNamespaceCondition(filter Filter, conditionIdentity string)
}

func NewConditionFilterBuilder(lookup NamespaceConditionLookup, reg FilterRegister) *ConditionFilterBuilder {
	return &ConditionFilterBuilder{
		lookup: lookup,
		reg: reg,
	}
}

type ConditionFilterBuilder struct {
	lookup NamespaceConditionLookup
	reg    FilterRegister
}

func (b *ConditionFilterBuilder) buildConditionFilter(when *config.ScrapeConditionWhen) (Filter, error) {
	condFilter := &conditionFilter{
		name: when.Name,
		annotations: when.Annotations,
		selector: labels.Everything(),
	}

	// add a label filter that checks for matching labels
	if when.Labels != nil {
		sel, err := metav1.LabelSelectorAsSelector(when.Labels)
		if err != nil {
			return nil, err
		}
		condFilter.selector = sel
	}

	return condFilter, nil
}

type filterSet struct {
	associated map[config.ScrapeResource]Filter
	associatedIndirect map[config.ScrapeResource]Filter
	fromSources map[config.ScrapeResource]Filter

	identity string
}

func newFilterSet(identity string) *filterSet {
	return &filterSet{
		identity: identity,

		associated: map[config.ScrapeResource]Filter{},
		associatedIndirect: map[config.ScrapeResource]Filter{},
		fromSources: map[config.ScrapeResource]Filter{},
	}

}

func (s *filterSet) AddAssociated(res config.ScrapeResource, filter Filter) {
	s.associated[res] = filter
}

func (s *filterSet) AddFrom(res config.ScrapeResource, filter Filter) {
	s.fromSources[res] = filter
}

func (s *filterSet) AddFromAll(res config.ScrapeResource) {
	s.fromSources[res] = &AllowAllFilter{}
}

func (s *filterSet) Filter(obj runtime.Object) bool {
	// we must match *all* associated filters (e.g. namespace requirements)
	for _, filter := range s.associatedIndirect {
		if !filter.Filter(obj) {
			return false
		}
	}

	// we much match *at least one* normal object filter
	for _, filter := range s.fromSources {
		if filter.Filter(obj) {
			return true
		}
	}

	return false
}

func resourceToKubeGK(res config.ScrapeResource) (schema.GroupKind, error) {
	var gk schema.GroupKind
	switch res {
	case config.ResourcePod:
		gk = schema.GroupKind{Kind: "Pod"}
	case config.ResourceService:
		gk = schema.GroupKind{Kind: "Service"}
	case config.ResourceEndpoints:
		gk = schema.GroupKind{Kind: "Endpoints"}
	default:
		return schema.GroupKind{}, fmt.Errorf("unknown scrape resource %q", res)
	}

	return gk, nil
}

func (s *filterSet) Register(lookup NamespaceConditionLookup, reg FilterRegister) error {
	nsFilter, hasNSFilter := s.associated[config.ResourceNamespace]
	if !hasNSFilter {
		nsFilter = &AllowAllFilter{}
	}

	lookup.RegisterNamespaceCondition(nsFilter, s.identity)
	matchFunc := func(namespace string) bool {
		return lookup.NamespaceMatchesCondition(namespace, s.identity)
	}

	s.associatedIndirect[config.ResourceNamespace] = &indirectNamespaceFilter{matchFunc}

	// TODO: this doesn't append namespace filters to our filter list, so we have to filter those out later

	for res, filter := range s.fromSources  {
		gk, err := resourceToKubeGK(res)
		if err != nil {
			return err
		}
		reg.RegisterFilter(gk, filter, s.identity)
	}

	return nil
}

// ConditionToFilters builds a filter for the given conditions, registering the filters with the builder's
// namespace condition registry and filter registry once the filter is built.  The full filter is returned
// for convinience's sake.
func (b *ConditionFilterBuilder) ConditionToFilters(ident string, conditions ...config.ScrapeCondition) (Filter, error) {
	filters := newFilterSet(ident)
	for i := range conditions {
		// grab a reference to the actual slice item, not the iteration variable
		condition := &conditions[i]

		// can only set one of "on" or "from"
		if (condition.On != "" && condition.From != "") || (condition.On == "" && condition.From == "") {
			return nil, fmt.Errorf("must specify one of 'from' or 'on' in conditions")
		}

		// 'on' conditions work on associated objects like namespaces
		// theoretically, we could work with other associated objects too
		if condition.On != "" {
			switch condition.On {
			case config.ResourceNamespace:
				filter, err := b.buildConditionFilter(condition.When)
				if err != nil {
					return nil, err
				}
				filters.AddAssociated(config.ResourceNamespace, filter)
			default:
				return nil, fmt.Errorf("unknown associated resource %q", condition.On)
			}
			continue
		}

		// from filters indicate that we should pull metrics from this type of object

		// if there are no "when" rules, always pull from this type of object
		if condition.When == nil {
			filters.AddFromAll(condition.From)
			continue
		}
		condFilter, err := b.buildConditionFilter(condition.When)
		if err != nil {
			return nil, err
		}

		filters.AddFrom(condition.From, condFilter)
	}

	if err := filters.Register(b.lookup, b.reg); err != nil {
		return nil, err
	}

	// TODO: validate that we have at least one "from" source?

	return filters, nil
}
