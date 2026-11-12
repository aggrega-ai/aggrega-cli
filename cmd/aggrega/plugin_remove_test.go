// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// placeholderPin returns a pin for a CLI plugin nobody installed: a well-formed
// content digest, "sha256:" and 64 lowercase hexadecimal digits, the spelling the
// install writer records and the configuration schema accepts. The removal tests
// seed a file the command reads and validates before it changes anything, so a
// pin spelled any other way is refused before the removal under test is reached.
// The digit it repeats keeps each test's pin distinct; what a digest is plays no
// part in what these tests assert.
func placeholderPin(fill byte) string {
	return "sha256:" + strings.Repeat(string(fill), 64)
}

// seedWorkspaceConfig writes body into a fresh temp workspace config and returns
// the resolved workspace and the config path, so a remove writes into that dir.
func seedWorkspaceConfig(t *testing.T, body string) (*workspaceconfig.ResolvedWorkspace, string) {
	t.Helper()
	return seedWorkspaceConfigAgainst(t, body, nil)
}

// seedWorkspaceConfigAgainst is seedWorkspaceConfig against an explicit
// environment reader. A test whose removal targets a named global file resolves
// this way, because the write destination comes off the resolution: the reader
// the resolution used is the reader the write uses, and there is no second seam
// beside it that could name a different file.
func seedWorkspaceConfigAgainst(t *testing.T, body string, look func(string) string) (*workspaceconfig.ResolvedWorkspace, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, workspaceconfig.ConfigFilename)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	rw, err := workspaceconfig.Resolve(dir, "", look)
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}
	return rw, path
}

// TestPluginRemoveRun_ConfigSocket proves 'plugin remove <socket> <query>' drops
// the plugins.<socket> entry, preserves the sibling, and confirms the removal —
// no registry search is involved.
func TestPluginRemoveRun_ConfigSocket(t *testing.T) {
	rw, _ := seedWorkspaceConfig(t, "workspace: {}\nplugins:\n  governance:\n"+
		"    - uri: governance/tag-injector:1.2.0\n      role: label-transform\n"+
		"    - uri: governance/tag-rewriter:1.0.0\n      role: label-transform\n")
	var out bytes.Buffer

	if err := pluginRemoveRun(pluginRemoveConfig{
		Socket:    "governance",
		Query:     "tag-injector",
		Workspace: true,
		ws:        rw,
		Out:       &out,
	}); err != nil {
		t.Fatalf("pluginRemoveRun (config socket): %v", err)
	}

	reread, err := workspaceconfig.Resolve(rw.Root(), "")
	if err != nil {
		t.Fatalf("resolve written config: %v", err)
	}
	got := reread.Config().Plugins.Governance
	if len(got) != 1 || got[0].URI != "governance/tag-rewriter:1.0.0" {
		t.Fatalf("sibling attachment not preserved; got %+v", got)
	}
	if !strings.Contains(out.String(), "Removed governance plugin") {
		t.Fatalf("confirmation line missing:\n%s", out.String())
	}
}

// TestPluginRemoveRun_Provider proves the provider path removes a binding by the
// capability it serves.
func TestPluginRemoveRun_Provider(t *testing.T) {
	rw, _ := seedWorkspaceConfig(t, "workspace: {}\nplugins:\n  providers:\n"+
		"    - capability: helm\n      uri: provider/helm\n")
	var out bytes.Buffer

	if err := pluginRemoveRun(pluginRemoveConfig{
		Socket:    "provider",
		Query:     "helm",
		Workspace: true,
		ws:        rw,
		Out:       &out,
	}); err != nil {
		t.Fatalf("pluginRemoveRun (provider): %v", err)
	}
	reread, err := workspaceconfig.Resolve(rw.Root(), "")
	if err != nil {
		t.Fatalf("resolve written config: %v", err)
	}
	if got := reread.Config().Plugins.Providers; len(got) != 0 {
		t.Fatalf("provider binding not removed; got %+v", got)
	}
}

// TestPluginRemoveRun_CLIDefaultsGlobal proves the cli type removes from the
// global config by default — symmetric with 'add cli', which installs there — and
// that the removed command no longer resolves from that layer.
func TestPluginRemoveRun_CLIDefaultsGlobal(t *testing.T) {
	globalDir := t.TempDir()
	globalPath := filepath.Join(globalDir, "config.yaml")
	if err := os.WriteFile(globalPath, []byte("workspace: {}\nplugins:\n  cli:\n"+
		"    - uri: oci://hub.aggrega.ai/plugin/cli/greet:1.0.0\n      digest: "+placeholderPin('b')+"\n"), 0o644); err != nil {
		t.Fatalf("seed global: %v", err)
	}
	lookup := func(k string) string {
		if k == "AGGREGA_CONFIG" {
			return globalPath
		}
		return ""
	}

	rw, _ := seedWorkspaceConfigAgainst(t, "workspace: {}\n", lookup)
	var out bytes.Buffer
	if err := pluginRemoveRun(pluginRemoveConfig{
		Socket: "cli",
		Query:  "greet",
		ws:     rw,
		Out:    &out,
	}); err != nil {
		t.Fatalf("pluginRemoveRun (cli, default global): %v", err)
	}

	body, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("read global after remove: %v", err)
	}
	if strings.Contains(string(body), "greet") {
		t.Fatalf("cli entry not removed from the global config:\n%s", body)
	}
	if !strings.Contains(out.String(), globalPath) {
		t.Fatalf("confirmation should name the global config file; got:\n%s", out.String())
	}
}

// TestPluginRemoveRun_NoMatchErrorsLoudly proves a query matching nothing halts
// with a specific error naming the type, query, and searched file rather than a
// silent no-op.
func TestPluginRemoveRun_NoMatchErrorsLoudly(t *testing.T) {
	rw, path := seedWorkspaceConfig(t, "workspace: {}\nplugins:\n  governance:\n    - uri: governance/tag-injector:1.2.0\n      role: label-transform\n")
	err := pluginRemoveRun(pluginRemoveConfig{
		Socket:    "governance",
		Query:     "absent",
		Workspace: true,
		ws:        rw,
		Out:       &bytes.Buffer{},
	})
	if err == nil {
		t.Fatalf("want a no-match error")
	}
	for _, want := range []string{"no governance plugin matching", "absent", path} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q should contain %q", err.Error(), want)
		}
	}
}

// TestPluginRemoveRun_UnknownType proves an unrecognized type argument halts with
// the accepted-type list, mirroring 'plugin add'.
func TestPluginRemoveRun_UnknownType(t *testing.T) {
	rw, _ := seedWorkspaceConfig(t, "workspace: {}\n")
	err := pluginRemoveRun(pluginRemoveConfig{Socket: "webhook", Query: "x", Workspace: true, ws: rw, Out: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "unknown type") {
		t.Fatalf("want an unknown-type error; got %v", err)
	}
}
