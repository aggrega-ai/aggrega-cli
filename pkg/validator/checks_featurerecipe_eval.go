// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// featureRecipeEvalDeclaresNoRenderingSurface refuses a FeatureRecipe that binds
// the eval capability and declares a rendering surface.
//
// An eval FeatureRecipe evaluates values in memory and emits nothing, so it has
// nowhere to render a chart, a workload or an artifact to. A rendering field
// declared on one is a contract nothing will ever read, and the author almost
// always meant a different capability. The inject mapping is not among the fields
// this refuses and stays available: it reshapes what an attachment delivers
// rather than rendering anything, which is the work an eval FeatureRecipe exists
// to do.
//
// It reads the capability alongside the four shape facts rather than being
// invoked only where a caller has already decided the FeatureRecipe is an eval
// one. The gate is part of the rule: a declaration reading the four fields alone
// would state, as a rule, that no FeatureRecipe may declare a rendering surface,
// which is false of every other capability. Where the gate lives in the caller a
// reader of the declaration cannot see it at all.
//
// It names the fields in the order below, and that order is this rule's own
// rather than the one the sibling rule over a Recipe uses. The two differ because
// the reference documentation for each condition names the blocks differently,
// and a reader meeting the diagnostic and the page is meant to meet one list. So
// the order here is fixed and is not reordered to match either the sibling rule
// or the FeatureRecipe's own field declaration.
//
// Every fact it reads is complete once one authored document has been parsed and
// envelope-checked, which is the point its declared view names. Nothing here
// needs the entity resolved, the catalog reached, or the capability's provider
// bound — what a capability selects is a later question, and this asks only what
// the author wrote.
//
// It is invoked once per document rather than once per declared field. The
// violation is a property of the capability and the field set taken together
// rather than of any one field, and the remedy is a single decision about which
// capability the FeatureRecipe meant — so the fields are named together in one
// finding rather than reported one at a time.
var featureRecipeEvalDeclaresNoRenderingSurface = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentSubject,
]{
	Sentinel: ErrFeatureRecipeEvalProviderConstraint(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DocumentView,
		s constraint.DocumentSubject,
	) []constraint.Objection {
		if types.BareCapability(v.FeatureRecipeDeclaredCapability(s.SourceFile())) != evalCapability {
			return nil
		}

		var declared []string
		if v.FeatureRecipeSourceDeclared(s.SourceFile()) {
			declared = append(declared, "source")
		}
		if v.FeatureRecipeValuesMappingDeclared(s.SourceFile()) {
			declared = append(declared, "valuesMapping")
		}
		if v.FeatureRecipeWorkloadDeclared(s.SourceFile()) {
			declared = append(declared, "workload")
		}
		if v.FeatureRecipeArtifactsDeclared(s.SourceFile()) {
			declared = append(declared, "artifacts")
		}
		if len(declared) == 0 {
			return nil
		}

		return []constraint.Objection{
			s.RaiseWithAdvice(
				ErrFeatureRecipeEvalProviderConstraint(),
				evalForbiddenFieldAdvice(declared),
			),
		}
	},
}
