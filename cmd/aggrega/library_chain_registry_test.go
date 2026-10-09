// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/refcycle"
)

// The fixtures and the tests in this file drive what a user meets whose Feature
// reaches a schema library only through another library — the Feature's schema
// references one library, that library's entry composes a second, and so on — when
// every library in the chain is served by a registry and by no catalog directory.
// The pull resolves the whole chain, however deep, and pins every library in it;
// the build, which fetches nothing, reads each through its pin. A chain that loops
// back on itself halts the pull naming the loop, exactly as it does when the
// libraries sit in a catalog directory.

// libraryChainVersion is the one version every registry-served entity below is
// published at.
const libraryChainVersion = "1.0.0"

// libraryChainLibrary returns a SchemaLibrary named name whose Settings entry
// composes next's Settings through a cross-document $ref and adds prop, or, where
// next is empty, declares prop alone. Each prop defaults to "from-<name>".
func libraryChainLibrary(name, next, prop string) string {
	head := fmt.Sprintf(`apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: %s
  version: %s
spec:
  schemas:
    Settings:
`, name, libraryChainVersion)
	if next == "" {
		return head + fmt.Sprintf(`      type: object
      properties:
        %s:
          type: string
          default: "from-%s"
`, prop, name)
	}
	return head + fmt.Sprintf(`      $ref: "%s#/schemas/Settings"
      properties:
        %s:
          type: string
          default: "from-%s"
`, next, prop, name)
}

// libraryChainLibraries are the libraries the registry serves, keyed by name. ChainA
// composes ChainB, which composes ChainC, which alone declares level — the value the
// FeatureRecipe delivers, so a build renders it only once the whole chain resolved.
// LoopA and LoopB compose each other.
var libraryChainLibraries = map[string]string{
	"ChainA": libraryChainLibrary("ChainA", "ChainB", "top"),
	"ChainB": libraryChainLibrary("ChainB", "ChainC", "middle"),
	"ChainC": libraryChainLibrary("ChainC", "", "level"),
	"LoopA":  libraryChainLibrary("LoopA", "LoopB", "level"),
	"LoopB":  libraryChainLibrary("LoopB", "LoopA", "other"),
}

// libraryChainComponent is the Component every row's Release deploys.
const libraryChainComponent = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: example/compute/app
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
    component.aggrega.ai/category: compute
spec:
  schema:
    values:
      message:
        type: string
        default: "hello"
`

// libraryChainFeature returns a Feature named name whose values schema is the
// Settings entry of library.
func libraryChainFeature(name, library string) string {
	return fmt.Sprintf(`apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: %s
  version: %s
spec:
  schema:
    values:
      $ref: "%s#/schemas/Settings"
`, name, libraryChainVersion, library)
}

// libraryChainRow is one shape the tests run: the Feature the Release attaches, the
// library its schema names, and every library the chain reaches from there.
type libraryChainRow struct {
	name    string
	feature string
	first   string
	reached []string
}

// short is the Feature's name without its namespace, which names its FeatureRecipe.
func (r libraryChainRow) short() string {
	_, s, _ := strings.Cut(r.feature, "/")
	return s
}

// libraryChainRows are the chains a build resolves. The first row names the
// library carrying level directly, as the control: it builds whether or not a
// library reached only through another one resolves, so a row failing beside it
// fails on the depth of its chain rather than on the fixture.
var libraryChainRows = []libraryChainRow{
	{name: "a Feature naming the library directly", feature: "example/one-hop", first: "ChainC", reached: []string{"ChainC"}},
	{name: "a library reached through one other library", feature: "example/two-hops", first: "ChainB", reached: []string{"ChainB", "ChainC"}},
	{name: "a library reached through two other libraries", feature: "example/three-hops", first: "ChainA", reached: []string{"ChainA", "ChainB", "ChainC"}},
}

// libraryChainCycleRow is the chain that loops back on itself.
var libraryChainCycleRow = libraryChainRow{name: "a cycle", feature: "example/looped", first: "LoopA", reached: []string{"LoopA", "LoopB"}}

// libraryChainCycle is the loop a halt over libraryChainCycleRow names.
const libraryChainCycle = "LoopA#/schemas/Settings -> LoopB#/schemas/Settings -> LoopA#/schemas/Settings"

// libraryChainRealizations returns the Recipe and the row's FeatureRecipe, keyed by
// their place in a catalog laid out by kind and version. The FeatureRecipe delivers
// the attachment's level into the Component's environment, and the Recipe renders it.
func libraryChainRealizations(row libraryChainRow) map[string]string {
	return map[string]string{
		"recipe/compose/app/1.0.0/recipe.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: app
  version: 1.0.0
spec:
  capability: compose
  implements: example/compute/app:1.0.0
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    containers:
      main:
        image: "app:1"
        variables:
          MESSAGE: "{{ .values.message }}"
          LEVEL: "{{ .context.inject.env.LEVEL }}"
`,
		"feature-recipe/compose/" + row.short() + "-env/1.0.0/recipe.yaml": fmt.Sprintf(`apiVersion: aggrega.ai/v1alpha1
kind: FeatureRecipe
metadata:
  name: %s-env
  version: 1.0.0
spec:
  capability: compose
  implements: %s:1.0.0
  injectMapping: |
    env:
      LEVEL: "{{ .values.level }}"
`, row.short(), row.feature),
	}
}

// libraryChainEntities returns the row's registry-published entities and libraries,
// keyed by their place in a catalog laid out by kind and version.
func libraryChainEntities(row libraryChainRow) map[string]string {
	out := map[string]string{
		"component/example/compute/app/1.0.0/component.yaml": libraryChainComponent,
		"feature/" + row.feature + "/1.0.0/feature.yaml":     libraryChainFeature(row.feature, row.first),
	}
	for _, name := range row.reached {
		out["schemalibrary/"+strings.ToLower(name)+"/1.0.0/library.yaml"] = libraryChainLibraries[name]
	}
	return out
}

// libraryChainRelease writes the Aggregate, the Manifest and the Release under root,
// attaching the row's Feature to the one Component, and returns the Release's path.
func libraryChainRelease(t *testing.T, root string, row libraryChainRow) string {
	t.Helper()
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: chained
  version: "1.0.0"
spec:
  components:
    app:
      type: example/compute/app:1.0.0
      features:
        settings:
          type: `+row.feature+`:1.0.0
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: chained-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        example/compute/app:1.0.0: compose/app:1.0.0
    features:
      type:
        `+row.feature+`:1.0.0: compose/`+row.short()+`-env:1.0.0
`)
	releasePath := filepath.Join(root, "release.yaml")
	writeFixture(t, releasePath, `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: chained-release
  environment: testing
  version: "1.0.0"
spec:
  aggregate: chained
  manifest: chained-manifest
  components:
    app: {}
`)
	return releasePath
}

// inProcessLibraryChainRegistry serves the Component, the Features and the
// libraries through the kind-bound fetches a pull uses — the entity fetch for a
// Component or a Feature, the library fetch for a SchemaLibrary — at
// libraryChainVersion, whether the reference names no version or names that one.
// It counts every fetch by identity.
type inProcessLibraryChainRegistry struct {
	mu        sync.Mutex
	entities  map[string]string // "<Kind> <identity>" → body
	libraries map[string]string // library name → body
	fetched   map[string]int
}

func newLibraryChainRegistry(rows ...libraryChainRow) *inProcessLibraryChainRegistry {
	r := &inProcessLibraryChainRegistry{
		entities:  map[string]string{"Component example/compute/app": libraryChainComponent},
		libraries: map[string]string{},
		fetched:   map[string]int{},
	}
	for _, row := range rows {
		r.entities["Feature "+row.feature] = libraryChainFeature(row.feature, row.first)
		for _, name := range row.reached {
			r.libraries[name] = libraryChainLibraries[name]
		}
	}
	return r
}

func (r *inProcessLibraryChainRegistry) Pull(context.Context, string, string) (*oci.PullResult, error) {
	return nil, oci.ErrFetchNotFound()
}

func (r *inProcessLibraryChainRegistry) PullEntity(_ context.Context, identity, kind, floorVersion string) (*oci.PullResult, string, error) {
	body, ok := r.entities[kind+" "+identity]
	if !ok || (floorVersion != "" && floorVersion != libraryChainVersion) {
		return nil, "", oci.ErrFetchNotFound()
	}
	mediaType, err := oci.MediaTypeFor(kind, false)
	if err != nil {
		return nil, "", err
	}
	return r.serve(identity, mediaType, body)
}

func (r *inProcessLibraryChainRegistry) PullLibrary(_ context.Context, name, kind, floorVersion string) (*oci.PullResult, string, error) {
	body, ok := r.libraries[name]
	if !ok || kind != "SchemaLibrary" || (floorVersion != "" && floorVersion != libraryChainVersion) {
		return nil, "", oci.ErrFetchNotFound()
	}
	return r.serve(name, oci.MediaTypeSchemaLibrary, body)
}

func (r *inProcessLibraryChainRegistry) serve(identity, mediaType, body string) (*oci.PullResult, string, error) {
	manifest, manifestDigest, err := ociManifestArtifact(mediaType, []byte(body))
	if err != nil {
		return nil, "", err
	}
	r.mu.Lock()
	r.fetched[identity]++
	r.mu.Unlock()
	return &oci.PullResult{MediaType: mediaType, Bytes: []byte(body), Digest: manifestDigest, ManifestBytes: manifest}, libraryChainVersion, nil
}

// fetches returns a copy of the fetch count by identity.
func (r *inProcessLibraryChainRegistry) fetches() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]int, len(r.fetched))
	for k, v := range r.fetched {
		out[k] = v
	}
	return out
}

// resolutionSourceOf returns the source a pull's --detail table reports for the
// entry of kind named name, and "" where the table has no such entry.
func resolutionSourceOf(detail, kind, name string) string {
	for _, line := range strings.Split(detail, "\n") {
		if fields := strings.Fields(line); len(fields) >= 3 && fields[1] == kind && fields[2] == name {
			return fields[0]
		}
	}
	return ""
}

// assertChainPinned fails unless the lockfile at lockPath pins exactly the
// libraries reached, each at the version the registry serves with both digests a
// registry-resolved row carries, each library after the first recorded as consumed
// by the one before it.
func assertChainPinned(t *testing.T, lockPath string, row libraryChainRow) {
	t.Helper()
	lf, _, err := lockfile.LoadLockfile(lockPath)
	if err != nil {
		t.Fatalf("load the lockfile: %v", err)
	}
	pinned := map[string]lockfile.SchemaLibrary{}
	for _, lib := range lf.SchemaLibraries {
		pinned[lib.URI] = lib
	}
	if len(pinned) != len(row.reached) {
		var got []string
		for uri := range pinned {
			got = append(got, uri)
		}
		t.Errorf("the lockfile pins the schema libraries %v; want exactly %v", got, row.reached)
	}
	for i, name := range row.reached {
		lib, ok := pinned[name]
		if !ok {
			t.Errorf("the lockfile does not pin %s, which the chain reaches", name)
			continue
		}
		if lib.ResolvedVersion != libraryChainVersion || lib.Digest == "" || lib.ManifestDigest == "" {
			t.Errorf("%s is pinned at (version %q, digest %q, manifest digest %q); want version %s and both digests",
				name, lib.ResolvedVersion, lib.Digest, lib.ManifestDigest, libraryChainVersion)
		}
		consumer := row.feature
		if i > 0 {
			consumer = row.reached[i-1]
		}
		var consumedBy []string
		for _, c := range lib.Consumers {
			consumedBy = append(consumedBy, c.ConsumerURI)
		}
		if len(consumedBy) != 1 || consumedBy[0] != consumer {
			t.Errorf("%s is recorded as consumed by %v; want %s, the entry that composes it", name, consumedBy, consumer)
		}
	}
}

// TestALibraryReachedThroughAnotherResolvesFromARegistryAtAnyDepth runs the pull
// and then the build on the same input, for each row, against a registry that is
// the only source of the Component, the Feature and every library in the chain.
//
// For each row the cold pull fetches every library in the chain and reports each
// as fetched from the registry, its own render of the Release finishes, and the
// lockfile pins every library in the chain with the edge that reached it; the build
// then completes — its lockfile verification passing over that lockfile — and
// renders the value only the last library of the chain declares; a verifying pull
// passes; and a second pull fetches nothing, reports every library as reused from
// the cache, and rewrites nothing.
func TestALibraryReachedThroughAnotherResolvesFromARegistryAtAnyDepth(t *testing.T) {
	t.Parallel()
	for _, row := range libraryChainRows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			for rel, body := range libraryChainRealizations(row) {
				writeFixture(t, filepath.Join(root, "catalog", filepath.FromSlash(rel)), body)
			}
			releasePath := libraryChainRelease(t, root, row)
			writeWorkspaceConfig(t, root, "workspace:\n  catalog_dirs: [catalog]\ndefault_registry: hub.aggrega.ai\n"+fakeRegistryTrust)
			registry := newLibraryChainRegistry(row)
			lockPath := lockfile.PathFor(releasePath)

			pull := func(verify bool) (string, error) {
				t.Helper()
				var diag, out bytes.Buffer
				err := pullRun(context.Background(), pullConfig{
					ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
					ReleasePath: releasePath,
					Verify:      verify,
					Detail:      !verify,
					DiagOut:     &diag,
					Out:         &out,
					diag:        buildDiagOptions{quiet: true},
					puller:      registry,
				})
				return diag.String() + out.String(), err
			}

			text, err := pull(false)
			if err != nil {
				t.Fatalf("pull: %v\n%s", err, text)
			}
			for _, marker := range renderStoppedMarkers {
				if strings.Contains(text, marker) {
					t.Errorf("the pull's own render of the Release did not finish (%q):\n%s", marker, text)
				}
			}
			cold := registry.fetches()
			for _, name := range row.reached {
				if cold[name] != 1 {
					t.Errorf("the pull fetched %s %d times from the registry; want once", name, cold[name])
				}
				if got := resolutionSourceOf(text, "SchemaLibrary", name); got != "fetched-fresh" {
					t.Errorf("the pull reports %s as %q; want fetched-fresh, since this pull fetched it from the registry:\n%s", name, got, text)
				}
			}
			assertChainPinned(t, lockPath, row)
			first := mustReadFile(t, lockPath)

			var buildDiag, buildOut bytes.Buffer
			if err := dispatchBuildInRoot(t, resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
				releasePath, nil, false, &buildDiag, &buildOut); err != nil {
				t.Fatalf("the build refused what its pull resolved: %v\n%s%s", err, buildDiag.String(), buildOut.String())
			}
			if rendered := readDistTree(t, filepath.Join(root, "dist")); !strings.Contains(rendered, "LEVEL: from-ChainC") {
				t.Errorf("the build did not render the value the chain's last library declares (LEVEL: from-ChainC); got:\n%s", rendered)
			}

			if text, err := pull(true); err != nil {
				t.Errorf("pull --verify over the lockfile the pull wrote: %v\n%s", err, text)
			}
			text, err = pull(false)
			if err != nil {
				t.Fatalf("second pull: %v\n%s", err, text)
			}
			for identity, n := range registry.fetches() {
				if n != cold[identity] {
					t.Errorf("a second pull fetched %s again (%d fetches, %d after the first pull)", identity, n, cold[identity])
				}
			}
			for _, name := range row.reached {
				if got := resolutionSourceOf(text, "SchemaLibrary", name); got != "cache-reuse" {
					t.Errorf("the second pull reports %s as %q; want cache-reuse:\n%s", name, got, text)
				}
			}
			if second := mustReadFile(t, lockPath); !bytes.Equal(second, first) {
				t.Errorf("a second pull over an unchanged workspace rewrote the lockfile\nfirst:\n%s\nsecond:\n%s", first, second)
			}
		})
	}
}

// TestALibraryCycleServedByARegistryHaltsAsItDoesFromACatalog drives a chain that
// loops back on itself, once with every library and the Feature served by the
// registry and once with them in the workspace's catalog directory. Both pulls halt
// on the loop itself, naming it, rather than on a library the walk failed to reach.
func TestALibraryCycleServedByARegistryHaltsAsItDoesFromACatalog(t *testing.T) {
	t.Parallel()
	row := libraryChainCycleRow
	for _, tc := range []struct {
		name string
		// local places the libraries, the Feature and the Component in the catalog
		// directory instead of the registry.
		local bool
	}{
		{name: "served by a registry"},
		{name: "authored in a catalog directory", local: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			catalogFiles := libraryChainRealizations(row)
			registry := newLibraryChainRegistry(row)
			if tc.local {
				for rel, body := range libraryChainEntities(row) {
					catalogFiles[rel] = body
				}
				registry = newLibraryChainRegistry()
			}
			for rel, body := range catalogFiles {
				writeFixture(t, filepath.Join(root, "catalog", filepath.FromSlash(rel)), body)
			}
			releasePath := libraryChainRelease(t, root, row)
			writeWorkspaceConfig(t, root, "workspace:\n  catalog_dirs: [catalog]\ndefault_registry: hub.aggrega.ai\n"+fakeRegistryTrust)

			var diag, out bytes.Buffer
			err := pullRun(context.Background(), pullConfig{
				ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
				ReleasePath: releasePath,
				DiagOut:     &diag,
				Out:         &out,
				diag:        buildDiagOptions{quiet: true},
				puller:      registry,
			})
			if err == nil {
				t.Fatalf("the pull accepted a chain of libraries that loops back on itself:\n%s%s", diag.String(), out.String())
			}
			printed := formatCompilerError(err)
			if !errors.Is(err, refcycle.ErrCycle()) {
				t.Errorf("the pull halted, but not on the loop:\n%s", printed)
			}
			if !strings.Contains(printed, libraryChainCycle) {
				t.Errorf("the halt does not name the loop %q:\n%s", libraryChainCycle, printed)
			}
			if !tc.local {
				fetched := registry.fetches()
				for _, name := range row.reached {
					if fetched[name] == 0 {
						t.Errorf("the pull never fetched %s, so the loop it reports was not reached through the registry", name)
					}
				}
			}
		})
	}
}
