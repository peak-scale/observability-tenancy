package stores

import (
	"sync"

	corev1 "k8s.io/api/core/v1"

	"github.com/peak-scale/observability-tenancy/internal/config"
	"github.com/peak-scale/observability-tenancy/internal/meta"
)

type NamespaceStore struct {
	sync.RWMutex
	namespaces map[string]string
}

func NewNamespaceStore() *NamespaceStore {
	return &NamespaceStore{
		namespaces: make(map[string]string),
	}
}

func (s *NamespaceStore) GetOrg(namespace string) string {
	return s.namespaces[namespace]
}

func (s *NamespaceStore) Update(namespace *corev1.Namespace, cfg *config.TenantConfig) {
	s.Lock()
	defer s.Unlock()

	if org := meta.NamespaceOrgName(namespace); org != "" {
		s.namespaces[namespace.Name] = org

		return
	}

	if cfg != nil && cfg.SetNamespaceAsDefault {
		s.namespaces[namespace.Name] = namespace.Name

		return
	}

	s.Delete(namespace)
}

func (s *NamespaceStore) Delete(namespace *corev1.Namespace) {
	delete(s.namespaces, namespace.Name)
}

func (s *NamespaceStore) Clear() {
	s.Lock()
	defer s.Unlock()

	s.namespaces = make(map[string]string)
}
