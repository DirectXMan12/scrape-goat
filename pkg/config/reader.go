package config

import (
	"os"
	"fmt"
	"io/ioutil"

	yaml "gopkg.in/yaml.v2"
)

func intPtr(base int) *int {
	return &base
}

// PrettyPrint prints a given target in a human-readable form.
func (t *ScrapeTarget) PrettyPrint() (string, error) {
	out, err := yaml.Marshal(t)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// TargetsFromFile reads in YAML configuration from the given
// file, and converts it into a series of scrape target configurations,
// with defaults applied.
func TargetsFromFile(filename string) ([]ScrapeTarget, error) {
	file, err := os.Open(filename)
	defer file.Close()
	if err != nil {
		return nil, fmt.Errorf("unable to load config file: %v", err)
	}
	contents, err := ioutil.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("unable to load config file: %v", err)
	}
	return TargetsFrom(contents)
}

// TargetsFrom converts the given YAML into a series of scrape targets,
// configurations, with defaults applied.
func TargetsFrom(source []byte) ([]ScrapeTarget, error) {
	var fullCfg CollectionConfig
	if err := yaml.Unmarshal(source, &fullCfg); err != nil {
		return nil, fmt.Errorf("unable to parse config: %v", err)
	}
	return Normalize(ApplyDefaults(fullCfg.DefaultConfig, fullCfg.Rules)), nil
}

// Normalize fixes up limits overrides so that empty string is coverted back
// to nil when used to override a default.
func Normalize(targets []ScrapeTarget) []ScrapeTarget {
	for i := range targets {
		limits := targets[i].Scrape.Limits
		if (limits.MaxSeriesPerFamily == nil) { limits.MaxSeriesPerFamily = intPtr(0) }
		if (limits.MaxSeries == nil) { limits.MaxSeries = intPtr(0) }
		if (limits.MaxFamilies == nil) { limits.MaxFamilies = intPtr(0) }
	}

	return targets
}

// ApplyDefaults takes the given defaults and applies them to the given targets,
// producing new targets with the defaults applied.
func ApplyDefaults(defaults ScrapeConfig, targets []ScrapeTarget) []ScrapeTarget {
	defaultedTargets := make([]ScrapeTarget, len(targets))
	for i := range defaultedTargets {
		target := &defaultedTargets[i]
		origTarget := targets[i].Scrape

		target.Name = targets[i].Name

		// TODO: do this in reflection?
		// TODO: too bad autogenerating code in golang is such a pain...

		// make copies so we don't change the defaults by accident
		var defaultLimits LimitsConfig
		var defaultTiming TimingConfig
		var defaultConnection ConnectionConfig
		if defaults.Limits != nil {
			defaultLimits = *defaults.Limits
		}
		if defaults.Timing != nil {
			defaultTiming = *defaults.Timing
		}
		if defaults.Connection != nil {
			defaultConnection = *defaults.Connection
		}

		// first, populate the appropriate parts from the default,
		// and non-defaulted parts from the original
		target.Scrape = ScrapeConfig{
			Limits: &defaultLimits,
			Timing: &defaultTiming,
			Connection: &defaultConnection,
		}

		target.Conditions = targets[i].Conditions
		target.Relabel = targets[i].Relabel

		// then, copy the overrides over the defaults
		if origTarget.Timing != nil {
			if (origTarget.Timing.ScrapeInterval != 0) { target.Scrape.Timing.ScrapeInterval = origTarget.Timing.ScrapeInterval }
			if (origTarget.Timing.ScrapeTimeout != 0) { target.Scrape.Timing.ScrapeTimeout = origTarget.Timing.ScrapeTimeout }
		}

		if origTarget.Limits != nil {
			if (origTarget.Limits.MaxSeriesPerFamily != nil) { target.Scrape.Limits.MaxSeriesPerFamily = origTarget.Limits.MaxSeriesPerFamily }
			if (origTarget.Limits.MaxSeries != nil) { target.Scrape.Limits.MaxSeries = origTarget.Limits.MaxSeries }
			if (origTarget.Limits.MaxFamilies != nil) { target.Scrape.Limits.MaxFamilies = origTarget.Limits.MaxFamilies }
		}

		if origTarget.Connection != nil {
			if (origTarget.Connection.Path != nil) { target.Scrape.Connection.Path = origTarget.Connection.Path }
			if (origTarget.Connection.Port != nil) { target.Scrape.Connection.Port = origTarget.Connection.Port }
			if (origTarget.Connection.Scheme != nil) { target.Scrape.Connection.Scheme = origTarget.Connection.Scheme }
			if (origTarget.Connection.Username != nil) { target.Scrape.Connection.Username = origTarget.Connection.Username }
			if (origTarget.Connection.Password != nil) { target.Scrape.Connection.Password = origTarget.Connection.Password }
		}
	}

	return defaultedTargets
}
