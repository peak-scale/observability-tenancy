package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	corev1 "k8s.io/api/core/v1"
	crtlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

type ProxyRecorder struct {
	MetricTimeseriesBatchesReceived        prometheus.Counter
	MetricTimeseriesBatchesReceivedBytes   prometheus.Histogram
	MetricTimeseriesReceived               *prometheus.CounterVec
	MetricTimeseriesRequestDurationSeconds *prometheus.HistogramVec
	MetricTimeseriesRequestErrors          *prometheus.CounterVec
	MetricTimeseriesRequests               *prometheus.CounterVec
}

func MustMakeRecorder(proxyName string) *ProxyRecorder {
	metricsRecorder := NewRecorder(proxyName)
	crtlmetrics.Registry.MustRegister(metricsRecorder.Collectors()...)

	return metricsRecorder
}

func NewRecorder(proxyName string) *ProxyRecorder {
	namespace := proxyName + "_proxy"

	return &ProxyRecorder{
		MetricTimeseriesBatchesReceived: prometheus.NewCounter(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "timeseries_batches_received_total",
				Help:      "The total number of batches received.",
			},
		),
		MetricTimeseriesBatchesReceivedBytes: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "timeseries_batches_received_bytes",
				Help:      "Size in bytes of timeseries batches received.",
				Buckets:   []float64{0.5, 1, 10, 25, 100, 250, 500, 1000, 5000, 10000, 30000, 300000, 600000, 1800000, 3600000},
			},
		),
		MetricTimeseriesReceived: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "timeseries_received_total",
				Help:      "The total number of timeseries received.",
			},
			[]string{"tenant"},
		),
		MetricTimeseriesRequestDurationSeconds: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "timeseries_request_duration_seconds",
				Help:      "HTTP write request duration for tenant-specific timeseries in seconds, filtered by response code.",
				Buckets:   []float64{0.5, 1, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000, 30000, 60000, 1800000, 3600000},
			},
			[]string{"code", "tenant"},
		),
		MetricTimeseriesRequestErrors: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "timeseries_request_errors_total",
				Help:      "The total number of tenant-specific timeseries writes that yielded errors.",
			},
			[]string{"tenant"},
		),
		MetricTimeseriesRequests: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "timeseries_requests_total",
				Help:      "The total number of tenant-specific timeseries writes.",
			},
			[]string{"tenant"},
		),
	}
}

func (r *ProxyRecorder) Collectors() []prometheus.Collector {
	return []prometheus.Collector{
		r.MetricTimeseriesBatchesReceived,
		r.MetricTimeseriesBatchesReceivedBytes,
		r.MetricTimeseriesReceived,
		r.MetricTimeseriesRequestDurationSeconds,
		r.MetricTimeseriesRequestErrors,
		r.MetricTimeseriesRequests,
	}
}

// DeleteCondition deletes the condition metrics for the ref.
func (r *ProxyRecorder) DeleteMetricsForNamespace(ns *corev1.Namespace) {
	r.MetricTimeseriesRequests.DeleteLabelValues(ns.Name)
	r.MetricTimeseriesRequestDurationSeconds.DeleteLabelValues(ns.Name)
	r.MetricTimeseriesRequestErrors.DeleteLabelValues(ns.Name)
	r.MetricTimeseriesRequests.DeleteLabelValues(ns.Name)
}
