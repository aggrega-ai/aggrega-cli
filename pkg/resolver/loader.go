// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
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
//
// # The catalog's structural read is a DELIBERATE near-duplicate of this one
//
// The catalog index reads Aggregate bytes through the same three steps — the
// envelope gate, the tolerant parse, the typed decode — and folds the result
// through the same normalization core. They are two functions on purpose, and
// three things keep them apart rather than one being an unmerged copy.
//
// Error classification. A failure met here is charged to the author of the
// Aggregate and reaches a reader as a developer-domain fault; a failure met on
// the catalog index is charged to the catalog. One set of malformed bytes
// therefore reaches a reader as two different authoring faults depending on
// which read met them, and that is the answer that names the surface whose
// author can act, not an inconsistency between two copies.
//
// The kind check. The envelope gate below asserts the document is an Aggregate,
// and this read stops there, because its caller already knows what it asked
// for. The catalog read asserts the decoded kind again, because it is indexing
// whatever a directory walk handed it and the decode is what tells it which
// index the entity belongs in.
//
// The normalization fold. The fold is the one thing the two must never differ
// on, and neither carries a copy of it: both call [types.Normalize], so the
// shape every downstream consumer sees cannot depend on which read produced it.
// What is duplicated is the wrapper, never the fold — and collapsing the
// wrapper would force one error classification onto both surfaces.
//
// What is NOT one of the three is a difference in error policy. Both refuse
// every failure and return it; neither skips, degrades, or yields a partial
// value. The envelope gate below matches on the envelope sentinel rather than
// on any error, which reads like the narrower of two policies and is not:
// [envelope.ValidateBytes] states, and implements, that every error it returns
// carries that sentinel, so the two reads refuse exactly the same documents. A
// caller that carries on past one of these failures — the same-directory scan
// recording a skip — is applying its own policy to the error it was handed
// rather than reading one out of either read.
func LoadAggregateFromBytes(data []byte, path string) (*types.Aggregate, error) {
	if _, eerr := envelope.ValidateBytes(envelope.KindAggregate, data, path); errors.Is(eerr, envelope.ErrEntityEnvelope()) {
		// No Message: the envelope failure is already a composed diagnostic for
		// this same condition, naming the kind, the file and every finding along
		// it. Restating it here would state the condition's sentence twice.
		return nil, &ResolverError{Entity: "Aggregate", Field: path, Err: eerr}
	}
	node, perr := envelope.ParseDecodable(data, path)
	if perr != nil {
		// Structurally unreachable, and kept marked rather than removed. The gate
		// above refuses every error ValidateBytes returns, and ValidateBytes
		// reaches a successful return only by obtaining a parse of these same
		// bytes — its own generic decode, or this very call where that decode
		// failed. These bytes have therefore parsed once already, and this parse
		// is a deterministic function of them.
		//
		// The identical arm stands at the catalog's structural read, for the
		// identical reason. Whether the two should stay two gates or collapse into
		// one is open and is not settled here: ValidateBytes already returns what
		// it parsed, so handing back a decodable node instead would remove the
		// second parse at both sites together — a change to its signature and to
		// every one of its callers, which is why it is named rather than taken.
		// Both sites carry this note meanwhile, so neither reads as a live failure
		// path the other lacks.
		//
		// What it would report is left exactly as it arrives: the parse failure
		// already names what failed and which file it was reading, and restating
		// that here would tell the reader twice, in two different vocabularies,
		// that one parse of one file did not succeed.
		return nil, &ResolverError{
			Entity:  "Aggregate",
			Field:   path,
			Message: perr.Error(),
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
		// No Message: see LoadAggregateFromBytes.
		return nil, &ResolverError{Entity: "Release", Field: path, Err: eerr}
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
// rejected at load time, and every offending entry is named rather than
// only the first. The failure is attributed to the Manifest, which is what
// routes it to the catalog-authoring domain. The
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
		// No Message: see LoadAggregateFromBytes.
		return nil, &ResolverError{Entity: "Manifest", Field: path, Err: eerr}
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

// rejectReservedAnnotationBindings asks the reserved-prefix rule of every
// annotation binding the Manifest at path declares, and refuses the load when
// any of them claims a key in the reserved keyspace.
//
// It presents the document and names the rule rather than walking the registry
// itself: the rule is declared in this package as a constraint, so where it runs
// follows from the fact it reads, and the provenance its findings carry is
// supplied by the framework rather than assembled here. The binding keys are read
// back off the presentation rather than off the Manifest beside it — they are one
// of the facts the presentation states, so reading them anywhere else would be
// answering from a second source that happens to agree.
//
// 🔴 It reports EVERY offending binding rather than the first. The rule is
// invoked once per declared binding and the framework accumulates what each
// invocation raised, so an author fixing a Manifest with several offending keys
// sees all of them in one build instead of one per build.
//
// The findings are joined, so a caller matching the refusal by identity keeps
// matching however many bindings offended.
func rejectReservedAnnotationBindings(man *types.Manifest, path string) error {
	var presented constraint.DocumentView = newManifestDocumentView(path, man)
	findings := findingsAtTheirOwnDocument(
		constraint.RunOnDocumentAnnotationBindings(
			path,
			presented.ManifestAnnotationBindingKeys(path),
			presented,
			annotationBindingKeyAvoidsTheReservedPrefix,
		),
		"Manifest",
		"Manifest",
	)
	if len(findings) == 0 {
		return nil
	}
	joined := make([]error, 0, len(findings))
	for _, f := range findings {
		joined = append(joined, f)
	}
	return errors.Join(joined...)
}

// rejectCrossBindingPathPillarCollision asks the one-claim-per-routing-slot rule
// of every annotation binding the Manifest at path declares, and refuses the load
// when any of them claims a slot an earlier binding on the same Feature already
// holds.
//
// It presents the document and names the rule rather than walking the registry
// itself, for the reason [rejectReservedAnnotationBindings] states: the rule is
// declared in this package as a constraint, so where it runs follows from the
// facts it reads, and the provenance its findings carry is supplied by the
// framework rather than assembled here.
//
// 🔴 It reports EVERY binding that collides rather than the first. The rule is
// invoked once per declared binding and the framework accumulates what each
// invocation raised, so an author fixing a Manifest whose third and fourth
// bindings both collide with its first sees both in one build instead of one per
// build.
//
// The findings are joined, so a caller matching the refusal by identity keeps
// matching however many bindings collided.
func rejectCrossBindingPathPillarCollision(man *types.Manifest, path string) error {
	var presented constraint.DocumentView = newManifestDocumentView(path, man)
	findings := findingsAtTheirOwnDocument(
		constraint.RunOnDocumentAnnotationBindings(
			path,
			presented.ManifestAnnotationBindingKeys(path),
			presented,
			annotationBindingRouteIsClaimedOnce,
		),
		"Manifest",
		"Manifest",
	)
	if len(findings) == 0 {
		return nil
	}
	joined := make([]error, 0, len(findings))
	for _, f := range findings {
		joined = append(joined, f)
	}
	return errors.Join(joined...)
}
