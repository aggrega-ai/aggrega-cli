// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// memTagTarget wraps an in-memory oras store with a tags slice so a per-repo
// target satisfies registry.TagLister — the capability oci.ListTags requires.
// oras-go's memory.Store implements ReadOnlyTarget but not TagLister; this
// wrapper plus the tags-on-seed tracking is the minimum to drive an index build
// against in-memory repositories.
type memTagTarget struct {
	*memory.Store
	tags []string
}

func (m *memTagTarget) Tags(_ context.Context, _ string, fn func(tags []string) error) error {
	return fn(m.tags)
}

// fakeIndexSource is an in-memory registryIndexSource: a fixed repository set
// plus a per-repo target map, so a registry update runs without a network.
type fakeIndexSource struct {
	repos   []string
	targets map[string]oras.ReadOnlyTarget
}

func (f fakeIndexSource) Repositories(_ context.Context, _ string, fn func(repos []string) error) error {
	return fn(f.repos)
}

func (f fakeIndexSource) Repository(repo string) (oras.ReadOnlyTarget, error) {
	return f.targets[repo], nil
}

// seedComponentRepo packs a real catalog Component manifest per tag with the
// searchable annotations a publish stamps, so the index build reads back
// annotations produced by the actual publish path.
func seedComponentRepo(t *testing.T, tags []string, meta oci.SearchableMetadata) *memTagTarget {
	t.Helper()
	ctx := context.Background()
	target := &memTagTarget{Store: memory.New()}
	const mediaType = "application/vnd.aggrega.component.v1alpha1+yaml"
	ann := meta.Annotations()
	for _, tag := range tags {
		// Vary content per tag so content-address dedupe does not collapse the
		// per-version manifests.
		content := []byte("# component " + tag + "\n")
		if _, err := oci.Push(ctx, target.Store, tag, mediaType, content, ann); err != nil {
			t.Fatalf("seed component Push(%q): %v", tag, err)
		}
		target.tags = append(target.tags, tag)
	}
	return target
}

// seedPluginRepo packs a real single-manifest plugin artifact per tag with the
// searchable annotations a publish stamps, through the actual plugin publish path.
func seedPluginRepo(t *testing.T, tags []string, meta oci.SearchableMetadata) *memTagTarget {
	t.Helper()
	ctx := context.Background()
	target := &memTagTarget{Store: memory.New()}
	ann := meta.Annotations()
	for _, tag := range tags {
		entityYAML := []byte("kind: Plugin\nmetadata:\n  version: " + tag + "\n")
		wasm := []byte("\x00asm" + tag)
		if _, err := oci.PushPluginManifest(ctx, target.Store, tag, entityYAML, wasm, ann); err != nil {
			t.Fatalf("seed plugin PushPluginManifest(%q): %v", tag, err)
		}
		target.tags = append(target.tags, tag)
	}
	return target
}

// withIndexTarget points the index-target seam at a fixed source for the
// duration of the test.
func withIndexTarget(t *testing.T, src registryIndexSource) {
	t.Helper()
	prev := registryIndexTarget
	registryIndexTarget = func(workspaceconfig.Config, string) (registryIndexSource, error) { return src, nil }
	t.Cleanup(func() { registryIndexTarget = prev })
}

// withIndexTargetFunc points the index-target seam at a caller-supplied resolver
// for the duration of the test, so a test can make one host's source resolution
// fail while another host succeeds.
func withIndexTargetFunc(t *testing.T, fn func(workspaceconfig.Config, string) (registryIndexSource, error)) {
	t.Helper()
	prev := registryIndexTarget
	registryIndexTarget = fn
	t.Cleanup(func() { registryIndexTarget = prev })
}

// TestRegistryUpdateBuildsSearchableIndex proves the update walk reads the
// searchable annotations off real published manifests, selects the highest
// strict-SemVer version per repository, writes a deterministic per-host index,
// and produces byte-identical records across two runs against the same state.
func TestRegistryUpdateBuildsSearchableIndex(t *testing.T) {
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
		repos: []string{
			"component/core/api-server",
			"plugin/provider/helm",
			"component/misc/legacy",
		},
		targets: map[string]oras.ReadOnlyTarget{
			// Several versions, out of order, plus a non-semver tag: latest = 1.2.0.
			"component/core/api-server": seedComponentRepo(t,
				[]string{"1.0.0", "1.2.0", "1.1.0", "latest"}, componentMeta),
			"plugin/provider/helm": seedPluginRepo(t,
				[]string{"0.3.0", "0.4.0"}, pluginMeta),
			// Only a non-conforming tag: no record, deterministic skip.
			"component/misc/legacy": seedComponentRepo(t,
				[]string{"latest"}, componentMeta),
		},
	}
	withIndexTarget(t, src)

	root := t.TempDir()
	content := "workspace: {}\ndefault_registry: hub.aggrega.ai\n" +
		"registries:\n  - host: hub.aggrega.ai\n    name: hub\n"
	rw := resolveWorkspaceWithConfig(t, root, content)

	frozen := func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }

	var out bytes.Buffer
	if err := registryUpdateRun(registryUpdateConfig{ws: rw, Out: &out, now: frozen}); err != nil {
		t.Fatalf("registryUpdateRun (all): %v", err)
	}

	cacheDir := rw.Config().ResolvedArtifactCacheDir()
	indexPath := oci.IndexBasePath(root, cacheDir, host) + ".json"
	updatedPath := oci.IndexBasePath(root, cacheDir, host) + ".updated"

	firstBytes, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read index file %s: %v", indexPath, err)
	}
	t.Logf("index file %s:\n%s", indexPath, firstBytes)

	var idx RegistryIndex
	if err := json.Unmarshal(firstBytes, &idx); err != nil {
		t.Fatalf("unmarshal index: %v", err)
	}
	if idx.Host != host {
		t.Errorf("index host = %q; want %q", idx.Host, host)
	}
	if len(idx.Records) != 2 {
		t.Fatalf("index has %d records; want 2 (legacy no-semver repo skipped): %+v", len(idx.Records), idx.Records)
	}

	// Records are sorted by URI: component/... sorts before plugin/...
	comp := idx.Records[0]
	if comp.URI != "oci://hub.aggrega.ai/component/core/api-server" {
		t.Errorf("component URI = %q", comp.URI)
	}
	if comp.Version != "1.2.0" {
		t.Errorf("component version = %q; want 1.2.0 (highest strict semver, non-semver 'latest' skipped)", comp.Version)
	}
	if comp.Kind != "Component" {
		t.Errorf("component kind = %q; want Component", comp.Kind)
	}
	if comp.Description != componentMeta.Description {
		t.Errorf("component description = %q; want %q", comp.Description, componentMeta.Description)
	}
	if comp.Category != "compute" {
		t.Errorf("component category = %q; want compute", comp.Category)
	}
	if comp.Labels == nil {
		t.Fatalf("component labels nil; want reconstructed category label")
	}
	if v, ok := comp.Labels.Get(componentCategoryLabel); !ok || v != "compute" {
		t.Errorf("component label %s = %q (ok=%v); want compute", componentCategoryLabel, v, ok)
	}

	plug := idx.Records[1]
	if plug.URI != "oci://hub.aggrega.ai/plugin/provider/helm" {
		t.Errorf("plugin URI = %q", plug.URI)
	}
	if plug.Version != "0.4.0" {
		t.Errorf("plugin version = %q; want 0.4.0", plug.Version)
	}
	if plug.Kind != "Plugin" {
		t.Errorf("plugin kind = %q; want Plugin", plug.Kind)
	}
	if plug.PluginType != "provider" {
		t.Errorf("plugin pluginType = %q; want provider", plug.PluginType)
	}
	wantCaps := []string{"render", "fs.read"}
	if len(plug.Capabilities) != len(wantCaps) {
		t.Fatalf("plugin capabilities = %v; want %v", plug.Capabilities, wantCaps)
	}
	for i, c := range wantCaps {
		if plug.Capabilities[i] != c {
			t.Errorf("plugin capability[%d] = %q; want %q (recovered from comma-joined value)", i, plug.Capabilities[i], c)
		}
	}
	// A plugin declares no category, so it carries no reconstructed label.
	if plug.Labels != nil {
		t.Errorf("plugin labels = %v; want nil (no category facet)", plug.Labels)
	}

	// Freshness timestamp lives in the sibling, not the records file.
	if _, err := os.Stat(updatedPath); err != nil {
		t.Errorf("timestamp sibling %s missing: %v", updatedPath, err)
	}
	if bytes.Contains(firstBytes, []byte("2026-01-02")) {
		t.Errorf("records file must not contain the timestamp; found it in:\n%s", firstBytes)
	}

	// Byte-identity across two runs against the same registry state.
	if err := registryUpdateRun(registryUpdateConfig{ws: rw, Out: &out, now: frozen}); err != nil {
		t.Fatalf("registryUpdateRun (second): %v", err)
	}
	secondBytes, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("re-read index file: %v", err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Errorf("records file not byte-identical across two runs:\nfirst:\n%s\nsecond:\n%s", firstBytes, secondBytes)
	}
}

// TestRegistryUpdateSingleRegistryByAlias proves the single-argument form
// resolves a friendly alias to its host and indexes only that registry.
func TestRegistryUpdateSingleRegistryByAlias(t *testing.T) {
	const host = "hub.aggrega.ai"
	meta := oci.SearchableMetadata{Description: "d", Category: "compute", Kind: "Component"}
	src := fakeIndexSource{
		repos:   []string{"component/core/api-server"},
		targets: map[string]oras.ReadOnlyTarget{"component/core/api-server": seedComponentRepo(t, []string{"1.0.0"}, meta)},
	}
	withIndexTarget(t, src)

	root := t.TempDir()
	content := "workspace: {}\ndefault_registry: hub.aggrega.ai\n" +
		"registries:\n  - host: hub.aggrega.ai\n    name: hub\n"
	rw := resolveWorkspaceWithConfig(t, root, content)

	var out bytes.Buffer
	if err := registryUpdateRun(registryUpdateConfig{ws: rw, Args: []string{"hub"}, Out: &out}); err != nil {
		t.Fatalf("registryUpdateRun (alias): %v", err)
	}

	cacheDir := rw.Config().ResolvedArtifactCacheDir()
	indexPath := oci.IndexBasePath(root, cacheDir, host) + ".json"
	data, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read index file %s: %v", indexPath, err)
	}
	var idx RegistryIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		t.Fatalf("unmarshal index: %v", err)
	}
	if idx.Host != host || len(idx.Records) != 1 || idx.Records[0].Version != "1.0.0" {
		t.Fatalf("single-alias index unexpected: host=%q records=%+v", idx.Host, idx.Records)
	}
}

// TestRegistryUpdateAllRegistriesPartialOnError proves the no-argument walk
// notes a host whose source resolution fails and continues to the next host:
// the reachable registry is still indexed and written, the unreachable one
// writes nothing, and the run returns a non-nil error summarizing the failure so
// the exit code stays non-zero. One unreachable registry must not abandon the
// reachable ones.
func TestRegistryUpdateAllRegistriesPartialOnError(t *testing.T) {
	const goodHost = "hub.aggrega.ai"
	const badHost = "broken.aggrega.ai"

	meta := oci.SearchableMetadata{Description: "d", Category: "compute", Kind: "Component"}
	good := fakeIndexSource{
		repos:   []string{"component/core/api-server"},
		targets: map[string]oras.ReadOnlyTarget{"component/core/api-server": seedComponentRepo(t, []string{"1.0.0"}, meta)},
	}
	// The unreachable host fails at source resolution; the reachable host
	// resolves to the in-memory source.
	withIndexTargetFunc(t, func(_ workspaceconfig.Config, host string) (registryIndexSource, error) {
		if host == goodHost {
			return good, nil
		}
		return nil, fmt.Errorf("unreachable")
	})

	root := t.TempDir()
	// broken.aggrega.ai is walked first (config order); hub.aggrega.ai second,
	// so the reachable host is only reached if the walk continues past a failure.
	content := "workspace: {}\ndefault_registry: hub.aggrega.ai\n" +
		"registries:\n" +
		"  - host: broken.aggrega.ai\n    name: broken\n" +
		"  - host: hub.aggrega.ai\n    name: hub\n"
	rw := resolveWorkspaceWithConfig(t, root, content)
	frozen := func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }

	var out bytes.Buffer
	err := registryUpdateRun(registryUpdateConfig{ws: rw, Out: &out, now: frozen})
	if err == nil {
		t.Fatalf("registryUpdateRun (all, one host failing): got nil error; want non-nil summary error")
	}

	// The reachable registry is still indexed and written.
	cacheDir := rw.Config().ResolvedArtifactCacheDir()
	goodPath := oci.IndexBasePath(root, cacheDir, goodHost) + ".json"
	data, rerr := os.ReadFile(goodPath)
	if rerr != nil {
		t.Fatalf("reachable registry index not written at %s: %v", goodPath, rerr)
	}
	var idx RegistryIndex
	if uerr := json.Unmarshal(data, &idx); uerr != nil {
		t.Fatalf("unmarshal reachable index: %v", uerr)
	}
	if idx.Host != goodHost || len(idx.Records) != 1 || idx.Records[0].Version != "1.0.0" {
		t.Fatalf("reachable index unexpected: host=%q records=%+v", idx.Host, idx.Records)
	}

	// The unreachable registry wrote nothing.
	badPath := oci.IndexBasePath(root, cacheDir, badHost) + ".json"
	if _, serr := os.Stat(badPath); !os.IsNotExist(serr) {
		t.Errorf("unreachable registry must not write an index; stat(%s) err=%v", badPath, serr)
	}

	// The failure is noted per host on the command output.
	if !bytes.Contains(out.Bytes(), []byte("registry broken.aggrega.ai:")) {
		t.Errorf("output missing per-host failure note; got:\n%s", out.String())
	}
}

// TestRegistryUpdateSingleRegistryFailureIsLoud proves the single-argument form
// returns immediately on a host failure and writes nothing — the operator
// explicitly targeted one registry, so its failure is not swallowed into a
// partial success.
func TestRegistryUpdateSingleRegistryFailureIsLoud(t *testing.T) {
	const host = "hub.aggrega.ai"
	withIndexTargetFunc(t, func(workspaceconfig.Config, string) (registryIndexSource, error) {
		return nil, fmt.Errorf("unreachable")
	})

	root := t.TempDir()
	content := "workspace: {}\ndefault_registry: hub.aggrega.ai\n" +
		"registries:\n  - host: hub.aggrega.ai\n    name: hub\n"
	rw := resolveWorkspaceWithConfig(t, root, content)

	var out bytes.Buffer
	if err := registryUpdateRun(registryUpdateConfig{ws: rw, Args: []string{"hub"}, Out: &out}); err == nil {
		t.Fatalf("registryUpdateRun (single, failing host): got nil error; want non-nil")
	}

	cacheDir := rw.Config().ResolvedArtifactCacheDir()
	indexPath := oci.IndexBasePath(root, cacheDir, host) + ".json"
	if _, serr := os.Stat(indexPath); !os.IsNotExist(serr) {
		t.Errorf("single-registry failure must write nothing; stat(%s) err=%v", indexPath, serr)
	}
}
