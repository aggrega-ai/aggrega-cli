// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compilerops

import (
	"context"
	"errors"
	"io"
	"slices"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/validateops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// ValidationClosureInputs carries the resolution inputs a front door supplies
// when it validates a Release. It is ClosureInputs without Augment, and the
// omission is the point: the footing a release-scope validation resolves on is
// fixed by the core that consumes the closure rather than chosen per front door,
// so there is no field here for a front door to set it with. Lockfile carries the
// same contract it has on ClosureInputs, which is where it is documented.
type ValidationClosureInputs struct {
	View            *catalog.LazyLookup
	ReleasePath     string
	WorkspaceRoot   string
	WorkspaceConfig workspaceconfig.Config
	Lockfile        *lockfile.Lockfile
}

// ResolveValidationClosure resolves the closure the release-scope
// semantic-validation core below runs over. Every front door that validates a
// Release resolves through here, so the footing is one value at one site rather
// than a field each front door fills for itself — and the reason it is off is
// held by the code that sets it instead of by a copy of a comment at each caller.
//
// Augment stays off: the release-scope validation core runs over the
// un-augmented closure (the same footing serve's Tier-4 validation uses).
// Folding the library axes here would turn a schema-pillar validation
// diagnostic — the case a $ref to an absent library exercises — into an earlier
// closure-resolution error, changing the verb's diagnostic surface.
//
// The references the modules make are resolved here too, through
// ResolveModuleReferences, so a type a module names and no catalog holds is
// refused as the author's fault before anything is compiled; that function says
// why the resolution cannot be left to the checks that follow.
//
// Only the construction is shared. The *ClosureError is handed back exactly as
// ResolveClosure produced it, stage included, because a front door annotates a
// resolution failure in its own terms and those surfaces legitimately differ.
func ResolveValidationClosure(ctx context.Context, in ValidationClosureInputs) (*ResolvedClosure, *ClosureError) {
	resolved, cerr := ResolveClosure(ctx, ClosureInputs{
		View:            in.View,
		ReleasePath:     in.ReleasePath,
		WorkspaceRoot:   in.WorkspaceRoot,
		WorkspaceConfig: in.WorkspaceConfig,
		Lockfile:        in.Lockfile,
		Augment:         false,
	})
	if cerr != nil {
		return nil, cerr
	}
	if cerr := ResolveModuleReferences(ctx, resolved); cerr != nil {
		return nil, cerr
	}
	return resolved, nil
}

// ResolveModuleReferences resolves every reference the modules a Release reaches
// make — each Aggregate its Aggregate imports, and each one those import in turn —
// over a closure ResolveClosure computed, and refuses the first that does not
// resolve, as a build refuses it. It is the one resolution of a module's
// references that runs without a lockfile, and every surface that needs them
// resolved without one asks it: a validation of the Release, a preview with no
// lockfile to reconstruct the module axis from, and a listing of the Release's
// dependencies.
//
// A reference that does not resolve is reported at the closure-compute stage,
// because it is the same failure the closure walk reports for a reference the
// Release's own Aggregate makes, found one module further in.
//
// # Why the references are resolved before anything is compiled
//
// The closure walk resolves the references the Release's own Aggregate makes and
// stops at the modules it imports: a module's body is walked by the sub-aggregate
// descent, which a build with a lockfile runs as it reconstructs what the lockfile
// pins. Without it nothing resolves a module's references ahead of the checks that
// compile the Release, and the first of those to meet a type a module names and no
// catalog holds is the sub-aggregate conformance check. That check is right to read
// a miss as a disagreement between the compiler's own views of the catalog, because
// it is entitled to assume every reference resolved before it ran — and on such a
// path that assumption would be false, so a fault the author can fix would be
// reported as a fault in the compiler. Resolving the references here keeps the
// assumption true: a reference nothing resolves is refused as the author's fault,
// naming the entity and where the module makes the reference, in the words a build
// uses; a type that resolves here and is then absent from the view the checks read
// is still the disagreement that check reports.
//
// A listing of a Release's dependencies compiles nothing, and asks for the same
// reason: it reports what a Release is built from, and a Release one of whose
// modules names nothing the catalog holds is not built from anything.
//
// # Why it is the build's descent, and why it descends over a copy
//
// It is the pinned-axis descent the build runs, handed the loader of the modules'
// bodies and no loader for either plugin pass, which makes both plugin passes do
// nothing: the plugin axes are what pinning records, and a caller with no lockfile
// holds nothing to hold them to. Reusing the descent rather than walking the
// modules again is what keeps a module's references resolved by one rule and
// refused in one wording on every surface.
//
// It descends over a copy of the entity set, and the copy is discarded. What
// follows covers the closure the walk computed — the closure-scoped checks, the
// pins, the listing — and widening that closure to the modules' entities would
// change what each of them reports for every Release that imports a module; this
// resolves what the modules reference and reports what does not resolve, and
// changes nothing else. Each entity's referencing sites are copied with it,
// because the descent merges into and sorts that list in place.
func ResolveModuleReferences(ctx context.Context, resolved *ResolvedClosure) *ClosureError {
	if err := resolveModuleReferences(ctx, resolved); err != nil {
		return &ClosureError{Stage: ClosureStageComputeClosure, msg: err.Error(), err: err}
	}
	return nil
}

// resolveModuleReferences is ResolveModuleReferences without the stage the
// failure is reported at.
func resolveModuleReferences(ctx context.Context, resolved *ResolvedClosure) error {
	loader, err := closure.RequireAggregateLoader(resolved.Lookup)
	if err != nil {
		return err
	}
	probe := closure.Closure{Entities: make([]closure.ResolvedRef, len(resolved.Closure.Entities))}
	for i, e := range resolved.Closure.Entities {
		e.ReferencedFrom = slices.Clone(e.ReferencedFrom)
		probe.Entities[i] = e
	}
	return closure.DescendPinnedAxes(ctx, &probe, loader, nil, resolved.Lookup, nil, nil, nil, nil, nil)
}

// ValidateInputs carries the explicit state Validate needs. View is the
// request-scoped catalog lookup every resolution reads through, so the whole
// validation sees one consistent pin snapshot. Closure is the pre-computed
// catalog closure the extends and pillar gates scope to. Release, Manifest,
// AggBytes, and AggregatePath are the pipeline's Steps 1-4 inputs;
// AggregatePath is a source-location label (no I/O). WorkspaceConfig and
// CompilerVersion feed the pipeline's resolution and provenance, and
// WorkspaceRoot anchors every cache address the resolution reads through; it
// carries its own contract below. ReleasePath locates the record the
// verdict-plugin trust gate reads and carries its own contract below.
type ValidateInputs struct {
	View    *catalog.LazyLookup
	Closure closure.Closure
	// ClosureLibrariesResolved is true where Closure was resolved with its library
	// axes folded in, so each library is read at the version Closure names. False,
	// which is what ResolveValidationClosure produces, reads each library at the
	// version a bare reference selects.
	ClosureLibrariesResolved bool
	Release                  *types.Release
	Manifest                 *types.Manifest
	// ReleasePath is the path of the Release being validated. It locates the record
	// of the plugin identities trusted for this Release's verdict attachments, which
	// sits beside that Release, and it is required: validation runs the same
	// pre-render phases a build runs, gate plugins included, so a validation that
	// could not locate the record would run those gates unheld. The refusal for an
	// unsupplied path is VerdictPluginTrust's, which is where the record's address is
	// derived, so there is one refusal rather than a second guard here that could
	// disagree with it.
	//
	// It is also handed to the pipeline as the Release's own path, so a finding
	// about what the Release wrote names the Release's file here exactly as it does
	// on every other surface that runs the pipeline over the same documents.
	ReleasePath string
	AggBytes    []byte
	// Lockfile is the record of what this Release resolved to, and supplying it is
	// what makes a validation run the pipeline on the same footing a build does. It
	// does two things at once, because both have one cause — the pipeline reads the
	// record for both and finds nothing when it is absent.
	//
	// It supplies the digests every plugin the pipeline dispatches is held to. This
	// validation runs the same pre-render phases a build runs, and two of them
	// dispatch operator-attached guests that CHANGE the graph, so a validation
	// without the record executes exactly the attachments that can alter what is
	// being validated, with nothing to check them against. A resolution that finds
	// no digest for a reference says so rather than failing, so a workspace that has
	// not been prepared yet still validates.
	//
	// And it supplies those attachments' own pinned inputs. A transform attachment's
	// source files are carried in the record rather than read from the host, so an
	// attachment reconstructed from it arrives with the content it was pinned to
	// while one taken live arrives with none at all — the guest then runs on an empty
	// input set and reports nothing, which reads exactly like a clean pass.
	//
	// A nil Lockfile is a legitimate value and keeps both of those off: the surface
	// that validates at editing cadence supplies none deliberately, so that it neither
	// re-derives the record on every keystroke nor republishes what the surfaces that
	// do read it have already reported.
	Lockfile      *lockfile.Lockfile
	AggregatePath string
	// WorkspaceRoot is the workspace's own directory, and it anchors every cache
	// address this validation resolves through: the plugin cascade's content-cache
	// tier and the two governance sockets that read a guest from the cache each
	// compose their path as this root joined with the configured cache directory.
	//
	// Omitting it does not disable those reads — it re-points them, which is why it
	// is supplied rather than left to a default. The configured cache directory
	// carries a default of its own and is never empty, so the cache tier is entered
	// either way; with no root the composed path stays relative and the read lands
	// under whatever directory the process was started in. A plugin whose bytes live
	// only in the workspace cache then resolves when the command happens to run at
	// the workspace root and is reported unresolvable from anywhere else — same
	// documents, same machine, different answer. Supplying it is what makes a
	// validation read the same cache a build of those documents reads, from any
	// working directory.
	WorkspaceRoot   string
	WorkspaceConfig workspaceconfig.Config
	CompilerVersion string

	// Display is where the validation's run displays what it raises as it raises
	// it (pipeline.Inputs.Display): a command hands its error stream, a
	// long-running process the stream it was launched with. Nil shows nothing;
	// every advisory still arrives on the result.
	Display io.Writer
}

// ValidateResult carries the semantic-validation outcome in the order the gates
// run. A caller inspects the fields in sequence: a non-empty ClosureExtendsErrors
// means the closure-scoped extends gate stopped validation there; otherwise a
// non-nil PillarError means the closure-scoped schema-pillar gate stopped it;
// otherwise a non-nil VerdictTrustError means the workspace configuration
// disagreed with the record of the plugin identities trusted for its verdict
// attachments and validation stopped before the pipeline ran; otherwise Tier4 and
// RunErr carry the pipeline outcome — the Steps 1-4 phases, then the Recipe and
// FeatureRecipe assertion stages, whose first failing assertion is RunErr in the
// shape a build reports it in — and ResolutionFindings carries the
// resolution-completeness findings its own producer still owns over the built IR
// graph — both feature attachment axes in full, and the component axis for a
// mapping value whose shape no recipe reference can express.
//
// Whether a component instance resolves to a recipe at all is not among them. It
// is decided earlier, over the flattened composition, where the node set is
// complete; each such finding travels out of the pipeline on
// Tier4.ResolveDiagnostics, the same channel the resolver-class violations
// arrive on, and a caller reading only ResolutionFindings for that axis would
// find it empty over a workspace that has one. A Manifest name-tier key that
// names nothing in the build is not among them either: it is decided once the
// graph is final, over the Manifest's own mapping entries, and travels on the
// same channel.
//
// InputKindErrors is an additional error class computed after the pipeline gates
// (only when the extends and pillar gates both passed): each entry names a
// closure Recipe or FeatureRecipe whose synthesized workload targets a provider
// whose serving plugin cannot synthesize — an early, non-authoritative detector
// whose entries contribute to a non-zero verdict alongside the pipeline
// diagnostics.
//
// LintWarnings and NarrowingWarnings are the advisory lines: the
// schema-pillar-overlap advisories for the closure's Components and Features,
// and the narrowing-on-extend advisories accumulated while composing the
// closure's extends chains. Nothing here folds them into the outcome, so they
// are exit-code-neutral wherever they are read.
//
// Each field's producer is single-sourced here, so the outcome is assembled in
// one place rather than in each caller.
type ValidateResult struct {
	ClosureExtendsErrors []error
	PillarError          error
	VerdictTrustError    error
	Tier4                *pipeline.Tier4Result
	RunErr               error
	ResolutionFindings   []error
	InputKindErrors      []error
	LintWarnings         []string
	NarrowingWarnings    []string
}

// Validate runs the semantic-validation core against explicit inputs: the
// closure-scoped extends gate, the closure-scoped schema-pillar gate, the
// verdict-plugin trust gate, the Steps 1-4 pipeline (resolve, topology, IR,
// analyse, Component and Feature assertions) followed by the stages a build runs
// ahead of its render to evaluate the Recipe and FeatureRecipe assertions against
// the Release's resolved values, the resolution-completeness findings over the
// built IR graph, the closure-scoped input-kind capability check, and the
// exit-code-neutral advisory lines. Nothing is rendered and nothing is written. It stops at the first of those three gates that fails and returns a
// discriminated result the caller maps into positioned diagnostics; it produces
// no diagnostics itself.
//
// The extends and pillar gates read through the request-scoped view, so an
// in-closure entity that a drifted cache slot skipped is reported rather than
// self-healed via a registry re-fetch. Only the entities the Release reaches
// are validated; a broken out-of-closure entity is never validated here.
//
// When the extends and pillar gates both pass, the pipeline runs and its
// diagnostics, the resolution-completeness findings, the input-kind capability
// errors, and the advisory lines are all populated on the result — the pipeline
// diagnostics and the input-kind errors do not short-circuit one another, so a
// caller sees the full set. The input-kind check reads the closure's Recipe and
// FeatureRecipe entities and the provider-plugin resolver through the same
// request-scoped view; the advisory lines read the closure-scoped overlap and
// narrowing accumulators (the narrowings are produced by the extends gate above,
// so they are populated before this point).
//
// # The verdict-plugin trust gate, and why validation carries one at all
//
// The pipeline this runs is the same pre-render pipeline a build runs, and two of
// its phases dispatch operator-attached guests: the label-transform role at
// topology construction and the ir-verdict role at static analysis. A validation
// that ran those guests without comparing their identity against the record of
// what was reviewed would execute unreviewed bytes on a surface an editor calls on
// every edit. So the configuration is held to the record, and the recorded
// digests are carried into the sockets' resolution, exactly as a build does — one
// composition serves both, so the two cannot come to disagree about which
// attachments are verdict attachments or about which digests are trusted.
//
// The gate runs after the extends and pillar gates and immediately before the
// pipeline. Those two gates dispatch no guest, so this placement refuses before
// any guest could run while leaving every diagnostic they produce, and the order
// they produce it in, exactly as it was. An absent record is a first-run state and
// stays silent — see pluginstrust.LoadOrStart, which owns that distinction — so a
// workspace that has never resolved a verdict attachment validates as it always
// did.
//
// # The bound: only a Release-scoped validation is held to the record
//
// This is the core a Release-scoped validation runs, and it is the only validation
// scope that dispatches an operator-attached guest. The whole-catalog and
// single-entity scopes drive a synthesized single-entity harness whose workspace
// configuration is workspaceconfig.Default(), which attaches nothing, so no
// verdict attachment runs there and there is nothing for a record to hold. The
// plugin scope inspects one artifact and runs no pipeline. The bound is therefore
// that those scopes have nothing at stake rather than that they merely cannot
// locate a record.
//
// Cancellation (the caller's shutdown drain) surfaces as a context error in
// RunErr; no findings are computed on a cancelled run — resolution-completeness,
// input-kind capability, and the advisory lines all stay empty.
func Validate(ctx context.Context, in ValidateInputs) *ValidateResult {
	r := &ValidateResult{}

	libraries := releaseLibraries(in.View, in.Closure, in.ClosureLibrariesResolved)
	if extErrs := in.View.ResolveClosureExtends(ctx, in.Closure, libraries.Schemas); len(extErrs) > 0 {
		r.ClosureExtendsErrors = extErrs
		return r
	}
	if err := in.View.ValidateClosureScopedPillars(ctx, in.Closure, libraries.Schemas); err != nil {
		r.PillarError = err
		return r
	}

	inputs := pipeline.Inputs{
		Release:           in.Release,
		Manifest:          in.Manifest,
		AggregateRawBytes: in.AggBytes,
		AggregatePath:     in.AggregatePath,
		// The path as this core holds it. The pipeline renders it against the
		// workspace root set below, and that is the one place the rendering happens.
		ReleasePath: in.ReleasePath,
		// A pointer into this call's own inputs value, so the composition below
		// re-points it at the copy it rewrites and the caller's configuration is
		// never written through.
		WorkspaceConfig: &in.WorkspaceConfig,
		// The root the cache addresses are composed against. It travels with the
		// configuration because the two are one address: the configuration names the
		// cache directory and this names what that directory is relative to, so a
		// resolution handed one without the other reads a cache somewhere else.
		WorkspaceRoot: in.WorkspaceRoot,
		Lockfile:      in.Lockfile,
		// A Release-scoped validation runs the same resolution a build of the same
		// documents runs, so it makes the same claim: the recorded identities
		// govern what its plugins resolve to. Validating against a different pin
		// model from the build would report on a composition the build never
		// produces.
		PluginPins:      pluginpath.RecordedPins(lockfile.PluginPins(in.Lockfile, in.ReleasePath)),
		CompilerVersion: in.CompilerVersion,
		Display:         in.Display,
		// The view resolved every closure entity in the two gates above, so its
		// advisories over those entities are complete when the run asks for them.
		CatalogAdvisories: in.View.SchemaAdvisories,
	}
	// The governance attachment list the pipeline dispatches and the digests
	// recorded as trusted for its verdict attachments arrive through the one
	// composition every surface that runs those attachments calls. Composing them
	// here rather than assigning the digests directly is what keeps this surface
	// from becoming a second home for that assignment — an omitted one is an empty
	// map, which is a legal value meaning nothing was recorded.
	//
	// Which of the composition's two halves fires depends on whether the caller
	// supplied the record: with one, the transform attachments are reconstructed
	// from it and arrive carrying their pinned inputs; without one there is nothing
	// to reconstruct from and the live attachment list stands as authored. Both are
	// the composition's own documented behaviour rather than a special case here.
	if err := ApplyGovernanceComposition(&inputs, in.View, in.ReleasePath); err != nil {
		r.VerdictTrustError = err
		return r
	}

	tier4, runErr := pipeline.RunTier4Pipeline(
		ctx,
		inputs,
		in.View.AsCatalogLookupWithSchemas(ctx, libraries.Schemas),
		in.View.RecipeLookupWithManifest(in.Manifest),
		in.View.SubAggregateLoader(),
	)
	r.Tier4 = tier4
	r.RunErr = runErr

	if !isCancellation(runErr) && tier4 != nil && tier4.IRGraph != nil {
		r.ResolutionFindings = in.View.ResolutionFindings(in.Manifest, tier4.IRGraph)
	}

	if !isCancellation(runErr) {
		recipes, featureRecipes := closureRecipes(in.View, in.Closure)
		r.InputKindErrors = validateops.InputKindCapabilityErrors(
			in.View.AsCatalogLookup().ProviderPlugin, recipes, featureRecipes)
		r.LintWarnings = in.View.ClosureLintWarnings(ctx, in.Closure)
		r.NarrowingWarnings = in.View.ClosureNarrowingWarnings()
	}
	return r
}

// closureRecipes resolves the Recipe and FeatureRecipe entities a closure reaches
// from the closure's recorded kind-hints, for the closure-scoped input-kind
// detector. Entities the closure classified as neither (an empty or other
// KindHint) are not recipe-shaped and are skipped; an entry that fails to resolve
// through the view is likewise skipped (the render-time gate remains the
// authoritative backstop).
func closureRecipes(view *catalog.LazyLookup, cl closure.Closure) ([]types.Recipe, []types.FeatureRecipe) {
	var recipes []types.Recipe
	var featureRecipes []types.FeatureRecipe
	for _, e := range cl.Entities {
		switch e.KindHint {
		case "recipe":
			if r, ok := view.RecipeByURI(e.URI); ok {
				recipes = append(recipes, r)
			}
		case "feature_recipe":
			if fr, ok := view.FeatureRecipeByURI(e.URI); ok {
				featureRecipes = append(featureRecipes, fr)
			}
		}
	}
	return recipes, featureRecipes
}

// isCancellation reports whether err is a shutdown-drain cancellation, for
// which validation yields no findings.
func isCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
