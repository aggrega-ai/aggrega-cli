// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package rendercore

import (
	"errors"
	"fmt"
	"strings"

	abi "github.com/aggrega-ai/aggrega-abi/go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// ArtifactProvenanceSkippedCode is the stable, machine-matchable code carried by
// the diagnostic a provider guest emits when it could not stamp build provenance
// into a rendered artifact and emitted the artifact's bytes unchanged instead.
// Every guest that splices provenance reports the class under this one code, so a
// consumer matches the class rather than the message text.
const ArtifactProvenanceSkippedCode = "artifact-provenance-skipped"

// ArtifactProvenanceSkippedDiagnostic wraps the advisory warning
// [SpliceArtifactProvenance] returns into the diagnostic a guest raises for it.
// It sits beside the function that produces the warning text so the class's code
// and severity have one home rather than one copy per guest: a guest passes the
// non-empty warning straight through and never names the code itself.
func ArtifactProvenanceSkippedDiagnostic(warning string) renderctx.Diagnostic {
	return renderctx.Diagnostic{
		Severity: abi.SeverityWarn,
		Code:     ArtifactProvenanceSkippedCode,
		Message:  warning,
	}
}

// SpliceArtifactProvenance stamps the supplied build-provenance label and
// annotation sets into a rendered Kubernetes-YAML artifact's metadata and returns
// the placed bytes. It is the exported placer a Kubernetes-YAML-emitting provider
// guest calls once per artifact entry: the guest supplies the entry's already-
// template-evaluated bytes and the host-computed provenance sets, and receives the
// stamped body, an optional warning to raise as a diagnostic, and a hard error.
//
// Placement path (identical for every YAML-splicing guest): a byte-faithful
// per-document surgical splice first, falling back to a multi-document round-trip
// for documents the splice cannot handle. defaultDelims selects the round-trip's
// parse-failure policy: true (the guest's target syntax was Aggrega's own default
// "{{"/"}}", already resolved) treats a parse failure as a CATALOG_ERROR; false
// (an alternate-delimiter guest whose output may still carry the target engine's
// "{{ ... }}", e.g. helm-chart) treats a non-round-trippable file as a skip —
// emitting the original bytes unchanged and returning warning naming the file, so
// provenance for such files rides in the chart's own helpers. indent is the YAML
// indentation width the round-trip re-marshal uses. Empty content is returned
// unchanged with no warning.
//
// A non-empty warning is advisory (the guest raises it as a diagnostic); a
// non-nil err is a hard render failure the guest surfaces as such.
//
// sensitiveContent is the entry's own marking: the template the content was
// evaluated from reads a value an author declared sensitive. The parser quotes
// the content it could not read, so with the marking set a parse failure reports
// which artifact did not parse and withholds the parser's text — see
// [ArtifactNotYAML]. Nothing else this placer reports quotes the content.
func SpliceArtifactProvenance(name string, rendered []byte, labels, annotations map[string]string, defaultDelims bool, indent int, sensitiveContent bool) (body []byte, warning string, err error) {
	r := string(rendered)
	if strings.TrimSpace(r) == "" {
		return rendered, "", nil
	}

	// Primary: byte-faithful per-document surgical splice. Every byte the splice
	// does not insert stays identical, so no target-engine token outside the
	// inserted label lines can be re-shaped. ok=false routes to the round-trip.
	if out, ok := spliceProvenanceMultiDoc(r, labels, annotations); ok {
		return []byte(out), "", nil
	}

	// Fallback: multi-document-aware round-trip for the documents the splice
	// cannot handle.
	b, outcome, rtErr := injectProvenanceRoundTrip(name, r, labels, annotations, defaultDelims, indent)
	switch outcome {
	case artifactInjected:
		if rtErr != nil {
			return nil, "", rtErr // CATALOG_ERROR from the metadata-shape guard
		}
		return b, "", nil
	case artifactParseFailDefault:
		return nil, "", ArtifactNotYAML("", name, rtErr, sensitiveContent)
	default: // artifactSkip
		return rendered, ProvenanceSkipWarning(name), nil
	}
}

// artifactParseWithheld is the cause a parse failure carries in place of the
// parser's own text when the artifact's content was produced by a template that
// reads a value an author declared sensitive.
const artifactParseWithheld = "the parser's text is withheld because the template that produced this artifact reads a value declared sensitive"

// ArtifactNotYAML is the refusal an artifact placer raises when an entry's
// evaluated content does not parse as YAML, under the frame every placer shares:
// the artifact's name, prefixed by the provider's own word where it has one.
//
// A YAML parser quotes what it could not read — a scalar, an anchor, a tag — so
// its message can carry any part of the content. Where sensitiveContent is set the
// content came from a template reading a declared value, the placer cannot re-run
// that template, and so nothing of the parser's message is kept: the refusal names
// the artifact and says the parser's text is withheld. Where it is not set the
// parser's message is the cause, exactly as it was raised.
func ArtifactNotYAML(provider, name string, parseErr error, sensitiveContent bool) error {
	frame := fmt.Sprintf("artifact %q template output is not valid YAML", name)
	if provider != "" {
		frame = provider + ": " + frame
	}
	cause := parseErr
	if sensitiveContent {
		cause = errors.New(artifactParseWithheld)
	}
	return cerrors.Wrap(cause, cerrors.CatalogError, cerrors.SourceLocation{File: name}, frame)
}

// ProvenanceSkipWarning is the user-facing message emitted when a
// helm-chart `$import:`-loaded artifact does not survive Aggrega's
// pre-render YAML round-trip, so provenance labels cannot be merged
// before the file is handed to Helm. The guidance points first at
// quoting scalar Helm substitutions — a quoted `{{ }}` scalar round-
// trips, so the compiler can then inject provenance — and reserves the
// chart's `_helpers.tpl` for files using structural Helm template
// actions, which never parse as YAML pre-render.
func ProvenanceSkipWarning(filename string) string {
	return fmt.Sprintf(
		"artifact %q ($import-loaded): skipped provenance label injection — the file does not survive Aggrega's pre-render YAML round-trip. Quote scalar Helm substitutions (e.g. namespace: \"{{ .Release.Namespace }}\") so the compiler can inject provenance; for structural Helm actions ({{- if }}, {{- range }}, {{- include }}), add provenance via the chart's _helpers.tpl instead.",
		filename,
	)
}
