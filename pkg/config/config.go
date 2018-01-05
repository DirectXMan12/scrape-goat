package config

import (
	"github.com/prometheus/common/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TODO: support auth options

// TimingConfig represents configuration of the timing of
// metrics ingestion.
type TimingConfig struct {
	// ScrapeInterval controls the interval at which metrics are scraped.
	ScrapeInterval model.Duration `yaml:"scrapeInterval,omitempty"`
	// ScrapeTimeout controls the timeout when scraping metrics.
	ScrapeTimeout model.Duration `yaml:"scrapeTimeout,omitempty"`
}

// LimitsConfig represents configurations of the limits imposed on
// scraping with regards to number of samples, number of metrics, and
// sample frequency.
type LimitsConfig struct {
	// MaxSeriesPerMetric controls the maximum number of series per metric family.
	MaxSeriesPerFamily *int `yaml:"maxSeriesPerFamily,omitempty"`
	// MaxSeries controls the overall maximum number of allowed series.
	MaxSeries *int `yaml:"maxSeries,omitempty"`
	// MaxFamilies controls the overall maximum number of allowed metric families.
	MaxFamilies *int `yaml:"maxFamilies,omitempty"`
}

// TODO: embed this in the SourceLabel struct to unify?

// VariableSource represents a source of information for the
// connection configuration.
type VariableSource struct {
	// From pulls from some metadata path.
	From string `yaml:"from,omitempty"`
	// Const uses a constant value.
	Const string `yaml:"const,omitempty"`
}

// ConnectionConfig represents configuration about how
// the discovery should find configuration about metrics from
// a particular discovery target.
type ConnectionConfig struct {
	// Path is the path at which the HTTP request should be made.
	Path *VariableSource `yaml:"path,omitempty"`
	// Port is the port on which the HTTP request should be made.
	Port *VariableSource `yaml:"port,omitempty"`
	// Scheme is the scheme with which the HTTP request should
	// be made (HTTP vs HTTPS).
	Scheme *VariableSource `yaml:"scheme,omitempty"`

	Username *VariableSource `yaml:"username,omitempty"`
	Password *VariableSource `yaml:"password,omitempty"`

	// TODO: support passing a token, query string as well?
}

// ScrapeConfig represents configuration about restrictions
// and transformations on scraping, independent of whether
// it's used for a particular target or as the default.
//
// This includes parts of the functionality of Prometheus's
// ScrapeConfig, but explicitly skips certain parts that are
// controlled by the scrape proxy.
type ScrapeConfig struct {
	// Timing controls the timing of the scraping.
	Timing *TimingConfig `yaml:"timing,omitempty"`
	// Limits controls the sample and metric limits of the scraping.
	Limits *LimitsConfig `yaml:"limits,omitempty"`
	// Connection specifies connection configuration sources
	Connection *ConnectionConfig `yaml:"connection,omitempty"`
}

// ScrapeResource represents a resource known to the scraper
type ScrapeResource string

var (
	ResourceNamespace ScrapeResource = "namespace"
	ResourcePod ScrapeResource = "pod"
	ResourceEndpoints ScrapeResource = "endpoints"
	ResourceService ScrapeResource = "service"
	ResourceAny ScrapeResource = ""
)

// ScrapeConditionWhen specifies a condition such as matching a particular
// set of labels or having a particular set of annotations.
type ScrapeConditionWhen struct {
	// Annotations matches objects with a particular set of annotations
	Annotations map[string]string `yaml:"annotations,omitempty"`
	// HasLabels matches objects with a particular set of labels
	Labels *metav1.LabelSelector `yaml:"labels,omitempty"`
	// Name matches an object with a particular name
	Name string `yaml:"name,omitempty"`
}

// ScrapeCondition specifies a condition on a particular kind of objects
// for scraping.
type ScrapeCondition struct {
	// From controls the kind of object this condition targets, in case
	// of objects that directly expose metrics.  From conditions are ORed
	// together, and there can be at most one per type.
	From ScrapeResource `yaml:"from,omitempty"`
	// On controls the kind of object this condition targets, in case
	// of "associated" objects that don't directly contain metrics,
	// like namespaces.  On conditions are ANDED together, and there
	// can be at most one per resource.
	On ScrapeResource `yaml:"on,omitempty"`
	// When controls which objects this conditions targets
	When *ScrapeConditionWhen `yaml:"when,omitempty"`
}

// SourceLabel represents the source of label contents.
type SourceLabel struct {
	// From pulls from some metadata path.
	From string `yaml:"from,omitempty"`
	// Const uses a constant value.
	Const string `yaml:"const,omitempty"`
	// Matches pulls from all labels matchinging the given regular expression
	// TODO: support concatenation
	Matches string `yaml:"matches,omitempty"`
	// Name pulls from a label with the given name.
	Name string `yaml:"name,omitempty"`

	// TODO: support changing label values?
}

// LabelAction is some action to take on a particular source label.
type LabelAction string

const (
	ReplaceLabel LabelAction = "replace"
)

// RelabelConfig specifies rules for relabeling series.
// TODO: work this out with regards to prometheus relabeling, or just replace
// with that...
type RelabelConfig struct {
	// Source controls the source of the label's contents.
	Source SourceLabel `yaml:"source"`
	// Target controls the target of the label's contents.
	// TODO: support renaming
	Target string `yaml:"target"`
	// Action controls the action taken on the label.
	Action LabelAction `yaml:"action"`
}

// ScrapeTarget represents a set of rules and limits about a particular
// class of objects.
type ScrapeTarget struct {
	// Name is a unique name for this configuration.
	Name string `yaml:"name"`
	// Conditions controls which objects these limits apply to.
	// The conditions are "ANDed" together to check for matches.
	Conditions []ScrapeCondition `yaml:"conditions"`
	// Scrape controls how the scraping occurs.
	Scrape ScrapeConfig `yaml:"scrape"`
	// Relabel specifies rules for rewriting labels
	Relabel []RelabelConfig `yaml:"relabel,omitempty"`
}

// CollectionConfig configures overall collection of metrics.
type CollectionConfig struct {
	// DefaultConfig is the default scraping config to insert into
	// the scrape configuration of the below conditions.
	DefaultConfig ScrapeConfig `yaml:"defaultConfig,omitempty"`
	// Configs is the list of configurations for scraping, checked in order
	Rules []ScrapeTarget `yaml:"rules"`
}
