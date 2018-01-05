/*
Copyright 2016 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"flag"
	"os"
	"runtime"
	"context"
	"net/http"
	"fmt"
	"time"
	"net"

	"k8s.io/apiserver/pkg/util/logs"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/informers"
	kclient "k8s.io/client-go/kubernetes"
	"github.com/spf13/pflag"
	"github.com/golang/glog"

	"github.com/directxman12/scrape-goat/pkg/collector"
	"github.com/directxman12/scrape-goat/pkg/config"
	"github.com/directxman12/scrape-goat/pkg/discovery"
	"github.com/directxman12/scrape-goat/pkg/export"
)

type ScrapeProxyOptions struct {
	ConfigFilePath string
}

type ServingOptions struct {
	BindAddress net.IP
	BindPort int
}

func (o *ServingOptions) AddFlags(fs *pflag.FlagSet) {
	fs.IPVar(&o.BindAddress, "bind-address", o.BindAddress,
		"The IP address on which to listen for the configured ports")

	fs.IntVar(&o.BindPort, "insecure-port", o.BindPort, "The port on which to serve HTTP")
}

// AddFlags populates a flagset to bind command line options to this options object.
func (o *ScrapeProxyOptions) AddFlags(fs *pflag.FlagSet) {
	fs.StringVarP(&o.ConfigFilePath, "config", "c", "", "the processing configuration for this scraper")
}

// Complete turns this set of options into a runnable configuration.
func (o ScrapeProxyOptions) Complete() (*ScrapeProxyConfig, error) {
	targets, err := config.TargetsFromFile(o.ConfigFilePath)
	if err != nil {
		return nil, fmt.Errorf("unable to load config: %v", err)
	}

	clientConfig, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("unable to load in-cluster kubeconfig: %v", err)
	}

	client, err := kclient.NewForConfig(clientConfig)
	if err != nil {
		return nil, fmt.Errorf("unable to configure Kubernetes client: %v", err)
	}

	informerFactory := informers.NewSharedInformerFactory(client, 10*time.Minute)

	// TODO: make the base HTTP client configurable?
	// TODO: make the base context configurable?

	return &ScrapeProxyConfig{
		Targets: targets,
		Informers: informerFactory,

		BaseContext: context.Background(),
		BaseHTTPClient: http.DefaultClient,
	}, nil
}

// ScrapeProxyConfig represents a runnable configuration for the scrape proxy.
type ScrapeProxyConfig struct {
	Targets []config.ScrapeTarget
	Informers informers.SharedInformerFactory

	BaseContext context.Context
	BaseHTTPClient *http.Client

	stopChan chan struct{}
}

// Run starts the Scrape Proxy, returning the http.Handler that serves the Prometheus metrics.
func (c *ScrapeProxyConfig) Run() (http.Handler, error) {
	prov := collector.NewScraperProvider(c.BaseContext, c.BaseHTTPClient)
	mgr := discovery.NewDiscoveryManager(prov)
	kubeSource := discovery.NewKubeDiscovery(mgr)
	conditionBuilder := discovery.NewConditionFilterBuilder(kubeSource, kubeSource)

	c.stopChan = make(chan struct{})

	var providers []export.SampleProvider
	for _, targetCfg := range c.Targets {
		if glog.V(7) {
			prettyPrintedTarget, err := targetCfg.PrettyPrint()
			if err != nil {
				glog.Errorf("unable to pretty-print target %q", targetCfg.Name)
			} else {
				glog.Infof("Target %q:\n\n%s\n\n", targetCfg.Name, prettyPrintedTarget)
			}
		}

		if _, err := conditionBuilder.ConditionToFilters(targetCfg.Name, targetCfg.Conditions...); err != nil {
			return nil, fmt.Errorf("error setting up for rule %q: %v", targetCfg.Name, err)
		}

		// TODO: do we really need to do this?
		relabelCfgs := make([]*config.RelabelConfig, len(targetCfg.Relabel))
		for i := range relabelCfgs {
			relabelCfgs[i] = &targetCfg.Relabel[i]
		}

		relabeler, err := discovery.NewRelabelerFromConfig(relabelCfgs)
		if err != nil {
			return nil, fmt.Errorf("error setting up for rule %q: %v", targetCfg.Name, err)
		}

		urlBuilder, err := discovery.NewReqBuilderFromConfig(targetCfg.Scrape.Connection)
		if err != nil {
			return nil, fmt.Errorf("error setting up for rule %q: %v", targetCfg.Name, err)
		}

		limitsCfg := targetCfg.Scrape.Limits
		limiter := discovery.NewSeriesFilter(*limitsCfg.MaxSeriesPerFamily, *limitsCfg.MaxSeries, *limitsCfg.MaxFamilies)
		runner := mgr.RunnerForConfig(targetCfg.Name, relabeler, limiter, urlBuilder, time.Duration(targetCfg.Scrape.Timing.ScrapeTimeout))
		go runner.RunUntil(time.Duration(targetCfg.Scrape.Timing.ScrapeInterval), c.stopChan)
		providers = append(providers, runner)
	}

	informers := c.Informers.Core().V1()
	kubeSource.Run(informers.Namespaces().Informer(),
		informers.Pods().Informer(),
		informers.Endpoints().Informer(),
		informers.Services().Informer())

	c.Informers.Start(c.stopChan)

	for i, runner := range providers {
		if err := runner.(discovery.Runner).RunOnce(); err != nil {
			return nil, fmt.Errorf("unable to run initial scrape for rule %q: %v", c.Targets[i].Name, err)
		}
	}

	handler := export.NewHandler(providers)

	return handler, nil
}

func main() {
	logs.InitLogs()
	defer logs.FlushLogs()

	if len(os.Getenv("GOMAXPROCS")) == 0 {
		runtime.GOMAXPROCS(runtime.NumCPU())
	}

	scrapeOpts := &ScrapeProxyOptions{}
	scrapeOpts.AddFlags(pflag.CommandLine)
	servingOpts := &ServingOptions{BindPort: 8080}
	servingOpts.AddFlags(pflag.CommandLine)
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	pflag.Parse()

	cfg, err := scrapeOpts.Complete()
	if err != nil {
		glog.Fatalf("error initializing configuration: %v", err)
	}

	handler, err := cfg.Run()
	if err != nil {
		glog.Fatalf("error running scrapers: %v", err)
	}

	// TODO: move this into a "Complete"-like method on servingOpts
	http.Handle("/output", handler)
	listenAddr := net.JoinHostPort(servingOpts.BindAddress.String(), fmt.Sprintf("%v", servingOpts.BindPort))
	glog.Fatal(http.ListenAndServe(listenAddr, nil))
}
