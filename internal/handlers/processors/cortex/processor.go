package cortex

import (
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/go-logr/logr"
	"github.com/gogo/protobuf/proto"
	"github.com/golang/snappy"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/prometheus/prometheus/prompb"
	fh "github.com/valyala/fasthttp"

	"github.com/peak-scale/observability-tenancy/internal/config"
	"github.com/peak-scale/observability-tenancy/internal/handlers/handler"
	"github.com/peak-scale/observability-tenancy/internal/metrics"
	"github.com/peak-scale/observability-tenancy/internal/stores"
)

func NewCortexProcessor(
	log logr.Logger,
	c config.Config,
	store *stores.NamespaceStore,
	metrics *metrics.ProxyRecorder,
) *handler.Handler {
	return handler.NewHandler(log, c, store, metrics, "cortex-tenant", process, fillRequestHeaders)
}

// Process Loki Stream for each request.
func process(processor *handler.Handler, req *fh.Request) (map[string][]byte, error) {
	wrReqIn, err := unmarshal(req.Body())
	if err != nil {
		return nil, err
	}

	if len(wrReqIn.Timeseries) == 0 {
		// If there's metadata - just accept the request and drop it
		if len(wrReqIn.Metadata) > 0 {
			return nil, errors.New("no streams found in the request")
		}
	}

	m, err := createTenantRequests(processor, wrReqIn)
	if err != nil {
		return nil, err
	}

	return m, nil
}

func unmarshal(b []byte) (*prompb.WriteRequest, error) {
	decoded, err := snappy.Decode(nil, b)
	if err != nil {
		return nil, errors.Wrap(err, "Unable to unpack Snappy")
	}

	req := &prompb.WriteRequest{}
	if err = proto.Unmarshal(decoded, req); err != nil {
		return nil, errors.Wrap(err, "Unable to unmarshal protobuf")
	}

	return req, nil
}

//nolint:unused
func marshal(wr *prompb.WriteRequest) (bufOut []byte, err error) {
	b := make([]byte, wr.Size())

	// Marshal to Protobuf
	if _, err = wr.MarshalTo(b); err != nil {
		return
	}

	// Compress with Snappy
	return snappy.Encode(nil, b), nil
}

func createTenantRequests(h *handler.Handler, req *prompb.WriteRequest) (r map[string][]byte, err error) {
	m := sync.Map{}

	var (
		wg       sync.WaitGroup
		errMutex sync.Mutex
		firstErr error
	)

	for _, ts := range req.Timeseries {
		wg.Add(1)

		go func(ts prompb.TimeSeries) {
			defer wg.Done()

			tenant, err := processTimeseries(h, &ts)
			if err != nil {
				errMutex.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errMutex.Unlock()

				return
			}

			// Tenant & Total
			h.Metrics.MetricTimeseriesReceived.WithLabelValues(tenant).Inc()
			h.Metrics.MetricTimeseriesReceived.WithLabelValues("").Inc()

			v, _ := m.LoadOrStore(tenant, &prompb.WriteRequest{Timeseries: []prompb.TimeSeries{}})

			req, ok := v.(*prompb.WriteRequest)
			if !ok {
				h.Log.Error(fmt.Errorf("expected *prompb.WriteRequest, got %T", v), "Unable to marshal tenant request")

				return
			}

			req.Timeseries = append(req.Timeseries, ts)
		}(ts)
	}

	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}

	r = make(map[string][]byte)

	m.Range(func(tenant, pushReq interface{}) bool {
		writeReq, ok := pushReq.(*prompb.WriteRequest)
		if !ok {
			h.Log.Error(fmt.Errorf("expected *prompb.WriteRequest, got %T", tenant), "Unable to marshal tenant request")

			return true
		}

		buf, err := marshal(writeReq)
		if err != nil {
			h.Log.Error(err, "failed to marshal request")

			return true
		}

		strTenant, ok := tenant.(string)
		if !ok {
			h.Log.Error(fmt.Errorf("expected tenant to be a string, got %T", tenant), "Unable to marshal tenant request")

			return false
		}

		r[strTenant] = buf

		return true
	})

	return r, nil
}

func removeOrdered(slice []prompb.Label, s int) []prompb.Label {
	return append(slice[:s], slice[s+1:]...)
}

func processTimeseries(h *handler.Handler, ts *prompb.TimeSeries) (tenant string, err error) {
	var (
		namespace string
		idx       int
	)

	for i, l := range ts.Labels {
		for _, configuredLabel := range h.Config.Tenant.Labels {
			if l.Name == configuredLabel {
				h.Log.Info("found", "label", configuredLabel, "value", l.Value)

				namespace = l.Value
				idx = i

				break
			}
		}
	}

	tenant = h.Store.GetOrg(namespace)

	if tenant == "" {
		if h.Config.Tenant.Default == "" {
			return "", fmt.Errorf("label(s): {'%s'} not found", strings.Join(h.Config.Tenant.Labels, "','"))
		}

		return h.Config.Tenant.Default, nil
	}

	if h.Config.Tenant.LabelRemove {
		// Order is important. See:
		// https://github.com/thanos-io/thanos/issues/6452
		// https://github.com/prometheus/prometheus/issues/11505
		ts.Labels = removeOrdered(ts.Labels, idx)
	}

	return
}

func fillRequestHeaders(
	clientIP net.Addr,
	reqID uuid.UUID,
	_ string,
	req *fh.Request,
) {
	req.Header.Set("X-Prometheus-Remote-Write-Version", "0.1.0")
	req.Header.Set("X-Cortex-Tenant-Client", clientIP.String())
	req.Header.Set("X-Cortex-Tenant-ReqID", reqID.String())
}
