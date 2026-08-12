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
// distinct across the nodes that contributed it. Two mechanisms carry that.
// Output names are scoped by the contributing node, so a node's outputs cannot
// displace another node's ([OutputLogicalID]). Resource names come from the
// Recipe that authored the fragment, which this core reads and never rewrites;
// [DeclaredNames] collects them, and the guest hands them to the host as the
// names it derived, which is what turns a duplicate into a halt naming both
// contributors instead of a resource quietly displaced during the merge.
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
// declares each fragment's names ([DeclaredNames]) and the host halts on a
// duplicate before any assembly runs.
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
			if err := mergeResources(resources, m, node.NodeID); err != nil {
				return nil, err
			}
			if err := mergeExplicitOutputs(outputs, m, node.NodeID); err != nil {
				return nil, err
			}
			if err := providesToOutputs(outputs, m, node.NodeID); err != nil {
				return nil, err
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

// mergeResources folds one fragment's Resources into the assembled Resources map
// under the logical ids the fragment authored, which this core reads and never
// rewrites. Keys are walked in sorted order so the merge is deterministic. A
// fragment with no Resources contributes nothing.
//
// Two nodes authoring one logical id are refused before any fragment is merged:
// the guest declares each fragment's logical ids ([DeclaredNames]) and the host
// halts on a duplicate, naming the id and every node that authored it. Renaming
// one of them here instead would move a resource to a name nothing in the
// deployment asked for, and the renamed id can collide in turn — which is how a
// declared resource comes to be absent from the emitted template with nothing
// said about it.
func mergeResources(dst, frag map[string]any, nodeID string) error {
	raw, ok := frag["Resources"]
	if !ok {
		return nil
	}
	rmap, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("cloudformation-provider: Resources in node %q is not a mapping", nodeID)
	}
	for _, id := range sortedKeys(rmap) {
		dst[id] = rmap[id]
	}
	return nil
}

// mergeExplicitOutputs folds one fragment's explicit Outputs block into the
// assembled Outputs map, each entry scoped by the contributing node
// ([OutputLogicalID]). Keys are walked in sorted order so the merge is
// deterministic.
func mergeExplicitOutputs(dst, frag map[string]any, nodeID string) error {
	raw, ok := frag["Outputs"]
	if !ok {
		return nil
	}
	omap, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("cloudformation-provider: Outputs is not a mapping")
	}
	for _, k := range sortedKeys(omap) {
		dst[OutputLogicalID(nodeID, k)] = omap[k]
	}
	return nil
}

// providesToOutputs projects a fragment's provides entries into stack Outputs.
// Each provides key becomes an Output, named for the contributing node and the
// key together ([OutputLogicalID]), whose Value is the provides value; a value
// that is an intrinsic referencing a resource in the assembled stack rides
// through as its long-form intrinsic. Keys are walked in sorted order for
// deterministic output.
func providesToOutputs(dst, frag map[string]any, nodeID string) error {
	raw, ok := frag["provides"]
	if !ok {
		return nil
	}
	pmap, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("cloudformation-provider: provides is not a mapping")
	}
	for _, k := range sortedKeys(pmap) {
		dst[OutputLogicalID(nodeID, k)] = map[string]any{"Value": pmap[k]}
	}
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

// DeclaredNames reads one already-normalized fragment and returns the names the
// assembled stack will address its content by: the logical ids of its resources,
// and the logical ids of the outputs its provides and explicit Outputs entries
// project to. The guest hands these to the host, which refuses a name two
// contributors to one assembled stack both claim.
//
// Resource ids come back exactly as the fragment authored them, because that is
// what the merge emits. Output ids come back already scoped by the node, because
// that is what the merge emits for them. Both are returned in sorted order so a
// declaration is a function of the fragment alone.
//
// A fragment that carries neither section declares nothing, which is a valid
// answer rather than an error: a fragment contributing only vendored content has
// no name for the host to check.
func DeclaredNames(nodeID string, fragment []byte) (resourceIDs, outputIDs []string, err error) {
	v, err := decodeToValue(fragment)
	if err != nil {
		return nil, nil, err
	}
	if v == nil {
		return nil, nil, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("cloudformation-provider: fragment from node %q is not a mapping", nodeID)
	}

	if raw, present := m["Resources"]; present {
		rmap, ok := raw.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("cloudformation-provider: Resources in node %q is not a mapping", nodeID)
		}
		resourceIDs = append(resourceIDs, sortedKeys(rmap)...)
	}
	for _, section := range []string{"Outputs", "provides"} {
		raw, present := m[section]
		if !present {
			continue
		}
		omap, ok := raw.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("cloudformation-provider: %s in node %q is not a mapping", section, nodeID)
		}
		for _, k := range sortedKeys(omap) {
			outputIDs = append(outputIDs, OutputLogicalID(nodeID, k))
		}
	}
	sort.Strings(outputIDs)
	return resourceIDs, outputIDs, nil
}

// DeclaredNamesSourced collects the declared names of a sourced source's packed
// files, reading the same files DeclaredNames would and skipping the same
// non-template content NormalizeSourced vendors verbatim — a directory source's
// README or parameters file carries no name the assembled stack addresses
// anything by. A single-file source's lone template is read whole. Names come
// back in the packed order the caller supplied, each file's own names sorted.
func DeclaredNamesSourced(nodeID string, files []SourcedFile, rootIsDir bool) (resourceIDs, outputIDs []string, err error) {
	for _, f := range files {
		if rootIsDir && !isTemplateFile(f.Path) {
			continue
		}
		res, outs, err := DeclaredNames(nodeID, f.Content)
		if err != nil {
			return nil, nil, fmt.Errorf("cloudformation-provider: read declared names from sourced template %q: %w", f.Path, err)
		}
		resourceIDs = append(resourceIDs, res...)
		outputIDs = append(outputIDs, outs...)
	}
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
