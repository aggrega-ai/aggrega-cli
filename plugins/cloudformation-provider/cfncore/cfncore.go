// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package cfncore is the filesystem-free CloudFormation domain core the
// cloudformation-provider Wasm guest runs: it normalizes CloudFormation
// fragments and sourced CloudFormation templates to pure-JSON long-form
// intrinsics, structurally validates a sourced template, and merges a dispatch
// group's per-node fragments into a single CloudFormation stack template. It
// holds all the CloudFormation-specific intelligence — short-form intrinsic
// expansion, per-fragment resource merging, output-name scoping, the
// provides-to-Outputs projection, and sourced-template parse validation — so
// the Wasm host that orchestrates the guest stays blind to CloudFormation
// semantics.
//
// A logical id is unique within one template, and several nodes assemble into
// one stack, so every name a merged template addresses something by has to be
// distinct across the nodes that contributed it. Three mechanisms carry that.
// Output names are scoped by the contributing node, so a node's outputs cannot
// displace another node's ([OutputLogicalID]). Resource names come from the
// Recipe that authored the fragment, which this core reads and never rewrites;
// [DeclaredNames] collects them across everything the node contributes, and the
// guest hands them to the host as the names it derived, which is what turns a
// duplicate across contributors into a halt naming both instead of a resource
// quietly displaced during the merge. And the merge refuses a name the assembled
// stack already carries with different content, which is what a declaration
// cannot do: one contributor claiming a name twice across its own fragments is
// recorded in that node's declaration as a repeated name, and refusing it is the
// merge's to do, since there is no second contributor to name.
//
// The core carries no build constraint so it compiles for both the wasip1 guest
// and the host test binary: the guest's exports call into it, and its behavior
// is exercised directly by host tests without a Wasm runtime. Output is
// deterministic: every emitted object serializes with sorted keys, and every
// walk over a fragment's keys takes them in sorted order, so identical input
// yields byte-identical output across runs.
package cfncore

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/pkg/anchorcycle"
	"github.com/awslabs/goformation/v7/intrinsics"
	"go.yaml.in/yaml/v3"
)

// StackTemplateName is the file name the assembled CloudFormation stack template
// lands at within a dispatch group's output. A dispatch group folds its per-node
// fragments into this one template, so the name is fixed per group rather than
// derived from any node.
const StackTemplateName = "cloudformation-stack.json"

// templateFormatVersion is the CloudFormation template format version stamped on
// every assembled stack template.
const templateFormatVersion = "2010-09-09"

// NodeFragment is one node's contribution to a dispatch group's assembled stack:
// the node id and the serialized bytes of each per-node fragment the render pass
// produced for it. The node id scopes the output names the fragment's provides
// and Outputs entries land under, so two nodes contributing the same entry name
// address separate outputs.
type NodeFragment struct {
	NodeID string
	Files  [][]byte
}

// Key spaces the guest declares its derived names into. Each is a name this core
// chose for one kind of name a CloudFormation template addresses something by;
// the host compares names for equality within one space and reads no meaning
// into either the space or the names, so these strings only ever have to be
// distinct from each other and stable across runs.
const (
	// KeySpaceResourceLogicalIDs holds the logical ids of the resources the
	// assembled stack carries.
	KeySpaceResourceLogicalIDs = "resource-logical-ids"
	// KeySpaceOutputLogicalIDs holds the logical ids of the outputs the
	// assembled stack carries.
	KeySpaceOutputLogicalIDs = "output-logical-ids"
)

// NormalizeFragment parses one CloudFormation fragment — a YAML or JSON object
// carrying a Resources map and optionally provides/Outputs material — and
// re-emits it as pure-JSON long-form intrinsics. YAML short-form intrinsic tags
// (!Ref, !GetAtt, !Sub, and the rest) are expanded to their long-form object
// spellings ({"Ref": ...}, {"Fn::GetAtt": [...]}); a fragment already in JSON
// long form passes through unchanged in meaning. The output serializes with
// sorted object keys so it is byte-identical across runs of identical input.
func NormalizeFragment(content []byte) ([]byte, error) {
	v, err := decodeToValue(content)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, fmt.Errorf("cloudformation-provider: fragment is empty")
	}
	return json.Marshal(v)
}

// SourcedFile is one packed file of a sourced CloudFormation source: its
// root-relative path and raw bytes. It mirrors the shape the pack-source host
// function returns so the guest hands its packed projection straight to the core.
type SourcedFile struct {
	Path    string
	Content []byte
}

// NormalizeSourcedTemplate normalizes one sourced CloudFormation template to
// pure-JSON long-form intrinsics. The template's YAML short-form intrinsic tags
// (!Ref, !GetAtt, !Sub, and the rest) expand to their long-form object spellings
// ({"Ref": ...}, {"Fn::GetAtt": ...}) and the document re-emits as JSON with sorted
// keys, so identical input yields byte-identical output across runs. No intrinsic is
// evaluated: a Ref or GetAtt survives as its long-form pointer object rather than
// being resolved to a value, so the template's deterministic references carry
// through untouched and nothing depends on runtime state. A template that does not
// parse is a loud error naming the parse failure.
func NormalizeSourcedTemplate(content []byte) ([]byte, error) {
	out, err := normalizeTemplateBytes(content)
	if err != nil {
		return nil, fmt.Errorf("cloudformation-provider: parse sourced template: %w", err)
	}
	return out, nil
}

// NormalizeSourced normalizes a sourced CloudFormation source's packed files to
// pure-JSON long-form intrinsics, preserving the packed order and each file's path.
// A single-file source (rootIsDir false) is a lone CloudFormation template whose one
// file normalizes whole. A directory source normalizes every CloudFormation template
// file — identified by extension — and vendors any other file (a README, a
// parameters file, an unrelated asset) verbatim, so the source's non-template content
// survives untouched. A template file that does not parse halts loudly, naming the
// file.
func NormalizeSourced(files []SourcedFile, rootIsDir bool) ([]SourcedFile, error) {
	out := make([]SourcedFile, 0, len(files))
	for _, f := range files {
		if rootIsDir && !isTemplateFile(f.Path) {
			out = append(out, f)
			continue
		}
		normalized, err := normalizeTemplateBytes(f.Content)
		if err != nil {
			return nil, fmt.Errorf("cloudformation-provider: normalize sourced template %q: %w", f.Path, err)
		}
		out = append(out, SourcedFile{Path: f.Path, Content: normalized})
	}
	return out, nil
}

// ValidateSourced structurally parses a sourced CloudFormation source's packed files
// and returns one diagnostic per parse finding, in the packed order. A single-file
// source's lone template is parsed whole; a directory source parses every
// CloudFormation template file (by extension) and leaves any other file untouched. It
// interprets nothing beyond the template's syntactic shape — it does not resolve
// intrinsics, evaluate conditions, contact AWS, or run any CloudFormation toolchain;
// it confirms the template parses. A template that fails to parse yields an
// error-severity diagnostic naming the file, which halts the build at the host's
// input gate. An empty result means every template parsed cleanly.
// templateParseCode is the machine-matchable code every diagnostic reporting a
// CloudFormation template that failed to parse carries.
const templateParseCode = "cloudformation-template-parse"

func ValidateSourced(files []SourcedFile, rootIsDir bool) []renderctx.Diagnostic {
	var diags []renderctx.Diagnostic
	for _, f := range files {
		if rootIsDir && !isTemplateFile(f.Path) {
			continue
		}
		if _, err := normalizeTemplateBytes(f.Content); err != nil {
			diags = append(diags, renderctx.Diagnostic{
				Severity: "error",
				Code:     templateParseCode,
				Message:  fmt.Sprintf("sourced CloudFormation template %q failed to parse: %v", f.Path, err),
				Location: &renderctx.DiagnosticLocation{Kind: "cloudformation", URI: f.Path},
			})
		}
	}
	return diags
}

// normalizeTemplateBytes converts one CloudFormation template's YAML or JSON bytes to
// pure-JSON long-form intrinsics without evaluating any intrinsic: short-form tags
// expand to their long-form object spellings and the document re-emits as sorted-key
// JSON with a trailing newline. Ref, GetAtt, and every other intrinsic pass through as
// their long-form pointer objects, never resolved, so the conversion is
// ahead-of-time-pure and deterministic across runs.
func normalizeTemplateBytes(content []byte) ([]byte, error) {
	out, err := intrinsics.ProcessYAML(content, &intrinsics.ProcessorOptions{NoProcess: true})
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// isTemplateFile reports whether a packed file's path names a CloudFormation template
// by its extension. A directory source normalizes only these files and vendors any
// other verbatim.
func isTemplateFile(path string) bool {
	switch {
	case strings.HasSuffix(path, ".yaml"),
		strings.HasSuffix(path, ".yml"),
		strings.HasSuffix(path, ".json"),
		strings.HasSuffix(path, ".template"):
		return true
	default:
		return false
	}
}

// AssembleStack merges every node's fragments in dispatch-group order into one
// CloudFormation stack template and returns its pure-JSON long-form bytes. Each
// fragment's Resources fold into one Resources map under the logical ids the
// fragment authored. Each fragment's provides entries become stack Outputs (a
// provides value that is an intrinsic referencing a resource in the assembled
// stack rides through as its long-form intrinsic — the intra-stack
// cross-reference), and any explicit Outputs block merges in as well; both are
// named for the contributing node and the entry together ([OutputLogicalID]), so
// two nodes exporting the same entry name reach separate outputs. The assembled
// template is validated with the standard-library JSON decoder: it must be
// well-formed JSON carrying a non-empty Resources object, or the assembly halts
// loudly. Cross-stack wiring is not performed here; a provides value pointing
// outside the assembled stack is piped through as whatever intrinsic the fragment
// authored.
//
// Two nodes authoring one resource logical id never reach this merge: the guest
// declares each node's names ([DeclaredNames]) and the host halts on a duplicate
// before any assembly runs. What still reaches it is one contributor authoring a
// name twice across its own fragments, which the merge refuses itself — see
// [insertEntry].
func AssembleStack(nodes []NodeFragment) ([]byte, error) {
	resources := map[string]any{}
	outputs := map[string]any{}

	for _, node := range nodes {
		for _, raw := range node.Files {
			frag, err := decodeToValue(raw)
			if err != nil {
				return nil, err
			}
			m, ok := frag.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("cloudformation-provider: fragment from node %q is not a mapping", node.NodeID)
			}
			res, outs, err := fragmentEntries(node.NodeID, m)
			if err != nil {
				return nil, err
			}
			for _, e := range res {
				if err := insertEntry(resources, KeySpaceResourceLogicalIDs, node.NodeID, e); err != nil {
					return nil, err
				}
			}
			for _, e := range outs {
				if err := insertEntry(outputs, KeySpaceOutputLogicalIDs, node.NodeID, e); err != nil {
					return nil, err
				}
			}
		}
	}

	if len(resources) == 0 {
		return nil, fmt.Errorf("cloudformation-provider: assembled stack carries no Resources")
	}

	stack := map[string]any{
		"AWSTemplateFormatVersion": templateFormatVersion,
		"Resources":                resources,
	}
	if len(outputs) > 0 {
		stack["Outputs"] = outputs
	}

	out, err := json.MarshalIndent(stack, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := validateStack(out); err != nil {
		return nil, err
	}
	out = append(out, '\n')
	return out, nil
}

// stackEntry is one name the assembled stack addresses a fragment's content by,
// paired with the content that lands under it.
type stackEntry struct {
	key   string
	value any
}

// fragmentEntries reads one decoded fragment and returns what the assembled
// stack carries for it, per key space: its resources under the logical ids the
// fragment authored, which this core reads and never rewrites, and its outputs —
// an explicit Outputs block's entries, then the entries its provides keys
// project to — each named for the contributing node and the entry together
// ([OutputLogicalID]). A provides value that is an intrinsic referencing a
// resource in the assembled stack rides through as its long-form intrinsic. A
// fragment carrying none of the three sections yields nothing, which is a valid
// answer rather than an error.
//
// This is the single derivation of which names a fragment claims. The merge
// inserts these entries and [DeclaredNames] gathers them across a node's
// fragments and reports their keys, so the set a node declares to the host is by
// construction the set the merge writes. Two walks kept in step by eye could
// drift instead, and the host would then be checking one derivation for
// duplicates while the merge wrote another — a divergence nothing would fail on,
// because no comparison between the two exists to fail.
//
// A fragment reaches one key space through more than one section, so it can name
// one key twice: an explicit Outputs block and the projected provides entries
// both land in the outputs space. Those repeats are folded here, on the terms the
// merge writes them ([collapseEntries]), which is what keeps the set exact rather
// than merely overlapping.
//
// Each section's keys are walked in sorted order, and the explicit Outputs
// entries precede the projected provides entries, so the returned entries are a
// function of the fragment and the node id alone.
func fragmentEntries(nodeID string, frag map[string]any) (resources, outputs []stackEntry, err error) {
	if raw, present := frag["Resources"]; present {
		rmap, ok := raw.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("cloudformation-provider: Resources in node %q is not a mapping", nodeID)
		}
		for _, id := range sortedKeys(rmap) {
			resources = append(resources, stackEntry{key: id, value: rmap[id]})
		}
	}
	if raw, present := frag["Outputs"]; present {
		omap, ok := raw.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("cloudformation-provider: Outputs in node %q is not a mapping", nodeID)
		}
		for _, k := range sortedKeys(omap) {
			outputs = append(outputs, stackEntry{key: OutputLogicalID(nodeID, k), value: omap[k]})
		}
	}
	if raw, present := frag["provides"]; present {
		pmap, ok := raw.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("cloudformation-provider: provides in node %q is not a mapping", nodeID)
		}
		for _, k := range sortedKeys(pmap) {
			outputs = append(outputs, stackEntry{key: OutputLogicalID(nodeID, k), value: map[string]any{"Value": pmap[k]}})
		}
	}
	return collapseEntries(resources), collapseEntries(outputs), nil
}

// collapseEntries folds a name stated more than once with the same body into one
// entry, keeping each surviving entry where it first appeared, and keeps a repeat
// whose body differs from one already recorded under that name.
//
// The merge holds one value per name, so a name restated with the same body
// reaches the assembled stack once. Folding it here is what makes the names a
// contributor claims exactly the names the stack carries for it, rather than
// merely a superset: a reader of the names alone counts what it is given, and a
// repeat that the stack does not carry reads as a collision that never happened.
//
// A repeat whose bodies differ is kept, and that is the whole reason the fold is
// scoped by body rather than by name. The merge refuses such a contributor
// ([insertEntry]) because one of the two bodies would be lost; the repeated name
// is what makes that loss visible to anyone holding the names without the bodies.
//
// Applied at both the scopes a name can repeat at — within one fragment, where
// two sections reach one key space, and across the fragments of one node, which
// is the scope a declaration speaks for — and to every key space rather than to
// the one that can repeat today, so the guarantee rests on the fold rather than
// on how many sections or fragments happen to feed a space.
func collapseEntries(in []stackEntry) []stackEntry {
	if len(in) < 2 {
		return in
	}
	out := make([]stackEntry, 0, len(in))
	kept := make(map[string][]any, len(in))
next:
	for _, e := range in {
		for _, body := range kept[e.key] {
			if sameBody(body, e.value) {
				continue next
			}
		}
		kept[e.key] = append(kept[e.key], e.value)
		out = append(out, e)
	}
	return out
}

// sameBody reports whether two bodies landing under one name are the same thing,
// so restating that name displaces nothing.
//
// One predicate answers that question for both the fold above and the merge's
// refusal, so what a fragment declares and what the assembled stack carries
// cannot come to disagree about which repeats are lossy. Two copies would agree
// today and drift the moment either side's notion of identical content moved.
func sameBody(a, b any) bool {
	return reflect.DeepEqual(a, b)
}

// insertEntry lands one entry in an assembled key space, refusing a name the
// space already carries with different content.
//
// A name in one of these spaces addresses exactly one thing — a logical id names
// one resource, an output name one output — so the space holds one value per
// name. Writing a second, different value under a name already taken is not a
// merge: it displaces what was there, and the displaced resource leaves the
// stack with nothing said about it, so a deployment that declared it gets a
// template that does not carry it. Refusing is what turns that into something
// the author can see and fix.
//
// Restating a name with content identical to what the space already carries
// displaces nothing, so it proceeds. Both bodies are decoded and in hand here,
// which is what lets the refusal be scoped to the case that actually loses
// something rather than to every repeat of a name.
//
// Collecting many distinct names from many fragments is the ordinary case and is
// untouched: a fragment's every name lands, and several fragments per node is
// the design — a dispatch group folds all of them into one template.
func insertEntry(dst map[string]any, space, nodeID string, e stackEntry) error {
	if existing, taken := dst[e.key]; taken && !sameBody(existing, e.value) {
		return fmt.Errorf("cloudformation-provider: in key space %q node %q contributes the name %q, which the assembled stack already carries with different content; change what one of them is named so each name addresses one thing", space, nodeID, e.key)
	}
	dst[e.key] = e.value
	return nil
}

// validateStack confirms the assembled bytes are well-formed JSON carrying a
// non-empty Resources object, using only the standard-library JSON decoder. A
// malformed document, a missing Resources key, or an empty Resources object is a
// loud failure.
func validateStack(b []byte) error {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(b, &top); err != nil {
		return fmt.Errorf("cloudformation-provider: assembled stack is not valid JSON: %w", err)
	}
	res, ok := top["Resources"]
	if !ok {
		return fmt.Errorf("cloudformation-provider: assembled stack has no Resources")
	}
	var rm map[string]json.RawMessage
	if err := json.Unmarshal(res, &rm); err != nil {
		return fmt.Errorf("cloudformation-provider: assembled stack Resources is not a JSON object: %w", err)
	}
	if len(rm) == 0 {
		return fmt.Errorf("cloudformation-provider: assembled stack Resources is empty")
	}
	return nil
}

// decodeToValue parses YAML or JSON content into a plain Go value tree, expanding
// CloudFormation short-form intrinsic tags to their long-form object spellings on
// the way. Maps decode to map[string]any, sequences to []any, and scalars to
// their native Go types, so a later json.Marshal emits sorted-key deterministic
// JSON. Empty content decodes to a nil value.
func decodeToValue(content []byte) (any, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		return nil, fmt.Errorf("cloudformation-provider: parse fragment: %w", err)
	}
	// Parsing does not expand aliases, so a fragment whose anchor is aliased
	// inside its own content parses cleanly here and only fails when nodeToValue
	// follows the alias links. The refusal therefore sits between the parse and
	// the walk. Depth alone is never refused: a deep fragment converts normally.
	if anchor, found := anchorcycle.Find(&root); found {
		return nil, fmt.Errorf("cloudformation-provider: parse fragment: %s", anchorcycle.Explain(anchor))
	}
	if root.Kind == 0 || len(root.Content) == 0 {
		return nil, nil
	}
	return nodeToValue(root.Content[0])
}

// nodeToValue converts one YAML node to a plain Go value, resolving aliases and
// expanding a CloudFormation short-form intrinsic tag into its long-form object
// spelling.
//
// It follows alias links without tracking where it has been, so it terminates
// only on a tree whose anchors are free of recursion. decodeToValue establishes
// that with anchorcycle.Find before calling; the check is not repeated here,
// because a guarantee that has one home cannot drift out of agreement with
// itself.
func nodeToValue(n *yaml.Node) (any, error) {
	if n.Kind == yaml.AliasNode {
		if n.Alias == nil {
			return nil, fmt.Errorf("cloudformation-provider: unresolved YAML alias")
		}
		return nodeToValue(n.Alias)
	}
	if isCustomTag(n.Tag) {
		return intrinsicFromNode(n)
	}
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil, nil
		}
		return nodeToValue(n.Content[0])
	case yaml.MappingNode:
		return mappingToValue(n)
	case yaml.SequenceNode:
		return sequenceToValue(n)
	case yaml.ScalarNode:
		return scalarToValue(n), nil
	default:
		return nil, fmt.Errorf("cloudformation-provider: unsupported YAML node kind %d", n.Kind)
	}
}

// mappingToValue converts a YAML mapping node to a map[string]any, recursing into
// each value. Long-form intrinsic objects (a mapping whose sole key is "Ref" or
// "Fn::*") are ordinary mappings and pass through unchanged.
func mappingToValue(n *yaml.Node) (map[string]any, error) {
	out := make(map[string]any, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		key := n.Content[i].Value
		v, err := nodeToValue(n.Content[i+1])
		if err != nil {
			return nil, err
		}
		out[key] = v
	}
	return out, nil
}

// sequenceToValue converts a YAML sequence node to a []any, recursing into each
// item.
func sequenceToValue(n *yaml.Node) ([]any, error) {
	out := make([]any, 0, len(n.Content))
	for _, item := range n.Content {
		v, err := nodeToValue(item)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// scalarToValue decodes a scalar YAML node to its native Go type per its resolved
// tag — null, bool, integer, or float — falling back to the raw string for any
// tag whose value does not parse as its type and for plain strings.
func scalarToValue(n *yaml.Node) any {
	switch n.Tag {
	case "!!null":
		return nil
	case "!!bool":
		if b, err := strconv.ParseBool(n.Value); err == nil {
			return b
		}
	case "!!int":
		if i, err := strconv.ParseInt(n.Value, 10, 64); err == nil {
			return i
		}
	case "!!float":
		if f, err := strconv.ParseFloat(n.Value, 64); err == nil {
			return f
		}
	}
	return n.Value
}

// intrinsicFromNode expands a CloudFormation short-form intrinsic node into its
// long-form object spelling. !Ref and !Condition keep their bare names; !GetAtt
// splits a dotted scalar into its [logicalId, attribute] pair (or passes a
// sequence form through); every other short-form tag !Xxx becomes {"Fn::Xxx":
// <value>}. The tagged node's value is read as its natural kind, ignoring the
// custom tag.
func intrinsicFromNode(n *yaml.Node) (any, error) {
	name := strings.TrimPrefix(n.Tag, "!")
	inner, err := innerValue(n)
	if err != nil {
		return nil, err
	}
	switch name {
	case "Ref":
		return map[string]any{"Ref": inner}, nil
	case "Condition":
		return map[string]any{"Condition": inner}, nil
	case "GetAtt":
		return map[string]any{"Fn::GetAtt": getAttValue(inner)}, nil
	default:
		return map[string]any{"Fn::" + name: inner}, nil
	}
}

// innerValue reads a custom-tagged node's value as its natural kind: a
// custom-tagged scalar yields its raw string (the intrinsic argument), while a
// sequence or mapping recurses through the normal conversion so nested intrinsics
// expand too.
func innerValue(n *yaml.Node) (any, error) {
	switch n.Kind {
	case yaml.ScalarNode:
		return n.Value, nil
	case yaml.SequenceNode:
		return sequenceToValue(n)
	case yaml.MappingNode:
		return mappingToValue(n)
	default:
		return nil, fmt.Errorf("cloudformation-provider: unsupported intrinsic node kind %d", n.Kind)
	}
}

// getAttValue shapes the argument of a !GetAtt intrinsic into the long-form
// [logicalId, attribute] pair. A string is split on its first dot; a value with
// no dot yields a single-element slice; an already-sequence argument passes
// through unchanged.
func getAttValue(inner any) any {
	switch v := inner.(type) {
	case string:
		if idx := strings.Index(v, "."); idx >= 0 {
			return []any{v[:idx], v[idx+1:]}
		}
		return []any{v}
	default:
		return inner
	}
}

// isCustomTag reports whether a YAML tag is a CloudFormation short-form intrinsic
// tag — a single-bang tag such as "!GetAtt" — as opposed to a resolved standard
// tag ("!!str", "!!map") or an untagged node.
func isCustomTag(tag string) bool {
	return tag != "" && strings.HasPrefix(tag, "!") && !strings.HasPrefix(tag, "!!")
}

// OutputLogicalID names the stack Output one node's entry lands at: the node's
// identity and the entry name, each read as words and joined without a separator
// — node payments-db exporting host reaches PaymentsDbHost. A logical id admits
// only letters and digits, so there is no separator character available to keep
// the two apart; capitalising each word's first letter is what keeps the parts
// legible in the emitted template without one.
//
// Scoping every output by its node, rather than only where two nodes happen to
// collide, is what makes an output's meaning independent of who else assembles
// into the stack: one node's connection identity cannot arrive under a name
// another node also exports, and a name does not change when a second node is
// added to the boundary.
//
// This is the guest's own naming of a node, and it is deliberately not the name
// any Recipe chose for its resources: a Recipe authors whatever logical ids it
// likes and this core reads them verbatim, so nothing here may depend on what a
// Recipe wrote. The cross-stack publish and resolve reach an output through this
// same function, which is what lets them address a producer's output from the
// node and entry name alone.
func OutputLogicalID(nodeID, entry string) string {
	return capitalizedWords(nodeID) + capitalizedWords(entry)
}

// capitalizedWords reads a string as words separated by runs of anything a
// CloudFormation logical id does not admit, capitalises each word's first
// letter, and joins them. A word's remaining characters are left as authored, so
// a name that already carries capitals keeps them and two inputs differing only
// in where their separators sit stay distinguishable.
func capitalizedWords(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	atWordStart := true
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			if atWordStart {
				r -= 'a' - 'A'
			}
			b.WriteRune(r)
			atWordStart = false
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
			atWordStart = false
		default:
			atWordStart = true
		}
	}
	return b.String()
}

// nodeDeclaration gathers what one node's fragments claim, across every fragment
// the node contributes, and projects it to the names the node declares.
//
// The scope is the node because that is the scope the declaration is read at: a
// node hands over one set of names per key space, however many fragments it
// contributed. Gathering entries and folding once at the end is what makes the
// declared set the set the node's assembled content carries; gathering names
// instead, and joining them per fragment, cannot — a name two fragments restate
// with one body reaches the assembled stack once and would be declared twice.
//
// This is the one place the node-scope fold happens, and both the fragment path
// and the sourced path reach it, so the two cannot come to disagree about which
// repeats a node's declaration keeps.
type nodeDeclaration struct {
	resources []stackEntry
	outputs   []stackEntry
}

// addFragment reads one already-normalized fragment and gathers what the
// assembled stack will carry for it. A fragment carrying none of the sections a
// stack is addressed through adds nothing, which is a valid answer rather than an
// error: a fragment contributing only vendored content has no name to declare.
//
// Each fragment's outputs are ordered by name as they are gathered, and fragments
// are gathered in the order the caller supplied, so what the node declares is a
// function of its fragments and their order alone.
func (d *nodeDeclaration) addFragment(nodeID string, fragment []byte) error {
	v, err := decodeToValue(fragment)
	if err != nil {
		return err
	}
	if v == nil {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("cloudformation-provider: fragment from node %q is not a mapping", nodeID)
	}
	resources, outputs, err := fragmentEntries(nodeID, m)
	if err != nil {
		return err
	}
	sort.SliceStable(outputs, func(i, j int) bool { return outputs[i].key < outputs[j].key })
	d.resources = append(d.resources, resources...)
	d.outputs = append(d.outputs, outputs...)
	return nil
}

// names projects the gathered entries to the names the node declares per key
// space, folding a name restated with the same body ([collapseEntries]) and
// keeping a repeat whose bodies differ.
//
// The fold is by body and never by name, and that distinction is the whole of
// what the declaration is worth. A repeat the assembled stack carries once is
// noise to a reader holding names without bodies — it reads as a collision that
// never happened. A repeat whose bodies differ is a body about to be lost, and
// the repeated name is the only trace of it such a reader can act on, so folding
// by name would erase exactly the case the declaration exists to surface.
func (d *nodeDeclaration) names() (resourceIDs, outputIDs []string) {
	return entryKeys(collapseEntries(d.resources)), entryKeys(collapseEntries(d.outputs))
}

// entryKeys reads the names off a run of entries, in the order they stand.
func entryKeys(entries []stackEntry) []string {
	if len(entries) == 0 {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.key)
	}
	return out
}

// DeclaredNames reads a node's already-normalized fragments and returns the names
// the assembled stack will address that node's content by: the logical ids of its
// resources, and the logical ids of the outputs its provides and explicit Outputs
// entries project to. The guest hands these to the host, which refuses a name two
// contributors to one assembled stack both claim.
//
// Resource ids come back exactly as the fragments authored them, because that is
// what the merge emits. Output ids come back already scoped by the node, because
// that is what the merge emits for them. Each fragment's names come back ordered
// by name, and the fragments in the order the caller supplied, so a declaration
// is a function of the node's fragments alone.
//
// The whole node is read at once because the whole node is what the declaration
// speaks for. Taking one fragment at a time would leave the caller to join the
// answers, and a join cannot see across fragments: two fragments restating one
// name with the same body reach the assembled stack once, so a joined declaration
// would offer a name the stack does not carry.
//
// The names are the keys of the entries [fragmentEntries] produces, which is the
// same derivation the merge inserts — so what is declared here cannot drift from
// what the assembled stack ends up addressing its content by. For a node that
// assembles, these are exactly the names the stack carries for it: no more and no
// fewer.
//
// Two differences from the merge's own walk, both deliberate. Order: a
// declaration orders each fragment's names by name, while the merge takes each
// section in turn. And multiplicity, for a node the merge refuses: two claims on
// one name with different bodies declare that name twice, because one of the two
// bodies would be lost, and the repetition is the only trace of that a reader
// holding the names without the bodies can act on.
//
// A node whose fragments carry none of those sections declares nothing, which is
// a valid answer rather than an error.
func DeclaredNames(nodeID string, fragments [][]byte) (resourceIDs, outputIDs []string, err error) {
	var d nodeDeclaration
	for _, fragment := range fragments {
		if err := d.addFragment(nodeID, fragment); err != nil {
			return nil, nil, err
		}
	}
	resourceIDs, outputIDs = d.names()
	return resourceIDs, outputIDs, nil
}

// DeclaredNamesSourced returns the names a sourced source's packed files declare
// for one node, reading the same files DeclaredNames would and skipping the same
// non-template content NormalizeSourced vendors verbatim — a directory source's
// README or parameters file carries no name the assembled stack addresses
// anything by. A single-file source's lone template is read whole.
//
// A source's files are the node's fragments, so they are gathered and folded as
// one declaration exactly as a fragment set is: two of a directory source's
// template files restating one name with the same body reach the assembled stack
// once, and the node declares it once.
func DeclaredNamesSourced(nodeID string, files []SourcedFile, rootIsDir bool) (resourceIDs, outputIDs []string, err error) {
	var d nodeDeclaration
	for _, f := range files {
		if rootIsDir && !isTemplateFile(f.Path) {
			continue
		}
		if err := d.addFragment(nodeID, f.Content); err != nil {
			return nil, nil, fmt.Errorf("cloudformation-provider: read declared names from sourced template %q: %w", f.Path, err)
		}
	}
	resourceIDs, outputIDs = d.names()
	return resourceIDs, outputIDs, nil
}

// sortedKeys returns a map's keys in lexical order so any iteration that must be
// deterministic (resource merging, Outputs merging, name declaration) walks a
// stable order.
func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
