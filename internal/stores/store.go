package stores

import (
	"sync"

	corev1 "k8s.io/api/core/v1"

	"github.com/peak-scale/observability-tenancy/internal/config"
	"github.com/peak-scale/observability-tenancy/internal/meta"
)

type NamespaceMapping struct {
	Organisation string
	Labels       map[string]string
}

type NamespaceStore struct {
	sync.RWMutex
	namespaces map[string]*NamespaceMapping
}

func NewNamespaceStore() *NamespaceStore {
	return &NamespaceStore{
		namespaces: make(map[string]*NamespaceMapping),
	}
}

func (s *NamespaceStore) GetOrg(namespace string) *NamespaceMapping {
	return s.namespaces[namespace]
}

func (s *NamespaceStore) Update(namespace *corev1.Namespace, cfg *config.TenantConfig) {
	s.Lock()
	defer s.Unlock()

	mapping := &NamespaceMapping{Labels: meta.GetAdditionalAnnotations(namespace)}

	if org := meta.NamespaceOrgName(namespace); org != "" {
		mapping.Organisation = org
	} else if cfg != nil && cfg.SetNamespaceAsDefault {
		mapping.Organisation = namespace.Name
	}

	if mapping.Organisation == "" {
		return
	}

	s.namespaces[namespace.Name] = mapping
}

func (s *NamespaceStore) Delete(namespace *corev1.Namespace) {
	delete(s.namespaces, namespace.Name)
}
