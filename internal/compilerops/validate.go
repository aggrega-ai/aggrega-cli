// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	"context"
	"errors"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// ValidateInputs carries the explicit state Validate needs. View is the
// request-scoped catalog lookup every resolution reads through, so the whole
// validation sees one consistent pin snapshot. Closure is the pre-computed
// catalog closure the extends and pillar gates scope to. Release, Manifest,
// AggBytes, and AggregatePath are the pipeline's Steps 1-4 inputs;
// AggregatePath is a source-location label (no I/O). WorkspaceConfig and
// CompilerVersion feed the pipeline's resolution and provenance.
type ValidateInputs struct {
	View            *catalog.LazyLookup
	Closure         closure.Closure
	Release         *types.Release
	Manifest        *types.Manifest
	AggBytes        []byte
	AggregatePath   string
	WorkspaceConfig workspaceconfig.Config
	CompilerVersion string
}

// ValidateResult carries the semantic-validation outcome in the order the gates
// run. A caller inspects the fields in sequence: a non-empty ClosureExtendsErrors
// means the closure-scoped extends gate stopped validation there; otherwise a
// non-nil PillarError means the closure-scoped schema-pillar gate stopped it;
// otherwise Tier4 and RunErr carry the Steps 1-4 pipeline outcome and
// ResolutionFindings carries the resolution-completeness findings over the built
// IR graph. Each field's producer is single-sourced here so every front end maps
// the same outcome into its own diagnostics.
type ValidateResult struct {
	ClosureExtendsErrors []error
	PillarError          error
	Tier4                *pipeline.Tier4Result
	RunErr               error
	ResolutionFindings   []error
}

// Validate runs the semantic-validation core against explicit inputs: the
// closure-scoped extends gate, the closure-scoped schema-pillar gate, the
// Steps 1-4 pipeline (resolve, topology, IR, analyse), and the
// resolution-completeness findings over the built IR graph. It stops at the
// first gate that fails and returns a discriminated result the caller maps into
// positioned diagnostics; it produces no diagnostics itself.
//
// The extends and pillar gates read through the request-scoped view, so an
// in-closure entity that a drifted cache slot skipped is reported rather than
// self-healed via a registry re-fetch. Only the entities the Release reaches
// are validated; a broken out-of-closure entity is never validated here.
//
// Cancellation (the caller's shutdown drain) surfaces as a context error in
// RunErr; resolution-completeness findings are not computed on a cancelled run.
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

	tier4, runErr := pipeline.RunTier4Pipeline(
		ctx,
		pipeline.Inputs{
			Release:           in.Release,
			Manifest:          in.Manifest,
			AggregateRawBytes: in.AggBytes,
			AggregatePath:     in.AggregatePath,
			WorkspaceConfig:   in.WorkspaceConfig,
			CompilerVersion:   in.CompilerVersion,
		},
		in.View.AsCatalogLookup(),
		in.View.SubAggregateLoader(),
	)
	r.Tier4 = tier4
	r.RunErr = runErr

	if !isCancellation(runErr) && tier4 != nil && tier4.IRGraph != nil {
		r.ResolutionFindings = in.View.ResolutionFindings(in.Manifest, tier4.IRGraph)
	}
	return r
}

// isCancellation reports whether err is a shutdown-drain cancellation, for
// which validation yields no findings.
func isCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
