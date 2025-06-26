// Copyright 2024 Peak Scale
// SPDX-License-Identifier: Apache-2.0

package meta

import (
	corev1 "k8s.io/api/core/v1"
)

const (
	// Annotation on Namespace.
	// Change the organisation Header value, by default namespace name is used.
	AnnotationOrganisationName = "observe.addons.projectcapsule.dev/org"
)

// Namespace Organisation association.
func NamespaceOrgName(namespace *corev1.Namespace) (name string) {
	return namespace.Annotations[AnnotationOrganisationName]
}
