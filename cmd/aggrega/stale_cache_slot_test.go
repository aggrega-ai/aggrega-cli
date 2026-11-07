// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci/ocitest"
)

// The fixtures below reproduce the shape an entity edited in place before its
// release leaves in a workspace: one name and one version, three bodies. The
// catalog directory the workspace reads first holds the body a pull pins; the
// content cache holds a complete slot — content and the manifest a registry
// served for it — with an older body of the same name and version, as a fetch
// made before the edit wrote it; and, where a row asks for it, a catalog
// directory read after the first declares a third body under the same identity,
// which the first shadows.
//
// Each body renders a different image tag, so the rendered output says which of
// the three a build read.

// staleSlotComponentURI is the reference the fixture's Aggregate names, bare, as
// an Aggregate component type is written.
const staleSlotComponentURI = "core/datastore/mariadb"

// staleSlotEntry is the cache slot's reference: the Component at the version every
// body declares.
const staleSlotEntry = staleSlotComponentURI + ":1.0.0"

// staleSlotPinnedBody is the body the first catalog directory holds and a pull
// pins. It sources part of its infra from a SchemaLibrary, so the lockfile pins a
// library as well as the Component, as the workspace this reproduces does.
const staleSlotPinnedBody = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/datastore/mariadb
  version: 1.0.0
  description: A MariaDB relational database, as the catalog directory now holds it.
  labels:
    component.aggrega.ai/type: stateful-server
spec:
  schema:
    values:
      database: { type: string, required: true }
    infra:
      engine_version: { type: string, default: "11.4" }
      storage:
        $ref: "DbSizing#/schemas/Storage"
`

// staleSlotCachedBody is the older body of the same name and version the cache
// slot holds.
const staleSlotCachedBody = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/datastore/mariadb
  version: 1.0.0
  description: A MariaDB relational database, as it was first fetched at this version.
  labels:
    component.aggrega.ai/type: stateful-server
spec:
  schema:
    values:
      database: { type: string, required: true }
      charset: { type: string, default: utf8mb4 }
    infra:
      engine_version: { type: string, default: "10.6" }
`

// staleSlotShadowedBody is the body a catalog directory read after the first
// declares under the same identity.
const staleSlotShadowedBody = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/datastore/mariadb
  version: 1.0.0
  description: A MariaDB relational database, as a later catalog directory declares it.
  labels:
    component.aggrega.ai/type: stateful-server
spec:
  schema:
    values:
      database: { type: string, required: true }
    infra:
      engine_version: { type: string, default: "12.0" }
`

// The image tag each body renders to.
const (
	staleSlotPinnedImage   = "image: mariadb:11.4"
	staleSlotCachedImage   = "image: mariadb:10.6"
	staleSlotShadowedImage = "image: mariadb:12.0"
)

// staleSlotOptions declares one fixture.
type staleSlotOptions struct {
	// mappingKey is the Manifest's mapping key for the Component. The versioned
	// form records a lockfile row under the versioned reference beside the
	// Aggregate's bare one; the bare form leaves the bare row alone, which is the
	// shape a reuse check keyed only by the versioned form cannot see a pin for.
	mappingKey string
	// shadowed adds the later catalog directory declaring the third body.
	shadowed bool
	// config is the workspace configuration's whole body.
	config string
	// leaveStderr runs the fixture's commands without capturing the process's
	// standard error, for a test that asserts nothing written there. Capturing it
	// replaces a process-wide stream, which a test running beside others may not do.
	leaveStderr bool
}

// staleSlotFixture is a workspace laid down by writeStaleSlotFixture.
type staleSlotFixture struct {
	root          string
	releasePath   string
	catalogDirs   []string
	componentPath string
	leaveStderr   bool
	// pinned and cached are the row digests a lockfile records for the pinned
	// body, as the catalog directory authors it, and for the body the stale slot
	// holds, as a registry published it: both over what the entity carries, so the
	// two are digests of one kind and a message naming both compares like with like.
	pinned string
	cached string
}

// writeStaleSlotFixture lays the workspace down. It runs no command: each test
// decides whether the stale slot exists before the first pull or appears after it.
func writeStaleSlotFixture(t *testing.T, opts staleSlotOptions) staleSlotFixture {
	t.Helper()
	root := t.TempDir()
	catalogDir := filepath.Join(root, "catalog")
	componentPath := filepath.Join(catalogDir, "component", "core", "datastore", "mariadb", "1.0.0", "component.yaml")
	writeFixture(t, componentPath, staleSlotPinnedBody)
	writeFixture(t, filepath.Join(catalogDir, "schemalibrary", "db-sizing.yaml"), dbSizingLibrarySource)
	writeHelmRendererPlugin(t, filepath.Join(catalogDir, "providers"))
	writeFixture(t, filepath.Join(catalogDir, "recipe", "helm", "mariadb-chart", "1.0.0", "recipe.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: mariadb-chart
  version: "1.0.0"
spec:
  capability: helm
  implements: core/datastore/mariadb
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: 1
    containers:
      db:
        image: "mariadb:{{ .infra.engine_version }}"
        ports:
          sql: 3306
`)
	catalogDirs := []string{catalogDir}
	if opts.shadowed {
		hubDir := filepath.Join(root, "hub")
		writeFixture(t, filepath.Join(hubDir, "component", "core", "datastore", "mariadb", "1.0.0", "component.yaml"), staleSlotShadowedBody)
		catalogDirs = append(catalogDirs, hubDir)
	}
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    db:
      type: core/datastore/mariadb
      values:
        database: app
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        `+opts.mappingKey+`: helm/mariadb-chart:1.0.0
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: rel
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        db:
          infra:
            storage:
              size: 20Gi
`)
	writeWorkspaceConfig(t, root, opts.config)
	return staleSlotFixture{
		root:          root,
		releasePath:   filepath.Join(root, "release.yaml"),
		catalogDirs:   catalogDirs,
		componentPath: componentPath,
		leaveStderr:   opts.leaveStderr,
		pinned:        rowDigestAt(t, componentPath),
		cached:        publishedRowDigest(t, []byte(staleSlotCachedBody)),
	}
}

// staleSlotRun is what one command over the fixture returned and wrote: its
// error, its diagnostic and status streams, and what the run wrote to the
// process's standard error. A build shows the catalog view's notices on the
// diagnostic writer it is handed; the pull command shows them on the process's
// standard error.
type staleSlotRun struct {
	err    error
	diag   string
	out    string
	stderr string
}

// pull runs the pull over the fixture — the verifying pull when verify is set —
// with puller as its registry tier: nil leaves the production puller, which an
// offline workspace builds with no registry tier at all.
func (f staleSlotFixture) pull(t *testing.T, verify bool, puller *staleSlotRegistry) staleSlotRun {
	t.Helper()
	var diag, out bytes.Buffer
	cfg := pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: f.root}),
		ReleasePath: f.releasePath,
		CatalogDirs: f.catalogDirs,
		Verify:      verify,
		DiagOut:     &diag,
		Out:         &out,
	}
	if puller != nil {
		cfg.puller = puller
	}
	var run staleSlotRun
	run.stderr = f.readingStderr(t, func() { run.err = pullRun(context.Background(), cfg) })
	run.diag, run.out = diag.String(), out.String()
	return run
}

// build runs the build over the fixture, writing the rendered tree under its root.
func (f staleSlotFixture) build(t *testing.T) staleSlotRun {
	t.Helper()
	var diag, out bytes.Buffer
	var run staleSlotRun
	run.stderr = f.readingStderr(t, func() {
		run.err = dispatchBuildInRoot(t, resolveTempWorkspace(t, tempWorkspaceOptions{Root: f.root}),
			f.releasePath, f.catalogDirs, false, &diag, &out)
	})
	run.diag, run.out = diag.String(), out.String()
	return run
}

// readingStderr runs fn and returns what it wrote to the process's standard
// error, or runs it and returns nothing when the fixture leaves that stream
// alone.
func (f staleSlotFixture) readingStderr(t *testing.T, fn func()) string {
	t.Helper()
	if f.leaveStderr {
		fn()
		return ""
	}
	return captureStderr(t, fn)
}

// layStaleSlot writes the older body into the Component's cache slot with the
// manifest a registry serves for it, as a fetch leaves a complete entry.
func (f staleSlotFixture) layStaleSlot(t *testing.T) {
	t.Helper()
	layFetchedSlot(t, f.root, staleSlotComponentURI, "1.0.0", []byte(staleSlotCachedBody), true)
}

// slotContent reads what the Component's cache slot holds.
func (f staleSlotFixture) slotContent(t *testing.T) string {
	t.Helper()
	cacheDir := resolveTempWorkspace(t, tempWorkspaceOptions{Root: f.root}).Config().ResolvedArtifactCacheDir()
	data, err := os.ReadFile(filepath.Join(ocitest.MustCachePath(f.root, cacheDir, "Component", staleSlotEntry, "1.0.0"), "blob"))
	if err != nil {
		t.Fatalf("read the cache slot: %v", err)
	}
	return string(data)
}

// pinnedDigest returns the digest the lockfile records for the Aggregate's bare
// reference to the Component.
func (f staleSlotFixture) pinnedDigest(t *testing.T) string {
	t.Helper()
	lf, _, err := lockfile.LoadLockfile(filepath.Join(f.root, "release.lock"))
	if err != nil {
		t.Fatalf("load the lockfile: %v", err)
	}
	for _, e := range lf.Entities {
		if e.URI == staleSlotComponentURI {
			return e.Digest
		}
	}
	t.Fatalf("the lockfile records no row for %q: %+v", staleSlotComponentURI, lf.Entities)
	return ""
}

// skipNotice is the notice the integrity gate prints for a stale slot it passes
// over, up to the path of the catalog-directory file it names.
func (f staleSlotFixture) skipNotice() string {
	return fmt.Sprintf("  reuse: cache entry for %s holds %s, not the pinned %s; passed over for ", staleSlotEntry, f.cached, f.pinned)
}

// staleSlotMappingKeys are the two Manifest shapes each test runs over.
var staleSlotMappingKeys = []struct {
	name string
	key  string
}{
	{name: "the Manifest maps the versioned reference", key: staleSlotEntry},
	{name: "the Manifest maps the bare reference", key: staleSlotComponentURI},
}

// TestAStaleCacheSlotIsPassedOverForTheCatalogDirectoryHoldingThePin holds the
// build and a pull's discovery of external inputs to the catalog directory when
// the cache holds an older body of the pinned entity's name and version. The
// catalog directory holds the pinned bytes, so both succeed from them, and the
// slot is passed over with a notice naming it and both digests — never read, and
// never rewritten. The later directory's shadowed body is never read either.
//
// The stale slot is laid before the first pull, as the workspace this reproduces
// held it: the pull pins the catalog directory's body, and its own discovery
// render then meets the slot.
func TestAStaleCacheSlotIsPassedOverForTheCatalogDirectoryHoldingThePin(t *testing.T) {
	// Not parallel: it reads the process's standard error by replacing that stream —
	// the pull's notice is shown there, and the build's must not be.
	for _, row := range staleSlotMappingKeys {
		t.Run(row.name, func(t *testing.T) {
			f := writeStaleSlotFixture(t, staleSlotOptions{mappingKey: row.key, shadowed: true, config: "offline: true\n"})
			f.layStaleSlot(t)
			// The file the notice names is the pinned body's, in the first catalog
			// directory; its path is matched from the directory down, so the check does
			// not depend on how the temporary root is spelled.
			wantHolder := filepath.Join("catalog", "component", "core", "datastore", "mariadb", "1.0.0", "component.yaml") +
				", which holds the pinned bytes"

			pull := f.pull(t, false, nil)
			if pull.err != nil {
				t.Fatalf("pull: %v\n%s", pull.err, pull.diag)
			}
			if strings.Contains(pull.diag, "could not finish rendering") || strings.Contains(pull.out, "not fully discovered") {
				t.Errorf("the pull's discovery of external inputs did not finish over a stale slot the catalog directory outranks:\n%s%s", pull.diag, pull.out)
			}
			if got := f.pinnedDigest(t); got != f.pinned {
				t.Errorf("the pull pinned %s; want the catalog directory's body %s", got, f.pinned)
			}
			if !strings.Contains(pull.stderr, f.skipNotice()) || !strings.Contains(pull.stderr, wantHolder) {
				t.Errorf("the pull did not name the stale slot it passed over; want %q … %q in:\n%s", f.skipNotice(), wantHolder, pull.stderr)
			}

			build := f.build(t)
			if build.err != nil {
				t.Fatalf("the build refused a stale slot the catalog directory holding the pinned bytes outranks: %v\n%s%s", build.err, build.diag, build.stderr)
			}
			if !strings.Contains(build.diag, f.skipNotice()) || !strings.Contains(build.diag, wantHolder) {
				t.Errorf("the build did not name the stale slot it passed over; want %q … %q in:\n%s", f.skipNotice(), wantHolder, build.diag)
			}
			if strings.Contains(build.stderr, f.skipNotice()) {
				t.Errorf("the build wrote its notice to the process's standard error rather than to the writer it was handed:\n%s", build.stderr)
			}
			rendered := readDistTree(t, filepath.Join(f.root, "dist"))
			if !strings.Contains(rendered, staleSlotPinnedImage) {
				t.Errorf("the build did not render the pinned body (%q); got:\n%s", staleSlotPinnedImage, rendered)
			}
			for _, other := range []string{staleSlotCachedImage, staleSlotShadowedImage} {
				if strings.Contains(rendered, other) {
					t.Errorf("the build rendered %q, a body the lockfile does not pin", other)
				}
			}

			if verify := f.pull(t, true, nil); verify.err != nil {
				t.Errorf("pull --verify: %v\n%s", verify.err, verify.diag)
			}
			if got := f.slotContent(t); got != staleSlotCachedBody {
				t.Errorf("the slot passed over was rewritten; it now holds:\n%s", got)
			}
		})
	}
}

// staleSlotRegistry is an in-process registry serving the pinned body under the
// Component's name and version and nothing else. It answers both fetch forms a
// pull can use for an entity: the kind-bound one and the already-located one.
type staleSlotRegistry struct {
	fetched int
}

func (r *staleSlotRegistry) serve() (*oci.PullResult, error) {
	body := []byte(staleSlotPinnedBody)
	manifest, manifestDigest, err := ociManifestArtifact(oci.MediaTypeComponent, body)
	if err != nil {
		return nil, err
	}
	r.fetched++
	return &oci.PullResult{MediaType: oci.MediaTypeComponent, Bytes: body, Digest: manifestDigest, ManifestBytes: manifest}, nil
}

func (r *staleSlotRegistry) Pull(_ context.Context, reference, tag string) (*oci.PullResult, error) {
	if reference != staleSlotComponentURI || tag != "1.0.0" {
		return nil, oci.ErrFetchNotFound()
	}
	return r.serve()
}

func (r *staleSlotRegistry) PullEntity(_ context.Context, identity, kind, floorVersion string) (*oci.PullResult, string, error) {
	if identity != staleSlotComponentURI || kind != "Component" || floorVersion != "1.0.0" {
		return nil, "", oci.ErrFetchNotFound()
	}
	res, err := r.serve()
	if err != nil {
		return nil, "", err
	}
	return res, floorVersion, nil
}

// TestAStaleCacheSlotIsFetchedAgainWhenOnlyARegistryHoldsThePin removes the
// catalog directory's copy and serves the pinned body from an in-process
// registry. The pull passes the stale slot over, fetches the pinned version
// again, rewrites the slot with it and says so, naming both digests; the pin is
// unchanged, and the build then reads the pinned body from the refreshed slot.
func TestAStaleCacheSlotIsFetchedAgainWhenOnlyARegistryHoldsThePin(t *testing.T) {
	t.Parallel()
	for _, row := range staleSlotMappingKeys {
		t.Run(row.name, func(t *testing.T) {
			f := writeStaleSlotFixture(t, staleSlotOptions{mappingKey: row.key, config: "default_registry: hub.aggrega.ai\n" + fakeRegistryTrust, leaveStderr: true})
			registry := &staleSlotRegistry{}
			if seed := f.pull(t, false, registry); seed.err != nil {
				t.Fatalf("seed pull: %v\n%s", seed.err, seed.diag)
			}
			if registry.fetched != 0 {
				t.Fatalf("the seed pull fetched from the registry although the catalog directory holds the entity")
			}
			f.layStaleSlot(t)
			if err := os.Remove(f.componentPath); err != nil {
				t.Fatalf("remove the catalog directory's copy: %v", err)
			}

			pull := f.pull(t, false, registry)
			if pull.err != nil {
				t.Fatalf("the pull refused a stale slot the registry holds the pinned bytes for: %v\n%s", pull.err, pull.diag)
			}
			if registry.fetched == 0 {
				t.Fatalf("the pull did not fetch the pinned version again")
			}
			want := fmt.Sprintf("  reuse: cache entry for %s held %s, not the pinned %s; refreshed from the registry (pin unchanged)",
				staleSlotEntry, f.cached, f.pinned)
			if !strings.Contains(pull.diag, want) {
				t.Errorf("the pull did not name the slot it refreshed and both digests; want %q in:\n%s", want, pull.diag)
			}
			if strings.Contains(pull.diag, "could not finish rendering") {
				t.Errorf("the pull's discovery of external inputs did not finish:\n%s", pull.diag)
			}
			if got := f.pinnedDigest(t); got != f.pinned {
				t.Errorf("the pin moved to %s; want it held at %s", got, f.pinned)
			}
			if got := f.slotContent(t); got != staleSlotPinnedBody {
				t.Errorf("the slot was not rewritten with the pinned body; it holds:\n%s", got)
			}

			if build := f.build(t); build.err != nil {
				t.Fatalf("build over the refreshed slot: %v\n%s%s", build.err, build.diag, build.stderr)
			}
			if rendered := readDistTree(t, filepath.Join(f.root, "dist")); !strings.Contains(rendered, staleSlotPinnedImage) {
				t.Errorf("the build did not render the pinned body (%q); got:\n%s", staleSlotPinnedImage, rendered)
			}
		})
	}
}

// TestAPinNoSourceHoldsHaltsNamingEverySourceItRead removes the catalog
// directory's copy offline, leaving the stale slot as the only source of the
// entity. No source holds the pinned bytes, so the build, the verifying pull and
// the pull each halt with the integrity condition, naming the pin, the slot and
// the digest it holds, and that no catalog directory holds the entity — never
// with the slot's bytes, and never as a reference nothing resolves, since a slot
// of its name and version exists.
func TestAPinNoSourceHoldsHaltsNamingEverySourceItRead(t *testing.T) {
	for _, row := range staleSlotMappingKeys {
		t.Run(row.name, func(t *testing.T) {
			f := writeStaleSlotFixture(t, staleSlotOptions{mappingKey: row.key, config: "offline: true\n"})
			if seed := f.pull(t, false, nil); seed.err != nil {
				t.Fatalf("seed pull: %v\n%s", seed.err, seed.diag)
			}
			f.layStaleSlot(t)
			if err := os.Remove(f.componentPath); err != nil {
				t.Fatalf("remove the catalog directory's copy: %v", err)
			}
			lockPath := filepath.Join(f.root, "release.lock")
			before, err := os.ReadFile(lockPath)
			if err != nil {
				t.Fatalf("read the lockfile: %v", err)
			}

			wantAll := []string{
				fmt.Sprintf("pinned entity %s: no source holds the pinned bytes %s", staleSlotComponentURI, f.pinned),
				fmt.Sprintf("the cache entry for %s holds %s, not the pinned %s", staleSlotEntry, f.cached, f.pinned),
				fmt.Sprintf("no catalog directory this workspace reads holds %s", staleSlotEntry),
			}
			// The pull runs last because it is the one command that could rewrite the
			// lockfile; neither the build nor the verifying pull writes it.
			for _, c := range []struct {
				name string
				err  error
				also string
			}{
				{name: "build", err: f.build(t).err},
				{name: "pull --verify", err: f.pull(t, true, nil).err, also: "this run reaches no registry"},
				{name: "pull", err: f.pull(t, false, nil).err, also: "this run reaches no registry"},
			} {
				if c.err == nil {
					t.Errorf("%s completed although no source holds the pinned bytes", c.name)
					continue
				}
				if !errors.Is(c.err, oci.ErrFetchIntegrity()) {
					t.Errorf("%s: want the integrity condition; got %v", c.name, c.err)
				}
				want := wantAll
				if c.also != "" {
					want = append(append([]string{}, wantAll...), c.also)
				}
				for _, w := range want {
					if !chainContains(c.err, w) {
						t.Errorf("%s: the halt does not say %q:\n%v", c.name, w, c.err)
					}
				}
			}
			if rendered := readDistTree(t, filepath.Join(f.root, "dist")); strings.Contains(rendered, staleSlotCachedImage) {
				t.Errorf("a render read the stale slot's body")
			}
			after, err := os.ReadFile(lockPath)
			if err != nil {
				t.Fatalf("read the lockfile after the commands: %v", err)
			}
			if !bytes.Equal(before, after) {
				t.Errorf("a command that halted rewrote the lockfile")
			}
		})
	}
}

// TestACacheSlotHoldingThePinStillStandsInForARemovedEntity is the converse's
// guard: with the catalog directory's copy removed and a complete slot holding
// exactly the pinned bytes, the slot stands in for it — the verifying pull, the
// build and the pull all complete offline, the build renders the pinned body, and
// no command reports the slot as one it passed over.
func TestACacheSlotHoldingThePinStillStandsInForARemovedEntity(t *testing.T) {
	// Not parallel: it reads the process's standard error by replacing that stream,
	// because the build's notices must reach neither the writer it is handed nor
	// that stream. The laid slot stands in for a hub fetch that recorded no signature.
	f := writeStaleSlotFixture(t, staleSlotOptions{mappingKey: staleSlotEntry, config: "offline: true\n" + fakeRegistryTrust})
	if seed := f.pull(t, false, nil); seed.err != nil {
		t.Fatalf("seed pull: %v\n%s", seed.err, seed.diag)
	}
	layFetchedSlot(t, f.root, staleSlotComponentURI, "1.0.0", []byte(staleSlotPinnedBody), true)
	if err := os.Remove(f.componentPath); err != nil {
		t.Fatalf("remove the catalog directory's copy: %v", err)
	}

	if verify := f.pull(t, true, nil); verify.err != nil {
		t.Errorf("pull --verify refused a slot holding the pinned bytes: %v\n%s%s", verify.err, verify.diag, verify.stderr)
	}
	build := f.build(t)
	if build.err != nil {
		t.Fatalf("the build refused a slot holding the pinned bytes: %v\n%s%s", build.err, build.diag, build.stderr)
	}
	if strings.Contains(build.diag+build.stderr, "passed over") {
		t.Errorf("the build passed over a slot holding the pinned bytes:\n%s%s", build.diag, build.stderr)
	}
	if rendered := readDistTree(t, filepath.Join(f.root, "dist")); !strings.Contains(rendered, staleSlotPinnedImage) {
		t.Errorf("the build did not render the pinned body (%q); got:\n%s", staleSlotPinnedImage, rendered)
	}
	if pull := f.pull(t, false, nil); pull.err != nil {
		t.Errorf("pull refused a slot holding the pinned bytes: %v\n%s", pull.err, pull.diag)
	}
	if got := f.pinnedDigest(t); got != f.pinned {
		t.Errorf("the pin moved to %s; want %s", got, f.pinned)
	}
}
