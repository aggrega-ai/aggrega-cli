// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

import (
	"fmt"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
)

// UnsupportedFieldCode is the stable, machine-matchable code carried by every
// diagnostic a render target emits when it cannot honor an authored Workload
// field. Both the full-omission and partial-support diagnostics share this
// one code so a single display filter can suppress the whole class. It is the
// key the build and lint suppress flag matches on; it is independent of the
// human-readable message text.
const UnsupportedFieldCode = "unsupported-field"

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
