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

// Process Cortex Request for each request.
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

	m, err := createTenantRequests(processor, req, wrReqIn)
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

func createTenantRequests(h *handler.Handler, req *fh.Request, wr *prompb.WriteRequest) (r map[string][]byte, err error) {
	m := sync.Map{}

	var (
		wg       sync.WaitGroup
		errMutex sync.Mutex
		firstErr error
	)

	for _, ts := range wr.Timeseries {
		wg.Add(1)

		go func(ts prompb.TimeSeries) {
			defer wg.Done()

			tenant, err := processTimeseries(h, req, &ts)
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

			wr, ok := v.(*prompb.WriteRequest)
			if !ok {
				h.Log.Error(fmt.Errorf("expected *prompb.WriteRequest, got %T", v), "Unable to marshal tenant request")

				return
			}

			wr.Timeseries = append(wr.Timeseries, ts)
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

		buf, err := proto.Marshal(writeReq)
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

func processTimeseries(processor *handler.Handler, req *fh.Request, ts *prompb.TimeSeries) (tenant string, err error) {
	var (
		namespace string
		idx       int
	)

	for i, l := range ts.Labels {
		for _, configuredLabel := range processor.Config.Tenant.Labels {
			if l.Name == configuredLabel {
				processor.Log.Info("found", "label", configuredLabel, "value", l.Value)

				namespace = l.Value
				idx = i

				break
			}
		}
	}

	mapping := processor.Store.GetOrg(namespace)
	if mapping == nil {
		return "", fmt.Errorf("no tenant assigned: {'%s'} not found and no default defined", strings.Join(processor.Config.Tenant.Labels, "','"))
	}

	tenantPrefix := processor.Config.Tenant.Prefix
	if processor.Config.Tenant.PrefixPreferSource {
		sourceTenantPrefix := string(req.Header.Peek(processor.Config.Tenant.Header))
		if sourceTenantPrefix != "" {
			tenantPrefix = sourceTenantPrefix + "-"
		}
	}

	tenant = tenantPrefix + mapping.Organisation

	if len(mapping.Labels) > 0 {
		for l, k := range mapping.Labels {
			ts.Labels = append(ts.Labels, prompb.Label{
				Name:  l,
				Value: k,
			})
		}
	}

	if processor.Config.Tenant.TenantLabel != "" {
		ts.Labels = append(ts.Labels, prompb.Label{
			Name:  processor.Config.Tenant.TenantLabel,
			Value: tenant,
		})
	}

	// Handling Label Removing
	if idx != 0 && processor.Config.Tenant.LabelRemove {
		// Order is important. See:
		// https://github.com/thanos-io/thanos/issues/6452
		// https://github.com/prometheus/prometheus/issues/11505
		ts.Labels = removeOrdered(ts.Labels, idx)
	}

	return tenant, err
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
