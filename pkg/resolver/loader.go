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
	"go.yaml.in/yaml/v3"
)

// Each Load* function in this file is a raw YAML reader: it reads a path,
// gates the bytes through the document envelope, unmarshals into the typed
// entity, and wraps any failure as a ResolverError with the entity kind and
// source path attached. The envelope gate checks the document against its
// kind's schema; the per-entity content validation is separate and runs in
// pkg/validator against the entity's authored Schema content composed with
// the usesSchema / featuresSchema bridge synthesis.
//
// Every reader is paired with a *FromBytes core doing the same work without
// the read, for callers that already hold the bytes. The two sides of a pair
// differ sharply in reach, so neither stands in for the other when tracing
// what a build touches: LoadAggregate is reached from the command that
// reports a single Aggregate as authored, while LoadAggregateFromBytes runs
// on every build — from the same-directory triad scan, the closure descent,
// the catalog's sub-aggregate parameter resolution and the pull path. Each of
// those reads the document as authored, before any parameter reference has
// been substituted.

// LoadAggregate reads and unmarshals an Aggregate YAML file.
func LoadAggregate(path string) (*types.Aggregate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, &ResolverError{
			Entity:  "Aggregate",
			Field:   path,
			Message: fmt.Sprintf("failed to read file: %v", err),
			Err:     ErrYAMLLoad(),
		}
	}
	return LoadAggregateFromBytes(data, path)
}

// LoadAggregateFromBytes unmarshals an Aggregate from already-read bytes,
// attributing any error to path. It is the read-free core of LoadAggregate,
// for callers that already hold the bytes (the same-directory triad scan) and
// must not read the file a second time.
//
// The document is read through the same tolerant parse the envelope gate
// applies to these bytes, so the two agree on what parses. A bare
// `{{ .parameters.x }}` reference is well-formed YAML that reads as a flow
// mapping rather than a scalar, and reading it structurally is what lets an
// author leave a reference unquoted: the reference is substituted into the raw
// bytes before the authoritative parse, and YAML's own quoting rule then
// decides the value's type — bare yields the value's natural type, quoted
// yields a string.
//
// The tolerant parse is structural. It does not preserve the text of an
// expression standing in a mapping key position, so a caller needing that text
// reads it from the raw bytes, which this does not modify.
func LoadAggregateFromBytes(data []byte, path string) (*types.Aggregate, error) {
	if _, eerr := envelope.ValidateBytes(envelope.KindAggregate, data, path); errors.Is(eerr, envelope.ErrEntityEnvelope()) {
		return nil, &ResolverError{Entity: "Aggregate", Field: path, Message: eerr.Error(), Err: eerr}
	}
	node, perr := envelope.ParseDecodable(data, path)
	if perr != nil {
		return nil, &ResolverError{
			Entity:  "Aggregate",
			Field:   path,
			Message: fmt.Sprintf("failed to unmarshal YAML: %v", perr),
			Err:     ErrYAMLLoad(),
		}
	}
	var agg types.Aggregate
	if err := node.Decode(&agg); err != nil {
		return nil, &ResolverError{
			Entity:  "Aggregate",
			Field:   path,
			Message: fmt.Sprintf("failed to unmarshal YAML: %v", err),
			Err:     ErrYAMLLoad(),
		}
	}
	// Single normalization core: fold a top-level components surface into the
	// synthesized `default` subsystem at birth, so every downstream consumer
	// sees the uniform subsystems-only shape. Normalize is non-mutating; return
	// the normalized value it produced.
	return types.Normalize(&agg).Aggregate(), nil
}

// LoadRelease reads and unmarshals a Release YAML file.
func LoadRelease(path string) (*types.Release, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, &ResolverError{
			Entity:  "Release",
			Field:   path,
			Message: fmt.Sprintf("failed to read file: %v", err),
			Err:     ErrYAMLLoad(),
		}
	}
	return LoadReleaseFromBytes(data, path)
}

// LoadReleaseFromBytes unmarshals a Release from already-read bytes, running the
// same envelope gate and boundary normalization as LoadRelease and attributing
// any error to path. It is the read-free core of LoadRelease, for callers that
// already hold the bytes (the same read-free split LoadManifestFromBytes has).
func LoadReleaseFromBytes(data []byte, path string) (*types.Release, error) {
	if _, eerr := envelope.ValidateBytes(envelope.KindRelease, data, path); errors.Is(eerr, envelope.ErrEntityEnvelope()) {
		return nil, &ResolverError{Entity: "Release", Field: path, Message: eerr.Error(), Err: eerr}
	}
	var rel types.Release
	if err := yaml.Unmarshal(data, &rel); err != nil {
		return nil, &ResolverError{
			Entity:  "Release",
			Field:   path,
			Message: fmt.Sprintf("failed to unmarshal YAML: %v", err),
			Err:     ErrYAMLLoad(),
		}
	}
	// Single normalization core: fold a top-level components surface into the
	// synthesized `default` boundary at birth, so every downstream consumer sees
	// the uniform boundary-bearing shape. NormalizeRelease is non-mutating;
	// return the normalized value it produced.
	normalized := types.NormalizeRelease(&rel).Release()
	if err := rejectDuplicateBoundaryComponents(normalized, path); err != nil {
		return nil, err
	}
	return normalized, nil
}

// boundaryPlacement is one thing a boundary places: a component it names, or a
// sub-module it routes. Both are checked in one sequence because both decide
// which boundary a node belongs to, and a conflict between them is the same fault
// as a conflict within either.
type boundaryPlacement struct {
	key          string // a component's flattened id, or a routed sub-module's dotted prefix
	boundaryPath string
	locator      []string
	synthesized  bool
	module       bool
}

// rejectDuplicateBoundaryComponents walks the Release boundary tree and rejects a
// document that places one node in more than one boundary. The rule's inputs are
// complete the moment the document is parsed — it reads nothing but the Release —
// so it is enforced here, at load, where every consumer of a Release sits behind
// it.
//
// It exists because the shape has no single meaning. A component's boundary
// context and its per-edge `uses` surface are read by two separate walks of the
// same tree, and a duplicated name resolves to a different declaration in each,
// so a node's output location can come from one declaration while its per-edge
// configuration comes from another. Halting is what makes that unreachable;
// ranking the declarations would only pick a winner nobody asked for.
//
// A boundary places nodes on two axes, and the check spans both:
//
//   - the same component id named at two boundaries — the direct case;
//   - the same sub-module routed into two boundaries, which places every one of
//     its components twice, and likewise a module routed into one boundary while
//     a module nested inside it is routed into another;
//   - a component named at one boundary while the sub-module containing it is
//     routed into a different one.
//
// Routing a module and naming one of its components under the SAME boundary is
// not a conflict: the module places the component and the named entry configures
// it, and both agree on where it belongs. Nesting a routing entry inside another
// under one boundary is likewise pure addressing.
//
// The diagnostic names BOTH declaration sites: which one to keep is the author's
// call, and a message that named only the second would send them hunting through
// the tree for the first. Walk order is author order — a boundary's own
// components, then the modules it routes, then its nested boundaries — so "first"
// and "second" are stable across runs and the message is byte-identical for the
// same input. First-violation-halt matches the document-shape rejections on the
// Manifest load path.
func rejectDuplicateBoundaryComponents(rel *types.Release, path string) error {
	if rel == nil {
		return nil
	}
	entity := "Release"
	if rel.Metadata.Name != "" {
		entity = fmt.Sprintf("Release %s", rel.Metadata.Name)
	}
	// Recorded in walk order rather than in a map, so the prior site a conflict
	// names is the earliest one in author order and never depends on map ordering.
	var placed []boundaryPlacement

	check := func(current boundaryPlacement) error {
		for _, prior := range placed {
			if !placementsConflict(prior, current) {
				continue
			}
			return &ResolverError{
				Entity:  entity,
				Field:   releaseComponentFieldPath(current.boundaryPath, current.locator, current.synthesized),
				Message: placementConflictMessage(prior, current, path),
				Err:     ErrDuplicateBoundaryComponent(),
			}
		}
		placed = append(placed, current)
		return nil
	}

	var walk func(boundaryPath string, b types.Boundary) error
	walk = func(boundaryPath string, b types.Boundary) error {
		// Declarations rather than the reconciled component view: the view folds two
		// entries resolving to one id together, which is precisely the collision this
		// guard exists to see.
		for _, decl := range types.BoundaryComponentDeclarations(b) {
			if err := check(boundaryPlacement{
				key:          decl.ID,
				boundaryPath: boundaryPath,
				locator:      decl.Locator,
				synthesized:  b.Synthesized,
			}); err != nil {
				return err
			}
		}
		for _, module := range types.BoundaryRoutedModules(b) {
			if err := check(boundaryPlacement{
				key:          module,
				boundaryPath: boundaryPath,
				locator:      routedModuleLocator(module),
				synthesized:  b.Synthesized,
				module:       true,
			}); err != nil {
				return err
			}
		}
		for childName, child := range b.Boundaries.All() {
			childPath := childName
			if boundaryPath != "" {
				childPath = boundaryPath + "/" + childName
			}
			if err := walk(childPath, child); err != nil {
				return err
			}
		}
		return nil
	}
	for name, b := range rel.Spec.Boundaries.All() {
		if err := walk(name, b); err != nil {
			return err
		}
	}
	return nil
}

// routedModuleLocator renders the authoring key path of a routed sub-module,
// whose dotted prefix corresponds to one `aggregates` key per segment.
func routedModuleLocator(module string) []string {
	segments := strings.Split(module, ".")
	locator := make([]string, 0, len(segments)*2)
	for _, segment := range segments {
		locator = append(locator, "aggregates", segment)
	}
	return locator
}

// placementsConflict reports whether two placements put one node in two
// boundaries. Two placements at the same boundary never conflict — they agree on
// where the node belongs, whichever axes they came from.
func placementsConflict(a, b boundaryPlacement) bool {
	if a.boundaryPath == b.boundaryPath {
		// One id declared twice under one boundary is still a fault: two entries
		// carry payloads for one node and nothing decides between them.
		return !a.module && !b.module && a.key == b.key
	}
	switch {
	case a.module && b.module:
		// Equality is what fires in practice: reaching a nested module requires an
		// entry for the module it nests under, which routes that outer module too, so
		// two boundaries meeting at a nested module meet at its ancestor first. The
		// containment test stands behind that, holding if the routed-module report
		// ever narrows to the modules an entry names rather than every one it implies.
		return types.IsUnderModulePrefix(a.key, b.key) || types.IsUnderModulePrefix(b.key, a.key)
	case a.module:
		return types.IsUnderModulePrefix(b.key, a.key)
	case b.module:
		return types.IsUnderModulePrefix(a.key, b.key)
	default:
		return a.key == b.key
	}
}

// placementConflictMessage phrases the halt in the terms of whichever axes the two
// placements came from, so the author reads about the thing they wrote — a
// component, a routed sub-module, or one of each.
func placementConflictMessage(prior, current boundaryPlacement, source string) string {
	switch {
	case prior.module && current.module:
		return fmt.Sprintf(
			"sub-module %q is routed into boundary %q and sub-module %q into boundary %q (source %s); routing one sub-module into a boundary places every component it contains there, so these two placements claim the same components — remove whichever routing is not intended",
			prior.key, prior.boundaryPath, current.key, current.boundaryPath, source,
		)
	case prior.module:
		return fmt.Sprintf(
			"component %q is declared in boundary %q while the sub-module %q that contains it is routed into boundary %q (source %s); a component belongs to exactly one boundary — either route the sub-module into %q or drop the component declaration",
			current.key, current.boundaryPath, prior.key, prior.boundaryPath, source, current.boundaryPath,
		)
	case current.module:
		return fmt.Sprintf(
			"sub-module %q is routed into boundary %q while its component %q is declared in boundary %q (source %s); routing a sub-module into a boundary places every component it contains there, so the two placements disagree — remove whichever one is not intended",
			current.key, current.boundaryPath, prior.key, prior.boundaryPath, source,
		)
	default:
		return fmt.Sprintf(
			"component %q is declared in boundary %q and again in boundary %q (source %s); a component belongs to exactly one boundary — remove whichever declaration is not intended",
			current.key, prior.boundaryPath, current.boundaryPath, source,
		)
	}
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
			Err:     ErrYAMLLoad(),
		}
	}
	return LoadManifestFromBytes(data, path)
}

// LoadManifestFromBytes unmarshals a Manifest from already-read bytes and runs
// the same reserved-prefix and cross-binding validations as LoadManifest,
// attributing any error to path. It is the read-free core of LoadManifest, for
// callers that already hold the bytes (the same-directory triad scan).
func LoadManifestFromBytes(data []byte, path string) (*types.Manifest, error) {
	if _, eerr := envelope.ValidateBytes(envelope.KindManifest, data, path); errors.Is(eerr, envelope.ErrEntityEnvelope()) {
		return nil, &ResolverError{Entity: "Manifest", Field: path, Message: eerr.Error(), Err: eerr}
	}
	var man types.Manifest
	if err := yaml.Unmarshal(data, &man); err != nil {
		return nil, &ResolverError{
			Entity:  "Manifest",
			Field:   path,
			Message: fmt.Sprintf("failed to unmarshal YAML: %v", err),
			Err:     ErrYAMLLoad(),
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
	reservedPrefix := types.ReservedLabelPrefixAggregaAI
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
					Err: ErrManifestReservedPrefix(),
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
							Err: ErrManifestCrossBindingPillarCollision(),
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
