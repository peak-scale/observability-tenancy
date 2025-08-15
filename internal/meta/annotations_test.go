// Copyright 2024 Peak Scale
// SPDX-License-Identifier: Apache-2.0

package meta

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNamespaceOrgName(t *testing.T) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				AnnotationOrganisationName: "my-org",
			},
		},
	}

	got := NamespaceOrgName(ns)
	want := "my-org"
	if got != want {
		t.Errorf("NamespaceOrgName() = %q, want %q", got, want)
	}
}

func TestNamespaceOrgName_MissingAnnotation(t *testing.T) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{},
		},
	}

	got := NamespaceOrgName(ns)
	if got != "" {
		t.Errorf("NamespaceOrgName() = %q, want empty string", got)
	}
}

func TestGetAdditionalLabels(t *testing.T) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				"label.observe.addons.projectcapsule.dev/env":  "prod",
				"label.observe.addons.projectcapsule.dev/team": "platform",
				"some.other/label":                         "value",
				"label.observe.addons.projectcapsule.dev/": "ignored", // edge case
			},
		},
	}

	got := GetAdditionalAnnotations(ns)
	want := map[string]string{
		"env":  "prod",
		"team": "platform",
	}

	if len(got) != len(want) {
		t.Fatalf("GetAdditionalLabels() = %v, want %v", got, want)
	}

	for k, v := range want {
		if got[k] != v {
			t.Errorf("GetAdditionalLabels()[%q] = %q, want %q", k, got[k], v)
		}
	}
}

func TestGetAdditionalLabels_Empty(t *testing.T) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"some-label": "some-value",
			},
		},
	}

	got := GetAdditionalAnnotations(ns)
	if len(got) != 0 {
		t.Errorf("GetAdditionalLabels() = %v, want empty map", got)
	}
}
