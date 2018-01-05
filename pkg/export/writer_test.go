package export

import (
	"testing"
	"bytes"

	"github.com/prometheus/prometheus/pkg/labels"
)

func BenchmarkWriteAsText(b *testing.B) {
	s := NewSample("some_metric_foo_bar", labels.Labels{
		{"label1", `some\first"value`}, {"label2", "some\nsecond value"},
		{"label3", "a third value"}, {"label4", "the fourth"},
		{"label5", "five!"}, {"label6", "hexalabel"},
	}, 86753090000, 3.14159265359)
	out := bytes.NewBuffer(make([]byte, b.N*50))
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		s.WriteAsText(out)
	}
}

func TestWriteAsText(t *testing.T) {
	s := NewSample("some_metric_foo_bar", labels.Labels{
		{"label1", `some\first"value`}, {"label2", "some\nsecond value"},
		{"label3", "a third value"}, {"label4", "the fourth"},
		{"label5", "five!"}, {"label6", "hexalabel"},
	}, 86753090000, 3.14159265359)

	expected := `some_metric_foo_bar{label1="some\\first\"value",label2="some\nsecond value",label3="a third value",label4="the fourth",label5="five!",label6="hexalabel"} 3.14159265359 86753090000`

	out := bytes.NewBuffer([]byte{})
	written, err := s.WriteAsText(out)
	if err != nil {
		t.Fatalf("%s: %v != nil", "did not expect an error", err)
	}
	if len(expected) != written {
		t.Errorf("%s: %v != %v", "expected the amount written to be equal to the length of the expected output", len(expected), written)
	}

	actual := string(out.Bytes())
	if expected != actual {
		t.Errorf("%s: %s != %s", "expected the actual output to be as desired", expected, actual)
	}
}
