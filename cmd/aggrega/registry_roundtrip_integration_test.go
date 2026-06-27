// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

// Package main's registry round-trip integration test. Build-tagged
// `integration` so the fast unit run excludes it (and the test-only
// go-containerregistry dependency never enters the production binary's import
// graph); CI runs it via `go test -tags=integration ./cmd/aggrega/...`.
//
// It stands up an in-process REAL HTTP OCI registry (go-containerregistry's
// registry.New wrapped in httptest.Server, on 127.0.0.1 loopback so the OCI
// client speaks plain HTTP), pushes the standard catalog, wipes the cache, and
// drives the real pull → build through the real OCI client — the standing
// regression guard for the consume side the manual smoke could not lock down.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"
	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// offlineReferenceSHA is the catalog-only compose output built offline against
// the standard catalog; the registry-tier build must reproduce it byte-for-byte.
const offlineReferenceSHA = "752c3b69b779dfdbc6029ff77c00c481d0afea2798fb44d815e2537e54e90167"

func standardCatalogDir(t *testing.T) string {
	t.Helper()
	// repo-root/aggrega-hub/aggrega-catalog, from cmd/aggrega.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := filepath.Clean(filepath.Join(wd, "..", "..", "..", "aggrega-hub", "aggrega-catalog"))
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("standard catalog not present at %s: %v", dir, err)
	}
	return dir
}

func catalogOnlyExampleDir(t *testing.T) string {
	t.Helper()
	wd, _ := os.Getwd()
	dir := filepath.Clean(filepath.Join(wd, "..", "..", "..", "aggrega-docs", "language-reference", "examples", "catalog-only"))
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("catalog-only example not present at %s: %v", dir, err)
	}
	return dir
}

// startRegistry stands up an in-process real OCI registry and returns its
// loopback host:port. handlerWrap, when non-nil, wraps the registry handler
// (used by the auth-refuse sub-test to inject 401s).
func startRegistry(t *testing.T, handlerWrap func(http.Handler) http.Handler) string {
	t.Helper()
	var h http.Handler = registry.New()
	if handlerWrap != nil {
		h = handlerWrap(h)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

// publishableKinds is the eight kinds aggrega push publishes; mirrors
// push-all.sh's is_publishable filter.
var publishableKinds = map[string]bool{
	"Component": true, "Feature": true, "Recipe": true, "FeatureRecipe": true,
	"Provider": true, "SchemaLibrary": true, "MappingLibrary": true, "Aggregate": true,
}

// pushCatalog walks catalogDir and pushes every publishable entity to host via
// the real push path (the programmatic equal of push-all.sh). Returns the count
// pushed per kind so the test can assert coverage.
func pushCatalog(t *testing.T, host, catalogDir string) map[string]int {
	t.Helper()
	counts := map[string]int{}
	err := filepath.WalkDir(catalogDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(path, ".yaml") {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		kind := peekKind(data)
		if !publishableKinds[kind] {
			return nil // non-entity YAML (recipe fragment, source/) or non-publishable
		}
		if perr := pushRun(pushConfig{EntityPath: path, Registry: host}); perr != nil {
			t.Fatalf("push %s (%s): %v", path, kind, perr)
		}
		counts[kind]++
		return nil
	})
	if err != nil {
		t.Fatalf("walk catalog: %v", err)
	}
	return counts
}

func peekKind(data []byte) string {
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "kind:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "kind:"))
		}
	}
	return ""
}

// newConsumerWorkspace copies the catalog-only example into a temp dir, points
// it at the registry (no local catalog_dirs), and wipes the lockfile + cache so
// the build must resolve everything over the wire. It chdir's into the workspace
// (build/pull read the cwd) and returns the workspace path.
func newConsumerWorkspace(t *testing.T, host string) string {
	t.Helper()
	ws := t.TempDir()
	copyTree(t, catalogOnlyExampleDir(t), ws)
	writeWorkspaceConfig(t, ws, "default_registry: \""+host+"\"\nauto_pull: true\noffline: false")
	_ = os.Remove(filepath.Join(ws, "release.lock"))
	_ = os.RemoveAll(filepath.Join(ws, ".aggrega"))
	_ = os.RemoveAll(filepath.Join(ws, "dist"))
	t.Chdir(ws)
	return ws
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copy tree: %v", err)
	}
}

func sha256Hex(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// TestIntegration_RegistryRoundTrip_Positive drives the real push → cache-wipe →
// pull → build of catalog-only against the in-process registry, and asserts the
// registry-tier output is byte-identical to the offline reference and that a
// rebuild from the generated lock is byte-identical (lock == build).
//
// This is also the behavioral no-bridge proof: the consumer workspace carries no
// local catalog directory, so every kind (Component / Feature / Recipe /
// FeatureRecipe / secret-origin + $ref Feature / SchemaLibrary / Aggregate)
// resolves purely through the registry-pulled closure read by the closure-backed
// lookup. There is no cache→catalog bridge in the path — the build reaching a
// byte-identical result from a no-local-catalog workspace is the proof, since the
// pre-unification path required that (now-deleted) bridge to fold registry
// entities into the build's view.
func TestIntegration_RegistryRoundTrip_Positive(t *testing.T) {
	host := startRegistry(t, nil)
	counts := pushCatalog(t, host, standardCatalogDir(t))
	// Every publishable kind present in the standard catalog pushed clean.
	for _, k := range []string{"Component", "Feature", "Recipe", "FeatureRecipe", "Provider", "SchemaLibrary"} {
		if counts[k] == 0 {
			t.Errorf("no %s pushed (coverage gap)", k)
		}
	}

	ws := newConsumerWorkspace(t, host)
	// No-bridge witness: the workspace has no local catalog dir, so resolution
	// has no local source to fall back on and must run through the registry-pulled
	// closure alone.
	if _, err := os.Stat(filepath.Join(ws, "catalog")); err == nil {
		t.Fatal("consumer workspace carries a local catalog dir; the no-bridge witness is invalid")
	}
	if err := pullRun(pullConfig{ReleasePath: "release.yaml"}); err != nil {
		t.Fatalf("registry pull: %v", err)
	}
	if err := dispatchBuild(nil, "release.yaml", nil, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("registry build: %v", err)
	}
	got := sha256Hex(t, filepath.Join("dist", "compose", "docker-compose.yaml"))
	if got != offlineReferenceSHA {
		t.Errorf("registry build output sha = %s; want offline reference %s", got, offlineReferenceSHA)
	}

	// lock == build: rebuild from the generated lock (no re-pull), byte-identical.
	if _, _, err := lockfile.LoadLockfile(lockfile.PathFor("release.yaml")); err != nil {
		t.Fatalf("lockfile absent after pull: %v", err)
	}
	_ = os.RemoveAll("dist")
	if err := dispatchBuild(nil, "release.yaml", nil, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("rebuild from lock: %v", err)
	}
	if rebuilt := sha256Hex(t, filepath.Join("dist", "compose", "docker-compose.yaml")); rebuilt != got {
		t.Errorf("lock != build: rebuild sha %s != %s", rebuilt, got)
	}
}

// TestIntegration_RegistryBuild_WithheldEntityHaltsAtClosure proves the unified
// view halts LOUDLY rather than silently building on a view gap. With every pin
// present the build resolves cleanly through the closure-backed lookup (no false
// halt); drop a referenced entity's pin from the lock while the Aggregate still
// references it by bare token, and the build halts at closure resolution with
// ErrUnresolvableURI. The halt is selective — it fires only on the genuine gap,
// never on a legitimately-present reference.
//
// The relative-path-vs-catalog-reference classifier arm of the guard
// view-consistency net is owned by the resolver unit suite
// (pkg/resolver/guard_view_consistency_test.go), not duplicated here: a real
// build cannot reach the guard's relative-path skip, because a missing entity
// halts at the closure walk before any guard runs, and a relative-path component
// type has no build-level load path (the closure resolves every entity URI
// through the lookup). So the reachable real-build property is this selective
// closure halt; the bare-token-absent-from-view guard halt is unreachable by
// construction once the build's resolved set and the guards' view are the one
// closure-backed lookup — which is the unification's whole point.
func TestIntegration_RegistryBuild_WithheldEntityHaltsAtClosure(t *testing.T) {
	host := startRegistry(t, nil)
	pushCatalog(t, host, standardCatalogDir(t))
	ws := newConsumerWorkspace(t, host)
	if err := pullRun(pullConfig{ReleasePath: "release.yaml"}); err != nil {
		t.Fatalf("pull: %v", err)
	}

	// Pin present: a legitimately-present reference resolves cleanly (no false halt).
	if err := dispatchBuild(nil, "release.yaml", nil, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("build with all pins present: %v (a present reference must resolve cleanly)", err)
	}

	// Withhold a referenced catalog entity's pin from the build's view.
	lockPath := lockfile.PathFor("release.yaml")
	lf, _, err := lockfile.LoadLockfile(lockPath)
	if err != nil {
		t.Fatalf("load lock: %v", err)
	}
	var kept []lockfile.Entity
	dropped := 0
	for _, e := range lf.Entities {
		if strings.HasPrefix(e.URI, "core/devtool/adminer") {
			dropped++
			continue
		}
		kept = append(kept, e)
	}
	if dropped == 0 {
		t.Fatal("no adminer pin found to withhold; fixture drifted")
	}
	lf.Entities = kept
	if err := lockfile.WriteLockfile(lockPath, lf); err != nil {
		t.Fatalf("rewrite lock: %v", err)
	}

	// Freeze the build: auto_pull is FALSE so the build resolves strictly against
	// the (now-incomplete) lockfile rather than reconciling. The withheld entity
	// is un-pinned in the frozen view, so the closure-backed lookup cannot resolve
	// it and the build halts loudly at closure. (Under auto_pull:true the build's
	// honoring auto-pull would resolve the un-pinned reference and re-add it — the
	// honoring default fills a gap rather than halting; the loud closure halt is
	// the frozen-mode guarantee.)
	writeWorkspaceConfig(t, ws, "default_registry: \""+host+"\"\nauto_pull: false\noffline: false")

	_ = os.RemoveAll("dist")
	err = dispatchBuild(nil, "release.yaml", nil, false, os.Stderr, os.Stdout)
	if err == nil {
		t.Fatal("build SUCCEEDED with a referenced entity withheld; the unified view silently proceeded on a gap")
	}
	if !errors.Is(err, closure.ErrUnresolvableURI) {
		t.Fatalf("withheld entity: err = %v; want errors.Is(closure.ErrUnresolvableURI) — the loud closure halt", err)
	}
}

// mutate applies an in-place string replacement to a workspace file.
func mutateFile(t *testing.T, ws, file, old, new string) {
	t.Helper()
	p := filepath.Join(ws, file)
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	if !strings.Contains(string(data), old) {
		t.Fatalf("mutation anchor %q not found in %s", old, file)
	}
	if err := os.WriteFile(p, []byte(strings.Replace(string(data), old, new, 1)), 0o644); err != nil {
		t.Fatalf("write %s: %v", file, err)
	}
}

// TestIntegration_RegistryBuild_GuardNegatives proves every guard FIRES against
// registry-resolved entities — the property that catches a silently-disabled
// validator (06-pre4's silent-skip class). Each case feeds invalid developer
// input (a mutated local Aggregate) through the registry-build path against the
// valid pushed catalog and requires the build to FAIL with the right sentinel.
// All five formerly-silent-skipping guards are covered; credential (which halts)
// is a bonus.
func TestIntegration_RegistryBuild_GuardNegatives(t *testing.T) {
	host := startRegistry(t, nil)
	pushCatalog(t, host, standardCatalogDir(t))

	cases := []struct {
		name    string
		mutate  func(t *testing.T, ws string)
		wantErr error
	}{
		{
			name: "schema_conformance",
			mutate: func(t *testing.T, ws string) {
				// database is a string field; a list violates the values schema.
				mutateFile(t, ws, "aggregate.yaml", "database: appdb", "database: [not, a, string]")
			},
			wantErr: resolver.ErrSchemaConformance,
		},
		{
			name: "unknown_uses_socket",
			mutate: func(t *testing.T, ws string) {
				// adminer declares a `databases` socket; `databases-typo` is undeclared.
				mutateFile(t, ws, "aggregate.yaml", "        databases:\n", "        databases-typo:\n")
			},
			wantErr: resolver.ErrUnknownUsesSocket,
		},
		{
			name: "self_inject_forbidden",
			mutate: func(t *testing.T, ws string) {
				// mariadb's spec.inject allowlist is [env, file]; a self-inject via
				// the non-allowlisted `arg` mechanism is forbidden.
				mutateFile(t, ws, "aggregate.yaml", "      values:\n        database: appdb", "      inject:\n        arg:\n          - in: X\n            from: host\n      values:\n        database: appdb")
			},
			wantErr: resolver.ErrSelfInjectForbidden,
		},
		{
			name: "invalid_inject_key",
			mutate: func(t *testing.T, ws string) {
				// adminer's uses-inject `from: host` resolves against the db
				// target's provides; `from: not-a-provides-key` does not exist on
				// the target — guard_inject (the 5th silent-skipper) must reject it.
				mutateFile(t, ws, "aggregate.yaml", "                  from: host", "                  from: not-a-provides-key")
			},
			wantErr: resolver.ErrInvalidInjectKey,
		},
		{
			name: "credential_partition",
			mutate: func(t *testing.T, ws string) {
				// A non-secret Feature (audit-logging) placed in a secrets: block.
				mutateFile(t, ws, "aggregate.yaml", "      secrets:\n        root_password:", "      secrets:\n        bad:\n          type: core/audit-logging\n        root_password:")
			},
			wantErr: resolver.ErrCredentialLabelPartition,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ws := t.TempDir()
			copyTree(t, catalogOnlyExampleDir(t), ws)
			writeWorkspaceConfig(t, ws, "default_registry: \""+host+"\"\nauto_pull: true\noffline: false")
			_ = os.Remove(filepath.Join(ws, "release.lock"))
			_ = os.RemoveAll(filepath.Join(ws, ".aggrega"))
			tc.mutate(t, ws)
			t.Chdir(ws)
			err := dispatchBuild(nil, "release.yaml", nil, false, os.Stderr, os.Stdout)
			if err == nil {
				t.Fatalf("%s: build SUCCEEDED; want failure with %v", tc.name, tc.wantErr)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("%s: build err = %v; want errors.Is(%v)", tc.name, err, tc.wantErr)
			}
		})
	}
}

// TestIntegration_Capstone_LintAndExample is the continuous-exercise capstone:
// the whole standard catalog lints warning-free, and the catalog-only example
// (the consumption exemplar) builds green offline against it, byte-identical to
// the reference. CI runs this (with the round-trip tests) so the catalog and its
// living documentation are exercised on every run. CI wires the same
// `aggrega lint` + per-example `aggrega build` invocations across the full
// migrated-example set.
func TestIntegration_Capstone_LintAndExample(t *testing.T) {
	std := standardCatalogDir(t)
	// Lint enumerates + validates via its own eager whole-catalog load, wholly
	// independent of any build closure. Run it from a bare working directory with
	// no Release and no lockfile present: there is no closure for it to read, so a
	// warning-free result comes purely from the eager enumeration the build's
	// closure-backed path does not touch — the retained-eager-load split.
	t.Chdir(t.TempDir())
	if err := lintCatalogWide("", "", []string{std}, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("whole-catalog lint not warning-free (independent eager enumeration): %v", err)
	}

	ws := t.TempDir()
	copyTree(t, catalogOnlyExampleDir(t), ws)
	_ = os.Remove(filepath.Join(ws, "release.lock"))
	_ = os.RemoveAll(filepath.Join(ws, ".aggrega"))
	_ = os.RemoveAll(filepath.Join(ws, "dist"))
	t.Chdir(ws)
	// No workspace config: defaults (auto_pull on). --catalog supplies every
	// entity locally, so the auto-pull resolves from the catalog with no network.
	if err := dispatchBuild(nil, "release.yaml", []string{std}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("catalog-only build against the shared catalog: %v", err)
	}
	if got := sha256Hex(t, filepath.Join("dist", "compose", "docker-compose.yaml")); got != offlineReferenceSHA {
		t.Errorf("offline example build sha = %s; want reference %s", got, offlineReferenceSHA)
	}
}

// publishWasmPlugin publishes a raw .wasm artifact to the in-process registry at
// the plugin's canonical OCI reference (host/wasm/<socket>/<name>) under the given
// version tag, returning the CONTENT digest the closure pins. A plugin is a raw Wasm
// artifact, not a catalog YAML entity, so it is published directly through the OCI
// client rather than the entity push path — the same reference SplitURIForOCI parses
// back at the closure walker's Tier-3 dispatch. The bytes are a minimal Wasm header
// stand-in; the pull pins the artifact by the content digest of the pulled bytes
// (the closure standardises on content digests, not the manifest digest oci.Push
// returns), without loading or executing it.
func publishWasmPlugin(t *testing.T, host, socket, name, version string) string {
	t.Helper()
	ref := host + "/wasm/" + socket + "/" + name
	repo, err := oci.NewRemoteRepository(ref)
	if err != nil {
		t.Fatalf("new plugin repo %s: %v", ref, err)
	}
	pluginBytes := []byte("\x00asm\x01\x00\x00\x00")
	if _, err := oci.Push(context.Background(), repo, version, oci.MediaTypeComponent, pluginBytes); err != nil {
		t.Fatalf("push plugin %s:%s: %v", ref, version, err)
	}
	return digest.ContentDigest(pluginBytes)
}

// TestIntegration_PipelinePluginPinning_RoundTrip proves a registry-resolved
// Recipe / FeatureRecipe spec.pipeline[].plugin URI is pinned into the written
// lockfile's wasm_plugins section with the right provenance — the same deterministic
// pin workspace-level plugins get. A controlled fixture catalog declares a Component
// with a pipeline-bearing Recipe and a Feature with a pipeline-bearing FeatureRecipe;
// both pipeline plugins are published as registry artifacts. After a real pull the
// written lockfile carries both plugin pins, each referenced_from its declaring
// recipe / feature-recipe URI.
func TestIntegration_PipelinePluginPinning_RoundTrip(t *testing.T) {
	host := startRegistry(t, nil)

	const (
		recipePluginName = "patch-server"
		frPluginName     = "patch-credential"
		pluginSocket     = "pipeline"
	)
	recipePluginURI := host + "/wasm/" + pluginSocket + "/" + recipePluginName + ":1.0.0"
	frPluginURI := host + "/wasm/" + pluginSocket + "/" + frPluginName + ":1.0.0"

	// A controlled fixture catalog: a Component implemented by a pipeline-bearing
	// Recipe, and a (secret) Feature implemented by a pipeline-bearing FeatureRecipe.
	cat := t.TempDir()
	writeFixtureEntity(t, cat, "provider/compose/1.0.0/provider.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Provider
metadata:
  name: compose
  version: "1.0.0"
spec:
  baseSchema: |
    type: object
    additionalProperties: true
`)
	writeFixtureEntity(t, cat, "component/test/svc/1.0.0/component.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: test/svc
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values: {}
`)
	writeFixtureEntity(t, cat, "recipe/compose/svc/1.0.0/recipe.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: svc
  version: "1.0.0"
spec:
  provider: compose
  implements: test/svc
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    containers:
      main:
        image: busybox:1
  pipeline:
    - id: patch
      plugin: `+recipePluginURI+`
      function: step
`)
	writeFixtureEntity(t, cat, "feature/test/cred/1.0.0/feature.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: test/cred
  version: "1.0.0"
  labels:
    feature.aggrega.ai/kind: secret
spec:
  schema:
    values: {}
`)
	writeFixtureEntity(t, cat, "featurerecipe/compose/cred/1.0.0/recipe.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: FeatureRecipe
metadata:
  name: cred
  version: "1.0.0"
spec:
  provider: compose
  implements: test/cred
  pipeline:
    - id: patch
      plugin: `+frPluginURI+`
      function: step
`)
	pushCatalog(t, host, cat)

	// Publish both pipeline plugins as registry artifacts so they resolve above
	// Tier-1 and participate in the lockfile pin.
	recipePluginDigest := publishWasmPlugin(t, host, pluginSocket, recipePluginName, "1.0.0")
	frPluginDigest := publishWasmPlugin(t, host, pluginSocket, frPluginName, "1.0.0")

	// A consumer workspace wiring the Component (with a secret attachment that pulls
	// the Feature) through the Manifest mappings, resolved purely over the registry.
	ws := t.TempDir()
	writeFixture(t, filepath.Join(ws, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    app:
      type: test/svc
      secrets:
        cred:
          type: test/cred
`)
	writeFixture(t, filepath.Join(ws, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        test/svc:1.0.0: compose/svc:1.0.0
    features:
      type:
        test/cred:1.0.0: compose/cred:1.0.0
`)
	writeFixture(t, filepath.Join(ws, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
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
        app:
          infra: {}
`)
	// The fixture plugins are published unsigned to the unsigned in-process
	// registry, so signature verification has nothing legitimate to check;
	// disable it loudly so the pull exercises the plugin-pin path, not the
	// signature path (which the dedicated pull-verify suites cover).
	writeWorkspaceConfig(t, ws, "default_registry: \""+host+"\"\nauto_pull: true\noffline: false\nskip_signature_verification: true")
	t.Chdir(ws)

	if err := pullRun(pullConfig{ReleasePath: "release.yaml"}); err != nil {
		t.Fatalf("registry pull: %v", err)
	}

	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor("release.yaml"))
	if err != nil {
		t.Fatalf("load lock: %v", err)
	}

	pluginByURI := map[string]lockfile.WasmPlugin{}
	for _, p := range lf.WasmPlugins {
		pluginByURI[p.URI] = p
	}

	cases := []struct {
		uri          string
		wantDigest   string
		wantRefedURI string
		label        string
	}{
		{recipePluginURI, recipePluginDigest, "compose/svc:1.0.0", "Recipe pipeline plugin"},
		{frPluginURI, frPluginDigest, "compose/cred:1.0.0", "FeatureRecipe pipeline plugin"},
	}
	for _, tc := range cases {
		got, ok := pluginByURI[tc.uri]
		if !ok {
			t.Fatalf("%s %q absent from written lockfile wasm_plugins (have %v)", tc.label, tc.uri, lf.WasmPlugins)
		}
		if got.Digest != tc.wantDigest {
			t.Errorf("%s digest = %q; want %q", tc.label, got.Digest, tc.wantDigest)
		}
		if got.ResolvedVersion != "1.0.0" {
			t.Errorf("%s resolved_version = %q; want 1.0.0", tc.label, got.ResolvedVersion)
		}
		foundRef := false
		for _, r := range got.ReferencedFrom {
			if strings.Contains(r, tc.wantRefedURI) {
				foundRef = true
				break
			}
		}
		if !foundRef {
			t.Errorf("%s referenced_from = %v; want an entry naming %q", tc.label, got.ReferencedFrom, tc.wantRefedURI)
		}
	}
}

// writeFixtureEntity writes an entity YAML into a fixture catalog at the
// per-version-subdir layout push requires, returning the file path.
func writeFixtureEntity(t *testing.T, catalogDir, relPath, yaml string) {
	t.Helper()
	p := filepath.Join(catalogDir, relPath)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	if err := os.WriteFile(p, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", relPath, err)
	}
}

// authoredFixtureCatalog writes a controlled fixture catalog: a Component with a
// REQUIRED uses socket, a trivial Recipe implementing it, and a publishable
// Aggregate. These isolate the required-socket guard (independent of whether any
// standard component builds standalone) and give the kind-coverage test a real
// Aggregate to round-trip. Returns the catalog dir.
func authoredFixtureCatalog(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFixtureEntity(t, dir, "component/test/needs-socket/1.0.0/component.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: test/needs-socket
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  usesSchema:
    backend:
      selector: "component.aggrega.ai/type=stateful-server"
      required: true
      inject: [env]
  schema:
    values: {}
`)
	writeFixtureEntity(t, dir, "recipe/compose/needs-socket/1.0.0/recipe.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: needs-socket
  version: "1.0.0"
spec:
  provider: compose
  implements: test/needs-socket
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    containers:
      main:
        image: busybox:1
`)
	writeFixtureEntity(t, dir, "aggregate/test/app-agg/1.0.0/aggregate.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: test/app-agg
  version: "1.0.0"
spec:
  components:
    db:
      type: core/datastore/mariadb
      values:
        database: appdb
      secrets:
        root_password:
          type: core/secret-value
          values:
            key: PW
            source: x
`)
	return dir
}

// TestIntegration_RequiredSocket_AuthoredFixture isolates guard_required_socket:
// a fixture Component with a required uses socket, referenced with the socket
// UNBOUND, must fail the registry build with ErrMissingRequiredSocket — and with
// that SPECIFIC sentinel, so a failure on an earlier guard (missing recipe /
// values) surfaces as "wrong guard", not a false pass. Completes the five
// silent-skip guards' negative coverage.
func TestIntegration_RequiredSocket_AuthoredFixture(t *testing.T) {
	host := startRegistry(t, nil)
	pushCatalog(t, host, standardCatalogDir(t)) // for the mariadb target the recipe selector needs
	pushCatalog(t, host, authoredFixtureCatalog(t))

	ws := t.TempDir()
	writeFixture(t, filepath.Join(ws, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    app:
      type: test/needs-socket
      # backend (required: true) is deliberately left UNBOUND.
`)
	writeFixture(t, filepath.Join(ws, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        test/needs-socket:1.0.0: compose/needs-socket:1.0.0
`)
	writeFixture(t, filepath.Join(ws, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
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
        app:
          infra:
            replicas: 1
`)
	writeWorkspaceConfig(t, ws, "default_registry: \""+host+"\"\nauto_pull: true\noffline: false")
	t.Chdir(ws)

	err := dispatchBuild(nil, "release.yaml", nil, false, os.Stderr, os.Stdout)
	if err == nil {
		t.Fatalf("required-socket: build SUCCEEDED; want failure with ErrMissingRequiredSocket")
	}
	if !errors.Is(err, resolver.ErrMissingRequiredSocket) {
		t.Fatalf("required-socket: build err = %v; want errors.Is(ErrMissingRequiredSocket) (a different error means the wrong guard fired)", err)
	}
}

// TestIntegration_KindCoverage_ProviderAndAggregate round-trips the two kinds the
// catalog-only build does not exercise — a Provider and a (published) Aggregate —
// directly through the real OCI client, asserting each pulls non-empty from the
// registry. Together with the positive build (Component/Recipe/FeatureRecipe/
// Feature/libraries) this covers every publishable kind fetched-from-registry.
func TestIntegration_KindCoverage_ProviderAndAggregate(t *testing.T) {
	host := startRegistry(t, nil)
	pushCatalog(t, host, standardCatalogDir(t))
	pushCatalog(t, host, authoredFixtureCatalog(t))

	for _, ent := range []struct{ kind, ref string }{
		{"Provider", host + "/provider/compose"},
		{"Aggregate", host + "/aggregate/test/app-agg"},
	} {
		repo, err := oci.NewRemoteRepository(ent.ref)
		if err != nil {
			t.Fatalf("%s: new repo %s: %v", ent.kind, ent.ref, err)
		}
		res, perr := oci.Pull(context.Background(), repo, "1.0.0")
		if perr != nil {
			t.Fatalf("%s: pull %s:1.0.0 from registry: %v", ent.kind, ent.ref, perr)
		}
		if len(res.Bytes) == 0 {
			t.Errorf("%s: pulled empty bytes from %s", ent.kind, ent.ref)
		}
	}
}

// TestIntegration_RegistryAuthRefuse proves an unauthenticated pull against a
// registry that demands auth fails with the unauthorized + docker-login
// remediation (the in-process registry is auth-off, so a 401-injecting wrapper
// stands in for the demand).
func TestIntegration_RegistryAuthRefuse(t *testing.T) {
	host := startRegistry(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Basic challenge (not Bearer) so the client surfaces a clean 401
			// rather than attempting a token exchange against a realm URL.
			w.Header().Set("WWW-Authenticate", `Basic realm="registry"`)
			w.WriteHeader(http.StatusUnauthorized)
		})
	})
	ws := t.TempDir()
	copyTree(t, catalogOnlyExampleDir(t), ws)
	writeWorkspaceConfig(t, ws, "default_registry: \""+host+"\"\nauto_pull: true\noffline: false")
	_ = os.Remove(filepath.Join(ws, "release.lock"))
	_ = os.RemoveAll(filepath.Join(ws, ".aggrega"))
	t.Chdir(ws)
	err := pullRun(pullConfig{ReleasePath: "release.yaml"})
	// The security property: the pull is REFUSED against an auth-demanding
	// registry — it does not silently succeed. (The exact unauthorized +
	// `docker login <host>` remediation string is unit-tested in
	// pkg/oci TestRemediationFor_Unauthorized; oras short-circuits the in-process
	// Basic challenge with "basic credential not found" before the round-trip, so
	// this in-process test asserts refusal, not the message.)
	if err == nil {
		t.Fatalf("auth-refuse: pull SUCCEEDED against an auth-demanding registry; want refusal")
	}
	low := strings.ToLower(err.Error())
	if !strings.Contains(low, "credential") && !strings.Contains(low, "unauthor") && !strings.Contains(low, "401") {
		t.Errorf("auth-refuse: err = %v; want an auth/credential refusal", err)
	}
}

// localExamplesRoot returns repo-root/aggrega-docs/language-reference/examples
// from cmd/aggrega, skipping when the sibling repo is absent (same
// sibling-checkout discipline as standardCatalogDir). Computed from the supplied
// working directory so callers can resolve it before chdir-ing into a workspace.
func localExamplesRoot(t *testing.T, wd string) string {
	t.Helper()
	dir := filepath.Clean(filepath.Join(wd, "..", "..", "..", "aggrega-docs", "language-reference", "examples"))
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("example set not present at %s: %v", dir, err)
	}
	return dir
}

// canonicalPins loads a written lockfile and returns its pins as canonical YAML
// with the wall-clock generated_at masked — the one non-deterministic field,
// which is not a pin. Everything that IS a pin (per-entity / per-library URI,
// resolved_version, digest, the referenced-from and consumer sets) is captured.
// Comparing this extracted form (not a raw on-disk file diff) is the invariance
// check: gitignored lockfiles have no committed baseline, and a raw diff would
// trip on generated_at.
func canonicalPins(t *testing.T, lockPath string) []byte {
	t.Helper()
	lf, _, err := lockfile.LoadLockfile(lockPath)
	if err != nil {
		t.Fatalf("load lock %s: %v", lockPath, err)
	}
	lf.GeneratedAt = ""
	out, err := yaml.Marshal(lf)
	if err != nil {
		t.Fatalf("marshal pins: %v", err)
	}
	return out
}

// TestIntegration_LocalPathPinInvariance is the durable, repeatable form of the
// local-catalog byte-identity guard: for the library-pinning examples (each pins
// a SchemaLibrary/MappingLibrary), regenerate the lockfile against the real
// example inputs and assert the extracted pins match a committed golden. The
// example *.lock files are gitignored (the examples ship inputs and regenerate
// lockfiles), so the baseline is a committed golden under testdata, not the lock
// itself; the comparison is extract-and-compare with generated_at masked, never a
// raw file diff. A legitimate pin change (a catalog version bump, a new digest)
// must update the golden in the same commit — that golden diff in review is the
// deliberate-change gate; a silent perturbation fails here.
//
// Regenerate the goldens with: AGGREGA_UPDATE_GOLDEN=1 go test -tags=integration
// -run TestIntegration_LocalPathPinInvariance ./cmd/aggrega/
func TestIntegration_LocalPathPinInvariance(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	examplesRoot := localExamplesRoot(t, wd)
	std := standardCatalogDir(t)
	goldenBase := filepath.Join(wd, "testdata", "local_pin_invariance")

	cases := []struct {
		name         string
		exampleDir   string
		release      string
		localCatalog []string
		needStd      bool
	}{
		{"annotation-driven-monitoring", "annotation-driven-monitoring", "release.yaml", []string{"catalog"}, true},
		{"mariadb-adminer-helm-chart", "mariadb-adminer", "release-helm-chart.yaml", []string{"catalog-helm-chart"}, false},
		{"mariadb-adminer-helm-synth", "mariadb-adminer", "release-helm-synth.yaml", []string{"catalog-helm-synth"}, false},
		{"cross-boundary-networking-compose", "cross-boundary-networking", "release-compose.yaml", []string{"catalog-compose"}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ws := t.TempDir()
			copyTree(t, filepath.Join(examplesRoot, tc.exampleDir), ws)
			// Drop the example's workspace config: its catalog_dirs are relative to
			// the real example location and do not resolve from the temp copy. We
			// supply the catalog dirs explicitly (absolute) instead — the same
			// local + standard stack the example resolves against in place.
			_ = os.Remove(filepath.Join(ws, ".aggrega-config.yaml"))
			_ = os.RemoveAll(filepath.Join(ws, ".aggrega"))

			var dirs []string
			for _, lc := range tc.localCatalog {
				dirs = append(dirs, filepath.Join(ws, lc))
			}
			if tc.needStd {
				dirs = append(dirs, std)
			}

			t.Chdir(ws)
			_ = os.Remove(lockfile.PathFor(tc.release)) // discard any copied stale lock
			if err := pullRun(pullConfig{ReleasePath: tc.release, CatalogDirs: dirs, Regenerate: true}); err != nil {
				t.Fatalf("regenerate lock: %v", err)
			}
			got := canonicalPins(t, lockfile.PathFor(tc.release))

			golden := filepath.Join(goldenBase, tc.name+".pins.golden")
			if os.Getenv("AGGREGA_UPDATE_GOLDEN") != "" {
				if err := os.MkdirAll(goldenBase, 0o755); err != nil {
					t.Fatalf("mkdir golden dir: %v", err)
				}
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				t.Logf("updated golden %s (%d bytes)", golden, len(got))
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("read golden %s (regenerate with AGGREGA_UPDATE_GOLDEN=1): %v", golden, err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("%s: lockfile pins drifted from the committed golden (generated_at masked).\n"+
					"A legitimate catalog/version change must update the golden in the same commit — the golden diff in review is the deliberate-change gate.\n"+
					"--- got ---\n%s\n--- want ---\n%s", tc.name, got, want)
			}
		})
	}
}
