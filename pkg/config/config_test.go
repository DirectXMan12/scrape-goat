package config

import (
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/prometheus/common/model"
)

func TestConfig(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Discovery")
}

var _ = Describe("Configuration", func() {
	Describe("Loading", func() {
		var sampleFilePath = "../../sample-config.yaml"
		expectedTargets := []ScrapeTarget{
			{
				Name: "infra",
				Conditions: []ScrapeCondition{
					{On: ResourceNamespace, When: &ScrapeConditionWhen{ Name: "openshift-infra" }},
					{From: ResourcePod},
					{From: ResourceEndpoints},
					{From: ResourceService},
				},
				Scrape: ScrapeConfig{
					Timing: &TimingConfig{
						ScrapeInterval: model.Duration(1*time.Minute),
						ScrapeTimeout: model.Duration(5*time.Second),
					},
					Limits: &LimitsConfig{intPtr(0), intPtr(0), intPtr(0)},
					Connection: &ConnectionConfig{
						Path: &VariableSource{From: "metadata:annotations:metrics.openshift.io/path"},
						Port: &VariableSource{From: "metadata:annotations:metrics.openshift.io/port"},
						Scheme: &VariableSource{Const: "http"},
						Username: &VariableSource{From: "metadata:annotations:metrics.openshift.io/password"},
						Password: &VariableSource{From: "metadata:annotations:metrics.openshift.io/username"},
					},
				},
			},
			{
				Name: "trusted",
				Conditions: []ScrapeCondition{
					{
						On: ResourceNamespace,
						When: &ScrapeConditionWhen{
							Annotations: map[string]string{"openshift.io/trust-metrics": "true"},
						},
					},
					{
						From: ResourcePod,
						When: &ScrapeConditionWhen{
							Annotations: map[string]string{"openshift.io/metrics-provider": "true"},
						},
					},
				},
				Scrape: ScrapeConfig{
					Timing: &TimingConfig{
						ScrapeInterval: model.Duration(1*time.Minute),
						ScrapeTimeout: model.Duration(5*time.Second),
					},
					Limits: &LimitsConfig{
						MaxSeries: intPtr(0),
						MaxSeriesPerFamily: intPtr(0),
						MaxFamilies: intPtr(1),
					},
					Connection: &ConnectionConfig{
						Path: &VariableSource{From: "metadata:annotations:metrics.openshift.io/path"},
						Port: &VariableSource{From: "metadata:annotations:metrics.openshift.io/port"},
						Scheme: &VariableSource{Const: "http"},
						Username: &VariableSource{From: "metadata:annotations:metrics.openshift.io/password"},
						Password: &VariableSource{From: "metadata:annotations:metrics.openshift.io/username"},
					},
				},
				Relabel: []RelabelConfig{
					{
						Source: SourceLabel{From: "metadata:namespace"},
						Target: "namespace",
					},
				},
			},
			{
				Name: "user",
				Conditions: []ScrapeCondition{
					{
						From: ResourcePod,
						When: &ScrapeConditionWhen{
							Annotations: map[string]string{"openshift.io/metrics-provider": "true"},
						},
					},
				},
				Scrape: ScrapeConfig{
					Timing: &TimingConfig{
						ScrapeInterval: model.Duration(1*time.Minute),
						ScrapeTimeout: model.Duration(5*time.Second),
					},
					Limits: &LimitsConfig{
						MaxFamilies: intPtr(0),
						MaxSeriesPerFamily: intPtr(0),
						MaxSeries: intPtr(10),
					},
					Connection: &ConnectionConfig{
						Path: &VariableSource{From: "metadata:annotations:metrics.openshift.io/path"},
						Port: &VariableSource{From: "metadata:annotations:metrics.openshift.io/port"},
						Scheme: &VariableSource{Const: "http"},
						Username: &VariableSource{From: "metadata:annotations:metrics.openshift.io/password"},
						Password: &VariableSource{From: "metadata:annotations:metrics.openshift.io/username"},
					},
				},
				Relabel: []RelabelConfig{
					{
						Source: SourceLabel{From: "metadata:namespace"},
						Target: "namespace",
					},
					{
						Source: SourceLabel{From: "metadata:name"},
						Target: "name",
					},
					{
						Source: SourceLabel{Const: "user-provided"},
						Target: "metric_kind",
					},
					{
						Source: SourceLabel{Matches: ".*"},
						Target: "user-${0}",
					},
				},
			},
		}

		It("should load configuration from a file and return defaulted targets", func() {
			targets, err := TargetsFromFile(sampleFilePath)
			Expect(err).ToNot(HaveOccurred())
			// Loop to get better error output
			Expect(targets).To(HaveLen(len(expectedTargets)))
			for i, expectedTarget := range expectedTargets {
				Expect(targets[i]).To(Equal(expectedTarget))
			}
		})
	})
})
