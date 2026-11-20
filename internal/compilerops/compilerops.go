// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

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
	"maps"
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

	// Augment, when true, folds into the computed closure what the entity walk does
	// not descend — the extends bases, then the SchemaLibrary and MappingLibrary
	// references — so the resolved closure matches the one a build or lint
	// verifies against. false leaves the closure as ComputeClosure produced it,
	// which is what a caller that folds the library axes separately needs so the
	// fold does not run twice.
	Augment bool
}

// ResolvedClosure bundles the entities a build or drift flow needs together —
// the parsed Release, its resolved Manifest and Aggregate, the raw Aggregate
// bytes the resolver interpolates, the Aggregate's source path, the Manifest's
// file, and the computed catalog closure.
//
// The closure names the file of each instance the Release and its Aggregate
// declare, relative to the workspace root (closure.Closure.NameInstanceFiles), and
// ManifestPath names the Manifest's file the same way, so a finding about an
// instance's version names each file a constraint on it is written in.
type ResolvedClosure struct {
	Release       *types.Release
	Manifest      *types.Manifest
	Aggregate     *types.Aggregate
	AggBytes      []byte
	AggregatePath string
	// ManifestPath is the file of the Manifest the Release resolves to, relative
	// to the workspace root where it lies inside it.
	ManifestPath string
	Closure      closure.Closure
	// LibrariesResolved records that the closure was resolved with its library
	// axes folded in (ClosureInputs.Augment), so Closure names each library at the
	// version this Release resolved. A closure resolved without them names no
	// library, and Libraries reads the default selection for it instead.
	LibrariesResolved bool
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

	// ReleaseCompositionAdvisories carries one finding per reading the fold of the
	// Release's extends chain advised about — today, a boundary whose output
	// directory the Release inherited and which carries no template variable, so
	// it resolves to the path the extended Release writes to.
	//
	// They are carried rather than returned as a failure because none of them is
	// one: each is a shape an author may legitimately mean, and the rule that
	// produced it declares warning severity for that reason. It is empty for a
	// Release that heads no chain, and for a chain the readings cleared.
	//
	// Every caller owes a verdict, on the same terms the Manifest findings above
	// state: a caller that emits an artifact reports them beside the build, a
	// caller reporting on a document lists them with its other findings. Neither
	// may halt on them.
	ReleaseCompositionAdvisories []error
}

// InstanceVersionLookup returns lookup scoped to the version every component
// instance of this Release compiles at (catalog.ScopeToInstanceVersions), read
// from view: the highest version every constraint declared on the instance
// admits, among the versions view offers — those its catalog directories hold, a
// lockfile's row where view holds a reference to one (catalog.HeldToLockfile), or
// those a registry lists where view is the one a pull resolves through
// (catalog.LazyLookup.ResolutionVersions). It is the one place a compilation of a
// Release settles those versions, and every surface that compiles one hands the
// pipeline the lookup it returns.
//
// It refuses where an instance's constraints admit no version the view holds,
// with the conflicting-version-constraints condition, and the compilation is not
// run: compiling the instance at a version one of its constraints refuses would
// render it through a Recipe written for another.
//
// The instances are the closure's, so a module's instances are among them once
// the sub-aggregate descent has run over the closure: a build's reconstruction
// from a lockfile runs it, and ResolveModuleReferences runs it where there is no
// lockfile.
func (r *ResolvedClosure) InstanceVersionLookup(lookup resolver.CatalogLookup, view catalog.InstanceVersionView) (resolver.CatalogLookup, error) {
	return instanceVersionLookup(lookup, view, r.Release, r.Manifest, r.ManifestPath, r.Closure)
}

// instanceVersionLookup is InstanceVersionLookup over explicit inputs, for a
// caller that holds the resolution's parts rather than the resolution.
func instanceVersionLookup(lookup resolver.CatalogLookup, view catalog.InstanceVersionView, release *types.Release,
	manifest *types.Manifest, manifestPath string, cl closure.Closure) (resolver.CatalogLookup, error) {
	var name string
	if release != nil {
		name = release.Metadata.Name
	}
	table, err := catalog.ResolveInstanceVersions(name, cl.Instances, manifest, manifestPath, view)
	if err != nil {
		return resolver.CatalogLookup{}, err
	}
	return catalog.WithHeldBackDetail(catalog.ScopeToInstanceVersions(lookup, table), lookup, table, view), nil
}

// Libraries returns the libraries a build or preview of this Release reads, one
// version per library name: each at the version the closure resolved where the
// closure was resolved with its library axes, and at the version a bare reference
// selects where it was not.
func (r *ResolvedClosure) Libraries(view *catalog.LazyLookup) catalog.LibrarySet {
	return releaseLibraries(view, r.Closure, r.LibrariesResolved, nil)
}

// releaseLibraries is the one choice of the library versions a Release's surfaces
// read.
//
// A closure resolved with its library axes (librariesResolved) names each library
// at the version it resolved, and is read strictly: a resolved version the view
// does not hold is absent, never replaced by another, so the reader reports the
// library missing. That reading belongs to the surfaces that build, pull or
// preview, whose drift gate refuses a lockfile that no longer matches the
// workspace.
//
// A closure resolved without them names no library. Where a lockfile is in hand
// its rows overlay the default selection: a library the lockfile records at a
// version the view holds is read at exactly that version, the version a build
// reads, and every other library (one the lockfile lacks, or records at a version
// the view does not hold) is read at the version a bare reference selects. The
// surfaces that read this way either run the drift check before validating or,
// keeping a quiet load on every edit, run it once a gate over the dependencies has
// refused on account of the recorded versions (LibraryRecordExplainsGateRefusal)
// and report the drift in place of that refusal, so a stale lockfile is reported
// by the drift check and never as a library the catalog holds being missing. With
// no lockfile every library is read at the version a bare reference selects.
func releaseLibraries(view *catalog.LazyLookup, cl closure.Closure, librariesResolved bool, record *lockfile.Lockfile) catalog.LibrarySet {
	if librariesResolved {
		return view.ClosureLibraries(cl)
	}
	libraries := view.DefaultLibraries()
	if record == nil {
		return libraries
	}
	for _, axis := range closure.LibraryAxes() {
		rows, _ := lockfile.LibraryPins(record, axis)
		libs := make([]closure.ResolvedLibrary, 0, len(rows))
		for _, row := range rows {
			libs = append(libs, closure.ResolvedLibrary{URI: row.URI, ResolvedVersion: row.ResolvedVersion, Digest: row.Digest})
		}
		axis.SetLibraries(&cl, libs)
	}
	recorded := view.ClosureLibraries(cl)
	maps.Copy(libraries.Schemas, recorded.Schemas)
	maps.Copy(libraries.Mappings, recorded.Mappings)
	maps.Copy(libraries.Workloads, recorded.Workloads)
	maps.Copy(libraries.Artifacts, recorded.Artifacts)
	return libraries
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
	// ClosureStageResolveReleaseExtends marks a failure following the Release's
	// own extends chain. It is its own stage because the fault belongs to the
	// Release and is decided before any sibling is looked for, so a caller
	// attributing it to the Manifest or the Aggregate would name the wrong
	// document — and a chain that cannot be followed is exactly the case in which
	// those two pointers are the ones still missing.
	ClosureStageResolveReleaseExtends
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
// The Release's own extends chain is followed first, ahead of either sibling, so
// a chain that loops, runs past the depth bound or names a parent that cannot be
// resolved is reported against the Release rather than as a sibling pointer that
// arrived empty. The returned closure records every Release file that chain read,
// by content, so a drift comparison against a lockfile notices an edit to any of
// them.
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
// The view's catalog directories are judged before the view is asked anything
// else: a link leading out of the root one is held to halts the resolution at the
// closure-walk stage, naming the link, before any name is resolved through the
// catalog (catalog.LazyLookup.CheckLinks). The Release, its extends chain and its
// Manifest are read first, because none of them is resolved through the view.
//
// On failure the returned *ClosureError names the failing stage and carries the
// rendered detail; the *ResolvedClosure is nil. On success the error is nil and
// the caller owes a verdict on ManifestConsistencyFindings.
func ResolveClosure(ctx context.Context, in ClosureInputs) (*ResolvedClosure, *ClosureError) {
	authored, err := resolver.LoadResolvedRelease(in.ReleasePath)
	if err != nil {
		return nil, &ClosureError{
			Stage: ClosureStageLoadRelease,
			msg:   fmt.Sprintf("load release %s: %v", in.ReleasePath, err),
			err:   err,
		}
	}
	rel := authored.Release
	releaseDir := filepath.Dir(in.ReleasePath)
	// The Release files the composition below reads, recorded by content so the
	// drift comparison notices an edit to any of them. They are read ahead of the
	// composition's own walk for the reason the pull gives: a file edited mid-run
	// is then compared as it stood before the edit, which halts rather than
	// passing. A failure is the composition's own, raised by the same walk, so it
	// is reported at the composition's stage and in its words.
	releaseDocs, derr := resolver.ReleaseDocuments(in.WorkspaceRoot, releaseDir, authored)
	if derr != nil {
		return nil, &ClosureError{
			Stage: ClosureStageResolveReleaseExtends,
			msg:   fmt.Sprintf("resolve release %q extends chain: %v", in.ReleasePath, derr),
			err:   derr,
		}
	}
	// The Release's own extends chain is followed before either sibling is looked
	// for. A chain that loops, runs past the depth bound, or names a parent that
	// cannot be resolved is a fault in the Release the caller named, and reporting
	// it here is what keeps it from surfacing one stage later as a sibling
	// reference the parent was going to supply — a diagnostic naming a pointer
	// that is empty rather than the chain that failed to fill it.
	//
	// The chain is followed AND folded here, and the result replaces the Release
	// every line below reads. That placement is what makes the fold reach every
	// front end: the Manifest and Aggregate pointers, the external-entity seed,
	// the closure walk and the Release this returns are all read from the value
	// this assignment produces, so a child that declares only a parent pointer
	// resolves its parent's siblings and topology rather than its own empty ones.
	//
	// A Release declaring no extends reference folds to itself, so the assignment
	// is a no-op for every Release that heads no chain.
	folded, releaseAdvisories, cerr := resolver.ResolveReleaseExtends(in.WorkspaceRoot, releaseDir, authored)
	if cerr != nil {
		return nil, &ClosureError{
			Stage: ClosureStageResolveReleaseExtends,
			msg:   fmt.Sprintf("resolve release %q extends chain: %v", in.ReleasePath, cerr),
			err:   cerr,
		}
	}
	rel = folded
	manRes, err := resolver.ResolveManifest(in.WorkspaceRoot, releaseDir, in.ReleasePath, rel.Spec.Manifest)
	if err != nil {
		return nil, &ClosureError{
			Stage: ClosureStageResolveManifest,
			msg:   fmt.Sprintf("load Manifest %q: %v", rel.Spec.Manifest, err),
			err:   err,
		}
	}
	manifest, err := resolver.ResolveManifestExtends(in.WorkspaceRoot, releaseDir, in.ReleasePath, manRes)
	if err != nil {
		return nil, &ClosureError{
			Stage: ClosureStageComposeManifest,
			msg:   fmt.Sprintf("compose Manifest %q extends chain: %v", rel.Spec.Manifest, err),
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
	if faults := resolver.ManifestRecipeReferenceFaults(manifest); len(faults) > 0 {
		return nil, &ClosureError{
			Stage: ClosureStageManifestReferenceShape,
			msg:   faults[0].Error(),
			err:   faults[0],
		}
	}
	// A link leading out of a catalog directory's root halts the resolution here,
	// where the view is first asked anything, and so before any name is resolved
	// through it. The view's walks pass such a link over unread, so a reference to
	// the entity it stands for would otherwise resolve another of the same name, or
	// report nothing behind a halt that names no link. Every use of the view below
	// comes after this one, and none of the steps above has any use for it.
	if err := in.View.CheckLinks(); err != nil {
		return nil, &ClosureError{
			Stage: ClosureStageComputeClosure,
			msg:   err.Error(),
			err:   err,
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
	aggRes, err := resolver.ResolveAggregate(in.WorkspaceRoot, releaseDir, in.ReleasePath, rel.Spec.Aggregate, in.View.AggregateLookup(), nil)
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
	lookup := in.View.ClosureLookup(nil, in.WorkspaceRoot, cacheDir, externals, nil, nil)
	// The two failures below carry the resolution's own text and no head of their
	// own: that text already says what could not be resolved and where the
	// reference came from, and the editor's entry opens with the stage in public
	// words, so a head here would name the stage again in the code's words rather
	// than the reader's. Stage is what tells a caller which step it was.
	cl, err := closure.ComputeClosure(ctx, rel, manifest, types.Normalize(aggregate), in.WorkspaceConfig, lookup)
	if err != nil {
		return nil, &ClosureError{
			Stage: ClosureStageComputeClosure,
			msg:   err.Error(),
			err:   err,
		}
	}
	cl.ReleaseDocuments = releaseDocs
	// The walk is handed the Release and the Aggregate already loaded, so the files
	// their instances are written in are named here, where they were loaded.
	cl.NameInstanceFiles(catalog.RelativeToWorkspace(in.WorkspaceRoot, in.ReleasePath),
		catalog.RelativeToWorkspace(in.WorkspaceRoot, aggRes.Path))
	// Fold in what the entity walk does not descend — the bases the closure's
	// Components and Features extend, then the cross-document library axes (see
	// closure.AugmentClosure) — so the resolved closure matches the one a build or
	// lint verifies against. Opt-in: a caller that folds these separately leaves it
	// off so the fold does not run twice.
	//
	// A reference a module writes too is settled over every instance writing it
	// first (closure.SettleSharedInstanceTypes), so the fold reads the version its
	// row ends at, as a pull folding the same Release reads it.
	if in.Augment {
		loader, lerr := closure.RequireAggregateLoader(lookup)
		if lerr != nil {
			return nil, &ClosureError{
				Stage: ClosureStageComputeClosure,
				msg:   lerr.Error(),
				err:   lerr,
			}
		}
		closure.SettleSharedInstanceTypes(ctx, &cl, loader, lookup)
		if aerr := closure.AugmentClosure(ctx, &cl, in.View, lookup); aerr != nil {
			return nil, &ClosureError{
				Stage: ClosureStageComputeClosure,
				msg:   aerr.Error(),
				err:   aerr,
			}
		}
	}
	return &ResolvedClosure{
		Release:                      rel,
		Manifest:                     manifest,
		Aggregate:                    aggregate,
		AggBytes:                     aggBytes,
		AggregatePath:                aggRes.Path,
		ManifestPath:                 catalog.RelativeToWorkspace(in.WorkspaceRoot, manRes.Path),
		Closure:                      cl,
		LibrariesResolved:            in.Augment,
		Lookup:                       lookup,
		ManifestConsistencyFindings:  consistencyFindings,
		ReleaseCompositionAdvisories: releaseAdvisories,
	}, nil
}

// lockfileHasRegistryPin reports whether the lockfile pins the Aggregate uri as a
// registry entity — one carrying a real resolved version. A version-less pin marks
// an external (local) top-level; a real version marks one registry-resolved at pull
// that must resolve through the view's pinned tier, not the external tier (which
// would re-pin version "" and drift against the lockfile). Only the Aggregate's
// own row answers: a Manifest sharing its name has a row of its own.
func lockfileHasRegistryPin(lf *lockfile.Lockfile, uri string) bool {
	if lf == nil {
		return false
	}
	want := closure.EntityKey{Kind: closure.KindAggregate, URI: uri}
	for _, e := range lf.Entities {
		if e.Key() == want {
			return e.ResolvedVersion != ""
		}
	}
	return false
}
