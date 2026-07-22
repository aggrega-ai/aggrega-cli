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

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
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
	pluginRepoTagLister = func(context.Context, workspaceconfig.Config, string, string) ([]string, error) {
		return tags, nil
	}
	t.Cleanup(func() { pluginRepoTagLister = prev })
}

// TestPluginAddRun_Provider proves the provider path searches, resolves the
// plugin repository, and writes a well-formed, schema-valid plugins.providers
// {capability, uri} binding. It asserts only the write — a subsequent provider
// resolve is not exercised here.
func TestPluginAddRun_Provider(t *testing.T) {
	withSearchTarget(t, fakeRepoLister{repos: searchFixtureRepos}, nil)
	rw := resolvedTestWorkspace(t)
	var out bytes.Buffer

	if err := pluginAddRun(context.Background(), pluginAddConfig{
		Socket:     "provider",
		Query:      "helm",
		Capability: "helm",
		ws:         rw,
		Out:        &out,
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

// TestPluginAddRun_ProviderRequiresCapability proves the provider socket halts
// when --capability is absent rather than writing a capability-less binding.
func TestPluginAddRun_ProviderRequiresCapability(t *testing.T) {
	withSearchTarget(t, fakeRepoLister{repos: searchFixtureRepos}, nil)
	rw := resolvedTestWorkspace(t)
	err := pluginAddRun(context.Background(), pluginAddConfig{Socket: "provider", Query: "helm", ws: rw, Out: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "requires --capability") {
		t.Fatalf("want a --capability-required error; got %v", err)
	}
}

// TestPluginAddRun_ConfigSocket proves a config-socket add strips the plugin/
// prefix to the bare entity identity, pins the latest non-pre-release published
// version, and writes a schema-valid plugins.<socket> entry.
func TestPluginAddRun_ConfigSocket(t *testing.T) {
	withSearchTarget(t, fakeRepoLister{repos: []string{"plugin/governance/tag-injector"}}, nil)
	withTagLister(t, []string{"1.0.0", "1.2.0", "2.0.0-rc1"})
	rw := resolvedTestWorkspace(t)
	var out bytes.Buffer

	if err := pluginAddRun(context.Background(), pluginAddConfig{
		Socket: "governance",
		Role:   "label-transform",
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
	got := reread.Config().Plugins.Governance
	if len(got) != 1 {
		t.Fatalf("want exactly one governance attachment; got %+v", got)
	}
	// 2.0.0-rc1 is pre-release and excluded; 1.2.0 is the highest stable tag.
	if got[0].URI != "governance/tag-injector:1.2.0" {
		t.Errorf("attachment URI = %q; want governance/tag-injector:1.2.0", got[0].URI)
	}
	if got[0].Role != "label-transform" {
		t.Errorf("attachment role = %q; want label-transform", got[0].Role)
	}
}

// TestPluginAddRun_ConfigSocketNoVersion proves the add halts loudly when the
// resolved plugin repository carries no published version tag, rather than
// writing an unpinned attachment.
func TestPluginAddRun_ConfigSocketNoVersion(t *testing.T) {
	withSearchTarget(t, fakeRepoLister{repos: []string{"plugin/governance/deny-gate"}}, nil)
	withTagLister(t, []string{"latest"}) // no strict-SemVer tag
	rw := resolvedTestWorkspace(t)
	err := pluginAddRun(context.Background(), pluginAddConfig{Socket: "governance", Role: "ir-verdict", Query: "deny-gate", ws: rw, Out: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "no published version") {
		t.Fatalf("want a no-published-version error; got %v", err)
	}
}

// TestPluginAddRun_UnknownType proves an unrecognized type argument halts
// with the list of accepted types.
func TestPluginAddRun_UnknownType(t *testing.T) {
	rw := resolvedTestWorkspace(t)
	err := pluginAddRun(context.Background(), pluginAddConfig{Socket: "webhook", Query: "x", ws: rw, Out: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "unknown type") {
		t.Fatalf("want an unknown-type error; got %v", err)
	}
}

// TestPluginAddRun_AmbiguousQuery proves a query that matches more than one
// plugin without an exact plugin-name hit halts with the candidate list rather
// than guessing.
func TestPluginAddRun_AmbiguousQuery(t *testing.T) {
	withSearchTarget(t, fakeRepoLister{repos: []string{
		"plugin/governance/tag-injector",
		"plugin/governance/tag-rewriter",
	}}, nil)
	rw := resolvedTestWorkspace(t)
	err := pluginAddRun(context.Background(), pluginAddConfig{Socket: "governance", Role: "label-transform", Query: "tag", ws: rw, Out: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "narrow the query") {
		t.Fatalf("want an ambiguity error; got %v", err)
	}
}

// TestPluginAddRun_CLIInstallsViaSearchResolve proves the cli type drives the
// cli-scoped registry search, pins the latest published version into an oci://
// reference, and installs the resolved plugin as a command end-to-end through the
// shared install path — the describe gate is the real one, the capability prompt
// is skipped with --yes, and the index gains the guest's declared command.
func TestPluginAddRun_CLIInstallsViaSearchResolve(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	withCLIRegistrations(t, nil)
	withSearchTarget(t, fakeRepoLister{repos: []string{"plugin/cli/moo"}}, nil)
	withTagLister(t, []string{"1.0.0", "1.2.0", "2.0.0-rc1"})
	guest := buildInstallGuest(t, "cli-install-guest")

	// Record the reference the install fetch receives so the search-resolve path's
	// ref construction is asserted; the describe gate stays the real one.
	var gotRef string
	recordingFetch := func(_ context.Context, _ *workspaceconfig.ResolvedWorkspace, ref string) (resolvedInstallable, error) {
		gotRef = ref
		return resolvedInstallable{URI: ref, Bytes: guest}, nil
	}
	withInstallSeams(t, recordingFetch, nil)

	rw := resolvedTestWorkspace(t)
	var out bytes.Buffer
	if err := pluginAddRun(context.Background(), pluginAddConfig{
		Socket:    "cli",
		Query:     "moo",
		Workspace: true,
		AssumeYes: true,
		ws:        rw,
		In:        strings.NewReader(""),
		Out:       &out,
	}); err != nil {
		t.Fatalf("pluginAddRun (cli): %v", err)
	}

	// 2.0.0-rc1 is pre-release and excluded; 1.2.0 is the highest stable tag.
	if gotRef != "oci://hub.aggrega.ai/plugin/cli/moo:1.2.0" {
		t.Fatalf("cli add built ref %q; want oci://hub.aggrega.ai/plugin/cli/moo:1.2.0", gotRef)
	}
	refs := reResolveCLI(t, rw)
	if len(refs) != 1 || refs[0].URI != "oci://hub.aggrega.ai/plugin/cli/moo:1.2.0" {
		t.Fatalf("plugins.cli missing the pinned ref after 'add cli': %+v", refs)
	}
	if refs[0].Digest != digest.ContentDigest(guest) {
		t.Fatalf("cli pin digest = %q; want the verified-bytes digest", refs[0].Digest)
	}
	if !strings.Contains(out.String(), "no additional capabilities") {
		t.Fatalf("capability prompt not shown:\n%s", out.String())
	}
}

// TestPluginAddRun_CapabilityFlagsRejectedOffCLI proves the cli-only capability
// flags halt with a usage error on a non-cli type rather than being silently
// ignored, mirroring the provider-only --capability guard.
func TestPluginAddRun_CapabilityFlagsRejectedOffCLI(t *testing.T) {
	rw := resolvedTestWorkspace(t)
	err := pluginAddRun(context.Background(), pluginAddConfig{
		Socket:    "governance",
		Role:      "label-transform",
		Query:     "x",
		AssumeYes: true,
		ws:        rw,
		Out:       &bytes.Buffer{},
	})
	if err == nil || !strings.Contains(err.Error(), "apply only to the cli type") {
		t.Fatalf("want a cli-only flag error; got %v", err)
	}
}

// TestResolveLatestPluginVersion_IndexDefault proves version discovery reads the
// local index by default: with a built index carrying the repository, the headline
// version is resolved from the snapshot and the live tag lister is never consulted.
func TestResolveLatestPluginVersion_IndexDefault(t *testing.T) {
	rw := resolvedTestWorkspace(t)
	const host = "hub.aggrega.ai"
	const repoPath = "plugin/governance/tag-injector"
	writeIndexFixture(t, rw.Root(), rw.Config().ResolvedArtifactCacheDir(), host, RegistryIndex{
		Host: host,
		Records: []IndexRecord{{
			URI:      "oci://" + host + "/" + repoPath,
			Name:     repoPath,
			Version:  "1.2.0",
			Versions: []string{"1.0.0", "1.2.0"},
			Kind:     "Plugin",
		}},
	}, "2026-07-16T12:00:00Z\n")

	// The live tag lister must NOT be consulted when the index answers.
	prev := pluginRepoTagLister
	pluginRepoTagLister = func(context.Context, workspaceconfig.Config, string, string) ([]string, error) {
		t.Fatalf("live tag lister called; discovery must read the local index by default")
		return nil, nil
	}
	t.Cleanup(func() { pluginRepoTagLister = prev })

	got, err := resolveLatestPluginVersion(context.Background(), rw, host, repoPath)
	if err != nil {
		t.Fatalf("resolveLatestPluginVersion: %v", err)
	}
	if got != "1.2.0" {
		t.Errorf("resolved version = %q; want 1.2.0 from the index headline", got)
	}
}

// TestResolveLatestPluginVersion_OfflineNoIndex proves an offline workspace with
// no local index entry fails loudly, pointing at 'registry update', rather than
// silently networking for discovery.
func TestResolveLatestPluginVersion_OfflineNoIndex(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, workspaceconfig.ConfigFilename)
	if err := os.WriteFile(cfgPath, []byte("workspace: {}\ndefault_registry: hub.aggrega.ai\noffline: true\n"), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	rw, err := workspaceconfig.Resolve(dir, "")
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}

	prev := pluginRepoTagLister
	pluginRepoTagLister = func(context.Context, workspaceconfig.Config, string, string) ([]string, error) {
		t.Fatalf("offline discovery must not network for a live tag list")
		return nil, nil
	}
	t.Cleanup(func() { pluginRepoTagLister = prev })

	_, err = resolveLatestPluginVersion(context.Background(), rw, "hub.aggrega.ai", "plugin/governance/tag-injector")
	if err == nil || !strings.Contains(err.Error(), "registry update") {
		t.Fatalf("want a loud offline error pointing at 'registry update'; got %v", err)
	}
}

// TestResolveSinglePluginRepo_ExactNameWins proves that when several plugins
// share a substring, an exact plugin-name (final segment) match is selected.
func TestResolveSinglePluginRepo_ExactNameWins(t *testing.T) {
	matches := []string{"plugin/governance/tag", "plugin/governance/tag-injector"}
	got, err := resolveSinglePluginRepo(matches, "tag", "governance")
	if err != nil {
		t.Fatalf("resolveSinglePluginRepo: %v", err)
	}
	if got != "plugin/governance/tag" {
		t.Errorf("exact-name match = %q; want plugin/governance/tag", got)
	}
}
