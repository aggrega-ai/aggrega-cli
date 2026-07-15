// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// AnnotationRegistry holds the flat annotation-binding registry derived
// from the resolved Manifest's `mappings.features.annotations` map. The
// registry is consulted at annotation-expansion time to translate
// Release-level annotations into synthetic Feature attachments. The
// post-migration Manifest carries annotation bindings under the flat
// features tier; provider determination at the component level flows
// from each component's resolved Recipe `spec.capability` at orchestrator
// time, so the binding-declaration registry itself is provider-agnostic.
type AnnotationRegistry struct {
	bindings types.OrderedMap[types.AnnotationBinding]
}

// BuildAnnotationRegistry reads the flat features.annotations map from
// the resolved Manifest and returns the registry consumed by
// ExpandAnnotations. Returns an empty registry when the Manifest carries
// no flat features tier or when the tier carries no annotation bindings;
// callers can call Lookup on an empty registry safely.
func BuildAnnotationRegistry(manifest *types.Manifest) *AnnotationRegistry {
	r := &AnnotationRegistry{}
	if manifest == nil {
		return r
	}
	if manifest.Spec.Mappings.Features == nil {
		return r
	}
	if manifest.Spec.Mappings.Features.Annotations == nil {
		return r
	}
	for k, v := range manifest.Spec.Mappings.Features.Annotations.All() {
		r.bindings.Set(k, v)
	}
	return r
}

// Lookup returns the binding declared for the given annotation key and
// whether the registry carries an entry for it. The lookup is exact-key
// (no prefix matching). Annotation keys are case-sensitive per YAML
// authoring convention.
func (r *AnnotationRegistry) Lookup(key string) (types.AnnotationBinding, bool) {
	if r == nil {
		return types.AnnotationBinding{}, false
	}
	return r.bindings.Get(key)
}

// Len returns the number of bindings in the registry.
func (r *AnnotationRegistry) Len() int {
	if r == nil {
		return 0
	}
	return r.bindings.Len()
}

// Keys returns the binding keys in author order.
func (r *AnnotationRegistry) Keys() []string {
	if r == nil {
		return nil
	}
	return r.bindings.Keys()
}
