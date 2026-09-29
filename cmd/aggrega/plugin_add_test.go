// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/pluginops"
	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// resolvedTestWorkspace seeds a minimal, schema-valid workspace config in a temp
// dir and returns the resolved workspace rooted there, so pluginAddRun writes its
// attachment into that dir's config file.
func resolvedTestWorkspace(t *testing.T) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	return resolvedTestWorkspaceAgainst(t, nil)
}

// resolvedTestWorkspaceAgainst is resolvedTestWorkspace against an explicit
// environment reader, and without the global layer.
//
// A test whose attachment is meant to land in a named global file resolves this
// way rather than seeding a lookup beside the resolution: the write destination
// comes off the resolution, so the reader the resolution used IS the reader the
// write uses. A nil reader keeps the ambient process environment, which the
// binary-wide home redirect has already isolated.
func resolvedTestWorkspaceAgainst(t *testing.T, look func(string) string) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, workspaceconfig.ConfigFilename)
	if err := os.WriteFile(path, []byte("workspace: {}\ndefault_registry: hub.aggrega.ai\noffline: false\n"), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	rw, err := workspaceconfig.ResolveWithoutGlobal(dir, "", look)
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}
	return rw
}

// withTagLister points the config-socket version-resolution seams at a fixed tag
// set for the duration of the test, so version resolution runs without a network.
//
// It wires BOTH seams the fallback resolution reads, because the fallback screens as
// well as selects: a test that fixed only the tags would leave the withdrawal-marker
// read pointing at the live registry, which is the one path in this package that can
// turn a unit test into a real network call. No withdrawal is reported, so selection
// over the fixed tags is unscreened — the arms that assert a screened outcome supply
// their own withdrawals through withWithdrawals.
func withTagLister(t *testing.T, tags []string) {
	t.Helper()
	prev := pluginops.RepoTagLister
	pluginops.RepoTagLister = func(context.Context, workspaceconfig.Config, string, string) ([]string, error) {
		return tags, nil
	}
	t.Cleanup(func() { pluginops.RepoTagLister = prev })
	withWithdrawals(t, nil)
}

// withWithdrawals points the withdrawal-marker seam at a fixed set of verified
// withdrawals for the duration of the test, keyed by version. Each is recorded with a
// signer, because only a signature-verified record screens a version; a nil map
// reports no withdrawal at all.
//
// The versions the seam is asked about are captured so an arm can assert WHICH
// versions were looked up — the network cost of the fallback is part of its contract,
// and a count read from the fixture is the only way to hold it.
func withWithdrawals(t *testing.T, withdrawn map[string]string) *[]string {
	t.Helper()
	asked := &[]string{}
	prev := pluginops.RepoWithdrawalLister
	pluginops.RepoWithdrawalLister = func(_ context.Context, _ workspaceconfig.Config, _, _ string, versions []string, _ io.Writer) ([]registryindex.VersionDeprecation, error) {
		*asked = append(*asked, versions...)
		var deps []registryindex.VersionDeprecation
		for _, v := range oci.SortedSemverTags(keysOf(withdrawn)) {
			deps = append(deps, registryindex.VersionDeprecation{
				Version: v,
				Marker: deprecation.Marker{
					Severity: deprecation.SeverityRetracted,
					Reason:   withdrawn[v],
				},
				Signer: &lockfile.VerifiedSignature{Identity: "publisher", Mode: "keyed"},
			})
		}
		return deps, nil
	}
	t.Cleanup(func() { pluginops.RepoWithdrawalLister = prev })
	return asked
}

// TestPluginAddRun_Provider proves the provider path searches, resolves the
// plugin repository, and writes a well-formed, schema-valid plugins.providers
// {capability, uri} binding. It asserts only the write — a subsequent provider
// resolve is not exercised here.
func TestPluginAddRun_Provider(t *testing.T) {
	// Not parallel: mutates process-global globalDisplay, newRepoDeprecationLookup seams other tests read.
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
	// Not parallel: mutates process-global globalDisplay, newRepoDeprecationLookup seams other tests read.
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
	// Not parallel: mutates process-global globalDisplay, newRepoDeprecationLookup seams other tests read.
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
	// Not parallel: mutates process-global globalDisplay, newRepoDeprecationLookup seams other tests read.
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
	// Not parallel: mutates the process-global globalDisplay seam other tests read.
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
	// Not parallel: mutates process-global globalDisplay, newRepoDeprecationLookup seams other tests read.
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
	// Not parallel: sets the process environment and swaps the process-global registration, search, tag-lister and install seams.
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
	// Not parallel: mutates the process-global globalDisplay seam other tests read.
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
	// Not parallel: mutates the process-global pluginops.RepoTagLister seam other tests read.
	rw := resolvedTestWorkspace(t)
	const host = "hub.aggrega.ai"
	const repoPath = "plugin/governance/tag-injector"
	writeIndexFixture(t, rw.Root(), rw.Config().ResolvedArtifactCacheDir(), host, RegistryIndex{
		Host: host,
		Records: []registryindex.Record{{
			URI:      "oci://" + host + "/" + repoPath,
			Name:     repoPath,
			Version:  "1.2.0",
			Versions: []string{"1.0.0", "1.2.0"},
			Kind:     "Plugin",
		}},
	}, "2026-07-16T12:00:00Z\n")

	// The live tag lister must NOT be consulted when the index answers.
	prev := pluginops.RepoTagLister
	pluginops.RepoTagLister = func(context.Context, workspaceconfig.Config, string, string) ([]string, error) {
		t.Fatalf("live tag lister called; discovery must read the local index by default")
		return nil, nil
	}
	t.Cleanup(func() { pluginops.RepoTagLister = prev })

	var out bytes.Buffer
	got, err := pluginops.ResolveLatestVersion(context.Background(), rw, host, repoPath, &out)
	if err != nil {
		t.Fatalf("pluginops.ResolveLatestVersion: %v", err)
	}
	if got != "1.2.0" {
		t.Errorf("resolved version = %q; want 1.2.0 from the index headline", got)
	}
	if out.Len() != 0 {
		t.Errorf("a record with no withdrawal must announce nothing; got:\n%s", out.String())
	}
}

// TestResolveLatestPluginVersion_OfflineNoIndex proves an offline workspace with
// no local index entry fails loudly, pointing at 'registry update', rather than
// silently networking for discovery.
func TestResolveLatestPluginVersion_OfflineNoIndex(t *testing.T) {
	// Not parallel: mutates the process-global pluginops.RepoTagLister seam other tests read.
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, workspaceconfig.ConfigFilename)
	if err := os.WriteFile(cfgPath, []byte("workspace: {}\ndefault_registry: hub.aggrega.ai\noffline: true\n"), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	rw, err := workspaceconfig.Resolve(dir, "")
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}

	prev := pluginops.RepoTagLister
	pluginops.RepoTagLister = func(context.Context, workspaceconfig.Config, string, string) ([]string, error) {
		t.Fatalf("offline discovery must not network for a live tag list")
		return nil, nil
	}
	t.Cleanup(func() { pluginops.RepoTagLister = prev })

	_, err = pluginops.ResolveLatestVersion(context.Background(), rw, "hub.aggrega.ai", "plugin/governance/tag-injector", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "registry update") {
		t.Fatalf("want a loud offline error pointing at 'registry update'; got %v", err)
	}
	// The wording is part of the contract: it is the only guidance an offline
	// workspace with no snapshot gets, so it is asserted verbatim rather than by
	// keyword.
	const wantOffline = "offline workspace has no local index entry for plugin/governance/tag-injector; " +
		"run 'aggrega registry update' to build the discovery index"
	if err.Error() != wantOffline {
		t.Fatalf("offline refusal = %q; want %q", err.Error(), wantOffline)
	}
}

const (
	screenedIndexHost = "hub.aggrega.ai"
	screenedIndexRepo = "plugin/governance/tag-injector"
	// screenedIndexBuiltAt is when the fixture index below records that it was
	// built. It is named because a screened selection off that snapshot quotes it
	// back, so a test asserting the rendered bytes reads the instant from the
	// fixture that planted it rather than transcribing it a second time.
	screenedIndexBuiltAt = "2026-08-06T09:00:00Z"
)

// writeScreenedPluginIndex writes a one-record local index for the plugin
// repository, deriving the headline and the version set from the supplied tag list
// through the SAME two primitives the index build applies — so the record under
// test is shaped by production code rather than by a hand-picked pair that could
// silently disagree with what a real `registry update` writes.
//
// withdrawn names the versions carrying a retraction marker; each is recorded with
// a verified signer, because only a signature-verified record screens a version.
func writeScreenedPluginIndex(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, tags []string, withdrawn map[string]string) {
	t.Helper()
	rec := registryindex.Record{
		URI:      "oci://" + screenedIndexHost + "/" + screenedIndexRepo,
		Name:     screenedIndexRepo,
		Version:  oci.SelectLatestVersion(tags, false /* includePreRelease */),
		Versions: oci.SortedSemverTags(tags),
		Kind:     "Plugin",
	}
	for _, v := range oci.SortedSemverTags(keysOf(withdrawn)) {
		rec.Deprecations = append(rec.Deprecations, registryindex.VersionDeprecation{
			Version: v,
			Marker: deprecation.Marker{
				Severity: deprecation.SeverityRetracted,
				Reason:   withdrawn[v],
			},
			Signer: &lockfile.VerifiedSignature{Identity: "publisher", Mode: "keyed"},
		})
	}
	writeIndexFixture(t, rw.Root(), rw.Config().ResolvedArtifactCacheDir(), screenedIndexHost,
		RegistryIndex{Host: screenedIndexHost, Records: []registryindex.Record{rec}},
		screenedIndexBuiltAt+"\n")
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// resolvePluginVersionFromIndexOnly resolves through the fast path with the live
// tag lister wired to fail the test, so a pass cannot come from a fallback: what is
// proven is the local-index resolution itself.
func resolvePluginVersionFromIndexOnly(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, out *bytes.Buffer) (string, error) {
	t.Helper()
	prev := pluginops.RepoTagLister
	pluginops.RepoTagLister = func(context.Context, workspaceconfig.Config, string, string) ([]string, error) {
		t.Fatalf("live tag lister called; the local index must answer")
		return nil, nil
	}
	t.Cleanup(func() { pluginops.RepoTagLister = prev })
	return pluginops.ResolveLatestVersion(context.Background(), rw, screenedIndexHost, screenedIndexRepo, out)
}

// TestResolveLatestPluginVersion_WithdrawalScreening is the discriminating pair the
// local-index fast path needs, plus the arms that fence it.
//
// Neither half of the pair proves anything alone: "the lower version resolves"
// passes identically whether the exclusion fired or whether the higher version was
// never in the record, so the SAME two-version record is resolved twice — once with
// the higher version withdrawn and once without — and only the DIFFERENCE between
// the two outcomes is evidence.
func TestResolveLatestPluginVersion_WithdrawalScreening(t *testing.T) {
	// Not parallel: these arms mutate the process-global pluginops.RepoTagLister seam.
	t.Run("withdrawn_higher_version_is_excluded_and_announced", func(t *testing.T) {
		rw := resolvedTestWorkspace(t)
		writeScreenedPluginIndex(t, rw, []string{"1.0.0", "2.0.0"},
			map[string]string{"2.0.0": "withdrawn in favour of a successor"})

		var out bytes.Buffer
		got, err := resolvePluginVersionFromIndexOnly(t, rw, &out)
		if err != nil {
			t.Fatalf("a record with one unwithdrawn version must resolve; got: %v", err)
		}
		if got != "1.0.0" {
			t.Fatalf("resolved %q; want 1.0.0 — the withdrawn 2.0.0 must not be pinned", got)
		}
		// Read as rendered bytes. A version the operator did not name was substituted
		// on evidence absent from their own files, so the substitution is stated.
		notice := out.String()
		t.Logf("rendered announcement:\n%s", notice)
		for _, want := range []string{
			screenedIndexHost + "/" + screenedIndexRepo + ":2.0.0 is retracted and was not selected",
			"withdrawn in favour of a successor",
			"resolved to 1.0.0 instead",
			"aggrega registry update",
		} {
			if !strings.Contains(notice, want) {
				t.Errorf("the announcement must contain %q; rendered bytes were:\n%s", want, notice)
			}
		}
	})

	t.Run("the_same_record_without_the_marker_pins_the_higher", func(t *testing.T) {
		// The other half of the pair: identical record, no marker.
		rw := resolvedTestWorkspace(t)
		writeScreenedPluginIndex(t, rw, []string{"1.0.0", "2.0.0"}, nil)

		var out bytes.Buffer
		got, err := resolvePluginVersionFromIndexOnly(t, rw, &out)
		if err != nil {
			t.Fatalf("an unmarked record must resolve: %v", err)
		}
		if got != "2.0.0" {
			t.Fatalf("resolved %q; want 2.0.0 for an unmarked record", got)
		}
		if out.Len() != 0 {
			t.Errorf("an ordinary resolution must announce nothing; rendered bytes were:\n%s", out.String())
		}
	})

	t.Run("resolution_is_unchanged_over_a_hostile_tag_set", func(t *testing.T) {
		// The behaviour-preservation property, by execution rather than by reading the
		// two primitives: the tag set carries non-conformant tags AND a pre-release —
		// the shape where a filter-invariance argument fails if it is wrong — and the
		// screened selection over the recorded version set must resolve exactly the
		// headline an unscreened selection over the raw tags records.
		tags := []string{"latest", "v1.2.3", "1.2.3.4", "2.0.0-rc.1", "1.0.0", "2.0.0", "0.9.0"}
		headline := oci.SelectLatestVersion(tags, false /* includePreRelease */)
		if headline != "2.0.0" {
			t.Fatalf("fixture precondition: headline = %q; want 2.0.0", headline)
		}

		rw := resolvedTestWorkspace(t)
		writeScreenedPluginIndex(t, rw, tags, nil)
		var out bytes.Buffer
		got, err := resolvePluginVersionFromIndexOnly(t, rw, &out)
		if err != nil {
			t.Fatalf("resolution over a hostile tag set must succeed: %v", err)
		}
		if got != headline {
			t.Fatalf("screened resolution = %q; want the unscreened headline %q — the fix must be invisible when nothing is withdrawn", got, headline)
		}
	})

	t.Run("a_record_with_no_version_set_keeps_its_headline", func(t *testing.T) {
		// A record carried forward from an index built before the version set was
		// recorded holds only its headline. There is nothing to select over, so the
		// headline stands rather than resolving to nothing.
		rw := resolvedTestWorkspace(t)
		writeIndexFixture(t, rw.Root(), rw.Config().ResolvedArtifactCacheDir(), screenedIndexHost,
			RegistryIndex{Host: screenedIndexHost, Records: []registryindex.Record{{
				URI:     "oci://" + screenedIndexHost + "/" + screenedIndexRepo,
				Name:    screenedIndexRepo,
				Version: "1.2.0",
				Kind:    "Plugin",
			}}}, "2026-08-06T09:00:00Z\n")

		var out bytes.Buffer
		got, err := resolvePluginVersionFromIndexOnly(t, rw, &out)
		if err != nil {
			t.Fatalf("a headline-only record must resolve: %v", err)
		}
		if got != "1.2.0" {
			t.Fatalf("resolved %q; want the headline 1.2.0", got)
		}
	})

	t.Run("a_softer_severity_does_not_change_what_is_pinned", func(t *testing.T) {
		// The sharpest form of the pair: the marker is present, recorded and signed
		// exactly as in the excluding arm, and only the SEVERITY differs. A screen that
		// excluded on any record rather than on withdrawal would pass that arm and fail
		// here. Severity below withdrawal is a build-time concern, not a selection one.
		rw := resolvedTestWorkspace(t)
		writeIndexFixture(t, rw.Root(), rw.Config().ResolvedArtifactCacheDir(), screenedIndexHost,
			RegistryIndex{Host: screenedIndexHost, Records: []registryindex.Record{{
				URI:      "oci://" + screenedIndexHost + "/" + screenedIndexRepo,
				Name:     screenedIndexRepo,
				Version:  "2.0.0",
				Versions: []string{"1.0.0", "2.0.0"},
				Kind:     "Plugin",
				Deprecations: []registryindex.VersionDeprecation{{
					Version: "2.0.0",
					Marker:  deprecation.Marker{Severity: deprecation.SeverityWarning, Reason: "superseded"},
					Signer:  &lockfile.VerifiedSignature{Identity: "publisher", Mode: "keyed"},
				}},
			}}}, "2026-08-06T09:00:00Z\n")

		var out bytes.Buffer
		got, err := resolvePluginVersionFromIndexOnly(t, rw, &out)
		if err != nil {
			t.Fatalf("a soft marker must not affect resolution: %v", err)
		}
		if got != "2.0.0" {
			t.Fatalf("resolved %q; want 2.0.0 — a soft severity warns and does not change what is pinned", got)
		}
	})

	t.Run("an_unsigned_record_does_not_exclude", func(t *testing.T) {
		// A record with no verified signer carries no trust basis. Letting one change
		// what is pinned would make an unauthenticated registry-side claim load-bearing.
		rw := resolvedTestWorkspace(t)
		writeIndexFixture(t, rw.Root(), rw.Config().ResolvedArtifactCacheDir(), screenedIndexHost,
			RegistryIndex{Host: screenedIndexHost, Records: []registryindex.Record{{
				URI:      "oci://" + screenedIndexHost + "/" + screenedIndexRepo,
				Name:     screenedIndexRepo,
				Version:  "2.0.0",
				Versions: []string{"1.0.0", "2.0.0"},
				Kind:     "Plugin",
				Deprecations: []registryindex.VersionDeprecation{{
					Version: "2.0.0",
					Marker:  deprecation.Marker{Severity: deprecation.SeverityRetracted, Reason: "withdrawn"},
				}},
			}}}, "2026-08-06T09:00:00Z\n")

		var out bytes.Buffer
		got, err := resolvePluginVersionFromIndexOnly(t, rw, &out)
		if err != nil {
			t.Fatalf("an unsigned record must not halt resolution: %v", err)
		}
		if got != "2.0.0" {
			t.Fatalf("resolved %q; want 2.0.0 — an unverified record must not change what is pinned", got)
		}
	})

	t.Run("no_writer_leaves_the_selection_unscreened", func(t *testing.T) {
		// A screened path that cannot announce is the silent substitution the design
		// forbids, so with no writer the selection is left unscreened rather than
		// excluding quietly.
		rw := resolvedTestWorkspace(t)
		writeScreenedPluginIndex(t, rw, []string{"1.0.0", "2.0.0"},
			map[string]string{"2.0.0": "withdrawn in favour of a successor"})

		prev := pluginops.RepoTagLister
		pluginops.RepoTagLister = func(context.Context, workspaceconfig.Config, string, string) ([]string, error) {
			t.Fatalf("live tag lister called; the local index must answer")
			return nil, nil
		}
		t.Cleanup(func() { pluginops.RepoTagLister = prev })

		got, err := pluginops.ResolveLatestVersion(context.Background(), rw, screenedIndexHost, screenedIndexRepo, nil)
		if err != nil {
			t.Fatalf("resolution with no writer must succeed: %v", err)
		}
		if got != "2.0.0" {
			t.Fatalf("resolved %q; want 2.0.0 — with nowhere to announce, selection stays unscreened", got)
		}
	})

	t.Run("every_version_withdrawn_halts_loudly_and_not_as_not_found", func(t *testing.T) {
		// The loud halt is what replaces pinning a withdrawn version by default. It
		// names every withdrawn version and its reason, and it must not read as a
		// missing repository — the repository exists and still holds those versions.
		rw := resolvedTestWorkspace(t)
		writeScreenedPluginIndex(t, rw, []string{"1.0.0", "2.0.0"},
			map[string]string{"1.0.0": "withdrawn", "2.0.0": "also withdrawn"})

		var out bytes.Buffer
		_, err := resolvePluginVersionFromIndexOnly(t, rw, &out)
		if !errors.Is(err, catalog.ErrAllVersionsRetracted()) {
			t.Fatalf("a fully-withdrawn repository must halt with ErrAllVersionsRetracted; got: %v", err)
		}
		if errors.Is(err, oci.ErrFetchNotFound()) {
			t.Fatalf("a fully-withdrawn repository must NOT report as not-found; got: %v", err)
		}
		for _, want := range []string{"1.0.0", "2.0.0", "withdrawn", "also withdrawn"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("halt message must name %q; got: %v", want, err)
			}
		}
	})
}

// resolvePluginVersionFromFallback resolves with NO local index written, so the
// index step reports absent and resolution takes the live fallback. The workspace is
// online, so nothing suppresses the fallback: what is proven is the fallback's own
// screening.
func resolvePluginVersionFromFallback(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, out io.Writer) (string, error) {
	t.Helper()
	return pluginops.ResolveLatestVersion(context.Background(), rw, screenedIndexHost, screenedIndexRepo, out)
}

// TestResolveLatestPluginVersion_FallbackWithdrawalScreening is the discriminating
// pair the live fallback needs — the path taken when the local index does not carry
// the repository at all — plus the arms that fence it.
//
// The pair is what makes either half evidence. "The lower version resolves" passes
// identically whether the exclusion fired or whether the higher version was never in
// the tag set, so the SAME two-version tag set is resolved twice, once with the higher
// version withdrawn and once without, and only the DIFFERENCE between the two
// outcomes proves the screen ran.
func TestResolveLatestPluginVersion_FallbackWithdrawalScreening(t *testing.T) {
	// Not parallel: these arms mutate the process-global resolution seams.
	t.Run("withdrawn_higher_version_is_excluded_and_announced", func(t *testing.T) {
		rw := resolvedTestWorkspace(t)
		withTagLister(t, []string{"1.0.0", "2.0.0"})
		withWithdrawals(t, map[string]string{"2.0.0": "withdrawn in favour of a successor"})

		var out bytes.Buffer
		got, err := resolvePluginVersionFromFallback(t, rw, &out)
		if err != nil {
			t.Fatalf("a tag set with one unwithdrawn version must resolve; got: %v", err)
		}
		if got != "1.0.0" {
			t.Fatalf("resolved %q; want 1.0.0 — the withdrawn 2.0.0 must not be pinned with no index to screen from", got)
		}
		// Read as rendered bytes, never as a format string.
		notice := out.String()
		t.Logf("rendered announcement:\n%s", notice)
		for _, want := range []string{
			screenedIndexHost + "/" + screenedIndexRepo + ":2.0.0 is retracted and was not selected",
			"withdrawn in favour of a successor",
			"resolved to 1.0.0 instead",
		} {
			if !strings.Contains(notice, want) {
				t.Errorf("the announcement must contain %q; rendered bytes were:\n%s", want, notice)
			}
		}
		// The provenance clause is part of the contract and it differs from the
		// snapshot path's on purpose: these markers were read during this command, so
		// there is no staleness to warn about and no refresh to point at. Pointing at
		// the index-refresh command here would misdescribe where the evidence came from.
		if !strings.Contains(notice, "read from the registry just now") {
			t.Errorf("the announcement must name its live source; rendered bytes were:\n%s", notice)
		}
		if strings.Contains(notice, "registry update") {
			t.Errorf("a live reading must not point at the snapshot-refresh command; rendered bytes were:\n%s", notice)
		}
	})

	t.Run("the_same_tags_without_the_marker_pin_the_higher", func(t *testing.T) {
		// The other half of the pair: identical tag set, no withdrawal.
		rw := resolvedTestWorkspace(t)
		withTagLister(t, []string{"1.0.0", "2.0.0"})

		var out bytes.Buffer
		got, err := resolvePluginVersionFromFallback(t, rw, &out)
		if err != nil {
			t.Fatalf("an unmarked tag set must resolve: %v", err)
		}
		if got != "2.0.0" {
			t.Fatalf("resolved %q; want 2.0.0 for an unmarked tag set", got)
		}
		if out.Len() != 0 {
			t.Errorf("an ordinary resolution must announce nothing; rendered bytes were:\n%s", out.String())
		}
	})

	t.Run("a_soft_severity_does_not_change_what_is_selected", func(t *testing.T) {
		// The sharpest form of the pair: a marker is present, signed and verified for
		// this very repository, and only the SEVERITY differs from the excluding arm.
		rw := resolvedTestWorkspace(t)
		withTagLister(t, []string{"1.0.0", "2.0.0"})
		prev := pluginops.RepoWithdrawalLister
		pluginops.RepoWithdrawalLister = func(_ context.Context, _ workspaceconfig.Config, _, _ string, _ []string, _ io.Writer) ([]registryindex.VersionDeprecation, error) {
			return []registryindex.VersionDeprecation{{
				Version: "2.0.0",
				Marker:  deprecation.Marker{Severity: deprecation.SeverityWarning, Reason: "superseded"},
				Signer:  &lockfile.VerifiedSignature{Identity: "publisher", Mode: "keyed"},
			}}, nil
		}
		t.Cleanup(func() { pluginops.RepoWithdrawalLister = prev })

		var out bytes.Buffer
		got, err := resolvePluginVersionFromFallback(t, rw, &out)
		if err != nil {
			t.Fatalf("a soft marker must not affect resolution: %v", err)
		}
		if got != "2.0.0" {
			t.Fatalf("resolved %q; want 2.0.0 — a soft severity warns and does not change selection", got)
		}
	})

	t.Run("an_unsigned_marker_does_not_screen", func(t *testing.T) {
		// A withdrawal nobody signed carries no trust basis. Letting it change what is
		// resolved would make an unauthenticated registry-side claim load-bearing.
		rw := resolvedTestWorkspace(t)
		withTagLister(t, []string{"1.0.0", "2.0.0"})
		prev := pluginops.RepoWithdrawalLister
		pluginops.RepoWithdrawalLister = func(_ context.Context, _ workspaceconfig.Config, _, _ string, _ []string, _ io.Writer) ([]registryindex.VersionDeprecation, error) {
			return []registryindex.VersionDeprecation{{
				Version: "2.0.0",
				Marker:  deprecation.Marker{Severity: deprecation.SeverityRetracted, Reason: "withdrawn"},
			}}, nil
		}
		t.Cleanup(func() { pluginops.RepoWithdrawalLister = prev })

		got, err := resolvePluginVersionFromFallback(t, rw, &bytes.Buffer{})
		if err != nil {
			t.Fatalf("an unsigned record must not halt resolution: %v", err)
		}
		if got != "2.0.0" {
			t.Fatalf("resolved %q; want 2.0.0 — an unverified record must not change what is pinned", got)
		}
	})

	t.Run("every_version_withdrawn_halts_loudly_and_not_as_not_found", func(t *testing.T) {
		rw := resolvedTestWorkspace(t)
		withTagLister(t, []string{"1.0.0", "2.0.0"})
		withWithdrawals(t, map[string]string{"1.0.0": "withdrawn", "2.0.0": "also withdrawn"})

		_, err := resolvePluginVersionFromFallback(t, rw, &bytes.Buffer{})
		if !errors.Is(err, catalog.ErrAllVersionsRetracted()) {
			t.Fatalf("a fully-withdrawn repository must halt with ErrAllVersionsRetracted; got: %v", err)
		}
		if errors.Is(err, oci.ErrFetchNotFound()) {
			t.Fatalf("a fully-withdrawn repository must NOT report as not-found; got: %v", err)
		}
		for _, want := range []string{"1.0.0", "2.0.0", "withdrawn", "also withdrawn"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("halt message must name %q; got: %v", want, err)
			}
		}
	})

	t.Run("no_writer_leaves_the_selection_unscreened_and_reads_no_marker", func(t *testing.T) {
		// With nowhere to announce a substitution, selection is left unscreened rather
		// than excluding quietly — and the marker reads are skipped along with it, so a
		// screen that would not be used costs nothing.
		rw := resolvedTestWorkspace(t)
		withTagLister(t, []string{"1.0.0", "2.0.0"})
		asked := withWithdrawals(t, map[string]string{"2.0.0": "withdrawn"})

		got, err := pluginops.ResolveLatestVersion(context.Background(), rw, screenedIndexHost, screenedIndexRepo, nil)
		if err != nil {
			t.Fatalf("resolution with no writer must succeed: %v", err)
		}
		if got != "2.0.0" {
			t.Fatalf("resolved %q; want 2.0.0 — with nowhere to announce, selection stays unscreened", got)
		}
		if len(*asked) != 0 {
			t.Errorf("no writer must cost no marker read; the seam was asked about %v", *asked)
		}
	})

	t.Run("no_conformant_tag_resolves_empty_and_reads_no_marker", func(t *testing.T) {
		// The caller names the absent publication in its own words, so this reports the
		// empty version rather than an error. There is also no version the discovery
		// step could select, so there is nothing to screen and no round trip to spend.
		rw := resolvedTestWorkspace(t)
		withTagLister(t, []string{"latest", "v1.2.3", "1.2.3.4"})
		asked := withWithdrawals(t, map[string]string{"1.0.0": "withdrawn"})

		got, err := resolvePluginVersionFromFallback(t, rw, &bytes.Buffer{})
		if err != nil {
			t.Fatalf("a non-conformant tag set must report the empty version, not an error; got: %v", err)
		}
		if got != "" {
			t.Fatalf("resolved %q; want the empty version for a tag set with no strict-SemVer tag", got)
		}
		if len(*asked) != 0 {
			t.Errorf("a tag set with nothing selectable must cost no marker read; the seam was asked about %v", *asked)
		}
	})

	t.Run("the_marker_read_covers_every_conforming_version", func(t *testing.T) {
		// The fallback's network cost is part of its contract, so it is held by the
		// fixture rather than left to a reading of the code: one marker read per
		// strict-SemVer published version, pre-releases included, which is exactly the
		// set the local index build records markers for. Holding the SET also pins the
		// correctness argument — screening over every version, rather than stopping at
		// the first clean one, is what makes the outcome independent of fetch order.
		rw := resolvedTestWorkspace(t)
		tags := []string{"latest", "2.0.0-rc.1", "1.0.0", "2.0.0", "0.9.0", "1.2.3.4"}
		withTagLister(t, tags)
		asked := withWithdrawals(t, nil)

		if _, err := resolvePluginVersionFromFallback(t, rw, &bytes.Buffer{}); err != nil {
			t.Fatalf("resolution over a hostile tag set must succeed: %v", err)
		}
		want := oci.SortedSemverTags(tags)
		if strings.Join(*asked, ",") != strings.Join(want, ",") {
			t.Errorf("markers were read for %v; want exactly the conforming versions %v", *asked, want)
		}
	})

	t.Run("an_unreadable_marker_read_degrades_out_loud_rather_than_halting", func(t *testing.T) {
		// A transient registry fault must not fail an attach that would otherwise
		// succeed, so resolution proceeds unscreened. Degrading can only fail to
		// exclude, never exclude on evidence that did not verify — and it says so, which
		// is the difference between a degraded result and a wrong one.
		rw := resolvedTestWorkspace(t)
		withTagLister(t, []string{"1.0.0", "2.0.0"})
		prev := pluginops.RepoWithdrawalLister
		pluginops.RepoWithdrawalLister = func(_ context.Context, _ workspaceconfig.Config, _, _ string, _ []string, _ io.Writer) ([]registryindex.VersionDeprecation, error) {
			return nil, errors.New("registry unreachable")
		}
		t.Cleanup(func() { pluginops.RepoWithdrawalLister = prev })

		var out bytes.Buffer
		got, err := resolvePluginVersionFromFallback(t, rw, &out)
		if err != nil {
			t.Fatalf("an unreadable marker read must not halt resolution: %v", err)
		}
		if got != "2.0.0" {
			t.Fatalf("resolved %q; want 2.0.0 — a failed marker read leaves selection unscreened", got)
		}
		notice := out.String()
		t.Logf("rendered degrade notice:\n%s", notice)
		for _, want := range []string{
			"markers could not be read",
			"registry unreachable",
			"were not screened for retraction",
		} {
			if !strings.Contains(notice, want) {
				t.Errorf("the degrade must be stated and contain %q; rendered bytes were:\n%s", want, notice)
			}
		}
	})

	t.Run("an_offline_workspace_reads_neither_seam", func(t *testing.T) {
		// The offline refusal is unchanged and must stay ahead of both network reads:
		// the withdrawal read is a second way to network, so it is fenced here too.
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, workspaceconfig.ConfigFilename)
		if err := os.WriteFile(cfgPath, []byte("workspace: {}\ndefault_registry: hub.aggrega.ai\noffline: true\n"), 0o644); err != nil {
			t.Fatalf("seed config: %v", err)
		}
		rw, err := workspaceconfig.Resolve(dir, "")
		if err != nil {
			t.Fatalf("resolve workspace: %v", err)
		}
		prevTags := pluginops.RepoTagLister
		pluginops.RepoTagLister = func(context.Context, workspaceconfig.Config, string, string) ([]string, error) {
			t.Fatalf("offline discovery must not network for a live tag list")
			return nil, nil
		}
		t.Cleanup(func() { pluginops.RepoTagLister = prevTags })
		prevMarkers := pluginops.RepoWithdrawalLister
		pluginops.RepoWithdrawalLister = func(_ context.Context, _ workspaceconfig.Config, _, _ string, _ []string, _ io.Writer) ([]registryindex.VersionDeprecation, error) {
			t.Fatalf("offline discovery must not network for withdrawal markers")
			return nil, nil
		}
		t.Cleanup(func() { pluginops.RepoWithdrawalLister = prevMarkers })

		_, err = pluginops.ResolveLatestVersion(context.Background(), rw, screenedIndexHost, screenedIndexRepo, &bytes.Buffer{})
		// The wording is the only guidance an offline workspace with no snapshot gets,
		// so it is asserted verbatim rather than by keyword.
		const wantOffline = "offline workspace has no local index entry for " + screenedIndexRepo + "; " +
			"run 'aggrega registry update' to build the discovery index"
		if err == nil || err.Error() != wantOffline {
			t.Fatalf("offline refusal = %v; want %q", err, wantOffline)
		}
	})
}

// TestPluginAddRun_HasNoVersionArgument records why an explicit pin has no
// representation on this route: the command takes a type and a name query and
// nothing else, so a caller cannot name a version for the discovery step to
// honour. The override for a withdrawn plugin version is therefore the pinned
// <entity>:<version> line in the config file, which the pull path resolves and
// announces.
func TestPluginAddRun_HasNoVersionArgument(t *testing.T) {
	t.Parallel()
	c := newPluginAddCmd()
	if err := c.Args(c, []string{"governance", "tag-injector", "2.0.0"}); err == nil {
		t.Fatal("plugin add must refuse a third argument; a version-naming surface would need its own design")
	}
	if err := c.Args(c, []string{"governance", "tag-injector"}); err != nil {
		t.Fatalf("plugin add must accept <type> <query>: %v", err)
	}
}

// TestResolveSinglePluginRepo_ExactNameWins proves that when several plugins
// share a substring, an exact plugin-name (final segment) match is selected.
func TestResolveSinglePluginRepo_ExactNameWins(t *testing.T) {
	matches := []string{"plugin/governance/tag", "plugin/governance/tag-injector"}
	got, err := pluginops.ResolveSingleRepo(matches, "tag", "governance")
	if err != nil {
		t.Fatalf("pluginops.ResolveSingleRepo: %v", err)
	}
	if got != "plugin/governance/tag" {
		t.Errorf("exact-name match = %q; want plugin/governance/tag", got)
	}
}
