// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
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
