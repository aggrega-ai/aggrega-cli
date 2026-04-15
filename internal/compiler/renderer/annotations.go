// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

// Well-known provider annotation keys. These are read by the provider engines
// (helm, helm-chart) out of NodeRenderContext.Annotations and applied to the
// rendered K8s manifests.
const (
	// AnnotationNamespace sets metadata.namespace on rendered K8s manifests.
	// Sourced mode uses it as the Helm Release namespace.
	AnnotationNamespace = "aggrega.ai/namespace"
)

// NamespaceFromAnnotations returns the value of AnnotationNamespace or "".
func NamespaceFromAnnotations(annotations map[string]string) string {
	if annotations == nil {
		return ""
	}
	return annotations[AnnotationNamespace]
}
