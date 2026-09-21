// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

import (
	"fmt"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
)

// SensitiveByValueCode is the stable, machine-matchable code carried by the
// advisory raised when an attachment delivers a datum its schema declares
// sensitive into a delivery key as a literal value, rather than as a reference
// the target resolves at deploy time.
//
// It is deliberately a code of its own rather than a member of the
// unsupported-field class, and the suppress flag for that class does not cover
// it. The two advisories answer different questions — "this target cannot
// express the field you authored" against "the value you declared sensitive is
// travelling as a literal" — and an operator silences them for unrelated
// reasons, so a single switch over both would let a target-fidelity preference
// also silence a security-adjacent notice. A later reader tidying "two codes on
// one channel" into one should read this paragraph first: the separation is the
// point, not an oversight.
const SensitiveByValueCode = "sensitive-by-value"

// SensitiveByValueDiagnostic builds the advisory for one delivery key carrying
// a declared-sensitive datum as a literal. attachment is the attachment
// instance name the author wrote; mechanism is the delivery mechanism
// the key lands in (for example "env" or "file"); deliveryKey is the key itself.
// The caller attaches the location naming the component and the target.
//
// The diagnostic is warn-severity and out-of-band: the build succeeds and
// the rendered bytes are unchanged. Delivering a literal is frequently the only
// thing a target accepts, and a whole family of attachments exists to do
// exactly that, so this reports a fact about how the value travels rather than
// a fault to be corrected. Whether it should instead be fatal is an
// organisation's decision, expressed as a policy over the build rather than as
// a compiler default.
func SensitiveByValueDiagnostic(attachment, mechanism, deliveryKey string) renderctx.Diagnostic {
	return renderctx.Diagnostic{
		Severity:  abi.SeverityWarn,
		Code:      SensitiveByValueCode,
		SourceRef: deliveryKey,
		Message: fmt.Sprintf(
			"attachment %q delivers a value declared sensitive to %s delivery key %q as a literal; where the target supports it, fill the slot with an attachment that delivers a reference instead",
			attachment, mechanism, deliveryKey),
	}
}

// RenderDiagnosticText renders one structured render diagnostic as a single
// line of user-facing text, attributed to the component and target its location
// names. Every surface that displays this channel formats through here — the
// terminal build output and the editor's problem list both — so the two cannot
// drift into two spellings of the same advisory. A diagnostic carrying no
// location renders its message alone.
func RenderDiagnosticText(d renderctx.Diagnostic) string {
	if d.Location == nil || d.Location.URI == "" {
		return d.Message
	}
	if d.Location.Kind == "" {
		return fmt.Sprintf("node %q: %s", d.Location.URI, d.Message)
	}
	return fmt.Sprintf("node %q (provider %q): %s", d.Location.URI, d.Location.Kind, d.Message)
}
