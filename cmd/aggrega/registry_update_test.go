// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/publishops"
	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/internal/registryops"
	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// countingTarget wraps a per-repo target and counts content-blob Fetch calls, so
// a test can assert that a conditional refresh skips the manifest read (and the
// referrer-payload reads) for an unchanged repository. Resolve and Tags are not
// counted — those are the cheap metadata reads the digest-compare relies on.
type countingTarget struct {
	*memTagTarget
	fetches *int
}

func (c *countingTarget) Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	*c.fetches++
	return c.memTagTarget.Fetch(ctx, target)
}

const updateTestConfig = "workspace: {}\ndefault_registry: hub.aggrega.ai\n" +
	"registries:\n  - host: hub.aggrega.ai\n    name: hub\n"

// keyedMarkerSigner returns a keyed signer plus a workspace config that pins its
// public key on the test host. The index build records a deprecation marker only
// when the marker's OWN signature verifies against the honored signer set, so a
// test that needs a recorded marker must attach a signed one and must configure the
// key that verifies it. A pinned key makes the same key the same signer, which is
// what keeps these in-memory tests free of a keyless trust root.
func keyedMarkerSigner(t *testing.T) (signing.Signer, string) {
	t.Helper()
	privPEM, pubPEM, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	var b strings.Builder
	b.WriteString("workspace: {}\ndefault_registry: hub.aggrega.ai\n")
	b.WriteString("registries:\n  - host: hub.aggrega.ai\n    name: hub\n")
	b.WriteString("    public_key: |\n")
	for _, line := range strings.Split(strings.TrimRight(string(pubPEM), "\n"), "\n") {
		b.WriteString("      " + line + "\n")
	}
	return signer, b.String()
}

// attachSignedMarkerToTag attaches marker to tag on an in-memory store through the
// production attach path, so the marker and the signature of its own manifest
// digest land in the same two-referrer shape `aggrega deprecate` writes. Calling
// the production path rather than re-deriving it is what keeps a test fixture from
// asserting against a shape the command does not actually produce.
//
// It then makes the marker manifest resolvable BY DIGEST, which is a fidelity shim
// for the in-memory store rather than anything the production path needs. A marker
// is an untagged referrer, and verifying its own signature means listing referrers
// of the marker's digest — a registry resolves a manifest by digest, while the
// in-memory store resolves only what it has tagged, so without this the marker's
// signature is unreachable in-memory and the marker is correctly reported
// unhonored. The end-to-end behaviour is proven against a real registry elsewhere;
// this only lets the offline index tests reach the same code path.
func attachSignedMarkerToTag(t *testing.T, store *memory.Store, tag string, marker deprecation.Marker, signer signing.Signer) {
	t.Helper()
	ctx := context.Background()
	_, err := registryindex.AttachSignedMarker(ctx, io.Discard, store, tag,
		"hub.aggrega.ai/component/core/api-server:"+tag, marker, signer)
	if err != nil {
		t.Fatalf("registryindex.AttachSignedMarker(%s): %v", tag, err)
	}
	markers, err := oci.ListReferrers(ctx, store, tag, deprecation.ArtifactType)
	if err != nil || len(markers) == 0 {
		t.Fatalf("marker not discoverable after attach on %s: %v (%d found)", tag, err, len(markers))
	}
	for _, m := range markers {
		if terr := store.Tag(ctx, m, m.Digest.String()); terr != nil {
			t.Fatalf("tag marker by digest: %v", terr)
		}
	}
}

func frozenClock() func() time.Time {
	return func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }
}

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

// tagList exposes the seeded tags so the in-memory ConditionalTags can serve them.
func (m *memTagTarget) tagList() []string {
	return m.tags
}

// syntheticTagsETag mirrors a registry that validates its tag listing with a strong
// entity-tag over the serialized set: stable for an unchanged set, different for a
// changed one. It stands in for the registry's synthetic listing validator so the
// in-memory build exercises the conditional revalidation path without a network.
func syntheticTagsETag(tags []string) string {
	sum := sha256.Sum256([]byte(strings.Join(tags, ",")))
	return fmt.Sprintf("%q", "sha256:"+hex.EncodeToString(sum[:]))
}

// ConditionalTags serves the seeded tags with a synthetic listing validator,
// answering a matching If-None-Match as not-modified so the conditional refresh's
// 304 reuse path runs without a network.
func (f fakeIndexSource) ConditionalTags(_ context.Context, repo, ifNoneMatch string) (tags []string, etag string, notModified bool, truncated bool, err error) {
	tgt, ok := f.targets[repo]
	if !ok || tgt == nil {
		return nil, "", false, false, fmt.Errorf("no target for %q", repo)
	}
	lister, ok := tgt.(interface{ tagList() []string })
	if !ok {
		return nil, "", false, false, fmt.Errorf("target for %q exposes no tag list", repo)
	}
	tags = lister.tagList()
	etag = syntheticTagsETag(tags)
	if ifNoneMatch != "" && ifNoneMatch == etag {
		return nil, etag, true, false, nil
	}
	return tags, etag, false, false, nil
}

// listingSpySource wraps fakeIndexSource and records the If-None-Match presented to
// the most recent ConditionalTags call, so a test can prove the stored listing
// validator is what drives the conditional refresh.
type listingSpySource struct {
	fakeIndexSource
	lastIfNoneMatch *string
}

func (s listingSpySource) ConditionalTags(ctx context.Context, repo, ifNoneMatch string) ([]string, string, bool, bool, error) {
	*s.lastIfNoneMatch = ifNoneMatch
	return s.fakeIndexSource.ConditionalTags(ctx, repo, ifNoneMatch)
}

// componentFixtureLabels is the authored label map the component fixtures carry:
// a category label plus a non-category one, in author order. Both are needed for
// a fixture to be a shape the publish path can actually produce — the category
// facet is derived from the category label, so a manifest cannot carry the facet
// without carrying the map.
func componentFixtureLabels() *types.OrderedMap[string] {
	labels := types.NewOrderedMap[string]()
	labels.Set("component.aggrega.ai/type", "server")
	labels.Set(publishops.ComponentCategoryLabel, "compute")
	return labels
}

// seedComponentRepo packs a real catalog Component manifest per tag with the
// searchable annotations a publish stamps, so the index build reads back
// annotations produced by the actual publish path.
func seedComponentRepo(t *testing.T, tags []string, meta oci.SearchableMetadata) *memTagTarget {
	t.Helper()
	ctx := context.Background()
	target := &memTagTarget{Store: memory.New()}
	const mediaType = "application/vnd.aggrega.component.v1alpha1+yaml"
	ann, err := meta.Annotations()
	if err != nil {
		t.Fatalf("seed component Annotations: %v", err)
	}
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
	ann, err := meta.Annotations()
	if err != nil {
		t.Fatalf("seed plugin Annotations: %v", err)
	}
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
//
// The seam is the shared layer's one seam, so pointing it here points it for
// every surface that builds an index — which is what makes a command-level test
// a test of the build the session performs too, rather than of a second one.
func withIndexTarget(t *testing.T, src registryIndexSource) {
	t.Helper()
	prev := registryops.IndexTarget
	registryops.IndexTarget = func(workspaceconfig.Config, string) (registryIndexSource, error) { return src, nil }
	t.Cleanup(func() { registryops.IndexTarget = prev })
}

// withIndexTargetFunc points the index-target seam at a caller-supplied resolver
// for the duration of the test, so a test can make one host's source resolution
// fail while another host succeeds.
func withIndexTargetFunc(t *testing.T, fn func(workspaceconfig.Config, string) (registryIndexSource, error)) {
	t.Helper()
	prev := registryops.IndexTarget
	registryops.IndexTarget = fn
	t.Cleanup(func() { registryops.IndexTarget = prev })
}

// TestRegistryUpdateBuildsSearchableIndex proves the update walk reads the
// searchable annotations off real published manifests, selects the highest
// strict-SemVer version per repository, writes a deterministic per-host index,
// and produces byte-identical records across two runs against the same state.
func TestRegistryUpdateBuildsSearchableIndex(t *testing.T) {
	// Not parallel: mutates the process-global registryops.IndexTarget seam other tests read.
	const host = "hub.aggrega.ai"

	componentMeta := oci.SearchableMetadata{
		Description: "Stateless HTTP API server",
		Category:    "compute",
		Kind:        "Component",
		// The category facet is derived from the category label at publish, so a
		// manifest carrying the facet always carries the label map it came from.
		Labels: componentFixtureLabels(),
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
	if err := registryUpdateRun(t.Context(), registryUpdateConfig{ws: rw, Out: &out, now: frozen}); err != nil {
		t.Fatalf("registryUpdateRun (all): %v", err)
	}

	cacheDir := rw.Config().ResolvedArtifactCacheDir()
	updatedPath := oci.IndexBasePath(root, cacheDir, host) + ".updated"
	compPath := oci.IndexRepoPath(root, cacheDir, host, "component/core/api-server")

	loaded, err := registryindex.LoadHost(root, cacheDir, host)
	if err != nil {
		t.Fatalf("loadHostIndex: %v", err)
	}
	if loaded.Host != host {
		t.Errorf("index host = %q; want %q", loaded.Host, host)
	}
	if len(loaded.Records) != 2 {
		t.Fatalf("index has %d records; want 2 (legacy no-semver repo skipped): %+v", len(loaded.Records), loaded.Records)
	}

	// Records load in URI-then-version order: component/... sorts before plugin/...
	comp := loaded.Records[0]
	if comp.URI != "oci://hub.aggrega.ai/component/core/api-server" {
		t.Errorf("component URI = %q", comp.URI)
	}
	if comp.Version != "1.2.0" {
		t.Errorf("component version = %q; want 1.2.0 (highest strict semver, non-semver 'latest' skipped)", comp.Version)
	}
	// The full version list is retained and ascending; the non-semver 'latest' is
	// excluded — the version history the flat index used to discard.
	wantVersions := []string{"1.0.0", "1.1.0", "1.2.0"}
	if len(comp.Versions) != len(wantVersions) {
		t.Fatalf("component versions = %v; want %v", comp.Versions, wantVersions)
	}
	for i, v := range wantVersions {
		if comp.Versions[i] != v {
			t.Errorf("component versions[%d] = %q; want %q", i, comp.Versions[i], v)
		}
	}
	// The resolved manifest digest of the headline version is recorded as a
	// revalidation validator.
	if comp.ManifestDigest == "" {
		t.Errorf("component manifestDigest is empty; want the resolved manifest digest")
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
		t.Fatalf("component labels nil; want the published label map")
	}
	if v, ok := comp.Labels.Get(publishops.ComponentCategoryLabel); !ok || v != "compute" {
		t.Errorf("component label %s = %q (ok=%v); want compute", publishops.ComponentCategoryLabel, v, ok)
	}
	// Every authored label is read back, not only the category, and in author order.
	if v, ok := comp.Labels.Get("component.aggrega.ai/type"); !ok || v != "server" {
		t.Errorf("component label component.aggrega.ai/type = %q (ok=%v); want server", v, ok)
	}
	if keys := comp.Labels.Keys(); len(keys) != 2 || keys[0] != "component.aggrega.ai/type" || keys[1] != publishops.ComponentCategoryLabel {
		t.Errorf("component label keys = %v; want author order [component.aggrega.ai/type %s]", keys, publishops.ComponentCategoryLabel)
	}

	plug := loaded.Records[1]
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

	// Freshness timestamp lives in the sibling, not any per-repo record file.
	if _, err := os.Stat(updatedPath); err != nil {
		t.Errorf("timestamp sibling %s missing: %v", updatedPath, err)
	}
	firstBytes, err := os.ReadFile(compPath)
	if err != nil {
		t.Fatalf("read per-repo record file %s: %v", compPath, err)
	}
	if bytes.Contains(firstBytes, []byte("2026-01-02")) {
		t.Errorf("per-repo record file must not contain the timestamp; found it in:\n%s", firstBytes)
	}

	// The on-disk file set is exactly the record set: one file per record.
	firstSet := indexFileSet(t, root, cacheDir, host)
	if len(firstSet) != 2 {
		t.Errorf("host index directory has %d record files; want 2: %v", len(firstSet), firstSet)
	}

	// Byte-identity per file AND identical file set across two runs against the
	// same registry state.
	if err := registryUpdateRun(t.Context(), registryUpdateConfig{ws: rw, Out: &out, now: frozen}); err != nil {
		t.Fatalf("registryUpdateRun (second): %v", err)
	}
	secondBytes, err := os.ReadFile(compPath)
	if err != nil {
		t.Fatalf("re-read per-repo record file: %v", err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Errorf("per-repo record file not byte-identical across two runs:\nfirst:\n%s\nsecond:\n%s", firstBytes, secondBytes)
	}
	secondSet := indexFileSet(t, root, cacheDir, host)
	if len(secondSet) != len(firstSet) {
		t.Errorf("file set changed across two runs: first %v second %v", firstSet, secondSet)
	}
	for name := range firstSet {
		if !secondSet[name] {
			t.Errorf("file %q present after first run, absent after second", name)
		}
	}
}

// indexFileSet returns the set of per-repo record file names in a host's index
// directory, so a test can assert the on-disk file set equals the record set.
func indexFileSet(t *testing.T, root, cacheDir, host string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(oci.IndexBasePath(root, cacheDir, host))
	if err != nil {
		t.Fatalf("read host index dir: %v", err)
	}
	set := make(map[string]bool)
	for _, e := range entries {
		if !e.IsDir() {
			set[e.Name()] = true
		}
	}
	return set
}

// TestRegistryUpdateSingleRegistryByAlias proves the single-argument form
// resolves a friendly alias to its host and indexes only that registry.
func TestRegistryUpdateSingleRegistryByAlias(t *testing.T) {
	// Not parallel: mutates the process-global registryops.IndexTarget seam other tests read.
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
	if err := registryUpdateRun(t.Context(), registryUpdateConfig{ws: rw, Args: []string{"hub"}, Out: &out}); err != nil {
		t.Fatalf("registryUpdateRun (alias): %v", err)
	}

	cacheDir := rw.Config().ResolvedArtifactCacheDir()
	loaded, err := registryindex.LoadHost(root, cacheDir, host)
	if err != nil {
		t.Fatalf("loadHostIndex: %v", err)
	}
	if loaded.Host != host || len(loaded.Records) != 1 || loaded.Records[0].Version != "1.0.0" {
		t.Fatalf("single-alias index unexpected: host=%q records=%+v", loaded.Host, loaded.Records)
	}
}

// TestRegistryUpdateAllRegistriesPartialOnError proves the no-argument walk
// notes a host whose source resolution fails and continues to the next host:
// the reachable registry is still indexed and written, the unreachable one
// writes nothing, and the run returns a non-nil error summarizing the failure so
// the exit code stays non-zero. One unreachable registry must not abandon the
// reachable ones.
func TestRegistryUpdateAllRegistriesPartialOnError(t *testing.T) {
	// Not parallel: mutates the process-global registryops.IndexTarget seam other tests read.
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
	err := registryUpdateRun(t.Context(), registryUpdateConfig{ws: rw, Out: &out, now: frozen})
	if err == nil {
		t.Fatalf("registryUpdateRun (all, one host failing): got nil error; want non-nil summary error")
	}

	// The reachable registry is still indexed and written.
	cacheDir := rw.Config().ResolvedArtifactCacheDir()
	loaded, rerr := registryindex.LoadHost(root, cacheDir, goodHost)
	if rerr != nil {
		t.Fatalf("reachable registry index not readable: %v", rerr)
	}
	if loaded.Host != goodHost || len(loaded.Records) != 1 || loaded.Records[0].Version != "1.0.0" {
		t.Fatalf("reachable index unexpected: host=%q records=%+v", loaded.Host, loaded.Records)
	}

	// The unreachable registry wrote nothing.
	badDir := oci.IndexBasePath(root, cacheDir, badHost)
	if _, serr := os.Stat(badDir); !os.IsNotExist(serr) {
		t.Errorf("unreachable registry must not write an index; stat(%s) err=%v", badDir, serr)
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
	// Not parallel: mutates the process-global registryops.IndexTarget seam other tests read.
	const host = "hub.aggrega.ai"
	withIndexTargetFunc(t, func(workspaceconfig.Config, string) (registryIndexSource, error) {
		return nil, fmt.Errorf("unreachable")
	})

	root := t.TempDir()
	content := "workspace: {}\ndefault_registry: hub.aggrega.ai\n" +
		"registries:\n  - host: hub.aggrega.ai\n    name: hub\n"
	rw := resolveWorkspaceWithConfig(t, root, content)

	var out bytes.Buffer
	if err := registryUpdateRun(t.Context(), registryUpdateConfig{ws: rw, Args: []string{"hub"}, Out: &out}); err == nil {
		t.Fatalf("registryUpdateRun (single, failing host): got nil error; want non-nil")
	}

	cacheDir := rw.Config().ResolvedArtifactCacheDir()
	hostDir := oci.IndexBasePath(root, cacheDir, host)
	if _, serr := os.Stat(hostDir); !os.IsNotExist(serr) {
		t.Errorf("single-registry failure must write nothing; stat(%s) err=%v", hostDir, serr)
	}
}

// TestRegistryUpdateRecordsPerVersionDeprecations proves the update walk records
// a per-version deprecation marker discovered on a version's referrers into the
// per-repo record — only the deprecated version appears, the whole marker is
// preserved, and the record round-trips through the sparse loader byte-for-byte
// across two builds against the same state.
func TestRegistryUpdateRecordsPerVersionDeprecations(t *testing.T) {
	// Not parallel: mutates the process-global registryops.IndexTarget seam other tests read.
	const host = "hub.aggrega.ai"
	const repo = "component/core/api-server"

	meta := oci.SearchableMetadata{Description: "d", Category: "compute", Kind: "Component"}
	target := seedComponentRepo(t, []string{"1.0.0", "2.0.0"}, meta)

	// Attach a SIGNED deprecation marker to 1.0.0 only; 2.0.0 stays undeprecated.
	// The signature is required, not incidental: an unsigned marker is not recorded
	// at all, because a consumer reading this record offline — including the
	// resolution step that refuses to select a withdrawn version — must be acting on
	// a signed claim rather than an unauthenticated one.
	signer, content := keyedMarkerSigner(t)
	marker := deprecation.Marker{
		Severity:    deprecation.SeverityWarning,
		Reason:      "superseded by 2.0.0",
		Replacement: "oci://hub.aggrega.ai/component/core/api-server:2.0.0",
		IssuedOn:    "2026-05-01T00:00:00Z",
	}
	attachSignedMarkerToTag(t, target.Store, "1.0.0", marker, signer)

	src := fakeIndexSource{
		repos:   []string{repo},
		targets: map[string]oras.ReadOnlyTarget{repo: target},
	}
	withIndexTarget(t, src)

	root := t.TempDir()
	rw := resolveWorkspaceWithConfig(t, root, content)
	frozen := func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }

	var out bytes.Buffer
	if err := registryUpdateRun(t.Context(), registryUpdateConfig{ws: rw, Out: &out, now: frozen}); err != nil {
		t.Fatalf("registryUpdateRun: %v", err)
	}

	cacheDir := rw.Config().ResolvedArtifactCacheDir()
	loaded, err := registryindex.LoadHost(root, cacheDir, host)
	if err != nil {
		t.Fatalf("loadHostIndex: %v", err)
	}
	if len(loaded.Records) != 1 {
		t.Fatalf("records = %d; want 1", len(loaded.Records))
	}
	rec := loaded.Records[0]
	if len(rec.Deprecations) != 1 {
		// The walk's own notes name a marker it declined to honor, so a failure here
		// says WHY rather than only that the slice is empty.
		t.Fatalf("deprecations = %+v; want exactly the one deprecated version\nwalk output:\n%s", rec.Deprecations, out.String())
	}
	got := rec.Deprecations[0]
	if got.Version != "1.0.0" {
		t.Errorf("deprecated version = %q; want 1.0.0 (2.0.0 is not deprecated)", got.Version)
	}
	if got.Marker.Severity != deprecation.SeverityWarning ||
		got.Marker.Reason != marker.Reason ||
		got.Marker.Replacement != marker.Replacement ||
		got.Marker.IssuedOn != marker.IssuedOn {
		t.Errorf("marker round-trip mismatch: got %+v want %+v", got.Marker, marker)
	}
	// Recording implies verification: the record carries the verified signature of
	// the marker's own manifest digest, so no read site has to re-judge trust.
	if got.Signer == nil {
		t.Fatal("recorded marker carries no verified signature; recording must imply verification")
	}
	if got.Signer.Mode != "keyed" {
		t.Errorf("recorded signer mode = %q; want keyed for a key-pinned registry", got.Signer.Mode)
	}

	// The per-repo record file is byte-identical across two runs against the same
	// state (the marker's fixed field order keeps validators content-derived).
	recPath := oci.IndexRepoPath(root, cacheDir, host, repo)
	first, err := os.ReadFile(recPath)
	if err != nil {
		t.Fatalf("read record file: %v", err)
	}
	if err := registryUpdateRun(t.Context(), registryUpdateConfig{ws: rw, Out: &out, now: frozen}); err != nil {
		t.Fatalf("registryUpdateRun (second): %v", err)
	}
	second, err := os.ReadFile(recPath)
	if err != nil {
		t.Fatalf("re-read record file: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("record file not byte-identical across two runs:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

// TestRegistryUpdateConditionalReusesUnchangedRepo proves the digest-compare
// baseline: an unchanged repository (same tag set, same headline manifest digest)
// is reused from the prior index on the next refresh without a manifest-blob read.
func TestRegistryUpdateConditionalReusesUnchangedRepo(t *testing.T) {
	// Not parallel: mutates the process-global registryops.IndexTarget seam other tests read.
	const host = "hub.aggrega.ai"
	const repo = "component/core/api-server"
	meta := oci.SearchableMetadata{Description: "d", Category: "compute", Kind: "Component"}
	base := seedComponentRepo(t, []string{"1.0.0", "1.1.0"}, meta)
	fetches := 0
	target := &countingTarget{memTagTarget: base, fetches: &fetches}
	src := fakeIndexSource{repos: []string{repo}, targets: map[string]oras.ReadOnlyTarget{repo: target}}
	withIndexTarget(t, src)

	root := t.TempDir()
	rw := resolveWorkspaceWithConfig(t, root, updateTestConfig)
	frozen := frozenClock()
	var out bytes.Buffer

	// First run — full build; the manifest blob is read.
	if err := registryUpdateRun(t.Context(), registryUpdateConfig{ws: rw, Out: &out, now: frozen}); err != nil {
		t.Fatalf("registryUpdateRun (first): %v", err)
	}
	if fetches == 0 {
		t.Fatalf("first build fetched no content blob; want the manifest read")
	}

	// Second run — unchanged registry state; the conditional refresh must reuse the
	// prior record and read no content blob.
	fetches = 0
	if err := registryUpdateRun(t.Context(), registryUpdateConfig{ws: rw, Out: &out, now: frozen}); err != nil {
		t.Fatalf("registryUpdateRun (second): %v", err)
	}
	if fetches != 0 {
		t.Errorf("unchanged repo read %d content blobs on refresh; want 0 (digest-compare reuse)", fetches)
	}
}

// TestRegistryUpdateConditionalFromScratchParityAndFileSet proves a conditional
// refresh produces the same on-disk result as a from-scratch build, and that a
// dropped repository's file is removed and a new repository's file is created.
func TestRegistryUpdateConditionalFromScratchParityAndFileSet(t *testing.T) {
	// Not parallel: mutates the process-global registryops.IndexTarget seam other tests read.
	const host = "hub.aggrega.ai"
	meta := oci.SearchableMetadata{Description: "d", Category: "compute", Kind: "Component"}
	repoA := "component/core/api-server"
	repoB := "component/core/worker"

	src := fakeIndexSource{
		repos: []string{repoA, repoB},
		targets: map[string]oras.ReadOnlyTarget{
			repoA: seedComponentRepo(t, []string{"1.0.0"}, meta),
			repoB: seedComponentRepo(t, []string{"2.0.0"}, meta),
		},
	}
	withIndexTarget(t, src)
	root := t.TempDir()
	rw := resolveWorkspaceWithConfig(t, root, updateTestConfig)
	cacheDir := rw.Config().ResolvedArtifactCacheDir()
	frozen := frozenClock()
	var out bytes.Buffer

	// First (full) build, then a conditional refresh against unchanged state.
	if err := registryUpdateRun(t.Context(), registryUpdateConfig{ws: rw, Out: &out, now: frozen}); err != nil {
		t.Fatalf("registryUpdateRun (build): %v", err)
	}
	aBytes, err := os.ReadFile(oci.IndexRepoPath(root, cacheDir, host, repoA))
	if err != nil {
		t.Fatalf("read repoA record: %v", err)
	}
	if err := registryUpdateRun(t.Context(), registryUpdateConfig{ws: rw, Out: &out, now: frozen}); err != nil {
		t.Fatalf("registryUpdateRun (conditional): %v", err)
	}
	aBytes2, err := os.ReadFile(oci.IndexRepoPath(root, cacheDir, host, repoA))
	if err != nil {
		t.Fatalf("re-read repoA record: %v", err)
	}
	if !bytes.Equal(aBytes, aBytes2) {
		t.Errorf("conditional refresh not byte-identical to build for repoA")
	}
	if set := indexFileSet(t, root, cacheDir, host); len(set) != 2 {
		t.Fatalf("file set after refresh = %v; want 2 files", set)
	}

	// Drop repoB, add a new repoC — the file set must track the live enumeration.
	repoC := "component/core/gateway"
	src2 := fakeIndexSource{
		repos: []string{repoA, repoC},
		targets: map[string]oras.ReadOnlyTarget{
			repoA: seedComponentRepo(t, []string{"1.0.0"}, meta),
			repoC: seedComponentRepo(t, []string{"3.0.0"}, meta),
		},
	}
	withIndexTarget(t, src2)
	if err := registryUpdateRun(t.Context(), registryUpdateConfig{ws: rw, Out: &out, now: frozen}); err != nil {
		t.Fatalf("registryUpdateRun (drop/add): %v", err)
	}
	if _, serr := os.Stat(oci.IndexRepoPath(root, cacheDir, host, repoB)); !os.IsNotExist(serr) {
		t.Errorf("dropped repo %s must have no record file; stat err=%v", repoB, serr)
	}
	if _, serr := os.Stat(oci.IndexRepoPath(root, cacheDir, host, repoC)); serr != nil {
		t.Errorf("new repo %s must have a record file; stat err=%v", repoC, serr)
	}
}

// TestRegistryUpdatePicksUpADeprecationOnAnExistingTag proves a refresh without
// --upgrade records a deprecation marker attached to an existing version — no new
// tag, an unchanged manifest digest — and that it records exactly what a full
// --upgrade re-read records. This source offers no way to revalidate a version's
// referrers, so the refresh reads every version again; the conditional path that
// keeps an unchanged version is proven against a registry that does.
func TestRegistryUpdatePicksUpADeprecationOnAnExistingTag(t *testing.T) {
	// Not parallel: mutates the process-global registryops.IndexTarget seam other tests read.
	const host = "hub.aggrega.ai"
	const repo = "component/core/api-server"
	meta := oci.SearchableMetadata{Description: "d", Category: "compute", Kind: "Component"}
	target := seedComponentRepo(t, []string{"1.0.0"}, meta)
	src := fakeIndexSource{repos: []string{repo}, targets: map[string]oras.ReadOnlyTarget{repo: target}}
	withIndexTarget(t, src)

	root := t.TempDir()
	signer, content := keyedMarkerSigner(t)
	rw := resolveWorkspaceWithConfig(t, root, content)
	cacheDir := rw.Config().ResolvedArtifactCacheDir()
	frozen := frozenClock()
	var out bytes.Buffer

	// Initial build: 1.0.0 carries no marker.
	if err := registryUpdateRun(t.Context(), registryUpdateConfig{ws: rw, Out: &out, now: frozen}); err != nil {
		t.Fatalf("registryUpdateRun (build): %v", err)
	}

	// Attach a signed marker to the EXISTING 1.0.0 tag: this adds sibling referrers,
	// so neither the tag set nor 1.0.0's manifest digest changes.
	marker := deprecation.Marker{Severity: deprecation.SeverityWarning, Reason: "old", IssuedOn: "2026-05-01T00:00:00Z"}
	attachSignedMarkerToTag(t, target.Store, "1.0.0", marker, signer)

	// A refresh without --upgrade sees the new marker.
	if err := registryUpdateRun(t.Context(), registryUpdateConfig{ws: rw, Out: &out, now: frozen}); err != nil {
		t.Fatalf("registryUpdateRun (conditional): %v", err)
	}
	loaded, err := registryindex.LoadHost(root, cacheDir, host)
	if err != nil {
		t.Fatalf("loadHostIndex: %v", err)
	}
	if len(loaded.Records) != 1 || len(loaded.Records[0].Deprecations) != 1 ||
		loaded.Records[0].Deprecations[0].Version != "1.0.0" {
		t.Fatalf("a refresh without --upgrade missed the marker on the existing tag: %+v\nwalk output:\n%s", loaded.Records, out.String())
	}
	recPath := oci.IndexRepoPath(root, cacheDir, host, repo)
	refreshed, err := os.ReadFile(recPath)
	if err != nil {
		t.Fatalf("read record file: %v", err)
	}

	// A full --upgrade re-read records the same thing, byte for byte.
	if err := registryUpdateRun(t.Context(), registryUpdateConfig{ws: rw, Out: &out, now: frozen, Upgrade: true}); err != nil {
		t.Fatalf("registryUpdateRun (upgrade): %v", err)
	}
	upgraded, err := os.ReadFile(recPath)
	if err != nil {
		t.Fatalf("re-read record file: %v", err)
	}
	if !bytes.Equal(refreshed, upgraded) {
		t.Errorf("the refresh and the full re-read recorded different files:\nrefresh:\n%s\nupgrade:\n%s", refreshed, upgraded)
	}
}

// TestRegistryUpdateListingDigestRevalidates proves the client half of the
// conditional-GET round-trip: a build records the registry's listing validator, and
// a later refresh presents exactly that validator on the tags/list probe so an
// unchanged listing revalidates as not-modified and the record is reused.
func TestRegistryUpdateListingDigestRevalidates(t *testing.T) {
	// Not parallel: mutates the process-global registryops.IndexTarget seam other tests read.
	const host = "hub.aggrega.ai"
	const repo = "component/core/api-server"
	meta := oci.SearchableMetadata{Description: "d", Category: "compute", Kind: "Component"}
	tags := []string{"1.0.0", "1.1.0"}
	target := seedComponentRepo(t, tags, meta)
	base := fakeIndexSource{repos: []string{repo}, targets: map[string]oras.ReadOnlyTarget{repo: target}}

	root := t.TempDir()
	rw := resolveWorkspaceWithConfig(t, root, updateTestConfig)
	cacheDir := rw.Config().ResolvedArtifactCacheDir()
	frozen := frozenClock()
	var out bytes.Buffer

	// First build records the listing validator.
	withIndexTarget(t, base)
	if err := registryUpdateRun(t.Context(), registryUpdateConfig{ws: rw, Out: &out, now: frozen}); err != nil {
		t.Fatalf("registryUpdateRun (build): %v", err)
	}
	loaded, err := registryindex.LoadHost(root, cacheDir, host)
	if err != nil {
		t.Fatalf("loadHostIndex: %v", err)
	}
	if len(loaded.Records) != 1 {
		t.Fatalf("want 1 record, got %d", len(loaded.Records))
	}
	wantETag := syntheticTagsETag(tags)
	if loaded.Records[0].ListingDigest != wantETag {
		t.Fatalf("stored ListingDigest = %q; want %q", loaded.Records[0].ListingDigest, wantETag)
	}

	// The conditional refresh must present exactly that stored validator on its probe.
	var seen string
	spy := listingSpySource{fakeIndexSource: base, lastIfNoneMatch: &seen}
	withIndexTarget(t, spy)
	if err := registryUpdateRun(t.Context(), registryUpdateConfig{ws: rw, Out: &out, now: frozen}); err != nil {
		t.Fatalf("registryUpdateRun (refresh): %v", err)
	}
	if seen != wantETag {
		t.Fatalf("refresh presented If-None-Match %q; want the stored %q", seen, wantETag)
	}

	// The record is unchanged and still carries the validator.
	reloaded, err := registryindex.LoadHost(root, cacheDir, host)
	if err != nil {
		t.Fatalf("loadHostIndex (reload): %v", err)
	}
	if reloaded.Records[0].ListingDigest != wantETag || reloaded.Records[0].Version != "1.1.0" {
		t.Fatalf("post-refresh record drifted: %+v", reloaded.Records[0])
	}
}
