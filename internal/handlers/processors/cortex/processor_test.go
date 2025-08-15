package cortex

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/stdr"
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

var baseLogger = stdr.New(log.New(GinkgoWriter, "[TEST] ", log.LstdFlags))

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

	//metric = metrics.MustMakeRecorder("cortex") // or a mock recorder
	//
	//clearRequests := func() {
	//	receivedMu.Lock()
	//	receivedReqs = nil
	//	receivedMu.Unlock()
	//}

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())
		ctx = logr.NewContext(ctx, baseLogger)
		log := logr.FromContextOrDiscard(ctx)
		ctx, cancel = context.WithCancel(context.Background())

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

		// Initialize configuration for the processor.
		// Ensure cfg.Target points to fakeTarget.URL.
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
				Default:            "default",
				Prefix:             "test-",
				PrefixPreferSource: false,
				TenantLabel:        "tenant",
			},
		}

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
			Labels: []string{
				"namespace",
				"target_namespace",
			},
			Header:                "X-Scope-OrgID",
			Default:               "default",
			Prefix:                "test-",
			PrefixPreferSource:    false,
			SetNamespaceAsDefault: true,
		})

		// Create the processor.
		// Start the processor webserver in a separate goroutine.
		proc = NewCortexProcessor(log, cfg, store, metric)

		log.Info("Starting Server")
		go func() {
			if err := proc.Start(ctx); err != nil && err != http.ErrServerClosed {
				log.Error(err, "processor failed")
			}
			log.Info("Starting Start")
		}()

		// Allow some time for the processor to start.
		time.Sleep(500 * time.Millisecond)
	})

	AfterEach(func() {
		cancel()
		fakeTarget.Close()
	})

	It("should correctly set headers", func() {
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
			req.SetRequestURI("http://127.0.0.1:31001/push")
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
				defer receivedMu.Unlock()
				if len(receivedReqs) == 0 {
					return false
				}
				received = receivedReqs[0]
				return true
			}, 5*time.Second, 200*time.Millisecond).Should(BeTrue())

			// Verify that the forwarded request contains the expected header.
			receivedMu.Lock()
			defer receivedMu.Unlock()
			Expect(received.Header).To(HaveKeyWithValue(http.CanonicalHeaderKey("X-Scope-OrgID"), []string{"test-default"}))
			Expect(received.Header).To(HaveKeyWithValue(http.CanonicalHeaderKey("X-Prometheus-Remote-Write-Version"), []string{"0.1.0"}))
			Expect(received.Header).To(HaveKeyWithValue(http.CanonicalHeaderKey("Content-Encoding"), []string{"snappy"}))

			//clearRequests()
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
			req.SetRequestURI("http://127.0.0.1:31001/push")
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
				defer receivedMu.Unlock()
				if len(receivedReqs) == 0 {
					return false
				}
				received = receivedReqs[0]
				return true
			}, 5*time.Second, 200*time.Millisecond).Should(BeTrue())

			// Verify that the forwarded request contains the expected header.
			receivedMu.Lock()
			defer receivedMu.Unlock()
			Expect(received.Header).To(HaveKeyWithValue(http.CanonicalHeaderKey("X-Scope-OrgID"), []string{cfg.Tenant.Prefix + "solar-org"}))
			Expect(received.Header).To(HaveKeyWithValue(http.CanonicalHeaderKey("X-Prometheus-Remote-Write-Version"), []string{"0.1.0"}))

			//clearRequests()
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
			req.SetRequestURI("http://127.0.0.1:31001/push")
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
				defer receivedMu.Unlock()
				if len(receivedReqs) == 0 {
					return false
				}
				received = receivedReqs[0]
				return true
			}, 5*time.Second, 200*time.Millisecond).Should(BeTrue())

			// Verify that the forwarded request contains the expected header.
			receivedMu.Lock()
			defer receivedMu.Unlock()
			Expect(received.Header).To(HaveKeyWithValue(http.CanonicalHeaderKey("X-Scope-OrgID"), []string{cfg.Tenant.Prefix + "wind"}))
			Expect(received.Header).To(HaveKeyWithValue(http.CanonicalHeaderKey("X-Prometheus-Remote-Write-Version"), []string{"0.1.0"}))

			//clearRequests()
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
			req.SetRequestURI("http://127.0.0.1:31001/push")
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
				defer receivedMu.Unlock()
				if len(receivedReqs) == 0 {
					return false
				}
				received = receivedReqs[0]
				return true
			}, 5*time.Second, 200*time.Millisecond).Should(BeTrue())

			// Verify that the forwarded request contains the expected header.
			receivedMu.Lock()
			defer receivedMu.Unlock()
			Expect(received.Header).To(HaveKeyWithValue(http.CanonicalHeaderKey("X-Scope-OrgID"), []string{cfg.Tenant.Prefix + "green-org"}))
			Expect(received.Header).To(HaveKeyWithValue(http.CanonicalHeaderKey("X-Prometheus-Remote-Write-Version"), []string{"0.1.0"}))

			//clearRequests()
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
			req.SetRequestURI("http://127.0.0.1:31001/push")
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
				defer receivedMu.Unlock()
				if len(receivedReqs) == 0 {
					return false
				}
				received = receivedReqs[0]
				return true
			}, 5*time.Second, 200*time.Millisecond).Should(BeTrue())

			// Verify that the forwarded request contains the expected header.
			receivedMu.Lock()
			defer receivedMu.Unlock()
			Expect(received.Header).To(HaveKeyWithValue(http.CanonicalHeaderKey("X-Scope-OrgID"), []string{cfg.Tenant.Prefix + cfg.Tenant.Default}))
			Expect(received.Header).To(HaveKeyWithValue(http.CanonicalHeaderKey("X-Prometheus-Remote-Write-Version"), []string{"0.1.0"}))

			//clearRequests()
		})

	})
})
