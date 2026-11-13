// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package rendercore

import (
	"fmt"

	abi "github.com/aggrega-ai/aggrega-abi/go/pkg/abi"
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
// drift into two spellings of the same advisory.
//
// Three kinds of location reach it, and each prints its own line:
//
//   - A location naming an instance — the one the compiler writes when it
//     locates an advisory at the component and provider it was rendering —
//     prints `node "<instance>"`, followed by `(provider "<kind>")` where a kind
//     is set.
//   - A location a plugin set itself, naming no instance, prints its uri in that
//     same slot: `node "<uri>"`, with the provider beside it the same way. The
//     uri there is usually a document, and the line still says node; that wording
//     is kept byte for byte, because a reader or a tool matching such an advisory
//     by its line would see it move for no change in what the plugin reported.
//   - No location, or one naming neither, prints the message alone.
//
// The instance is printed as `node "<id>"` rather than in the "(instance <id>)"
// shape cerrors.SourceLocation.String composes for a position, and that
// difference is deliberate for the same reason: it is the advisory line every
// surface displays. This is the one place an advisory's location is put into
// text, so the two shapes never meet in one line.
func RenderDiagnosticText(d renderctx.Diagnostic) string {
	if d.Location == nil {
		return d.Message
	}
	subject := d.Location.Instance
	if subject == "" {
		subject = d.Location.URI
	}
	if subject == "" {
		return d.Message
	}
	if d.Location.Kind == "" {
		return fmt.Sprintf("node %q: %s", subject, d.Message)
	}
	return fmt.Sprintf("node %q (provider %q): %s", subject, d.Location.Kind, d.Message)
}
