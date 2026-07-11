// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
)

// installedPluginIndexFile is the name of the file, under the shared aggrega
// cache root, that records the CLI plugins installed as top-level commands. It
// sits alongside the compile and describe caches so every persistent cache lives
// under the one root.
const installedPluginIndexFile = "installed-cli-plugins.json"

// installedPluginEntry is one installed CLI plugin as a top-level command: the
// plugin artifact URI the runner resolves and runs, plus the describe metadata
// cached at install time so help renders without loading the guest.
type installedPluginEntry struct {
	// URI is the plugin artifact reference the runner resolves and runs.
	URI string `json:"uri"`
	// Usage is the one-line command summary shown in the command list and help.
	Usage string `json:"usage"`
	// Capabilities is the plugin's declared capability list, cached at install
	// time so help can describe the command without loading the guest. Optional.
	Capabilities []string `json:"capabilities,omitempty"`
}

// installedPluginIndex maps a top-level command name to the installed CLI plugin
// registered under it. A zero-value index (nil Plugins) means nothing is
// installed and no commands are registered.
type installedPluginIndex struct {
	// Plugins maps a command name to its installed plugin entry.
	Plugins map[string]installedPluginEntry `json:"plugins"`
}

// names returns the index's command names in ascending order, so registration
// and help listing are deterministic regardless of the JSON object's key order.
func (idx installedPluginIndex) names() []string {
	names := make([]string, 0, len(idx.Plugins))
	for name := range idx.Plugins {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// installedPluginIndexPath returns the path to the installed-CLI-plugin index
// under the shared aggrega cache root, and whether that root could be resolved.
func installedPluginIndexPath() (string, bool) {
	root, ok := wasm.AggregaCacheRoot()
	if !ok {
		return "", false
	}
	return filepath.Join(root, installedPluginIndexFile), true
}

// readInstalledPluginIndex loads the installed-CLI-plugin index from disk. An
// unresolvable cache root, an absent index file, or an empty file loads cleanly
// as an empty index with a nil error: "nothing installed" is the normal state,
// not a failure. A present-but-malformed file returns an error so a corrupt index
// is surfaced loudly rather than silently dropping installed commands.
func readInstalledPluginIndex() (installedPluginIndex, error) {
	path, ok := installedPluginIndexPath()
	if !ok {
		return installedPluginIndex{}, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return installedPluginIndex{}, nil
		}
		return installedPluginIndex{}, fmt.Errorf("read installed CLI plugin index at %s: %w", path, err)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return installedPluginIndex{}, nil
	}
	var idx installedPluginIndex
	if err := json.Unmarshal(raw, &idx); err != nil {
		return installedPluginIndex{}, fmt.Errorf("parse installed CLI plugin index at %s: %w", path, err)
	}
	return idx, nil
}

// writeInstalledPluginIndex persists idx to the installed-CLI-plugin index file
// under the shared aggrega cache root, creating the root as needed. An
// unresolvable cache root is an error: an install cannot record a plugin it has
// nowhere to persist. The write is deterministic — the index marshals with the
// object keys sorted by encoding/json and the entry fields in declared order —
// so re-installing a plugin whose entry is unchanged rewrites byte-identical
// content. It is the write counterpart of readInstalledPluginIndex and targets
// the same path helper, so the read and write sides cannot address different
// files.
func writeInstalledPluginIndex(idx installedPluginIndex) error {
	path, ok := installedPluginIndexPath()
	if !ok {
		return errors.New("no user cache directory is available to record the installed CLI plugin index")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create cache directory for installed CLI plugin index: %w", err)
	}
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("encode installed CLI plugin index: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write installed CLI plugin index at %s: %w", path, err)
	}
	return nil
}
