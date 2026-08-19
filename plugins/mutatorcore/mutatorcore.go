// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package mutatorcore holds the pure label-patch computations the embedded
// mutator guests run. A mutator reads a labels-only projection of the topology
// graph plus its own attachment inputs — a free-form settings object and a set
// of definition files — and returns JSON Patch documents that add or overwrite
// node labels and nothing else.
//
// The computations live here, outside the guest main packages, so they compile
// and run on the host toolchain and are exercised directly in tests — the guest
// main packages are built only for GOOS=wasip1 and hold nothing but the ABI
// decode, the call into this package, and the ABI encode.
//
// Byte-identical output: every function walks nodes in sorted node-ID order and
// emits per-label patches in a stable order (rules in author order, label keys
// in sorted order), so the emitted patch list is deterministic regardless of
// the guest's map iteration order. Each emitted patch is a single-operation JSON
// Patch document rooted at /nodes/<id>/labels/<key>, so the host applies it to
// its labels-only projection and rejects any path that reaches another surface.
package mutatorcore

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/plugins/parsercore"
)

// InjectorRule is one match-and-set rule the label injector evaluates: a node
// whose labels contain every key/value pair in Match receives every key/value
// pair in Set as an added or overwritten label. An empty Match matches every
// node, which expresses org-wide tagging.
type InjectorRule struct {
	Match map[string]string `json:"match"`
	Set   map[string]string `json:"set"`
}

// InjectorConfig is the label injector's rule set. It is read from the
// attachment's settings object when settings carry rules, otherwise from the
// first definition file that carries them.
type InjectorConfig struct {
	Rules []InjectorRule `json:"rules"`
}

// TaggerSettings is the selector tagger's settings object: the tag applied to a
// node whose component is absent from the mapping. An empty Default leaves such
// a node untouched.
type TaggerSettings struct {
	Default string `json:"default"`
}

// TaggerLabelKey is the label key the selector tagger writes on every tagged
// node.
const TaggerLabelKey = "tag"

// LabelInjectorPatches computes the label injector's patch list: for each node,
// in sorted node-ID order, every rule whose Match is a subset of the node's
// labels contributes its Set entries as added labels, in sorted key order. Rules
// are read from settings when settings carry a rule set, otherwise from the
// first definition file that does. A configuration with no rules yields no
// patches.
func LabelInjectorPatches(nodes map[string]abi.MutatorNode, settings json.RawMessage, sources []abi.MutatorSource) ([]json.RawMessage, error) {
	cfg, err := injectorConfig(settings, sources)
	if err != nil {
		return nil, err
	}
	var patches []json.RawMessage
	for _, id := range sortedNodeIDs(nodes) {
		labels := nodes[id].Labels
		for _, rule := range cfg.Rules {
			if !subsetMatch(labels, rule.Match) {
				continue
			}
			for _, key := range sortedKeys(rule.Set) {
				patch, err := labelPatch(id, key, rule.Set[key])
				if err != nil {
					return nil, err
				}
				patches = append(patches, patch)
			}
		}
	}
	return patches, nil
}

// SelectorTaggerPatches computes the selector tagger's patch list: for each
// node, in sorted node-ID order, it sets the tag label to the value the mapping
// assigns the node's component, or to the settings default when the component is
// absent from the mapping. A node whose component is unmapped and for which no
// default is configured is left untouched. The mapping is read from the
// attachment's definition files.
func SelectorTaggerPatches(nodes map[string]abi.MutatorNode, settings json.RawMessage, sources []abi.MutatorSource) ([]json.RawMessage, error) {
	mapping, err := taggerMapping(sources)
	if err != nil {
		return nil, err
	}
	var st TaggerSettings
	if len(settings) > 0 {
		if err := json.Unmarshal(settings, &st); err != nil {
			return nil, fmt.Errorf("decode settings: %w", err)
		}
	}
	var patches []json.RawMessage
	for _, id := range sortedNodeIDs(nodes) {
		tag, ok := mapping[nodes[id].Component]
		if !ok {
			if st.Default == "" {
				continue
			}
			tag = st.Default
		}
		patch, err := labelPatch(id, TaggerLabelKey, tag)
		if err != nil {
			return nil, err
		}
		patches = append(patches, patch)
	}
	return patches, nil
}

// injectorConfig resolves the rule set from settings first, then from the
// definition files. Settings win when they carry rules; otherwise the first
// definition file that carries a rule set is used. Both surfaces are the same
// InjectorConfig shape — settings arrive as JSON, definition files as a foreign
// text parsed into the canonical tree — so a rule set authored either way yields
// the same result.
func injectorConfig(settings json.RawMessage, sources []abi.MutatorSource) (InjectorConfig, error) {
	if len(settings) > 0 {
		var cfg InjectorConfig
		if err := json.Unmarshal(settings, &cfg); err != nil {
			return InjectorConfig{}, fmt.Errorf("decode settings: %w", err)
		}
		if len(cfg.Rules) > 0 {
			return cfg, nil
		}
	}
	for _, src := range sources {
		tree, err := yamlTree(src)
		if err != nil {
			return InjectorConfig{}, err
		}
		var cfg InjectorConfig
		if err := json.Unmarshal(tree, &cfg); err != nil {
			return InjectorConfig{}, fmt.Errorf("decode source %q: %w", src.Path, err)
		}
		if len(cfg.Rules) > 0 {
			return cfg, nil
		}
	}
	return InjectorConfig{}, nil
}

// taggerMapping merges the component-to-tag map from every definition file, in
// file order, so a later file overrides an earlier entry for the same component.
func taggerMapping(sources []abi.MutatorSource) (map[string]string, error) {
	mapping := map[string]string{}
	for _, src := range sources {
		tree, err := yamlTree(src)
		if err != nil {
			return nil, err
		}
		var m map[string]string
		if err := json.Unmarshal(tree, &m); err != nil {
			return nil, fmt.Errorf("decode source %q: %w", src.Path, err)
		}
		for k, v := range m {
			mapping[k] = v
		}
	}
	return mapping, nil
}

// yamlTree converts a definition file's content into the canonical JSON tree
// through the shared parser conversion, so the mutator guests carry no YAML
// parser of their own. An error diagnostic from the conversion surfaces as an
// error rather than a silently empty tree.
func yamlTree(src abi.MutatorSource) (json.RawMessage, error) {
	out := parsercore.ParseYAML([]byte(src.Content))
	for _, d := range out.Diagnostics {
		if d.Severity == abi.SeverityHalt {
			return nil, fmt.Errorf("parse source %q: %s", src.Path, d.Message)
		}
	}
	return out.Tree, nil
}

// subsetMatch reports whether labels contain every key/value pair in match. An
// empty match is a subset of any label set, so it matches every node.
func subsetMatch(labels, match map[string]string) bool {
	for k, v := range match {
		if labels[k] != v {
			return false
		}
	}
	return true
}

// patchOp is one JSON Patch operation. The mutator guests emit only add
// operations against the label surface.
type patchOp struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value string `json:"value"`
}

// labelPatch builds a single-operation JSON Patch document that adds one label
// to one node. The node ID and label key are escaped as JSON Pointer reference
// tokens so a key containing "/" or "~" resolves to a single label entry rather
// than a nested path.
func labelPatch(nodeID, key, value string) (json.RawMessage, error) {
	op := patchOp{
		Op:    "add",
		Path:  "/nodes/" + escapePointerToken(nodeID) + "/labels/" + escapePointerToken(key),
		Value: value,
	}
	b, err := json.Marshal([]patchOp{op})
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}

// escapePointerToken escapes a JSON Pointer reference token per RFC 6901: "~"
// becomes "~0" and "/" becomes "~1". The order matters — "~" is escaped first so
// a "/" rewritten to "~1" is not re-escaped.
func escapePointerToken(tok string) string {
	tok = strings.ReplaceAll(tok, "~", "~0")
	tok = strings.ReplaceAll(tok, "/", "~1")
	return tok
}

// sortedNodeIDs returns the node IDs in sorted order so the emitted patch list
// is byte-identical across runs of identical input.
func sortedNodeIDs(nodes map[string]abi.MutatorNode) []string {
	ids := make([]string, 0, len(nodes))
	for id := range nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// sortedKeys returns a string map's keys in sorted order for deterministic
// per-rule label emission.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
