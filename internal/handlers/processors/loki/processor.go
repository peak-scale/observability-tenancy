package loki

import (
	"fmt"
	"strings"
	"sync"

	"github.com/go-logr/logr"
	"github.com/gogo/protobuf/proto"
	"github.com/golang/snappy"
	"github.com/grafana/loki/pkg/push"
	"github.com/pkg/errors"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
	fh "github.com/valyala/fasthttp"

	"github.com/peak-scale/observability-tenancy/internal/config"
	"github.com/peak-scale/observability-tenancy/internal/handlers/handler"
	"github.com/peak-scale/observability-tenancy/internal/metrics"
	"github.com/peak-scale/observability-tenancy/internal/stores"
)

func NewLokiProcessor(
	log logr.Logger,
	c config.Config,
	store *stores.NamespaceStore,
	metrics *metrics.ProxyRecorder,
) *handler.Handler {
	return handler.NewHandler(log, c, store, metrics, "loki-tenant", process, nil)
}

// Process Loki Stream for each request.
func process(processor *handler.Handler, req *fh.Request) (map[string][]byte, error) {
	wrReqIn, err := unmarshal(req.Body())
	if err != nil {
		return nil, err
	}

	if len(wrReqIn.Streams) == 0 {
		return nil, errors.New("no streams found in the request")
	}

	m, err := createTenantRequests(processor, req, wrReqIn)
	if err != nil {
		return nil, err
	}

	return m, nil
}

func unmarshal(b []byte) (*push.PushRequest, error) {
	decoded, err := snappy.Decode(nil, b)
	if err != nil {
		return nil, errors.Wrap(err, "Unable to unpack Snappy")
	}

	req := &push.PushRequest{}
	if err := proto.Unmarshal(decoded, req); err != nil {
		return nil, errors.Wrap(err, "Unable to unmarshal protobuf")
	}

	return req, nil
}

//nolint:unused
func marshal(wr *push.PushRequest) (bufOut []byte, err error) {
	b := make([]byte, wr.Size())

	// Marshal to Protobuf
	if _, err = wr.MarshalTo(b); err != nil {
		return
	}

	// Compress with Snappy
	return snappy.Encode(nil, b), nil
}

func createTenantRequests(h *handler.Handler, req *fh.Request, pr *push.PushRequest) (r map[string][]byte, err error) {
	m := sync.Map{}

	var (
		wg       sync.WaitGroup
		errMutex sync.Mutex
		firstErr error
	)

	for _, stream := range pr.Streams {
		wg.Add(1)

		go func(stream push.Stream) {
			defer wg.Done()

			tenant, err := processStreamRequest(h, req, &stream)
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

			v, _ := m.LoadOrStore(tenant, &push.PushRequest{Streams: []push.Stream{}})

			pr, ok := v.(*push.PushRequest)
			if !ok {
				h.Log.Error(fmt.Errorf("expected *push.PushRequest, got %T", v), "Unable to marshal tenant request")

				return
			}

			pr.Streams = append(pr.Streams, stream)
		}(stream)
	}

	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}

	r = make(map[string][]byte)

	m.Range(func(tenant, pushReq any) bool {
		writeReq, ok := pushReq.(*push.PushRequest)
		if !ok {
			h.Log.Error(fmt.Errorf("expected *push.PushRequest, got %T", tenant), "Unable to marshal tenant request")

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

func processStreamRequest(processor *handler.Handler, req *fh.Request, stream *push.Stream) (tenant string, err error) {
	var (
		namespace      string
		namespaceLabel string
	)

	var streamLabels labels.Labels

	if streamLabels, err = parseStreamLabels(stream.Labels); err != nil {
		return "", err
	}

	streamLabels.Range(func(l labels.Label) {
		for _, configuredLabel := range processor.Config.Tenant.Labels {
			if l.Name == configuredLabel {
				namespace = streamLabels.Get(configuredLabel)
				namespaceLabel = configuredLabel

				processor.Log.V(5).Info("found", "label", configuredLabel, "value", namespace)

				break
			}
		}
	})

	labelBuilder := labels.NewBuilder(streamLabels)

	mapping := processor.Store.GetOrg(namespace)
	if mapping == nil {
		if processor.Config.Tenant.Default == "" {
			return "", fmt.Errorf("no tenant assigned for %s: labels {'%s'} not found and no defaulting defined", namespace, strings.Join(processor.Config.Tenant.Labels, "','"))
		}

		tenant = processor.Config.Tenant.Default
	} else {
		// Add Additional Labels
		if mapping.Labels != nil {
			for l, k := range mapping.Labels {
				labelBuilder.Set(l, k)
			}
		}

		tenant = mapping.Organisation
	}

	tenantPrefix := processor.Config.Tenant.Prefix

	if processor.Config.Tenant.PrefixPreferSource {
		sourceTenantPrefix := string(req.Header.Peek(processor.Config.Tenant.Header))
		if sourceTenantPrefix != "" {
			tenantPrefix = sourceTenantPrefix + "-"
		}
	}

	tenant = tenantPrefix + tenant

	// Add Tenant as Label
	if processor.Config.Tenant.TenantLabel != "" {
		labelBuilder.Set(processor.Config.Tenant.TenantLabel, tenant)
	}

	// Handling Label Removing
	if namespaceLabel != "" && processor.Config.Tenant.LabelRemove {
		// Order is important. See:
		// https://github.com/thanos-io/thanos/issues/6452
		// https://github.com/prometheus/prometheus/issues/11505
		labelBuilder.Del(namespaceLabel)
	}

	stream.Labels = labelBuilder.Labels().String()

	return tenant, err
}

func parseStreamLabels(labels string) (labels.Labels, error) {
	return parser.NewParser(parser.Options{}).ParseMetric(labels)
}
