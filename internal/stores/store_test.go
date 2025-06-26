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
		},
	}

	ns2 := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "tenant-2",
			Annotations: map[string]string{
				meta.AnnotationOrganisationName: "tenant1",
			},
		},
	}

	// Update the store with tenant1 for ns1 and ns2.
	store.Update(ns1, nil)
	store.Update(ns2, nil)
	Expect(store.GetOrg(ns1.Name)).To(Equal(""))
	Expect(store.GetOrg(ns2.Name)).To(Equal("tenant1"))

	// Delete tenant; ns2 and ns3 should be removed.
	store.Delete(ns2)
	Expect(store.GetOrg(ns2.Name)).To(Equal(""))
}

func TestTenantStore_Config(t *testing.T) {
	RegisterTestingT(t)

	store := stores.NewNamespaceStore()

	ns1 := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "tenant-1",
		},
	}

	ns2 := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "tenant-2",
		},
	}

	cfg := &config.TenantConfig{
		SetNamespaceAsDefault: true,
	}

	// Update the store with tenant1 for ns1 and ns2.
	store.Update(ns1, cfg)
	store.Update(ns2, cfg)
	Expect(store.GetOrg(ns1.Name)).To(Equal(ns1.Name))
	Expect(store.GetOrg(ns2.Name)).To(Equal(ns2.Name))
}
