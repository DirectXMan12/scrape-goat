package discovery

import (
	"fmt"

	"github.com/golang/glog"

	"k8s.io/client-go/tools/cache"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime"
	kapi "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	indexCondition = "condition"
	indexNamespace = "namespace"
)

// kubeTarget is a ScrapeTarget with a bit of extra info
// to identify the original Kubenetes object that it came from.
type kubeTarget struct {
	groupKind schema.GroupKind
	*metav1.ObjectMeta

	// these must be set in a deterministic order (filter specification order)
	matchedConditions []string
	passedNSFilters map[string]struct{}

	hostnames []string
}

func (t *kubeTarget) String() string {
	return fmt.Sprintf("(%s: %v / %v --> %v)", t.Key(), t.matchedConditions, t.passedNSFilters, t.hostnames)
}

func (t *kubeTarget) Key() string {
	key, _ := kubeTargetKeyFunc(t)
	return key
}

func (t *kubeTarget) Hostnames() []string {
	return t.hostnames
}

func (t *kubeTarget) SourceValue(path ...string) string {
	if len(path) < 2 || path[0] != "metadata" {
		return ""
	}

	// TODO: use reflection?
	switch path[1] {
	case "name":
		return t.Name
	case "namespace":
		return t.Namespace
	case "annotations":
		return t.Annotations[path[2]]
	case "labels":
		return t.Labels[path[2]]
	default:
		return ""
	}
}

// TODO: allow configuring these based on annotations/what-have-you

func (t *kubeTarget) Path() string {
	return "/metrics"
}

func (t *kubeTarget) Scheme() string {
	return "https"
}

func (t *kubeTarget) Equals(other *kubeTarget) bool {
	if t.Namespace != other.Namespace || t.Name != other.Name || t.groupKind != other.groupKind {
		return false
	}

	if len(t.hostnames) != len(other.hostnames) {
		return false
	}

	for i, name := range t.hostnames {
		if name != other.hostnames[i] {
			return false
		}
	}

	return true
}

func (t *kubeTarget) equalsWithConditions(other *kubeTarget) bool {
	if !t.Equals(other) {
		return false
	}

	if len(t.matchedConditions) != len(other.matchedConditions) {
		return false
	}

	if len(t.passedNSFilters) != len(other.passedNSFilters) {
		return false
	}


	for i, cond := range t.matchedConditions {
		if other.matchedConditions[i] != cond {
			return false
		}
		_, otherExists := other.passedNSFilters[cond]
		if _, currentExists := t.passedNSFilters[cond]; currentExists != otherExists {
			return false
		}
	}

	return true
}

func (t *kubeTarget) fullyMatchedConditions() []string {
	conds := []string{}
	for _, ident := range t.matchedConditions {
		if _, passed := t.passedNSFilters[ident]; passed {
			conds = append(conds, ident)
		}
	}

	return conds
}

func (t kubeTarget) passesNSFilters() bool {
	return len(t.passedNSFilters) > 0
}

// nsInfo contains information about which namespaces matched particular conditions.
type nsInfo struct {
	name string
	matchedConditions []string
}

func (i *nsInfo) String() string {
	return fmt.Sprintf("(%s: %v)", i.name, i.matchedConditions)
}

func nsInfoKeyFunc(raw interface{}) (string, error) {
	info, ok := raw.(nsInfo)
	if !ok {
		return "", fmt.Errorf("unknown object %v passed into or out of the cache", raw)
	}
	return info.name, nil
}

func nsConditionIndexerFunc(raw interface{}) ([]string, error) {
	ns, isNS := raw.(nsInfo)
	if !isNS {
		return nil, fmt.Errorf("unknown object %v passed to namespace indexer function", raw)
	}

	return ns.matchedConditions, nil
}

// filterInfo is a Filter with extra information about the
// source condition identity.
type filterInfo struct {
	Filter
	identity string
}

func kubeTargetKeyFunc(obj interface{}) (string, error) {
	target, ok := obj.(*kubeTarget)
	if !ok {
		return "", fmt.Errorf("unknown object %v passed into or out of the cache", obj)
	}

	if len(target.Namespace) == 0 {
		return target.groupKind.String() + "/" + target.Name, nil
	}

	return target.groupKind.String() + "/" + target.Namespace + "/" + target.Name, nil
}

type KubeDiscovery struct {
	// nsLookup indexes namespaces by matched conditions
	nsLookup cache.Indexer

	// cache indexes all the discovered kubeTargets which have passed the filters
	// by the conditions that they match.
	cache cache.Indexer
	filters map[schema.GroupKind][]filterInfo
	nsFilters map[string]filterInfo

	handler TargetHandler
}

func (d *KubeDiscovery) RegisterFilter(gk schema.GroupKind, filter Filter, conditionIdentity string) {
	d.filters[gk] = append(d.filters[gk], filterInfo{
		Filter: filter,
		identity: conditionIdentity,
	})
}

func (d *KubeDiscovery) RegisterNamespaceCondition(filter Filter, conditionIdentity string) {
	d.nsFilters[conditionIdentity] = filterInfo{
		Filter: filter,
		identity: conditionIdentity,
	}
}

func (d *KubeDiscovery) namespaceConditionsMatches(namespace string, conditionIdentities ...string) map[string]struct{} {
	ns, exists, err := d.nsLookup.GetByKey(namespace)
	if err != nil {
		glog.Errorf("error looking up namespace %q: %v", namespace, err)
		return nil
	}
	if !exists {
		glog.Errorf("error looking up namespace %q: no such namespace found in cache", namespace)
		return nil
	}

	matches := map[string]struct{}{}
	for _, ident := range conditionIdentities {
		for _, nsIdent := range ns.(nsInfo).matchedConditions {
			if ident == nsIdent {
				matches[ident] = struct{}{}
			}
		}
	}

	return matches
}

func (d *KubeDiscovery) NamespaceMatchesCondition(namespace string, conditionIdentity string) bool {
	return len(d.namespaceConditionsMatches(namespace, conditionIdentity)) > 0
}

func NewKubeDiscovery(handler TargetHandler) *KubeDiscovery {
	disc := &KubeDiscovery{
		filters: map[schema.GroupKind][]filterInfo{},
		nsFilters: map[string]filterInfo{},
		handler: handler,
	}

	disc.cache = cache.NewIndexer(kubeTargetKeyFunc, cache.Indexers{
		indexCondition: disc.conditionIndexerFunc,
		indexNamespace: func(raw interface{}) ([]string, error) {
			target, isTarget := raw.(*kubeTarget)
			if !isTarget {
				return nil, fmt.Errorf("unknown object %v passed to indexer function", raw)
			}
			return []string{target.Namespace}, nil
		},
	})

	disc.nsLookup = cache.NewIndexer(nsInfoKeyFunc, cache.Indexers{
		indexCondition: nsConditionIndexerFunc,
	})

	return disc
}

// conditionIndexerFunc uses the cached conditions on the target object to index
// the object by the conditions that it met.
func (d *KubeDiscovery) conditionIndexerFunc(raw interface{}) ([]string, error) {
	target, isTarget := raw.(*kubeTarget)
	if !isTarget {
		return nil, fmt.Errorf("unknown object %v passed to indexer function", raw)
	}

	return target.matchedConditions, nil
}

// Run begins collecting targets from Kubernetes.  It should only be run after all filters are registered.
func (d *KubeDiscovery) Run(nsInformer cache.SharedInformer, podInformer cache.SharedInformer, endpointsInformer cache.SharedInformer, serviceInformer cache.SharedInformer) {
	podInformer.AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: d.makeFilter(schema.GroupKind{Kind: "Pod"}, true),
		Handler: cache.ResourceEventHandlerFuncs{
			AddFunc: d.cacheAdder(schema.GroupKind{Kind: "Pod"}),
			UpdateFunc: d.cacheUpdater(schema.GroupKind{Kind: "Pod"}),
			DeleteFunc: d.cacheDeleter(schema.GroupKind{Kind: "Pod"}),
		},
	})
	serviceInformer.AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: d.makeFilter(schema.GroupKind{Kind: "Service"}, true),
		Handler: cache.ResourceEventHandlerFuncs{
			AddFunc: d.cacheAdder(schema.GroupKind{Kind: "Service"}),
			UpdateFunc: d.cacheUpdater(schema.GroupKind{Kind: "Service"}),
			DeleteFunc: d.cacheDeleter(schema.GroupKind{Kind: "Service"}),
		},
	})
	endpointsInformer.AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: d.makeFilter(schema.GroupKind{Kind: "Endpoints"}, true),
		Handler: cache.ResourceEventHandlerFuncs{
			AddFunc: d.cacheAdder(schema.GroupKind{Kind: "Endpoints"}),
			UpdateFunc: d.cacheUpdater(schema.GroupKind{Kind: "Endpoints"}),
			DeleteFunc: d.cacheDeleter(schema.GroupKind{Kind: "Endpoints"}),
		},
	})

	nsInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(raw interface{}) {
			obj := raw.(*kapi.Namespace)
			matchedConditions := []string{}
			for ident, filter := range d.nsFilters {
				if filter.Filter.Filter(obj) {
					matchedConditions = append(matchedConditions, ident)
				}
			}
			err := d.nsLookup.Add(nsInfo{
				name: obj.Name,
				matchedConditions: matchedConditions,
			})
			if err != nil {
				glog.Errorf("[discovery] unable to add namespace (%s: %v) to lookup cache: %v", obj.Name, matchedConditions, err)
			}
			glog.V(6).Infof("[discovery] added namespace: (%s: %v)", obj.Name, matchedConditions)

			if err := d.refreshForNamespace(obj.Name); err != nil {
				glog.Errorf("error refreshing target list on namespace change: %v", err)
			}
		},
		UpdateFunc: func(_, newRaw interface{}) {
			obj := newRaw.(*kapi.Namespace)
			matchedConditions := []string{}
			for ident, filter := range d.nsFilters {
				if filter.Filter.Filter(obj) {
					matchedConditions = append(matchedConditions, ident)
				}
			}
			err := d.nsLookup.Update(nsInfo{
				name: obj.Name,
				matchedConditions: matchedConditions,
			})
			if err != nil {
				glog.Errorf("[discovery] unable to add namespace (%s: %v) in lookup cache: %v", obj.Name, matchedConditions, err)
				return
			}
			glog.V(6).Infof("[discovery] updated namespace: (%s: %v)", obj.Name, matchedConditions)

			if err := d.refreshForNamespace(obj.Name); err != nil {
				glog.Errorf("error refreshing target list on namespace change: %v", err)
			}
		},
		DeleteFunc: func(raw interface{}) {
			obj := raw.(*kapi.Namespace)
			err := d.nsLookup.Delete(nsInfo{
				name: obj.Name,
			})
			if err != nil {
				glog.Errorf("[discovery] unable to delete namespace %q in lookup cache: %v", obj.Name, err)
				return
			}

			glog.V(6).Infof("[discovery] deleted namespace %q", obj.Name)

			if err := d.refreshForNamespace(obj.Name); err != nil {
				glog.Errorf("error refreshing target list on namespace change: %v", err)
			}
		},
	})
}

func (d *KubeDiscovery) makeFilter(gk schema.GroupKind, checkNamespace bool) func(interface{}) bool {
	var acceptibleFunc func(runtime.Object) bool
	switch gk {
	case schema.GroupKind{Kind: "Pod"}:
		acceptibleFunc = func(raw runtime.Object) bool {
			obj := raw.(*kapi.Pod)
			return obj.Status.PodIP != ""
		}
	case schema.GroupKind{Kind: "Service"}:
		acceptibleFunc = func(raw runtime.Object) bool {
			obj := raw.(*kapi.Service)
			return obj.Spec.ClusterIP != ""
		}
	case schema.GroupKind{Kind: "Endpoints"}:
		acceptibleFunc = func(raw runtime.Object) bool {
			obj := raw.(*kapi.Endpoints)
			for _, subset := range obj.Subsets {
				for _, addr := range subset.Addresses {
					// TODO: support non-IP endpoints?
					if addr.IP != "" {
						return true
					}
				}
			}

			return false
		}
	}
	return func(raw interface{}) bool {
		var obj runtime.Object
		var ok bool
		if obj, ok = raw.(runtime.Object); !ok {
			glog.Errorf("indexer passed a non-Kubernetes object %#v", raw)
			return false
		}
		if !acceptibleFunc(obj) {
			glog.V(6).Infof("[discovery] the object %v was judged not yet ready to be a metrics target", obj)
			return false
		}

		for _, filter := range d.filters[gk] {
			if filter.Filter.Filter(obj) {
				return true
			}
		}
		glog.V(6).Infof("[discovery] the object %v did not match any filters", obj)
		return false
	}
}

func (d *KubeDiscovery) cacheAdder(gk schema.GroupKind) func(interface{}) {
	return func(obj interface{}) {
		target, err := d.objToTargetSource(gk, obj)
		if err != nil {
			glog.Errorf("error adding item to cache: %v", err)
			return
		}

		// check namespace filters for all of our matched conditions too
		passesNSFilters := d.namespaceConditionsMatches(target.Namespace, target.matchedConditions...)
		target.passedNSFilters = passesNSFilters

		if err = d.cache.Add(target); err != nil {
			glog.Errorf("error adding item to cache: %v", err)
			return
		}

		// always keep objects in our cache in case of namespace changes, but don't
		// run the hooks unless the namespace passes the check
		if !target.passesNSFilters() {
			glog.V(6).Infof("[discovery] added target %v to cache (without triggering add handler)", target)
			return
		}
		glog.V(6).Infof("[discovery] added target %v", target)

		d.handler.OnAdd(target, target.fullyMatchedConditions()...)
	}
}

func (d *KubeDiscovery) cacheUpdater(gk schema.GroupKind) func(interface{}, interface{}) {
	return func(oldObj, newObj interface{}) {
		target, err := d.objToTargetSource(gk, newObj)
		if err != nil {
			glog.Errorf("error updating item in cache: %v", err)
			return
		}

		if err := d.dispatchUpdate(target); err != nil {
			glog.Errorf("error updating item in cache: %v", err)
		}
	}
}

func (d *KubeDiscovery) refreshForNamespace(namespace string) error {
	targets, err := d.cache.ByIndex(indexNamespace, namespace)
	if err != nil {
		return err
	}

	for _, target := range targets {
		// avoid mutating the cache
		targetCpy := *(target.(*kubeTarget))
		if err := d.dispatchUpdate(&targetCpy); err != nil {
			// TODO: continue anyway?  return error at end?
			return err
		}
	}

	return nil
}

// dispatchUpdate figures out what event needs to be sent to the handler, if any, based
// on some updates to the underlying resources.  It makes sure to check and update if
// the namespace filters on the given object pass.
func (d *KubeDiscovery) dispatchUpdate(target *kubeTarget) error {
	// fetch the old target
	oldTargetRaw, exists, err := d.cache.Get(target)
	if err != nil {
		return fmt.Errorf("error fetching item in cache to process update: %v", err)
	}
	var oldTarget *kubeTarget
	if exists {
		oldTarget = oldTargetRaw.(*kubeTarget)
	}

	// check and update namespace filters for all of our matched conditions
	passesNSFilters := d.namespaceConditionsMatches(target.Namespace, target.matchedConditions...)
	target.passedNSFilters = passesNSFilters

	if err := d.cache.Update(target); err != nil {
		return fmt.Errorf("error updating item in cache: %v", err)
	}

	switch {
	case !exists:
		glog.V(6).Infof("[discovery] received update for object which didn't previously exist in cache")
		fallthrough
	case target.passesNSFilters() && !oldTarget.passesNSFilters():
		glog.V(6).Infof("[discovery] updated target %v (triggering add)", target)
		d.handler.OnAdd(target, target.fullyMatchedConditions()...)

	case !target.passesNSFilters() && oldTarget.passesNSFilters():
		glog.V(6).Infof("[discovery] updated target %v (triggering delete of target %v)", target, oldTarget)
		d.handler.OnDelete(oldTarget)

	case !target.equalsWithConditions(oldTarget):
		glog.V(6).Infof("[discovery] updated target %v (triggering delete of target %v, then add)", target, oldTarget)
		d.handler.OnDelete(oldTarget)
		d.handler.OnAdd(target, target.fullyMatchedConditions()...)

	default:
		glog.V(6).Infof("[discovery] updated target %v in cache (without triggering handlers)", target)
	}

	return nil
}


func (d *KubeDiscovery) cacheDeleter(gk schema.GroupKind) func(interface{}) {
	return func(obj interface{}) {
		target, err := d.objToTargetSource(gk, obj)
		if err != nil {
			glog.Errorf("error deleting item in cache: %v", err)
			return
		}
		if err = d.cache.Delete(target); err != nil {
			glog.Errorf("error deleting item in cache: %v", err)
			return
		}
		oldTargetRaw, exists, err := d.cache.Get(target)
		if err != nil {
			glog.Errorf("error fetching item in cache to process delete: %v", err)
			return
		}

		// don't process the on-delete method if we didn't pass the filters historically
		if exists && !oldTargetRaw.(*kubeTarget).passesNSFilters() {
			glog.V(6).Infof("[discovery] deleting target %v from cache (without triggering delete handler)", target)
			return
		}

		glog.V(6).Infof("[discovery] deleting target %v", target)
		d.handler.OnDelete(target)
	}
}

func (d *KubeDiscovery) objToTargetSource(groupKind schema.GroupKind, raw interface{}) (*kubeTarget, error) {
	target := &kubeTarget{
		groupKind: groupKind,
	}
	switch groupKind {
	case schema.GroupKind{Kind: "Pod"}:
		obj := raw.(*kapi.Pod)
		target.ObjectMeta = &obj.ObjectMeta

		target.hostnames = []string{obj.Status.PodIP}
	case schema.GroupKind{Kind: "Service"}:
		obj := raw.(*kapi.Service)
		target.ObjectMeta = &obj.ObjectMeta

		// TODO: filter out services whose type isn't ClusterIP?

		target.hostnames = []string{obj.Spec.ClusterIP}
	case schema.GroupKind{Kind: "Endpoints"}:
		obj := raw.(*kapi.Endpoints)
		target.ObjectMeta = &obj.ObjectMeta

		target.hostnames = nil
		for _, subset := range obj.Subsets {
			for _, addr := range subset.Addresses {
				if addr.IP != "" {
					target.hostnames = append(target.hostnames, addr.IP)
				}
			}
		}
	default:
		return nil, fmt.Errorf("cannot convert unknown group-kind %s to scrape target", groupKind.String())
	}

	// check which conditions it meets
	for _, filter := range d.filters[groupKind] {
		if filter.Filter.Filter(raw.(runtime.Object)) {
			target.matchedConditions = append(target.matchedConditions, filter.identity)
		}
	}

	return target, nil
}
