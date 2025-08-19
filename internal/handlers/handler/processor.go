package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-logr/logr"
	"github.com/golang/snappy"
	"github.com/google/uuid"
	me "github.com/hashicorp/go-multierror"
	fh "github.com/valyala/fasthttp"

	"github.com/peak-scale/observability-tenancy/internal/config"
	"github.com/peak-scale/observability-tenancy/internal/handlers/middleware"
	"github.com/peak-scale/observability-tenancy/internal/metrics"
	"github.com/peak-scale/observability-tenancy/internal/stores"
)

type Handler struct {
	Config  config.Config
	Srv     *fh.Server
	Cli     *fh.Client
	Log     logr.Logger
	Store   *stores.NamespaceStore
	Metrics *metrics.ProxyRecorder
	Auth    struct {
		EgressHeader []byte
	}
	Closing uint32
	// Process Payload
	process    func(*Handler, *fh.Request) (map[string][]byte, error)
	headerFunc func(clientIP net.Addr, reqID uuid.UUID, tenant string, req *fh.Request)
}

// Initialize a new Processor.
func NewHandler(
	log logr.Logger,
	c config.Config,
	store *stores.NamespaceStore,
	metrics *metrics.ProxyRecorder,
	name string,
	processFunc func(*Handler, *fh.Request) (map[string][]byte, error),
	headerFunc func(clientIP net.Addr, reqID uuid.UUID, tenant string, req *fh.Request),
) *Handler {
	p := &Handler{
		Config:     c,
		Log:        log,
		Store:      store,
		Metrics:    metrics,
		process:    processFunc,
		headerFunc: headerFunc,
	}

	p.Srv = &fh.Server{
		Name:               name,
		Handler:            middleware.LoggerMiddleware(log)(p.handle),
		MaxRequestBodySize: 8 * 1024 * 1024,
		ReadTimeout:        c.Timeout,
		WriteTimeout:       c.Timeout,
		IdleTimeout:        60 * time.Second,
		Concurrency:        c.Concurrency,
	}

	p.Cli = &fh.Client{
		Name:               name,
		ReadTimeout:        c.Timeout,
		WriteTimeout:       c.Timeout,
		MaxConnWaitTimeout: 1 * time.Second,
		MaxConnsPerHost:    c.MaxConnsPerHost,
	}

	if c.Backend.Auth.Username != "" {
		authString := []byte(fmt.Sprintf("%s:%s", c.Backend.Auth.Username, c.Backend.Auth.Password))
		p.Auth.EgressHeader = []byte("Basic " + base64.StdEncoding.EncodeToString(authString))
	}

	// For testing
	if c.PipeOut != nil {
		p.Cli.Dial = func(_ string) (net.Conn, error) {
			return c.PipeOut.Dial()
		}
	}

	return p
}

// Start implements the Runnable interface.
// It should block until the context is done (i.e. shutdown is triggered).
func (p *Handler) Start(ctx context.Context) error {
	p.Log.Info("starting", "name", p.Srv.Name, "addr", p.Config.Bind)

	// Run your processor (blocking call)
	if err := p.run(); err != nil {
		return fmt.Errorf("failed to run processor: %w", err)
	}

	// Wait for shutdown signal via the context
	<-ctx.Done()

	// Perform any graceful shutdown/cleanup
	if err := p.close(); err != nil {
		return fmt.Errorf("failed to shutdown processor: %w", err)
	}

	return nil
}

func (p *Handler) Health() error {
	// If the processor is shutting down, return an error
	if atomic.LoadUint32(&p.Closing) == 1 {
		return fmt.Errorf("processor is shutting down")
	}

	// Here you can add additional health checks (e.g., backend reachability)
	return nil
}

// Handles all the requests.
func (p *Handler) handle(ctx *fh.RequestCtx) {
	if bytes.Equal(ctx.Path(), []byte("/alive")) {
		if atomic.LoadUint32(&p.Closing) == 1 {
			ctx.SetStatusCode(fh.StatusServiceUnavailable)
		}

		return
	}

	if !bytes.Equal(ctx.Method(), []byte("POST")) {
		p.Log.Error(
			fmt.Errorf("invalid HTTP method"),
			"Expecting POST",
			"method", string(ctx.Method()),
		)

		ctx.Error("Expecting POST", fh.StatusBadRequest)

		return
	}

	if !bytes.Equal(ctx.Path(), []byte("/push")) {
		p.Log.Error(
			fmt.Errorf("invalid request path"),
			"Expecting /push",
			"path", string(ctx.Path()),
		)

		ctx.SetStatusCode(fh.StatusNotFound)

		return
	}

	p.Metrics.MetricTimeseriesBatchesReceivedBytes.Observe(float64(ctx.Request.Header.ContentLength()))
	p.Metrics.MetricTimeseriesBatchesReceived.Inc()

	data, err := p.process(p, &ctx.Request)
	if err != nil {
		p.Log.Error(err, "Processing function failed")
		ctx.Error("Internal Server Error", p.Config.HTTPErrorCode)

		return
	}

	reqID, _ := uuid.NewRandom()

	var errs *me.Error

	results := p.dispatch(ctx.RemoteAddr(), reqID, data)

	code, body := fh.StatusOK, []byte("Ok")

	// Return 204 regardless of errors if AcceptAll is enabled
	if p.Config.Tenant.AcceptAll {
		code, body = fh.StatusOK, nil

		goto out
	}

	for _, r := range results {
		p.Metrics.MetricTimeseriesRequests.WithLabelValues(r.Tenant).Inc()

		if r.Error != nil {
			p.Metrics.MetricTimeseriesRequestErrors.WithLabelValues(r.Tenant).Inc()
			errs = me.Append(errs, r.Error)
			p.Log.Error(r.Error, "request failed", "source", ctx.RemoteAddr())

			continue
		}

		if r.Code < 200 || r.Code >= 300 {
			p.Log.V(5).Info("src=%s req_id=%s HTTP code %d (%s)", ctx.RemoteAddr(), reqID, r.Code, string(r.Body))
		}

		if r.Code > code {
			code, body = r.Code, r.Body
		}

		p.Metrics.MetricTimeseriesRequestDurationSeconds.WithLabelValues(strconv.Itoa(r.Code), r.Tenant).Observe(r.Duration)
	}

	if errs.ErrorOrNil() != nil {
		ctx.Error(errs.Error(), p.Config.HTTPErrorCode)

		return
	}

out:
	ctx.SetBody(body)
	ctx.SetStatusCode(code)
}

// Dispatch Request for each tenant.
func (p *Handler) dispatch(
	clientIP net.Addr,
	reqID uuid.UUID,
	m map[string][]byte,
) (res []DispatchResult) {
	var wg sync.WaitGroup

	res = make([]DispatchResult, len(m))

	i := 0

	for tenant, buf := range m {
		wg.Add(1)

		go func(idx int, tenant string, buf []byte) {
			defer wg.Done()

			r := p.send(clientIP, reqID, tenant, buf)
			res[idx] = r
		}(i, tenant, buf)

		i++
	}

	wg.Wait()

	return
}

// Send a request to the backend.
func (p *Handler) send(
	clientIP net.Addr,
	reqID uuid.UUID,
	tenant string,
	body []byte,
) (r DispatchResult) {
	start := time.Now()
	r.Tenant = tenant

	req := fh.AcquireRequest()
	resp := fh.AcquireResponse()

	defer func() {
		fh.ReleaseRequest(req)
		fh.ReleaseResponse(resp)
	}()

	// Process headers
	if p.headerFunc != nil {
		p.headerFunc(clientIP, reqID, tenant, req)
	}

	req.Header.Set("Content-Encoding", "snappy")
	req.Header.Set("Content-Type", "application/x-protobuf")

	if p.Config.Tenant.SetHeader {
		req.Header.Set(p.Config.Tenant.Header, tenant)
	}

	req.SetRequestURI(p.Config.Backend.URL)

	// Authentication Headers
	if p.Auth.EgressHeader != nil {
		req.Header.SetBytesV("Authorization", p.Auth.EgressHeader)
	}

	req.Header.SetMethod(fh.MethodPost)
	req.SetBody(snappy.Encode(nil, body))

	if err := p.Cli.DoTimeout(req, resp, p.Config.Timeout); err != nil {
		return DispatchResult{Error: err}
	}

	r.Code = resp.Header.StatusCode()
	r.Body = make([]byte, len(resp.Body()))
	copy(r.Body, resp.Body())
	r.Duration = time.Since(start).Seconds()

	return DispatchResult{Code: resp.StatusCode(), Body: resp.Body(), Duration: time.Since(start).Seconds(), Tenant: tenant}
}

func (p *Handler) run() (err error) {
	l, err := net.Listen("tcp", p.Config.Bind)
	if err != nil {
		return err
	}

	go func() {
		if err := p.Srv.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
			p.Log.Error(err, "failed to serve HTTP")

			return
		}
	}()

	return
}

// Close shuts down the server (common behavior for all processors).
func (p *Handler) close() (err error) {
	// Signal that we're shutting down
	atomic.StoreUint32(&p.Closing, 1)
	// Let healthcheck detect that we're offline
	time.Sleep(p.Config.TimeoutShutdown)
	// Shutdown
	return p.Srv.Shutdown()
}
