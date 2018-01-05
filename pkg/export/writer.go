package export

import (
	"io"
	"fmt"
	"strings"

	// TODO: can this be taken from common/model/expformat?
	"github.com/prometheus/prometheus/pkg/labels"
)

// Some of this was inspired by the prometheus expfmt code,
// which does close to what we want, but with slightly more
// overhead in some cases and some mechanics that are cumbersome
// for us.

func NewSample(name string, lbls labels.Labels, timestamp int64, value float64) *Sample {
	return &Sample{
		Series: Series{
			Name: name,
			Labels: lbls,
		},
		Timestamp: timestamp,
		Value: value,
	}
}

// Series represents a single series with a name and unique set of labels.
type Series struct {
	// Name is the name of the metric.
	Name string

	// Labels are the labels for this series.
	Labels labels.Labels
}

// Sample represents a single, timestamped sample with associated
// metric name and series labels.
type Sample struct {
	Series

	// Timestamp is the time at which this sample was collected.
	Timestamp int64

	// Value is the actual measured value of this sample.
	Value float64
}

// SampleProvider knows how to provide a some set of collected samples.
// The samples are not necessarily sorted, but some implementations may
// provide sorted samples.
type SampleProvider interface {
	// Samples produces the most recent set of samples collected, placing
	// them in the given channel.  It should not return until the last
	// sample has been sent.
	Samples(chan<- *Sample)
}

// NB: Prometheus 2.0 dropped support for the protobuf format,
// so for now we just support text.

// WriteAsText writes the metric name and labels
// to the given io.Writer.
func (s *Series) WriteAsText(out io.Writer) (int, error) {
	// so that callers can back out things if we return an error
	written := 0
	n, err := out.Write([]byte(s.Name))
	written += n
	if err != nil || len(s.Labels) == 0 {
		return written, err
	}

	sep := '{'
	for _, lblPair := range s.Labels {
		// TODO: is it better to avoid fmt?
		n, err = fmt.Fprintf(out, `%c%s="%s"`, sep, lblPair.Name, escapeLabelValue(lblPair.Value))
		written += n
		if err != nil {
			return written, err
		}
		sep = ','
	}
	n, err = out.Write([]byte{'}'})
	written += n
	return written, err
}

// WriteAsText writes the full sample line out to the given io.Writer.
func (s *Sample) WriteAsText(out io.Writer) (int, error) {
	written, err := s.Series.WriteAsText(out)
	if err != nil {
		return written, err
	}

	n, err := fmt.Fprintf(out, " %v %v", s.Value, s.Timestamp)
	written += n
	return written, err
}

var labelValueEscaper = strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`)

// escapeLabelValue escapes occurences of problematic characters in Prometheus labels,
// namely '\' with '\\', newline with '\n' and '"' with '\"'.
func escapeLabelValue(in string) string {
	return labelValueEscaper.Replace(in)
}
