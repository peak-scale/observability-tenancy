package cortex

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/gogo/protobuf/proto"
	"github.com/golang/snappy"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/prometheus/prompb"
	fh "github.com/valyala/fasthttp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/peak-scale/observability-tenancy/internal/config"
	"github.com/peak-scale/observability-tenancy/internal/handlers/handler"
	"github.com/peak-scale/observability-tenancy/internal/meta"
	"github.com/peak-scale/observability-tenancy/internal/metrics"
	"github.com/peak-scale/observability-tenancy/internal/stores"
)

var endpoint = "http://127.0.0.1:31001/push"

// helper: decode a forwarded write request body into prompb.WriteRequest
var decodeWriteReq = func(b []byte) *prompb.WriteRequest {
	raw, err := snappy.Decode(nil, b)
	Expect(err).NotTo(HaveOccurred(), "snappy decode failed")
	var wr prompb.WriteRequest
	Expect(proto.Unmarshal(raw, &wr)).To(Succeed(), "proto unmarshal failed")
	return &wr
}

// helper: fetch a label value by name
var getLabel = func(lbls []prompb.Label, name string) (string, bool) {
	for _, l := range lbls {
		if l.Name == name {
			return l.Value, true
		}
	}
	return "", false
}

var _ = Describe("Processor Forwarding", func() {
	var (
		proc       *handler.Handler
		fakeTarget *httptest.Server
		receivedMu sync.Mutex
		ctx        context.Context
		cancel     context.CancelFunc
		cfg        config.Config
		store      *stores.NamespaceStore
		metric     *metrics.ProxyRecorder
	)

	type receivedRequest struct {
		Header http.Header
		Body   []byte
	}

	var receivedReqs []receivedRequest

	clearRequests := func() {
		receivedMu.Lock()
		receivedReqs = receivedReqs[:0] // or: receivedReqs = nil
		receivedMu.Unlock()
	}

	snapshotRequests := func() []receivedRequest {
		receivedMu.Lock()
		defer receivedMu.Unlock()
		out := make([]receivedRequest, len(receivedReqs))
		copy(out, receivedReqs)
		return out
	}

	// Waits until at least n requests have been captured, then returns a snapshot.
	_ = func(n int, timeout time.Duration) []receivedRequest {
		Eventually(func() int {
			receivedMu.Lock()
			l := len(receivedReqs)
			receivedMu.Unlock()
			return l
		}, timeout, 50*time.Millisecond).Should(BeNumerically(">=", n))
		return snapshotRequests()
	}

	metric = metrics.MustMakeRecorder("cortex") // or a mock recorder

	BeforeEach(func() {
		// Initialize any required dependencies (store, metrics, logger).
		store = stores.NewNamespaceStore() // or a suitable mock
		store.Update(&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "solar",
				Annotations: map[string]string{
					meta.AnnotationOrganisationName: "solar-org",
				},
			},
		}, cfg.Tenant)

		// Add another namespace with a different organisation.
		store.Update(&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "green",
				Annotations: map[string]string{
					meta.AnnotationOrganisationName: "green-org",
				},
			},
		}, cfg.Tenant)

		// Use namespace as organisation
		store.Update(&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "wind",
			},
		}, &config.TenantConfig{
			SetNamespaceAsDefault: true,
		})
	})

	AfterEach(func() {
		cancel()
		clearRequests()
		fakeTarget.Close()
	})

	It("Proxy Headers correctly", func() {

		ctx, cancel = context.WithCancel(context.Background())
		log, _ := logr.FromContext(ctx)

		// Create a fake target server that records request headers.
		fakeTarget = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedMu.Lock()
			defer receivedMu.Unlock()

			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "failed to read request body", http.StatusInternalServerError)
				return
			}
			defer r.Body.Close()

			receivedReqs = append(receivedReqs, receivedRequest{
				Header: r.Header.Clone(),
				Body:   append([]byte(nil), body...),
			})

			w.Header().Set("Connection", "close")
			w.WriteHeader(http.StatusOK)
		}))

		cfg = config.Config{
			Bind: "0.0.0.0:31001",
			Backend: &config.Backend{
				URL: fakeTarget.URL,
			},

			Timeout: 5 * time.Second,
			// Set other fields as needed, for example Tenant config.
			Tenant: &config.TenantConfig{
				Labels: []string{
					"namespace",
					"target_namespace",
				},
				Header:             "X-Scope-OrgID",
				SetHeader:          true,
				Default:            "default",
				Prefix:             "test-",
				PrefixPreferSource: false,
				TenantLabel:        "tenant",
			},
		}

		proc = NewCortexProcessor(log, cfg, store, metric)

		log.Info("Starting Server")
		go func() {
			if err := proc.Start(ctx); err != nil && err != http.ErrServerClosed {
				log.Error(err, "processor failed")
			}
			log.Info("Starting Start")
		}()

		By("settings default tenant", func() {

			// Prepare a minimal prompb.WriteRequest.
			wr := &prompb.WriteRequest{
				Timeseries: []prompb.TimeSeries{
					{
						Labels: []prompb.Label{
							{Name: "job", Value: "test"},
							{Name: "instance", Value: "localhost:9090"},
						},
						Samples: []prompb.Sample{
							{Value: 123, Timestamp: time.Now().UnixMilli()},
						},
					},
				},
			}
			// Marshal and compress using the processor helper.
			buf, err := marshal(wr)
			Expect(err).NotTo(HaveOccurred())

			// Build a POST request to the processor's /push endpoint.
			// Since processor uses fasthttp, use its client for the test.
			var req fh.Request
			var resp fh.Response
			req.SetRequestURI(endpoint)
			req.Header.SetMethod(fh.MethodPost)
			req.Header.Set("Content-Encoding", "snappy")
			req.Header.Set("Content-Type", "application/x-protobuf")
			req.SetBody(buf)

			// Send the request using fasthttp.
			err = fh.DoTimeout(&req, &resp, cfg.Timeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode()).To(Equal(fh.StatusOK))

			// Wait until the fake target receives the forwarded request.
			var received receivedRequest

			Eventually(func() bool {
				receivedMu.Lock()
				if len(receivedReqs) == 0 {
					return false
				}
				received = receivedReqs[0]
				return true
			}, 5*time.Second, 200*time.Millisecond).Should(BeTrue())
			receivedMu.Unlock()

			Expect(received.Header).To(HaveKeyWithValue(http.CanonicalHeaderKey("X-Scope-OrgID"), []string{"test-default"}))
			Expect(received.Header).To(HaveKeyWithValue(http.CanonicalHeaderKey("X-Prometheus-Remote-Write-Version"), []string{"0.1.0"}))
			Expect(received.Header).To(HaveKeyWithValue(http.CanonicalHeaderKey("Content-Encoding"), []string{"snappy"}))

			clearRequests()
		})

		By("proxy correct tenant (solar)", func() {

			// Prepare a minimal prompb.WriteRequest.
			wr := &prompb.WriteRequest{
				Timeseries: []prompb.TimeSeries{
					{
						Labels: []prompb.Label{
							{Name: "job", Value: "test"},
							{Name: "instance", Value: "localhost:9090"},
							{Name: "namespace", Value: "solar"},
						},
						Samples: []prompb.Sample{
							{Value: 123, Timestamp: time.Now().UnixMilli()},
						},
					},
				},
			}

			// Marshal and compress using the processor helper.
			buf, err := marshal(wr)
			Expect(err).NotTo(HaveOccurred())

			// Build a POST request to the processor's /push endpoint.
			// Since processor uses fasthttp, use its client for the test.
			var req fh.Request
			var resp fh.Response
			req.SetRequestURI(endpoint)
			req.Header.SetMethod(fh.MethodPost)
			req.Header.Set("Content-Encoding", "snappy")
			req.Header.Set("Content-Type", "application/x-protobuf")
			req.SetBody(buf)

			// Send the request using fasthttp.
			err = fh.DoTimeout(&req, &resp, cfg.Timeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode()).To(Equal(fh.StatusOK))

			// Wait until the fake target receives the forwarded request.
			var received receivedRequest
			Eventually(func() bool {
				if len(receivedReqs) == 0 {
					return false
				}
				received = receivedReqs[0]
				return true
			}, 5*time.Second, 200*time.Millisecond).Should(BeTrue())

			Expect(received.Header).To(HaveKeyWithValue(http.CanonicalHeaderKey("X-Scope-OrgID"), []string{cfg.Tenant.Prefix + "solar-org"}))
			Expect(received.Header).To(HaveKeyWithValue(http.CanonicalHeaderKey("X-Prometheus-Remote-Write-Version"), []string{"0.1.0"}))

			clearRequests()
		})

		By("proxy correct tenant (wind)", func() {

			// Prepare a minimal prompb.WriteRequest.
			wr := &prompb.WriteRequest{
				Timeseries: []prompb.TimeSeries{
					{
						Labels: []prompb.Label{
							{Name: "job", Value: "test"},
							{Name: "instance", Value: "localhost:9090"},
							{Name: "namespace", Value: "wind"},
						},
						Samples: []prompb.Sample{
							{Value: 123, Timestamp: time.Now().UnixMilli()},
						},
					},
				},
			}

			// Marshal and compress using the processor helper.
			buf, err := marshal(wr)
			Expect(err).NotTo(HaveOccurred())

			// Build a POST request to the processor's /push endpoint.
			// Since processor uses fasthttp, use its client for the test.
			var req fh.Request
			var resp fh.Response
			req.SetRequestURI(endpoint)
			req.Header.SetMethod(fh.MethodPost)
			req.Header.Set("Content-Encoding", "snappy")
			req.Header.Set("Content-Type", "application/x-protobuf")
			req.SetBody(buf)

			// Send the request using fasthttp.
			err = fh.DoTimeout(&req, &resp, cfg.Timeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode()).To(Equal(fh.StatusOK))

			// Wait until the fake target receives the forwarded request.
			var received receivedRequest
			Eventually(func() bool {
				if len(receivedReqs) == 0 {
					return false
				}
				received = receivedReqs[0]
				return true
			}, 5*time.Second, 200*time.Millisecond).Should(BeTrue())

			Expect(received.Header).To(HaveKeyWithValue(http.CanonicalHeaderKey("X-Scope-OrgID"), []string{cfg.Tenant.Prefix + "wind"}))
			Expect(received.Header).To(HaveKeyWithValue(http.CanonicalHeaderKey("X-Prometheus-Remote-Write-Version"), []string{"0.1.0"}))

			clearRequests()
		})

		By("proxy correct tenant (green)", func() {

			// Prepare a minimal prompb.WriteRequest.
			wr := &prompb.WriteRequest{
				Timeseries: []prompb.TimeSeries{
					{
						Labels: []prompb.Label{
							{Name: "job", Value: "test"},
							{Name: "instance", Value: "localhost:9090"},
							{Name: "target_namespace", Value: "green"},
						},
						Samples: []prompb.Sample{
							{Value: 123, Timestamp: time.Now().UnixMilli()},
						},
					},
				},
			}

			// Marshal and compress using the processor helper.
			buf, err := marshal(wr)
			Expect(err).NotTo(HaveOccurred())

			// Build a POST request to the processor's /push endpoint.
			// Since processor uses fasthttp, use its client for the test.
			var req fh.Request
			var resp fh.Response
			req.SetRequestURI(endpoint)
			req.Header.SetMethod(fh.MethodPost)
			req.Header.Set("Content-Encoding", "snappy")
			req.Header.Set("Content-Type", "application/x-protobuf")
			req.SetBody(buf)

			// Send the request using fasthttp.
			err = fh.DoTimeout(&req, &resp, cfg.Timeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode()).To(Equal(fh.StatusOK))

			// Wait until the fake target receives the forwarded request.
			var received receivedRequest
			Eventually(func() bool {
				if len(receivedReqs) == 0 {
					return false
				}
				received = receivedReqs[0]
				return true
			}, 5*time.Second, 200*time.Millisecond).Should(BeTrue())

			Expect(received.Header).To(HaveKeyWithValue(http.CanonicalHeaderKey("X-Scope-OrgID"), []string{cfg.Tenant.Prefix + "green-org"}))
			Expect(received.Header).To(HaveKeyWithValue(http.CanonicalHeaderKey("X-Prometheus-Remote-Write-Version"), []string{"0.1.0"}))

			clearRequests()
		})

		By("default on no match", func() {

			// Prepare a minimal prompb.WriteRequest.
			wr := &prompb.WriteRequest{
				Timeseries: []prompb.TimeSeries{
					{
						Labels: []prompb.Label{
							{Name: "job", Value: "test"},
							{Name: "instance", Value: "localhost:9090"},
							{Name: "target_namespace", Value: "oil"},
						},
						Samples: []prompb.Sample{
							{Value: 123, Timestamp: time.Now().UnixMilli()},
						},
					},
				},
			}

			// Marshal and compress using the processor helper.
			buf, err := marshal(wr)
			Expect(err).NotTo(HaveOccurred())

			// Build a POST request to the processor's /push endpoint.
			// Since processor uses fasthttp, use its client for the test.
			var req fh.Request
			var resp fh.Response
			req.SetRequestURI(endpoint)
			req.Header.SetMethod(fh.MethodPost)
			req.Header.Set("Content-Encoding", "snappy")
			req.Header.Set("Content-Type", "application/x-protobuf")
			req.SetBody(buf)

			// Send the request using fasthttp.
			err = fh.DoTimeout(&req, &resp, cfg.Timeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode()).To(Equal(fh.StatusOK))

			// Wait until the fake target receives the forwarded request.
			var received receivedRequest
			Eventually(func() bool {
				if len(receivedReqs) == 0 {
					return false
				}
				received = receivedReqs[0]
				return true
			}, 5*time.Second, 200*time.Millisecond).Should(BeTrue())

			Expect(received.Header).To(HaveKeyWithValue(http.CanonicalHeaderKey("X-Scope-OrgID"), []string{cfg.Tenant.Prefix + cfg.Tenant.Default}))
			Expect(received.Header).To(HaveKeyWithValue(http.CanonicalHeaderKey("X-Prometheus-Remote-Write-Version"), []string{"0.1.0"}))

			clearRequests()
		})

		By("sending two series for different tenants in a single request", func() {
			wr := &prompb.WriteRequest{
				Timeseries: []prompb.TimeSeries{
					{ // goes to solar -> test-solar-org
						Labels: []prompb.Label{
							{Name: "job", Value: "test"},
							{Name: "instance", Value: "localhost:9090"},
							{Name: "namespace", Value: "solar"},
						},
						Samples: []prompb.Sample{
							{Value: 1, Timestamp: time.Now().UnixMilli()},
						},
					},
					{ // goes to wind -> test-wind (namespace-as-org)
						Labels: []prompb.Label{
							{Name: "job", Value: "test"},
							{Name: "instance", Value: "localhost:9090"},
							{Name: "namespace", Value: "wind"},
						},
						Samples: []prompb.Sample{
							{Value: 2, Timestamp: time.Now().UnixMilli()},
						},
					},
				},
			}

			buf, err := marshal(wr)
			Expect(err).NotTo(HaveOccurred())

			var req fh.Request
			var resp fh.Response
			req.SetRequestURI(endpoint)
			req.Header.SetMethod(fh.MethodPost)
			req.Header.Set("Content-Encoding", "snappy")
			req.Header.Set("Content-Type", "application/x-protobuf")
			req.SetBody(buf)

			err = fh.DoTimeout(&req, &resp, cfg.Timeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode()).To(Equal(fh.StatusOK))

			Eventually(func() int {
				receivedMu.Lock()
				l := len(receivedReqs)
				receivedMu.Unlock()
				return l
			}, 5*time.Second, 100*time.Millisecond).Should(Equal(2), "expected exactly two forwarded requests")

			// snapshot without holding the lock while asserting
			receivedMu.Lock()
			reqs := make([]receivedRequest, len(receivedReqs))
			copy(reqs, receivedReqs)
			receivedMu.Unlock()

			// collect headers for assertions
			orgIDs := []string{
				reqs[0].Header.Get("X-Scope-OrgID"),
				reqs[1].Header.Get("X-Scope-OrgID"),
			}
			// Order can be nondeterministic; assert as a set.
			Expect(orgIDs).To(ConsistOf(
				cfg.Tenant.Prefix+"solar-org",
				cfg.Tenant.Prefix+"wind",
			))

			// For each forwarded request: decode body and assert labels
			for i, r := range reqs {
				buf, err := unmarshal(r.Body)
				Expect(err).NotTo(HaveOccurred(), "failed to unmarshal body of forwarded request %d", i)

				lbls := buf.Timeseries[0].Labels

				// tenant label must be set and equal to the header org id
				tenantLabelName := cfg.Tenant.TenantLabel // e.g. "tenant"
				val, ok := getLabel(lbls, tenantLabelName)
				Expect(ok).To(BeTrue(), "missing tenant label %q in forwarded request %d", tenantLabelName, i)
				Expect(val).To(Equal(r.Header.Get("X-Scope-OrgID")), "tenant label must match header in forwarded request %d", i)

				// original labels are preserved (pick a couple of canaries)
				job, ok := getLabel(lbls, "job")
				Expect(ok).To(BeTrue(), "missing 'job' label in forwarded request %d", i)
				Expect(job).To(Equal("test"))

				// expected meta header still present
				Expect(r.Header.Get("X-Prometheus-Remote-Write-Version")).To(Equal("0.1.0"))
				Expect(r.Header.Get("Content-Encoding")).To(Equal("snappy"))
			}

			clearRequests() // call when not holding the lock
		})
	})
})
