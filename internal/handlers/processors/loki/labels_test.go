package loki

import (
	"testing"

	"github.com/go-logr/logr"
	"github.com/grafana/loki/pkg/push"
	"github.com/prometheus/prometheus/model/labels"
	fh "github.com/valyala/fasthttp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/peak-scale/observability-tenancy/internal/config"
	"github.com/peak-scale/observability-tenancy/internal/handlers/handler"
	"github.com/peak-scale/observability-tenancy/internal/meta"
	"github.com/peak-scale/observability-tenancy/internal/stores"
)

func TestProcessStreamRequestLabels(t *testing.T) {
	for _, tc := range []struct {
		name       string
		input      string
		remove     bool
		wantTenant string
		wantLabels string
	}{
		{
			name:       "remove the first namespace label",
			input:      `{namespace="solar", pod="one"}`,
			remove:     true,
			wantTenant: "test-solar-org",
			wantLabels: `{cluster="central", pod="one", tenant="test-solar-org"}`,
		},
		{
			name:       "preserve namespace when removal is disabled",
			input:      `{namespace="solar", pod="one"}`,
			wantTenant: "test-solar-org",
			wantLabels: `{cluster="central", namespace="solar", pod="one", tenant="test-solar-org"}`,
		},
		{
			name:       "replace existing labels without duplicates",
			input:      `{cluster="old", namespace="solar", tenant="old"}`,
			remove:     true,
			wantTenant: "test-solar-org",
			wantLabels: `{cluster="central", tenant="test-solar-org"}`,
		},
		{
			name:       "preserve unrelated labels when using the default tenant",
			input:      `{app="logs"}`,
			remove:     true,
			wantTenant: "test-default",
			wantLabels: `{app="logs", tenant="test-default"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Config{Tenant: &config.TenantConfig{
				Labels:      []string{"namespace"},
				LabelRemove: tc.remove,
				TenantLabel: "tenant",
				Prefix:      "test-",
				Default:     "default",
			}}
			store := stores.NewNamespaceStore()
			store.Update(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
				Name: "solar",
				Annotations: map[string]string{
					meta.AnnotationOrganisationName:      "solar-org",
					meta.AnnotationLabelName + "cluster": "central",
				},
			}}, cfg.Tenant)
			processor := &handler.Handler{Config: cfg, Store: store, Log: logr.Discard()}
			stream := &push.Stream{Labels: tc.input}

			tenant, err := processStreamRequest(processor, &fh.Request{}, stream)
			if err != nil {
				t.Fatal(err)
			}
			if tenant != tc.wantTenant {
				t.Fatalf("tenant = %q, want %q", tenant, tc.wantTenant)
			}
			got, err := parseStreamLabels(stream.Labels)
			if err != nil {
				t.Fatalf("invalid output labels: %v", err)
			}
			want, err := parseStreamLabels(tc.wantLabels)
			if err != nil {
				t.Fatal(err)
			}
			if !labels.Equal(got, want) {
				t.Fatalf("labels = %s, want %s", got, want)
			}
		})
	}
}
