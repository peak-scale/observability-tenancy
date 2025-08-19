package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	corev1 "k8s.io/api/core/v1"
)

func TestNewRecorder_MetricNamesAreNamespaced(t *testing.T) {
	rec := NewRecorder("cortex")
	reg := prometheus.NewRegistry()
	reg.MustRegister(rec.Collectors()...)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather failed: %v", err)
	}

	got := map[string]bool{}
	for _, mf := range mfs {
		got[mf.GetName()] = true
	}

	expect := []string{
		"cortex_proxy_timeseries_batches_received_total",
		"cortex_proxy_timeseries_batches_received_bytes",
		"cortex_proxy_timeseries_received_total",
		"cortex_proxy_timeseries_request_duration_seconds",
		"cortex_proxy_timeseries_request_errors_total",
		"cortex_proxy_timeseries_requests_total",
	}

	for _, name := range expect {
		if !got[name] {
			t.Errorf("expected metric %q to be registered; got set: %v", name, keys(got))
		}
	}
}

func TestCollectors_ReturnsAll(t *testing.T) {
	rec := NewRecorder("any")
	cs := rec.Collectors()
	if len(cs) != 6 {
		t.Fatalf("Collectors() expected 6 collectors, got %d", len(cs))
	}
	// ensure none are nil
	for i, c := range cs {
		if c == nil {
			t.Fatalf("collector at index %d is nil", i)
		}
	}
}

func TestDeleteMetricsForNamespace_PanicsOnWrongLabelCardinality(t *testing.T) {
	rec := NewRecorder("cortex")

	// Seed some series so Delete* actually has something to remove.
	rec.MetricTimeseriesRequests.WithLabelValues("ns1").Inc()
	rec.MetricTimeseriesRequestErrors.WithLabelValues("ns1").Inc()
	// The duration vec has **two** labels: ("code", "tenant").
	rec.MetricTimeseriesRequestDurationSeconds.WithLabelValues("200", "ns1").Observe(1)

	ns := &corev1.Namespace{}
	ns.Name = "ns1"

	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected DeleteMetricsForNamespace to panic due to label cardinality mismatch, but it did not")
		}
	}()

	// Current implementation calls:
	//   r.MetricTimeseriesRequestDurationSeconds.DeleteLabelValues(ns.Name)
	// which is missing the "code" label and will panic with:
	//   "inconsistent label cardinality"
	rec.DeleteMetricsForNamespace(ns)
}

// --- helpers ---

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Optional: sanity to ensure the gathered families contain at least a single metric.
func TestNewRecorder_GatherHasMetrics(t *testing.T) {
	rec := NewRecorder("probe")
	reg := prometheus.NewRegistry()
	reg.MustRegister(rec.Collectors()...)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather failed: %v", err)
	}
	if len(mfs) == 0 {
		t.Fatalf("expected some metric families, got none")
	}
	// Spot-check that families have a name and type.
	for _, mf := range mfs {
		if mf.GetName() == "" {
			t.Fatalf("metric family has empty name: %+v", mf)
		}
		_ = mf.GetType() // dto.MetricType
		if len(mf.GetMetric()) == 0 && mf.GetType() != dto.MetricType_SUMMARY {
			// Not a hard failure, just a note: counters/histograms often have zero samples before use.
			break
		}
	}
}
