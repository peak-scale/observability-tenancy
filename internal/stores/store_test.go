package stores_test

import (
	"testing"

	"github.com/peak-scale/observability-tenancy/internal/config"
	"github.com/peak-scale/observability-tenancy/internal/meta"
	"github.com/peak-scale/observability-tenancy/internal/stores"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/gomega"
)

// TestTenantStore_Basic verifies that updating, retrieving, and deleting tenants works as expected.
func TestTenantStore_Basic(t *testing.T) {
	RegisterTestingT(t)

	store := stores.NewNamespaceStore()

	ns1 := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "tenant-1",
			Annotations: map[string]string{
				meta.AnnotationLabelName + "test": "value-2",
			},
		},
	}

	ns2 := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "tenant-2",
			Annotations: map[string]string{
				meta.AnnotationOrganisationName:   "tenant-1",
				meta.AnnotationLabelName + "test": "value-1",
				meta.AnnotationLabelName + "org":  "org-1",
			},
		},
	}

	// Update the store with tenant1 for ns1 and ns2.
	store.Update(ns1, nil)
	store.Update(ns2, nil)
	Expect(store.GetOrg(ns1.Name)).To(BeNil())
	Expect(store.GetOrg(ns2.Name)).To(Equal(&stores.NamespaceMapping{
		Organisation: "tenant-1",
		Labels: map[string]string{
			"test": "value-1",
			"org":  "org-1",
		},
	}))

	// Delete tenant; ns2 and ns3 should be removed.
	store.Delete(ns2)
	Expect(store.GetOrg(ns2.Name)).To(BeNil())
}

func TestTenantStore_Config(t *testing.T) {
	RegisterTestingT(t)

	store := stores.NewNamespaceStore()

	ns1 := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "tenant-1",
			Annotations: map[string]string{
				meta.AnnotationLabelName + "test": "value-1",
				meta.AnnotationLabelName + "org":  "org-1",
			},
		},
	}

	ns2 := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "tenant-2",
			Annotations: map[string]string{
				meta.AnnotationLabelName + "test": "value-2",
				meta.AnnotationLabelName + "org":  "org-2",
			},
		},
	}

	ns3 := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "tenant-3",
		},
	}

	cfg := &config.TenantConfig{
		Default:               "infra",
		SetNamespaceAsDefault: true,
	}

	// Update the store with tenant1 for ns1 and ns2.
	store.Update(ns1, cfg)
	store.Update(ns2, cfg)
	store.Update(ns3, cfg)
	Expect(store.GetOrg(ns1.Name)).To(Equal(&stores.NamespaceMapping{
		Organisation: ns1.Name,
		Labels: map[string]string{
			"test": "value-1",
			"org":  "org-1",
		},
	}))
	Expect(store.GetOrg(ns2.Name)).To(Equal(&stores.NamespaceMapping{
		Organisation: ns2.Name,
		Labels: map[string]string{
			"test": "value-2",
			"org":  "org-2",
		},
	}))
	Expect(store.GetOrg(ns3.Name)).To(Equal(&stores.NamespaceMapping{
		Organisation: ns3.Name,
		Labels:       map[string]string{},
	}))

}
