package discovery

// TODO: support configuring params

type ScrapeTarget interface {
	// Hostnames returns the list of hostnames and ports that the
	// scraper should scrape metrics from.
	Hostnames() []string

	// SourceValue extracts a value from the source object
	// that this scrape target was scraped from, for relabeling.
	// It will return an empty string if the given path is not
	// supported or not present.
	SourceValue(path ...string) string

	// Key is a unique identifier for this scrape target.
	Key() string
}

// TargetHandler knows how to respond to changes in the available
// set of targets.
type TargetHandler interface {
	// OnAdd is called when a new scrape target is added.  The listed
	// "discovery groups" can be used to map the scrape targets back to
	// the particular scrape configurations that they matched.
	OnAdd(target ScrapeTarget, discoveryGroups ...string)

	// OnDelete is called when an existing scrape target should be removed.
	OnDelete(target ScrapeTarget)
}
