// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package rendercore

import (
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
)

// Well-known provider annotation keys. Provider engines read these out of
// NodeRenderContext.Annotations — the set [WellKnownAnnotationSet] returns — and
// apply them to the rendered artifacts. Which
// engines honor a given key varies by key (a key applies only where the target
// format has a matching concept) — see each key's own documentation.
const (
	// AnnotationNamespace sets metadata.namespace on rendered K8s manifests.
	// Sourced mode uses it as the Helm Release namespace.
	AnnotationNamespace = "aggrega.ai/namespace"

	// AnnotationServiceAccount binds the rendered pods' serviceAccountName from
	// the node's annotations. It is the annotation-layer alternative to the
	// recipe-layer workload.serviceAccount field, and overrides that field when
	// both are set.
	AnnotationServiceAccount = "aggrega.ai/service-account"

	// AnnotationImageRegistry is a single global registry prefix applied to
	// every rendered container image reference — regular and init containers
	// alike, across the helm and compose synth engines. When set, each image
	// ref is relocated under this registry: any existing registry-host segment
	// is stripped and the prefix prepended (the air-gap "one internal registry"
	// pattern). Tags and digests are preserved. It is not a per-source mirror
	// mapping table.
	AnnotationImageRegistry = "aggrega.ai/image-registry"

	// AnnotationImagePullSecret names an image pull secret unioned into the
	// rendered pod's imagePullSecrets, coexisting with any recipe-declared
	// entries (a K8s-pod concept; compose has no equivalent and ignores it).
	AnnotationImagePullSecret = "aggrega.ai/image-pull-secret"

	// AnnotationKubernetesVersion names the Kubernetes version a chart rendered
	// from source is rendered for: the version its templates read as
	// .Capabilities.KubeVersion. The helm engine parses it as `helm template
	// --kube-version` parses its flag; absent or empty, it renders for the version
	// the stock `helm template` of the same helm release renders for.
	AnnotationKubernetesVersion = "aggrega.ai/kubernetes-version"
)

// Precedence of well-known annotation fields over the matching workload field is
// resolved by field cardinality:
//
//   - A scalar annotation-field overrides the workload field
//     (AnnotationServiceAccount wins over workload.serviceAccount) — the
//     annotation is last-writer over the recipe default.
//   - A list annotation-field unions with the workload field
//     (AnnotationImagePullSecret appends into workload.imagePullSecrets) —
//     overriding a list would silently drop recipe-declared entries.
//
// The resolution for each field lives in exactly one render site per engine.

// WellKnownAnnotationSet returns the annotation map a provider render reads the
// well-known keys above from: the render context's platform annotations
// ([renderctx.RenderAnnotations.PlatformAnnotations]) — what the Aggregate wrote
// for the node, with what the Release wrote for it layered over, the Release
// winning on a key both write — flattened into a plain map.
//
// The catalog bucket reaches a guest beside it, and a guest may read it for
// anything of its own, but it never decides a well-known key. The set is the one
// the compiler reads a node's namespace from when it places the node's
// connections, so a render reading the keys from anywhere else would place the
// node somewhere its connections do not look for it.
func WellKnownAnnotationSet(a renderctx.RenderAnnotations) map[string]string {
	platform := a.PlatformAnnotations()
	out := make(map[string]string, platform.Len())
	for k, v := range platform.All() {
		out[k] = v
	}
	return out
}

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

// ImageRegistryFromAnnotations returns the value of AnnotationImageRegistry or "".
func ImageRegistryFromAnnotations(annotations map[string]string) string {
	if annotations == nil {
		return ""
	}
	return annotations[AnnotationImageRegistry]
}

// ImagePullSecretFromAnnotations returns the value of AnnotationImagePullSecret
// or "". Being a list-cardinality field, its value unions with (rather than
// overrides) the workload's imagePullSecrets at the render site.
func ImagePullSecretFromAnnotations(annotations map[string]string) string {
	if annotations == nil {
		return ""
	}
	return annotations[AnnotationImagePullSecret]
}

// KubernetesVersionFromAnnotations returns the value of
// AnnotationKubernetesVersion or "". The value is returned as authored; the
// engine that reads it decides what it accepts.
func KubernetesVersionFromAnnotations(annotations map[string]string) string {
	if annotations == nil {
		return ""
	}
	return annotations[AnnotationKubernetesVersion]
}

// ApplyImageRegistry rewrites a container image reference to sit under the given
// registry prefix, relocating every image under a single registry regardless of
// its original source. It strips any existing registry-host segment and prepends
// the prefix.
//
// The host segment is the portion before the first "/" when that segment looks
// like a registry host — it contains "." or ":", or equals "localhost" (the
// Docker reference convention). A reference with no such host segment (for
// example "postgres:16" or "library/postgres:16") keeps its whole path, so both
// "docker.io/library/postgres:16" and "library/postgres:16" normalize to
// "<prefix>/library/postgres:16". Tags ("name:tag") and digests
// ("name@sha256:...") are preserved: only the leading host is removed. An empty
// prefix or empty ref returns ref unchanged.
func ApplyImageRegistry(ref, prefix string) string {
	if prefix == "" || ref == "" {
		return ref
	}
	remainder := ref
	if i := strings.IndexByte(ref, '/'); i >= 0 {
		first := ref[:i]
		if strings.ContainsAny(first, ".:") || first == "localhost" {
			remainder = ref[i+1:]
		}
	}
	return strings.TrimSuffix(prefix, "/") + "/" + remainder
}
