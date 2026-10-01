// Copyright 2024 Peak Scale
// SPDX-License-Identifier: Apache-2.0

package meta

import (
	"regexp"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

const (
	// AnnotationOrganisationName overrides the organisation header for a namespace.
	AnnotationOrganisationName = "observe.addons.projectcapsule.dev/org"

	// AnnotationLabelName prefixes annotations that add labels to namespace traffic.
	AnnotationLabelName = "label.observe.addons.projectcapsule.dev/"
)

var validLabelName = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)

// NamespaceOrgName returns the organisation associated with a namespace.
func NamespaceOrgName(namespace *corev1.Namespace) (name string) {
	return namespace.Annotations[AnnotationOrganisationName]
}

// GetAdditionalAnnotations extracts additional labels from namespace annotations.
func GetAdditionalAnnotations(namespace *corev1.Namespace) map[string]string {
	result := make(map[string]string)

	for label, value := range namespace.GetAnnotations() {
		if !strings.HasPrefix(label, AnnotationLabelName) {
			continue
		}

		// Strip the prefix
		stripped := strings.TrimPrefix(label, AnnotationLabelName)
		if stripped == "" || !validLabelName.MatchString(stripped) {
			continue
		}

		result[stripped] = value
	}

	return result
}
