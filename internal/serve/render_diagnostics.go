// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// keyPathSpan resolves a path into an authored document to the editor position
// that spans the offending key, falling back to the head of the file.
//
// It is the ONE place the fall-back lives. Every class that positions a finding
// from a document path — the halting resolver violations, the static-analysis
// violations, and the advisories — reaches its position through here, so a class
// cannot acquire a different fall-back from the others by being written
// separately. The fall-back is the part worth single-sourcing rather than the
// walk: the walk is already one function, while "what position does a finding get
// when the path resolves nowhere" was stated independently at each caller and is
// exactly the kind of agreement that holds until one copy is edited.
//
// The head of the file is returned in three distinct situations, and the caller
// cannot tell them apart because it does not need to: no document was available,
// the finding carried no path, or the path walked somewhere this document does not
// contain — which is what happens when the finding concerns a different file.
func keyPathSpan(doc *yaml.Node, idx yamlpos.LineIndex, keyPath string) (line, col, endLine, endCol int) {
	line, col = 1, 1
	if doc == nil || keyPath == "" {
		return line, col, 0, 0
	}
	if span, ok := yamlpos.FieldRange(doc, idx, keyPath); ok {
		return span.Line, span.Column, span.EndLine, span.EndColumn
	}
	return line, col, 0, 0
}

// advisoryToDiagnostic maps one advisory off the compiler's advisory channel onto
// the editor wire, recovering a source position where the advisory carries a path
// into the document being positioned against.
//
// This is the ONE mapping from that channel to this wire. Both surfaces that
// consume the channel — the validation path and the graph path — route through it,
// so an advisory cannot arrive at the editor positioned one way through one
// request and another way through the other.
//
// # Position recovery, and the placeholder
//
// An advisory about an authored document carries the path to the offending key
// rather than a resolved line, because the position is recoverable only by a
// surface holding the parsed file — which is this one. The path is walked against
// the document and the entry spans the offending token, exactly as a halting
// violation's does, so an advisory lands on its authoring key rather than at the
// head of the file.
//
// An advisory whose producer had no such path is positioned at the head of the
// file, and that is a property of the producer rather than a shortcut here. The
// case that remains is the render advisory: it names a component and a delivery
// key, which are graph coordinates, and mapping those back to a position in the
// authoring document is a separate capability the daemon does not have. A
// governance verdict is positioned only where the plugin that raised it set a
// location, since nothing else knows what its rule was about.
//
// The head of the file is also the fallback when a path walks to somewhere the
// document does not contain, which is what happens when the advisory concerns a
// different file from the one being positioned against.
//
// # The text
//
// The message is rendered through the render package's own formatter, which is
// the one formatter every surface displaying this channel reads, so an advisory
// reads identically in the editor and in a terminal build. An advisory carrying a
// graph coordinate is attributed by that formatter; one carrying a document path
// renders its message alone, because a surface drawing the entry ON the offending
// key already shows the file and highlights the token, and spelling the path
// again inside the sentence would say it twice in the one place a reader cannot
// scroll away from.
//
// # Severity and code get opposite treatment, and the contrast is the point
//
// The severity is translated rather than copied: an advisory arrives on the plugin
// wire, whose severities are halt/warn/info, and leaves on the editor wire, whose
// severities are error/warning/info. The two vocabularies serve different
// boundaries and are deliberately separate, so this is the one place a value
// crosses between them. A code, by contrast, is an opaque machine-matchable string
// that means the same thing on both sides, and translating one would mint a second
// spelling for a single identity — so the entry publishes whatever the advisory
// arrived carrying, verbatim, and carries nothing where the advisory carried
// nothing, the empty string and the omitted key being the same statement here.
//
// The code is published as a CLASS code, which is what fixes the entry's other two
// identifier keys at absent. What an advisory names is a class of advisory rather
// than a registered halting condition, so there is no short code for a reader to
// quote and nothing on the halting-conditions page for a pointer to resolve to —
// see ConditionCodes, where that division is stated for every layer.
//
// # The structured path is BOTH resolved and published
//
// The path is walked to a position, and it is also carried on the entry. The two
// are not one fact twice: the position says where the entry is drawn, and the path
// says what the producer named, which is what a client needs to re-anchor an entry
// after an edit or to group entries by the element they concern. Publishing only
// the position would discard a coordinate the producer computed; publishing only
// the path would leave every advisory at the head of the file.
//
// The remedy and the version-impact verdict are carried through for the same
// reason the code is: they mean the same thing on both sides of this boundary, and
// the message formatter folds neither of them into its text, so an entry that
// dropped them would report less than the advisory said.
func advisoryToDiagnostic(d renderer.Diagnostic, doc *yaml.Node, idx yamlpos.LineIndex) Diagnostic {
	var keyPath string
	if d.Location != nil {
		keyPath = d.Location.KeyPath
	}
	line, col, endLine, endCol := keyPathSpan(doc, idx, keyPath)
	return Diagnostic{
		Line:           line,
		Column:         col,
		EndLine:        endLine,
		EndColumn:      endCol,
		Severity:       editorSeverity(d.Severity),
		Message:        renderer.RenderDiagnosticText(d),
		ConditionCodes: classCode(d.Code),
		Classification: d.Classification,
		Suggest:        d.Suggest,
		KeyPath:        keyPath,
	}
}

// advisoryPositionSource parses content into the document and line index the
// advisories are positioned against, in this client's position encoding.
//
// It exists because the surfaces that run a build hold the authored bytes but no
// parsed document — the parse belongs to the request path, and a build request
// carries a Release reference rather than a buffer. Parsing here is what lets an
// advisory raised by a BUILD land on its authoring key, the way one raised by a
// validation already does.
//
// Unparseable content yields a nil document rather than an error: the advisories
// are still published, positioned at the head of the file. A document that will
// not parse is a condition the envelope layer already reports on its own, and
// refusing to publish the advisories because of it would withhold findings for a
// reason unrelated to them.
func (s *Server) advisoryPositionSource(content []byte) (*yaml.Node, yamlpos.LineIndex) {
	if len(content) == 0 {
		return nil, yamlpos.LineIndex{}
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return nil, yamlpos.LineIndex{}
	}
	return &doc, yamlpos.BuildLineIndexWithEncoding(content, s.positionEncoding())
}

// publishRenderDiagnostics replaces the build diagnostic layer with the
// advisories this run raised, attributed to file, and pushes every file the
// replacement touched so a cleared advisory disappears from the editor as
// reliably as a new one appears.
//
// content is the authored bytes of file, parsed here so an advisory that named
// an authoring key lands on that key. It is taken as bytes rather than as a
// parsed document because the surfaces that run a build hold the bytes and no
// document — the parse belongs to the request path, and a build request carries a
// Release reference rather than a buffer. A caller with no bytes to offer passes
// nil, and every advisory then lands at the head of the file, which is a
// degradation of position only and never a dropped entry.
//
// extra carries advisories about the run that the run's own phases did not
// produce — a reference declined for want of a pin, which the host declined and
// the guest never saw. They are passed in and published here rather than through
// a call of their own because this function REPLACES the whole layer: a second
// publication would erase this one's entries, so every advisory belonging to the
// layer arrives in one call or not at all.
func (s *Server) publishRenderDiagnostics(ctx context.Context, file string, diags []renderer.Diagnostic, extra []Diagnostic, content []byte) {
	byFile := map[string][]Diagnostic{}
	if file != "" && len(diags)+len(extra) > 0 {
		doc, idx := s.advisoryPositionSource(content)
		mapped := make([]Diagnostic, 0, len(diags)+len(extra))
		for _, d := range diags {
			mapped = append(mapped, advisoryToDiagnostic(d, doc, idx))
		}
		mapped = append(mapped, extra...)
		byFile[file] = mapped
	}
	for _, f := range s.setRenderDiagnostics(byFile) {
		_ = s.publishDiagnostics(ctx, f)
	}
}

// editorSeverity translates a plugin-wire diagnostic severity into the editor
// wire's own severity. halt is the plugin wire's build-stopping severity and
// error is the editor wire's, warn maps to warning, and info is spelled the same
// on both. A value the plugin wire does not declare maps to the editor's error
// tier, so an unmodelled severity surfaces visibly instead of being downgraded.
func editorSeverity(pluginSeverity string) string {
	switch pluginSeverity {
	case abi.SeverityHalt:
		return editorSeverityError
	case abi.SeverityWarn:
		return editorSeverityWarning
	case abi.SeverityInfo:
		return editorSeverityInfo
	default:
		return editorSeverityError
	}
}
