package loki

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
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

var endpoint = "http://127.0.0.1:31002/push"

// labelHas checks that `{...}` label set contains key="value" (order-agnostic).
func labelHas(set, key, value string) bool {
	// match at start `{` or after `,`, allow optional spaces, then key="value", then `,` or `}`
	pat := fmt.Sprintf(`(^\{|,)\s*%s="%s"(\s*,|})`, regexp.QuoteMeta(key), regexp.QuoteMeta(value))
	ok, _ := regexp.MatchString(pat, set)
	return ok
}

var _ = Describe("Processor Forwarding (Loki)", func() {
	var (
		proc       *handler.Handler
		fakeTarget *httptest.Server
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
		receivedReqs = receivedReqs[:0] // or: receivedReqs = nil
	}

	metric = metrics.MustMakeRecorder("loki_test") // or a mock recorder

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

	It("should correctly set headers", func() {
		ctx, cancel = context.WithCancel(context.Background())
		log, _ := logr.FromContext(ctx)

		log.Info("Starting Server")

		// Create a fake target server that records request headers.
		fakeTarget = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
				SetHeader:          true,
				Default:            "default",
				Prefix:             "test-",
				TenantLabel:        "tenant",
				PrefixPreferSource: false,
			},
		}

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
			req.SetRequestURI(endpoint)
			req.Header.SetMethod(fh.MethodPost)
			req.Header.Set("Content-Encoding", "snappy")
			req.Header.Set("Content-Type", "application/x-protobuf")
			req.SetBody(buf)

			// Send the request using fasthttp.
			err = fh.DoTimeout(&req, &resp, cfg.Timeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode()).To(Equal(fh.StatusOK))

			var received receivedRequest
			Eventually(func() bool {
				if len(receivedReqs) == 0 {
					return false
				}
				received = receivedReqs[0]
				return true
			}, 5*time.Second, 200*time.Millisecond).Should(BeTrue())

			Expect(received.Header).To(HaveKeyWithValue(http.CanonicalHeaderKey("X-Scope-OrgID"), []string{"test-default"}))
			Expect(received.Header).To(HaveKeyWithValue(http.CanonicalHeaderKey("Content-Encoding"), []string{"snappy"}))

			clearRequests()
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
			req.SetRequestURI(endpoint)
			req.Header.SetMethod(fh.MethodPost)
			req.Header.Set("Content-Encoding", "snappy")
			req.Header.Set("Content-Type", "application/x-protobuf")
			req.SetBody(buf)

			// Send the request using fasthttp.
			err = fh.DoTimeout(&req, &resp, cfg.Timeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode()).To(Equal(fh.StatusOK))

			var received receivedRequest
			Eventually(func() bool {
				if len(receivedReqs) == 0 {
					return false
				}
				received = receivedReqs[0]
				return true
			}, 5*time.Second, 200*time.Millisecond).Should(BeTrue())

			Expect(received.Header).To(HaveKeyWithValue(http.CanonicalHeaderKey("X-Scope-OrgID"), []string{cfg.Tenant.Prefix + "solar-org"}))

			clearRequests()
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
			req.SetRequestURI(endpoint)
			req.Header.SetMethod(fh.MethodPost)
			req.Header.Set("Content-Encoding", "snappy")
			req.Header.Set("Content-Type", "application/x-protobuf")
			req.SetBody(buf)

			// Send the request using fasthttp.
			err = fh.DoTimeout(&req, &resp, cfg.Timeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode()).To(Equal(fh.StatusOK))

			var received receivedRequest
			Eventually(func() bool {
				if len(receivedReqs) == 0 {
					return false
				}
				received = receivedReqs[0]
				return true
			}, 5*time.Second, 200*time.Millisecond).Should(BeTrue())

			Expect(received.Header).To(HaveKeyWithValue(http.CanonicalHeaderKey("X-Scope-OrgID"), []string{cfg.Tenant.Prefix + "wind"}))

			clearRequests()
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
			req.SetRequestURI(endpoint)
			req.Header.SetMethod(fh.MethodPost)
			req.Header.Set("Content-Encoding", "snappy")
			req.Header.Set("Content-Type", "application/x-protobuf")
			req.SetBody(buf)

			// Send the request using fasthttp.
			err = fh.DoTimeout(&req, &resp, cfg.Timeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode()).To(Equal(fh.StatusOK))

			var received receivedRequest
			Eventually(func() bool {
				if len(receivedReqs) == 0 {
					return false
				}
				received = receivedReqs[0]
				return true
			}, 5*time.Second, 200*time.Millisecond).Should(BeTrue())

			Expect(received.Header).To(HaveKeyWithValue(http.CanonicalHeaderKey("X-Scope-OrgID"), []string{cfg.Tenant.Prefix + "green-org"}))

			clearRequests()
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
			req.SetRequestURI(endpoint)
			req.Header.SetMethod(fh.MethodPost)
			req.Header.Set("Content-Encoding", "snappy")
			req.Header.Set("Content-Type", "application/x-protobuf")
			req.SetBody(buf)

			// Send the request using fasthttp.
			err = fh.DoTimeout(&req, &resp, cfg.Timeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode()).To(Equal(fh.StatusOK))

			var received receivedRequest
			Eventually(func() bool {
				if len(receivedReqs) == 0 {
					return false
				}
				received = receivedReqs[0]
				return true
			}, 5*time.Second, 200*time.Millisecond).Should(BeTrue())

			Expect(received.Header).To(HaveKeyWithValue(http.CanonicalHeaderKey("X-Scope-OrgID"), []string{cfg.Tenant.Prefix + cfg.Tenant.Default}))

			clearRequests()
		})
		By("sending two series for different tenants in a single request", func() {
			wr := &logproto.PushRequest{
				Streams: []logproto.Stream{
					{
						Labels: `{app="pyroscope", instance="observability-system/pyroscope-0:pyroscope", job="observability-system/pyroscope", target_namespace="oil-prod", pod="pyroscope-0"}`,
						Entries: []logproto.Entry{
							{Timestamp: time.Now(), Line: "test log"},
						},
					},
					{
						Labels: `{app="pyroscope", instance="observability-system/pyroscope-0:pyroscope", job="observability-system/pyroscope", target_namespace="wind", pod="pyroscope-0"}`,
						Entries: []logproto.Entry{
							{Timestamp: time.Now(), Line: "test log"},
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
		})

		Eventually(func() int {
			return len(receivedReqs)
		}, 5*time.Second, 100*time.Millisecond).Should(Equal(2), "expected exactly two forwarded requests")

		// snapshot to avoid races while asserting
		reqs := make([]receivedRequest, len(receivedReqs))
		copy(reqs, receivedReqs)

		// Header orgIDs (order-agnostic)
		orgIDs := []string{
			reqs[0].Header.Get("X-Scope-OrgID"),
			reqs[1].Header.Get("X-Scope-OrgID"),
		}
		Expect(orgIDs).To(ConsistOf(
			cfg.Tenant.Prefix+cfg.Tenant.Default,
			cfg.Tenant.Prefix+"wind",
		))

		tenantLabelName := cfg.Tenant.TenantLabel // e.g. "tenant"
		if tenantLabelName == "" {
			tenantLabelName = "tenant" // fallback if config leaves it empty
		}

		for i, r := range reqs {
			// Decode forwarded body (accept framed or block snappy)
			pr, err := unmarshal(r.Body)
			Expect(err).NotTo(HaveOccurred(), "failed to decode forwarded loki body for request %d", i)
			Expect(pr.Streams).To(HaveLen(1), "forwarded request %d should contain exactly one stream", i)

			lblset := pr.Streams[0].Labels
			expectedTenant := r.Header.Get("X-Scope-OrgID")

			// tenant label injected and equals header
			Expect(labelHas(lblset, tenantLabelName, expectedTenant)).
				To(BeTrue(), "missing or wrong %q label in request %d; labels=%s", tenantLabelName, i, lblset)

			// original labels preserved
			Expect(labelHas(lblset, "app", "pyroscope")).To(BeTrue(), "missing original app label in request %d; labels=%s", i, lblset)

			// meta headers still present
			Expect(strings.ToLower(r.Header.Get("Content-Encoding"))).To(Equal("snappy"))
			Expect(r.Header.Get("Content-Type")).To(Equal("application/x-protobuf"))
		}

	})
})
