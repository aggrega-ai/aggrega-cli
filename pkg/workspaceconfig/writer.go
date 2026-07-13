// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"fmt"
	"os"

	"go.yaml.in/yaml/v3"
)

// UpsertRegistry adds or replaces the per-registry trust policy for
// reg.Host in the workspace config file at path, preserving every other
// setting in the file, and writes the result back. A file that does not
// yet exist is created carrying a `workspace: {}` marker and the one
// registry entry. An existing entry for the same host is replaced in place
// (its list position is preserved); a new host is appended.
//
// The merge operates on the document's YAML node tree, rewriting only the
// `registries` key. Other keys — and their values — are carried through
// untouched, so unrelated settings are neither dropped nor re-defaulted.
// UpsertRegistry validates the merged document against the config schema
// before writing, so a malformed result never reaches disk.
//
// This is the single config-write entry point; the loader is otherwise
// read-only. Callers that register a registry's trust baseline (e.g.
// `aggrega registry add`) route through here so the write path, schema
// gate, and ordering discipline live in one place.
func UpsertRegistry(path string, reg RegistryConfig) error {
	if reg.Host == "" {
		return fmt.Errorf("workspaceconfig: UpsertRegistry: registry host is required")
	}

	var root yaml.Node
	data, rerr := os.ReadFile(path)
	switch {
	case rerr == nil:
		if verr := validateBytes(data, path); verr != nil {
			return verr
		}
		if uerr := yaml.Unmarshal(data, &root); uerr != nil {
			return fmt.Errorf("workspaceconfig: parse existing %s: %w", path, uerr)
		}
	case os.IsNotExist(rerr):
		// A fresh file: a document whose root mapping carries a
		// `workspace: {}` marker so discovery anchors here.
		if err := root.Encode(map[string]any{"workspace": map[string]any{}}); err != nil {
			return fmt.Errorf("workspaceconfig: seed fresh config: %w", err)
		}
	default:
		return fmt.Errorf("workspaceconfig: read %s: %w", path, rerr)
	}

	mapping := rootMapping(&root)
	if mapping == nil {
		return fmt.Errorf("workspaceconfig: %s: top-level document is not a mapping", path)
	}

	registries := readRegistries(mapping)
	replaced := false
	for i := range registries {
		if registries[i].Host == reg.Host {
			registries[i] = reg
			replaced = true
			break
		}
	}
	if !replaced {
		registries = append(registries, reg)
	}

	var regNode yaml.Node
	if err := regNode.Encode(registries); err != nil {
		return fmt.Errorf("workspaceconfig: encode registries: %w", err)
	}
	setMappingKey(mapping, "registries", &regNode)

	out, merr := yaml.Marshal(&root)
	if merr != nil {
		return fmt.Errorf("workspaceconfig: marshal %s: %w", path, merr)
	}
	if verr := validateBytes(out, path); verr != nil {
		return fmt.Errorf("workspaceconfig: merged config failed validation: %w", verr)
	}
	if werr := os.WriteFile(path, out, 0o644); werr != nil {
		return fmt.Errorf("workspaceconfig: write %s: %w", path, werr)
	}
	return nil
}

// UpsertProviderBinding adds or updates the `plugins.providers` entry for
// target in the workspace config file at path, preserving every other setting,
// and writes the result back. A file that does not yet exist is created
// carrying a `workspace: {}` marker and the one binding. An existing entry for
// the same target has its plugin URI updated in place (its list position and
// any other members are preserved); a new target is appended.
//
// Like UpsertRegistry it operates on the document's YAML node tree, rewriting
// only the `plugins.providers` list (creating the `plugins` block if absent),
// and validates the merged document against the config schema before writing,
// so a malformed result never reaches disk. This is the write path the
// interactive pull binding-helper routes through, keeping the config-write,
// schema-gate, and node-surgery discipline in one place.
func UpsertProviderBinding(path, target, plugin string) error {
	if target == "" || plugin == "" {
		return fmt.Errorf("workspaceconfig: UpsertProviderBinding: target and plugin are required")
	}

	var root yaml.Node
	data, rerr := os.ReadFile(path)
	switch {
	case rerr == nil:
		if verr := validateBytes(data, path); verr != nil {
			return verr
		}
		if uerr := yaml.Unmarshal(data, &root); uerr != nil {
			return fmt.Errorf("workspaceconfig: parse existing %s: %w", path, uerr)
		}
	case os.IsNotExist(rerr):
		if err := root.Encode(map[string]any{"workspace": map[string]any{}}); err != nil {
			return fmt.Errorf("workspaceconfig: seed fresh config: %w", err)
		}
	default:
		return fmt.Errorf("workspaceconfig: read %s: %w", path, rerr)
	}

	mapping := rootMapping(&root)
	if mapping == nil {
		return fmt.Errorf("workspaceconfig: %s: top-level document is not a mapping", path)
	}

	pluginsNode := getOrCreateMapping(mapping, "plugins")
	providers := UpsertProviderRef(readProviderBindings(pluginsNode), target, plugin)

	var provNode yaml.Node
	if err := provNode.Encode(providers); err != nil {
		return fmt.Errorf("workspaceconfig: encode providers: %w", err)
	}
	setMappingKey(pluginsNode, "providers", &provNode)

	out, merr := yaml.Marshal(&root)
	if merr != nil {
		return fmt.Errorf("workspaceconfig: marshal %s: %w", path, merr)
	}
	if verr := validateBytes(out, path); verr != nil {
		return fmt.Errorf("workspaceconfig: merged config failed validation: %w", verr)
	}
	if werr := os.WriteFile(path, out, 0o644); werr != nil {
		return fmt.Errorf("workspaceconfig: write %s: %w", path, werr)
	}
	return nil
}

// UpsertProviderRef sets the plugin URI for target in refs, updating an
// existing entry in place (preserving its list position and any other members)
// or appending a new {target, uri} entry when target is absent. It is the
// single by-target upsert primitive shared by the config-file writer and the
// interactive pull binding-helper's in-memory update, so both apply the same
// replace-or-append semantic.
func UpsertProviderRef(refs []ProviderRef, target, uri string) []ProviderRef {
	for i := range refs {
		if refs[i].Target == target {
			refs[i].URI = uri
			return refs
		}
	}
	return append(refs, ProviderRef{Target: target, URI: uri})
}

// readProviderBindings decodes the existing `providers` list from a `plugins`
// mapping node, or returns nil when the node is absent or carries no providers.
func readProviderBindings(pluginsNode *yaml.Node) []ProviderRef {
	if pluginsNode == nil || pluginsNode.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(pluginsNode.Content); i += 2 {
		if pluginsNode.Content[i].Value == "providers" {
			var refs []ProviderRef
			_ = pluginsNode.Content[i+1].Decode(&refs)
			return refs
		}
	}
	return nil
}

// getOrCreateMapping returns the mapping node bound to key in parent, creating
// and attaching an empty mapping when the key is absent. The returned node is
// live in parent's content tree, so mutating it (e.g. via setMappingKey)
// rewrites parent in place.
func getOrCreateMapping(parent *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			return parent.Content[i+1]
		}
	}
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	parent.Content = append(parent.Content, keyNode, node)
	return node
}

// rootMapping returns the mapping node at the root of a parsed document,
// unwrapping the document node yaml.Unmarshal produces. Returns nil when
// the root is not a mapping.
func rootMapping(root *yaml.Node) *yaml.Node {
	n := root
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return nil
		}
		n = n.Content[0]
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	return n
}

// readRegistries decodes the existing `registries` value from a mapping
// node into typed RegistryConfig values, or returns nil when the key is
// absent.
func readRegistries(mapping *yaml.Node) []RegistryConfig {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == "registries" {
			var regs []RegistryConfig
			_ = mapping.Content[i+1].Decode(&regs)
			return regs
		}
	}
	return nil
}

// setMappingKey replaces the value node for key in mapping, or appends the
// key/value pair when key is absent.
func setMappingKey(mapping *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = value
			return
		}
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	mapping.Content = append(mapping.Content, keyNode, value)
}
