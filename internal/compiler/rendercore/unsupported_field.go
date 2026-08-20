// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

import (
	"fmt"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
)

// UnsupportedFieldCode is the stable, machine-matchable code carried by every
// diagnostic reporting an authored input a render target cannot honor — a
// Workload field the target cannot express, and a platform annotation the
// resolved target does not act on for the node's input kind. Every constructor
// in this class shares this one code so a single display filter can suppress the
// whole class: an author silencing "things I authored that this target ignores"
// silences them together rather than discovering one switch per kind of authored
// input. It is the key the build and lint suppress flag matches on; it is
// independent of the human-readable message text.
const UnsupportedFieldCode = "unsupported-field"

// UnhonorableAnnotationDiagnostic builds the warning raised when a node carries
// an authored platform annotation the resolved target's own support claim does
// not cover for that node's input kind, so the annotation reaches no consumer and
// changes nothing. target is the target's user-facing name; key is the annotation
// key exactly as authored; inputKind is the node's input kind in the same words
// every other diagnostic about an input kind spells it. The caller attaches the
// location naming the node and the target.
//
// It shares UnsupportedFieldCode with its siblings because it reports the same
// fact about a different authored input: something the author declared, which
// this target cannot act on, dropped out-of-band with the rendered bytes
// untouched. Warn-severity for the same reason the class is: a boundary
// annotation legitimately cascades across components rendering through
// heterogeneous targets, so on a mixed boundary some nodes cannot act on it and
// refusing the build would reject a correct deployment.
//
// One authored annotation can raise several of these, one per node that cannot
// act on it. That is not duplication — each names a different node, and the fact
// being reported is per node.
func UnhonorableAnnotationDiagnostic(target, key, inputKind string) renderctx.Diagnostic {
	return renderctx.Diagnostic{
		Severity:  abi.SeverityWarn,
		Code:      UnsupportedFieldCode,
		SourceRef: key,
		Message: fmt.Sprintf(
			"target %q does not support annotation %s on %s input and ignored it",
			target, key, inputKind),
	}
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
// can express an authored Workload field only in a reduced form rather than
// dropping it outright. target is the target's user-facing name; field is the
// authored schema field name; reducedTo describes, in user-facing terms, what
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
