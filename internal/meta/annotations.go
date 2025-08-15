// Copyright 2024 Peak Scale
// SPDX-License-Identifier: Apache-2.0

package meta

import (
	"regexp"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

const (
	// Annotation on Namespace.
	// Change the organisation Header value, by default namespace name is used.
	AnnotationOrganisationName = "observe.addons.projectcapsule.dev/org"

	// Annotation on Namespace.
	// Add additional labels with attached value for the given namespace
	AnnotationLabelName = "label.observe.addons.projectcapsule.dev/"
)

var validLabelName = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)

// Namespace Organisation association.
func NamespaceOrgName(namespace *corev1.Namespace) (name string) {
	return namespace.Annotations[AnnotationOrganisationName]
}

// Get Additional Labels from Annotations
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
