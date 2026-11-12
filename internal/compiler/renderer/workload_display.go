// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parse"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
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
// only route that does not interpret what a value means.
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
// The substitution is the one substitution of a template's data, the same one a
// failing template's second run goes through, applied at the coordinates the set
// derives for each datum it names. The pillars printed beside the document reach
// the same positions through the display's own appliers, which keep the author
// order the template's data does not; the two are held to one answer by a parity
// test over one fixture.
//
// The shown document is folded exactly as the emitted one is: every entry naming
// a connection is rewritten onto the slot name the compiler composes for it,
// through the same fold and the same slots. Without it the shown document would
// carry each such entry in its authored spelling while the emitted one carries the
// folded spelling — a structural difference at a position no substitution touched.
// A fold refusal reports false, as any other failure does.
func renderWorkloadForDisplay(
	recipeName, tmplText string,
	ctx *types.TemplateContext,
	keys redact.Keys,
	slots validator.WorkloadSlots,
) (*types.Workload, bool) {
	// The template name matches the real evaluation's, so a diagnostic raised
	// from either names the same authored template. Nothing here surfaces a
	// diagnostic today; the name is what a future reader would grep for.
	output, err := evalSubstitutedTemplate(
		fmt.Sprintf("workload[%s]", recipeName),
		tmplText,
		ctx,
		keys,
	)
	if err != nil {
		return nil, false
	}
	var shown types.Workload
	if err := parse.TemplateOutput([]byte(output), &shown); err != nil {
		return nil, false
	}
	if _, err := validator.FoldWorkloadConnections(&shown, slots, "", recipeName, ""); err != nil {
		return nil, false
	}
	return &shown, true
}
