package loki

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/grafana/loki/v3/pkg/logproto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	fh "github.com/valyala/fasthttp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/peak-scale/observability-tenancy/internal/config"
	"github.com/peak-scale/observability-tenancy/internal/handlers/handler"
	"github.com/peak-scale/observability-tenancy/internal/meta"
	"github.com/peak-scale/observability-tenancy/internal/metrics"
	"github.com/peak-scale/observability-tenancy/internal/stores"
)

var _ = Describe("Processor Forwarding", func() {
	var (
		proc           *handler.Handler
		fakeTarget     *httptest.Server
		receivedMu     sync.Mutex
		receivedHeader http.Header
		ctx            context.Context
		cancel         context.CancelFunc
		cfg            config.Config
		store          *stores.NamespaceStore
		metric         *metrics.ProxyRecorder
	)

	metric = metrics.MustMakeRecorder("loki_test") // or a mock recorder

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())
		log, _ := logr.FromContext(ctx)

		log.Info("Starting Server")

		// Create a fake target server that records request headers.
		fakeTarget = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedMu.Lock()
			receivedHeader = r.Header.Clone()
			receivedMu.Unlock()
			w.Header().Set("Connection", "close")
			w.WriteHeader(http.StatusOK)
		}))

		// Initialize configuration for the processor.
		cfg = config.Config{
			Bind: "0.0.0.0:31002",
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
		proc = NewLokiProcessor(log, cfg, store, metric)

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
			wr := &logproto.PushRequest{
				Streams: []logproto.Stream{
					{
						Labels: "{app=\"pyroscope\", instance=\"observability-system/pyroscope-0:pyroscope\", job=\"observability-system/pyroscope\", namespace=\"observability-system\", pod=\"pyroscope-0\"}",
						Entries: []logproto.Entry{
							{Timestamp: time.Now(), Line: "test log"},
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
			req.SetRequestURI("http://127.0.0.1:31002/push")
			req.Header.SetMethod(fh.MethodPost)
			req.Header.Set("Content-Encoding", "snappy")
			req.Header.Set("Content-Type", "application/x-protobuf")
			req.SetBody(buf)

			// Send the request using fasthttp.
			err = fh.DoTimeout(&req, &resp, cfg.Timeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode()).To(Equal(fh.StatusOK))

			// Wait until the fake target receives the forwarded request.
			Eventually(func() http.Header {
				receivedMu.Lock()
				defer receivedMu.Unlock()
				return receivedHeader
			}, 5*time.Second, 200*time.Millisecond).ShouldNot(BeEmpty())

			// Verify that the forwarded request contains the expected header.
			receivedMu.Lock()
			defer receivedMu.Unlock()
			Expect(receivedHeader).To(HaveKeyWithValue(http.CanonicalHeaderKey("X-Scope-OrgID"), []string{"test-default"}))
			Expect(receivedHeader).To(HaveKeyWithValue(http.CanonicalHeaderKey("Content-Encoding"), []string{"snappy"}))
		})

		By("proxy correct tenant (solar)", func() {
			wr := &logproto.PushRequest{
				Streams: []logproto.Stream{
					{
						Labels: "{app=\"pyroscope\", instance=\"observability-system/pyroscope-0:pyroscope\", job=\"observability-system/pyroscope\", namespace=\"solar\", pod=\"pyroscope-0\"}",
						Entries: []logproto.Entry{
							{Timestamp: time.Now(), Line: "test log"},
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
			req.SetRequestURI("http://127.0.0.1:31002/push")
			req.Header.SetMethod(fh.MethodPost)
			req.Header.Set("Content-Encoding", "snappy")
			req.Header.Set("Content-Type", "application/x-protobuf")
			req.SetBody(buf)

			// Send the request using fasthttp.
			err = fh.DoTimeout(&req, &resp, cfg.Timeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode()).To(Equal(fh.StatusOK))

			// Wait until the fake target receives the forwarded request.
			Eventually(func() http.Header {
				receivedMu.Lock()
				defer receivedMu.Unlock()
				return receivedHeader
			}, 5*time.Second, 200*time.Millisecond).ShouldNot(BeEmpty())

			// Verify that the forwarded request contains the expected header.
			receivedMu.Lock()
			defer receivedMu.Unlock()
			Expect(receivedHeader).To(HaveKeyWithValue(http.CanonicalHeaderKey("X-Scope-OrgID"), []string{cfg.Tenant.Prefix + "solar-org"}))
		})

		By("proxy correct tenant (wind)", func() {
			wr := &logproto.PushRequest{
				Streams: []logproto.Stream{
					{
						Labels: "{app=\"pyroscope\", instance=\"observability-system/pyroscope-0:pyroscope\", job=\"observability-system/pyroscope\", namespace=\"wind\", pod=\"pyroscope-0\"}",
						Entries: []logproto.Entry{
							{Timestamp: time.Now(), Line: "test log"},
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
			req.SetRequestURI("http://127.0.0.1:31002/push")
			req.Header.SetMethod(fh.MethodPost)
			req.Header.Set("Content-Encoding", "snappy")
			req.Header.Set("Content-Type", "application/x-protobuf")
			req.SetBody(buf)

			// Send the request using fasthttp.
			err = fh.DoTimeout(&req, &resp, cfg.Timeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode()).To(Equal(fh.StatusOK))

			// Wait until the fake target receives the forwarded request.
			Eventually(func() http.Header {
				receivedMu.Lock()
				defer receivedMu.Unlock()
				return receivedHeader
			}, 5*time.Second, 200*time.Millisecond).ShouldNot(BeEmpty())

			// Verify that the forwarded request contains the expected header.
			receivedMu.Lock()
			defer receivedMu.Unlock()
			Expect(receivedHeader).To(HaveKeyWithValue(http.CanonicalHeaderKey("X-Scope-OrgID"), []string{cfg.Tenant.Prefix + "wind"}))
		})

		By("proxy correct tenant (oil)", func() {
			wr := &logproto.PushRequest{
				Streams: []logproto.Stream{
					{
						Labels: "{app=\"pyroscope\", instance=\"observability-system/pyroscope-0:pyroscope\", job=\"observability-system/pyroscope\", target_namespace=\"green\", pod=\"pyroscope-0\"}",
						Entries: []logproto.Entry{
							{Timestamp: time.Now(), Line: "test log"},
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
			req.SetRequestURI("http://127.0.0.1:31002/push")
			req.Header.SetMethod(fh.MethodPost)
			req.Header.Set("Content-Encoding", "snappy")
			req.Header.Set("Content-Type", "application/x-protobuf")
			req.SetBody(buf)

			// Send the request using fasthttp.
			err = fh.DoTimeout(&req, &resp, cfg.Timeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode()).To(Equal(fh.StatusOK))

			// Wait until the fake target receives the forwarded request.
			Eventually(func() http.Header {
				receivedMu.Lock()
				defer receivedMu.Unlock()
				return receivedHeader
			}, 5*time.Second, 200*time.Millisecond).ShouldNot(BeEmpty())

			// Verify that the forwarded request contains the expected header.
			receivedMu.Lock()
			defer receivedMu.Unlock()
			Expect(receivedHeader).To(HaveKeyWithValue(http.CanonicalHeaderKey("X-Scope-OrgID"), []string{cfg.Tenant.Prefix + "green-org"}))
		})

		By("default on no match", func() {
			wr := &logproto.PushRequest{
				Streams: []logproto.Stream{
					{
						Labels: "{app=\"pyroscope\", instance=\"observability-system/pyroscope-0:pyroscope\", job=\"observability-system/pyroscope\", target_namespace=\"oil-prod\", pod=\"pyroscope-0\"}",
						Entries: []logproto.Entry{
							{Timestamp: time.Now(), Line: "test log"},
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
			req.SetRequestURI("http://127.0.0.1:31002/push")
			req.Header.SetMethod(fh.MethodPost)
			req.Header.Set("Content-Encoding", "snappy")
			req.Header.Set("Content-Type", "application/x-protobuf")
			req.SetBody(buf)

			// Send the request using fasthttp.
			err = fh.DoTimeout(&req, &resp, cfg.Timeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode()).To(Equal(fh.StatusOK))

			// Wait until the fake target receives the forwarded request.
			Eventually(func() http.Header {
				receivedMu.Lock()
				defer receivedMu.Unlock()
				return receivedHeader
			}, 5*time.Second, 200*time.Millisecond).ShouldNot(BeEmpty())

			// Verify that the forwarded request contains the expected header.
			receivedMu.Lock()
			defer receivedMu.Unlock()
			Expect(receivedHeader).To(HaveKeyWithValue(http.CanonicalHeaderKey("X-Scope-OrgID"), []string{cfg.Tenant.Prefix + cfg.Tenant.Default}))
		})

	})
})
