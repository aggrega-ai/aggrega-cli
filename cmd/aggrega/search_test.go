// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// searchFrozen is the fixed freshness time the round-trip tests stamp into the
// index so the freshness header is deterministic.
func searchFrozen() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }

// buildSearchWorkspace writes a real local index into a temp workspace by driving
// the registry-update path against an in-memory source, so the search runs read
// back bytes produced by the actual index writer. The seeded index carries a
// component (Kind Component, a category label) and a plugin (Kind Plugin, a
// plugin type and capabilities) under host hub.aggrega.ai.
func buildSearchWorkspace(t *testing.T) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	const host = "hub.aggrega.ai"

	componentMeta := oci.SearchableMetadata{
		Description: "Stateless HTTP API server",
		Category:    "compute",
		Kind:        "Component",
	}
	pluginMeta := oci.SearchableMetadata{
		Description:  "Helm rendering provider",
		Kind:         "Plugin",
		PluginType:   "provider",
		Capabilities: []string{"render", "fs.read"},
	}
	src := fakeIndexSource{
		repos: []string{"component/core/api-server", "plugin/provider/helm"},
		targets: map[string]oras.ReadOnlyTarget{
			"component/core/api-server": seedComponentRepo(t, []string{"1.2.0"}, componentMeta),
			"plugin/provider/helm":      seedPluginRepo(t, []string{"0.4.0"}, pluginMeta),
		},
	}
	withIndexTarget(t, src)

	root := t.TempDir()
	content := "workspace: {}\ndefault_registry: hub.aggrega.ai\n" +
		"registries:\n  - host: hub.aggrega.ai\n    name: hub\n"
	rw := resolveWorkspaceWithConfig(t, root, content)

	if err := registryUpdateRun(registryUpdateConfig{ws: rw, Out: new(bytes.Buffer), now: searchFrozen}); err != nil {
		t.Fatalf("seed index via registryUpdateRun: %v", err)
	}
	// Pin the staleness clock just after the stamped time so the seeded index reads
	// fresh in the round-trip tests (no staleness advisory perturbs the freshness
	// header). The dedicated staleness test overrides this to a far-future clock.
	withSearchClock(t, searchFrozen().Add(time.Hour))
	return rw
}

// withSearchClock pins the search staleness clock for the duration of the test.
func withSearchClock(t *testing.T, at time.Time) {
	t.Helper()
	prev := searchNow
	searchNow = func() time.Time { return at }
	t.Cleanup(func() { searchNow = prev })
}

// runSearch drives the search run function and returns its stdout, stderr, and
// error, so an assertion can inspect the result data and the freshness header
// independently.
func runSearch(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, opts searchOptions) (string, string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err := searchRun(rw, &out, &errOut, opts)
	return out.String(), errOut.String(), err
}

const (
	componentURI = "oci://hub.aggrega.ai/component/core/api-server"
	pluginURI    = "oci://hub.aggrega.ai/plugin/provider/helm"
)

// TestSearchRoundTripQueryAndFacets proves the search reads back the real
// written index and applies each facet against the actually-stamped record
// fields — most pointedly that --kind plugin matches the capitalized "Plugin"
// kind the publish path stamps.
func TestSearchRoundTripQueryAndFacets(t *testing.T) {
	rw := buildSearchWorkspace(t)

	cases := []struct {
		name string
		opts searchOptions
		want []string // URIs, in order
	}{
		{"free-text query over name/description", searchOptions{Query: "api", Output: "names"}, []string{componentURI}},
		{"query matches plugin description", searchOptions{Query: "helm", Output: "names"}, []string{pluginURI}},
		{"--kind plugin folds onto stamped Plugin", searchOptions{Kinds: []string{"plugin"}, Output: "names"}, []string{pluginURI}},
		{"--kind featurerecipe form is accepted", searchOptions{Kinds: []string{"component"}, Output: "names"}, []string{componentURI}},
		{"category via label selector", searchOptions{Selector: "component.aggrega.ai/category=compute", Output: "names"}, []string{componentURI}},
		{"--capability scopes to plugin", searchOptions{Capabilities: []string{"render"}, Output: "names"}, []string{pluginURI}},
		{"--plugin-type scopes to plugin", searchOptions{PluginType: "provider", Output: "names"}, []string{pluginURI}},
		{"no filter returns both, URI-sorted", searchOptions{Output: "names"}, []string{componentURI, pluginURI}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, errOut, err := runSearch(t, rw, tc.opts)
			if err != nil {
				t.Fatalf("searchRun: %v", err)
			}
			var got []string
			for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
				if line != "" {
					got = append(got, line)
				}
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("names = %v; want %v", got, tc.want)
			}
			wantHeader := "Index for hub.aggrega.ai last updated 2026-01-02T03:04:05Z\n"
			if errOut != wantHeader {
				t.Errorf("freshness header = %q; want %q", errOut, wantHeader)
			}
		})
	}
}

// TestSearchOutputFormatsDeterministic proves each output format emits the
// expected deterministic stdout and that running the same search twice yields
// byte-identical stdout.
func TestSearchOutputFormatsDeterministic(t *testing.T) {
	rw := buildSearchWorkspace(t)

	t.Run("table", func(t *testing.T) {
		out, _, err := runSearch(t, rw, searchOptions{Output: "table"})
		if err != nil {
			t.Fatalf("searchRun: %v", err)
		}
		if !strings.Contains(out, "URI") || !strings.Contains(out, "KIND") ||
			!strings.Contains(out, componentURI) || !strings.Contains(out, pluginURI) {
			t.Errorf("table output missing header or rows:\n%s", out)
		}
		assertByteIdentical(t, rw, searchOptions{Output: "table"}, out)
	})

	t.Run("names", func(t *testing.T) {
		out, _, err := runSearch(t, rw, searchOptions{Output: "names"})
		if err != nil {
			t.Fatalf("searchRun: %v", err)
		}
		want := componentURI + "\n" + pluginURI + "\n"
		if out != want {
			t.Errorf("names output = %q; want %q", out, want)
		}
		assertByteIdentical(t, rw, searchOptions{Output: "names"}, out)
	})

	t.Run("json", func(t *testing.T) {
		out, _, err := runSearch(t, rw, searchOptions{Output: "json"})
		if err != nil {
			t.Fatalf("searchRun: %v", err)
		}
		var payload searchJSON
		if uerr := json.Unmarshal([]byte(out), &payload); uerr != nil {
			t.Fatalf("json output does not parse: %v\n%s", uerr, out)
		}
		if payload.Updated["hub.aggrega.ai"] != "2026-01-02T03:04:05Z" {
			t.Errorf("json updated = %v; want hub.aggrega.ai=2026-01-02T03:04:05Z", payload.Updated)
		}
		if len(payload.Records) != 2 || payload.Records[0].URI != componentURI || payload.Records[1].URI != pluginURI {
			t.Errorf("json records = %+v; want component then plugin", payload.Records)
		}
		if !strings.HasSuffix(out, "\n") {
			t.Errorf("json output missing trailing newline")
		}
		assertByteIdentical(t, rw, searchOptions{Output: "json"}, out)
	})
}

// assertByteIdentical re-runs the same search and fails when the stdout differs
// from the first run — the deterministic-output guarantee on the result stream.
func assertByteIdentical(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, opts searchOptions, first string) {
	t.Helper()
	second, _, err := runSearch(t, rw, opts)
	if err != nil {
		t.Fatalf("searchRun (second): %v", err)
	}
	if first != second {
		t.Errorf("stdout not byte-identical across runs:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

// TestSearchLimitCaps proves --limit caps the result deterministically, keeping
// the first records in the URI-sorted order.
func TestSearchLimitCaps(t *testing.T) {
	rw := buildSearchWorkspace(t)
	out, _, err := runSearch(t, rw, searchOptions{Output: "names", Limit: 1})
	if err != nil {
		t.Fatalf("searchRun: %v", err)
	}
	if out != componentURI+"\n" {
		t.Errorf("--limit 1 output = %q; want just the first URI-sorted record %q", out, componentURI)
	}
}

// TestSearchNoMatchesTable proves a zero-match search prints an explicit line
// rather than a bare blank.
func TestSearchNoMatchesTable(t *testing.T) {
	rw := buildSearchWorkspace(t)
	out, _, err := runSearch(t, rw, searchOptions{Query: "nonesuch", Output: "table"})
	if err != nil {
		t.Fatalf("searchRun: %v", err)
	}
	if strings.TrimSpace(out) != "(no matches)" {
		t.Errorf("zero-match table output = %q; want \"(no matches)\"", out)
	}
}

// TestSearchFailsLoudWhenNoIndexSingleRegistry proves that targeting one
// registry whose index has not been built returns a loud error naming the index
// build, rather than an empty result.
func TestSearchFailsLoudWhenNoIndexSingleRegistry(t *testing.T) {
	root := t.TempDir()
	content := "workspace: {}\ndefault_registry: hub.aggrega.ai\n" +
		"registries:\n  - host: hub.aggrega.ai\n    name: hub\n"
	rw := resolveWorkspaceWithConfig(t, root, content)

	_, _, err := runSearch(t, rw, searchOptions{Registry: "hub", Output: "names"})
	if err == nil {
		t.Fatal("searchRun (single registry, no index): got nil error; want a loud error")
	}
	if !strings.Contains(err.Error(), "aggrega registry update") {
		t.Errorf("error %q must name 'aggrega registry update'", err)
	}
}

// TestSearchFailsLoudWhenNoIndexAllRegistries proves the default all-registries
// path, when every configured registry's index is absent, returns a loud error
// naming the index build rather than presenting an empty result as success.
func TestSearchFailsLoudWhenNoIndexAllRegistries(t *testing.T) {
	root := t.TempDir()
	content := "workspace: {}\ndefault_registry: hub.aggrega.ai\n" +
		"registries:\n  - host: hub.aggrega.ai\n    name: hub\n"
	rw := resolveWorkspaceWithConfig(t, root, content)

	out, errOut, err := runSearch(t, rw, searchOptions{Output: "names"})
	if err == nil {
		t.Fatal("searchRun (all registries, no index): got nil error; want a loud error")
	}
	if !strings.Contains(err.Error(), "aggrega registry update") {
		t.Errorf("error %q must name 'aggrega registry update'", err)
	}
	if out != "" {
		t.Errorf("no-index run wrote stdout %q; want empty", out)
	}
	// The per-host not-built note is written to errOut before the loud return.
	if !strings.Contains(errOut, "hub.aggrega.ai") {
		t.Errorf("errOut %q should note the not-built host", errOut)
	}
}

// TestSearchStalenessAdvisory proves the thin staleness surface: a clearly-old
// snapshot draws a one-line advisory to run 'registry update' on errOut, while
// stdout still carries the results and no network is contacted. A fresh snapshot
// draws no advisory.
func TestSearchStalenessAdvisory(t *testing.T) {
	rw := buildSearchWorkspace(t)

	// Fresh (fixture pins the clock ~1h after the stamp): no advisory.
	_, errFresh, err := runSearch(t, rw, searchOptions{Output: "names"})
	if err != nil {
		t.Fatalf("searchRun (fresh): %v", err)
	}
	if strings.Contains(errFresh, "stale") {
		t.Errorf("fresh snapshot must draw no staleness advisory; errOut:\n%s", errFresh)
	}

	// Clearly old: pin the clock far past indexStaleAfter.
	withSearchClock(t, searchFrozen().Add(90*24*time.Hour))
	out, errStale, err := runSearch(t, rw, searchOptions{Output: "names"})
	if err != nil {
		t.Fatalf("searchRun (stale): %v", err)
	}
	if !strings.Contains(errStale, "run 'aggrega registry update' to refresh") {
		t.Errorf("clearly-old snapshot must draw the refresh advisory; errOut:\n%s", errStale)
	}
	// The results still come through — the advisory never blocks or forces a fetch.
	if !strings.Contains(out, componentURI) {
		t.Errorf("stale search still returns results; stdout:\n%s", out)
	}
}
