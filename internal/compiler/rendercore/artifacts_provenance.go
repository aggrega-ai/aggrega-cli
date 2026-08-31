// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"go.yaml.in/yaml/v3"
)

// artifactInjectOutcome is the result of the multi-doc round-trip fallback.
type artifactInjectOutcome int

const (
	artifactInjected         artifactInjectOutcome = iota // out carries the injected body
	artifactSkip                                          // emit the original bytes + warn
	artifactParseFailDefault                              // default-delims parse failure → CATALOG_ERROR
)

// spliceProvenanceMultiDoc is the primary provenance path: a byte-faithful,
// per-document SURGICAL splice. It decodes every document in the rendered stream
// (a Decoder loop — multi-document-aware, unlike a single yaml.Unmarshal which
// decodes only the first document and silently drops the rest), plans a
// line-granularity insertion of the provenance label/annotation lines under each
// document's metadata, applies the insertions to the ORIGINAL rendered bytes, and
// runs a post-splice guard (re-parse + Helm-token survival + labels-landed). Every
// byte the splice does not insert stays identical, so no Helm `{{ }}` token outside
// the inserted lines can be re-shaped.
//
// Returns ok=false (the caller falls back to the round-trip) when ANY document's
// metadata region is non-spliceable (flow-style, missing metadata, a provenance-key
// collision needing overwrite, or a structural shape the line planner does not
// handle) or when the post-splice guard finds an anomaly. ok=false never emits a
// partial splice — the caller owns the fallback.
func spliceProvenanceMultiDoc(rendered string, labels, annotations map[string]string) (string, bool) {
	docs, err := decodeAllDocs(rendered)
	if err != nil || len(docs) == 0 {
		return "", false
	}

	lines := strings.Split(rendered, "\n")
	body := []byte(rendered)
	offs := lineStartOffsets(rendered)
	bo := byteOffsetFor(body, offs)
	var inserts []lineInsertion
	stampedAny := false
	for _, doc := range docs {
		di, ok := planDocProvenanceSplice(doc, lines, body, offs, bo, labels, annotations)
		if !ok {
			return "", false
		}
		if len(di) > 0 {
			stampedAny = true
			inserts = append(inserts, di...)
		}
	}
	if !stampedAny {
		// No document in the stream carried a metadata block, so this splice found
		// no Kubernetes-manifest provenance site and inserted nothing. The bytes go
		// back unchanged and unstamped — which is the whole answer only for a
		// document model whose provenance site IS metadata. A guest emitting a
		// different model locates its own sites through its own placer; nothing
		// here can judge a shape it does not model.
		return rendered, true
	}

	out := applyLineInsertions(lines, inserts)
	if !postSpliceGuardOK(rendered, out, labels, annotations) {
		return "", false
	}
	return out, true
}

// lineInsertion records new lines to insert AFTER a 0-based line index of the
// original split stream. Insertions at the same index preserve append order.
type lineInsertion struct {
	afterIdx int
	seq      int // tie-break for stable ordering at the same afterIdx
	lines    []string
}

// planDocProvenanceSplice plans the label + annotation insertions for one decoded
// document, or returns ok=false when the document's metadata region is not safely
// spliceable. A document with no metadata mapping plans no insertions and reports
// ok=true: this planner reads the Kubernetes-manifest model, where metadata is the
// provenance site, so its absence means there is no site to plan against. The
// caller sees that as "nothing stamped" and owns what follows from it.
func planDocProvenanceSplice(root *yaml.Node, lines []string, body []byte, offs []int, bo func(int, int) int, labels, annotations map[string]string) ([]lineInsertion, bool) {
	mapping := DocumentMapping(root)
	if mapping == nil {
		return nil, true // no-op: not a mapping document (e.g. a list / scalar fragment)
	}
	metaKey, metaVal := FindKeyValueNode(mapping, "metadata")
	if metaKey == nil || metaVal == nil {
		return nil, true // no metadata block: no site in this model to plan against
	}
	metaVal = UnwrapAlias(metaVal)
	if metaVal.Kind != yaml.MappingNode || isFlow(metaVal) || len(metaVal.Content) == 0 {
		return nil, false // flow-style or empty metadata → non-spliceable, fall back
	}

	var ins []lineInsertion
	li, ok := planMappingChildSplice(metaKey, metaVal, lines, body, offs, bo, "labels", labels, 0)
	if !ok {
		return nil, false
	}
	ins = append(ins, li...)
	ai, ok := planMappingChildSplice(metaKey, metaVal, lines, body, offs, bo, "annotations", annotations, 1)
	if !ok {
		return nil, false
	}
	ins = append(ins, ai...)
	return ins, true
}

// planMappingChildSplice plans inserting entries into metadata.<key> (labels or
// annotations): append after the block's last child when the child exists, or
// create the block under metadata when it is absent. Returns ok=false on a
// non-spliceable shape (flow-style child, or a provenance-key collision that would
// need an in-place overwrite — routed to the round-trip fallback rather than
// handled by the append-only splice). Empty entries is a no-op.
func planMappingChildSplice(metaKey, metaVal *yaml.Node, lines []string, body []byte, offs []int, bo func(int, int) int, key string, entries map[string]string, seq int) ([]lineInsertion, bool) {
	if len(entries) == 0 {
		return nil, true
	}
	keys := sortedStringKeys(entries)

	childKey, childVal := FindKeyValueNode(metaVal, key)
	if childKey != nil && childVal != nil {
		childVal = UnwrapAlias(childVal)
		if childVal.Kind != yaml.MappingNode || isFlow(childVal) {
			return nil, false // non-mapping or flow child → fall back
		}
		// Append-only: a pre-existing provenance key would need an in-place
		// value overwrite, which the line splice does not do — fall back.
		for _, k := range keys {
			if ek, _ := FindKeyValueNode(childVal, k); ek != nil {
				return nil, false
			}
		}
		if len(childVal.Content) == 0 {
			return nil, false // `labels: {}` empty mapping has no child line to anchor to
		}
		lastChildKey := childVal.Content[len(childVal.Content)-2] // last key node
		lastChildVal := childVal.Content[len(childVal.Content)-1] // its value node
		indent := leadingIndent(lineAt(lines, lastChildKey.Line))
		// The new entries land after the last child's true last source line,
		// which — when that child's value is a multi-line block scalar — is past
		// the key line. The shared span primitive resolves it, so the splice
		// never lands inside a block scalar's continuation.
		afterLine := lineOfOffset(offs, spanOfNode(body, lastChildVal, lastChildKey.Column-1, false, offs, bo).End-1)
		var newLines []string
		for _, k := range keys {
			newLines = append(newLines, indent+k+": "+yamlScalar(entries[k]))
		}
		return []lineInsertion{{afterIdx: afterLine - 1, seq: seq, lines: newLines}}, true
	}

	// Create the block under metadata: insert after the `metadata:` key line, at
	// the metadata-children indent (read from metadata's first existing child).
	firstChild := metaVal.Content[0]
	childIndent := leadingIndent(lineAt(lines, firstChild.Line))
	entryIndent := childIndent + "  "
	newLines := []string{childIndent + key + ":"}
	for _, k := range keys {
		newLines = append(newLines, entryIndent+k+": "+yamlScalar(entries[k]))
	}
	return []lineInsertion{{afterIdx: metaKey.Line - 1, seq: seq, lines: newLines}}, true
}

// applyLineInsertions splices the planned insertions into the split stream and
// rejoins. Insertions are applied in descending afterIdx order (ties by seq) so an
// earlier insertion never shifts a later insertion's target index.
func applyLineInsertions(lines []string, inserts []lineInsertion) string {
	sort.SliceStable(inserts, func(i, j int) bool {
		if inserts[i].afterIdx != inserts[j].afterIdx {
			return inserts[i].afterIdx > inserts[j].afterIdx
		}
		return inserts[i].seq > inserts[j].seq
	})
	out := lines
	for _, ins := range inserts {
		at := ins.afterIdx + 1
		if at < 0 || at > len(out) {
			at = len(out)
		}
		next := make([]string, 0, len(out)+len(ins.lines))
		next = append(next, out[:at]...)
		next = append(next, ins.lines...)
		next = append(next, out[at:]...)
		out = next
	}
	return strings.Join(out, "\n")
}

// postSpliceGuardOK is the runtime safety guard: the spliced output must
// re-parse as valid YAML across every document, preserve every Helm `{{ }}` token
// the input carried, and carry the provenance keys under each stamped document's
// metadata.labels / metadata.annotations. Any anomaly → false (caller falls back).
func postSpliceGuardOK(rendered, spliced string, labels, annotations map[string]string) bool {
	docs, err := decodeAllDocs(spliced)
	if err != nil {
		return false
	}
	if !helmTokensPreserved(rendered, spliced) {
		return false
	}
	// At least one document must carry the provenance — confirms the splice landed
	// under metadata.labels/annotations rather than at some wrong parent.
	landed := false
	for _, doc := range docs {
		mapping := DocumentMapping(doc)
		if mapping == nil {
			continue
		}
		_, metaVal := FindKeyValueNode(mapping, "metadata")
		if metaVal == nil {
			continue
		}
		metaVal = UnwrapAlias(metaVal)
		if metaVal.Kind != yaml.MappingNode {
			continue
		}
		if mappingChildHasAllKeys(metaVal, "labels", labels) && mappingChildHasAllKeys(metaVal, "annotations", annotations) {
			landed = true
		}
	}
	return landed
}

func mappingChildHasAllKeys(metaVal *yaml.Node, key string, entries map[string]string) bool {
	if len(entries) == 0 {
		return true
	}
	_, child := FindKeyValueNode(metaVal, key)
	if child == nil {
		return false
	}
	child = UnwrapAlias(child)
	if child.Kind != yaml.MappingNode {
		return false
	}
	for k := range entries {
		if ek, _ := FindKeyValueNode(child, k); ek == nil {
			return false
		}
	}
	return true
}

// injectProvenanceRoundTrip is the multi-document-aware fallback: it decodes EVERY
// document (a Decoder loop), injects provenance into each via the shared
// injectProvenanceNode, re-marshals all documents, and (external-delims only)
// verifies Helm-token preservation over the whole stream. Multi-doc-aware by
// construction — the single-yaml.Unmarshal path it replaces decoded only the first
// document and silently dropped the rest. Re-marshalling is not byte-faithful (the
// reason the splice is preferred), but it is the verified-safe path for the
// documents the splice cannot handle.
func injectProvenanceRoundTrip(filename, rendered string, labels, annotations map[string]string, usingDefaultDelims bool, indent int) ([]byte, artifactInjectOutcome, error) {
	dec := yaml.NewDecoder(strings.NewReader(rendered))
	var docs []*yaml.Node
	for {
		var n yaml.Node
		if derr := dec.Decode(&n); derr != nil {
			if derr == io.EOF {
				break
			}
			if usingDefaultDelims {
				return nil, artifactParseFailDefault, derr
			}
			return nil, artifactSkip, nil
		}
		docs = append(docs, &n)
	}
	if len(docs) == 0 {
		return []byte(rendered), artifactInjected, nil
	}

	for _, d := range docs {
		if d.Kind == 0 {
			continue
		}
		if err := injectProvenanceNode(filename, d, labels, annotations); err != nil {
			return nil, artifactInjected, err // CATALOG_ERROR from the labels-shape guard
		}
	}

	var buf bytes.Buffer
	for i, d := range docs {
		if d.Kind == 0 {
			continue
		}
		b, err := MarshalYAMLIndent(d, indent)
		if err != nil {
			return nil, artifactInjected, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: filename}, fmt.Sprintf("could not serialise artifact %q as YAML", filename))
		}
		if i > 0 {
			buf.WriteString("---\n")
		}
		buf.Write(b)
	}
	body := buf.Bytes()

	if !usingDefaultDelims && !helmTokensPreserved(rendered, string(body)) {
		return nil, artifactSkip, nil
	}
	return body, artifactInjected, nil
}

// helmTokensPreserved reports whether every Helm `{{ ... }}` token in in survives
// verbatim in out, with matching count — the substring + count check that guards
// the round-trip body (the same gate the single-doc path used). Provenance labels
// add no `{{ }}`, so exact equality holds.
func helmTokensPreserved(in, out string) bool {
	inTokens := helmTemplateTokens(in)
	if len(inTokens) != len(helmTemplateTokens(out)) {
		return false
	}
	for _, tok := range inTokens {
		if !strings.Contains(out, tok) {
			return false
		}
	}
	return true
}

// decodeAllDocs decodes every document in a YAML stream (multi-document-aware).
func decodeAllDocs(s string) ([]*yaml.Node, error) {
	docs, _, err := decodeAllDocsWithLines(s)
	return docs, err
}

// DecodeAllYAMLDocs decodes every document in a YAML stream, so a provenance
// placer for a document model this package does not itself splice reaches the
// one decoder rather than opening a second one. A stream carrying `---`
// separators yields one node per document; a single-yaml.Unmarshal caller would
// keep only the first and drop the rest, which is a silent misread rather than
// an error, and is why the decoder is shared instead of re-rolled per placer.
func DecodeAllYAMLDocs(s string) ([]*yaml.Node, error) {
	return decodeAllDocs(s)
}

// decodeAllDocsWithLines is the single multi-document decoder. It runs one
// yaml.Decoder loop over the stream and returns, per document, the decoded
// DocumentNode plus its 1-based start line — the DocumentNode's own Line, which
// the parser reports as the line of the `---` separator that introduces the
// document (or the first significant token for a leading document with no
// separator). decodeAllDocs delegates here and discards the lines, so a single
// decoder governs both the provenance splice and the byte-faithful segmenter;
// there is no second YAML document decoder in the renderer.
func decodeAllDocsWithLines(s string) ([]*yaml.Node, []int, error) {
	dec := yaml.NewDecoder(strings.NewReader(s))
	var docs []*yaml.Node
	var startLines []int
	for {
		var n yaml.Node
		if err := dec.Decode(&n); err != nil {
			if err == io.EOF {
				break
			}
			return nil, nil, err
		}
		docs = append(docs, &n)
		startLines = append(startLines, n.Line)
	}
	return docs, startLines, nil
}

func isFlow(n *yaml.Node) bool { return n != nil && n.Style&yaml.FlowStyle != 0 }

// lineAt returns the 1-based line of the split stream, or "" when out of range.
func lineAt(lines []string, oneBasedLine int) string {
	idx := oneBasedLine - 1
	if idx < 0 || idx >= len(lines) {
		return ""
	}
	return lines[idx]
}

func leadingIndent(line string) string {
	return line[:len(line)-len(strings.TrimLeft(line, " "))]
}

func sortedStringKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// yamlScalar renders a provenance value as a YAML scalar, quoting when the value
// would otherwise be misparsed (so the spliced line round-trips to the same
// string). Provenance values are simple identifiers / paths / versions; the
// conservative rule double-quotes anything not matching a safe plain scalar.
func yamlScalar(v string) string {
	if v != "" && v == strings.TrimSpace(v) && !strings.ContainsAny(v, ":#{}[]&*!|>'\"%@`,\n") {
		return v
	}
	b, err := yaml.Marshal(v)
	if err != nil {
		return `"` + strings.ReplaceAll(v, `"`, `\"`) + `"`
	}
	return strings.TrimRight(string(b), "\n")
}
