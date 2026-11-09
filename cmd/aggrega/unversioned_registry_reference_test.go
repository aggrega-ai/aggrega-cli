// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci/ocitest"
)

// The fixtures and the test in this file drive what a user meets who writes a
// Component or a Feature reference without its version — the form a Manifest key
// and an Aggregate type are commonly written in — when the entity is served by a
// registry and by no catalog directory: the pull resolves it, pins the version it
// resolved to, and caches it; the build, which fetches nothing, then reads it
// through that pin. Pull and build read one cache entry for it, so the pull's own
// render, the build, a verifying pull and a second pull all find what the first
// pull fetched.

// registryReferenceVersion is the one version every registry-only entity below is
// published at.
const registryReferenceVersion = "1.0.0"

// registryOnlyEntities are the Components and Features the registry serves, keyed
// by kind and identity. No catalog directory holds any of them.
var registryOnlyEntities = map[string]string{
	"Component example/compute/caller": `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: example/compute/caller
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
  usesSchema:
    peers:
      selector: "component.aggrega.ai/category=backend"
      featuresSchema:
        credential:
          description: The credential the caller presents to the peer this connection reaches.
`,
	"Component example/compute/peer": `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: example/compute/peer
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
    component.aggrega.ai/category: backend
spec:
  schema:
    values:
      message:
        type: string
        default: "peer"
`,
	"Feature example/token": `apiVersion: aggrega.ai/v1beta1
kind: Feature
metadata:
  name: example/token
  version: 1.0.0
spec:
  schema:
    values:
      key:
        type: string
        default: "token"
    provides:
      key:
        type: string
`,
	"Feature example/badge": `apiVersion: aggrega.ai/v1beta1
kind: Feature
metadata:
  name: example/badge
  version: 1.0.0
spec:
  schema:
    infra:
      issuer:
        type: string
        default: "internal"
    provides:
      issuer:
        type: string
`,
}

// registryReferenceRealizations are the Recipes and FeatureRecipes the Manifest
// binds, keyed by their place in a catalog laid out by kind and version. The
// in-process registry test keeps them in the workspace's catalog directory; the
// test against a real registry publishes them beside the entities above.
var registryReferenceRealizations = map[string]string{
	"recipe/compose/caller/1.0.0/recipe.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: caller
  version: 1.0.0
spec:
  capability: compose
  implements: example/compute/caller:1.0.0
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    containers:
      main:
        image: "caller:1"
        variables:
          MESSAGE: "{{ .values.message }}"
`,
	"recipe/compose/peer/1.0.0/recipe.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: peer
  version: 1.0.0
spec:
  capability: compose
  implements: example/compute/peer:1.0.0
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    containers:
      main:
        image: "peer:1"
        variables:
          MESSAGE: "{{ .values.message }}"
`,
	"feature-recipe/compose/token-plain/1.0.0/recipe.yaml": `apiVersion: aggrega.ai/v1beta1
kind: FeatureRecipe
metadata:
  name: token-plain
  version: 1.0.0
spec:
  capability: compose
  implements: example/token:1.0.0
  provides:
    key: "{{ .values.key }}"
`,
	"feature-recipe/compose/badge-plain/1.0.0/recipe.yaml": `apiVersion: aggrega.ai/v1beta1
kind: FeatureRecipe
metadata:
  name: badge-plain
  version: 1.0.0
spec:
  capability: compose
  implements: example/badge:1.0.0
  provides:
    issuer: "{{ .infra.issuer }}"
`,
}

// registryReferenceForms says how one row writes its references: the suffix each
// Component reference and each Feature reference carries, empty for a reference
// written without its version.
type registryReferenceForms struct {
	component string
	feature   string
}

// writtenBare returns every reference the row writes without its version.
func (f registryReferenceForms) writtenBare() []string {
	var bare []string
	if f.component == "" {
		bare = append(bare, "example/compute/caller", "example/compute/peer")
	}
	if f.feature == "" {
		bare = append(bare, "example/token", "example/badge")
	}
	return bare
}

// writtenBareKind is the published kind a reference writtenBare returns names,
// which its cache slot is kept under: the two Components, or else the two Features.
func writtenBareKind(uri string) string {
	if uri == "example/compute/caller" || uri == "example/compute/peer" {
		return "Component"
	}
	return "Feature"
}

// registryReferenceRelease writes the Aggregate, the Manifest and the Release
// under root, each reference in the form the row asks for, and returns the
// Release's path. The caller reaches each of two peers through one socket; the
// Aggregate attaches a credential Feature on the first connection, which the
// Manifest binds by the attachment's name and so writes nowhere else, and the
// Release attaches one on the second, which the Manifest binds by type.
func registryReferenceRelease(t *testing.T, root string, forms registryReferenceForms) string {
	t.Helper()
	c, f := forms.component, forms.feature
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: callers
  version: "1.0.0"
spec:
  components:
    caller:
      type: example/compute/caller`+c+`
      uses:
        peers:
          first:
            target: peer-a
            features:
              credential:
                type: example/token`+f+`
                values:
                  key: first-key
          second:
            target: peer-b
    peer-a:
      type: example/compute/peer`+c+`
    peer-b:
      type: example/compute/peer`+c+`
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: callers-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        example/compute/caller`+c+`: compose/caller:1.0.0
        example/compute/peer`+c+`: compose/peer:1.0.0
    features:
      type:
        example/badge`+f+`: compose/badge-plain:1.0.0
      name:
        first.credential: compose/token-plain:1.0.0
`)
	releasePath := filepath.Join(root, "release.yaml")
	writeFixture(t, releasePath, `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: callers-release
  environment: testing
  version: "1.0.0"
spec:
  aggregate: callers
  manifest: callers-manifest
  boundaries:
    app:
      directory: app
      components:
        caller:
          uses:
            peers:
              second:
                features:
                  credential:
                    type: example/badge`+f+`
                    infra:
                      issuer: partner
        peer-a: {}
        peer-b: {}
`)
	return releasePath
}

// registryReferenceRows are the shapes each test in this file runs over. Every
// row but the last writes at least one reference without its version, and the
// last writes every reference with it, as the control: it builds whether or not
// pull and build read one cache entry for a reference, so a row failing beside it
// fails on the form the row writes rather than on the fixture.
var registryReferenceRows = []struct {
	name  string
	forms registryReferenceForms
	// offlineBuild builds with the workspace switched offline after the pull, so the
	// build reads only what the pull left on the machine.
	offlineBuild bool
}{
	{
		name:  "a Component reference written without its version",
		forms: registryReferenceForms{component: "", feature: ":" + registryReferenceVersion},
	},
	{
		name:  "a Feature attached on a connection, written without its version",
		forms: registryReferenceForms{component: ":" + registryReferenceVersion, feature: ""},
	},
	{
		name:         "every reference without its version, pulled into an empty cache and built offline",
		forms:        registryReferenceForms{},
		offlineBuild: true,
	},
	{
		name:  "every reference with its version",
		forms: registryReferenceForms{component: ":" + registryReferenceVersion, feature: ":" + registryReferenceVersion},
	},
}

// inProcessEntityRegistry serves registryOnlyEntities through the kind-bound fetch
// a pull uses for a Component or a Feature, at registryReferenceVersion, whether
// the reference names no version or names that one. It counts every fetch by
// identity.
type inProcessEntityRegistry struct {
	mu      sync.Mutex
	fetched map[string]int
}

func (r *inProcessEntityRegistry) Pull(context.Context, string, string) (*oci.PullResult, error) {
	return nil, oci.ErrFetchNotFound()
}

func (r *inProcessEntityRegistry) PullEntity(_ context.Context, identity, kind, floorVersion string) (*oci.PullResult, string, error) {
	body, ok := registryOnlyEntities[kind+" "+identity]
	if !ok || (floorVersion != "" && floorVersion != registryReferenceVersion) {
		return nil, "", oci.ErrFetchNotFound()
	}
	mediaType, err := oci.MediaTypeFor(kind, false)
	if err != nil {
		return nil, "", err
	}
	manifest, manifestDigest, err := ociManifestArtifact(mediaType, []byte(body))
	if err != nil {
		return nil, "", err
	}
	r.mu.Lock()
	r.fetched[identity]++
	r.mu.Unlock()
	return &oci.PullResult{MediaType: mediaType, Bytes: []byte(body), Digest: manifestDigest, ManifestBytes: manifest}, registryReferenceVersion, nil
}

// fetches returns a copy of the fetch count by identity.
func (r *inProcessEntityRegistry) fetches() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]int, len(r.fetched))
	for k, v := range r.fetched {
		out[k] = v
	}
	return out
}

// assertPinnedAtTheResolvedVersion fails unless the lockfile at lockPath records
// every reference in bare under that bare form, at the version the registry
// serves, with the digests a registry-resolved row carries.
func assertPinnedAtTheResolvedVersion(t *testing.T, lockPath string, bare []string) {
	t.Helper()
	lf, _, err := lockfile.LoadLockfile(lockPath)
	if err != nil {
		t.Fatalf("load the lockfile: %v", err)
	}
	rows := map[string]lockfile.Entity{}
	for _, e := range lf.Entities {
		rows[e.URI] = e
	}
	for _, uri := range bare {
		e, ok := rows[uri]
		if !ok {
			t.Errorf("the lockfile records no row for %q, which the Release writes without its version", uri)
			continue
		}
		if e.ResolvedVersion != registryReferenceVersion || e.Digest == "" || e.ManifestDigest == "" {
			t.Errorf("the row for %q pins (version %q, digest %q, manifest digest %q); want version %s and both digests",
				uri, e.ResolvedVersion, e.Digest, e.ManifestDigest, registryReferenceVersion)
		}
	}
}

// renderStoppedMarkers are the words a pull prints when its own render of the
// Release, which is the build's, did not finish.
var renderStoppedMarkers = []string{"could not finish rendering", "not fully discovered"}

// TestAReferenceWrittenWithoutItsVersionPullsAndBuildsFromARegistry runs the
// pull and then the build on the same input, for each row, against a registry
// that is the only source of every Component and Feature the Release names.
//
// For each row the cold pull fetches from the registry and completes, its own
// render of the Release finishes, and the lockfile pins each reference written
// without its version under that form at the version the registry served; the
// build then completes — its lockfile verification passing over that lockfile —
// and renders both workloads; a verifying pull passes; and a second pull fetches
// nothing and rewrites nothing, because it finds every entity where the first
// pull cached it.
func TestAReferenceWrittenWithoutItsVersionPullsAndBuildsFromARegistry(t *testing.T) {
	t.Parallel()
	for _, row := range registryReferenceRows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			for rel, body := range registryReferenceRealizations {
				writeFixture(t, filepath.Join(root, "catalog", filepath.FromSlash(rel)), body)
			}
			releasePath := registryReferenceRelease(t, root, row.forms)
			const online = "workspace:\n  catalog_dirs: [catalog]\ndefault_registry: hub.aggrega.ai\n" + fakeRegistryTrust
			writeWorkspaceConfig(t, root, online)
			registry := &inProcessEntityRegistry{fetched: map[string]int{}}
			lockPath := lockfile.PathFor(releasePath)

			pull := func(verify bool) (string, error) {
				t.Helper()
				var diag, out bytes.Buffer
				err := pullRun(context.Background(), pullConfig{
					ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
					ReleasePath: releasePath,
					Verify:      verify,
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
			for key := range registryOnlyEntities {
				if identity := key[strings.Index(key, " ")+1:]; cold[identity] == 0 {
					t.Errorf("the pull did not fetch %s from the registry, so the row proves nothing about a registry-served entity", identity)
				}
			}
			assertPinnedAtTheResolvedVersion(t, lockPath, row.forms.writtenBare())
			first := mustReadFile(t, lockPath)

			if row.offlineBuild {
				writeWorkspaceConfig(t, root, online+"offline: true\n")
			}
			var buildDiag, buildOut bytes.Buffer
			if err := dispatchBuildInRoot(t, resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
				releasePath, nil, false, &buildDiag, &buildOut); err != nil {
				t.Fatalf("the build refused what its pull resolved: %v\n%s%s", err, buildDiag.String(), buildOut.String())
			}
			rendered := readDistTree(t, filepath.Join(root, "dist"))
			for _, image := range []string{"caller:1", "peer:1"} {
				if !strings.Contains(rendered, image) {
					t.Errorf("the build did not render %q; got:\n%s", image, rendered)
				}
			}
			if row.offlineBuild {
				writeWorkspaceConfig(t, root, online)
			}

			if text, err := pull(true); err != nil {
				t.Errorf("pull --verify over the lockfile the pull wrote: %v\n%s", err, text)
			}
			if text, err := pull(false); err != nil {
				t.Fatalf("second pull: %v\n%s", err, text)
			}
			for identity, n := range registry.fetches() {
				if n != cold[identity] {
					t.Errorf("a second pull fetched %s again (%d fetches, %d after the first pull): it did not find the entry the first pull cached",
						identity, n, cold[identity])
				}
			}
			if second := mustReadFile(t, lockPath); !bytes.Equal(second, first) {
				t.Errorf("a second pull over an unchanged workspace rewrote the lockfile\nfirst:\n%s\nsecond:\n%s", first, second)
			}
		})
	}
}

// TestARegistryFetchOfAReferenceWithoutItsVersionIsCachedWhereItsPinIsRead holds
// the cache entry itself: after the pull, an entity written without its version
// is cached under its reference at the version it resolved to — the one entry the
// build's read of its pin opens — and under nothing else.
func TestARegistryFetchOfAReferenceWithoutItsVersionIsCachedWhereItsPinIsRead(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for rel, body := range registryReferenceRealizations {
		writeFixture(t, filepath.Join(root, "catalog", filepath.FromSlash(rel)), body)
	}
	releasePath := registryReferenceRelease(t, root, registryReferenceForms{})
	writeWorkspaceConfig(t, root, "workspace:\n  catalog_dirs: [catalog]\ndefault_registry: hub.aggrega.ai\n"+fakeRegistryTrust)
	ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
	if err := pullRun(context.Background(), pullConfig{
		ws:          ws,
		ReleasePath: releasePath,
		DiagOut:     &bytes.Buffer{},
		Out:         &bytes.Buffer{},
		diag:        buildDiagOptions{quiet: true},
		puller:      &inProcessEntityRegistry{fetched: map[string]int{}},
	}); err != nil {
		t.Fatalf("pull: %v", err)
	}
	cacheDir := ws.Config().ResolvedArtifactCacheDir()
	for _, uri := range (registryReferenceForms{}).writtenBare() {
		pinned := ocitest.MustCachePath(root, cacheDir, writtenBareKind(uri), uri+":"+registryReferenceVersion, registryReferenceVersion)
		if _, err := os.Stat(filepath.Join(pinned, "blob")); err != nil {
			t.Errorf("%s is not cached where a read of its pin opens (%s): %v", uri, pinned, err)
		}
		if _, err := os.Stat(ocitest.MustCachePath(root, cacheDir, writtenBareKind(uri), uri, registryReferenceVersion)); err == nil {
			t.Errorf("%s is also cached under the reference alone, an entry no read of its pin opens", uri)
		}
	}
}
