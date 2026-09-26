// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"errors"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/serve/standalonekind"
	"github.com/aggrega-ai/aggrega-cli/internal/validateops"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
	jsonvalidator "github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// entityKindByName maps the value an author writes in a document's `kind:`
// field to the envelope family that validates documents of that kind.
//
// It is built from the declared entity-kind vocabulary rather than written out,
// so the kinds the daemon offers as a `kind:` completion and the kinds whose
// documents it validates are one set by construction. Offering a kind and then
// declining to validate documents of it is a divergence invisible in either
// surface read alone, and a hand-written list on this side is how a kind comes to
// be dropped from one of them.
//
// The envelope families are the wider set — they cover generated and
// configuration documents as well as authored entities — so this is a narrowing
// of them, and it narrows to what an author writes as a document of their own. A
// family with no entity kind behind it contributes nothing here; a kind with no
// family behind it likewise contributes nothing, so this cannot name a family
// that has no schema to validate against, and a test reports either gap rather
// than leaving it silent.
//
// Two kinds of aggrega content are absent for two different reasons, and both
// reasons hold whatever the vocabulary declares.
//
// The workspace configuration and the lockfile are unreachable by any
// apiVersion+kind classification: they carry no such header at all, being
// flat-top-level documents, so classifyByFilename classifies those instead.
//
// Content written inside another kind's document is a different case. It does
// carry a header, and its schema fixes both halves of it, but the header names
// the content for validation where it sits rather than making it a document of
// its own — so an editor buffer is not where such content is read from, and the
// kind is not one this classification reaches. What a buffer of it classifies as
// is foreign YAML, which is the same answer the surrounding document's own
// classification already gives for the region the content occupies.
var entityKindByName = authoredEnvelopeKindsByName()

// authoredEnvelopeKindsByName pairs every kind an author writes a document of
// with its envelope family, in the vocabulary's own terms: a kind and its family
// carry the same name, so the pairing is the name itself rather than a per-kind
// decision.
func authoredEnvelopeKindsByName() map[string]envelope.Kind {
	families := make(map[envelope.Kind]bool)
	for _, family := range envelope.Kinds() {
		families[family] = true
	}
	declared := standalonekind.Kinds()
	out := make(map[string]envelope.Kind, len(declared))
	for _, k := range declared {
		family := envelope.Kind(string(k))
		if families[family] {
			out[string(k)] = family
		}
	}
	return out
}

// classifyAggregaDocument peeks apiVersion + kind from the parsed buffer
// node and returns the envelope.Kind for an authored aggrega entity, or
// ok=false for anything else — the non-aggrega guard. A document without
// an aggrega.ai apiVersion, without a known entity kind, or that is not a
// single mapping document classifies as not-ours, so the per-file
// envelope leg never touches foreign YAML (chart templates, compose
// files, Kubernetes manifests, arbitrary YAML).
func classifyAggregaDocument(doc *yaml.Node) (envelope.Kind, bool) {
	_, kind, ok := peekDocumentHeader(doc)
	if !ok {
		return "", false
	}
	k, known := entityKindByName[kind]
	return k, known
}

// peekDocumentHeader reads the apiVersion and kind scalars off the top level of
// a parsed buffer, reporting ok only for a single mapping document carrying an
// aggrega.ai apiVersion — the non-aggrega guard. Foreign YAML (chart templates,
// compose files, Kubernetes manifests, arbitrary documents) reports not-ok, so
// nothing keyed on the header reaches it.
//
// It is separate from the classification above because two questions are asked
// of the same two scalars and they resolve them in different vocabularies: the
// envelope gate maps the kind onto the schema that validates it, while the
// publishing-conventions layer asks the entity-kind vocabulary whether the
// conventions apply to that kind at all. A second walk per question is how one
// of them would come to disagree about which document it is looking at.
//
// The kind is returned as authored rather than as any resolved token, because a
// caller resolving it is the one that knows which vocabulary it means.
func peekDocumentHeader(doc *yaml.Node) (apiVersion, kind string, ok bool) {
	if doc == nil {
		return "", "", false
	}
	root := doc
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) == 0 {
			return "", "", false
		}
		root = root.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		return "", "", false
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		key := root.Content[i]
		val := root.Content[i+1]
		if key.Kind != yaml.ScalarNode || val.Kind != yaml.ScalarNode {
			continue
		}
		switch key.Value {
		case "apiVersion":
			apiVersion = val.Value
		case "kind":
			kind = val.Value
		}
	}
	if !strings.HasPrefix(apiVersion, aggregaAPIGroup+"/") {
		return "", "", false
	}
	return apiVersion, kind, true
}

// classifyByFilename classifies the two flat-top-level aggrega documents
// that carry no apiVersion+kind envelope and so cannot be reached by
// classifyAggregaDocument: the workspace config and the lockfile of the
// Release the request named. The workspace config is the fixed, unique
// name ".aggrega-config.yaml". The lockfile is matched by correlation to
// releasePath — only the basename that lockfile.PathFor derives from it
// classifies as a lockfile; with no Release named, nothing does. The bare
// ".lock" extension is deliberately NOT used: it belongs to many foreign
// ecosystems (Cargo.lock, flake.lock, poetry.lock, yarn.lock,
// Gemfile.lock, …), none of which is an aggrega lockfile, and validating
// them against the lockfile schema would lint-bomb foreign files.
// Correlation keeps the classification anchored to the documents of the
// Release the request is about, and never touches another ecosystem's
// lockfile.
//
// The Release is a parameter rather than daemon state so this
// classification and the semantic validation running beside it are
// decided against one and the same Release. Read from a stored selection
// instead, the two could disagree — a buffer classified against one
// Release while its findings were computed against another.
func classifyByFilename(file, releasePath string) (envelope.Kind, bool) {
	base := filepath.Base(file)
	if base == workspaceconfig.ConfigFilename {
		return envelope.KindAggregaConfig, true
	}
	if releasePath != "" && base == filepath.Base(lockfile.PathFor(releasePath)) {
		return envelope.KindLockfile, true
	}
	return "", false
}

// envelopeDiagnosticsForBuffer runs the document-envelope gate over the
// editor BUFFER content for a classifiable aggrega document and returns
// its findings as per-file diagnostics. Classification is by filename
// first (the workspace config and the named Release's lockfile, which
// carry no apiVersion+kind envelope) and then by the document's
// apiVersion+kind (every kind an author writes a document of). The second return is
// true only when the document classifies AND the envelope rejected it or
// it leaves a required value empty — the signal the caller uses to
// early-return (which short-circuits the semantic pass for the named
// Release's Aggregate and is the coverage for every other file).
// Foreign YAML and envelope-clean documents that owe no value return
// (nil, false), so the caller falls through to its existing flow unchanged.
//
// A document classified above as a kind the single-entity validation accepts
// is also read for its placeholders — the required values it leaves empty — by
// the reading that validation itself runs, so the editor names every placeholder the command
// names for the same bytes, with what each takes. That includes an empty value
// the schema admits, which the envelope reports nothing about. An envelope
// finding at a placeholder's key is dropped in the placeholder's favour, by the
// rule the command's refusal drops it by; every other finding is reported as
// it would be without the reading. Each placeholder is drawn at the key the
// reading found it under — a key whose own name contains a dot included, which
// no field path addresses — converted through the same client-encoding index an
// envelope finding is drawn through.
func (s *Server) envelopeDiagnosticsForBuffer(file, releasePath string, content []byte, doc *yaml.Node) ([]Diagnostic, bool) {
	kind, ok := classifyByFilename(file, releasePath)
	if !ok {
		kind, ok = classifyAggregaDocument(doc)
	}
	if !ok {
		return nil, false
	}
	_, err := envelope.ValidateBytes(kind, content, "")
	var envErr *envelope.Error
	if err != nil && !errors.As(err, &envErr) {
		// A non-envelope engine error: this leg produces no findings; fall
		// through.
		return nil, false
	}
	owed, hasPlaceholders := validateops.ReadPlaceholders(kind, content, file, err)
	if envErr == nil && !hasPlaceholders {
		// Clean, and nothing still owed: this leg produces no findings.
		return nil, false
	}
	idx := yamlpos.BuildLineIndexWithEncoding(content, s.positionEncoding())
	// The sentence is read off the condition rather than written here, and the
	// message a terminal shows for the same rejection reads it from that same
	// place, so the two surfaces cannot disagree about which condition they are
	// reporting. That is a property of where the words come from, not of two
	// literals that happen to match today.
	//
	// The kind is this occurrence's value and goes in the parentheses; the file
	// is deliberately not among them, because a diagnostic is already attached
	// to the file it was raised on. Nor is the advice the grouped report closes
	// with: this surface emits one diagnostic per finding, so advice would be
	// repeated once per squiggle and add lines without adding facts.
	header := cerrors.ComposeMessage(envelope.ErrEntityEnvelope(), []string{string(kind)}, "")
	var diags []Diagnostic
	if envErr != nil {
		// One rejection, one condition: every finding beneath it reports the same
		// identifiers and opens with the same sentence, and what separates them is
		// the key each one points at.
		codes := conditionCodesOf(envErr)
		findings := leafmostFindings(envErr.Findings)
		if hasPlaceholders {
			// Dropped after the leaf-most narrowing rather than before it, so a
			// finding that narrowing hid stays hidden: the placeholder takes the
			// place of the finding it replaces and uncovers nothing else.
			findings = owed.FindingsAwayFrom(findings)
		}
		for _, f := range findings {
			line, col, endLine, endCol := fieldPathSpan(doc, idx, f.FieldPath)
			diags = append(diags, Diagnostic{
				Line:           line,
				Column:         col,
				EndLine:        endLine,
				EndColumn:      endCol,
				Severity:       editorSeverityError,
				Message:        header + ": " + f.Message,
				ConditionCodes: codes,
			})
		}
	}
	if hasPlaceholders {
		// The refusal the command reports for these bytes is what the identifiers
		// are read from: the envelope's condition where the gate refused the
		// document, and none where it admitted every empty value. The message opens
		// with the condition's sentence exactly when there is a condition to name.
		codes := conditionCodesOf(owed.Refusal)
		lead := ""
		if envErr != nil {
			lead = header + ": "
		}
		for _, p := range owed.Entries {
			line, col, endLine, endCol := keySpan(idx, p.Key)
			diags = append(diags, Diagnostic{
				Line:      line,
				Column:    col,
				EndLine:   endLine,
				EndColumn: endCol,
				Severity:  editorSeverityError,
				Message: lead + p.KeyPath + " is a placeholder still owed. An empty required value is not a value, " +
					"so it needs one before the document validates",
				ConditionCodes: codes,
				Suggest:        "fill it with " + p.Takes,
				KeyPath:        p.KeyPath,
			})
		}
	}
	return diags, true
}

// fieldPathSpan draws a key the envelope's field-path spelling names, in the
// client's negotiated position encoding, and at the head of the file when the
// path resolves to nothing.
//
// The path is resolved through the same walk the envelope's own rendered
// message uses, against the same buffer bytes it just validated, so the two
// surfaces cannot disagree about WHICH key a violation points at. The span is
// recomputed here rather than read off the error because it must be expressed
// in the client's negotiated position encoding, which only the daemon knows —
// so the line is the same number on both surfaces and the column is the same
// position counted in the units each surface reports in.
func fieldPathSpan(doc *yaml.Node, idx yamlpos.LineIndex, fieldPath string) (line, col, endLine, endCol int) {
	line, col = 1, 1
	if span, ok := yamlpos.FieldRange(doc, idx, fieldPath); ok {
		line, col, endLine, endCol = span.Line, span.Column, span.EndLine, span.EndColumn
	}
	return line, col, endLine, endCol
}

// keySpan draws a key node in the client's negotiated position encoding: from
// where the key starts to where its text ends, point-style for a key whose text
// runs over more than one line, and at the head of the file when there is no
// node.
//
// It converts the node exactly as yamlpos.FieldRange converts the node a field
// path resolves to, through the same index, so a key both can reach is drawn at
// one span by either. That is a deliberate second statement of the conversion,
// not a drift: FieldRange takes a path, and a placeholder is drawn at the key
// the placeholder reading found, which for a key whose own name contains a dot
// is one no field path addresses. TestAKeyNodeIsDrawnAsTheFieldPathWalkDrawsIt
// holds the two to one span over every key a field path does reach.
func keySpan(idx yamlpos.LineIndex, key *yaml.Node) (line, col, endLine, endCol int) {
	if key == nil {
		return 1, 1, 0, 0
	}
	start := idx.NodeToPosition(key)
	line, col = start.Line+1, start.Character+1
	// A multi-line key has no single-line span to underline, so the end is left
	// unset rather than pointing at a coordinate on another line.
	if strings.Contains(key.Value, "\n") {
		return line, col, 0, 0
	}
	end := idx.ScalarEnd(key)
	return line, col, end.Line + 1, end.Character + 1
}

// leafmostFindings keeps only findings whose FieldPath is not a strict
// ancestor of another finding's FieldPath. The envelope engine reports a
// single violation at each level of the path to it (e.g. spec →
// spec.boundaries → spec.boundaries.main); keeping the leaf-most yields
// one diagnostic at the deepest walkable key rather than a squiggle at
// every ancestor.
func leafmostFindings(fs []jsonvalidator.Finding) []jsonvalidator.Finding {
	var out []jsonvalidator.Finding
	for i, f := range fs {
		ancestor := false
		for j, g := range fs {
			if i != j && f.FieldPath != g.FieldPath &&
				(f.FieldPath == "" || strings.HasPrefix(g.FieldPath, f.FieldPath+".")) {
				ancestor = true
				break
			}
		}
		if !ancestor {
			out = append(out, f)
		}
	}
	return out
}
