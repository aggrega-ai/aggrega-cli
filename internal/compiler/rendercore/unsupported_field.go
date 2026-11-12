// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package rendercore

import (
	"fmt"
	"strings"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
)

// UnsupportedFieldCode is the stable, machine-matchable code carried by every
// diagnostic reporting an authored input a render target cannot honor — an
// authored field the target cannot express, or can express only in a reduced
// form, and a platform annotation the resolved target does not act on for any
// of the input kinds the node declares. Every constructor
// in this class shares this one code so a single display filter can suppress the
// whole class: an author silencing "things I authored that this target ignores"
// silences them together rather than discovering one switch per kind of authored
// input. It is the key the build and lint suppress flag matches on; it is
// independent of the human-readable message text.
const UnsupportedFieldCode = "unsupported-field"

// UnhonorableAnnotationDiagnostic builds the warning raised when an authored
// platform annotation reaches a render whose resolved target's own support claim
// covers it for NONE of the input kinds that render declares, so the annotation
// reaches no consumer and changes nothing. target is the target's user-facing
// name; key is the annotation key exactly as authored; inputKinds are every input
// kind that render declares, in the same words every other diagnostic about an
// input kind spells them. The caller attaches the location naming the node and
// the target.
//
// The kinds are plural because a Recipe may declare more than one and their
// outputs are added together, so the target may act on an annotation through one
// declared kind while having no slot for it on another. Naming one kind in such a
// render would assert something false about a render that emits two — and this
// warning says the value was ignored, which invites deleting an annotation that
// is in fact changing the rendered output. So the raiser asks about every declared
// kind and reaches this constructor only when the answer is no for all of them,
// and the sentence names all of them.
//
// A render declaring a single input kind reads as a single kind: the kinds
// are joined into one phrase, and a phrase built from one kind is that kind's own
// word.
//
// It shares UnsupportedFieldCode with its siblings because it reports the same
// fact about a different authored input: something the author declared, which
// this target cannot act on, dropped out-of-band with the rendered bytes
// untouched. Warn-severity for the same reason the class is: a boundary
// annotation legitimately cascades across components rendering through
// heterogeneous targets, so on a mixed boundary some nodes cannot act on it and
// refusing the build would reject a correct Release.
//
// One authored annotation can raise several of these, one per render that cannot
// act on it. That is not duplication — a node's own Recipe and each feature
// attached to it render separately, through independently declared targets and
// input kinds, so two of these may name the same node while reporting different
// emitted output. The fact being reported is per render.
//
// At least one input kind is expected. A render declaring none emits nothing, so
// there is no annotation support to compare and the raiser emits nothing for it.
func UnhonorableAnnotationDiagnostic(target, key string, inputKinds ...string) renderctx.Diagnostic {
	return renderctx.Diagnostic{
		Severity:  abi.SeverityWarn,
		Code:      UnsupportedFieldCode,
		SourceRef: key,
		Message: fmt.Sprintf(
			"target %q does not support annotation %s on %s input and ignored it",
			target, key, joinInputKinds(inputKinds)),
	}
}

// joinInputKinds renders input-kind words as the noun phrase a diagnostic reads
// them in. The last two are joined with "or" rather than a comma because the
// sentence around it is negative — the target supports the key on none of them —
// and "or" is what carries that reading in English. One kind yields that kind's
// own word unchanged, which is what keeps a single-kind node's sentence identical
// to the one this class has always printed.
func joinInputKinds(kinds []string) string {
	switch len(kinds) {
	case 0:
		return ""
	case 1:
		return kinds[0]
	}
	return strings.Join(kinds[:len(kinds)-1], ", ") + " or " + kinds[len(kinds)-1]
}

// UnsupportedFieldDiagnostic builds the warning a render target emits when it
// cannot express an authored Workload field at all and drops it from the output.
// target is the target's user-facing name (for example "compose"); field is the
// authored schema field name (for example "scheduling", "runtimeClassName", or
// an extended-resource key such as "nvidia.com/gpu"), never a Go field name. The
// diagnostic is warn-severity and out-of-band: it never affects the rendered
// bytes. The target constructs one per authored-and-dropped field.
func UnsupportedFieldDiagnostic(target, field string) renderctx.Diagnostic {
	return renderctx.Diagnostic{
		Severity:  abi.SeverityWarn,
		Code:      UnsupportedFieldCode,
		SourceRef: field,
		Message:   fmt.Sprintf("target %q does not support %s and omitted it", target, field),
	}
}

// PartialSupportDiagnostic builds the warning a render target emits when it
// can express an authored field only in a reduced form rather than dropping it
// outright. target is the target's user-facing name; field is the authored field
// named where the author wrote it — a Workload field, or a field authored on the
// Release boundary that boundary assembly reduces; reducedTo describes what
// the field becomes in the target (for example "per-replica volume templating is
// not available and the volume degrades to a shared persistent volume"). It
// shares UnsupportedFieldCode with the full-omission diagnostic so the same
// suppress filter covers both, and is likewise warn-severity and out-of-band.
func PartialSupportDiagnostic(target, field, reducedTo string) renderctx.Diagnostic {
	return renderctx.Diagnostic{
		Severity:  abi.SeverityWarn,
		Code:      UnsupportedFieldCode,
		SourceRef: field,
		Message:   fmt.Sprintf("target %q does not fully support %s; %s", target, field, reducedTo),
	}
}
