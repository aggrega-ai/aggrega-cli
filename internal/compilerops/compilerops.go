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
	"context"
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
// Lockfile and Augment are per-caller opt-ins; their zero values preserve the
// base resolution shape (no pin gate, no library-axis fold).
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
	// lookup is built.
	//
	// The sub-aggregate axis needs one capability beyond the resolver surface: a
	// read of a resolved Aggregate's parsed body. closure.RequireAggregateLoader
	// obtains it from this Lookup and refuses loudly when the Lookup cannot serve
	// it, so a caller cannot end up reconstructing that axis with no loader — which
	// the descent would read as "none wired" and skip without a word. Callers that
	// only render may ignore Lookup entirely.
	Lookup closure.Lookup

	// ManifestConsistencyFindings carries one finding per Manifest mapping whose
	// mapped type does not resolve to a recipe declaring it — directly or through a
	// base the mapped type extends. It is empty when every mapping is consistent.
	//
	// The findings are carried rather than returned as a failure because the
	// verdict differs by what the caller produces, not by what the gate found: a
	// caller that emits a deployable artifact joins them and halts, while a caller
	// that reports on a document enumerates them beside its other findings. A
	// failure at this position could not express that — resolution stops there, so
	// a reporting caller would be left with one line in place of everything else it
	// had to say about the document, at every keystroke on an editor surface.
	//
	// Every caller owes a verdict. An empty read is a legitimate one only where the
	// caller genuinely produces no report and no artifact.
	ManifestConsistencyFindings []error
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
// external Aggregate seed on a registry pin; in.Augment folds the cross-document
// library axes into the returned closure. Their zero values leave the base
// resolution unchanged.
//
// Two Manifest-level judgements run unconditionally on the composed Manifest,
// before the Aggregate resolves and before the closure walk: the reference-shape
// gate, which fails, and the mapping-table consistency gate, whose findings ride
// back on ResolvedClosure.ManifestConsistencyFindings for the caller to reach a
// verdict on. Neither is a caller opt-in — every front end reads the same
// Manifest, so a per-caller gate would leave the rest of them silent.
//
// On failure the returned *ClosureError names the failing stage and carries the
// rendered detail; the *ResolvedClosure is nil. On success the error is nil and
// the caller owes a verdict on ManifestConsistencyFindings.
func ResolveClosure(ctx context.Context, in ClosureInputs) (*ResolvedClosure, *ClosureError) {
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
	// Reference shape is judged here, on the composed Manifest, before anything
	// looks a mapping value up. It is unconditional rather than a per-caller
	// opt-in: the verdict is syntactic, every front end that computes a closure
	// needs it, and a caller-supplied hook would cover only the callers that wired
	// it and leave the rest reporting a present recipe as absent. Only the first
	// fault is reported, because the closure walk that follows would halt on it
	// anyway.
	if faults := catalog.ManifestRecipeReferenceFaults(manifest); len(faults) > 0 {
		return nil, &ClosureError{
			Stage: ClosureStageManifestReferenceShape,
			msg:   faults[0].Error(),
			err:   faults[0],
		}
	}
	// Mapping-table consistency is judged here too, at the same pre-closure-walk
	// moment and for the same reason: the composed Manifest is in hand and nothing
	// downstream has resolved against it yet. It is unconditional rather than a
	// per-caller opt-in — every front end that computes a closure reads the same
	// mapping table, so a caller-supplied hook would cover the callers that wire it
	// and leave the rest reporting a recipe that does not implement the mapped type
	// as though the mapping were sound.
	//
	// It runs AFTER the reference-shape gate above because it looks each mapping
	// value up, and a value whose shape a recipe reference cannot express has no
	// recipe to judge it against.
	//
	// The ancestry resolver comes from the view rather than from the recipe shell:
	// the view's own resolver reads the Component cascade, so a mapping onto a
	// base's Recipe is judged consistent. The shell carries no Component index and
	// would refuse every such mapping.
	//
	// Unlike the shape gate, this one produces findings rather than a failure. The
	// verdict belongs to the caller — see ResolvedClosure.ManifestConsistencyFindings.
	consistencyFindings := in.View.VerifyManifestImplementsConsistency(manifest, manRes.Path)
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
	cl, err := closure.ComputeClosure(ctx, rel, manifest, types.Normalize(aggregate), in.WorkspaceConfig, lookup)
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
		if aerr := closure.AugmentClosure(ctx, &cl, in.View, lookup); aerr != nil {
			return nil, &ClosureError{
				Stage: ClosureStageComputeClosure,
				msg:   fmt.Sprintf("closure augment: %v", aerr),
				err:   aerr,
			}
		}
	}
	return &ResolvedClosure{
		Release:                     rel,
		Manifest:                    manifest,
		Aggregate:                   aggregate,
		AggBytes:                    aggBytes,
		AggregatePath:               aggRes.Path,
		Closure:                     cl,
		Lookup:                      lookup,
		ManifestConsistencyFindings: consistencyFindings,
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
