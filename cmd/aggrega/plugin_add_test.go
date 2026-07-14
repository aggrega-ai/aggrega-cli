// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// resolvedTestWorkspace seeds a minimal, schema-valid workspace config in a temp
// dir and returns the resolved workspace rooted there, so pluginAddRun writes its
// attachment into that dir's config file.
func resolvedTestWorkspace(t *testing.T) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, workspaceconfig.ConfigFilename)
	if err := os.WriteFile(path, []byte("workspace: {}\ndefault_registry: hub.aggrega.ai\noffline: false\n"), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	rw, err := workspaceconfig.Resolve(dir, "")
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}
	return rw
}

// withTagLister points the config-socket version-resolution seam at a fixed tag
// set for the duration of the test, so version resolution runs without a network.
func withTagLister(t *testing.T, tags []string) {
	t.Helper()
	prev := pluginRepoTagLister
	pluginRepoTagLister = func(context.Context, string, string) ([]string, error) {
		return tags, nil
	}
	t.Cleanup(func() { pluginRepoTagLister = prev })
}

// TestPluginAddRun_Provider proves the provider path searches, resolves the
// plugin repository, and writes a well-formed, schema-valid plugins.providers
// {target, uri} binding. It asserts only the write — a subsequent provider
// resolve is not exercised here.
func TestPluginAddRun_Provider(t *testing.T) {
	withSearchTarget(t, fakeRepoLister{repos: searchFixtureRepos}, nil)
	rw := resolvedTestWorkspace(t)
	var out bytes.Buffer

	if err := pluginAddRun(context.Background(), pluginAddConfig{
		Socket: "provider",
		Query:  "helm",
		Target: "helm",
		ws:     rw,
		Out:    &out,
	}); err != nil {
		t.Fatalf("pluginAddRun (provider): %v", err)
	}

	// Re-resolve the written file: a green resolve is the config-schema validation.
	reread, err := workspaceconfig.Resolve(rw.Root(), "")
	if err != nil {
		t.Fatalf("resolve written config: %v", err)
	}
	got := reread.Config().Plugins.Providers
	if len(got) != 1 {
		t.Fatalf("want exactly one provider binding; got %+v", got)
	}
	if got[0].Capability != "helm" || got[0].URI != "oci://hub.aggrega.ai/plugin/provider/helm" {
		t.Errorf("provider binding = %+v; want {helm, oci://hub.aggrega.ai/plugin/provider/helm}", got[0])
	}
	if body, rerr := os.ReadFile(filepath.Join(rw.Root(), workspaceconfig.ConfigFilename)); rerr == nil {
		t.Logf("written .aggrega-config.yaml:\n%s", body)
	}
}

// TestPluginAddRun_ProviderRequiresTarget proves the provider socket halts when
// --target is absent rather than writing a target-less binding.
func TestPluginAddRun_ProviderRequiresTarget(t *testing.T) {
	withSearchTarget(t, fakeRepoLister{repos: searchFixtureRepos}, nil)
	rw := resolvedTestWorkspace(t)
	err := pluginAddRun(context.Background(), pluginAddConfig{Socket: "provider", Query: "helm", ws: rw, Out: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "requires --target") {
		t.Fatalf("want a --target-required error; got %v", err)
	}
}

// TestPluginAddRun_ConfigSocket proves a config-socket add strips the plugin/
// prefix to the bare entity identity, pins the latest non-pre-release published
// version, and writes a schema-valid plugins.<socket> entry.
func TestPluginAddRun_ConfigSocket(t *testing.T) {
	withSearchTarget(t, fakeRepoLister{repos: []string{"plugin/mutator/tag-injector"}}, nil)
	withTagLister(t, []string{"1.0.0", "1.2.0", "2.0.0-rc1"})
	rw := resolvedTestWorkspace(t)
	var out bytes.Buffer

	if err := pluginAddRun(context.Background(), pluginAddConfig{
		Socket: "mutator",
		Query:  "tag-injector",
		ws:     rw,
		Out:    &out,
	}); err != nil {
		t.Fatalf("pluginAddRun (config socket): %v", err)
	}

	reread, err := workspaceconfig.Resolve(rw.Root(), "")
	if err != nil {
		t.Fatalf("resolve written config: %v", err)
	}
	got := reread.Config().Plugins.Mutator
	if len(got) != 1 {
		t.Fatalf("want exactly one mutator attachment; got %+v", got)
	}
	// 2.0.0-rc1 is pre-release and excluded; 1.2.0 is the highest stable tag.
	if got[0].URI != "mutator/tag-injector:1.2.0" {
		t.Errorf("attachment URI = %q; want mutator/tag-injector:1.2.0", got[0].URI)
	}
}

// TestPluginAddRun_ConfigSocketNoVersion proves the add halts loudly when the
// resolved plugin repository carries no published version tag, rather than
// writing an unpinned attachment.
func TestPluginAddRun_ConfigSocketNoVersion(t *testing.T) {
	withSearchTarget(t, fakeRepoLister{repos: []string{"plugin/policy/deny-gate"}}, nil)
	withTagLister(t, []string{"latest"}) // no strict-SemVer tag
	rw := resolvedTestWorkspace(t)
	err := pluginAddRun(context.Background(), pluginAddConfig{Socket: "policy", Query: "deny-gate", ws: rw, Out: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "no published version") {
		t.Fatalf("want a no-published-version error; got %v", err)
	}
}

// TestPluginAddRun_UnknownSocket proves an unrecognized socket argument halts
// with the list of accepted sockets.
func TestPluginAddRun_UnknownSocket(t *testing.T) {
	rw := resolvedTestWorkspace(t)
	err := pluginAddRun(context.Background(), pluginAddConfig{Socket: "webhook", Query: "x", ws: rw, Out: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "unknown socket") {
		t.Fatalf("want an unknown-socket error; got %v", err)
	}
}

// TestPluginAddRun_AmbiguousQuery proves a query that matches more than one
// plugin without an exact plugin-name hit halts with the candidate list rather
// than guessing.
func TestPluginAddRun_AmbiguousQuery(t *testing.T) {
	withSearchTarget(t, fakeRepoLister{repos: []string{
		"plugin/mutator/tag-injector",
		"plugin/mutator/tag-rewriter",
	}}, nil)
	rw := resolvedTestWorkspace(t)
	err := pluginAddRun(context.Background(), pluginAddConfig{Socket: "mutator", Query: "tag", ws: rw, Out: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "narrow the query") {
		t.Fatalf("want an ambiguity error; got %v", err)
	}
}

// TestResolveSinglePluginRepo_ExactNameWins proves that when several plugins
// share a substring, an exact plugin-name (final segment) match is selected.
func TestResolveSinglePluginRepo_ExactNameWins(t *testing.T) {
	matches := []string{"plugin/mutator/tag", "plugin/mutator/tag-injector"}
	got, err := resolveSinglePluginRepo(matches, "tag", "mutator")
	if err != nil {
		t.Fatalf("resolveSinglePluginRepo: %v", err)
	}
	if got != "plugin/mutator/tag" {
		t.Errorf("exact-name match = %q; want plugin/mutator/tag", got)
	}
}
