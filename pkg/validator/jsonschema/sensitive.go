// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package jsonschema

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
)

// SensitiveKeyword is the extension keyword a schema position carries, with the
// boolean value true, to declare the value written there sensitive: a value the
// tool does not reproduce in what it prints for a person to read.
const SensitiveKeyword = "x-aggrega-sensitive"

// DeclaresSensitive reports whether a schema node's keywords declare the value at
// its position sensitive. keywords is the node's keyword map: a compiled schema
// node as decoded from JSON, or the extension keywords a decoded schema carries
// beside the standard ones. One answer serves a reader holding either, so a
// reader collecting the sensitive positions of a document and a validator
// withholding a refused value cannot come to disagree about which nodes declare
// it.
//
// Only the boolean true counts. Presence of the key alone is deliberately not
// enough: a composed schema records, in a sibling map, which entity in an
// `extends` chain declared each keyword, so the same keyword spelling also appears
// there mapped to an entity name rather than to a boolean.
//
// Nothing about a value is examined. A position is sensitive because a schema
// says so, never because of what a value there looks like.
func DeclaresSensitive(keywords map[string]any) bool {
	v, has := keywords[SensitiveKeyword]
	if !has {
		return false
	}
	b, ok := v.(bool)
	return ok && b
}

// Option adjusts how a Validator built by New reports what it finds.
type Option func(*options)

// options is what the Options passed to New decided.
type options struct {
	withholdSensitive bool
}

// WithholdSensitiveValues makes a Validator report a failure at a position whose
// schema declares its value sensitive without reproducing anything of that value.
//
// A finding at such a position, and at every position inside one, carries a
// sentence composed from the schema alone: the keyword that refused the value
// where the position declares one the finding names, that keyword's constraint as
// the schema states it, and the one placeholder every human-readable surface
// prints in place of a withheld value. The library's own sentence, which quotes
// the value, is replaced whole rather than edited, so nothing derived from the
// value survives it — no excerpt, no length, and no type the value was found to
// have. The finding's path and its keyword are unchanged, so a surface still
// names the key that was refused.
//
// It is an option rather than the default because the keyword is read by more
// than one family of schemas and does not mean the same thing in all of them. On
// a published document schema it says the value is secret; on the projection of
// an entity's attachment slots it says only that whatever binds the slot handles
// secret material, and a fill refused there is not secret itself. The caller that
// owns a family of schemas is the one that knows which of the two it holds.
func WithholdSensitiveValues() Option {
	return func(o *options) { o.withholdSensitive = true }
}

// withheldNotice closes every sentence a withheld finding carries, so a reader
// meeting the placeholder is told why the value is not there rather than left to
// wonder whether it was ever read.
const withheldNotice = " (the value is withheld because the schema declares it sensitive)"

// withheldFinding returns f carrying the sentence composed for a value withheld at
// the position node describes, and leaves a finding already withheld as it is.
//
// node is the schema at the finding's own position when the caller holds it, and
// nil when the finding lies beneath the sensitive position the caller is handling.
// definition is the reusable definition node was built from, empty for none, and
// is named in the sentence exactly as any other finding at the position names it.
//
// A finding withheld once is left alone, so the position closest to a value — the
// one whose schema describes it — decides the sentence, and an enclosing sensitive
// position handled afterwards does not replace that sentence with a vaguer one.
func withheldFinding(f Finding, node *jsonschema.Schema, definition string) Finding {
	if f.withheld {
		return f
	}
	if keyword, constraint, ok := refusedConstraint(f, node, definition); ok {
		f.Message = nameViolatedDefinition(keyword+": "+redact.Placeholder()+" does not satisfy "+
			keyword+" "+constraint+withheldNotice, definition)
	} else {
		f.Message = nameViolatedDefinition(redact.Placeholder()+" does not satisfy the schema declared here"+
			withheldNotice, definition)
	}
	f.withheld = true
	return f
}

// withhold replaces, in place, every finding at path or beneath it with the
// sentence withheldFinding composes. A finding at path itself is described by
// node and definition; one beneath it has no schema of its own in hand here and
// takes the sentence that names none.
//
// The empty path is the instance root, which covers every finding.
func withhold(findings []Finding, path string, node *jsonschema.Schema, definition string) {
	for i := range findings {
		at := findings[i].FieldPath
		if path != "" && !isUnderPath(at, path) {
			continue
		}
		if at == path {
			findings[i] = withheldFinding(findings[i], node, definition)
			continue
		}
		findings[i] = withheldFinding(findings[i], nil, "")
	}
}

// withholdBesideWithheld extends a withholding made at one path to every other
// finding in findings at or beneath that path.
//
// It serves a sweep over a region the library evaluates whole, where one schema
// node may be reached along more than one route and the route passing through a
// declaration of sensitivity is not always the first to arrive. A value one route
// declares sensitive is withheld whichever route reported it.
func withholdBesideWithheld(findings []Finding) {
	var paths []string
	for _, f := range findings {
		if f.withheld {
			paths = append(paths, f.FieldPath)
		}
	}
	for _, path := range paths {
		withhold(findings, path, nil, "")
	}
}

// refusedConstraint names the keyword a finding reports against and returns that
// keyword's constraint as node states it, reporting false where node declares no
// such keyword.
//
// The keyword is looked for in two places: the finding's own keyword, which the
// walk sets for the checks it performs itself, and the head of the finding's
// sentence, where a schema library names the keyword it checked. What is read
// there is used only as a key into node's own keywords, and nothing is returned
// unless node declares that key — so the keyword printed is always one the schema
// spells, and a sentence whose head is anything else falls back to naming none.
func refusedConstraint(f Finding, node *jsonschema.Schema, definition string) (string, string, bool) {
	if node == nil {
		return "", "", false
	}
	keywords := schemaKeywords(node)
	if constraint, ok := constraintOf(keywords, f.Keyword); ok {
		return f.Keyword, constraint, true
	}
	sentence := strings.TrimPrefix(f.Message, nameViolatedDefinition("", definition))
	if keyword, _, found := strings.Cut(sentence, ": "); found {
		if constraint, ok := constraintOf(keywords, keyword); ok {
			return keyword, constraint, true
		}
	}
	return "", "", false
}

// schemaKeywords reads a schema node's keywords as the schema document spells
// them, each with its value as written, or nil for a node that does not encode.
func schemaKeywords(node *jsonschema.Schema) map[string]json.RawMessage {
	raw, err := json.Marshal(node)
	if err != nil {
		return nil
	}
	var keywords map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keywords); err != nil {
		return nil
	}
	return keywords
}

// constraintOf renders a keyword's value as the schema states it, for a keyword
// whose value is a scalar or a list of scalars — a pattern, a type, a bound, an
// enumeration. It reports false for a keyword the node does not declare, for a
// keyword whose value is a schema or a map of them, which is a shape to walk
// rather than a constraint to quote, and for an extension keyword: the
// sensitivity marker itself, and the predicates and metadata whose text is not a
// constraint a reader could satisfy by reading it.
//
// The value is re-encoded with HTML escaping off, so a pattern carrying an angle
// bracket or an ampersand reads as the author wrote it.
func constraintOf(keywords map[string]json.RawMessage, keyword string) (string, bool) {
	if keyword == "" || strings.HasPrefix(keyword, "x-") {
		return "", false
	}
	raw, declared := keywords[keyword]
	if !declared {
		return "", false
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil || !scalarOrScalarList(value) {
		return "", false
	}
	var b bytes.Buffer
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "", false
	}
	return strings.TrimSuffix(b.String(), "\n"), true
}

// scalarOrScalarList reports whether a decoded keyword value is a JSON scalar or
// an array holding nothing but scalars.
func scalarOrScalarList(v any) bool {
	switch value := v.(type) {
	case nil, bool, float64, string:
		return true
	case []any:
		for _, item := range value {
			switch item.(type) {
			case nil, bool, float64, string:
			default:
				return false
			}
		}
		return true
	}
	return false
}

// enterSensitive notes, while the index is being built, that the position in
// hand is inside one whose value is sensitive when its schema declares so, and
// returns the function that undoes the note once the position is built.
//
// declared is what the position's schema declares before a reference it carries
// is substituted, and node is the schema after it: the substitution keeps nothing
// that sits beside the reference, so a declaration written there is read before
// it and one inside the referenced schema after it.
//
// The count, not a flag, is what lets a sensitive position inside another undo
// only its own note. Construction is single-threaded, so it needs no guarding.
func (a *googleAdapter) enterSensitive(declared bool, node *jsonschema.Schema) func() {
	if !declared && (node == nil || !DeclaresSensitive(node.Extra)) {
		return func() {}
	}
	a.sensitiveDepth++
	return func() { a.sensitiveDepth-- }
}

// withholdIndexed composes from the schema every finding at or inside a position
// the index built whose value is sensitive. The positions are taken deepest
// first, so a sensitive position inside another is described by its own schema
// rather than by the enclosing one's, and in a fixed order, so the same report is
// produced on every run.
//
// It runs once, over the finished report, because the findings at an indexed
// position arrive from several places — the position's own check, the regions
// the library evaluates whole there, a predicate's else: branch, the whole-document
// check for a position that could not be stood up — and one pass over what they
// produced reaches all of them without each having to remember to.
func (a *googleAdapter) withholdIndexed(rpt *Report) {
	if !a.withholdSensitive || len(a.sensitive) == 0 {
		return
	}
	paths := make([]string, 0, len(a.sensitive))
	for path := range a.sensitive {
		paths = append(paths, path)
	}
	sort.Slice(paths, func(i, j int) bool {
		if len(paths[i]) != len(paths[j]) {
			return len(paths[i]) > len(paths[j])
		}
		return paths[i] < paths[j]
	})
	for _, path := range paths {
		withhold(rpt.Findings, path, a.sensitive[path], a.subDefinition[path])
	}
}

// chainDeclaresSensitive reports whether any schema position a library failure's
// location chain passes through declares its value sensitive, reading each
// location as a pointer into root, and returns the schema at the last location
// the chain names — the one the failure was raised at.
//
// It serves a subtree the library evaluates whole on the walk's behalf, where the
// walk built no position of its own for what lies inside. Every location is read,
// and every step along each, because a reference moves the chain to where the
// referenced schema is declared: a declaration may sit on the position the value
// occupies or on the schema it references, and only the whole chain names both.
func chainDeclaresSensitive(root *jsonschema.Schema, locations []string) (*jsonschema.Schema, bool) {
	var last *jsonschema.Schema
	sensitive := false
	for _, location := range locations {
		pointer := location
		if !strings.HasPrefix(pointer, "/") {
			// The library labels the root of its call rather than pointing at it.
			pointer = ""
		}
		segments := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
		if pointer == "" {
			segments = nil
		}
		for i := 0; i <= len(segments); i++ {
			prefix := ""
			if i > 0 {
				prefix = "/" + strings.Join(segments[:i], "/")
			}
			node := schemaAtPointer(root, prefix)
			if node == nil {
				// A prefix ending inside a keyword that takes a name after it names
				// no schema of its own; the step completing it does.
				continue
			}
			if DeclaresSensitive(node.Extra) {
				sensitive = true
			}
			if i == len(segments) {
				last = node
			}
		}
	}
	return last, sensitive
}
