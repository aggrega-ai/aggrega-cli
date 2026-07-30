// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package compilerops is the shared compiler-operations layer that language
// front ends drive to run the compiler pipeline against explicit inputs. It
// single-sources the three request-scoped operations a front end needs —
// resolving a Release's catalog closure, running the semantic-validation
// pipeline, and building the IR graph to an in-memory buffer — so every front
// end runs one implementation rather than its own copy.
//
// The layer takes explicit inputs (a workspace root and configuration, the
// Release path, a request-scoped catalog lookup view) and returns plain
// results and typed errors. It holds no session state and reads no front-end
// fields: a caller assembles the inputs from whatever state it holds and adapts
// the plain results back into its own wire shapes. The layer performs no
// filesystem writes — BuildToBuffer returns rendered artifacts in memory.
package compilerops

import (
	"fmt"
	"path/filepath"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// ClosureInputs carries the explicit state ResolveClosure needs. View is the
// request-scoped catalog lookup the closure walk resolves against — seeded with
// this request's pins, so a cache-resident entity resolves against the right
// pin set and another concurrent request's pins can never interleave into this
// walk. ReleasePath is the absolute path of the Release whose siblings and
// closure are resolved; WorkspaceRoot and WorkspaceConfig supply the closure
// walk's workspace anchor and resolved artifact-cache directory.
//
// Lockfile, Augment, and AfterManifest are per-caller opt-ins; their zero values
// preserve the base resolution shape (no pin gate, no library-axis fold, no
// hook).
type ClosureInputs struct {
	View            *catalog.LazyLookup
	ReleasePath     string
	WorkspaceRoot   string
	WorkspaceConfig workspaceconfig.Config

	// Lockfile, when non-nil and carrying a registry pin (a real resolved
	// version) for the Release's top-level Aggregate, suppresses seeding that
	// Aggregate as an external entity. A registry-pinned top-level resolves
	// through the view's pinned tier; seeding it external — externals are
	// consulted first — would shadow it with a version-less pin and drift against
	// the pin set. A nil Lockfile skips the gate, leaving the Aggregate seeded
	// external. The Manifest is always external; it has no registry tier.
	Lockfile *lockfile.Lockfile

	// Augment, when true, folds the cross-document library axes into the computed
	// closure — the SchemaLibrary and MappingLibrary references the entity walk
	// does not descend — so the resolved closure matches the one a build or lint
	// verifies against. false leaves the closure as ComputeClosure produced it,
	// which is what a caller that folds the library axes separately needs so the
	// fold does not run twice.
	Augment bool

	// AfterManifest, when non-nil, runs after the Manifest's extends chain is
	// composed and before the top-level Aggregate resolves, receiving the composed
	// Manifest and its source path. A caller injects a manifest-level consistency
	// gate here so it fires at the earliest loud point — before the closure walk. A
	// returned error aborts resolution with a compose-stage ClosureError. nil skips
	// the hook.
	AfterManifest func(manifest *types.Manifest, manifestPath string) error
}

// ResolvedClosure bundles the entities a build or drift flow needs together —
// the parsed Release, its resolved Manifest and Aggregate, the raw Aggregate
// bytes the resolver interpolates, the Aggregate's source path, and the
// computed catalog closure.
type ResolvedClosure struct {
	Release       *types.Release
	Manifest      *types.Manifest
	Aggregate     *types.Aggregate
	AggBytes      []byte
	AggregatePath string
	Closure       closure.Closure
	// Lookup is the hermetic Lookup (nil OCIPuller) the closure was walked
	// through. A caller that verifies drift against a lockfile reuses it to
	// reconstruct the sub-aggregate and plugin axes ComputeClosure leaves empty,
	// so the reconstruction resolves against the identical pinned view — no second
	// lookup is built. Callers that only render may ignore it.
	Lookup closure.Lookup
}

// ClosureStage names the point in ResolveClosure at which resolution failed, so
// a caller can shape a stage-specific diagnostic (which path to attribute the
// failure to, whether to annotate with catalog-skip context).
type ClosureStage int

const (
	// ClosureStageLoadRelease marks a failure loading the Release document.
	ClosureStageLoadRelease ClosureStage = iota
	// ClosureStageResolveManifest marks a failure resolving the Manifest sibling.
	ClosureStageResolveManifest
	// ClosureStageComposeManifest marks a failure composing the Manifest extends chain.
	ClosureStageComposeManifest
	// ClosureStageLoadAggregate marks a failure loading the top-level Aggregate.
	ClosureStageLoadAggregate
	// ClosureStageComputeClosure marks a failure walking the catalog closure.
	ClosureStageComputeClosure
	// ClosureStageManifestReferenceShape marks a Manifest mapping value whose
	// shape a recipe reference cannot express. It is its own stage because the
	// fault belongs to the Manifest and is decided before any lookup runs, so a
	// caller attributing it to the closure walk would name the wrong document.
	ClosureStageManifestReferenceShape
)

// ClosureError reports a ResolveClosure failure. Stage names which step failed;
// the message carries the same detail the CLI renders for that step. A caller
// that needs catalog-skip annotation applies it to the ComputeClosure stage's
// message.
type ClosureError struct {
	Stage ClosureStage
	msg   string
	err   error
}

// Error returns the rendered failure detail for the failing stage.
func (e *ClosureError) Error() string { return e.msg }

// Unwrap returns the underlying resolution error.
func (e *ClosureError) Unwrap() error { return e.err }

// ResolveClosure loads the Manifest and Aggregate siblings to the Release at
// in.ReleasePath and computes the catalog closure over the request-scoped view.
//
// The top-level Aggregate is resolved through the request-scoped view's
// aggregate lookup — a same-directory scan, a `./`/`../` file, or the catalog
// tier the view exposes — so a co-located or a catalog-resident top-level
// Aggregate both resolve against the request's pin set.
//
// The closure compute uses a hermetic lookup (nil puller): registry pull never
// fires during resolution; a missing cache entry surfaces as a closure-walk
// failure rather than an out-of-band network fetch. A broken out-of-closure
// entity is never enumerated-fatal.
//
// The optional inputs shape the resolution: a non-nil in.Lockfile gates the
// external Aggregate seed on a registry pin; in.AfterManifest runs a
// manifest-level hook before the Aggregate resolves; in.Augment folds the
// cross-document library axes into the returned closure. Their zero values leave
// the base resolution unchanged.
//
// On failure the returned *ClosureError names the failing stage and carries the
// rendered detail; the *ResolvedClosure is nil. On success the error is nil.
func ResolveClosure(in ClosureInputs) (*ResolvedClosure, *ClosureError) {
	rel, err := resolver.LoadRelease(in.ReleasePath)
	if err != nil {
		return nil, &ClosureError{
			Stage: ClosureStageLoadRelease,
			msg:   fmt.Sprintf("load release %s: %v", in.ReleasePath, err),
			err:   err,
		}
	}
	releaseDir := filepath.Dir(in.ReleasePath)
	manRes, err := resolver.ResolveManifest(releaseDir, rel.Spec.Manifest)
	if err != nil {
		return nil, &ClosureError{
			Stage: ClosureStageResolveManifest,
			msg:   fmt.Sprintf("load manifest %q: %v", rel.Spec.Manifest, err),
			err:   err,
		}
	}
	manifest, err := resolver.ResolveManifestExtends(releaseDir, manRes.Manifest)
	if err != nil {
		return nil, &ClosureError{
			Stage: ClosureStageComposeManifest,
			msg:   fmt.Sprintf("compose manifest %q extends chain: %v", rel.Spec.Manifest, err),
			err:   err,
		}
	}
	if in.AfterManifest != nil {
		if herr := in.AfterManifest(manifest, manRes.Path); herr != nil {
			return nil, &ClosureError{
				Stage: ClosureStageComposeManifest,
				msg:   herr.Error(),
				err:   herr,
			}
		}
	}
	// Reference shape is judged here, on the composed Manifest, before anything
	// looks a mapping value up. It is unconditional rather than a per-caller
	// opt-in: the verdict is syntactic, every front end that computes a closure
	// needs it, and hanging it off the AfterManifest hook would cover the one
	// caller that wires that hook and leave the rest reporting a present recipe
	// as absent. Only the first fault is reported, because the closure walk that
	// follows would halt on it anyway.
	if faults := catalog.ManifestRecipeReferenceFaults(manifest); len(faults) > 0 {
		return nil, &ClosureError{
			Stage: ClosureStageManifestReferenceShape,
			msg:   faults[0].Error(),
			err:   faults[0],
		}
	}
	aggRes, err := resolver.ResolveAggregate(releaseDir, rel.Spec.Aggregate, in.View.AggregateLookup(), nil)
	if err != nil {
		return nil, &ClosureError{
			Stage: ClosureStageLoadAggregate,
			msg:   fmt.Sprintf("load aggregate %q: %v", rel.Spec.Aggregate, err),
			err:   err,
		}
	}
	aggregate := aggRes.Aggregate
	aggBytes := aggRes.Bytes
	// A registry-pinned top-level Aggregate resolves through the view's pinned
	// tier; seeding it external — externals are consulted first — would shadow it
	// with a version-less pin and drift against the pin set. Gate the external seed
	// on the pin. The Manifest is always external (it has no registry tier).
	aggExternal := aggRes.Bytes
	if in.Lockfile != nil && lockfileHasRegistryPin(in.Lockfile, rel.Spec.Aggregate) {
		aggExternal = nil
	}
	externals := lockfile.ExternalEntitiesFromBytes(rel, aggExternal, manRes.Bytes)
	cacheDir := in.WorkspaceConfig.ResolvedArtifactCacheDir()
	lookup := in.View.ClosureLookup(nil, in.WorkspaceRoot, cacheDir, externals)
	cl, err := closure.ComputeClosure(rel, manifest, aggregate, in.WorkspaceConfig, lookup)
	if err != nil {
		return nil, &ClosureError{
			Stage: ClosureStageComputeClosure,
			msg:   fmt.Sprintf("closure compute: %v", err),
			err:   err,
		}
	}
	// Fold the cross-document library axes into the closure (the SchemaLibrary and
	// MappingLibrary references the entity walk does not descend) so the resolved
	// closure matches the one a build or lint verifies against. Opt-in: a caller
	// that folds these axes separately leaves it off so the fold does not run twice.
	if in.Augment {
		if aerr := closure.AugmentClosure(&cl, in.View, lookup); aerr != nil {
			return nil, &ClosureError{
				Stage: ClosureStageComputeClosure,
				msg:   fmt.Sprintf("closure augment: %v", aerr),
				err:   aerr,
			}
		}
	}
	return &ResolvedClosure{
		Release:       rel,
		Manifest:      manifest,
		Aggregate:     aggregate,
		AggBytes:      aggBytes,
		AggregatePath: aggRes.Path,
		Closure:       cl,
		Lookup:        lookup,
	}, nil
}

// lockfileHasRegistryPin reports whether the lockfile pins uri as a registry
// entity — one carrying a real resolved version. A version-less pin marks an
// external (local) top-level; a real version marks one registry-resolved at pull
// that must resolve through the view's pinned tier, not the external tier (which
// would re-pin version "" and drift against the lockfile).
func lockfileHasRegistryPin(lf *lockfile.Lockfile, uri string) bool {
	if lf == nil {
		return false
	}
	for _, e := range lf.Entities {
		if e.URI == uri {
			return e.ResolvedVersion != ""
		}
	}
	return false
}
