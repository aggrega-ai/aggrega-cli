// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

// Well-known provider annotation keys. These are read by the provider engines
// (helm, helm-chart) out of NodeRenderContext.Annotations and applied to the
// rendered K8s manifests.
const (
	// AnnotationNamespace sets metadata.namespace on rendered K8s manifests.
	// Sourced mode uses it as the Helm Release namespace.
	AnnotationNamespace = "aggrega.ai/namespace"

	// AnnotationServiceAccount binds the rendered pods' serviceAccountName from
	// the operator boundary layer. It is the boundary-layer alternative to the
	// recipe-layer workload.serviceAccount field, and overrides that field when
	// both are set.
	AnnotationServiceAccount = "aggrega.ai/service-account"
)

// NamespaceFromAnnotations returns the value of AnnotationNamespace or "".
func NamespaceFromAnnotations(annotations map[string]string) string {
	if annotations == nil {
		return ""
	}
	return annotations[AnnotationNamespace]
}

// ServiceAccountFromAnnotations returns the value of AnnotationServiceAccount or "".
func ServiceAccountFromAnnotations(annotations map[string]string) string {
	if annotations == nil {
		return ""
	}
	return annotations[AnnotationServiceAccount]
}
