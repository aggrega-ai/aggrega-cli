// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parse"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// renderWorkloadForDisplay evaluates a Recipe's workload template a second time,
// against a context whose declared-sensitive data has been replaced by the
// placeholder, producing the document a display SHOWS in place of the one the
// build emitted.
//
// It exists because a rendered workload is the one projection no key-addressed
// substitution can reach: its fields carry no link back to the declarations the
// template read to produce them. What IS key-addressed is the context the template
// read, so substituting there and evaluating again reaches the same data by the
// only route that stays blind to what a value means.
//
// Substituting rather than removing is what makes the two documents agree in
// shape: the template sees a string where a string was, so it takes the same
// branches, every field the emitted document carries is present here, and only the
// substituted datum's own position differs. Removing the keys would change which
// branches the template takes and the documents would diverge structurally, which
// is a worse display than one that is honest about a narrow divergence.
//
// It runs NO validator, and it never fails a build. A placeholder can legitimately
// violate the workload's own contract — a plain string where a constrained name or
// an integer is required — and a display projection that halted a build would turn
// a debugging aid into a build dependency. An evaluation or parse failure reports
// false and the caller shows the withholding statement instead.
//
// The substituted context comes from the one applier that builds one, so the
// document a reader sees and the pillars printed beside it are substituted by the
// same code and agree by construction rather than by care.
func renderWorkloadForDisplay(
	recipeName, tmplText string,
	ctx *types.TemplateContext,
	keys redact.Keys,
) (*types.Workload, bool) {
	// The template name matches the real evaluation's, so a diagnostic raised
	// from either names the same authored template. Nothing here surfaces a
	// diagnostic today; the name is what a future reader would grep for.
	output, err := EvalTemplate(
		fmt.Sprintf("workload[%s]", recipeName),
		tmplText,
		redact.TemplateContext(ctx, keys),
	)
	if err != nil {
		return nil, false
	}
	var shown types.Workload
	if err := parse.TemplateOutput([]byte(output), &shown); err != nil {
		return nil, false
	}
	return &shown, true
}
