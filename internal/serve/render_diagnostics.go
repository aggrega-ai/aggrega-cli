// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"context"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// keyPathSpan resolves a path into an authored document to the editor position
// that spans the offending key, falling back to the key that names the document
// itself (documentAnchorSpan).
//
// Every class that positions a finding from a document path — the halting
// resolver violations, the static-analysis violations, the advisories, a refusal
// raised while rendering, and the single-entity check's finding — reaches its
// position through here, and the envelope findings reach the same fall-back
// through fieldPathSpan, so a class cannot acquire a different fall-back from the
// others by being written separately. The fall-back is the part worth
// single-sourcing rather than the walk: the walk is already one function, while
// "what position does a finding get when the path resolves nowhere" was stated
// independently at each caller and is exactly the kind of agreement that holds
// until one copy is edited.
//
// The fall-back is taken in three distinct situations, and the position alone
// does not tell them apart: no document was available, the finding carried no
// path, or the path walked somewhere this document does not contain — which is
// what happens when the finding concerns a different file. resolved is false in
// all three, for the caller whose own surface states the absence of a position
// rather than drawing one, and for the entry that records it as unplaced; it is
// true only where the path walked to a key.
func keyPathSpan(doc *yaml.Node, idx yamlpos.LineIndex, keyPath string) (line, col, endLine, endCol int, resolved bool) {
	if doc != nil && keyPath != "" {
		if span, ok := yamlpos.FieldRange(doc, idx, keyPath); ok {
			return span.Line, span.Column, span.EndLine, span.EndColumn, true
		}
	}
	line, col, endLine, endCol = documentAnchorSpan(doc, idx)
	return line, col, endLine, endCol, false
}

// documentAnchorSpan is where an entry is drawn when nothing it carries resolves
// to a key in its document: on the document's kind: key, on its metadata.name
// where it declares no kind:, and at the head of the file only where it declares
// neither or could not be parsed.
//
// It is the ONE place that fall-back lives; keyPathSpan and fieldPathSpan both
// take it. The head of the file is the last resort rather than the first because
// a document commonly opens with a comment — a licence header, say — and an entry
// drawn there reads as a fault in that comment. The document's own identity is
// the nearest thing to the whole document a reader can see, and it is a line the
// author wrote.
//
// Each anchor must be present as written: a document whose metadata carries no
// name is not drawn on its metadata key, because that key is not what this
// fall-back names.
func documentAnchorSpan(doc *yaml.Node, idx yamlpos.LineIndex) (line, col, endLine, endCol int) {
	for _, keys := range documentAnchorKeys {
		if span, ok := yamlpos.KeyRange(doc, idx, keys...); ok {
			return span.Line, span.Column, span.EndLine, span.EndColumn
		}
	}
	return 1, 1, 0, 0
}

// documentAnchorKeys are the keys documentAnchorSpan draws on, in the order it
// tries them.
var documentAnchorKeys = [][]string{{"kind"}, {"metadata", "name"}}

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
// violation's does, so an advisory lands on its authoring key rather than on the
// key that names the whole document.
//
// An advisory whose producer had no such path is drawn on the key that names the
// document — its kind:, else its metadata.name, else the head of the file
// (documentAnchorSpan) — and that is a property of the producer rather than a
// shortcut here. The case that remains is the render advisory: it names a
// component and a delivery key, which are graph coordinates, and mapping those
// back to a position in the authoring document is a separate capability the
// daemon does not have. A governance verdict is positioned only where the plugin
// that raised it set a location, since nothing else knows what its rule was about.
//
// The same fall-back is taken when a path walks to somewhere the document does
// not contain, which is what happens when the advisory concerns a different file
// from the one being positioned against.
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
// the path would leave every advisory on the document's fall-back key.
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
	line, col, endLine, endCol, located := keyPathSpan(doc, idx, keyPath)
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
		unplaced:       !located,
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
// are still published, positioned at the head of the file, since a document that
// did not parse has no key to draw them on. A document that will not parse is a
// condition the envelope layer already reports on its own, and refusing to
// publish the advisories because of it would withhold findings for a reason
// unrelated to them.
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
// nil, and every advisory then lands at the head of the file, since there is no
// document to find a key in — a degradation of position only and never a dropped
// entry.
//
// extra carries advisories about the run that the run's own phases did not
// produce — a reference declined for want of a pin, which the host declined and
// the guest never saw. They are passed in and published here rather than through
// a call of their own because this function REPLACES the whole layer: a second
// publication would erase this one's entries, so every advisory belonging to the
// layer arrives in one call or not at all.
//
// refusal is the refusal the pipeline run stopped on, while rendering or ahead of
// it, or nil for a run that did not, and it travels in the same call for the same
// reason. Each finding
// it states is filed on the document that finding points at, which is often not
// file — a Workload refusal points into its Recipe, a refusal over the graph into
// the Release that wrote the edge — and after the advisories the run raised before
// it stopped, which is the order the run raised them in. A run that does not stop
// there publishes the layer without it, so the next run that renders clears an
// entry the last one left. Every file is keyed as the layer keys it, so an entry
// pointing into file itself joins file's entries rather than replacing them.
//
// The refusal's entries are handed to the layer as refusals as well as entries,
// because a document's own validation does not report again what one of them
// already reports — see snapshotDiagnostics.
func (s *Server) publishRenderDiagnostics(ctx context.Context, file string, diags []renderer.Diagnostic, extra []Diagnostic, content []byte, refusal *renderRefusalReport) {
	byFile := map[string][]Diagnostic{}
	if file != "" && len(diags)+len(extra) > 0 {
		doc, idx := s.advisoryPositionSource(content)
		mapped := make([]Diagnostic, 0, len(diags)+len(extra))
		for _, d := range diags {
			mapped = append(mapped, advisoryToDiagnostic(d, doc, idx))
		}
		mapped = append(mapped, extra...)
		byFile[s.canonicalFile(file)] = mapped
	}
	refusals := map[string][]Diagnostic{}
	for _, entry := range refusal.entriesOrNone() {
		if entry.file == "" {
			continue
		}
		key := s.canonicalFile(entry.file)
		byFile[key] = append(byFile[key], entry.diagnostic)
		refusals[key] = append(refusals[key], entry.diagnostic)
	}
	for _, f := range s.setRenderDiagnostics(byFile, refusals) {
		_ = s.publishDiagnostics(ctx, f)
	}
}

// renderRefusalReport is a refusal raised while rendering as the daemon reports
// it: one entry for each finding the refusal states, each with the document it is
// filed on.
//
// It is built once per refusal, by renderRefusalFinding, and both places the
// daemon reports the refusal read it — the build layer's entries and the error
// answer's structured position — so the two cannot name different places for one
// refusal.
type renderRefusalReport struct {
	// message is the refusal as a whole in the compiler's words, every finding
	// and every cause beneath each, which is what the error answer carries.
	message string

	// entries holds one entry per finding, in the order the refusal states them,
	// and never none.
	entries []refusalEntry
}

// refusalEntry is one finding of a refusal raised while rendering, as the build
// layer carries it.
type refusalEntry struct {
	file       string
	diagnostic Diagnostic
	// located reports whether the entry's position is a key the finding named
	// rather than the document's fall-back key it is drawn on otherwise.
	located bool
}

// entriesOrNone returns the report's entries, or none for no report, so a caller
// holding a possibly absent report reads it without a guard of its own.
func (r *renderRefusalReport) entriesOrNone() []refusalEntry {
	if r == nil {
		return nil
	}
	return r.entries
}

// errorData is the structured position the error answer carries for the refusal:
// the place its first finding points at — the document, and the span of the key
// there where that key was found. Where no key was found the answer names the
// document alone, the shape every other error answer here takes for a file-level
// failure — a client reads a position or none, and never the fall-back position
// the build layer draws the entry at standing in for one. A refusal stating
// several findings names the first, the one the answer's message opens with;
// every finding is on the build layer.
func (r *renderRefusalReport) errorData() *errorData {
	first := r.entries[0]
	if !first.located {
		return newErrorData(first.file, 0, 0)
	}
	d := first.diagnostic
	return newErrorDataWithRange(first.file, d.Line, d.Column, d.EndLine, d.EndColumn)
}

// renderRefusalFinding maps the refusal a preview stopped on while rendering onto
// the editor wire, one entry per finding it states, or returns nil for a preview
// that did not stop there.
//
// It is the ONE mapping from such a refusal to a diagnostic. Every condition the
// run can raise takes it — a Workload that does not satisfy its checks, a template
// that fails, an assertion, a realization short of its contract, a composition the
// graph refuses, and any other — so no condition reaches an editor positioned by a
// route of its own. How many findings a refusal states and where each points are
// decided by the shared preview, from the refusal's own chain, and only resolved
// to a position here.
//
// The position is the key's span through keyPathSpan, the one place a finding
// positioned from a document path gets its position and its fall-back, in the
// encoding this client negotiated. The message is the finding as the compiler
// raised it, with every cause — never a sentence composed here from the data the
// refusal was raised over, so whatever the raising site withheld stays withheld.
// The identifiers are the ones conditionCodesOf decides together, read off the
// finding's own chain, so each entry carries the short code the command line
// prints at the head of the same finding, and the pointer to where that condition
// is explained.
func (s *Server) renderRefusalFinding(preview *compilerops.PreviewResult) *renderRefusalReport {
	refusal, ok := preview.RenderRefusal()
	if !ok || len(refusal.Findings) == 0 {
		return nil
	}
	report := &renderRefusalReport{message: refusal.Message}
	for _, finding := range refusal.Findings {
		doc, idx := s.advisoryPositionSource(finding.Content)
		line, col, endLine, endCol, located := keyPathSpan(doc, idx, finding.KeyPath)
		report.entries = append(report.entries, refusalEntry{
			file: finding.Document,
			diagnostic: Diagnostic{
				Line:           line,
				Column:         col,
				EndLine:        endLine,
				EndColumn:      endCol,
				Severity:       editorSeverityError,
				Message:        finding.Message,
				ConditionCodes: conditionCodesOf(finding.Err),
				KeyPath:        finding.KeyPath,
				unplaced:       !located,
			},
			located: located,
		})
	}
	return report
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
