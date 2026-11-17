// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pullops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parse"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/internal/dryrunreport"
	"github.com/aggrega-ai/aggrega-cli/internal/pluginfetchverify"
	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress/livetrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// Phase names a stage of the resolution a caller's progress surface can display.
// The values are the stages a caller can meaningfully show; they carry no
// ordering authority over the run, and a caller is free to map them onto a
// surface of its own with more or fewer rows.
type Phase int

const (
	// PhaseLoadCatalog is the opening of the catalog view the run resolves through,
	// and the check of the Manifest's mapping table against it.
	PhaseLoadCatalog Phase = iota
	// PhaseDependencies resolves the Aggregate the Release names — fetching it
	// from a registry where no local tier holds it — and decides which of the
	// pins an existing lockfile records this run honours.
	PhaseDependencies
	// PhaseClosure is the closure walk over the selected references.
	PhaseClosure
	// PhaseLibraries is the descent to extends bases, then library discovery.
	PhaseLibraries
	// PhaseValidate is the closure-scoped validation.
	PhaseValidate
	// PhaseSources is the per-component source descent.
	PhaseSources
	// PhaseExternalInputs is the render that discovers which external inputs the
	// Release consumes, and pins them.
	PhaseExternalInputs
	// PhaseLockfile is the lockfile write.
	PhaseLockfile
)

// Reporter carries a caller's diagnostic surface into the run. Every field is
// optional and a nil field is a no-op, so a surface with no terminal supplies
// none of them.
type Reporter struct {
	// Phase fires as the run enters each stage.
	Phase func(Phase)

	// Resolved is handed the resolution this run computed, at the point the run
	// reaches it and before the verify-or-write split, so a caller reporting what
	// each reference resolved to writes it in the same sequence the run produced
	// it. The same resolution is returned in Result, for a caller that wants it
	// after the fact rather than in order.
	//
	// It carries the resolution rather than rendered text because how a
	// per-reference report reads belongs to the surface a person is looking at,
	// and the in-memory resolution tier it reports on is not persisted in the
	// lockfile — so a caller that wants it has to be handed it here.
	Resolved func(cl closure.Closure)
}

func (r Reporter) phase(p Phase) {
	if r.Phase != nil {
		r.Phase(p)
	}
}

func (r Reporter) resolved(cl closure.Closure) {
	if r.Resolved != nil {
		r.Resolved(cl)
	}
}

// ResolveProviderAmbiguity settles which provider plugin renders a capability
// more than one plugin serves, by asking a person and recording the answer.
//
// It is the layer's one channel to a terminal, and it is optional for that
// reason: a caller with nobody watching supplies nil, and the ambiguity then
// halts loudly rather than blocking on an answer that will never come. Supplying
// it is the caller's declaration that a person is watching and can answer.
//
// candidates is the sorted candidate set the ambiguity was raised over; rw is
// the resolution a recorded answer is written against — the resolution rather
// than its root, so the answer lands in the config file this run read; wsCfg is
// updated in place so the re-resolution immediately following adopts the answer;
// out is the stream the question is put on.
type ResolveProviderAmbiguity func(target string, candidates []types.Plugin, rw *workspaceconfig.ResolvedWorkspace, wsCfg *workspaceconfig.Config, out io.Writer) error

// Config is the dependency-resolution operation's input contract. A caller
// assembles it from whatever state it holds — flag values, a request payload —
// and hands it to Run.
type Config struct {
	// Workspace is the resolved workspace the caller threads in. Required: Run
	// refuses a nil rather than resolving one of its own, because resolving here
	// would make the result depend on a directory no caller named.
	Workspace *workspaceconfig.ResolvedWorkspace

	// ReleasePath is the path of the Release whose dependencies are resolved.
	ReleasePath string

	// CatalogDirs holds the repeatable catalog-directory flag values
	// (UNresolved): Run prepends them to the workspace catalog_dirs, so a direct
	// invocation and a build's automatic fill derive the identical stack under the
	// same config.
	CatalogDirs []string

	// Upgrade discards the existing pin set and re-resolves, floating each
	// dependency to the highest available version rather than honoring existing
	// pins.
	Upgrade bool

	// UpgradeTargets names the entity URIs to upgrade when Upgrade is set. Empty
	// with Upgrade set is a full refresh (float every dependency); non-empty is a
	// targeted upgrade — only the named pins are dropped and re-resolved, every
	// other pin and the recorded external inputs are held. Each URI is matched by
	// exact string, in the authored bare form (e.g. core/database, not a versioned
	// form), against a lockfile pin key OR against a configured attachment whose
	// plugin carries no pin because its identity is recorded beside the lockfile
	// instead; a URI matching neither halts. The second case reduces no pin —
	// there is none to drop — and re-resolves the attachment it names.
	UpgradeTargets []string

	// Verify checks the existing lockfile against the resolution the same run
	// would perform if it wrote — the recorded pins honoured exactly as that run
	// honours them — and reports what the lockfile records. It writes no lockfile,
	// no plugin trust record and no entity lock. The content cache it resolves
	// through is read and filled as that run reads and fills it: an entry the cache
	// holds under its pin is read from there, one it does not hold is fetched, and
	// one that no longer matches its pin is met exactly as that run meets it.
	//
	// It is refused beside Upgrade, DryRun, PinSources or UpgradeTargets; the
	// exclusions have one home, VerifyConflict.
	Verify bool

	// DryRun computes everything the run would put on disk and puts none of it
	// there: the files it would have written come back in Result.DryRunFiles, each
	// with the bytes it would have held, and the lockfile, the entity locks and the
	// plugin trust record are left exactly as they were.
	//
	// It is scoped to the files the run PRODUCES and says nothing about the
	// artifact cache, which fills exactly as it would otherwise — the
	// resolution is what discovers the answer, so a run that declined to acquire
	// would have nothing to report. That is the same shape a rendered build's dry
	// run carries, and it is what makes the report describe a real effect rather
	// than an empty one.
	DryRun bool

	// IncludePrerelease is accepted and not yet honored, so a caller that passes
	// it keeps working when it starts having an effect.
	IncludePrerelease bool

	// PinSources is this invocation's answer to whether the resolution runs the
	// pin step over the entities it builds — recording, in the lock beside each
	// Recipe and FeatureRecipe with a writable home, the pins its inputs need —
	// rather than halting on an entity whose lock does not record them.
	//
	// It is a pointer because the invocation's answer overrides the workspace's
	// in BOTH directions, and an unset invocation defers to the workspace: nil
	// takes the workspace's pin_sources_at_pull setting, true pins whatever that
	// setting says, and false halts whatever it says. A plain boolean could not
	// tell an invocation that asked for nothing from one that asked for the halt.
	PinSources *bool

	// CompilerVersion is the build identity stamped into IR provenance. It is
	// supplied rather than derived here, because the value names the binary the
	// caller is running and a layer this far from the entry point cannot know
	// which that is.
	CompilerVersion string

	// Toolchain is the versions recorded into the lockfile it writes: the
	// compiler, and each core plugin the binary carries. Supplied for the same
	// reason CompilerVersion is — it describes the binary, which this layer cannot
	// read. A nil stamp records none, which is what a caller with no binary
	// identity to report should hand over rather than an invented one.
	Toolchain *lockfile.ToolchainStamp

	// RenderOptions carries the knobs the external-input discovery render runs
	// under. The whole option set travels rather than a chosen subset of it, so a
	// knob added to the pipeline reaches this render without a second list here
	// having to be widened to admit it — a list that would be widened late or not
	// at all, and would meanwhile decide by omission which knobs a discovery
	// render honours.
	//
	// The set is behavioural rather than cosmetic, and the member easiest to read
	// the other way is the clearest case: attaching the plugin-compile callbacks
	// turns on the eager guest-compilation pass, so leaving them unset changes
	// WHEN guests compile rather than only what a reader is shown. The zero value
	// renders with every knob off, which is what a caller holding none of them
	// hands over.
	//
	// Two further render knobs — the plugin-cache switch and the render-twice
	// determinism check — ride on the context rather than here, so a caller that
	// wants either wraps the context it hands to Run and owes nothing on this
	// struct.
	RenderOptions pipeline.Options

	// DiagOut is the sink for the run's reuse, verification, retraction and
	// withdrawal notices. Nil defaults to os.Stderr at the top of Run;
	// callers pass a buffer to capture diagnostics without mutating a process
	// global.
	DiagOut io.Writer

	// Display is where the compiler runs this operation makes show what they raise
	// as they raise it — the resolution's advisories, the discovery render's
	// warnings — and where its catalog view writes its notices about the catalog
	// files it reads. It is apart from DiagOut because the two are read differently:
	// DiagOut carries the operation's own account of its work, which a caller may
	// collect and hand on as that account, while these lines are each run's own,
	// which every run also returns on its result. Nil shows nothing.
	Display io.Writer

	// FormatFailure is the caller's rendering of a failure, used where a notice on
	// DiagOut carries a failure the run reports and survives: the external-input
	// discovery render that could not finish. It is supplied rather than chosen here
	// because how a failure reads belongs to the surface a reader is looking at, and
	// a surface whose own refusals print in a rendering of their own hands that
	// rendering over, so the same failure reads the same whether the surface refuses
	// it or a notice carries it. Nil prints the failure's cause chain, which is what
	// a caller with no rendering of its own hands over.
	FormatFailure func(error) string

	// Report carries the caller's progress surface and its report of what
	// resolved. The zero value reports nothing.
	Report Reporter

	// ResolveProviderAmbiguity settles an ambiguously-served capability by asking
	// a person. Nil halts the ambiguity loudly instead, which is the answer for
	// every caller with nobody watching.
	ResolveProviderAmbiguity ResolveProviderAmbiguity

	// Puller overrides the production artifact-store client; tests inject an
	// in-memory implementation to exercise a remote fetch without the network. Nil
	// builds the production client, which is nil when the workspace is offline:
	// the network-egress gate refuses before any dial, so no registry tier exists
	// and unresolved references surface as NotFound rather than as a fetch error.
	Puller catalog.OCIPuller

	// SourceResolver overrides the production source-resolver shim; tests inject a
	// recording or stub implementation to assert the source identity computed from
	// the materialized pillars without a real artifact fetch. Nil uses the shim.
	SourceResolver closure.SourceResolver
}

// Result is what a completed resolution reports back.
//
// It carries the material rather than a rendered sentence, because how a
// completion reads belongs to the surface a reader is looking at. Closure is the
// resolution this run computed, including the in-memory resolution tier that is
// deliberately not persisted in the lockfile and is therefore unrecoverable after
// the run. Toolchain is filled only by a verify, where it is what the lockfile on
// disk records rather than what this binary is.
type Result struct {
	// LockfilePath is the per-Release lockfile this run wrote, or verified.
	LockfilePath string

	// Verified reports that this run only checked the lockfile against the
	// resolution it would have performed had it written, and wrote none of the
	// files a writing run produces.
	Verified bool

	// Closure is the resolution this run computed.
	Closure closure.Closure

	// Toolchain is the stamp the verified lockfile records — the compiler and core
	// plugin versions that wrote it. Reported and never compared against this
	// binary: the toolchain is not part of the pinned closure, and checking it
	// would pin the compiler by proxy. Nil on a writing run, and for a lockfile
	// written before the stamp existed.
	Toolchain *lockfile.ToolchainStamp

	// TrustedPlugins is how many plugin identities the record beside the lockfile
	// covers after this run.
	TrustedPlugins int

	// ExternalInputs is how many external inputs the lockfile pins after this run,
	// across the ones carried forward and the ones this run discovered.
	ExternalInputs int

	// DiscoveryRender is the result of the render the external-input discovery
	// performed, carried out whole rather than reduced to the pins it was run to
	// find. It holds the artifacts, the dependency graph, the trace map and the
	// advisories of a render of this Release against the closure this run pinned,
	// so a caller needing any of them holds it already and renders nothing again
	// to reach it.
	//
	// It is filled on a degraded run too, where the discovery render stopped part
	// way. That is safe because the result carries its own verdict and reports no
	// completed payload for a run that did not finish, so a caller reaching the
	// payload through that verdict cannot read a stopped render as a finished
	// one. Nil where the discovery was not reached at all — a verify-only run, or
	// a failure above it.
	DiscoveryRender *compilerops.PreviewResult

	// ExternalInputDiscovery is the failure that stopped the discovery render,
	// or nil where it completed or was stood down.
	//
	// It rides back on a SUCCESSFUL result rather than being returned as the
	// run's error, and that is the shape of the outcome rather than a softening
	// of it: the dependency resolution finished and its pins are on disk, so
	// reporting the whole run as failed would tell a reader to redo work that is
	// already correct. What the caller owes is to say so — the pins that landed,
	// and the references that were not reached — which is why the failure is
	// handed over rather than only written to a diagnostic stream.
	ExternalInputDiscovery error

	// DryRunFiles is what a run asked to report rather than write would have put
	// on disk: one entry per file whose bytes would MOVE, carrying the path and the
	// content it would have held, ordered by path. Empty on every other run, which
	// writes those files instead.
	//
	// It carries the material rather than a rendered transcript, for the reason the
	// rest of this result does: how a report reads belongs to the surface a reader
	// is looking at, and a surface answering in a structured payload renders no
	// transcript at all.
	//
	// A file the run would leave byte-identical is absent, because naming it would
	// tell a reader that a write was coming for it.
	DryRunFiles []dryrunreport.File
}

// requireWorkspace returns the workspace a caller threaded in from its own
// resolution chokepoint. A nil workspace is a wiring error — the call was reached
// without any caller having resolved one — so it is reported rather than
// repaired. Resolving here from the current directory would make the result
// depend on a directory no caller named, which is exactly the dependency routing
// every surface through one resolution point exists to remove.
func requireWorkspace(ws *workspaceconfig.ResolvedWorkspace) (*workspaceconfig.ResolvedWorkspace, error) {
	if ws == nil {
		return nil, fmt.Errorf("internal: no resolved workspace supplied — the caller must resolve one and pass it in")
	}
	return ws, nil
}

// annotate carries, on a resolution or validation failure, the catalog files the
// tolerant load refused and the workspace's account of where it reads local
// entities for the Release (workspaceconfig.ResolvedWorkspace.LocalCatalogAdviceNote),
// composed by the one annotation every surface's account comes from
// (compilerops.AnnotateSkipContext).
//
// It is composed here, in the operation, rather than handed in by each caller,
// because the account belongs to the failure and not to the surface reporting it:
// a hook each surface had to remember to supply is one a surface can forget, and
// then the same failed resolution tells a reader at a terminal where entities
// are read from and tells an assistant or an editor nothing. Only a resolution or
// validation failure can be explained by a refused file or by where entities are
// read from, so only those reach it; the annotation itself adds the account only
// where a reference resolved to nothing.
func (c Config) annotate(rw *workspaceconfig.ResolvedWorkspace, err error, skipped []catalog.SkippedEntity) error {
	return compilerops.AnnotateSkipContext(err, skipped, rw.LocalCatalogAdviceNote(c.ReleasePath))
}

// withPullLibraryRemedy follows a library discovery that found nothing for a
// reference with the remediation line naming the cached libraries no lockfile
// entry pins (catalog.LazyLookup.PullLibraryRemedy), or returns err unchanged
// where the failure is a different one or the cache holds no such library.
//
// The failure keeps its condition and its text; the line is added beneath it, as
// the decoration of a failure that carries no categorised envelope, which is the
// shape the unresolvable-reference condition reaches a reader in. offline is the
// workspace's offline posture, which decides the action the line can name.
func withPullLibraryRemedy(err error, view *catalog.LazyLookup, offline bool) error {
	if !errors.Is(err, closure.ErrUnresolvableURI()) {
		return err
	}
	remedy := view.PullLibraryRemedy(offline)
	if remedy == "" {
		return err
	}
	return fmt.Errorf("%w\n  %s", err, remedy)
}

// irEntityInfra derives the per-instance source infra for FeatureRecipe
// entities from the IR render-set (graph.Spec) — the exact attachment set the
// renderer renders, assembled across all five tiers (Aggregate component/subsystem,
// Release boundary-wide/component, annotation-expanded) at IR-build. Reading the IR
// (rather than the topology or a Release-boundary walk, each short by tiers) is what
// makes the no-using-instance skip in source-descent congruent with rendering.
//
// Each entry is keyed by the Manifest-mapped FeatureRecipe URI — the verbatim
// string the closure records as the source-descent entity — so the returned map
// joins against closure entities. An instance whose type has no Manifest mapping
// carries no source-descent entity and is skipped. Per-instance infra is the
// IR-resolved entry.Infra, so no Release-side join is needed.
//
// scopeOf reports a FeatureRecipe's executionScope by feature type. A
// boundary-scope FR renders once at its boundary from the boundary's own infra,
// but its per-component FeatureValuesMapping entries are Tier-4 folds at the
// wrong granularity for source identity; those entries are skipped here and the
// FR's source is pinned once per (boundaryPath, instance) from the IR
// boundary-feature projection — the same infra the renderer resolves the source
// from, so the lock-time pin and the build-time resolution agree by
// construction. scopeOf returning found=false (FR not resolvable) falls through
// to the per-component pin, preserving prior behavior.
func irEntityInfra(graph *types.IRGraph, manifest *types.Manifest, scopeOf func(instanceName, featType string) (string, bool)) (map[string][]closure.ComponentSourceInfra, error) {
	out := map[string][]closure.ComponentSourceInfra{}
	add := func(nodeID, scope, instance, featType string, entryInfra, entryValues types.OrderedMap[any]) error {
		if featType == "" {
			return nil
		}
		frURI, found, err := catalog.FeatureRecipeURI(manifest, instance, featType)
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		var infra map[string]any
		if entryInfra.Len() > 0 {
			infra = entryInfra.TemplateAlphabeticalMapDeep()
		}
		var values map[string]any
		if entryValues.Len() > 0 {
			values = entryValues.TemplateAlphabeticalMapDeep()
		}
		out[frURI] = append(out[frURI],
			closure.ComponentSourceInfra{Component: nodeID + "." + scope + "." + instance, Infra: infra, Values: values})
		return nil
	}
	for nodeID, feats := range graph.Spec.FeatureValuesMapping.All() {
		for instName, entry := range feats.All() {
			if scope, ok := scopeOf(instName, entry.Type); ok && scope == "boundary" {
				continue // pinned once from the boundary projection below
			}
			if err := add(nodeID, "features", instName, entry.Type, entry.Infra, entry.Values); err != nil {
				return nil, err
			}
		}
	}
	for boundaryPath, feats := range graph.Spec.BoundaryFeatures.All() {
		for instName, bf := range feats.All() {
			if scope, ok := scopeOf(instName, bf.Type); !ok || scope != "boundary" {
				continue // component-scope (or unresolvable): pinned per-component above
			}
			// Boundary-scope features carry no values (Release pillar-split).
			if err := add(boundaryPath, "features", instName, bf.Type, bf.Infra, types.OrderedMap[any]{}); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// stopped reports that the caller no longer wants the resolution, as an error
// that errors.Is matches against context.Canceled and context.DeadlineExceeded.
// It returns nil while the caller is still waiting, which is every ordinary run.
//
// It is one function rather than an inline check written at each interruption
// point so the sentence a stop produces is stated once in this package. Two
// hand-written phrasings of one condition agree until either is edited, and
// nothing fails when they stop agreeing.
//
// The sentence names the lockfile because the lockfile is what a withdrawn
// resolution did not write: the pins a reader asked for are not on disk, and the
// ones that were there are untouched. The cause is wrapped rather than replaced
// because it is the part a caller acts on — a surface distinguishing "the caller
// withdrew" from "the budget ran out" reads it off the wrapped error, and a
// surface that does not care prints the sentence.
func stopped(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("aggrega pull: stopped before the lockfile was written: %w", err)
	}
	return nil
}

// runContext returns the context one run of the resolution is carried out under:
// the caller's, holding a live trust root of the run's own (livetrust.WithSharedRoot),
// so every verification in the run shares one fetch of it and no root fetched for
// an earlier run, in a process that runs many, is handed to this one; and a memo of
// registry reads of the run's own (oci.WithRegistryReadMemo), so each pass of the
// dependency walk can fetch what it is about to read several at once, and no answer
// read for an earlier run is handed to this one.
func runContext(ctx context.Context) context.Context {
	return oci.WithRegistryReadMemo(livetrust.WithSharedRoot(ctx))
}

// Run executes the dependency resolution against the supplied configuration.
// Loads workspace config + Release + Manifest + Aggregate, walks the catalog and
// source closures, and writes the per-Release lockfile.
//
// Config.DryRun turns every one of those writes into a report: the run is the
// same resolution, and the files it would have put on disk come back in
// Result.DryRunFiles instead of landing there.
//
// A configuration that asks a verify for something it cannot also do is refused
// before anything is read, with the *VerifyConflictError Config.VerifyConflict
// returns.
//
// Returns wrapped errors on workspace-config validation failure,
// Release/Manifest/Aggregate load failure, closure walk failure
// (typically a missing catalog entity surfaced by closure.ErrUnresolvableURI),
// source descent failure (closure.ErrSourceTemplateAtLockTime), and
// lockfile write failure.
//
// # A caller that gives up stops the work, not only the reply
//
// ctx is the caller's own; the resolution carries it into every step that leaves
// the process and observes it at five points of its own: ahead of the closure
// walk, which is the first step that can reach a registry; ahead of the source
// descent; ahead of the pin step, which is the first step that writes; ahead of
// the acquisition render; and ahead of the lockfile write. A withdrawal at any
// of them leaves the pins that were already on disk exactly as they were, which
// is the same outcome a run that failed before the write produces.
//
// The five points are the same on a reporting run, which has no write for them
// to protect. They are kept rather than skipped because a check that fires in one
// mode only is one whose other mode nobody exercises, and because a caller that
// withdrew should stop at the point it named whatever it asked the run to do.
//
// The first half of that is what the five points alone cannot give. A check
// between steps stops the NEXT step; it does nothing to a fetch already in
// flight, and a resolution spends most of its wall time inside one. So the
// context reaches the registry call itself: every fetch made through the
// closure lookup — the entity walk, library resolution, the sub-aggregate
// descent, the plugin entity and artifact descents, and the verdict-socket
// caching — is issued on this context, and a withdrawal lands on the transport
// rather than waiting for the fetch to finish and be discarded.
//
// The reads the run makes through the lazy catalog view after the walk are
// issued on it too: library discovery, closure-scoped validation, the pin step's
// question of which references a Component or Feature shadows, the graph build's
// catalog lookups and the discovery render's extends and lint passes. Most of
// them are served from the cache slots the walk has just written, and a read
// that leaves the process — a parent an extends chain names that no local tier
// holds, say — leaves it under this context. Those reads ask a registry for a
// Component or Feature only under a reference the walk collected as that kind,
// so a Recipe, a plugin or the Release's own documents cost no round trip.
//
// Past the lockfile write it is deliberately NOT observed again. The trust
// record written beside the file is part of the same landing, so a stop between
// the two would leave a lockfile whose companion record does not describe it —
// a worse state than either finishing or not starting.
//
// # One live trust root per run
//
// Every signature the run verifies against the live public trust root — the
// signature pass, the deprecation-marker pass, the verdict-plugin verification —
// verifies against one fetch of it, made by the first verification that needs it
// (runContext). A run pinning its own trust root, or an offline one, is unchanged.
func Run(ctx context.Context, cfg Config) (Result, error) {
	ctx = runContext(ctx)
	if err := cfg.VerifyConflict(); err != nil {
		return Result{}, err
	}
	if cfg.DiagOut == nil {
		cfg.DiagOut = os.Stderr
	}
	report := cfg.Report
	releaseDir := filepath.Dir(cfg.ReleasePath)

	rw, err := requireWorkspace(cfg.Workspace)
	if err != nil {
		return Result{}, err
	}
	wsCfg := rw.Config()

	authored, err := resolver.LoadResolvedRelease(cfg.ReleasePath)
	if err != nil {
		return Result{}, fmt.Errorf("load release: %w", err)
	}
	// The Release files this composition reads, recorded by content so a build
	// notices an edit to any of them. They are recorded BEFORE the composition
	// below re-walks the chain, and the order is the safe one: a file edited while
	// this run is under way is then recorded as it stood before the edit, so the
	// next build halts on it rather than trusting pins resolved from content the
	// record does not describe. The Release named here needs no such ordering — its
	// digest comes from the very read the composition folds.
	//
	// A failure here is the composition's own, raised by the same walk, so it is
	// reported in the composition's words.
	releaseDocs, err := resolver.ReleaseDocuments(rw.Root(), releaseDir, authored)
	if err != nil {
		return Result{}, fmt.Errorf("compose release extends: %w", err)
	}
	// Compose the Release's own extends chain before either sibling is looked
	// for, because both pointers are things a child may inherit rather than
	// declare: the canonical child names a parent and a Manifest and nothing
	// else, so its Aggregate reference is the parent's and reading it unfolded
	// would resolve an empty one.
	//
	// The composition rules' advisories are dropped here rather than reported.
	// This operation pins what a build will resolve, and the build composes the
	// same chain and reports the same readings; emitting them at both would show
	// an author one finding twice for one cause.
	rel, _, err := resolver.ResolveReleaseExtends(rw.Root(), releaseDir, authored)
	if err != nil {
		return Result{}, fmt.Errorf("compose release extends: %w", err)
	}
	manRes, err := resolver.ResolveManifest(rw.Root(), releaseDir, cfg.ReleasePath, rel.Spec.Manifest)
	if err != nil {
		return Result{}, fmt.Errorf("load manifest: %w", err)
	}
	// Compose the Manifest's extends chain so the closure walk and recipe
	// resolution see the inherited mappings rather than the child's own
	// entries alone. A Manifest with no extends reference is unchanged.
	manifest, err := resolver.ResolveManifestExtends(rw.Root(), releaseDir, cfg.ReleasePath, manRes)
	if err != nil {
		return Result{}, fmt.Errorf("compose manifest extends: %w", err)
	}
	// Everything above read the Release's own files; everything below can fill the
	// artifact cache, from the first registry reach onward, under every mode — a
	// verify and a dry run fill it as a write does. A workspace that was found
	// nowhere and named by nobody gets no cache, so it is refused here, before
	// anything is fetched, with the folder and the way out in the refusal; the
	// caller's own usage errors above still come first.
	if err := rw.CacheWriteRefusal(); err != nil {
		return Result{}, err
	}
	// The aggregate resolve is deferred until the catalog view is opened (below) so
	// its identity cascade can reach the catalog tier — this operation must
	// resolve the top-level aggregate the same way a build does, or a
	// catalog-resident aggregate would build but not pin (lock/build divergence).

	// This operation walks the closure to populate the lockfile and never emits
	// IR, so the catalog's source-file accessors go unused. The resolved workspace
	// root is still supplied, because it is the anchor the configured cache
	// directory is written against: with it, the walk steps over cached
	// library.yaml files rather than ingesting them as authored Tier-1 catalog no
	// matter which directory the run is invoked from.
	workspaceRoot := rw.Root()
	cacheDir := wsCfg.ResolvedArtifactCacheDir()
	// Effective first-win catalog search stack for this Release: the flag dirs
	// prepended to the directories the workspace configuration gives it.
	// Resolved here (not by the caller) so a direct invocation and a build's
	// automatic fill derive the identical stack under the same config. Its named
	// part travels beside it to every reader of it, unjudged.
	// A configured directory that is not a directory is refused here, before
	// anything is fetched — by this run and by the build that would read its
	// lockfile, since both read the stack from the one resolver.
	dirs, err := rw.ResolveCatalogDirs(cfg.ReleasePath, cfg.CatalogDirs)
	if err != nil {
		return Result{}, err
	}
	namedDirs, err := rw.NamedCatalogDirs(cfg.ReleasePath, cfg.CatalogDirs)
	if err != nil {
		return Result{}, err
	}
	puller := cfg.Puller
	if puller == nil {
		// The discovery step consults the local registry index so a version recorded
		// withdrawn is not selected by a reference that did not name it, and every
		// skip is announced on the same writer the deprecation notices use. A
		// workspace with no index built gets a nil screen and an unscreened
		// discovery step. The index is kept per registry host and records every
		// repository the host serves, a default's repository path included, so it
		// is read by the default's host alone. An index that leads through a link out
		// of the root the cache is held to halts the run here, naming the link.
		screen, err := registryindex.NewScreen(rw, wsCfg.DefaultRegistryLocation().Host, cfg.DiagOut)
		if err != nil {
			return Result{}, err
		}
		puller = catalog.NewOCIPullerWithRetractionScreen(wsCfg, screen)
	}
	// The catalog is read through the one lazy view a build resolves through, and
	// through nothing else: every pass below — the aggregate resolve, the closure
	// walk, library discovery, the descents, closure-scoped validation and the
	// source descent — asks this view, so what this run pins is what the build that
	// reads its lockfile resolves. The view resolves a Component or Feature only
	// when a resolution reaches it and enumerates the other kinds tolerantly, so a
	// broken catalog entity OUTSIDE this Release's closure never fails the run,
	// while one inside it fails the resolution that needed it, with the refusals the
	// view recorded handed to the failure annotation. It carries the network
	// puller, because this operation's job is to fetch and cache what no local tier
	// holds.
	report.phase(PhaseLoadCatalog)
	lazyView := catalog.NewLazyLookup(workspaceRoot, cacheDir, dirs, puller, namedDirs...)
	lazyView.WriteNoticesTo(cfg.Display)
	// A link leading out of a catalog directory's root halts the run before
	// anything is resolved or pinned, as it halts a build: the view passes such a
	// link over unread, and pinning around it could record another entity of the
	// name it stands for.
	if err := lazyView.CheckLinks(); err != nil {
		return Result{}, err
	}
	// Verify the Manifest's mapping table is consistent before the closure walk,
	// so the run halts on a mismatched mapping with the same Loud Guardrail the
	// build does (lock/build parity).
	if err := lazyView.AssertManifestImplementsConsistency(manifest, manRes.Path); err != nil {
		return Result{}, err
	}

	report.phase(PhaseDependencies)
	// The registry (Tier-3) seam for a bare-name top-level spec.aggregate that the
	// same-directory scan and the catalog both miss: fetch + cache it by identity,
	// pinning its metadata.version (identity), not the OCI tag. Resolution-only —
	// build passes a nil puller, so Tier-3 never networks and a registry top-level
	// resolves from the pinned cache through the lazy lookup.
	//
	// The bytes handed back are already the entity document whatever shape the
	// coordinate served, so this seam never meets the published shape and has no
	// discrimination of its own to get wrong; the digest beside them still covers
	// the whole artifact, which is what the pin commits to.
	registryFetch := func(ref string) (resolver.ResolvedAggregate, bool, error) {
		document, version, digest, manifestDigest, found, ferr := catalog.PullAndCacheAggregate(ctx, workspaceRoot, cacheDir, puller, ref)
		if ferr != nil || !found {
			return resolver.ResolvedAggregate{}, found, ferr
		}
		agg, perr := resolver.LoadAggregateFromBytes(document, ref)
		if perr != nil {
			return resolver.ResolvedAggregate{}, false, perr
		}
		return resolver.ResolvedAggregate{Aggregate: agg, Bytes: document, ResolvedVersion: version, Digest: digest, ManifestDigest: manifestDigest, FromRegistry: true}, true, nil
	}
	aggRes, err := resolver.ResolveAggregate(workspaceRoot, releaseDir, cfg.ReleasePath, rel.Spec.Aggregate, lazyView.AggregateLookup(), registryFetch)
	if err != nil {
		return Result{}, fmt.Errorf("load aggregate: %w", err)
	}
	aggregate := aggRes.Aggregate

	lockfilePath := lockfile.PathFor(cfg.ReleasePath)
	// The lockfile and the plugin trust record are composed beside the Release, so
	// both are held to the root the Release's directory picks: the workspace root
	// where the Release lies inside the workspace, and its own directory where a
	// Release typed on the command line, or named by the configuration, lies
	// outside it.
	releaseRoot := writescope.RootFor(workspaceRoot, releaseDir)
	if cfg.Verify {
		// Verify-only: a missing lockfile is fatal — the flag
		// presumes one exists. The no-lockfile case is what the
		// default run (no verify) addresses.
		if _, statErr := os.Stat(lockfilePath); statErr != nil {
			return Result{}, fmt.Errorf("verify: lockfile %s not found: %w", lockfilePath, statErr)
		}
	}

	// A registry-resolved top-level is pinned as a real registry entity (its
	// metadata.version + digest), seeded through the registry tier rather than the
	// version-less external tier; a local top-level (same-dir / ./file / catalog)
	// stays external exactly as before.
	var externals map[closure.EntityKey][]byte
	var registrySeeds map[closure.EntityKey]closure.Resolution
	if aggRes.FromRegistry {
		externals = lockfile.ExternalEntitiesFromBytes(rel, nil, manRes.Bytes)
		registrySeeds = map[closure.EntityKey]closure.Resolution{
			{Kind: closure.KindAggregate, URI: rel.Spec.Aggregate}: {ResolvedVersion: aggRes.ResolvedVersion, Digest: aggRes.Digest, ManifestDigest: aggRes.ManifestDigest, ResolutionPath: closure.ResolutionPathRegistry},
		}
	} else {
		externals = lockfile.ExternalEntitiesFromBytes(rel, aggRes.Bytes, manRes.Bytes)
	}
	// The prior lockfile is read exactly once AS THE PRIOR PIN SOURCE — by this one
	// decision, which performs that read and hands the value back — and every later
	// consumer of those pins reads what it returned rather than the file: the reuse
	// verification handed to the closure lookup below, and the verified-fact and
	// external-input carry-forwards at the end of the run.
	//
	// The claim is scoped to that role and is not a claim about the operation. The
	// verify branch loads the same path again further down, as the object under
	// verification rather than as a pin source; that read answers a different
	// question and is not a bypass of this one. Reading "exactly once" as
	// whole-operation would invite "fixing" either this comment or that read.
	//
	// Which pins are honored, and whether the prior lockfile is read at
	// all, is that decision's to make: a full refresh floats every pin and carries
	// forward neither the recorded facts nor the external inputs, and holding
	// the read together with the policy that governs it is what keeps the three
	// consumers from disagreeing about it.
	//
	// The file on disk is left untouched by the decision. It is replaced only by the
	// single write at the end of a successful run, so a refresh that cannot complete
	// leaves the previous pins in place rather than destroying pins it never replaced.
	//
	// The invocation the decision reads is named once here and handed to both of
	// its consumers — the reuse decision itself, and the verdict-plugin trust
	// record written at the end, which has to know whether this run re-resolves a
	// given attachment. A second composite literal would be a second answer to
	// "what kind of run is this", and the two could then disagree.
	//
	// Not every identity a run re-resolves is pinned. A plugin attached in a role
	// that only returns an accept-or-reject verdict shapes no rendered output, so its
	// identity is recorded beside the lockfile rather than pinned in it — and a
	// named target checked against the pin set alone would be refused before the run
	// could reach the record it governs. The attachments the workspace configures,
	// and the identities that record already holds, are therefore handed in as
	// targets the decision accepts without a pin behind them.
	//
	// The record is read here rather than only at the two places that compare and
	// write it, because a target naming a plugin the configuration no longer
	// attaches has to be accepted for the run to be able to drop its entry — and the
	// only place that target can come from is the record. A record that exists and
	// does not satisfy its schema halts the run at this point, which is the same
	// fail-closed reading every other consumer of the file takes.
	recordedTrust, _, err := pluginstrust.LoadOrStart(pluginstrust.PathFor(cfg.ReleasePath))
	if err != nil {
		return Result{}, err
	}
	pinMode := catalog.PinReuseMode{Upgrade: cfg.Upgrade, UpgradeTargets: cfg.UpgradeTargets, VerifyOnly: cfg.Verify}
	pinReuse, err := catalog.PinReuseFromLockfile(workspaceRoot, lockfilePath, cfg.ReleasePath, pinMode,
		verdictUpgradeTargets(wsCfg, recordedTrust),
		func(notice string) { fmt.Fprintf(cfg.DiagOut, "  reuse: %s\n", notice) })
	if err != nil {
		return Result{}, err
	}
	// A full refresh drops the pins this run holds in order to re-resolve them,
	// floating each to the highest version a registry currently offers — and
	// "highest available" is a claim about what a registry serves right now,
	// which an offline workspace has no way to obtain. Running it from a warm
	// cache would pin whatever happened to be cached while reporting that it had
	// floated to the newest, so the posture refuses the refresh rather than
	// quietly narrowing it into something else. A targeted refresh is the same
	// act over fewer references and refuses on the same reasoning.
	//
	// The ordinary reconcile is deliberately NOT refused. It honours the pins it
	// already records and resolves only what has none, so a workspace whose
	// references are all cached or present in a local catalog completes it
	// without reaching out at all; where it genuinely does reach out, the gate
	// refuses it there. Refusing it here would break the case an offline
	// workspace with a local copy of its dependencies exists to serve.
	//
	// It is taken HERE rather than at the top of the operation, and the position
	// is the point: everything above reads local files, and the closure walk
	// below is the first thing that can reach a registry — so this is the last
	// moment before the network act, which is what the refusal is keyed on. It
	// also leaves the caller's own usage errors ahead of it, above all a named
	// upgrade target the resolution cannot account for. Both diagnostics are
	// true, and the one about the caller's own input is the more useful of the
	// two: the posture is a property of the workspace they can read off the
	// configuration, while a mistyped target is knowable from nowhere else.
	if cfg.Upgrade {
		if err := egress.RefuseIfOffline(rw,
			"re-resolving a dependency to the highest version a registry offers is a network round trip"); err != nil {
			return Result{}, err
		}
	}
	// The last moment before the closure walk, and the position is the point: it
	// sits beside the offline refusal above for the same reason that refusal does.
	// Everything higher reads local files, so a caller that gave up during the
	// load has reached no registry yet and nothing of this run is on disk — which
	// makes this the first place a withdrawal is worth observing and the cheapest.
	if err := stopped(ctx); err != nil {
		return Result{}, err
	}
	lookup := lazyView.ClosureLookup(puller, workspaceRoot, cacheDir, externals, registrySeeds, pinReuse.Verification)
	report.phase(PhaseClosure)
	// Reference shape is judged before the walk, for the same reason the shared
	// closure resolver judges it: a mapping value the reference grammar cannot
	// express matches no entity, so the walk would report a recipe that is in the
	// catalog as absent. This path computes its own closure rather than going
	// through the shared resolver, so it carries the check itself.
	if faults := resolver.ManifestRecipeReferenceFaults(manifest); len(faults) > 0 {
		return Result{}, cfg.annotate(rw, faults[0], lazyView.SkippedEntities())
	}
	// A failure of this walk, and of the resolution steps below it, is returned as the
	// walk words it, with no label of this function's own: the walk's frames open by
	// saying what was being resolved, so a label here would say it a second time.
	cl, err := closure.ComputeClosure(ctx, rel, manifest, types.Normalize(aggregate), wsCfg, lookup)
	if err != nil {
		return Result{}, cfg.annotate(rw, err, lazyView.SkippedEntities())
	}
	cl.ReleaseDocuments = releaseDocs
	// Resolve the workspace provider-socket bindings to their Plugin entities at
	// resolution time only. ComputeClosure does not read the provider socket's config,
	// so build / serve reconstruct these entities from the lockfile rather than
	// from live config — the entity axis stays invariant to a config edit
	// afterwards.
	if err := closure.ResolveProviderSocketEntities(ctx, &cl, wsCfg.Plugins.Providers, lookup); err != nil {
		return Result{}, err
	}
	// The post-fetch consumers — closure-library discovery, closure-scoped
	// validation, and the source-descent SubAggregateLoader below — read the view
	// pinned to the closure just resolved, with no cache→catalog bridge. The pins
	// come from that closure rather than from a lockfile, because this operation
	// WRITES the lockfile, so a Tier-2 hit is verified against the version the walk
	// just resolved.
	closurePins, err := catalog.PinsFromClosure(cl)
	if err != nil {
		return Result{}, err
	}
	lazyView.SetPins(closurePins)

	// Resolve what ComputeClosure's entity walk does not descend — the bases the
	// closure's Components and Features extend, then the cross-document library axes
	// (see closure.AugmentClosure) — reading the resolved closure through lazyView,
	// the same producer a build runs, so this operation and a build of the same
	// Release compute the identical closure.
	report.phase(PhaseLibraries)
	if err := closure.AugmentClosure(ctx, &cl, lazyView, lookup); err != nil {
		return Result{}, withPullLibraryRemedy(err, lazyView, wsCfg.Offline)
	}
	// The pinned axes the closure walk leaves empty — the transitive sub-aggregates,
	// the pipeline and provider plugin entities the closure's Recipes and
	// FeatureRecipes reach, and those plugins' .wasm artifacts — are descended by the
	// one computation a build runs to hold the lockfile to (closure.DescendPinnedAxes).
	// This is where those pins are WRITTEN; what a pull hands it differs from what a
	// build hands it only where the two must differ, and each difference is one of
	// the arguments below.
	//
	// The sub-aggregate pass and both plugin passes read the reconciling lookup the
	// walk used, which honours the prior pins and may fetch.
	aggLoader, aggErr := closure.RequireAggregateLoader(lookup)
	if aggErr != nil {
		return Result{}, aggErr
	}
	// Provider resolution honors the workspace-config plugins.providers selection
	// and records the resolved target → plugin binding for the lockfile, so build
	// selects the engine from the lockfile rather than re-reading the config. On an
	// ambiguous target with no binding, a caller that supplied a way to ask a person
	// offers to write the binding; with nobody watching the ambiguity halts loudly.
	resolvedBindings := map[string]lockfile.ProviderBinding{}
	providerBindResolver := func(ref string) (types.Plugin, bool, error) {
		bare := types.BareCapability(ref)
		p, ok, err := lazyView.ProviderPluginForTarget(ref, wsCfg.Plugins.ProviderTargetURIs())
		if errors.Is(err, catalog.ErrAmbiguousProviderPlugin()) && cfg.ResolveProviderAmbiguity != nil {
			if herr := cfg.ResolveProviderAmbiguity(bare, lazyView.ProviderPluginCandidates(bare), rw, &wsCfg, cfg.DiagOut); herr != nil {
				return types.Plugin{}, false, herr
			}
			p, ok, err = lazyView.ProviderPluginForTarget(ref, wsCfg.Plugins.ProviderTargetURIs())
		}
		if err != nil {
			return types.Plugin{}, false, err
		}
		if ok {
			resolvedBindings[bare] = lockfile.ProviderBinding{Capability: bare, Plugin: p.Coordinate()}
		}
		return p, ok, err
	}
	// The artifact of a Plugin entity authored in a catalog directory is read from this
	// machine and never fetched, and a build fetches no plugin, so an artifact the
	// build could not read now it could not read then either. What it resolves to is
	// asked of the cascade the build itself loads plugins through, over the same
	// sources, before the lockfile is written, and a pull asks it at both plugin
	// passes: the entity descent refuses the one no tier holds, and the artifact
	// descent pins the bytes of a transform-role attachment authored there. The
	// build's computation asks the artifact pass the same question, so a pull and the
	// build it feeds cannot disagree about that pin. The same question serves the
	// verdict-plugin caching further down.
	//
	// It is taken over the co-located directories the view holds now, which are the
	// ones the entity pass reads: the descent changes the view's pins only at its
	// re-fold, after the entity pass has asked its questions.
	catalogArtifacts := pipeline.CatalogPluginArtifacts(workspaceRoot, wsCfg, lazyView.PluginColocatedDirs())
	// The re-fold makes the plugin entities the entity pass just pinned visible to
	// the artifact pass: re-applying the closure's pins drops the view's recipe set,
	// and the loader the artifact pass reads is re-derived from the view afterwards.
	refold := func() (closure.RecipeLoader, error) {
		pins, err := catalog.PinsFromClosure(cl)
		if err != nil {
			return nil, err
		}
		lazyView.SetPins(pins)
		return lazyView.ClosureRecipeLoader(), nil
	}
	// The recipe loader handed in is taken before the sub-aggregate pass. It reads the
	// view's recipe set as it stands, and nothing changes that set until the re-fold:
	// the sub-aggregate pass walks the reconciling lookup, which reads the view but
	// installs no pins on it, and no pin set is installed on the view before the
	// entity pass reads it.
	if err := closure.DescendPinnedAxes(ctx, &cl, aggLoader, lazyView.ClosureRecipeLoader(), lookup, lookup,
		providerBindResolver, refold, catalogArtifacts, catalogArtifacts); err != nil {
		return Result{}, err
	}
	// Resolve the transform-role governance attachments (label-transform /
	// annotation-transform) to their pinned lockfile records, in authored order. A
	// transform role shapes output bytes, so its attachment set / order / role /
	// export function is recorded for the lockfile and build reconstructs it from
	// there rather than from live config — keeping build output invariant to a
	// plugins.governance transform change made afterwards.
	mutatorAttachments, err := resolveMutatorAttachments(wsCfg.Plugins.TransformGovernanceRefs(), lazyView)
	if err != nil {
		return Result{}, err
	}
	// Record the verdict-role attachments' external rule-input digests for the
	// lockfile. This is provenance, not a pin: the verdict plugin is cache-not-pin
	// and reads its rule inputs live, so only the digest is recorded — enough to
	// audit the inputs that shaped a verdict and to surface a changed rule file as
	// a lockfile diff on the next run.
	verdictInputs, err := resolveVerdictInputs(wsCfg.VerdictAttachments())
	if err != nil {
		return Result{}, err
	}
	// Cache the verdict-socket (policy / linter) gate plugins so a hermetic build
	// can run them, WITHOUT pinning them in the lockfile. A verdict socket renders
	// an accept/reject verdict without shaping the output bytes, so its plugin is
	// not part of the reproducibility closure (cache-not-pin): this operation
	// fetches and caches a registry-served plugin's entity + .wasm, and the build
	// resolves it from the cache by URI. One authored in a catalog directory is read
	// from this machine through the same question the descents above asked.
	verdictHTTPClient, verdictEgressErr := egress.HTTPClient(wsCfg)
	// The identities the caching walk resolved are collected here and recorded
	// further down, after the verify-only branch has had its chance to return.
	// Collecting and recording are deliberately separate steps: the caching walk
	// runs on every invocation and may fill the content cache, and a verify-only
	// run records no identity and writes no trust record.
	verdictResolutions, err := cacheVerdictSocketPlugins(ctx, wsCfg, lookup, catalogArtifacts, lazyView, workspaceRoot, cacheDir,
		pluginfetchverify.RemoteTargetFor(verdictHTTPClient), verdictPluginLookupFor(cfg.Verify, verdictEgressErr), cfg.DiagOut)
	if err != nil {
		return Result{}, err
	}
	// Hold each resolved identity to the one recorded as trusted for it. This sits
	// ABOVE the verify-only return below on purpose: comparing needs no write, so
	// the mode that exists to report whether the committed files still describe the
	// resolution a pull would perform now covers the record as well as the lockfile.
	// It also sits as early as the identities exist, so a disagreement stops the run
	// before it does further work on a resolution it is not going to accept.
	if err := holdVerdictPluginsToTheirRecord(cfg.ReleasePath, verdictResolutions, pinMode); err != nil {
		return Result{}, err
	}
	// Re-derive each plugin's host functions from the freshly-cached, digest-
	// verified .wasm and verify they match the machine-owned stamp the publish
	// flow wrote into the entity. Defense-in-depth over the content-digest pin:
	// halts on a divergence the digest alone would not surface (a non-aggrega
	// push, a publish-side stamping bug).
	if err := verifyPluginHostFunctions(ctx, &cl, lazyView.ClosureRecipeLoader(), workspaceRoot, cacheDir, cfg.DiagOut); err != nil {
		return Result{}, fmt.Errorf("verify plugin host functions: %w", err)
	}
	// Refresh the pin set so it covers the descended sub-aggregates the source-
	// descent flatten resolves through lazyView.SubAggregateLoader below, and the
	// providers + pipeline plugins the descents just pinned.
	if closurePins, err = catalog.PinsFromClosure(cl); err != nil {
		return Result{}, err
	}
	lazyView.SetPins(closurePins)

	// The resolution report reads the closure's in-memory resolution tier (not
	// persisted in the lockfile) and is unrecoverable after this run. It is handed
	// over before the verify/write split so it reflects the run regardless of mode.
	report.resolved(cl)

	if cfg.Verify {
		// Verify-only path: load the existing lockfile and check drift against the
		// closure just computed, which honoured the recorded pins exactly as a writing
		// run does — the reuse decision above makes no distinction between the two,
		// and nothing here builds a pin set of its own. This branch writes
		// none of the files a writing run produces; the content cache — read through
		// by the walk, and given a signature bundle wherever this branch asks a
		// registry about a recorded signature — is the only thing it may have filled.
		//
		// The source-artifact axis is checked separately, and it has to be: this
		// branch returns before the graph build below, so the closure in hand has
		// resolved no sources and holds none — a source-artifact axis folded into
		// lockfile.Verify here would compare the recorded rows against an empty set
		// and report nothing, which is a silent pass wearing the shape of a check.
		// So the recorded references are re-asked at their remotes instead, from the
		// rows themselves, which is the only place the answer exists.
		//
		// The build-time digest gate does NOT cover this. That gate compares the
		// cached bytes against the digest the lockfile records for them, and a pull
		// that adopts a moved reference rewrites BOTH in the same act — so the two
		// values it compares moved together and it is satisfied by construction.
		lf, _, lerr := lockfile.Load(workspaceRoot, lockfilePath)
		if lerr != nil {
			return Result{}, fmt.Errorf("verify: load lockfile %s: %w", lockfilePath, lockfile.WithRewriteRemedy(lerr, cfg.ReleasePath))
		}
		if verr := lockfile.Verify(lf, cl); verr != nil {
			return Result{}, fmt.Errorf("verify: %w", verr)
		}
		if verr := verifyRecordedSourceReferences(lf, cfg.sourceResolver(ctx, wsCfg), wsCfg,
			workspaceRoot, cacheDir, cfg.DiagOut); verr != nil {
			return Result{}, fmt.Errorf("verify: %w", verr)
		}
		// The locks beside the Release's own Recipes and FeatureRecipes are held to
		// what a build compares them with. Nothing above reads them: the lockfile
		// records each entity by its document's digest, and a lock is a file of its
		// own, so without this a verify reports a match over a workspace whose build
		// halts on a drifted pin.
		if verr := verifyEntityLocks(lf, lazyView, cl.Entities); verr != nil {
			return Result{}, fmt.Errorf("verify: %w", verr)
		}
		// The signature facts both files record are held to what a pull would record
		// now, from the pass's own selection of the rows it asks about and the
		// record's own computation of what it would become. Nothing above reads them:
		// the pin comparison holds a row to its reference, version and digest, and the
		// hold on the record compares identities. It comes after the pins, so a fact
		// is compared only over a lockfile whose pins are the ones a pull would write.
		verifyHTTPClient, _ := egress.HTTPClient(wsCfg)
		if verr := verifyRecordedSignatures(ctx, lf, cl, cfg.ReleasePath, lockfilePath, wsCfg, workspaceRoot, cacheDir,
			lazyView, verdictResolutions, pinMode, cfg.DiagOut, pluginfetchverify.RemoteTargetFor(verifyHTTPClient)); verr != nil {
			return Result{}, fmt.Errorf("verify: %w", verr)
		}
		// The toolchain the lockfile records is handed back for the caller to
		// report. Verify is the surface a user reaches to find out what a lockfile
		// says, and it has the file loaded already, so the stamp is read back here
		// rather than through an operation of its own. It is reported and never
		// compared against this binary: the toolchain is not part of the pinned
		// closure, and checking it would pin the compiler by proxy.
		return Result{LockfilePath: lockfilePath, Verified: true, Closure: cl, Toolchain: lf.Toolchain}, nil
	}

	// Closure-scoped Pass-2: validate ONLY the entities the Release reaches against
	// the closure's libraries, read through lazyView (the resolved closure, library
	// $refs expanded against the lazily-walked SchemaLibrary set) — the same gate
	// build applies, so this operation and a build of the same Release agree on what
	// is valid. An in-closure schema-invalid entity, or a $ref to a skipped/missing
	// library, fails loudly here; an out-of-closure break is never validated.
	report.phase(PhaseValidate)
	if err := lazyView.ValidateClosureScopedPillars(ctx, cl); err != nil {
		return Result{}, cfg.annotate(rw, err, lazyView.SkippedEntities())
	}

	// Per-component source resolution. Build the graph through the compiler's
	// one graph-construction entry — the path a build reaches too — so
	// each component's source resolves against that component's own
	// cascade-resolved infra (not a flattened Release-wide bag), and the
	// component-to-Recipe join uses the same Manifest mapping the closure walker
	// recorded (lock-time cannot drift from build-time). aggBytes feeds the
	// resolver, which re-parses internally.
	//
	// What this needs is the graph, not a gated preview. This is the operation
	// that FILLS the record a drift verify reads, and at this point no record
	// exists to hold anything to; the verify runs once one does, over the lockfile
	// this pass composes, in the discovery render below. The gates that belong here
	// are the ones already run above, against the closure this pass is pinning.
	report.phase(PhaseSources)
	aggBytes := aggRes.Bytes
	pullLookup := lazyView.AsCatalogLookupForClosure(ctx, cl)
	// The lockfile this run assembles is carried out of the source-descent scope
	// and written further down, once the external-input discovery below has had its
	// chance to add to it. Assembling and writing are separate steps for two
	// reasons that point the same way. The discovery pass renders the Release,
	// which is a resolve scope of its own and does not belong nested inside the
	// one the descent holds open. And a write here followed by a second write
	// afterwards would put a lockfile on disk that is missing pins this run is
	// about to acquire, so anything reading the file in between would read a
	// closure this run never produced.
	var assembled *lockfile.Lockfile
	// The files a reporting run would have written, collected as the run reaches
	// each of them and sorted by path once the set is complete. Empty on a writing
	// run: each write below performs itself instead of reporting.
	var reported []dryrunreport.File
	// The configuration the graph below is built against: this workspace's own,
	// with the transform-role attachments replaced by the reconstruction of the
	// records this pass just resolved.
	//
	// The substitution is what makes the graph drawn here the graph a later build
	// draws. A transform attachment's settings and source content reach the guest
	// from the RECORD, never from live configuration or the host filesystem, so a
	// live attachment handed straight over arrives carrying no source content at
	// all — the guest then runs on an empty input set and decides nothing, which is
	// indistinguishable from a guest that ran and found nothing to do. Running the
	// reconstruction instead means both passes run one attachment list: the content
	// is identical by construction, because this pass read those files a moment ago
	// and is about to record exactly what it read.
	//
	// Only the transform half is composed here, and the omission of the verdict
	// half is deliberate. The graph entry dispatches the two roles that shape the
	// graph and nothing else, so no verdict-role attachment runs on this path;
	// carrying the verdict list into a configuration nothing on this path reads
	// would state, to the next reader, that those gates ran.
	//
	// It would also be a second composition of the governance attachment list, and
	// that list has one home for a reason that does not apply here but would apply
	// to whatever this site grew into: composing it is the same act as acquiring
	// the digests recorded as trusted for the verdict attachments, and a surface
	// that composes without acquiring runs every configured gate while holding none
	// of them to the identity that was reviewed — silently, because an unsupplied
	// record is indistinguishable from a workspace that has recorded nothing.
	graphCfg := wsCfg
	graphCfg.Plugins.Governance = lockfile.MutatorAttachmentRefs(mutatorAttachments)
	// The graph entry scopes the resolved view + parameter map to this
	// callback, so the entire source-descent runs while parameters are live
	// and nothing here can hold them past it. The descent reads BoundaryInfra
	// and the materialised IR pillars, never the raw parameter map.
	if err := pipeline.BuildGraph(
		ctx,
		pipeline.Inputs{
			Release:           rel,
			Manifest:          manifest,
			AggregateRawBytes: aggBytes,
			AggregatePath:     catalog.RelativeToWorkspace(workspaceRoot, aggRes.Path),
			// The Release's path as this pass holds it; the pipeline renders it
			// against WorkspaceRoot below, so a finding about what the Release wrote
			// names its file here as it does on a build.
			ReleasePath:     cfg.ReleasePath,
			CompilerVersion: cfg.CompilerVersion,
			// The composed configuration above, not the live one: a transform-role
			// attachment shapes the graph, so a graph drawn without it is not the
			// graph these pins describe.
			WorkspaceConfig: &graphCfg,
			// The root and the cache directory the transform tier resolves a
			// non-embedded guest through. No lockfile: this pass is the one that
			// writes it, so there is nothing yet to pin against, and an attachment
			// whose bytes are not already cached halts rather than resolving to
			// something unpinned.
			WorkspaceRoot: workspaceRoot,
			// Stated rather than left out, and pin-free rather than pinned-but-empty.
			// This pass produces the record, so no recorded identity governs what it
			// resolves, and a pinned claim here would report every guest it resolves
			// as missing a digest that was never going to exist yet — advising a pull
			// from inside the pull.
			PluginPins: pluginpath.NoPins(),
			// This resolution is the first of the two the pull makes of the Release,
			// so its advisories are shown here, and the render that follows records
			// them without showing them again.
			Display:           cfg.Display,
			CatalogAdvisories: lazyView.SchemaAdvisories,
		},
		pullLookup,
		lazyView.RecipeLookupWithManifest(manifest),
		lazyView.SubAggregateLoader(),
		func(gs *pipeline.GraphScope) error {
			topo := gs.Topology
			// Attachment instances are derived from the IR render-set, not a Release
			// walk. The renderer renders graph.Spec (the IR), whose attachment set is
			// assembled across all five tiers (Aggregate component/subsystem, Release
			// boundary-wide/component, annotation-expanded) at graph build — a
			// Release-boundary walk and the topology are each short by tiers, so the
			// using-set must come from the graph for the no-using-instance skip to be
			// congruent with rendering. Per-instance infra is the resolved entry.Infra,
			// so no Release-side join is needed.
			//
			// Building a graph here is acyclic, and the claim is about WHICH
			// artifacts it depends on rather than about none at all. It consumes
			// topology, resolve and catalog and copies each recipe source pointer
			// without fetching it, so nothing it reads depends on the per-component
			// sources this pass is about to resolve.
			//
			// It does dispatch the guests of the transform-role attachments, whose
			// bytes are a different axis and are already in hand: the closure walk
			// records each transform plugin's entity on the pinned axis and the
			// plugin-artifact descent above brings its .wasm down, both before this
			// point. So the dependency runs one way — the graph build reads an axis
			// this pass finished, never one it is still resolving.
			//
			// What that costs is the graph-build failure surface, which this operation
			// therefore inherits, now including a transform attachment whose guest
			// will not resolve. That is the same refusal a build of the same workspace
			// raises, arriving at the pass that writes the record rather than at the
			// first pass to read it.
			graph := gs.IRGraph

			// Key per-instance infra by the Manifest-mapped entity's identity: the kind
			// the mapping binds (a component mapping binds a Recipe, a feature mapping a
			// FeatureRecipe) and the mapping value — the same verbatim string the closure
			// walker records as the source-descent entity (catalog.ComponentRecipeURI /
			// FeatureRecipeURI return the mapping value verbatim), so this map joins
			// against closure entities regardless of short-form vs full-path mapping
			// authoring, and a Recipe and a FeatureRecipe sharing a name are each handed
			// only their own instances. An instance with no Manifest mapping carries no
			// source-descent entity and is skipped.
			perEntityInfra := map[closure.EntityKey][]closure.ComponentSourceInfra{}

			executionScopeOf := func(instanceName, featType string) (string, bool) {
				// Scope classification (name tier wins over type): a miss OR a
				// version-ambiguous mapping falls through to the per-component pin,
				// preserving prior behavior. The loud ambiguity halt belongs to the
				// source-pin joins below (irEntityInfra for features, ComponentRecipeURI
				// for components), where a wrong pick would mis-pin a fetched source.
				frURI, ok, _ := catalog.FeatureRecipeURI(manifest, instanceName, featType)
				if !ok {
					return "", false
				}
				fr, ok := lazyView.FeatureRecipeByURI(frURI)
				if !ok {
					return "", false
				}
				return fr.Spec.ExecutionScope, true
			}
			featInfra, err := irEntityInfra(graph, manifest, executionScopeOf)
			if err != nil {
				return fmt.Errorf("resolving the FeatureRecipe the Manifest maps each Feature to: %w", err)
			}
			for frURI, comps := range featInfra {
				key := closure.EntityKey{Kind: closure.KindFeatureRecipe, URI: frURI}
				perEntityInfra[key] = append(perEntityInfra[key], comps...)
			}

			// Component instances: the component->Recipe join comes from the flattened
			// topology; the per-component source-eval values and infra come from the
			// MATERIALIZED IR pillars (graph.Spec.ValuesMapping / InfraMapping — schema
			// defaults applied), the same pillars the renderer resolves the source
			// against, so a value/infra-driven source identity pins identically at lock
			// and build. Runs after BuildIR so the materialized pillars exist.
			for _, node := range topo.Nodes.All() {
				recURI, found, err := catalog.ComponentRecipeURI(manifest, node.InstanceID, node.ComponentType)
				if err != nil {
					return fmt.Errorf("resolving the Recipe the Manifest maps Component %q to: %w", node.ComponentType, err)
				}
				if !found {
					continue
				}
				key := closure.EntityKey{Kind: closure.KindRecipe, URI: recURI}
				perEntityInfra[key] = append(perEntityInfra[key],
					closure.ComponentSourceInfra{
						Component: node.InstanceID,
						Infra:     parse.NodePayloadMap(graph.Spec.InfraMapping, node.InstanceID),
						Values:    parse.NodePayloadMap(graph.Spec.ValuesMapping, node.InstanceID),
					})
			}

			// One resolver serves both the descent and the pin step below, and it
			// fetches each distinct source once: the pin step resolves the
			// authored-identity sources of the entities this Release builds a second
			// time, and those are the same requests the descent has just made.
			srcResolver := resolveEachSourceOnce(cfg.sourceResolver(ctx, wsCfg))
			// Ahead of the source descent, which fetches and digests every remote
			// source the closure names. The descent itself now runs under this
			// context — the shim carries it into each source's fetch — so a
			// withdrawal arriving part way through ends the download in flight and
			// the descent reports it. This check is what answers a caller who had
			// already withdrawn before the descent began, in one stated reason
			// rather than as whichever source's transport happened to fail first.
			if err := stopped(ctx); err != nil {
				return err
			}
			sourceArtifacts, err := closure.DescendRecipeSources(
				perEntityInfra, cl, lazyView.ClosureRecipeLoader(), srcResolver,
				workspaceRoot, cacheDir,
			)
			if err != nil {
				return fmt.Errorf("resolving the source of each Recipe and FeatureRecipe the Release uses: %w", err)
			}
			cl.SourceArtifacts = sourceArtifacts

			// Hold each freshly-resolved source reference to the version the prior
			// lockfile pins for it. A tag or a branch is a name at a remote and the
			// resolver says so in its own contract: only a commit is immutable, a
			// re-pushed reference is drift at the next pull, and an upgrade is the
			// remediation. This is where that sentence is kept.
			//
			// The position is the point. It sits after the descent, so the freshly
			// resolved version exists; and before the pin step, which is the first act
			// of this run that can WRITE — asked to pin, it records the resolved pins in
			// the lock beside each entity with a writable home, and that file is
			// workspace content a caller cannot take back. Halting here leaves the
			// workspace exactly as the run found it.
			//
			// A full refresh carries no prior lockfile, so it compares against nothing
			// and adopts — which is how `aggrega pull --upgrade` remains the remediation
			// without this site testing a flag. A targeted upgrade does hold a prior
			// lockfile: it names entities to float, a source reference is not one of
			// them, and floating an unnamed one silently is the thing being fixed.
			if drift := lockfile.VerifySourceReferences(pinReuse.Prior, sourceArtifacts); drift != nil {
				return drift
			}

			// The pin step over the entities this Release builds: it either records
			// each one's pins in the lock beside it or holds that lock to them and
			// halts on one that does not record them — which of the two is this
			// invocation's answer, falling back to the workspace's setting. See
			// entityPins for which entities it covers.
			//
			// Ahead of it, because pinning is the first step in this run that
			// writes: it records pins beside each entity with a writable home, and
			// that file is workspace content a caller cannot take back. Everything
			// above it is fetching and computing, which a withdrawn caller simply
			// discards.
			//
			// The check is kept on a reporting run and on a run that only holds the
			// locks to their pins, neither of which writes here and so neither of
			// which has anything to protect. It is one comparison, and a guard that
			// fires only in one mode is a guard whose other mode nobody exercises —
			// so a caller that has withdrawn stops at the same point either way, and
			// the reason it stops does not depend on what it asked for.
			if err := stopped(ctx); err != nil {
				return err
			}
			pinnedEntityDigests, pinnedFiles, err := entityPins(lazyView, cl.Entities, perEntityInfra,
				srcResolver, workspaceRoot, cacheDir, wsCfg.PullPinsSources(cfg.PinSources), cfg.DryRun,
				wsCfg.Plugins.ProviderTargetURIs(), namedDirs)
			if err != nil {
				return fmt.Errorf("pin sources: %w", err)
			}
			reported = append(reported, pinnedFiles...)
			// Where an entity's local source was pinned under the ignore rules of a
			// plugin other than the one this workspace binds for its capability, say
			// so — never halt. It runs after the pin step, so an entity this run has
			// just pinned is compared with the lock as written; a run that reports
			// rather than writes would compare the lock it did not write, and says
			// nothing.
			if !(cfg.DryRun && wsCfg.PullPinsSources(cfg.PinSources)) {
				adviseIgnoreRuleDifferences(cfg.DiagOut, lazyView, cl.Entities, perEntityInfra,
					wsCfg.Plugins.ProviderTargetURIs())
			}

			lf := buildLockfile(cfg.ReleasePath, cl, cfg.Toolchain, resolvedBindings, mutatorAttachments, verdictInputs, pinnedEntityDigests)
			// Every entity row records its kind; a row that did not would be refused as
			// the lockfile is read again, and the pull that clears that refusal would
			// compose the same row, so it is refused here, before anything is written.
			if err := lockfile.CheckEntityKinds(lf); err != nil {
				return err
			}
			// Carry forward each unchanged pin's recorded facts — its verified
			// signature and its deprecation marker — from the prior lockfile before
			// verification runs. An entry the content cache served that carries its
			// facts is not verified or re-discovered below, because both passes read
			// the registry and a cache hit does not touch it; the prior facts hold for a
			// pin that is unchanged. An entry fetched from the registry is verified and
			// re-discovered below, and so is a cache-served one a build would refuse —
			// its row still records no signature where one is required, or records one
			// the build does not accept; what that finds overwrites whatever was
			// carried, on its own row and on every row pinning the same bytes.
			// Without this, a warm re-pull — or a build's honoring automatic fill —
			// would silently drop a recorded signature and a recorded deprecation, the
			// second of which masks a deprecated dependency.
			carryForwardVerifiedFacts(lf, pinReuse.Prior)
			// Carry forward the prior lockfile's external-input pins. These are
			// render-time external-input pins, not part of the catalog closure
			// buildLockfile materialises, so this operation would otherwise assemble a
			// lockfile with an empty list and discard pins whose content is already in
			// the store. The discovery render below adds whatever this Release now
			// requests on top of what is carried here. A full refresh reads no prior
			// lockfile, so it carries nothing and the discovery render re-pins the set
			// from scratch.
			carryForwardExternalInputs(lf, pinReuse.Prior)
			// Lock-time signature verification: verify each registry-resolved
			// entity's signature against the registry's trust policy, record
			// the verified fact, and cache the bundle for air-gapped re-verify.
			// This operation verifies; build reads the recorded fact and never
			// re-verifies (the AOT boundary). The verify is explicitly requested, so
			// when the workspace is offline a registry-resolved entity's referrer
			// fetch is refused loudly through the egress gate (a nil client) rather
			// than silently passing as unverified — and so is a cache-served entry
			// whose row records no signature a requirement asks for, or records one a
			// build does not accept, which the pass asks its registry about rather
			// than write a row a build would refuse. A closure with nothing to ask a
			// registry about needs no fetch and is unaffected.
			verifyHTTPClient, _ := egress.HTTPClient(wsCfg)
			if err := VerifyClosureSignatures(ctx, lf, cl, wsCfg, workspaceRoot, cacheDir, cfg.DiagOut, pluginfetchverify.RemoteTargetFor(verifyHTTPClient)); err != nil {
				return fmt.Errorf("verify signatures: %w", err)
			}
			assembled = lf
			return nil
		}); err != nil {
		return Result{}, err
	}
	// The graph callback above is the only thing that assembles this, so a nil here
	// means the graph build reported success without ever running its callback. That
	// is a wiring error rather than a state this run can repair — a lockfile
	// invented at this point would record a closure no descent produced — so it is
	// reported, on the same terms a nil workspace is.
	if assembled == nil {
		return Result{}, fmt.Errorf("internal: no lockfile was assembled — the graph build reported success without running its callback")
	}

	// External-input discovery. Which URLs a build consumes is a product of the
	// render rather than a declaration in any document, so this run renders the
	// Release to find out, fetches what it asks for, and pins it beside the
	// component pins — one command, one moment, one rule for a reader to learn.
	//
	// It runs unconditionally, and that is the whole of the acquisition rule: this
	// is the only moment anything acquires. No render anywhere else does — a build
	// is held to the closure this write records, whichever surface asked for it —
	// so a run that skipped discovery would leave nothing else to pin what a build
	// will ask for. There is consequently no caller declaration to consult and no
	// dial to turn it off, because either would preserve exactly the surprise the
	// single moment exists to remove: a Release that resolves cleanly while an
	// input it needs is still unpinned.
	//
	// A failure here does not fail the run, with one exception. The dependency
	// resolution above finished and its pins are correct whatever stopped the
	// render, so the pins are written and the failure is reported: a resolution
	// that names what it could not reach is more use than one that throws away work
	// a reader would only redo. The exception is the render refusing the lockfile
	// itself — the build's drift verify, run over the closure a build of this
	// Release reconstructs, disagreeing with what this run composed. Then the pins
	// are NOT correct for the build that reads them, so the run fails and writes
	// neither the lockfile nor the plugin trust record, rather than leaving a
	// lockfile on disk that the next build refuses.
	//
	// A withdrawal is observed ahead of it for the same reason: the render is the
	// one moment anything here acquires, reaching the network and writing what it
	// fetches into the content-addressed store, so a run the caller has given up
	// on must not begin one.
	if err := stopped(ctx); err != nil {
		return Result{}, err
	}
	report.phase(PhaseExternalInputs)
	// The record beside the lockfile, as this run will write it. The render below
	// holds each verdict-role plugin to the signature fact this run verified, and the
	// record is written only after the render, so the render is handed it as composed
	// rather than reading the file it is about to replace — which on a first
	// resolution does not exist yet. The write further down works the same outcome
	// out from the same inputs.
	composedTrust, err := verdictTrustAfterPull(cfg.ReleasePath, wsCfg.VerdictAttachments(), verdictResolutions, pinMode)
	if err != nil {
		return Result{}, err
	}
	pins, discoveryRender, discoveryErr := discoverExternalInputs(ctx, cfg, wsCfg, workspaceRoot, cacheDir, lazyView, assembled, composedTrust.Trust)
	assembled.ExternalInputs = pins
	// Ahead of BOTH the discovery diagnostic and the write, and the order matters.
	// A withdrawal reaching the render arrives back here as a render failure, and
	// reporting it as one would tell a reader that the dependency pins "were
	// written and are complete" while this run is about to write nothing at all.
	// The write below is the run's product, so this is the last moment at which a
	// stop leaves the pins already on disk exactly as they were.
	if err := stopped(ctx); err != nil {
		return Result{}, err
	}
	if compilerops.IsComposedLockfileRefusal(discoveryErr) {
		return Result{}, discoveryErr
	}
	if discoveryErr != nil {
		reportExternalInputDiscoveryFailure(cfg.DiagOut, lockfilePath, len(pins), cfg.DryRun, discoveryErr, cfg.FormatFailure)
	}

	report.phase(PhaseLockfile)
	// The lockfile is a file this run composes beside the Release, so it is held to
	// its root before it is written — and before it is reported, so a report halts
	// where the write would.
	if err := writescope.CheckWrite(releaseRoot, lockfilePath); err != nil {
		return Result{}, fmt.Errorf("write lockfile: %w", err)
	}
	if cfg.DryRun {
		// The bytes come from the write's own serializer, through the timestamp rule
		// the write applies, so what is reported here is what the write would put
		// there rather than a second rendering of the same lockfile.
		want, merr := lockfile.CanonicalBytesPreservingTimestamp(lockfilePath, assembled)
		if merr != nil {
			return Result{}, fmt.Errorf("report lockfile: %w", merr)
		}
		if f := reportedWrite(lockfilePath, want); f != nil {
			reported = append(reported, *f)
		}
	} else if err := lockfile.WriteLockfilePreservingTimestamp(lockfilePath, assembled); err != nil {
		return Result{}, fmt.Errorf("write lockfile: %w", err)
	}
	// Record the identity resolved for each verdict-returning attachment, beside
	// the lockfile just written, and drop any entry the configuration no longer
	// accounts for when this run asked for that. This runs only here, past the
	// verify-only return above, so a verify-only run neither records nor drops
	// anything.
	//
	// The attachment set goes in alongside the resolutions because the two answer
	// different halves of one question. The resolutions say what was acquired,
	// which is what gets recorded; the attachments say what the configuration
	// still names, which is what keeps an entry from being dropped — and a
	// detached plugin is precisely the case with an entry and no resolution.
	var trustedCount int
	if cfg.DryRun {
		count, trustFile, terr := reportVerdictPluginTrust(releaseRoot, cfg.ReleasePath, wsCfg.VerdictAttachments(), verdictResolutions, pinMode)
		if terr != nil {
			return Result{}, terr
		}
		trustedCount = count
		if trustFile != nil {
			reported = append(reported, *trustFile)
		}
	} else {
		count, terr := recordVerdictPluginTrust(releaseRoot, cfg.ReleasePath, wsCfg.VerdictAttachments(), verdictResolutions, pinMode, cfg.DiagOut)
		if terr != nil {
			return Result{}, terr
		}
		trustedCount = count
	}
	// Sorted by path once the set is complete, rather than left in the order the
	// run reached each file. The closure's entity order is not a promise, so a
	// report carrying the collection order would reshuffle between two runs over
	// identical input — and a report of what a run would write has to be as stable
	// as what it would write.
	sort.Slice(reported, func(i, j int) bool { return reported[i].Path < reported[j].Path })
	return Result{
		LockfilePath:           lockfilePath,
		Closure:                cl,
		TrustedPlugins:         trustedCount,
		ExternalInputs:         len(assembled.ExternalInputs),
		DiscoveryRender:        discoveryRender,
		ExternalInputDiscovery: discoveryErr,
		DryRunFiles:            reported,
	}, nil
}

// reportedWrite returns the entry a reporting run makes for a file whose
// post-run bytes are want, and nil where the file already holds exactly those
// bytes.
//
// A file the run would leave byte-identical is deliberately not named: naming it
// would tell a reader a write was coming for it, and the one question a report of
// this kind answers is which files move. A file that is not there yet has no
// bytes to match, so it is reported — which is how a first resolution names the
// lockfile it would create.
func reportedWrite(path string, want []byte) *dryrunreport.File {
	if prior, err := os.ReadFile(path); err == nil && bytes.Equal(prior, want) {
		return nil
	}
	return &dryrunreport.File{Path: path, Content: want}
}

// buildLockfile materialises a *lockfile.Lockfile from the closure
// result. Field-for-field copy across the closure / lockfile package
// boundary; both types intentionally mirror the same shape.
//
// toolchain is the caller's record of which compiler and which core plugins
// produced the file. Versions only, so it stays identical across machines that
// resolved the same closure with the same compiler; advisory, so nothing compares
// it.
func buildLockfile(releasePath string, cl closure.Closure, toolchain *lockfile.ToolchainStamp, bindings map[string]lockfile.ProviderBinding, mutators []lockfile.MutatorAttachment, verdictInputs []lockfile.VerdictInput, pinnedEntityDigests map[closure.EntityKey]string) *lockfile.Lockfile {
	lf := &lockfile.Lockfile{
		Version:     1,
		Release:     filepath.Base(releasePath),
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Toolchain:   toolchain,
	}

	for _, b := range bindings {
		lf.Providers = append(lf.Providers, b)
	}
	sort.Slice(lf.Providers, func(i, j int) bool { return lf.Providers[i].Capability < lf.Providers[j].Capability })

	// Author order is semantic for the transform runner, so the attachments are
	// carried through unsorted (WriteLockfile likewise preserves the order). The
	// copy avoids aliasing the caller's slice.
	lf.Mutators = append([]lockfile.MutatorAttachment(nil), mutators...)

	// Verdict-input digest records are provenance (WriteLockfile canonicalises
	// their order), so they are carried through with only an aliasing-safe copy.
	lf.VerdictInputs = append([]lockfile.VerdictInput(nil), verdictInputs...)

	for _, d := range cl.ReleaseDocuments {
		lf.ReleaseDocuments = append(lf.ReleaseDocuments, lockfile.ReleaseDocument{Path: d.Path, Digest: d.Digest})
	}

	for _, e := range cl.Entities {
		// A writable recipe's row is overlaid from the pin step's own reading of the
		// entity, taken after anything the step wrote, so the lockfile records the
		// entity as the build will digest it. The row covers the lock beside the
		// document, which the step writes after the dependency resolution digested
		// the entity, so the overlay is what makes the recorded row describe the
		// lock the step left. The step's reading is keyed by the identity of the
		// entity it read, so it lands on that entity's row and on no row of another
		// kind sharing the name.
		entDigest := e.Digest
		if pf, ok := pinnedEntityDigests[e.Key()]; ok {
			entDigest = pf
		}
		lf.Entities = append(lf.Entities, lockfile.Entity{
			Kind:            e.Kind(),
			URI:             e.URI,
			ResolvedVersion: e.ResolvedVersion,
			Digest:          entDigest,
			ManifestDigest:  e.ManifestDigest,
			ReferencedFrom:  append([]string(nil), e.ReferencedFrom...),
		})
	}
	sort.Slice(lf.Entities, func(i, j int) bool { return lf.Entities[i].Key().Less(lf.Entities[j].Key()) })

	for _, l := range cl.SchemaLibraries {
		lf.SchemaLibraries = append(lf.SchemaLibraries, lockfile.SchemaLibrary{
			URI:             l.URI,
			ResolvedVersion: l.ResolvedVersion,
			Digest:          l.Digest,
			ManifestDigest:  l.ManifestDigest,
			Consumers:       copyConsumers(l.Consumers),
		})
	}
	sort.Slice(lf.SchemaLibraries, func(i, j int) bool { return lf.SchemaLibraries[i].URI < lf.SchemaLibraries[j].URI })

	for _, l := range cl.MappingLibraries {
		lf.MappingLibraries = append(lf.MappingLibraries, lockfile.MappingLibrary{
			URI:             l.URI,
			ResolvedVersion: l.ResolvedVersion,
			Digest:          l.Digest,
			ManifestDigest:  l.ManifestDigest,
			Consumers:       copyConsumers(l.Consumers),
		})
	}
	sort.Slice(lf.MappingLibraries, func(i, j int) bool { return lf.MappingLibraries[i].URI < lf.MappingLibraries[j].URI })

	for _, l := range cl.WorkloadLibraries {
		lf.WorkloadLibraries = append(lf.WorkloadLibraries, lockfile.WorkloadLibrary{
			URI:             l.URI,
			ResolvedVersion: l.ResolvedVersion,
			Digest:          l.Digest,
			ManifestDigest:  l.ManifestDigest,
			Consumers:       copyConsumers(l.Consumers),
		})
	}
	sort.Slice(lf.WorkloadLibraries, func(i, j int) bool { return lf.WorkloadLibraries[i].URI < lf.WorkloadLibraries[j].URI })

	for _, l := range cl.ArtifactLibraries {
		lf.ArtifactLibraries = append(lf.ArtifactLibraries, lockfile.ArtifactLibrary{
			URI:             l.URI,
			ResolvedVersion: l.ResolvedVersion,
			Digest:          l.Digest,
			ManifestDigest:  l.ManifestDigest,
			Consumers:       copyConsumers(l.Consumers),
		})
	}
	sort.Slice(lf.ArtifactLibraries, func(i, j int) bool { return lf.ArtifactLibraries[i].URI < lf.ArtifactLibraries[j].URI })

	for _, w := range cl.Plugins {
		lf.Plugins = append(lf.Plugins, lockfile.Plugin{
			URI:             w.URI,
			ResolvedVersion: w.ResolvedVersion,
			Digest:          w.Digest,
			ManifestDigest:  w.ManifestDigest,
			ReferencedFrom:  append([]string(nil), w.ReferencedFrom...),
		})
	}
	sort.Slice(lf.Plugins, func(i, j int) bool { return lf.Plugins[i].URI < lf.Plugins[j].URI })

	for _, s := range cl.SourceArtifacts {
		refs := make([]lockfile.SourceArtifactReference, 0, len(s.ReferencedBy))
		for _, r := range s.ReferencedBy {
			refs = append(refs, lockfile.SourceArtifactReference{
				Entity:       r.Entity,
				URI:          r.URI,
				Field:        r.Field,
				RequestedRef: r.RequestedRef,
			})
		}
		lf.SourceArtifacts = append(lf.SourceArtifacts, lockfile.SourceArtifact{
			Kind:           s.Kind,
			URL:            s.URL,
			Chart:          s.Chart,
			ModuleSubdir:   s.ModuleSubdir,
			PinnedVersion:  s.PinnedVersion,
			Digest:         s.Digest,
			ManifestDigest: s.ManifestDigest,
			Mediatype:      s.Mediatype,
			ResolvedURL:    s.ResolvedURL,
			ReferencedBy:   refs,
		})
	}

	return lf
}

// resolveMutatorAttachments resolves each configured transform-role governance
// attachment (label-transform / annotation-transform) to the pinned record the
// lockfile carries: the plugin's resolved coordinate identity, the transform
// role, the export override, the attachment's settings object, and a
// content-addressed pin of each declared source file, in authored order. Build
// and serve reconstruct the attachment list from these records rather than from
// live workspace config, so the set / order / role / export function / settings /
// source content that shapes the rendered output is config-invariant afterwards.
//
// Each attachment's Plugin entity resolves through the same pinned lazy view the
// rest of the resolution uses; the coordinate is derived through the single
// Plugin.Coordinate rule, the same identity ProviderBinding.Plugin records. A
// ref that does not resolve is a build fault (the closure walk already pinned
// the transform-role governance entity, halting loudly on a missing one), so it halts here rather
// than writing an unresolvable coordinate.
//
// Each declared source path is already anchored to an absolute host path by the
// config layer; the pin records the file's content verbatim, its sha256 digest,
// and its base name only (never the absolute host path — portability and
// determinism), sorted by base name so the pinned wire is deterministic. A read
// failure is a loud fault (a misconfigured attachment).
func resolveMutatorAttachments(refs []workspaceconfig.PluginRef, lazyView *catalog.LazyLookup) ([]lockfile.MutatorAttachment, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	out := make([]lockfile.MutatorAttachment, 0, len(refs))
	for _, ref := range refs {
		if ref.URI == "" {
			continue
		}
		p, ok := lazyView.PluginByURI(ref.URI)
		if !ok {
			return nil, fmt.Errorf("resolve: transform-role governance plugin %q not resolvable for lockfile pin", ref.URI)
		}
		att := lockfile.MutatorAttachment{
			Plugin:   p.Coordinate(),
			Function: ref.Function,
			// Pin the transform role verbatim so the build reconstructs the
			// attachment at the runner it was authored for. The role is recorded on
			// every attachment (never inferred at reconstruction), so the pinned
			// dispatch cannot diverge from the authored one.
			Role: string(ref.Role),
		}
		if ref.With != nil {
			att.Settings = ref.With.Settings
			for _, src := range ref.With.Sources {
				content, err := os.ReadFile(src)
				if err != nil {
					return nil, fmt.Errorf("resolve: read transform-role governance source %q: %w", src, err)
				}
				att.Sources = append(att.Sources, lockfile.MutatorSourcePin{
					Path:    filepath.Base(src),
					Content: string(content),
					Digest:  digest.ContentDigest(content),
				})
			}
			sort.Slice(att.Sources, func(i, j int) bool { return att.Sources[i].Path < att.Sources[j].Path })
		}
		out = append(out, att)
	}
	return out, nil
}

// resolveVerdictInputs records, for each verdict-role governance attachment in
// the workspace's verdict attachment set, the content digest of every external
// rule input it reads at compile time — a Kyverno / Rego file supplied through
// the attachment's with.sources. Unlike a transform attachment this is
// PROVENANCE, not a build input: the verdict plugin is cache-not-pin and reads
// its rule inputs LIVE, so the lockfile records only the digest (never the
// content), so the inputs that shaped an accept/reject decision are auditable
// and a changed rule file surfaces as a lockfile diff on the next run. An
// attachment that declares no sources contributes no entry. Digests are recorded
// in source base-name order for a deterministic record.
//
// The verdict plugin is cache-not-pin and so is absent from the entities /
// plugins sections; the record therefore identifies the attachment by its
// authored URI (self-standing provenance), not by a resolved coordinate that
// would join into those sections.
func resolveVerdictInputs(atts []workspaceconfig.VerdictAttachment) ([]lockfile.VerdictInput, error) {
	if len(atts) == 0 {
		return nil, nil
	}
	var out []lockfile.VerdictInput
	for _, att := range atts {
		// The record identifies an attachment by (plugin, role), so it can carry only
		// an attachment that HAS a role: a linter attachment is a verdict attachment
		// and is read from the same set, but it has no role to key by, so its rule
		// inputs are not recorded here. The narrowing is on what the record can
		// identify, never a second answer to which attachments are verdict
		// attachments. It reads the socket rather than the role string, because the
		// governance socket is the only one whose verdict membership is decided by a
		// role — a role authored onto any other socket's attachment resolves to none
		// of the roles this record can key by.
		if att.Socket != workspaceconfig.SocketGovernance {
			continue
		}
		ref := att.Ref
		if ref.With == nil || len(ref.With.Sources) == 0 {
			continue
		}
		vi := lockfile.VerdictInput{
			Plugin: ref.URI,
			Role:   string(ref.Role),
		}
		for _, src := range ref.With.Sources {
			content, err := os.ReadFile(src)
			if err != nil {
				return nil, fmt.Errorf("resolve: read verdict socket source %q: %w", src, err)
			}
			vi.Sources = append(vi.Sources, lockfile.VerdictSourcePin{
				Path:   filepath.Base(src),
				Digest: digest.ContentDigest(content),
			})
		}
		sort.Slice(vi.Sources, func(i, j int) bool { return vi.Sources[i].Path < vi.Sources[j].Path })
		out = append(out, vi)
	}
	return out, nil
}

// verdictPluginLookup is whether the walk over the plugins attached at a verdict
// role asks each plugin's registry for its signature and its withdrawal marker.
// Its zero value asks.
type verdictPluginLookup int

const (
	// askEachRegistry verifies each registry-served plugin's signature at its
	// registry and discovers its withdrawal there, as every pull does.
	askEachRegistry verdictPluginLookup = iota
	// askNoRegistry asks no registry anything, and marks each plugin whose
	// signature would have been asked about as not asked.
	askNoRegistry
)

// verdictPluginLookupFor is the one decision of whether the walk over verdict
// plugins asks their registries: every pull asks — offline too, where a plugin no
// recorded pin addresses halts the walk at its .wasm and any other question is
// refused by the egress gate — and so does every verify but one under the offline
// posture, which asks none. Such a verify still holds each plugin's identity to
// the record and re-checks its host functions, and it reports the signatures it
// did not check rather than refusing at the gate. A pull that writes asks always,
// because a resolution that asked nothing carries no fact for the record it
// writes.
//
// The offline posture is read from the egress gate's own answer for this run
// (egressErr, the error egress.HTTPClient returned), never from a second reading
// of the configuration.
func verdictPluginLookupFor(verify bool, egressErr error) verdictPluginLookup {
	if verify && errors.Is(egressErr, egress.ErrOfflineEgress()) {
		return askNoRegistry
	}
	return askEachRegistry
}

// cacheVerdictSocketPlugins resolves every verdict-socket (policy / linter) gate
// plugin the effective config attaches, fetching and caching a registry-served one,
// WITHOUT recording any of them in the lockfile. A verdict socket renders an accept/reject verdict without
// shaping the output bytes, so its plugin is cached-not-pinned: this operation
// populates the cache slot (entity config blob + OCI manifest, and the .wasm
// artifact) so a hermetic build can resolve and run the gate by URI, but the
// plugin never enters the reproducibility closure and is never drift-gated.
//
// It resolves each gate plugin the way the pinned mutator entity + artifact descent
// does — ResolveCatalogEntity for the entity, closure.ResolvePluginArtifact for the
// .wasm — inheriting that path's digest verification. So a gate plugin whose entity
// is authored in a catalog directory has its .wasm read from this machine through
// catalogArtifacts, the question the pull's descents ask about such an entity: no
// registry serving the same identity is asked for it, and one that no local source
// holds is refused as the descents refuse it. It does not append to the closure, so
// nothing it caches reaches the lockfile.
//
// Trust posture: a freshly cached gate plugin is verified exactly as a pinned
// plugin is — registry signature verification against the resolving registry's
// trust policy AND host-function stamp re-derivation from the cached .wasm —
// through the one fetch-verification home every tier shares
// (pluginfetchverify.Verify). Because a verdict gate is cache-not-pin, the verified
// fact is not recorded in the lockfile and never enters the reproducibility
// closure; it travels on the resolution to the record beside the lockfile instead,
// with its bundle cached, so a build holds the gate to it. A tampered or unsigned
// gate plugin a signature is required for halts the run here. A registry gate
// carries a manifest digest and is signature-verified; a local / embedded gate
// carries none and skips signature, exactly as the pinned path skips a
// non-registry item.
//
// A capability-only / empty-URI attachment carries nothing to fetch and nothing to
// identify, so the verdict attachment set omits it; an attachment whose ENTITY does
// not resolve is skipped rather than treated as a closure gap — a verdict gate is
// not on the pinned axis a missing closure entity would halt.
//
// The skip covers the entity axis only, and the artifact axis deliberately does
// not share it: an attachment whose entity resolves while its `.wasm` does not
// HALTS. That is the configured-but-unloadable gate, and a gate an operator
// attached which no build can load is exactly the case that must fail loudly
// rather than pass with the gate quietly absent.
//
// It returns the identity each attachment's plugin resolved to, in attachment
// order, for the caller to record beside the lockfile. Returning them rather than
// recording them here is what keeps a verify-only run from writing: the caching
// walk runs on every invocation, and only the writing one reaches the recording
// step. An attachment whose plugin is not in the content cache yields no identity
// — the same clean-miss skip the verification below takes.
//
// registries is whether the walk asks each plugin's registry for its signature
// and its withdrawal marker (verdictPluginLookupFor): every pull asks, and so does
// every verify but one under the offline posture, whose resolutions then say
// they were not asked. That verify alone reads a plugin whose .wasm the artifact
// resolution found no copy of from the content cache, as a build reads it.
func cacheVerdictSocketPlugins(ctx context.Context, cfg workspaceconfig.Config, lookup closure.Lookup, catalogArtifacts closure.LocalPluginArtifact, lazyView *catalog.LazyLookup, workspaceRoot, cacheDir string, targetFor pullverify.TargetFor, registries verdictPluginLookup, diagOut io.Writer) ([]verdictPluginResolution, error) {
	var resolutions []verdictPluginResolution
	for _, att := range cfg.VerdictAttachments() {
		ref := att.Ref
		entRes, err := lookup.ResolveCatalogEntity(ctx, ref.URI, closure.KindPlugin)
		if err != nil {
			return nil, fmt.Errorf("cache %s socket plugin %q: %w", att.Socket, ref.URI, err)
		}
		if entRes.NotFound {
			continue
		}
		bare, version := splitAttachmentRef(ref.URI)
		if version == "" {
			version = entRes.ResolvedVersion
		}
		// An entity the local catalog resolved is matched by its identity coordinate,
		// so the attachment's bare reference is that entity's artifact reference too.
		artRes, err := closure.ResolvePluginArtifact(ctx, lookup, catalogArtifacts, ref.URI, entRes.ResolutionPath, bare, version)
		if err != nil {
			return nil, fmt.Errorf("cache %s socket plugin artifact %q: %w", att.Socket, ref.URI, err)
		}
		// The lookup reads a registry-served plugin's cached .wasm only through a
		// recorded lockfile pin, which a plugin attached at a verdict role never has,
		// and otherwise fetches it from the plugin's registry. So where no registry is
		// reached the resolution finds no copy even of a plugin the content cache
		// holds. A run that asks no registry reads such a plugin from the content
		// cache as a build reads it instead (verifyCachedVerdictSocketPlugin); every
		// other run halts here, the offline pull among them.
		if artRes.NotFound && registries != askNoRegistry {
			return nil, fmt.Errorf("cache %s socket plugin artifact %q: entity resolved but its .wasm did not: %w",
				att.Socket, ref.URI, closure.ErrUnresolvableURI())
		}
		resolved, err := verifyCachedVerdictSocketPlugin(ctx, cfg, lazyView, ref.URI, artRes, workspaceRoot, cacheDir, targetFor, registries, diagOut)
		if err != nil {
			return nil, fmt.Errorf("verify %s socket plugin %q: %w", att.Socket, ref.URI, err)
		}
		if resolved.Coordinate == "" {
			continue
		}
		// The authored reference minus any version suffix is carried alongside the
		// resolved identity because a targeted upgrade names that form, not the
		// identity coordinate: the two coincide for a bare-name attachment and
		// diverge for a location-shaped one.
		resolved.BareURI = bare
		resolutions = append(resolutions, resolved)
	}
	return resolutions, nil
}

// verifyCachedVerdictSocketPlugin routes a freshly cached verdict-socket gate
// plugin through the shared fetch-verification home. It binds the plugin's
// host-less bare identity to its registry location and kind for signature referrer
// discovery through catalog.PluginSignatureSubjectOf — the one binding the build
// reads the same plugin's host and kind through when it holds the plugin to the
// fact recorded here — and reads the entity stamp + content-verified .wasm from the
// freshly populated cache (the same cache-not-pin read the build resolves the gate
// through), then hands both to pluginfetchverify.Verify.
//
// Where that verification verified a signature for a plugin it identified, the
// signature bundle is cached at the slot the build re-hashes and the fact rides on
// the returned resolution, for the record beside the lockfile: a verdict gate is
// cache-not-pin and never enters the lockfile, and that record is where a build
// reads its fact from. Where it verified none, the resolution carries no fact, and
// that absence is recorded too.
//
// It also returns the identity that one cache read reports — the plugin's identity
// coordinate, the version its entity declares, and the content digest of its .wasm
// layer. That triple comes from THIS read and no other, because the same read is
// what a later resolution of the same plugin performs: a second derivation of the
// digest from the manifest descriptor, or of the version from the entity
// resolution, would let the recorded identity and the resolved one disagree while
// both looked right. A clean cache miss returns the zero value, whose empty
// coordinate is the caller's signal that there was nothing to identify.
//
// Where registries is askNoRegistry the plugin's registry is asked nothing: the
// host functions are still re-derived from the cached bytes and held to the
// stamp, through the half of the fetch home that asks no registry, and a plugin
// whose signature that home would have asked about is returned marked as not
// asked, with no fact and no withdrawal. Its identity is the same cache read's, so
// the hold of the identity to the record is unchanged.
//
// artifact is the walk's resolution of the plugin's .wasm. Its manifest digest is
// the one the signature is asked about. Where it found no copy — which the walk
// hands on only where no registry is asked — nothing in this run wrote the .wasm,
// so the manifest is the one the cache read held the entity to, and the .wasm is
// read as a build reads it (wasmAsABuildReadsIt).
func verifyCachedVerdictSocketPlugin(ctx context.Context, cfg workspaceconfig.Config, lazyView *catalog.LazyLookup, uri string, artifact closure.Resolution, workspaceRoot, cacheDir string, targetFor pullverify.TargetFor, registries verdictPluginLookup, diagOut io.Writer) (verdictPluginResolution, error) {
	var identity verdictPluginResolution
	fp := pluginfetchverify.Fetched{
		URI:            uri,
		ManifestDigest: artifact.ManifestDigest,
		Kind:           catalog.EntityKindForHint(closure.KindPlugin),
		Host:           registryhost.Of(uri),
	}
	// A gate plugin's URI is a host-less bare identity; bind its registry host and
	// path from the workspace registry and the Plugin kind so referrer discovery
	// targets the location it was fetched from. A reference the binding finds no
	// registry for keeps the host its own text names, which the fetch home reads as
	// naming no registry, so it asks nothing about the signature.
	if subject, ok := catalog.PluginSignatureSubjectOf(cfg.DefaultRegistryLocation().Prefix(), uri); ok {
		fp.Host = subject.Host
		fp.Location = subject.Location
		fp.Kind = subject.Kind
	}
	// Read the entity stamp and its .wasm digest from the cache-not-pin slot the
	// resolution just populated — the same read the build uses — so the
	// host-function re-derivation runs against the identical bytes the build will.
	// A clean cache miss leaves the host-function inputs zero (skip); only a
	// verification/parse fault or a non-not-found read error halts.
	read, ok, err := lazyView.CachedPluginByURI(uri)
	if err != nil {
		return verdictPluginResolution{}, err
	}
	if artifact.NotFound {
		fp.ManifestDigest = read.ManifestDigest
	}
	if ok {
		entity := read.Entity
		artifactRef := entity.ArtifactReference()
		identity = verdictPluginResolution{
			Coordinate: artifactRef,
			Version:    entity.Metadata.Version,
			Digest:     read.WasmDigest,
		}
		var wasmBytes []byte
		var rerr error
		if artifact.NotFound {
			if wasmBytes, rerr = wasmAsABuildReadsIt(lazyView, read, uri, workspaceRoot, cacheDir); rerr != nil {
				return verdictPluginResolution{}, rerr
			}
		} else {
			wasmBytes, rerr = oci.ReadCached(workspaceRoot, cacheDir, oci.PluginArtifactKind(), artifactRef, read.WasmDigest)
			if rerr != nil && !errors.Is(rerr, oci.ErrFetchNotFound()) {
				return verdictPluginResolution{}, fmt.Errorf("read cached .wasm %s: %w", artifactRef, rerr)
			}
		}
		if rerr == nil {
			fp.Entity = &entity
			fp.EntityURI = uri
			fp.ArtifactURI = artifactRef
			fp.WasmBytes = wasmBytes
		}
	}
	if registries == askNoRegistry {
		if err := pluginfetchverify.VerifyWithoutSignature(fp, diagOut); err != nil {
			return verdictPluginResolution{}, err
		}
		identity.SignatureNotAsked = fp.AsksRegistryForSignature()
		return identity, nil
	}
	verified, err := pluginfetchverify.Verify(ctx, fp, cfg, targetFor, diagOut)
	if err != nil {
		return verdictPluginResolution{}, err
	}
	// The verified fact is recorded only where there is an identity to record it on,
	// and its bundle is cached where the build re-hashes it — the slot the closure
	// pass caches a lockfile row's bundle in, written by the same function.
	if verified != nil && identity.Coordinate != "" {
		if err := cacheBundleBlob(workspaceRoot, cacheDir, verified.BundleDigest, verified.Bundle); err != nil {
			return verdictPluginResolution{}, fmt.Errorf("cache signature bundle for %s: %w", uri, err)
		}
		identity.Signature = pluginfetchverify.SignatureFact(*verified)
	}
	// Withdrawal discovery runs at this same registry-touching moment, over the same
	// bound location the signature verification above just used. It runs only where
	// something was identified: with no identity there is no record to carry a
	// withdrawal, so discovering one would have nowhere to go.
	if identity.Coordinate != "" {
		rec, derr := discoverVerdictPluginDeprecation(ctx, cfg, pullverify.Entity{
			URI:            fp.URI,
			ManifestDigest: fp.ManifestDigest,
			Host:           fp.Host,
			Location:       fp.Location,
			Kind:           fp.Kind,
		}, targetFor, diagOut)
		if derr != nil {
			return verdictPluginResolution{}, derr
		}
		if rec != nil {
			identity.Deprecation = &pluginstrust.Deprecation{
				Message:  rec.Message,
				Severity: rec.Severity,
			}
		}
	}
	return identity, nil
}

// wasmAsABuildReadsIt reads the .wasm of a plugin attached at a verdict role from
// the content cache through the resolution a build loads such a plugin with,
// resolver.ResolveVerdictPluginFromCache, over the sources a build reads it from.
// It is handed the cache read of the plugin's entity already taken rather than
// reading the entity again, so the bytes of a plugin the binary does not carry are
// held to the .wasm digest that read reported: a .wasm that no longer hashes to it
// is refused, and so is one no source holds, each as the build refuses it.
//
// It is handed no record, so the bytes are held to the cached manifest alone. The
// walk holds the plugin's identity to the record (holdVerdictPluginsToTheirRecord),
// and the verify compares the signature the record carries through the build's own
// check of the record; a record handed here would instead halt the verify on a
// recorded signature that comparison reports.
func wasmAsABuildReadsIt(lazyView *catalog.LazyLookup, read catalog.CachedPlugin, uri, workspaceRoot, cacheDir string) ([]byte, error) {
	resolved, err := resolver.ResolveVerdictPluginFromCache(
		resolver.VerdictPluginTierSources{
			ColocatedDirs: lazyView.PluginColocatedDirs(),
			WorkspaceRoot: workspaceRoot,
			CacheDir:      cacheDir,
		},
		resolver.VerdictPluginTrust{},
		func(string) (types.Plugin, string, bool, error) { return read.Entity, read.WasmDigest, true, nil },
		uri)
	if err != nil {
		return nil, err
	}
	return resolved.Bytes, nil
}

func copyConsumers(in []closure.LibraryConsumer) []lockfile.LibraryConsumer {
	out := make([]lockfile.LibraryConsumer, 0, len(in))
	for _, c := range in {
		out = append(out, lockfile.LibraryConsumer{
			ConsumerURI: c.ConsumerURI,
			Fragments:   append([]string(nil), c.Fragments...),
		})
	}
	return out
}
