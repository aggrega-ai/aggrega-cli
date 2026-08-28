// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	"context"
	"errors"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/validateops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
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
// Only the construction is shared. The *ClosureError is handed back exactly as
// ResolveClosure produced it, stage included, because a front door annotates a
// resolution failure in its own terms and those surfaces legitimately differ.
func ResolveValidationClosure(in ValidationClosureInputs) (*ResolvedClosure, *ClosureError) {
	return ResolveClosure(ClosureInputs{
		View:            in.View,
		ReleasePath:     in.ReleasePath,
		WorkspaceRoot:   in.WorkspaceRoot,
		WorkspaceConfig: in.WorkspaceConfig,
		Lockfile:        in.Lockfile,
		Augment:         false,
	})
}

// ValidateInputs carries the explicit state Validate needs. View is the
// request-scoped catalog lookup every resolution reads through, so the whole
// validation sees one consistent pin snapshot. Closure is the pre-computed
// catalog closure the extends and pillar gates scope to. Release, Manifest,
// AggBytes, and AggregatePath are the pipeline's Steps 1-4 inputs;
// AggregatePath is a source-location label (no I/O). WorkspaceConfig and
// CompilerVersion feed the pipeline's resolution and provenance. ReleasePath
// locates the record the verdict-plugin trust gate reads and carries its own
// contract below.
type ValidateInputs struct {
	View     *catalog.LazyLookup
	Closure  closure.Closure
	Release  *types.Release
	Manifest *types.Manifest
	// ReleasePath is the path of the Release being validated. It locates the record
	// of the plugin identities trusted for this Release's verdict attachments, which
	// sits beside that Release, and it is required: validation runs the same
	// pre-render phases a build runs, gate plugins included, so a validation that
	// could not locate the record would run those gates unheld. The refusal for an
	// unsupplied path is VerdictPluginTrust's, which is where the record's address is
	// derived, so there is one refusal rather than a second guard here that could
	// disagree with it.
	ReleasePath     string
	AggBytes        []byte
	AggregatePath   string
	WorkspaceConfig workspaceconfig.Config
	CompilerVersion string
}

// ValidateResult carries the semantic-validation outcome in the order the gates
// run. A caller inspects the fields in sequence: a non-empty ClosureExtendsErrors
// means the closure-scoped extends gate stopped validation there; otherwise a
// non-nil PillarError means the closure-scoped schema-pillar gate stopped it;
// otherwise a non-nil VerdictTrustError means the workspace configuration
// disagreed with the record of the plugin identities trusted for its verdict
// attachments and validation stopped before the pipeline ran; otherwise Tier4 and
// RunErr carry the Steps 1-4 pipeline outcome and ResolutionFindings carries the
// resolution-completeness findings over the built IR graph.
//
// InputKindErrors is an additional error class computed after the pipeline gates
// (only when the extends and pillar gates both passed): each entry names a
// closure Recipe or FeatureRecipe whose synthesized workload targets a provider
// whose serving plugin cannot synthesize — an early, non-authoritative detector
// whose entries contribute to a non-zero verdict alongside the pipeline
// diagnostics.
//
// LintWarnings and NarrowingWarnings are advisory, exit-code-neutral lines: the
// schema-pillar-overlap advisories for the closure's Components and Features, and
// the narrowing-on-extend advisories accumulated while composing the closure's
// extends chains. A caller surfaces them informationally; they never change the
// verdict.
//
// Each field's producer is single-sourced here so every front end maps the same
// outcome into its own diagnostics.
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
// analyse), the resolution-completeness findings over the built IR graph, the
// closure-scoped input-kind capability check, and the exit-code-neutral advisory
// lines. It stops at the first of those three gates that fails and returns a
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

	if extErrs := in.View.ResolveClosureExtends(in.Closure); len(extErrs) > 0 {
		r.ClosureExtendsErrors = extErrs
		return r
	}
	if err := in.View.ValidateClosureScopedPillars(in.Closure); err != nil {
		r.PillarError = err
		return r
	}

	inputs := pipeline.Inputs{
		Release:           in.Release,
		Manifest:          in.Manifest,
		AggregateRawBytes: in.AggBytes,
		AggregatePath:     in.AggregatePath,
		WorkspaceConfig:   in.WorkspaceConfig,
		CompilerVersion:   in.CompilerVersion,
	}
	// The governance attachment list the pipeline dispatches and the digests
	// recorded as trusted for its verdict attachments arrive through the one
	// composition every surface that runs those attachments calls. Composing them
	// here rather than assigning the digests directly is what keeps this surface
	// from becoming a second home for that assignment — an omitted one is an empty
	// map, which is a legal value meaning nothing was recorded.
	//
	// This surface supplies no lockfile, so the composition's reconstruction half
	// finds nothing to reconstruct from and the live attachment list stands as
	// authored. That is the composition's documented no-lockfile behaviour rather
	// than a special case here.
	if err := ApplyGovernanceComposition(&inputs, in.View, in.ReleasePath); err != nil {
		r.VerdictTrustError = err
		return r
	}

	tier4, runErr := pipeline.RunTier4Pipeline(
		ctx,
		inputs,
		in.View.AsCatalogLookup(),
		in.View.RecipeLookupWithManifest(in.Manifest).Recipe,
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
		r.LintWarnings = in.View.ClosureLintWarnings(in.Closure)
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
