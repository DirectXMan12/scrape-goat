package export

import (
	"net/http"

	"github.com/golang/glog"
)

// SampleProviderHandler provides Prometheus metrics to
// HTTP clients using samples from the given SampleProvider.
// The given SampleProvider must provide samples which meet
// the Prometheus exposition format requirements
// (right order, etc).
type SampleProviderHandler struct {
	provider SampleProvider
}

const prometheusTextContentType = "text/plain; version=0.0.4"
var newlineChar = []byte{'\n'}

// TODO: support the Prometheus exposition format?  Prometheus 2.0
// doesn't support ingesting it, so there's not much point...

func (e *SampleProviderHandler) ServeHTTP(resp http.ResponseWriter, req *http.Request) {
	// TODO: reject incorrect accept handling, etc

	resp.Header().Set("Content-Type", prometheusTextContentType)

	samples := make(chan *Sample, 100)
	go func() {
		defer close(samples)
		e.provider.Samples(samples)
	}()

	for sample := range samples {
		_, err := sample.WriteAsText(resp)
		if err != nil {
			// TODO: deal with this more gracefully?
			glog.Errorf("unable to write samples as exposition format: %v", err)
			return
		}
		_, err = resp.Write(newlineChar)
		if err != nil {
			// TODO: deal with this more gracefully?
			glog.Errorf("unable to write samples (newline) as exposition format: %v", err)
			return
		}
	}
}

// NewHandler creates a new HTTP Handler which serves metrics from
// the given providers.  It takes care of sorting the metrics appropriately.
func NewHandler(providers []SampleProvider) http.Handler {
	return &SampleProviderHandler{
		provider: &SortedSampleProvider{
			inputProvider: &AggregatedSampleProvider{
				providers: providers,
			},
		},
	}
}
