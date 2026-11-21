// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// A pull records, in each reference's lockfile row, the version every constraint
// declared on the reference's instances admits, and a build from that lockfile
// compiles at the row and refuses a row the constraints do not admit.
//
// The workspaces are the quickstart copies of the instance-version fixtures: the
// Aggregate writes the server with no version, the Manifest maps it at 1.0.0, and
// the server is held at 1.0.0 and 1.1.0 — in the fixtures' catalog directory, or,
// for the registry cases, by a registry alone.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// instanceVersionsServer is the Component every case here resolves.
const instanceVersionsServer = "core/compute/server"

// instanceVersionsServerVersions are the versions of the server the fixtures hold.
var instanceVersionsServerVersions = []string{"1.0.0", "1.1.0"}

// versionRegistry serves the server Component at each version the fixtures hold,
// read from the fixtures' catalog, through the kind-bound fetch and the tag
// listing a pull reads a registry through. It records every document it serves,
// by identity and version, and every listing it answers, by identity.
type versionRegistry struct {
	bodies map[string][]byte
	// listErr, where set, is what every listing answers with in place of the
	// versions.
	listErr error

	mu      sync.Mutex
	fetched map[string]int
	listed  map[string]int
}

// newVersionRegistry reads the server's documents out of the fixtures' catalog.
func newVersionRegistry(t *testing.T) *versionRegistry {
	t.Helper()
	r := &versionRegistry{bodies: map[string][]byte{}, fetched: map[string]int{}, listed: map[string]int{}}
	for _, version := range instanceVersionsServerVersions {
		r.bodies[version] = mustReadFile(t, filepath.Join("testdata", "instance_versions", "catalog", "component",
			filepath.FromSlash(instanceVersionsServer), version, "component.yaml"))
	}
	return r
}

func (r *versionRegistry) Pull(context.Context, string, string) (*oci.PullResult, error) {
	return nil, oci.ErrFetchNotFound()
}

// PullEntity serves the server at the version asked for, and at the highest
// version it holds for a request naming none, as a registry's tag listing selects.
func (r *versionRegistry) PullEntity(_ context.Context, identity, kind, floorVersion string) (*oci.PullResult, string, error) {
	if kind != "Component" || identity != instanceVersionsServer {
		return nil, "", oci.ErrFetchNotFound()
	}
	version := floorVersion
	if version == "" {
		version = instanceVersionsServerVersions[len(instanceVersionsServerVersions)-1]
	}
	body, ok := r.bodies[version]
	if !ok {
		return nil, "", oci.ErrFetchNotFound()
	}
	mediaType, err := oci.MediaTypeFor(kind, false)
	if err != nil {
		return nil, "", err
	}
	manifest, manifestDigest, err := ociManifestArtifact(mediaType, body)
	if err != nil {
		return nil, "", err
	}
	r.mu.Lock()
	r.fetched[identity+":"+version]++
	r.mu.Unlock()
	return &oci.PullResult{MediaType: mediaType, Bytes: body, Digest: manifestDigest, ManifestBytes: manifest}, version, nil
}

// ListEntityVersions lists the versions the registry publishes the server at.
func (r *versionRegistry) ListEntityVersions(_ context.Context, identity, kind string) ([]string, error) {
	r.mu.Lock()
	r.listed[identity]++
	r.mu.Unlock()
	if r.listErr != nil {
		return nil, r.listErr
	}
	if kind != "Component" || identity != instanceVersionsServer {
		return nil, oci.ErrFetchNotFound()
	}
	return slices.Clone(instanceVersionsServerVersions), nil
}

// fetches returns how many times the registry served the server at version.
func (r *versionRegistry) fetches(version string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fetched[instanceVersionsServer+":"+version]
}

// listings returns how many times the registry listed the server's versions.
func (r *versionRegistry) listings() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.listed[instanceVersionsServer]
}

// registryInstanceVersionsWorkspace lays the named workspace down as
// instanceVersionsWorkspace does, then removes the server from the catalog
// directory and points the workspace at a registry, so the registry is the only
// source of the server.
func registryInstanceVersionsWorkspace(t *testing.T, workspace string) string {
	t.Helper()
	root := instanceVersionsWorkspace(t, workspace)
	if err := os.RemoveAll(filepath.Join(root, "catalog", "component", filepath.FromSlash(instanceVersionsServer))); err != nil {
		t.Fatalf("remove the server from the catalog directory: %v", err)
	}
	writeWorkspaceConfig(t, root, "workspace:\n  catalog_dirs:\n    - ./catalog\ndefault_registry: hub.aggrega.ai\n"+fakeRegistryTrust)
	return root
}

// pullInstanceVersions pulls the Release at releasePath in the workspace at root,
// reading puller as its registry where it is not nil, and returns the pull's
// verdict beside what it wrote.
func pullInstanceVersions(t *testing.T, root, releasePath string, puller catalog.OCIPuller) (string, error) {
	t.Helper()
	return pullInstanceVersionsVerifying(t, root, releasePath, puller, false)
}

// pullInstanceVersionsVerifying is pullInstanceVersions, verifying the lockfile
// beside the Release rather than writing one where verify is set.
func pullInstanceVersionsVerifying(t *testing.T, root, releasePath string, puller catalog.OCIPuller, verify bool) (string, error) {
	t.Helper()
	var diag, out bytes.Buffer
	err := pullRun(context.Background(), pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		Verify:      verify,
		DiagOut:     &diag,
		Out:         &out,
		diag:        buildDiagOptions{quiet: true},
		puller:      puller,
	})
	return diag.String() + out.String(), err
}

// assertPullHoldsItsLockfile fails unless the lockfile a pull wrote at releasePath
// is what a pull of the same workspace finds again: a verifying pull passes it,
// and a second pull, honouring its pins, rewrites it byte for byte apart from when
// it was written.
func assertPullHoldsItsLockfile(t *testing.T, root, releasePath string, puller catalog.OCIPuller) {
	t.Helper()
	lockPath := lockfile.PathFor(releasePath)
	first := mustReadFile(t, lockPath)
	if text, err := pullInstanceVersionsVerifying(t, root, releasePath, puller, true); err != nil {
		t.Errorf("pull --verify over the lockfile the pull wrote: %v\n%s", err, text)
	}
	if text, err := pullInstanceVersions(t, root, releasePath, puller); err != nil {
		t.Fatalf("second pull: %v\n%s", err, text)
	}
	if second := mustReadFile(t, lockPath); !bytes.Equal(stripGeneratedAt(first), stripGeneratedAt(second)) {
		t.Errorf("a second pull over an unchanged workspace rewrote the lockfile\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

// serverRow returns the lockfile row at lockPath recording the server as the
// Aggregate writes it, with no version.
func serverRow(t *testing.T, lockPath string) lockfile.Entity {
	t.Helper()
	lf, _, err := lockfile.LoadLockfile(lockPath)
	if err != nil {
		t.Fatalf("load the lockfile: %v", err)
	}
	for _, e := range lf.Entities {
		if e.Kind == closure.KindComponent && e.URI == instanceVersionsServer {
			return e
		}
	}
	t.Fatalf("the lockfile at %s records no row for %s", lockPath, instanceVersionsServer)
	return lockfile.Entity{}
}

// lockedBuild runs the build's own computation over the Release at root, held to
// the lockfile beside it — the view pinned by its rows and the drift verify on, as
// `aggrega build` runs it — and returns the result beside its refusal.
func lockedBuild(t *testing.T, root string) (*compilerops.PreviewResult, error) {
	t.Helper()
	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve the workspace: %v", err)
	}
	releasePath := filepath.Join(root, "release.yaml")
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		t.Fatalf("load the lockfile: %v", err)
	}
	dirs, err := rw.ResolveCatalogDirs(releasePath, nil)
	if err != nil {
		t.Fatalf("resolve the catalog directories: %v", err)
	}
	view := catalog.NewLazyLookup(rw.Root(), rw.Config().ResolvedArtifactCacheDir(), dirs, nil)
	pins, err := catalog.PinsFromLockfile(lf)
	if err != nil {
		t.Fatalf("pin the view to the lockfile: %v", err)
	}
	view.SetPins(pins)
	return compilerops.PreviewForArtifact(context.Background(),
		compilerops.NewPreviewInputs(compilerops.NoExternalInputs(), view, releasePath, rw.Root(), rw.Config(),
			provenanceCompilerVersion(), compilerops.WithLockfile(lf, true)),
		pipeline.Options{SkipRender: true})
}

// compiledVersion returns the version the build compiled the node named node at.
func compiledVersion(t *testing.T, preview *compilerops.PreviewResult, node string) string {
	t.Helper()
	result := preview.PipelineOutcome().Completed()
	if result == nil || result.IRGraph == nil {
		t.Fatal("the build drew no graph")
	}
	n, ok := result.IRGraph.Spec.Nodes.Get(node)
	if !ok {
		t.Fatalf("the graph holds no node %q; it holds %v", node, result.IRGraph.Spec.Nodes.Keys())
	}
	return n.ResolvedVersion
}

// mapServerByName rewrites the quickstart workspace at root so the server is
// reached by a name-axis entry rather than a type-axis key: the name says nothing
// about a type, so the Recipe's implements is the only constraint beside the type.
func mapServerByName(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, "manifest.yaml")
	replaceInFixture(t, path, "        core/compute/server:1.0.0: compose/server:1.0.0\n", "")
	replaceInFixture(t, path, "    features:\n", "      name:\n        web: compose/server:1.0.0\n    features:\n")
}

// TestPullRecordsInstanceVersion holds a pull to recording, in the server's row,
// the version the constraints on its instances admit — 1.0.0 — though a bare
// request resolves to 1.1.0: from a catalog directory, for a module's instance,
// and from a registry. From a registry the pull lists the server's versions and
// fetches only the version it records, except where judging a candidate needs
// that candidate's extends ancestry, which only its document holds: a type-axis
// key decides 1.1.0 outright, while for an instance a name maps, the Recipe's
// implements is judged through the candidate's ancestry.
func TestPullRecordsInstanceVersion(t *testing.T) {
	t.Parallel()

	for _, c := range []struct{ name, workspace string }{
		{name: "from a catalog directory", workspace: "quickstart-web"},
		{name: "a module's instance from a catalog directory", workspace: "quickstart-site"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			root := instanceVersionsWorkspace(t, c.workspace)
			releasePath := filepath.Join(root, "release.yaml")
			if text, err := pullInstanceVersions(t, root, releasePath, nil); err != nil {
				t.Fatalf("pull: %v\n%s", err, text)
			}
			if row := serverRow(t, lockfile.PathFor(releasePath)); row.ResolvedVersion != "1.0.0" {
				t.Errorf("the pull records %s at %q, want 1.0.0", instanceVersionsServer, row.ResolvedVersion)
			}
			assertPullHoldsItsLockfile(t, root, releasePath, nil)
		})
	}

	for _, c := range []struct {
		name          string
		byName        bool
		ancestryFetch int
	}{
		{name: "from a registry, mapped by a type-axis key"},
		{name: "from a registry, mapped by name", byName: true, ancestryFetch: 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			root := registryInstanceVersionsWorkspace(t, "quickstart-web")
			if c.byName {
				mapServerByName(t, root)
			}
			registry := newVersionRegistry(t)
			releasePath := filepath.Join(root, "release.yaml")
			if text, err := pullInstanceVersions(t, root, releasePath, registry); err != nil {
				t.Fatalf("pull: %v\n%s", err, text)
			}
			row := serverRow(t, lockfile.PathFor(releasePath))
			if row.ResolvedVersion != "1.0.0" || row.ManifestDigest == "" {
				t.Errorf("the pull records %s at %q (manifest digest %q), want 1.0.0 as a registry row",
					instanceVersionsServer, row.ResolvedVersion, row.ManifestDigest)
			}
			if registry.listings() == 0 {
				t.Error("the pull never listed the server's versions at the registry")
			}
			if n := registry.fetches("1.0.0"); n != 1 {
				t.Errorf("the registry served the server at 1.0.0 %d times, want once", n)
			}
			if n := registry.fetches("1.1.0"); n != c.ancestryFetch {
				t.Errorf("the registry served the server at 1.1.0 %d times, want %d", n, c.ancestryFetch)
			}
			assertPullHoldsItsLockfile(t, root, releasePath, registry)
			if n := registry.fetches("1.0.0"); n != 1 {
				t.Errorf("the pulls after the first fetched the server at 1.0.0 again (%d fetches in all)", n)
			}
		})
	}
}

// TestPullReselectsAPinItsConstraintsMovedFrom holds a pull reconciling against
// the lockfile an earlier pull wrote to re-selecting a registry pin the constraints
// no longer admit, rather than holding it: once the Manifest maps the server at
// 1.1.0, the second pull records 1.1.0, as the pull that finds no lockfile would.
func TestPullReselectsAPinItsConstraintsMovedFrom(t *testing.T) {
	t.Parallel()
	root := registryInstanceVersionsWorkspace(t, "quickstart-web")
	registry := newVersionRegistry(t)
	releasePath := filepath.Join(root, "release.yaml")
	if text, err := pullInstanceVersions(t, root, releasePath, registry); err != nil {
		t.Fatalf("pull: %v\n%s", err, text)
	}
	replaceInFixture(t, filepath.Join(root, "manifest.yaml"),
		"core/compute/server:1.0.0: compose/server:1.0.0", "core/compute/server:1.1.0: compose/server:2.0.0")
	if text, err := pullInstanceVersions(t, root, releasePath, registry); err != nil {
		t.Fatalf("the pull over the lockfile pinning 1.0.0: %v\n%s", err, text)
	}
	if row := serverRow(t, lockfile.PathFor(releasePath)); row.ResolvedVersion != "1.1.0" {
		t.Errorf("the pull records %s at %q, want 1.1.0", instanceVersionsServer, row.ResolvedVersion)
	}
}

// TestPullConflictWritesNoLockfile holds a pull of a Release whose server is pinned
// at 1.0.0 while the Manifest maps it at 1.1.0 to halting with the conflict
// between the two, writing no lockfile, from a catalog directory and from a
// registry.
func TestPullConflictWritesNoLockfile(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name     string
		root     func(*testing.T) string
		registry bool
	}{
		{name: "from a catalog directory", root: func(t *testing.T) string { return instanceVersionsWorkspace(t, "quickstart-web") }},
		{name: "from a registry", registry: true, root: func(t *testing.T) string {
			return registryInstanceVersionsWorkspace(t, "quickstart-web")
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			root := c.root(t)
			pinServerAgainstNewerKey(t, root)
			var puller catalog.OCIPuller
			if c.registry {
				puller = newVersionRegistry(t)
			}
			releasePath := filepath.Join(root, "release.yaml")
			text, err := pullInstanceVersions(t, root, releasePath, puller)
			if !errors.Is(err, catalog.ErrConflictingVersionConstraints()) {
				t.Fatalf("the pull halts with %v; want the conflicting-version-constraints condition\n%s", err, text)
			}
			if _, serr := os.Stat(lockfile.PathFor(releasePath)); !errors.Is(serr, os.ErrNotExist) {
				t.Errorf("the pull that halted left a lockfile behind (%v)", serr)
			}
			// The versions are named in words true of a catalog directory and of a
			// registry alike.
			if msg := err.Error(); !strings.Contains(msg, `versions held "1.0.0, 1.1.0"`) || strings.Contains(msg, "catalog holds") {
				t.Errorf("the conflict does not name the versions held in words true of where they are held:\n%s", msg)
			}
		})
	}
}

// TestBuildRefusesLockedVersionOutsideConstraints holds a build from a lockfile to
// the row: with the row the pull wrote it compiles the server at 1.0.0; with the
// row moved to 1.1.0 — the row a pull of another Manifest records, copied in as a
// hand edit would — it halts with the conflict, naming the lockfile among the
// sources, where a registry holds the server; and where a catalog directory holds
// it, the build selects again and reports the lockfile out of date.
func TestBuildRefusesLockedVersionOutsideConstraints(t *testing.T) {
	t.Parallel()

	t.Run("from a registry", func(t *testing.T) {
		t.Parallel()
		root := registryInstanceVersionsWorkspace(t, "quickstart-web")
		registry := newVersionRegistry(t)
		releasePath := filepath.Join(root, "release.yaml")
		if text, err := pullInstanceVersions(t, root, releasePath, registry); err != nil {
			t.Fatalf("pull: %v\n%s", err, text)
		}
		preview, err := lockedBuild(t, root)
		if err != nil {
			t.Fatalf("the build refused the lockfile its pull wrote: %v", err)
		}
		if got := compiledVersion(t, preview, "web"); got != "1.0.0" {
			t.Errorf("the build compiled web at %q, want the row's 1.0.0", got)
		}

		newer := newerServerRow(t, root, registry)
		moveServerRow(t, lockfile.PathFor(releasePath), newer)

		_, err = lockedBuild(t, root)
		var conflict interface{ Message() string }
		if !errors.Is(err, catalog.ErrConflictingVersionConstraints()) || !errors.As(err, &conflict) {
			t.Fatalf("the build halts with %v; want the conflicting-version-constraints condition", err)
		}
		if errors.Is(err, lockfile.ErrLockfileDrift()) {
			t.Errorf("the build reports the lockfile out of date beside the conflict: %v", err)
		}
		for _, value := range []string{`locked at "1.1.0"`, `written in "release.lock"`, `Manifest mapping key "core/compute/server:1.0.0"`, "`aggrega pull --release release.yaml`"} {
			if !strings.Contains(conflict.Message(), value) {
				t.Errorf("the conflict does not name %s:\n%s", value, conflict.Message())
			}
		}
	})

	t.Run("from a catalog directory", func(t *testing.T) {
		t.Parallel()
		root := instanceVersionsWorkspace(t, "quickstart-web")
		releasePath := filepath.Join(root, "release.yaml")
		if text, err := pullInstanceVersions(t, root, releasePath, nil); err != nil {
			t.Fatalf("pull: %v\n%s", err, text)
		}
		preview, err := lockedBuild(t, root)
		if err != nil {
			t.Fatalf("the build refused the lockfile its pull wrote: %v", err)
		}
		if got := compiledVersion(t, preview, "web"); got != "1.0.0" {
			t.Errorf("the build compiled web at %q, want the row's 1.0.0", got)
		}

		row := serverRow(t, lockfile.PathFor(releasePath))
		row.ResolvedVersion = "1.1.0"
		moveServerRow(t, lockfile.PathFor(releasePath), row)

		_, err = lockedBuild(t, root)
		if !errors.Is(err, lockfile.ErrLockfileDrift()) {
			t.Fatalf("the build halts with %v; want the lockfile reported out of date", err)
		}
		if errors.Is(err, catalog.ErrConflictingVersionConstraints()) {
			t.Errorf("the build reports the conflict beside the out-of-date lockfile: %v", err)
		}
	})
}

// newerServerRow pulls, in a directory of the workspace at root, a copy of its
// Release whose Manifest maps the server at 1.1.0, and returns the row that pull
// records for the server — a row naming 1.1.0, whose document the pull leaves in
// the workspace's cache.
func newerServerRow(t *testing.T, root string, registry *versionRegistry) lockfile.Entity {
	t.Helper()
	dir := filepath.Join(root, "newer")
	for _, name := range []string{"aggregate.yaml", "manifest.yaml", "release.yaml"} {
		writeFixture(t, filepath.Join(dir, name), string(mustReadFile(t, filepath.Join(root, name))))
	}
	replaceInFixture(t, filepath.Join(dir, "manifest.yaml"),
		"core/compute/server:1.0.0: compose/server:1.0.0", "core/compute/server:1.1.0: compose/server:2.0.0")
	releasePath := filepath.Join(dir, "release.yaml")
	if text, err := pullInstanceVersions(t, root, releasePath, registry); err != nil {
		t.Fatalf("pull the Release mapping the server at 1.1.0: %v\n%s", err, text)
	}
	row := serverRow(t, lockfile.PathFor(releasePath))
	if row.ResolvedVersion != "1.1.0" {
		t.Fatalf("the Release mapping the server at 1.1.0 records it at %q", row.ResolvedVersion)
	}
	return row
}

// moveServerRow rewrites the server's row of the lockfile at lockPath to the
// version and digests of row, and leaves every other row as it is.
func moveServerRow(t *testing.T, lockPath string, row lockfile.Entity) {
	t.Helper()
	lf, _, err := lockfile.LoadLockfile(lockPath)
	if err != nil {
		t.Fatalf("load the lockfile: %v", err)
	}
	moved := false
	for i, e := range lf.Entities {
		if e.Kind == closure.KindComponent && e.URI == instanceVersionsServer {
			lf.Entities[i].ResolvedVersion = row.ResolvedVersion
			lf.Entities[i].Digest = row.Digest
			lf.Entities[i].ManifestDigest = row.ManifestDigest
			moved = true
		}
	}
	if !moved {
		t.Fatalf("the lockfile at %s records no row for %s", lockPath, instanceVersionsServer)
	}
	if err := lockfile.WriteLockfilePreservingTimestamp(lockPath, lf); err != nil {
		t.Fatalf("write the lockfile: %v", err)
	}
}

// TestClosureGatesReadTheSelectedVersion holds the gates a pull and a build run
// over the closure — the extends bases the closure's Components reach, the pillar
// and extends validation of its entities — to reading each reference at the
// version its row records. The server at 1.1.0 extends a base no catalog holds, so
// a gate reading the highest version of the bare reference refuses the Release,
// while the version the constraints select, 1.0.0, extends nothing.
func TestClosureGatesReadTheSelectedVersion(t *testing.T) {
	t.Parallel()
	root := instanceVersionsWorkspace(t, "quickstart-web")
	writeFixture(t, filepath.Join(root, "catalog", "component", filepath.FromSlash(instanceVersionsServer), "1.1.0", "component.yaml"),
		`apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/compute/server
  version: 1.1.0
  labels:
    component.aggrega.ai/type: server
spec:
  extends: core/compute/absent-base:1.0.0
  schema: inherited
`)
	releasePath := filepath.Join(root, "release.yaml")

	if text, err := pullInstanceVersions(t, root, releasePath, nil); err != nil {
		t.Fatalf("pull: %v\n%s", err, text)
	}
	if row := serverRow(t, lockfile.PathFor(releasePath)); row.ResolvedVersion != "1.0.0" {
		t.Errorf("the pull records %s at %q, want 1.0.0", instanceVersionsServer, row.ResolvedVersion)
	}
	if _, err := lockedBuild(t, root); err != nil {
		t.Errorf("the build refused the Release: %v", err)
	}
	resolved, vr := instanceVersionsCoreValidate(t, root)
	if findings := coreFindings(resolved, vr); len(findings) > 0 {
		t.Errorf("the validation refused the Release:\n%v", errors.Join(findings...))
	}
}

// writeSharedServerWorkspace rewrites the module workspace at root so the server is
// written twice — by the Release's own Aggregate as web, and by the module it
// includes as site.web — and each instance is mapped by name: web to webRecipe and
// site.web to the Recipe implementing 1.0.0. A Recipe implementing the server at
// any 1.x version, compose/server-any, is added to the catalog directory.
func writeSharedServerWorkspace(t *testing.T, root, webRecipe string) {
	t.Helper()
	recipeDir := filepath.Join(root, "catalog", "recipe", "compose")
	anyRecipe := filepath.Join(recipeDir, "server-any", "1.0.0", "recipe.yaml")
	writeFixture(t, anyRecipe, string(mustReadFile(t, filepath.Join(recipeDir, "server", "1.0.0", "recipe.yaml"))))
	replaceInFixture(t, anyRecipe, "  name: server\n", "  name: server-any\n")
	replaceInFixture(t, anyRecipe, "implements: core/compute/server:1.0.0", "implements: core/compute/server:^1.0.0")
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: quickstart-site-host
  version: "1.0.0"
spec:
  components:
    web:
      type: core/compute/server
  aggregates:
    site:
      type: example/quickstart-site
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: quickstart-site-compose
  version: "1.0.0"
spec:
  mappings:
    components:
      name:
        web: `+webRecipe+`
        site.web: compose/server:1.0.0
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: quickstart-site-local
  environment: development
  version: "1.0.0"
spec:
  aggregate: quickstart-site-host
  manifest: quickstart-site-compose
  boundaries:
    local:
      directory: ./dist/local
      components:
        web:
          infra:
            image: registry.example.invalid/quickstart-web:1.0.0
            port: 8080
        site.web:
          infra:
            image: registry.example.invalid/quickstart-site:1.0.0
            port: 8081
`)
}

// TestPullSettlesAReferenceOverEveryInstance holds the row of a reference written
// both by the Release's own Aggregate and by a module it includes to the one
// version every instance writing it admits, which is the version the build
// compiles each of them at. Mapped alone, web admits 1.0.0 and 1.1.0, and site.web
// 1.0.0 only, so the row records 1.0.0; mapped to Recipes implementing 1.1.0 and
// 1.0.0, the two admit no version together and the pull halts with the conflict,
// writing no lockfile — from a catalog directory and from a registry alike.
func TestPullSettlesAReferenceOverEveryInstance(t *testing.T) {
	t.Parallel()
	for _, source := range []struct {
		name     string
		registry bool
	}{{name: "from a catalog directory"}, {name: "from a registry", registry: true}} {
		workspace := func(t *testing.T) (string, catalog.OCIPuller) {
			t.Helper()
			if !source.registry {
				return instanceVersionsWorkspace(t, "quickstart-site"), nil
			}
			return registryInstanceVersionsWorkspace(t, "quickstart-site"), newVersionRegistry(t)
		}
		t.Run(source.name+", the instances agree", func(t *testing.T) {
			t.Parallel()
			root, puller := workspace(t)
			writeSharedServerWorkspace(t, root, "compose/server-any:1.0.0")
			releasePath := filepath.Join(root, "release.yaml")
			if text, err := pullInstanceVersions(t, root, releasePath, puller); err != nil {
				t.Fatalf("pull: %v\n%s", err, text)
			}
			if row := serverRow(t, lockfile.PathFor(releasePath)); row.ResolvedVersion != "1.0.0" {
				t.Errorf("the pull records %s at %q, want 1.0.0, the one version both instances admit", instanceVersionsServer, row.ResolvedVersion)
			}
			preview, err := lockedBuild(t, root)
			if err != nil {
				t.Fatalf("the build refused the lockfile its pull wrote: %v", err)
			}
			for _, node := range []string{"web", "site.web"} {
				if got := compiledVersion(t, preview, node); got != "1.0.0" {
					t.Errorf("the build compiled %s at %q, want the row's 1.0.0", node, got)
				}
			}
			assertPullHoldsItsLockfile(t, root, releasePath, puller)
			if !source.registry {
				// A validation reads the closure at the version it compiles too.
				resolved, vr := instanceVersionsCoreValidate(t, root)
				if findings := coreFindings(resolved, vr); len(findings) > 0 {
					t.Errorf("the validation refused the Release:\n%v", errors.Join(findings...))
				}
				for _, e := range resolved.Closure.Entities {
					if e.Kind() == closure.KindComponent && e.URI == instanceVersionsServer && e.ResolvedVersion != "1.0.0" {
						t.Errorf("the validation's closure records %s at %q, want 1.0.0", instanceVersionsServer, e.ResolvedVersion)
					}
				}
			}
		})
		t.Run(source.name+", the instances cannot agree", func(t *testing.T) {
			t.Parallel()
			root, puller := workspace(t)
			writeSharedServerWorkspace(t, root, "compose/server:2.0.0")
			releasePath := filepath.Join(root, "release.yaml")
			text, err := pullInstanceVersions(t, root, releasePath, puller)
			if !errors.Is(err, catalog.ErrConflictingVersionConstraints()) {
				t.Fatalf("the pull halts with %v; want the conflicting-version-constraints condition\n%s", err, text)
			}
			for _, instance := range []string{`instance "web"`, `instance "site.web"`} {
				if !strings.Contains(err.Error(), instance) {
					t.Errorf("the conflict does not name %s:\n%v", instance, err)
				}
			}
			if _, serr := os.Stat(lockfile.PathFor(releasePath)); !errors.Is(serr, os.ErrNotExist) {
				t.Errorf("the pull that halted left a lockfile behind (%v)", serr)
			}
		})
	}
}

// TestPullHaltsOnAFailedVersionListing holds a pull to halting, in the registry's
// own condition, where the registry fails to list a Component's versions for any
// reason but holding none — a selection over a listing that failed part-way would
// change the version recorded with no word said — while a reference naming an
// exact version is offered that version alone and never listed, so a registry
// that serves versions but refuses to list them still serves it.
func TestPullHaltsOnAFailedVersionListing(t *testing.T) {
	t.Parallel()
	t.Run("a refused listing halts", func(t *testing.T) {
		t.Parallel()
		root := registryInstanceVersionsWorkspace(t, "quickstart-web")
		registry := newVersionRegistry(t)
		registry.listErr = fmt.Errorf("list tags: %w", oci.ErrFetchUnauthorized())
		releasePath := filepath.Join(root, "release.yaml")
		text, err := pullInstanceVersions(t, root, releasePath, registry)
		if !errors.Is(err, oci.ErrFetchUnauthorized()) {
			t.Fatalf("the pull halts with %v; want the registry's refusal\n%s", err, text)
		}
		if _, serr := os.Stat(lockfile.PathFor(releasePath)); !errors.Is(serr, os.ErrNotExist) {
			t.Errorf("the pull that halted left a lockfile behind (%v)", serr)
		}
	})
	t.Run("an exact reference is not listed", func(t *testing.T) {
		t.Parallel()
		root := registryInstanceVersionsWorkspace(t, "quickstart-web")
		replaceInFixture(t, filepath.Join(root, "aggregate.yaml"),
			"      type: core/compute/server\n", "      type: core/compute/server:1.0.0\n")
		registry := newVersionRegistry(t)
		registry.listErr = fmt.Errorf("list tags: %w", oci.ErrFetchUnauthorized())
		releasePath := filepath.Join(root, "release.yaml")
		if text, err := pullInstanceVersions(t, root, releasePath, registry); err != nil {
			t.Fatalf("pull: %v\n%s", err, text)
		}
		if n := registry.listings(); n != 0 {
			t.Errorf("the pull asked the registry for the versions of an exact reference %d times", n)
		}
		if n := registry.fetches("1.0.0"); n != 1 {
			t.Errorf("the registry served the server at 1.0.0 %d times, want once", n)
		}
		lockPath := lockfile.PathFor(releasePath)
		lf, _, err := lockfile.LoadLockfile(lockPath)
		if err != nil {
			t.Fatalf("load the lockfile: %v", err)
		}
		recorded := false
		for _, e := range lf.Entities {
			if e.Kind == closure.KindComponent && e.URI == instanceVersionsServer+":1.0.0" {
				recorded = e.ResolvedVersion == "1.0.0"
			}
		}
		if !recorded {
			t.Errorf("the lockfile does not record %s:1.0.0 at 1.0.0:\n%s", instanceVersionsServer, mustReadFile(t, lockPath))
		}
	})
}

// TestInspectRefusesLockedVersionOutsideConstraints holds `aggrega inspect` of a
// Release to the judgement a build from its lockfile makes: with the server's row
// moved to a version the constraints do not admit, the inspection halts with the
// same conflict the build halts with.
func TestInspectRefusesLockedVersionOutsideConstraints(t *testing.T) {
	t.Parallel()
	root := registryInstanceVersionsWorkspace(t, "quickstart-web")
	registry := newVersionRegistry(t)
	releasePath := filepath.Join(root, "release.yaml")
	if text, err := pullInstanceVersions(t, root, releasePath, registry); err != nil {
		t.Fatalf("pull: %v\n%s", err, text)
	}
	moveServerRow(t, lockfile.PathFor(releasePath), newerServerRow(t, root, registry))

	_, buildErr := lockedBuild(t, root)
	if !errors.Is(buildErr, catalog.ErrConflictingVersionConstraints()) {
		t.Fatalf("the build halts with %v; the case needs it to halt with the conflict", buildErr)
	}
	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve the workspace: %v", err)
	}
	err = renderReleaseFile(context.Background(), rw, releasePath, nil, nil, testInspectOptions(io.Discard))
	if !errors.Is(err, catalog.ErrConflictingVersionConstraints()) {
		t.Errorf("the inspection halts with %v; want the conflict the build halts with", err)
	}
}

// extendingServerDocument is the server at 1.0.0 written as a Component extending
// a base that carries the whole of its schema, so it reaches the base and nothing
// of its own.
const extendingServerDocument = `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/compute/server
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
  extends: core/compute/server-base:1.0.0
  schema: inherited
`

// serverBase is the reference of the base the extending server names.
const serverBase = "core/compute/server-base:1.0.0"

// writeServerBase adds to the catalog directory at root the base the extending
// server names: the server's own 1.0.0 document under the base's name.
func writeServerBase(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, "catalog", "component", "core", "compute", "server-base", "1.0.0", "component.yaml")
	writeFixture(t, path, string(mustReadFile(t, filepath.Join("testdata", "instance_versions", "catalog", "component",
		filepath.FromSlash(instanceVersionsServer), "1.0.0", "component.yaml"))))
	replaceInFixture(t, path, "  name: core/compute/server\n", "  name: core/compute/server-base\n")
	return path
}

// augmentsWorkspace lays down the shared-server workspace, with web mapped to
// webRecipe, over a catalog whose server at 1.0.0 extends a base carrying its
// whole schema — in the catalog directory, or, with registry set, served by the
// registry beside 1.1.0 — and returns its root, the puller to pull it with, and
// the path of the base's document.
func augmentsWorkspace(t *testing.T, registry bool, webRecipe string) (string, catalog.OCIPuller, string) {
	t.Helper()
	var root string
	var puller catalog.OCIPuller
	if registry {
		root = registryInstanceVersionsWorkspace(t, "quickstart-site")
		r := newVersionRegistry(t)
		r.bodies["1.0.0"] = []byte(extendingServerDocument)
		puller = r
	} else {
		root = instanceVersionsWorkspace(t, "quickstart-site")
		writeFixture(t, filepath.Join(root, "catalog", "component", filepath.FromSlash(instanceVersionsServer), "1.0.0", "component.yaml"),
			extendingServerDocument)
	}
	basePath := writeServerBase(t, root)
	writeSharedServerWorkspace(t, root, webRecipe)
	return root, puller, basePath
}

// serverDependencyRows returns the rows of the lockfile at lockPath that record
// the server and what it depends on: the server's own rows and its base's, and
// every schema library with its consumers.
func serverDependencyRows(t *testing.T, lockPath string) ([]lockfile.Entity, []lockfile.SchemaLibrary) {
	t.Helper()
	lf, _, err := lockfile.LoadLockfile(lockPath)
	if err != nil {
		t.Fatalf("load the lockfile: %v", err)
	}
	var rows []lockfile.Entity
	for _, e := range lf.Entities {
		if e.Kind == closure.KindComponent && strings.HasPrefix(e.URI, instanceVersionsServer) {
			e.ReferencedFrom = nil
			rows = append(rows, e)
		}
	}
	return rows, lf.SchemaLibraries
}

// TestPullAugmentsTheVersionTheRowRecords holds what a lockfile pins of a
// reference's dependencies — the base its Component extends, the libraries its
// schema reaches and the edges from it to them — to the version its row records,
// where the settlement over every instance writing the reference moves it below
// the version the Release's own instance alone selects. The server at 1.0.0
// extends a base carrying its whole schema, and 1.1.0 extends nothing; web mapped
// alone admits both and site.web only 1.0.0. The lockfile records exactly what a
// pull records where web itself selects 1.0.0 — the base's row and every library
// edge — rewrites byte for byte on a second pull, and an edit to the base after
// the pull makes the build report the lockfile out of date.
func TestPullAugmentsTheVersionTheRowRecords(t *testing.T) {
	t.Parallel()
	for _, source := range []struct {
		name     string
		registry bool
	}{{name: "from a catalog directory"}, {name: "from a registry", registry: true}} {
		t.Run(source.name, func(t *testing.T) {
			t.Parallel()
			control, controlPuller, _ := augmentsWorkspace(t, source.registry, "compose/server:1.0.0")
			if text, err := pullInstanceVersions(t, control, filepath.Join(control, "release.yaml"), controlPuller); err != nil {
				t.Fatalf("pull the Release whose web selects 1.0.0 itself: %v\n%s", err, text)
			}
			wantRows, wantLibraries := serverDependencyRows(t, lockfile.PathFor(filepath.Join(control, "release.yaml")))
			if !slices.ContainsFunc(wantRows, func(e lockfile.Entity) bool { return e.URI == serverBase }) {
				t.Fatalf("the control pins no row for %s, so the comparison below proves nothing", serverBase)
			}

			root, puller, basePath := augmentsWorkspace(t, source.registry, "compose/server-any:1.0.0")
			releasePath := filepath.Join(root, "release.yaml")
			if text, err := pullInstanceVersions(t, root, releasePath, puller); err != nil {
				t.Fatalf("pull: %v\n%s", err, text)
			}
			lockPath := lockfile.PathFor(releasePath)
			if row := serverRow(t, lockPath); row.ResolvedVersion != "1.0.0" {
				t.Fatalf("the pull records %s at %q, want 1.0.0", instanceVersionsServer, row.ResolvedVersion)
			}
			gotRows, gotLibraries := serverDependencyRows(t, lockPath)
			if !reflect.DeepEqual(gotRows, wantRows) {
				t.Errorf("the pull pins the server and its base as\n%+v\nwant what a pull selecting 1.0.0 itself pins:\n%+v", gotRows, wantRows)
			}
			if !reflect.DeepEqual(gotLibraries, wantLibraries) {
				t.Errorf("the pull records the schema libraries as\n%+v\nwant what a pull selecting 1.0.0 itself records:\n%+v", gotLibraries, wantLibraries)
			}
			assertPullHoldsItsLockfile(t, root, releasePath, puller)

			writeFixture(t, basePath, string(mustReadFile(t, basePath))+"# Edited after the pull.\n")
			if _, err := lockedBuild(t, root); !errors.Is(err, lockfile.ErrLockfileDrift()) {
				t.Errorf("the build over a base edited after the pull halts with %v; want the lockfile reported out of date", err)
			}
		})
	}
}
