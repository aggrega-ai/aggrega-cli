// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
	"gopkg.in/yaml.v3"
)

// The three Load* functions in this file are raw YAML readers: each reads a
// path, unmarshals into the typed entity, and wraps any failure as a
// ResolverError with the entity kind and source path attached. They do not
// validate against the JSON Schema envelope — schema validation is handled
// in pkg/validator against the entity's authored Schema content composed
// with the usesSchema / secretsSchema bridge synthesis.
//
// LoadRelease and LoadManifest are used directly: cmd/aggrega/build.go
// pre-loads the Release to discover the Aggregate and Manifest paths, and
// Resolve loads the Manifest as part of its pipeline. LoadAggregate exists
// for symmetry but is not used by Resolve, which reads raw Aggregate bytes
// for parameter interpolation and unmarshals them only after interpolation.

// LoadAggregate reads and unmarshals an Aggregate YAML file.
func LoadAggregate(path string) (*types.Aggregate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, &ResolverError{
			Entity:  "Aggregate",
			Field:   path,
			Message: fmt.Sprintf("failed to read file: %v", err),
			Err:     ErrYAMLLoad,
		}
	}
	return LoadAggregateFromBytes(data, path)
}

// LoadAggregateFromBytes unmarshals an Aggregate from already-read bytes,
// attributing any error to path. It is the read-free core of LoadAggregate,
// for callers that already hold the bytes (the same-directory triad scan) and
// must not read the file a second time.
func LoadAggregateFromBytes(data []byte, path string) (*types.Aggregate, error) {
	if _, eerr := envelope.ValidateBytes(envelope.KindAggregate, data, path); errors.Is(eerr, envelope.ErrEntityEnvelope) {
		return nil, &ResolverError{Entity: "Aggregate", Field: path, Message: eerr.Error(), Err: eerr}
	}
	var agg types.Aggregate
	if err := yaml.Unmarshal(data, &agg); err != nil {
		return nil, &ResolverError{
			Entity:  "Aggregate",
			Field:   path,
			Message: fmt.Sprintf("failed to unmarshal YAML: %v", err),
			Err:     ErrYAMLLoad,
		}
	}
	return &agg, nil
}

// LoadRelease reads and unmarshals a Release YAML file.
func LoadRelease(path string) (*types.Release, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, &ResolverError{
			Entity:  "Release",
			Field:   path,
			Message: fmt.Sprintf("failed to read file: %v", err),
			Err:     ErrYAMLLoad,
		}
	}
	if _, eerr := envelope.ValidateBytes(envelope.KindRelease, data, path); errors.Is(eerr, envelope.ErrEntityEnvelope) {
		return nil, &ResolverError{Entity: "Release", Field: path, Message: eerr.Error(), Err: eerr}
	}
	var rel types.Release
	if err := yaml.Unmarshal(data, &rel); err != nil {
		return nil, &ResolverError{
			Entity:  "Release",
			Field:   path,
			Message: fmt.Sprintf("failed to unmarshal YAML: %v", err),
			Err:     ErrYAMLLoad,
		}
	}
	// Desugar a top-level components surface into the synthesized `default`
	// boundary at load, so every downstream consumer sees a uniform
	// boundary-bearing Release.
	NormalizeReleaseScopes(&rel)
	return &rel, nil
}

// LoadManifest reads and unmarshals a Manifest YAML file. The loader
// enforces the reserved-prefix rule on annotation-binding keys: any
// features.annotations entry keyed under the bare aggrega.ai/ prefix is
// rejected with a CATALOG_ERROR-prefixed message at load time. The
// prefix is reserved for the provider well-known annotations documented
// at aggrega-docs/language-reference/providers/well-known-annotations.md;
// authors of annotation-driven Feature bindings use feature.aggrega.ai/
// for standard-library shortcuts or a custom domain for enterprise
// shortcuts.
func LoadManifest(path string) (*types.Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, &ResolverError{
			Entity:  "Manifest",
			Field:   path,
			Message: fmt.Sprintf("failed to read file: %v", err),
			Err:     ErrYAMLLoad,
		}
	}
	return LoadManifestFromBytes(data, path)
}

// LoadManifestFromBytes unmarshals a Manifest from already-read bytes and runs
// the same reserved-prefix and cross-binding validations as LoadManifest,
// attributing any error to path. It is the read-free core of LoadManifest, for
// callers that already hold the bytes (the same-directory triad scan).
func LoadManifestFromBytes(data []byte, path string) (*types.Manifest, error) {
	if _, eerr := envelope.ValidateBytes(envelope.KindManifest, data, path); errors.Is(eerr, envelope.ErrEntityEnvelope) {
		return nil, &ResolverError{Entity: "Manifest", Field: path, Message: eerr.Error(), Err: eerr}
	}
	var man types.Manifest
	if err := yaml.Unmarshal(data, &man); err != nil {
		return nil, &ResolverError{
			Entity:  "Manifest",
			Field:   path,
			Message: fmt.Sprintf("failed to unmarshal YAML: %v", err),
			Err:     ErrYAMLLoad,
		}
	}
	if err := rejectReservedAnnotationBindings(&man, path); err != nil {
		return nil, err
	}
	if err := rejectCrossBindingPathPillarCollision(&man, path); err != nil {
		return nil, err
	}
	return &man, nil
}

// rejectReservedAnnotationBindings walks every annotation-binding key
// declared on the Manifest (flat features tier plus any legacy
// per-provider tiers within the migration window) and rejects any key
// using the reserved aggrega.ai/ prefix. First-violation-halt matches
// existing Manifest-load patterns for shape-level rejections.
func rejectReservedAnnotationBindings(man *types.Manifest, path string) error {
	const reservedPrefix = "aggrega.ai/"
	check := func(annotations *types.OrderedMap[types.AnnotationBinding]) error {
		if annotations == nil {
			return nil
		}
		for _, key := range annotations.Keys() {
			if strings.HasPrefix(key, reservedPrefix) {
				return &ResolverError{
					Entity: "Manifest",
					Field:  path,
					Message: fmt.Sprintf(
						"CATALOG_ERROR: features.annotations binding %q uses reserved prefix %q; reserved for provider well-known annotations. Use feature.<your-domain>/ for Feature bindings.",
						key, reservedPrefix,
					),
					Err: ErrManifestReservedPrefix,
				}
			}
		}
		return nil
	}

	if man.Spec.Mappings.Features != nil {
		if err := check(man.Spec.Mappings.Features.Annotations); err != nil {
			return err
		}
	}
	for _, prov := range man.Spec.Mappings.Legacy.Keys() {
		tier, _ := man.Spec.Mappings.Legacy.Get(prov)
		if tier.Features != nil {
			if err := check(tier.Features.Annotations); err != nil {
				return err
			}
		}
	}
	return nil
}

// rejectCrossBindingPathPillarCollision walks every annotation binding
// on the Manifest and rejects the case where two bindings target the
// same Feature with the same dotted-path subkey routing to the same
// pillar. The collision case produces an ambiguous "which binding routes
// this path?" question the expansion pipeline cannot resolve. A single
// Feature may carry distinct bindings for distinct (path, pillar) pairs;
// the rule rejects only the same-Feature same-(path, pillar) duplicate.
//
// First-violation-halt matches existing Manifest-load patterns for
// shape-level rejections.
func rejectCrossBindingPathPillarCollision(man *types.Manifest, path string) error {
	type pillarTarget struct{ path, pillar string }

	check := func(annotations *types.OrderedMap[types.AnnotationBinding]) error {
		if annotations == nil {
			return nil
		}
		// seen tracks, per Feature URI, the set of (path, pillar) pairs
		// and the first annotation key that declared the pair.
		seen := map[string]map[pillarTarget]string{}
		for _, key := range annotations.Keys() {
			binding, _ := annotations.Get(key)
			featureMap, ok := seen[binding.Feature]
			if !ok {
				featureMap = map[pillarTarget]string{}
				seen[binding.Feature] = featureMap
			}
			collect := func(paths []string, pillar string) error {
				for _, p := range paths {
					target := pillarTarget{path: p, pillar: pillar}
					if priorKey, dup := featureMap[target]; dup {
						return &ResolverError{
							Entity: "Manifest",
							Field:  path,
							Message: fmt.Sprintf(
								"CATALOG_ERROR: annotation bindings %q and %q both target Feature %q with path %q routing to pillar %q; only one binding may declare a given (path, pillar) pair per Feature.",
								priorKey, key, binding.Feature, p, pillar,
							),
							Err: ErrManifestCrossBindingPillarCollision,
						}
					}
					featureMap[target] = key
				}
				return nil
			}
			if err := collect(binding.Maps.Infra, "infra"); err != nil {
				return err
			}
			if err := collect(binding.Maps.Values, "values"); err != nil {
				return err
			}
		}
		return nil
	}

	if man.Spec.Mappings.Features != nil {
		if err := check(man.Spec.Mappings.Features.Annotations); err != nil {
			return err
		}
	}
	for _, prov := range man.Spec.Mappings.Legacy.Keys() {
		tier, _ := man.Spec.Mappings.Legacy.Get(prov)
		if tier.Features != nil {
			if err := check(tier.Features.Annotations); err != nil {
				return err
			}
		}
	}
	return nil
}
