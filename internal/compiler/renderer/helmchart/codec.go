// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmchart

import (
	"encoding/json"
	"fmt"
	"strings"

	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
)

// helmChartMediaType tags a unit the helm-chart codec carved from a chart
// template file — a structural YAML scaffold with template actions blanked to
// placeholders.
const helmChartMediaType = "application/vnd.helm.chart.template"

// holeSentinelPrefix and holeSentinelSuffix bracket the opaque placeholder a
// template action is blanked to so the scaffold parses as YAML. The bracketing
// is chosen so the placeholder is a plain YAML scalar in value, key, and
// list-item positions (it survives parse→re-emit without quoting), and so it is
// improbable in real chart source; a collision with existing bytes halts loudly
// rather than risking a corrupt restore.
const (
	holeSentinelPrefix = "__AGGREGA_HOLE_"
	holeSentinelSuffix = "__"
)

// structureEmittingFuncs are template functions whose output is multi-line or
// structural — an action that uses one does not occupy a single scalar value
// even when the blanked scaffold parses it as one (the `include … | nindent`
// shape). A value-position hole that invokes one of these is NOT a safe PATCH
// target, so the codec declines a tree for the document carrying it. WHICH
// functions count is the provider's call (the core stays dumb): this list is
// helm-chart's declared boundary between a patchable value hole and a
// structural injection, not a core inference about meaning.
var structureEmittingFuncs = []string{
	"nindent", "indent", "include", "toYaml", "toYAML", "tpl", "fromYaml", "fromYAML",
}

// HelmChartCodec is the helm-chart provider's codec override: a
// structure-with-holes [frp.Codec] for chart source. It carves chart files
// without rendering them (AOT-pure — it edits chart source; Helm renders at
// deploy time):
//
//   - A template file (one carrying Go-template actions `{{ … }}`) is tokenized
//     into action spans versus structural scaffold; each action is blanked to an
//     opaque placeholder scalar so the scaffold parses as YAML. The scaffold is
//     treed and patched through the stock node-span YAML codec; on serialize the
//     placeholders are restored to the original action bytes verbatim. A
//     document whose structure is indeterminate at compile time — a control-flow
//     region (`{{ if }}`/`{{ range }}`/`{{ define }}`), an interpolated scalar
//     mixing literal text with an action, or a structure-emitting injection
//     (`include … | nindent`) — is carved as an OPAQUE unit (no tree): a
//     SELECT/EMIT target, never an RFC-6902 PATCH target. A PATCH against it
//     halts loudly through the core's not-structured guardrail; the operator
//     patches values.yaml instead.
//   - A non-template file (values.yaml, Chart.yaml — no `{{ … }}`) is delegated
//     to the stock YAML codec unchanged, so anchors, comments, and scalar styles
//     are preserved by the same node-span surgery flat providers use.
//
// The codec composes the stock YAML codec for all node-span structural work
// rather than re-implementing it; the only helm-specific logic is the
// blank/restore of template actions and the value-versus-control-flow
// classification.
type HelmChartCodec struct {
	stock  frp.Codec
	indent int
}

// NewHelmChartCodec returns the helm-chart structure-with-holes codec at the
// configured indent width. It borrows the core's stock YAML codec for all
// node-span structural carving and surgery.
func NewHelmChartCodec(indent int) frp.Codec {
	return HelmChartCodec{stock: rendercore.NewStockYAMLCodec(indent), indent: indent}
}

// Segment carves one chart file into addressable units. A non-template file is
// delegated to the stock YAML codec verbatim. A template file is blanked to a
// parseable scaffold, segmented through the stock codec, then each unit is
// reclassified against its holes: a unit whose every hole is a clean
// value-position scalar keeps its scaffold tree (a PATCH target); a unit with a
// control-flow, interpolated, or structure-emitting hole — or a scaffold
// document the YAML parser still rejects — is downgraded to opaque (no tree).
// Every unit's Bytes are the ORIGINAL chart bytes (the guest's view is the real
// source, never the blanked scaffold).
func (c HelmChartCodec) Segment(name string, body []byte) ([]frp.Unit, error) {
	acts, err := findActions(body)
	if err != nil {
		return nil, fmt.Errorf("helm-chart codec: tokenize %q: %w", name, err)
	}
	if len(acts) == 0 {
		// No template actions: a values.yaml / Chart.yaml-shaped file. Stock
		// YAML codec handles it, preserving anchors/comments/styles.
		return c.stock.Segment(name, body)
	}

	scaffold, holes, err := blank(body, acts)
	if err != nil {
		return nil, fmt.Errorf("helm-chart codec: blank %q: %w", name, err)
	}

	scaffoldUnits, err := c.stock.Segment(name, []byte(scaffold))
	if err != nil {
		// The blanked scaffold is unparseable as a whole: the file carries a
		// control-flow region somewhere, so its document structure is
		// indeterminate at compile time. The honest unit is one OPAQUE unit over
		// the whole file (the guest's view is the real source; a PATCH halts
		// loudly; the operator patches values.yaml instead).
		return []frp.Unit{{Token: name, Bytes: append([]byte(nil), body...), MediaType: helmChartMediaType}}, nil
	}

	// Each scaffold unit's bytes restore to its original document bytes through
	// the hole table — this single-sources the scaffold↔original byte mapping
	// rather than re-splitting the original (which the parser rejects when an
	// action sits in a structural position, e.g. a colon inside a value action).
	// The guest's view is the real source: a unit's Bytes are the restored
	// original, never the blanked scaffold.
	out := make([]frp.Unit, 0, len(scaffoldUnits))
	for _, su := range scaffoldUnits {
		u := frp.Unit{
			Token:     su.Token,
			Bytes:     []byte(restore(string(su.Bytes), holes)),
			MediaType: helmChartMediaType,
		}
		// A document the stock codec could not tree stays opaque. A treed
		// document is patchable only when every hole inside it is a clean
		// value-position scalar; any control-flow/interpolated/structure-emitting
		// hole downgrades it to opaque.
		if su.Structured() && holesAreCleanValues(su.Tree, holes) {
			u.Tree = su.Tree
		}
		out = append(out, u)
	}
	return out, nil
}

// Reassemble rebuilds one chart file from its segmentation, splicing patched
// units back surgically and restoring template actions verbatim. A non-template
// file is delegated to the stock codec. A template file is re-blanked
// deterministically (blanking is a pure function of the bytes), the patched
// units are spliced into the scaffold by the stock node-span codec, and the
// placeholders are then restored to their original action bytes: a hole the
// guest left untouched comes back verbatim; a hole the guest overwrote with a
// literal stays overwritten (its placeholder no longer appears in the patched
// scaffold).
func (c HelmChartCodec) Reassemble(name string, body []byte, segs []frp.Unit, patched []frp.PatchedUnit) ([]byte, []string, error) {
	if len(patched) == 0 {
		return append([]byte(nil), body...), nil, nil
	}

	acts, err := findActions(body)
	if err != nil {
		return nil, nil, fmt.Errorf("helm-chart codec: tokenize %q: %w", name, err)
	}
	if len(acts) == 0 {
		return c.stock.Reassemble(name, body, segs, patched)
	}

	scaffold, holes, err := blank(body, acts)
	if err != nil {
		return nil, nil, fmt.Errorf("helm-chart codec: blank %q: %w", name, err)
	}

	// Re-segment the scaffold so the stock codec edits the scaffold bytes (its
	// byte spans are the scaffold's, not the original's).
	scaffoldUnits, err := c.stock.Segment(name, []byte(scaffold))
	if err != nil {
		return nil, nil, fmt.Errorf("helm-chart codec: re-segment scaffold %q: %w", name, err)
	}
	editedScaffold, warnings, err := c.stock.Reassemble(name, []byte(scaffold), scaffoldUnits, patched)
	if err != nil {
		return nil, nil, err
	}

	return []byte(restore(string(editedScaffold), holes)), warnings, nil
}

// action is one Go-template action span in a chart file's bytes: the half-open
// byte range [start,end) and the original action bytes verbatim (delimiters and
// any `{{- … -}}` whitespace trims included).
type action struct {
	start, end int
	text       string
}

// findActions scans body for every Go-template action (`{{ … }}`) and returns
// the spans in source order. The scan is delimiter-aware and brace-balanced: it
// honors quoted strings inside an action (a `}}` inside a quote does not end the
// action) and balances nested `{`/`}` (a dict literal `{{ dict "a" 1 }}` closes
// at the right `}}`), and tolerates `{{- … -}}` trims. An unterminated action or
// quote returns an error rather than guessing a boundary — the caller surfaces
// it rather than risking a corrupt blank/restore.
func findActions(body []byte) ([]action, error) {
	var out []action
	i := 0
	for i < len(body) {
		j := indexFrom(body, i, "{{")
		if j < 0 {
			break
		}
		k, err := scanActionEnd(body, j)
		if err != nil {
			return nil, fmt.Errorf("at byte %d: %w", j, err)
		}
		out = append(out, action{start: j, end: k, text: string(body[j:k])})
		i = k
	}
	return out, nil
}

// indexFrom returns the index of sub in body at or after from, or -1.
func indexFrom(body []byte, from int, sub string) int {
	idx := strings.Index(string(body[from:]), sub)
	if idx < 0 {
		return -1
	}
	return from + idx
}

// scanActionEnd returns the offset just past the closing `}}` of the action
// that starts at start (body[start:start+2] == "{{"). It skips quoted strings
// (double-quote with backslash escapes, single-quote and backtick literal) so a
// `}}` inside a quote is not mistaken for the closer, and tracks nested `{`/`}`
// depth so an inner brace pair does not close the action early.
func scanActionEnd(body []byte, start int) (int, error) {
	n := len(body)
	p := start + 2 // past the opening "{{"
	depth := 0     // nesting of single "{" beyond the opening "{{"
	for p < n {
		switch body[p] {
		case '"':
			q := skipQuoted(body, p, '"')
			if q < 0 {
				return 0, fmt.Errorf("unterminated double-quoted string in template action")
			}
			p = q
		case '\'':
			q := skipQuoted(body, p, '\'')
			if q < 0 {
				return 0, fmt.Errorf("unterminated single-quoted string in template action")
			}
			p = q
		case '`':
			q := skipBacktick(body, p)
			if q < 0 {
				return 0, fmt.Errorf("unterminated backtick string in template action")
			}
			p = q
		case '}':
			if depth == 0 && p+1 < n && body[p+1] == '}' {
				return p + 2, nil
			}
			if depth > 0 {
				depth--
			}
			p++
		case '{':
			depth++
			p++
		default:
			p++
		}
	}
	return 0, fmt.Errorf("unterminated template action (no closing }})")
}

// skipQuoted returns the offset just past the closing quote of the string that
// opens at p (body[p] == quote), or -1 if unterminated. A double-quoted string
// honors backslash escapes (Go string syntax); a single-quoted string is
// literal (no escapes, matching Go template raw-character literals' nearest
// analog — a single quote ends only at the next single quote).
func skipQuoted(body []byte, p int, quote byte) int {
	n := len(body)
	p++ // past the opening quote
	for p < n {
		c := body[p]
		if quote == '"' && c == '\\' {
			p += 2
			continue
		}
		if c == quote {
			return p + 1
		}
		p++
	}
	return -1
}

// skipBacktick returns the offset just past the closing backtick of the raw
// string that opens at p (body[p] == '`'), or -1 if unterminated. A backtick
// raw string has no escapes.
func skipBacktick(body []byte, p int) int {
	n := len(body)
	p++
	for p < n {
		if body[p] == '`' {
			return p + 1
		}
		p++
	}
	return -1
}

// placeholderFor returns the opaque placeholder scalar for the i-th hole.
func placeholderFor(i int) string {
	return fmt.Sprintf("%s%d%s", holeSentinelPrefix, i, holeSentinelSuffix)
}

// blank replaces every action span with its placeholder scalar, returning the
// scaffold and the hole table (index → original action bytes). It refuses to
// run when the placeholder sentinel already appears in the source — a collision
// would make restore (a placeholder→action substitution) ambiguous — surfacing
// a loud error rather than silently picking another sentinel.
func blank(body []byte, acts []action) (string, []string, error) {
	if strings.Contains(string(body), holeSentinelPrefix) {
		return "", nil, fmt.Errorf("template-action placeholder sentinel %q already present in source; cannot safely blank holes", holeSentinelPrefix)
	}
	var sb strings.Builder
	holes := make([]string, len(acts))
	prev := 0
	for i, a := range acts {
		sb.Write(body[prev:a.start])
		sb.WriteString(placeholderFor(i))
		holes[i] = a.text
		prev = a.end
	}
	sb.Write(body[prev:])
	return sb.String(), holes, nil
}

// restore substitutes each placeholder still present in scaffold back to its
// original action bytes verbatim. A placeholder a guest patched away (replaced
// with a literal) is absent, so its action is dropped — the guest's
// intentional overwrite stands. Substitution is by exact placeholder string,
// never by byte offset, so a structural edit that shifted bytes elsewhere does
// not disturb an untouched hole.
func restore(scaffold string, holes []string) string {
	out := scaffold
	for i, h := range holes {
		out = strings.Replace(out, placeholderFor(i), h, 1)
	}
	return out
}

// holesAreCleanValues reports whether every placeholder reachable in a scaffold
// document's tree occupies a clean value position: it is the ENTIRE value of its
// scalar node (not interpolated into a larger scalar) AND its original action
// does not invoke a structure-emitting function. A hole that fails either test
// makes the document's compile-time structure indeterminate, so the codec
// declines a tree for it (the unit becomes opaque). A scalar that carries no
// placeholder is ordinary structural YAML and is ignored.
func holesAreCleanValues(tree json.RawMessage, holes []string) bool {
	var node any
	if err := json.Unmarshal(tree, &node); err != nil {
		return false
	}
	clean := true
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			for k, child := range t {
				// A placeholder in a KEY position is never a clean value hole — a
				// templated map key (`{{ $k }}: …`) is control flow over
				// structure, not a value the codec can address.
				if containsAnyPlaceholder(k) {
					clean = false
				}
				walk(child)
			}
		case []any:
			for _, child := range t {
				walk(child)
			}
		case string:
			switch holeStatus(t, holes) {
			case holeInterpolated, holeStructure:
				clean = false
			}
		}
	}
	walk(node)
	return clean
}

// holeStatusKind classifies how a scalar string relates to the hole table.
type holeStatusKind int

const (
	holeNone         holeStatusKind = iota // no placeholder in the scalar
	holePureValue                          // the scalar IS exactly one placeholder
	holeInterpolated                       // a placeholder is a substring of a larger scalar
	holeStructure                          // the scalar is exactly one placeholder, but its action emits structure
)

// holeStatus classifies a scalar's relationship to the hole table: a clean
// pure-value hole, an interpolated hole, a structure-emitting hole, or none.
func holeStatus(scalar string, holes []string) holeStatusKind {
	for i := range holes {
		ph := placeholderFor(i)
		if scalar == ph {
			if actionEmitsStructure(holes[i]) {
				return holeStructure
			}
			return holePureValue
		}
		if strings.Contains(scalar, ph) {
			return holeInterpolated
		}
	}
	return holeNone
}

// containsAnyPlaceholder reports whether s carries any hole placeholder.
func containsAnyPlaceholder(s string) bool {
	return strings.Contains(s, holeSentinelPrefix)
}

// actionEmitsStructure reports whether a template action invokes a function
// that produces multi-line / structural output (the `include … | nindent`
// family). Such an action does not occupy a single scalar value even when the
// blanked scaffold parses it as one, so a document carrying it is not a safe
// PATCH target. The function set is helm-chart's declared classification, not a
// core inference.
func actionEmitsStructure(actionText string) bool {
	inner := strings.TrimPrefix(actionText, "{{")
	inner = strings.TrimSuffix(inner, "}}")
	inner = strings.TrimSpace(inner)
	inner = strings.TrimPrefix(inner, "-")
	inner = strings.TrimSuffix(inner, "-")
	for _, fn := range structureEmittingFuncs {
		if containsWord(inner, fn) {
			return true
		}
	}
	return false
}

// containsWord reports whether word appears in s delimited by a non-identifier
// character (or string boundary) on each side, so "indent" does not match
// inside "nindent" and a function name does not match inside an unrelated
// identifier. Identifier characters are ASCII letters, digits, and underscore.
func containsWord(s, word string) bool {
	from := 0
	for {
		idx := strings.Index(s[from:], word)
		if idx < 0 {
			return false
		}
		i := from + idx
		beforeOK := i == 0 || !isIdentByte(s[i-1])
		after := i + len(word)
		afterOK := after >= len(s) || !isIdentByte(s[after])
		if beforeOK && afterOK {
			return true
		}
		from = i + len(word)
	}
}

func isIdentByte(b byte) bool {
	return b == '_' ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9')
}

// ensure the codec satisfies the interface at compile time.
var _ frp.Codec = HelmChartCodec{}
