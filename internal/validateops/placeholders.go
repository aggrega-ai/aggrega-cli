// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validateops

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	schemas "github.com/aggrega-ai/aggrega-schemas"
	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
	aggerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
	jsonvalidator "github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// placeholder is one value a document's kind requires that the document leaves
// as an empty string: the dotted key path, in the spelling the envelope gate
// reports its findings under, the position of the key, and what the value takes.
type placeholder struct {
	path   string
	line   int
	column int
	takes  string
}

// placeholderError reports every placeholder a document carries, at once, each
// named with what it takes.
//
// It wraps the envelope gate's own refusal of the same document when the gate
// refused it, so the condition that refusal carries — its short code, its
// registered sentence and its sentinel — reaches every caller exactly as it did
// before, and only the explanation of the empty values changes. When the schema
// admits every empty value listed, the gate refused nothing and there is no
// condition to carry: the refusal is this scope's alone.
type placeholderError struct {
	path         string
	placeholders []placeholder

	// schema is the envelope gate's refusal of the document, or nil when the
	// gate accepted it.
	schema error

	// rest is the gate's own rendering of the findings that are not about a
	// placeholder, empty when every finding was one. It is the gate's text,
	// never a restatement of it.
	rest string
}

// Error renders the refusal: the gate's remaining findings when there are any,
// otherwise the gate's condition sentence when the gate refused the document,
// then one line per placeholder in document order.
func (e *placeholderError) Error() string {
	var b strings.Builder
	switch {
	case e.rest != "":
		b.WriteString(e.rest)
		b.WriteString("\n  ")
	case e.schema != nil:
		b.WriteString(aggerrors.ComposeMessage(envelope.ErrEntityEnvelope(), nil, ""))
		b.WriteString(": ")
	}
	noun, owes := "placeholders", "each of these needs one"
	if len(e.placeholders) == 1 {
		noun, owes = "placeholder", "it needs one"
	}
	fmt.Fprintf(&b, "%d %s still owed in %s. An empty required value is not a value, so %s before the document validates:",
		len(e.placeholders), noun, e.path, owes)
	for _, p := range e.placeholders {
		fmt.Fprintf(&b, "\n    %d:%d: %s takes %s", p.line, p.column, p.path, p.takes)
	}
	return b.String()
}

// Unwrap reports the envelope gate's refusal, so a caller matching the gate's
// condition matches this refusal too wherever the gate refused the document.
func (e *placeholderError) Unwrap() error { return e.schema }

// placeholderRefusal reads the values a document leaves empty and, when there
// are any, returns the refusal naming every one of them. It returns nil when
// the document carries none, and the caller then reports the gate's own verdict
// unchanged.
//
// schemaErr is the envelope gate's verdict on the same bytes. The reading sits
// beside that gate rather than inside it, and that is deliberate: every catalog
// load runs the gate, so a change to what the gate reports would change what
// every load reports. Read here, the explanation reaches the scope an author
// runs over one document — the scope a drafted document is checked with — and
// a catalog load meeting the same empty value reports exactly what it reported
// before.
//
// A document the gate could not parse, or could not validate at all, has no
// values to read, so the gate's own failure stands.
func placeholderRefusal(kind envelope.Kind, data []byte, path string, schemaErr error) error {
	var gate *envelope.Error
	if schemaErr != nil && !errors.As(schemaErr, &gate) {
		return nil
	}
	found, err := readPlaceholders(kind, data, path)
	if err != nil {
		return errors.Join(fmt.Errorf("read the values %q leaves empty: %w", path, err), schemaErr)
	}
	if len(found) == 0 {
		return nil
	}
	refusal := &placeholderError{path: path, placeholders: found, schema: schemaErr}
	if gate != nil {
		if rest := findingsAwayFrom(gate.Findings, found); len(rest) > 0 {
			remaining := &envelope.Error{Kind: gate.Kind, Path: gate.Path, Findings: rest, Positions: gate.Positions}
			refusal.rest = remaining.Error()
		}
	}
	return refusal
}

// Placeholder is one value a document of a single-entity kind leaves empty, as
// the single-entity scope names it: the dotted key path, spelled as the envelope
// gate spells the paths of its findings, and what the value takes.
type Placeholder struct {
	KeyPath string
	Takes   string
}

// Placeholders is the single-entity scope's reading of a document's
// placeholders, for a surface that reports each one at its own key rather than
// as the one grouped refusal ValidateEntity returns.
type Placeholders struct {
	// Entries are the placeholders, ordered by where each sits in the document.
	Entries []Placeholder

	// Refusal is the refusal ValidateEntity returns for the same bytes. Where
	// the envelope gate refused the document its chain carries the condition
	// that refusal carries, and where the gate accepted it the chain carries no
	// condition, so the identifiers a surface reads off it are the ones the
	// command reports for those bytes.
	Refusal error

	found []placeholder
}

// FindingsAwayFrom returns the envelope findings the refusal still reports
// beside its placeholders: those whose field path is not a placeholder's. It is
// the rule ValidateEntity applies to the same findings, so a surface dropping
// what it drops reports the placeholder in place of the finding the empty value
// fails, and every other finding as the gate reported it.
func (p Placeholders) FindingsAwayFrom(findings []jsonvalidator.Finding) []jsonvalidator.Finding {
	return findingsAwayFrom(findings, p.found)
}

// ReadPlaceholders reads the values a document leaves empty exactly as
// ValidateEntity reads them before it reports the envelope gate's verdict:
// kind is the kind the caller classified the document as, and schemaErr is the
// gate's verdict on the same bytes.
//
// It reports false when kind is not one the single-entity scope accepts, when
// the document leaves no required value empty, and when there was nothing to
// read — the gate could not parse or validate the document, or the reading
// could not run over it. The caller then reports the gate's verdict as it
// stands. A reading that could not run is where this and ValidateEntity part:
// the command refuses the document naming the failure, and a surface reporting
// each placeholder at its key has no key to report that failure at.
func ReadPlaceholders(kind envelope.Kind, data []byte, path string, schemaErr error) (Placeholders, bool) {
	if _, accepted := knownEntityKind(string(kind)); !accepted {
		return Placeholders{}, false
	}
	var refusal *placeholderError
	if !errors.As(placeholderRefusal(kind, data, path, schemaErr), &refusal) {
		return Placeholders{}, false
	}
	entries := make([]Placeholder, 0, len(refusal.placeholders))
	for _, p := range refusal.placeholders {
		entries = append(entries, Placeholder{KeyPath: p.path, Takes: p.takes})
	}
	return Placeholders{Entries: entries, Refusal: refusal, found: refusal.placeholders}, true
}

// findingsAwayFrom returns the findings whose field path is not a placeholder's.
// A finding at a placeholder's path is a constraint the empty value fails, and
// the placeholder line already says what the value takes.
func findingsAwayFrom(findings []jsonvalidator.Finding, found []placeholder) []jsonvalidator.Finding {
	owed := make(map[string]bool, len(found))
	for _, p := range found {
		owed[p.path] = true
	}
	var rest []jsonvalidator.Finding
	for _, f := range findings {
		if !owed[f.FieldPath] {
			rest = append(rest, f)
		}
	}
	return rest
}

// readPlaceholders walks a document against its kind's own schema and returns
// every required value the document leaves as an empty string, ordered by
// where each sits in the document.
//
// Which values are required is read from the schema, never from a list kept
// here. The walk follows a property's `required` list from the document root
// through the properties those lists name, and through a `$ref` or an `allOf`
// member on the way, because each of those applies unconditionally. It does not
// descend a branch that applies only sometimes — `anyOf`, `oneOf`, `if`,
// `then`, `else` — nor into the items of a list or the entries of a map: a
// property required only once its optional container exists is not a value
// every document of the kind owes.
//
// An empty string is a placeholder unless the schema itself names the empty
// string as a value it takes, through an enumeration or a constant, and only
// where the property may be a string at all. A key the document omits is not a
// placeholder, because the gate already names a missing property; a null is not
// either, because the drafted form of a required string is the empty string.
func readPlaceholders(kind envelope.Kind, data []byte, path string) ([]placeholder, error) {
	raw, ok := schemas.SchemaByKind(entitykind.Segment(entitykind.Kind(kind)))
	if !ok {
		return nil, fmt.Errorf("no schema is embedded for kind %s", kind)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(raw), &root); err != nil {
		return nil, fmt.Errorf("decode the %s schema: %w", kind, err)
	}
	doc, err := envelope.ParseDecodable(data, path)
	if err != nil {
		return nil, err
	}
	top := doc
	if top.Kind == yaml.DocumentNode {
		if len(top.Content) == 0 {
			return nil, nil
		}
		top = top.Content[0]
	}
	r := placeholderReader{
		root: root,
		idx:  yamlpos.BuildLineIndexWithEncoding(data, yamlpos.PositionEncodingUTF8),
	}
	r.walk(root, top, nil, 0)
	sort.SliceStable(r.found, func(i, j int) bool {
		if r.found[i].line != r.found[j].line {
			return r.found[i].line < r.found[j].line
		}
		return r.found[i].column < r.found[j].column
	})
	return r.found, nil
}

// maxSchemaDepth bounds the walk and every reference it follows, so a schema
// whose references form a loop ends the walk rather than the process.
const maxSchemaDepth = 64

// placeholderReader carries one walk: the schema document references resolve
// against, the index positions are converted through, and what was found.
type placeholderReader struct {
	root  map[string]any
	idx   yamlpos.LineIndex
	found []placeholder
}

// walk reads one mapping of the document against the schema that governs it.
func (r *placeholderReader) walk(schema map[string]any, node *yaml.Node, keyPath []string, depth int) {
	node = dealias(node)
	if schema == nil || node == nil || node.Kind != yaml.MappingNode || depth > maxSchemaDepth {
		return
	}
	for _, name := range r.requiredOf(schema, 0) {
		key, value := mappingEntry(node, name)
		if key == nil {
			continue
		}
		value = dealias(value)
		if value == nil {
			continue
		}
		prop := r.resolve(r.propertyOf(schema, name, 0), 0)
		path := append(append([]string(nil), keyPath...), name)
		switch {
		case value.Kind == yaml.ScalarNode && value.ShortTag() == "!!str" && value.Value == "":
			if prop == nil || declaresEmpty(prop) || !mayBeString(prop) {
				continue
			}
			pos := r.idx.NodeToPosition(key)
			r.found = append(r.found, placeholder{
				path:   strings.Join(path, "."),
				line:   pos.Line + 1,
				column: pos.Character + 1,
				takes:  takes(prop),
			})
		case value.Kind == yaml.MappingNode:
			r.walk(prop, value, path, depth+1)
		}
	}
}

// requiredOf returns the property names a schema requires, in the order it
// lists them, with the names its `allOf` members require after them.
func (r *placeholderReader) requiredOf(schema map[string]any, depth int) []string {
	schema = r.resolve(schema, depth)
	if schema == nil || depth > maxSchemaDepth {
		return nil
	}
	var out []string
	for _, v := range asSlice(schema["required"]) {
		if name, ok := v.(string); ok {
			out = append(out, name)
		}
	}
	for _, member := range asSlice(schema["allOf"]) {
		if m, ok := member.(map[string]any); ok {
			out = append(out, r.requiredOf(m, depth+1)...)
		}
	}
	return dedupe(out)
}

// propertyOf returns the schema a property is declared with, looked up on the
// schema itself and then on its `allOf` members in order.
func (r *placeholderReader) propertyOf(schema map[string]any, name string, depth int) map[string]any {
	schema = r.resolve(schema, depth)
	if schema == nil || depth > maxSchemaDepth {
		return nil
	}
	if props, ok := schema["properties"].(map[string]any); ok {
		if p, ok := props[name].(map[string]any); ok {
			return p
		}
	}
	for _, member := range asSlice(schema["allOf"]) {
		if m, ok := member.(map[string]any); ok {
			if p := r.propertyOf(m, name, depth+1); p != nil {
				return p
			}
		}
	}
	return nil
}

// resolve follows a schema's local `$ref` to the schema it names. A reference
// into another document is left unresolved, and the schema is returned as it
// stands.
func (r *placeholderReader) resolve(schema map[string]any, depth int) map[string]any {
	for hops := depth; schema != nil && hops <= maxSchemaDepth; hops++ {
		ref, ok := schema["$ref"].(string)
		if !ok || !strings.HasPrefix(ref, "#/") {
			return schema
		}
		target := any(r.root)
		for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
			m, ok := target.(map[string]any)
			if !ok {
				return nil
			}
			target = m[strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")]
		}
		next, ok := target.(map[string]any)
		if !ok {
			return nil
		}
		schema = next
	}
	return schema
}

// declaresEmpty reports whether a schema names the empty string among the
// values it takes, which makes an empty value a value rather than a placeholder.
func declaresEmpty(schema map[string]any) bool {
	if c, ok := schema["const"]; ok && c == "" {
		return true
	}
	for _, v := range asSlice(schema["enum"]) {
		if v == "" {
			return true
		}
	}
	return false
}

// mayBeString reports whether a schema admits a string, reading its declared
// type. A schema declaring no type admits one.
func mayBeString(schema map[string]any) bool {
	switch t := schema["type"].(type) {
	case nil:
		return true
	case string:
		return t == "string"
	case []any:
		for _, v := range t {
			if v == "string" {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// semanticVersionExample is the value a placeholder for a semantic version is
// shown as, and one the pattern is confirmed to accept before it is shown.
const semanticVersionExample = "1.0.0"

// takes says what a placeholder's value takes, read from the property's own
// schema: its enumeration, or its constant; for a pattern that behaves as the
// semantic-version grammar, a semantic version; otherwise the first sentence of
// the property's own description, then its pattern; and a value that is not
// empty where the schema says nothing more.
func takes(schema map[string]any) string {
	if values := asSlice(schema["enum"]); len(values) > 1 {
		parts := make([]string, 0, len(values))
		for _, v := range values {
			parts = append(parts, fmt.Sprint(v))
		}
		return "one of: " + strings.Join(parts, ", ")
	} else if len(values) == 1 {
		return fmt.Sprintf("exactly %v", values[0])
	}
	if c, ok := schema["const"]; ok {
		return fmt.Sprintf("exactly %v", c)
	}
	pattern, _ := schema["pattern"].(string)
	if pattern != "" && isSemanticVersionPattern(pattern) {
		return "a semantic version, such as " + semanticVersionExample
	}
	if d, ok := schema["description"].(string); ok {
		if s := firstSentence(d); s != "" {
			return "what its schema describes: " + s
		}
	}
	if pattern != "" {
		return fmt.Sprintf("a value matching the pattern %q", pattern)
	}
	return "a value that is not empty"
}

// semanticVersionAccepted and semanticVersionRefused decide whether a pattern
// behaves as the semantic-version grammar, which is what lets a placeholder say
// "a semantic version" rather than print the pattern.
//
// The schemas state that grammar inline, once per kind, and nothing else in the
// product carries it, so there is no single statement to compare a pattern
// against. Probing is the reading that holds whichever way a schema spells the
// grammar: a pattern accepting every value on the first list and none on the
// second is treated as a version pattern, and any other pattern is described in
// its own words instead. A pattern that stops behaving this way is therefore
// never mislabelled; it is only described less briefly.
var (
	semanticVersionAccepted = []string{semanticVersionExample, "0.1.0", "10.20.30", "1.0.0-rc.1", "1.0.0+build.5", "1.0.0-alpha.1+001"}
	semanticVersionRefused  = []string{"", "1", "1.0", "v1.0.0", "01.0.0", "1.0.0.0", "1.0.0-", "latest"}
)

// isSemanticVersionPattern reports whether a pattern accepts every value on the
// accepted list and refuses every value on the refused list.
func isSemanticVersionPattern(pattern string) bool {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false
	}
	for _, v := range semanticVersionAccepted {
		if !re.MatchString(v) {
			return false
		}
	}
	for _, v := range semanticVersionRefused {
		if re.MatchString(v) {
			return false
		}
	}
	return true
}

// firstSentence returns a description's opening sentence: the text up to the
// first full stop that is followed by a space or ends the text, outside any
// parentheses. A description of one sentence is returned whole.
func firstSentence(description string) string {
	d := strings.TrimSpace(description)
	depth := 0
	for i := 0; i < len(d); i++ {
		switch d[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case '.':
			if depth == 0 && (i+1 == len(d) || d[i+1] == ' ' || d[i+1] == '\n') {
				return d[:i+1]
			}
		}
	}
	return d
}

// mappingEntry returns the key and value nodes for a key in a mapping node, or
// two nils when the key is absent.
func mappingEntry(node *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i], node.Content[i+1]
		}
	}
	return nil, nil
}

// dealias follows an alias to the node it names. The document came through the
// envelope's parse, which refuses an anchor aliased inside its own content, so
// the chain ends; the bound is there regardless.
func dealias(node *yaml.Node) *yaml.Node {
	for hops := 0; node != nil && node.Kind == yaml.AliasNode && hops <= maxSchemaDepth; hops++ {
		node = node.Alias
	}
	return node
}

// asSlice returns a decoded JSON array, or nil for anything else.
func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

// dedupe drops repeated names, keeping the first occurrence of each in order.
func dedupe(names []string) []string {
	seen := make(map[string]bool, len(names))
	out := names[:0]
	for _, n := range names {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}
