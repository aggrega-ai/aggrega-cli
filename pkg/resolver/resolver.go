// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"fmt"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
	"os"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"gopkg.in/yaml.v3"
)

// ResolveConfig configures the Step 1 resolver pipeline. All three paths are
// required; relative paths resolve against the working directory of the
// caller. The typical caller (cmd/aggrega/build.go) discovers AggregatePath
// and ManifestPath from a pre-loaded Release.
type ResolveConfig struct {
	AggregatePath string // path to Aggregate YAML file
	ReleasePath   string // path to Release YAML file
	ManifestPath  string // path to Manifest YAML file
}

// ResolveInputs configures the Step 1 resolver pipeline against caller-
// supplied in-memory inputs. Use this entry point when the Release and
// Manifest have already been parsed and the raw Aggregate YAML is held
// as a byte slice — the long-running daemon (which holds parse caches)
// and the internal/compiler/pipeline package take this shape. CLI callers
// hold paths and use the ResolveConfig surface instead.
//
// AggregatePath identifies the source location of AggregateRawBytes for
// error provenance and downstream source-location strings (Annotation
// expansion records the Aggregate's path in AnnotationProvenance entries).
// The resolve pipeline performs no filesystem operations against
// AggregatePath; it is a label only.
type ResolveInputs struct {
	Release           *types.Release
	Manifest          *types.Manifest
	AggregateRawBytes []byte
	AggregatePath     string // source-location label only; no I/O
}

// ResolveResult carries the artifacts produced by Step 1. The result is
// passed by pointer through topology, IR, and renderer; mutations after
// Resolve returns are not expected.
//
// Parameters carries the merged parameter map alive through the resolve
// scope so that orchestrator-time path-template evaluation can consume
// `.parameters.<key>` references in Release.spec.boundaries[].directory and
// subDirectory templates. ResolveAndUse owns the active-purge: it fires in a
// deferred PurgeParameters after the use callback (which runs the rest of the
// pipeline, render included) returns, so the renderer still sees live
// parameters and the map is zeroed before control leaves the scope. The purge
// mechanism (zero values, delete keys, nil pointer) is unchanged; see
// PurgeParameters. Value-template surfaces (Recipe valuesMapping, FR
// valuesMapping) do NOT receive `.parameters` — that pillar is reserved for
// path templates per the documented carve-out.
type ResolveResult struct {
	Aggregate     *types.Aggregate
	Release       *types.Release
	Manifest      *types.Manifest
	BoundaryInfra map[string]*BoundaryInfra // per-component boundary context

	// BoundaryAnnotations carries the renderer-facing Scalar
	// projection of the post-cascade annotation set per boundary
	// path. Only scalar annotation values surface here; structured
	// annotation values are excluded by construction (they
	// participate in the cascade but flow through Feature expansion
	// rather than template substitution). The effective set excludes
	// per-component overrides — those land in BoundaryInfra.Annotations
	// per-component. Consumers needing structured information — the
	// annotation-expansion phase, future per-subkey-routing surfaces —
	// read fresh from rr.Release.Spec.Boundaries.<...>.Annotations
	// rather than from this field.
	BoundaryAnnotations map[string]types.OrderedMap[string]

	Parameters    map[string]any // merged parameter map (alive through the resolve scope; purged when ResolveAndUse's callback returns)
	AggregatePath string         // Aggregate file path; carried so downstream phases (annotation expansion, inspect) can construct source-location strings for Aggregate-authored annotations without re-discovering it
	ReleasePath   string         // Release file path; carried so downstream phases (annotation expansion, inspect) can construct source-location strings without re-discovering it

	// AnnotationProvenance records, for every synthetic Feature
	// attachment produced by ExpandAnnotations, the annotation key
	// that triggered the expansion and the source location where
	// the annotation was authored. The IR builder consults this
	// side-table during Tier 5 (Release per-component Features)
	// processing to populate the IRFeatureEntry's Origin /
	// AnnotationKey / AnnotationSource fields. Explicit Feature
	// attachments do not appear in the map; their absence is the
	// "explicit" signal at the IR builder layer.
	//
	// Keyed via AnnotationProvenanceKey(boundaryPath,
	// componentName, featureName). The map is always non-nil after
	// Resolve returns; consumers can range or Get without nil
	// checks.
	AnnotationProvenance map[string]AnnotationProvenance
}

// Resolve executes the Step 1 pipeline against the configured paths and
// returns the resolved artifacts. The pipeline is:
//
//  1. Load Release.
//  2. Load Manifest.
//  3. Load Release.spec.parameters.$import: target if present (single-
//     source file; multi-source layering uses Release extends: chains).
//  4. Merge the loaded values with Release.spec.parameters inline content
//     when both are populated; in the current single-source-only design
//     exactly one mode is populated per Release.
//  5. Read raw Aggregate YAML bytes.
//  6. Interpolate the merged parameter map into the raw bytes via Go
//     template + Sprig (missingkey=zero, so undefined references render
//     as empty rather than failing).
//  7. Unmarshal the interpolated bytes into a typed Aggregate.
//  8. Validate user-authored instance names against the dot-free pattern.
//  9. Wire per-component boundary infra from the Release boundary tree.
//  10. Carry the merged parameter map through ResolveResult.Parameters
//     for downstream consumption (orchestrator-time path-template
//     evaluation). ResolveAndUse owns the active-purge, firing it after the
//     use callback returns, so path-template evaluation reads the live map
//     and the purge still runs by construction.
//
// Resolve takes a continuation: it loads the configured files, then delegates
// to ResolveAndUse so the result is scoped and purged like every other resolve
// path. It does not return a ResolveResult.
//
// Error semantics:
//
//   - Any I/O, unmarshal, or template error short-circuits the pipeline
//     and is returned wrapped in a ResolverError with the source entity
//     and path attached.
//   - Instance-name validation returns only the first violation — the name
//     pattern is a hard gate, and a single illegal name implies the
//     Aggregate cannot be processed further.
//   - Semantic guards (uses sockets, inject keys, requiredFeatures, etc.)
//     are NOT run by Resolve. Callers invoke RunGuards separately after
//     Resolve to obtain the catalog-aware checks; RunGuards collects all
//     violations rather than short-circuiting.
//
// Steps must run in this order: name validation operates on the resolved
// YAML, so it must follow interpolation and unmarshal; boundary infra
// wiring depends on the Release; the parameter purge must run after every
// step that consumes parameters.
func Resolve(cfg ResolveConfig, use func(*ResolveResult) error) error {
	release, err := LoadRelease(cfg.ReleasePath)
	if err != nil {
		return err
	}
	manifest, err := LoadManifest(cfg.ManifestPath)
	if err != nil {
		return err
	}
	rawAgg, err := os.ReadFile(cfg.AggregatePath)
	if err != nil {
		return &ResolverError{
			Entity:  "Aggregate",
			Field:   cfg.AggregatePath,
			Message: fmt.Sprintf("failed to read file: %v", err),
			Err:     ErrYAMLLoad,
		}
	}
	return ResolveAndUse(ResolveInputs{
		Release:           release,
		Manifest:          manifest,
		AggregateRawBytes: rawAgg,
		AggregatePath:     cfg.AggregatePath,
	}, func(result *ResolveResult) error {
		result.ReleasePath = cfg.ReleasePath
		return use(result)
	})
}

// ResolveAndUse is the single exported producer of a ResolveResult. It runs
// Step 1 against caller-supplied in-memory inputs and invokes use with the
// resolved artifacts, then purges the merged parameter map in a defer it owns,
// before returning. Parameters are reachable ONLY inside use's scope: a caller
// cannot obtain a live ResolveResult — and therefore cannot leak secret
// parameter values by forgetting to purge — because the result never escapes
// the callback. This is the structural security guarantee; the purge holds by
// construction, not by every caller remembering a deferred PurgeParameters.
//
// The purge runs on every path: use is invoked only when resolution succeeds,
// and the deferred purge fires whether use returns nil or an error. A resolve
// error short-circuits before use (no result is produced, nothing to purge).
//
// Higher layers compose through this entry rather than the unexported producer:
// internal/compiler/pipeline's ResolveAndValidate / ResolveAndFlatten call
// ResolveAndUse and thread their own continuations, so the whole pipeline —
// including render — runs inside the scope and the purge fires after it.
func ResolveAndUse(in ResolveInputs, use func(*ResolveResult) error) error {
	result, err := resolveFromInputs(in)
	if err != nil {
		return err
	}
	defer PurgeParameters(&result.Parameters)
	return use(result)
}

// resolveFromInputs executes the Step 1 pipeline against caller-supplied
// in-memory inputs. It is unexported by design: the only way to obtain a
// ResolveResult is through ResolveAndUse, which scopes the result to a
// callback and owns the parameter purge. Keeping the value-returning producer
// private is what makes the purge structural rather than conventional — no
// caller, in any package, can hold a live result and forget to purge it.
//
// ResolveResult.ReleasePath is left empty here because the caller already
// knows where the Release came from; the Resolve path fills it inside the
// ResolveAndUse callback.
func resolveFromInputs(in ResolveInputs) (*ResolveResult, error) {
	release := in.Release
	manifest := in.Manifest

	// Loud guardrail (Invariant 4): a Release born through the normalization core
	// never carries top-level components alongside a `default` boundary —
	// NormalizeRelease folds the root into the default and clears it, and the
	// authored-both shape is envelope-rejected. Reaching resolve in that state is
	// a normalization bypass (a hand-built literal, or a load path that skipped
	// the parse core). Assert here, at the earliest point every boundary consumer
	// (WireBoundaryInfra below, the release guards, the renderer) sits behind, so
	// none of them silently misses a components-only Release's components. An
	// internal-invariant breach, not an authoring error.
	if release != nil {
		if _, hasDefault := release.Spec.Boundaries.Get("default"); hasDefault && release.Spec.Components.Len() > 0 {
			return nil, fmt.Errorf("%w: construct releases through the normalization core (types.NormalizeRelease via the load path), not a hand-built literal", ErrUnnormalizedRelease)
		}
	}

	// Load parameters' $import: target if present (mode 2). Inline
	// content (mode 1) flows through MergeParameters directly.
	var externalParams map[string]any
	if release.Spec.Parameters.Import != "" {
		var err error
		externalParams, err = LoadParametersImport(release.Spec.Parameters.Import)
		if err != nil {
			return nil, err
		}
	}

	// Merge parameters
	params := MergeParameters(release.Spec.Parameters, externalParams)

	// Step 1a: validate merged parameter map against the Aggregate's
	// typed contract before interpolation. Shallow-parse the contract
	// from raw bytes; no I/O against in.AggregatePath.
	contract, err := LoadAggregateParameterContractFromBytes(in.AggregateRawBytes, in.AggregatePath)
	if err != nil {
		return nil, err
	}
	// Apply contract-declared defaults to slots the Release omitted, so
	// the contract validator and downstream template interpolation see
	// the resolved values. Three-case semantic for nested-object
	// parameters: omitted parent synthesizes children; explicit empty
	// parent suppresses children's defaults; partial parent merges.
	params = ApplyParameterDefaults(contract, params)
	if verrs := ValidateParametersAgainstContract(contract, params, in.AggregatePath); len(verrs) > 0 {
		return nil, aggregateParameterViolations(verrs)
	}

	// Interpolate the raw Aggregate bytes with the merged parameter map.
	interpolated, err := InterpolateParameters(in.AggregateRawBytes, params)
	if err != nil {
		return nil, err
	}

	// Validate the interpolated document's envelope before typed
	// unmarshal — post-interpolation is the final authored shape.
	if _, eerr := envelope.ValidateBytes(envelope.KindAggregate, interpolated, in.AggregatePath); errors.Is(eerr, envelope.ErrEntityEnvelope) {
		return nil, &ResolverError{Entity: "Aggregate", Field: in.AggregatePath, Message: eerr.Error(), Err: eerr}
	}

	// Unmarshal interpolated Aggregate
	var agg types.Aggregate
	if err := yaml.Unmarshal(interpolated, &agg); err != nil {
		return nil, &ResolverError{
			Entity:  "Aggregate",
			Field:   in.AggregatePath,
			Message: fmt.Sprintf("failed to unmarshal interpolated YAML: %v", err),
			Err:     ErrYAMLLoad,
		}
	}

	// Normalize the re-parsed (post-interpolation) Aggregate — the same step the
	// parse cores apply — and store the normalized value so flatten/IR see the
	// synthesized `default` subsystem. Normalize is non-mutating; the local agg
	// stays raw and is replaced by the normalized result.
	na := types.Normalize(&agg)
	agg = *na.Aggregate()

	// Validate instance names
	nameErrs := ValidateInstanceNames(na)
	if len(nameErrs) > 0 {
		return nil, nameErrs[0]
	}

	// Wire boundary infra (per-component) plus per-boundary annotations cascade.
	// release is already normalized at load; NormalizeRelease here is the
	// idempotent wrap producing the NormalizedRelease the walker requires.
	boundaryInfra, boundaryAnnotations := WireBoundaryInfra(types.NormalizeRelease(release))

	// Parameter map flows through to the renderer via ResolveResult.Parameters.
	// ResolveAndUse (the sole exported producer that wraps this function) owns
	// the active-purge in a deferred PurgeParameters that fires after its
	// callback returns, so every resolve path inherits one purge contract and
	// no caller carries a purge defer of its own.
	return &ResolveResult{
		Aggregate:            &agg,
		Release:              release,
		Manifest:             manifest,
		BoundaryInfra:        boundaryInfra,
		BoundaryAnnotations:  boundaryAnnotations,
		Parameters:           params,
		AggregatePath:        in.AggregatePath,
		AnnotationProvenance: map[string]AnnotationProvenance{},
	}, nil
}
