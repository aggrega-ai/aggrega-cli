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

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// offlineReferenceCompose builds the catalog-only example offline against the
// standard catalog and returns the SHA-256 of its rendered compose output. It is
// the reference the registry-tier build must reproduce byte-for-byte.
//
// The reference is computed at test time rather than frozen as a constant: a
// core provider resolves from the binary's embedded entity, whose metadata
// version is a build-provenance stamp that moves commit to commit and lands in
// the rendered output as the aggrega.ai/provider-version label. A frozen constant
// would therefore flake on every version bump; regenerating the reference from an
// offline build that resolves the SAME embedded provider keeps the comparison a
// genuine cross-path byte-identity check (registry path vs local path) with no
// dependence on the version string. Product provenance behavior is unchanged —
// the label still carries the resolved provider version; the test just anchors on
// a freshly-built reference instead of a stale literal.
//
// std and exampleDir are resolved by the caller before any chdir (both helpers
// walk up from cwd); the reference build runs in its own temp workspace and
// restores the working directory before returning.
func offlineReferenceCompose(t *testing.T, std, exampleDir string) string {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(prev) }()

	ws := t.TempDir()
	copyTree(t, exampleDir, ws)
	_ = os.Remove(filepath.Join(ws, "release.lock"))
	_ = os.RemoveAll(filepath.Join(ws, ".aggrega"))
	_ = os.RemoveAll(filepath.Join(ws, "dist"))
	if err := os.Chdir(ws); err != nil {
		t.Fatalf("chdir reference workspace: %v", err)
	}
	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), ReleasePath: "release.yaml", CatalogDirs: []string{std}}); err != nil {
		t.Fatalf("offline reference pull: %v", err)
	}
	if err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), "release.yaml", []string{std}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("offline reference build: %v", err)
	}
	return sha256HexFile(t, filepath.Join("dist", "compose", "docker-compose.yaml"))
}

// pipelineStepPluginType is the single fixture-side source for the pipeline plugins'
// socket. A Plugin's identity coordinate is <pluginType>/<name> and its OCI
// location is plugin/<lower(pluginType)>/<lower(name)>; every fixture that
// authors, references, or resolves a pipeline-step plugin derives both from this
// const through types.PluginCoordinate and catalog.OCILocation, so no site
// hardcodes a divergent literal.
const pipelineStepPluginType = "pipeline-step"

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

// nonRoundTripKinds names each kind the OCI transport resolves a mediatype for
// that this round-trip nonetheless does not push, mapped to the reason it does
// not. It is the only way a kind leaves the walk below, so every narrowing of
// that walk is a named entry here with a reason attached — and the reason is
// carried in the value rather than in a comment so the check below can require
// one to exist.
//
// A kind is listed here only when pushing it would be WRONG, never merely because
// the catalog happens to hold none of it: the walk below is zero-count safe, so a
// kind with no instances contributes no pushes and needs no exclusion. Reading it
// the other way is how ArtifactLibrary came to be excluded on the strength of an
// empty catalog — which then read as a standing decision not to publish the kind,
// when publishing it is exactly what it is for.
var nonRoundTripKinds = map[string]string{
	"Plugin": "the catalog walk publishes entity YAML only; a Plugin's .wasm " +
		"artifact is a separate push with its own mediatype",
}

// publishableKinds is the kinds this round-trip pushes: every kind the OCI
// transport resolves a mediatype for, minus the exclusions named in
// nonRoundTripKinds. It is derived rather than hand-listed so that a kind added
// to the transport is either pushed here or explicitly excluded — a hand-listed
// copy agrees on the day it is written and then diverges in silence.
var publishableKinds = func() map[string]bool {
	m := map[string]bool{}
	for _, kind := range oci.MediaTypeKinds() {
		if _, excluded := nonRoundTripKinds[kind]; !excluded {
			m[kind] = true
		}
	}
	return m
}()

// TestPublishableKindsDerivation checks the half of the derivation above that is
// not structural.
//
// Two directions matter, and only one of them needs a test. That the walk covers
// every kind the transport resolves is guaranteed by construction — the set is
// the transport's own kinds minus the exclusions, so it cannot omit a kind, and
// an assertion to that effect could never fail. The other direction is not
// guaranteed: an exclusion is a hand-written key, so it can name a kind the
// transport stopped resolving (or never resolved, on a typo), and it can be added
// with no reason recorded. Either way the walk would quietly narrow and the
// derivation's whole point — that a kind is skipped only deliberately — would be
// lost. That is what this checks.
//
// The non-emptiness checks are the instrument's calibration: an empty transport
// set or an empty walk set would make every assertion here vacuous, and a vacuous
// pass reads exactly like a real one.
func TestPublishableKindsDerivation(t *testing.T) {
	kinds := oci.MediaTypeKinds()
	if len(kinds) == 0 {
		t.Fatal("the transport resolves no kinds at all, so the checks below assert nothing")
	}
	if len(publishableKinds) == 0 {
		t.Fatal("the walk pushes no kinds at all — the exclusions have swallowed the whole set")
	}
	resolved := map[string]bool{}
	for _, kind := range kinds {
		resolved[kind] = true
	}
	for kind, reason := range nonRoundTripKinds {
		if !resolved[kind] {
			t.Errorf("kind %q is named as excluded from this round-trip, but the transport "+
				"resolves no mediatype for it — the exclusion is stale or misspelled, and it "+
				"is silently excluding nothing", kind)
		}
		if reason == "" {
			t.Errorf("kind %q is excluded with no reason recorded", kind)
		}
	}
}

// pushCatalog walks catalogDir and pushes every entity in publishableKinds to
// host through the real push path, which is what a bulk catalog publisher does
// one file at a time. Returns the count pushed per kind so the test can assert
// coverage.
//
// The workspace is declared here rather than at each push, and deliberately is
// not catalogDir: a publisher needs a destination registry and a file to read,
// nothing from the tree it is reading, and catalogDir is sometimes a directory
// this test does not own. One declaration serves the whole walk because nothing
// in the walk writes workspace configuration.
func pushCatalog(t *testing.T, host, catalogDir string) map[string]int {
	t.Helper()
	rw := resolveTempWorkspace(t, tempWorkspaceOptions{})
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
		if perr := pushRun(t.Context(), pushConfig{Workspace: rw, EntityPath: path, Registry: host}); perr != nil {
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

func sha256HexFile(t *testing.T, path string) string {
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
	// Resolve the reference before any chdir: a core provider resolves from the
	// binary's embedded entity, so both the offline reference build and the
	// registry-tier build below route compose through the SAME embedded provider —
	// the reference is the offline path's rendered output, the byte-identity target.
	std := standardCatalogDir(t)
	wantSHA := offlineReferenceCompose(t, std, catalogOnlyExampleDir(t))

	host := startRegistry(t, nil)
	counts := pushCatalog(t, host, std)
	// Every publishable kind present in the standard catalog pushed clean.
	for _, k := range []string{"Component", "Feature", "Recipe", "FeatureRecipe", "SchemaLibrary"} {
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
	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), ReleasePath: "release.yaml"}); err != nil {
		t.Fatalf("registry pull: %v", err)
	}
	if err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), "release.yaml", nil, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("registry build: %v", err)
	}
	got := sha256HexFile(t, filepath.Join("dist", "compose", "docker-compose.yaml"))
	if got != wantSHA {
		t.Errorf("registry build output sha = %s; want offline reference %s", got, wantSHA)
	}

	// lock == build: rebuild from the generated lock (no re-pull), byte-identical.
	if _, _, err := lockfile.LoadLockfile(lockfile.PathFor("release.yaml")); err != nil {
		t.Fatalf("lockfile absent after pull: %v", err)
	}
	_ = os.RemoveAll("dist")
	if err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), "release.yaml", nil, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("rebuild from lock: %v", err)
	}
	if rebuilt := sha256HexFile(t, filepath.Join("dist", "compose", "docker-compose.yaml")); rebuilt != got {
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
	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), ReleasePath: "release.yaml"}); err != nil {
		t.Fatalf("pull: %v", err)
	}

	// Pin present: a legitimately-present reference resolves cleanly (no false halt).
	if err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), "release.yaml", nil, false, os.Stderr, os.Stdout); err != nil {
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
	err = dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), "release.yaml", nil, false, os.Stderr, os.Stdout)
	if err == nil {
		t.Fatal("build SUCCEEDED with a referenced entity withheld; the unified view silently proceeded on a gap")
	}
	if !errors.Is(err, closure.ErrUnresolvableURI()) {
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
			wantErr: resolver.ErrSchemaConformance(),
		},
		{
			// This case drives the OPERATOR edge surface rather than the Aggregate's
			// own uses block, and the asymmetry is the point rather than fixture
			// convenience. A component's own `uses:` may name a socket its catalog
			// Component does not declare: the edge is built and delivered, and the IR
			// edge records that the socket was author-named rather than
			// catalog-declared, so a governance policy can refuse the connection
			// outright without the compiler taking a position on it. A typo'd socket
			// name in the Aggregate therefore builds cleanly and is not a guard
			// negative — do not re-point this case at aggregate.yaml.
			//
			// The Release is where an undeclared socket still halts: it may wire and
			// configure an edge within a socket the consuming component declares, but
			// it may not invent the socket, since that would introduce topology the
			// Aggregate never declared.
			name: "release_unknown_uses_socket",
			mutate: func(t *testing.T, ws string) {
				// web is core/devtool/adminer, whose usesSchema declares `databases`;
				// `databases-typo` is undeclared, so the Release wires an edge into a
				// socket that does not exist on the consuming component.
				mutateFile(t, ws, "release.yaml",
					"      infra:\n        port: 8080\n",
					"      uses:\n        databases-typo:\n          db:\n            target: db\n      infra:\n        port: 8080\n")
			},
			wantErr: resolver.ErrUnknownUsesSocket(),
		},
		{
			name: "self_inject_forbidden",
			mutate: func(t *testing.T, ws string) {
				// mariadb's spec.inject allowlist is [env, file]; a self-inject via
				// the non-allowlisted `arg` mechanism is forbidden.
				mutateFile(t, ws, "aggregate.yaml", "      values:\n        database: appdb", "      inject:\n        arg:\n          - in: X\n            from: host\n      values:\n        database: appdb")
			},
			wantErr: resolver.ErrSelfInjectForbidden(),
		},
		{
			name: "invalid_inject_key",
			mutate: func(t *testing.T, ws string) {
				// adminer's uses-inject `from: host` resolves against the db
				// target's provides; `from: not-a-provides-key` does not exist on
				// the target — guard_inject (the 5th silent-skipper) must reject it.
				mutateFile(t, ws, "aggregate.yaml", "                  from: host", "                  from: not-a-provides-key")
			},
			wantErr: resolver.ErrInvalidInjectKey(),
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
			err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), "release.yaml", nil, false, os.Stderr, os.Stdout)
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
	// Resolve the example dir from the original working directory BEFORE any chdir
	// (the resolver walks "../.." from cwd); resolving it after the lint chdir into
	// a temp dir would misresolve and skip the build half of the capstone.
	exampleDir := catalogOnlyExampleDir(t)
	// The reference is an independent offline build in its own workspace; comparing
	// this capstone's offline build to it asserts two independent offline builds are
	// byte-identical (the deterministic-output invariant), resolving compose through
	// the same embedded provider both times.
	wantSHA := offlineReferenceCompose(t, std, exampleDir)
	// Lint enumerates + validates via its own eager whole-catalog load, wholly
	// independent of any build closure. Run it from a bare working directory with
	// no Release and no lockfile present: there is no closure for it to read, so a
	// warning-free result comes purely from the eager enumeration the build's
	// closure-backed path does not touch — the retained-eager-load split.
	t.Chdir(t.TempDir())
	if err := validateCatalogWide("", "", []string{std}, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("whole-catalog lint not warning-free (independent eager enumeration): %v", err)
	}

	ws := t.TempDir()
	copyTree(t, exampleDir, ws)
	_ = os.Remove(filepath.Join(ws, "release.lock"))
	_ = os.RemoveAll(filepath.Join(ws, ".aggrega"))
	_ = os.RemoveAll(filepath.Join(ws, "dist"))
	t.Chdir(ws)
	// Build is read-only by default: prime the lockfile with an explicit pull (the
	// sole writer of pins), then build reads it. --catalog supplies every entity
	// locally, so the pull resolves from the catalog with no network.
	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), ReleasePath: "release.yaml", CatalogDirs: []string{std}}); err != nil {
		t.Fatalf("prime lockfile via pull: %v", err)
	}
	if err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), "release.yaml", []string{std}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("catalog-only build against the shared catalog: %v", err)
	}
	if got := sha256HexFile(t, filepath.Join("dist", "compose", "docker-compose.yaml")); got != wantSHA {
		t.Errorf("offline example build sha = %s; want reference %s", got, wantSHA)
	}
}

// publishPluginEntity publishes a REAL kind:Plugin entity to the in-process
// registry through the production entity-publish path (pushRun with --plugin): the
// entity and its .wasm land as ONE OCI manifest tagged by metadata.version — the
// entity YAML is the manifest's config blob, the .wasm an inline layer addressed
// by content digest. spec.plugin is the digest-free artifact identity
// (oci://<host>/plugin/<pluginType>/<name>); the version comes from
// metadata.version, so the version tag resolves to both the entity and the .wasm
// at once. The entity's authored exports agree with the committed frp-step-go
// fixture's describe() (export "step", operates=patch, scope=own), so the
// publish-time cross-check passes. name is the plugin's BARE metadata.name (e.g.
// "patch-server"); the socket is pipelineStepPluginType. pluginPath is the pre-built
// fixture (resolved before any chdir). Returns the .wasm content digest the
// consumer pins at pull.
func publishPluginEntity(t *testing.T, host, name, version, pluginPath string) string {
	t.Helper()
	return publishPluginEntityWithRuntime(t, host, name, version, pluginPath, "")
}

// publishPluginEntityWithRuntime is publishPluginEntity plus an optional
// authored spec.runtime block. runtimeBlock is the YAML body indented two
// spaces under spec (e.g. "  runtime:\n    timeoutMs: 1\n"); an empty string
// authors no runtime block, which is the bare publishPluginEntity behaviour.
// Authoring an explicit runtime block is how a test drives an entity-declared
// execution constraint (a memory or per-call wall-time cap) through the publish
// path, so the constraint can be observed flowing to the build-time load.
func publishPluginEntityWithRuntime(t *testing.T, host, name, version, pluginPath, runtimeBlock string) string {
	t.Helper()
	wasmBytes, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read plugin fixture %s: %v", pluginPath, err)
	}
	contentDigest := digest.ContentDigest(wasmBytes)

	entityDir := t.TempDir()
	entityPath := filepath.Join(entityDir, "plugin.yaml")
	// metadata.name is the BARE leaf; the socket is metadata.pluginType. The
	// identity coordinate <pluginType>/<name> and the OCI location derive from the
	// pair — spec.plugin is the digest-free artifact identity, the version is
	// supplied by metadata.version, and the .wasm rides inline in the entity's
	// single manifest at <repo>:<version>.
	writeFixture(t, entityPath, `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: `+name+`
  pluginType: `+pipelineStepPluginType+`
  version: "`+version+`"
spec:
  abiVersion: v1alpha1
  exports:
    - name: step
      operates: patch
      scope: own
`+runtimeBlock)
	if err := pushRun(t.Context(), pushConfig{Workspace: resolveTempWorkspace(t, tempWorkspaceOptions{Root: entityDir}), EntityPath: entityPath, PluginPath: pluginPath, Registry: host}); err != nil {
		t.Fatalf("publish plugin entity %s/%s:%s: %v", pipelineStepPluginType, name, version, err)
	}
	return contentDigest
}

// TestIntegration_PipelinePluginPinning_RoundTrip proves the full publish → pull →
// build round-trip for a recipe/FR-referenced Plugin entity under the
// single-manifest model. A controlled fixture catalog declares a Component with a
// pipeline-bearing Recipe and a Feature with a pipeline-bearing FeatureRecipe;
// both reference their pipeline plugin by the BARE Plugin entity identity
// (<socket>/<name>:<version>). Both plugins are published as REAL kind:Plugin
// entities: ONE OCI manifest tagged by metadata.version carries the entity YAML as
// its config blob and the .wasm as an inline layer.
//
// The round-trip asserts, from real invocations against the in-process registry:
//   - the version tag resolves to ONE manifest carrying BOTH the entity and the
//     inline .wasm layer;
//   - after a real pull the lockfile carries BOTH the plugin ENTITY rows (in
//     entities, referenced_from the recipe / feature-recipe URI, with a pinned
//     manifest digest since the entity resolves from the manifest config blob) AND
//     the .wasm ARTIFACT rows (in plugins, referenced_from the entity URI, digest =
//     resolved_version = the .wasm content digest, with a pinned manifest digest);
//   - a build resolves each bare entity reference AND reads the cached .wasm,
//     rendering successfully;
//   - a second pull + build reproduces the rendered tree byte-for-byte;
//   - a frozen rebuild against a tampered cached plugin manifest is REJECTED.
func TestIntegration_PipelinePluginPinning_RoundTrip(t *testing.T) {
	// Resolve the committed .wasm fixture BEFORE any chdir (umbrella-root discovery
	// walks up from cwd); a clean skip if the toolchain is absent.
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")

	// The helm PROVIDER-rendering plugin is core infrastructure: its entity ships
	// in the standard catalog and its .wasm is embedded in the binary. It is
	// supplied locally (resolved before any chdir), so the entity under test — the
	// registry-published pipeline plugins — resolves over the wire while the render
	// provider resolves from the embedded core, exactly as a real workspace mixes a
	// registry catalog with core providers.
	std := standardCatalogDir(t)

	host := startRegistry(t, nil)

	const (
		recipePluginName = "patch-server"
		frPluginName     = "patch-credential"
	)
	// Plugin identity coordinates <pluginType>/<name>:<version> — the canonical
	// reference shape. The pull binds <pluginType>/<name> to its OCI location
	// <registry>/plugin/<lower(pluginType)>/<lower(name)> itself; a
	// registry-qualified reference would be refused with ErrQualifiedPluginReference.
	recipePluginURI := types.PluginCoordinate(pipelineStepPluginType, recipePluginName) + ":1.0.0"
	frPluginURI := types.PluginCoordinate(pipelineStepPluginType, frPluginName) + ":1.0.0"

	// A controlled fixture catalog: a Component implemented by a pipeline-bearing
	// Recipe, and a (secret) Feature implemented by a pipeline-bearing FeatureRecipe.
	// The committed frp-step-go fixture self-describes as a helm/helm-chart
	// compatible pipeline-step plugin that content-selects a Deployment, PATCHes an
	// own-scope annotation, and EMITs an own-scope ConfigMap. So the fixture
	// catalog renders under the helm provider: the Recipe produces a Deployment the
	// recipe-plugin patches, and the resource-generating FeatureRecipe runs its own
	// pipeline (the artifacts: entry makes it resource-generating, which is what
	// triggers the FR pipeline to execute).
	cat := t.TempDir()
	writeFixtureEntity(t, cat, "component/test/svc/1.0.0/component.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: test/svc
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    infra:
      port: { type: integer, default: 8080 }
      replicas: { type: integer, default: 1 }
`)
	writeFixtureEntity(t, cat, "recipe/helm/svc/1.0.0/recipe.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: svc
  version: "1.0.0"
spec:
  capability: helm
  implements: test/svc
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: {{ optional .infra `+"`replicas`"+` 1 }}
    containers:
      server:
        image: test-server:latest
        ports:
          web: {{ optional .infra `+"`port`"+` 8080 }}
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
	writeFixtureEntity(t, cat, "featurerecipe/helm/cred/1.0.0/recipe.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: FeatureRecipe
metadata:
  name: cred
  version: "1.0.0"
spec:
  capability: helm
  implements: test/cred
  artifacts:
    cred-config.yaml: |
      apiVersion: v1
      kind: ConfigMap
      metadata:
        name: cred-config
      data:
        source: frp
  pipeline:
    - id: patch
      plugin: `+frPluginURI+`
      function: step
`)
	pushCatalog(t, host, cat)

	// Publish both pipeline plugins as REAL Plugin entities (the .wasm fixture +
	// the stamped entity) so each resolves above Tier-1 and participates in the
	// lockfile pin on both axes. Both reuse the same committed .wasm fixture; their
	// distinct OCI locations give distinct artifact URIs (same content digest).
	recipePluginDigest := publishPluginEntity(t, host, recipePluginName, "1.0.0", pluginPath)
	frPluginDigest := publishPluginEntity(t, host, frPluginName, "1.0.0", pluginPath)
	// The .wasm ARTIFACT URIs are the entities' identity coordinates <pluginType>/
	// <name> — the single-manifest model addresses the .wasm by the entity's version
	// tag, not by a content-digest-pinned reference; the plugins rows pin these
	// coordinate URIs. Both reuse the same committed fixture, so the content digests
	// match while the locations differ.
	recipeArtifactURI := types.PluginCoordinate(pipelineStepPluginType, recipePluginName)
	frArtifactURI := types.PluginCoordinate(pipelineStepPluginType, frPluginName)

	// Single-manifest witness: the plugin repo's version tag resolves to ONE
	// manifest whose config blob is the entity YAML and whose inline layer is the
	// .wasm — proven by a real registry read, not inferred from the lockfile.
	assertSinglePluginManifest(t, host, recipePluginName, "1.0.0", recipePluginDigest)

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
      features:
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
        test/svc:1.0.0: helm/svc:1.0.0
    features:
      type:
        test/cred:1.0.0: helm/cred:1.0.0
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
	// The fixture plugins are published unsigned to a registry with no
	// signature requirement, so the real verify pass runs (it is NOT globally
	// disabled) and finds nothing to require — the verify-if-present default —
	// while this test exercises the plugin-pin path. The dedicated signed
	// closure suites cover a registry that requires signatures end to end.
	writeWorkspaceConfig(t, ws, "default_registry: \""+host+"\"\nauto_pull: true\noffline: false")
	t.Chdir(ws)

	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), ReleasePath: "release.yaml", CatalogDirs: []string{std}}); err != nil {
		t.Fatalf("registry pull: %v", err)
	}

	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor("release.yaml"))
	if err != nil {
		t.Fatalf("load lock: %v", err)
	}

	entityByURI := map[string]lockfile.Entity{}
	for _, e := range lf.Entities {
		entityByURI[e.URI] = e
	}
	pluginByURI := map[string]lockfile.Plugin{}
	for _, p := range lf.Plugins {
		pluginByURI[p.URI] = p
	}

	// Entity-axis pins: each plugin ENTITY (bare identity) is in lf.Entities,
	// referenced_from its declaring recipe / feature-recipe URI. The entity now
	// resolves from the single manifest's config blob, so a manifest digest is
	// pinned alongside the entity-YAML content digest.
	entityCases := []struct {
		uri          string
		wantRefedURI string
		label        string
	}{
		{recipePluginURI, "helm/svc:1.0.0", "Recipe pipeline plugin ENTITY"},
		{frPluginURI, "helm/cred:1.0.0", "FeatureRecipe pipeline plugin ENTITY"},
	}
	for _, tc := range entityCases {
		got, ok := entityByURI[tc.uri]
		if !ok {
			t.Fatalf("%s %q absent from written lockfile entities (have %v)", tc.label, tc.uri, lf.Entities)
		}
		if got.ResolvedVersion != "1.0.0" {
			t.Errorf("%s resolved_version = %q; want 1.0.0", tc.label, got.ResolvedVersion)
		}
		if got.ManifestDigest == "" {
			t.Errorf("%s pinned no manifest digest; the config-blob entity read must pin the manifest digest", tc.label)
		}
		if !containsRef(got.ReferencedFrom, tc.wantRefedURI) {
			t.Errorf("%s referenced_from = %v; want an entry naming %q", tc.label, got.ReferencedFrom, tc.wantRefedURI)
		}
	}

	// Artifact-axis pins: each .wasm ARTIFACT is in lf.plugins under its BARE
	// spec.plugin URI, referenced_from its Plugin entity URI, with the content
	// digest of the .wasm fixture and the manifest digest of the single manifest
	// the .wasm rides in.
	artifactCases := []struct {
		uri          string
		wantDigest   string
		wantRefedURI string
		label        string
	}{
		{recipeArtifactURI, recipePluginDigest, recipePluginURI, "Recipe pipeline plugin ARTIFACT"},
		{frArtifactURI, frPluginDigest, frPluginURI, "FeatureRecipe pipeline plugin ARTIFACT"},
	}
	for _, tc := range artifactCases {
		got, ok := pluginByURI[tc.uri]
		if !ok {
			t.Fatalf("%s %q absent from written lockfile plugins (have %v)", tc.label, tc.uri, lf.Plugins)
		}
		if got.Digest != tc.wantDigest {
			t.Errorf("%s digest = %q; want %q", tc.label, got.Digest, tc.wantDigest)
		}
		// The .wasm rides inline as a layer, addressed by content digest, so the
		// resolved_version IS the content digest (not a semver tag).
		if got.ResolvedVersion != tc.wantDigest {
			t.Errorf("%s resolved_version = %q; want the content digest %q", tc.label, got.ResolvedVersion, tc.wantDigest)
		}
		if got.ManifestDigest == "" {
			t.Errorf("%s pinned no manifest digest; the inline-layer .wasm must pin its single manifest's digest", tc.label)
		}
		if !containsRef(got.ReferencedFrom, tc.wantRefedURI) {
			t.Errorf("%s referenced_from = %v; want an entry naming %q", tc.label, got.ReferencedFrom, tc.wantRefedURI)
		}
	}

	// Build: each bare entity reference resolves through the entity-front (reading
	// the entity from the cached manifest config blob) and the build reads the
	// cached inline .wasm. This is the publish → pull → BUILD proof.
	if err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), "release.yaml", []string{std}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("registry build (resolving the bare plugin entity refs from cache): %v", err)
	}
	firstTree := hashDistTree(t, "dist")

	// A second pull + build reproduces the rendered tree byte-for-byte from the pin.
	_ = os.RemoveAll("dist")
	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), ReleasePath: "release.yaml", CatalogDirs: []string{std}}); err != nil {
		t.Fatalf("second registry pull: %v", err)
	}
	if err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), "release.yaml", []string{std}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("second registry build: %v", err)
	}
	if second := hashDistTree(t, "dist"); second != firstTree {
		t.Errorf("second build not byte-identical: %s != %s", second, firstTree)
	}

	// Freeze the build (auto_pull off) over the untampered cache first. Every build
	// above filled its own lockfile, and a build that fills skips the drift verify,
	// so this is the one build in the test that holds the pulled lockfile to the
	// closure the build reconstructs — every recipe here and both plugins were
	// resolved from the registry, so the whole closure arrives from the cache.
	writeWorkspaceConfig(t, ws, "default_registry: \""+host+"\"\nauto_pull: false\noffline: false")
	_ = os.RemoveAll("dist")
	if err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), "release.yaml", []string{std}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("frozen build over the pulled lockfile refused it: %v", err)
	}
	if frozen := hashDistTree(t, "dist"); frozen != firstTree {
		t.Errorf("frozen build not byte-identical to the filling build: %s != %s", frozen, firstTree)
	}

	// Tamper: corrupt the cached plugin entity's persisted manifest blob and build
	// frozen again, so it resolves strictly from cache rather than re-pulling and
	// healing. The build's pinned-integrity gate, which reads every pinned cache slot
	// before the closure is walked, must REJECT the tampered slot rather than
	// silently build on it — and the refusal must be an integrity condition, not any
	// error: a build that reached the drift verify instead would also fail, and would
	// pass an "any error" check while reporting the wrong thing.
	//
	// WHICH integrity condition is fixed by the entity row this build reads, and that
	// row carries its manifest digest. The lockfile on disk was last written by the
	// second build's fill (auto_pull is on for every build above), and a pull that
	// finds the plugin entity already in the cache records the manifest digest of the
	// manifest persisted beside it — the digest the cold first pull recorded, which is
	// what the entity-axis assertion above reads. So the gate holds the cached
	// manifest to that pin first, and the tampered manifest is refused against its pin
	// with the cached-manifest condition, before content is checked against it. The
	// assertion is therefore tied to what a warm pull records for a cached entity: a
	// row that had lost its manifest digest would skip that check and be refused one
	// check later, as the fetch-integrity condition of content checked against a
	// manifest that no longer parses.
	entitySlot := oci.CachePath(ws, ".aggrega/cache", recipePluginURI, "1.0.0")
	tamperedManifest := filepath.Join(entitySlot, "manifest")
	if _, err := os.Stat(tamperedManifest); err != nil {
		t.Fatalf("expected a persisted plugin-entity manifest at %s (the config-blob read must persist one): %v", tamperedManifest, err)
	}
	if err := os.WriteFile(tamperedManifest, []byte("{ not a manifest }"), 0o644); err != nil {
		t.Fatalf("tamper cached manifest: %v", err)
	}
	_ = os.RemoveAll("dist")
	err = dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), "release.yaml", []string{std}, false, os.Stderr, os.Stdout)
	if err == nil {
		t.Fatal("frozen build SUCCEEDED against a tampered plugin manifest; the pinned-integrity gate did not reject it")
	}
	if errors.Is(err, lockfile.ErrLockfileDrift()) {
		t.Fatalf("frozen build over a tampered plugin manifest refused as lockfile drift, not as an integrity failure: %v", err)
	}
	if !errors.Is(err, oci.ErrCachedManifestIntegrity()) {
		t.Fatalf("frozen build over a tampered plugin manifest refused with the wrong condition; want the cached-manifest "+
			"refusal of a manifest that no longer hashes to the digest the entity row pins: %v", err)
	}
}

// assertSinglePluginManifest resolves the plugin repo's version tag against the
// registry and asserts it is ONE manifest carrying BOTH the Plugin entity YAML
// (config blob) and the inline .wasm (layer) — the single-manifest publish shape.
// wantWasmDigest is the .wasm content digest the caller published; the fetched
// layer must hash to it, tying the manifest to the artifact axis.
func assertSinglePluginManifest(t *testing.T, host, name, version, wantWasmDigest string) {
	t.Helper()
	loc := catalog.OCILocation("Plugin", pipelineStepPluginType, name)
	repo, err := oci.NewRemoteRepository(host+"/"+loc, &http.Client{})
	if err != nil {
		t.Fatalf("new repo for %s: %v", loc, err)
	}
	entityYAML, wasm, manifestDigest, err := oci.FetchPluginManifest(context.Background(), repo, version)
	if err != nil {
		t.Fatalf("fetch single plugin manifest %s:%s: %v", loc, version, err)
	}
	if !strings.Contains(string(entityYAML), "kind: Plugin") {
		t.Errorf("manifest config blob is not a Plugin entity YAML: %q", string(entityYAML))
	}
	if len(wasm) == 0 {
		t.Error("manifest carries no inline .wasm layer")
	}
	if got := digest.ContentDigest(wasm); got != wantWasmDigest {
		t.Errorf("inline .wasm digest = %q; want %q", got, wantWasmDigest)
	}
	if manifestDigest == "" {
		t.Error("single plugin manifest resolved with an empty manifest digest")
	}
}

// writePipelinePluginFixtureCatalog writes a controlled fixture catalog with a
// single helm-provider Component whose Recipe carries a one-step pipeline that
// references the named Plugin entity by its bare identity, and writes a
// consumer workspace (aggregate / manifest / release / workspace config) wired to
// build that Component over the registry at host. The committed frp-step-go
// fixture self-describes as a helm-compatible pipeline-step plugin (export "step",
// patch, own-scope), so the Recipe renders under the helm provider and the build
// invokes the plugin. Returns the consumer workspace path (not yet chdir'd into).
//
// This is the shared scaffolding for the runtime-limit-flows-to-build proof: two
// arms build the SAME catalog + workspace shape, differing only in the published
// plugin entity's authored runtime block, so a fail-vs-succeed outcome is
// attributable to that one authored difference and nothing else.
func writePipelinePluginFixtureCatalog(t *testing.T, host, pluginURI string) string {
	t.Helper()
	cat := t.TempDir()
	writeFixtureEntity(t, cat, "component/test/svc/1.0.0/component.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: test/svc
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    infra:
      port: { type: integer, default: 8080 }
      replicas: { type: integer, default: 1 }
`)
	writeFixtureEntity(t, cat, "recipe/helm/svc/1.0.0/recipe.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: svc
  version: "1.0.0"
spec:
  capability: helm
  implements: test/svc
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: {{ optional .infra `+"`replicas`"+` 1 }}
    containers:
      server:
        image: test-server:latest
        ports:
          web: {{ optional .infra `+"`port`"+` 8080 }}
  pipeline:
    - id: patch
      plugin: `+pluginURI+`
      function: step
`)
	pushCatalog(t, host, cat)

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
        test/svc:1.0.0: helm/svc:1.0.0
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
	// The fixture plugin is published unsigned to a registry with no signature
	// requirement, so the real verify pass runs (it is NOT globally disabled)
	// and finds nothing to require — the verify-if-present default — while this
	// test exercises the plugin-pin path. The dedicated signed closure suites
	// cover a registry that requires signatures end to end.
	writeWorkspaceConfig(t, ws, "default_registry: \""+host+"\"\nauto_pull: true\noffline: false")
	return ws
}

// TestIntegration_PipelinePlugin_RuntimeTimeoutFlowsToBuild closes the one
// lifecycle leg the round-trip test does not exercise: an entity-authored runtime
// LIMIT flowing publish → pull → build to the load. The round-trip / cross-check /
// trust / limits-enforcement legs are proven elsewhere and are NOT rebuilt here;
// the round-trip publishes the plugin with NO runtime block, so the
// entity.runtime.limits → published entity → pulled → build-load chain is what
// this test adds end-to-end.
//
// The proof is a fail-vs-succeed contrast on the one authored difference. The
// fail arm publishes a Plugin entity authoring spec.runtime.timeoutMs: 1 — a
// per-call execution ceiling far below what a real guest's describe()/step Call
// needs. The ceiling is Call-scoped, and the build invokes the plugin, so the
// 1 ms ceiling trips at the guest Call and the build halts with the loader's
// ceiling cancellation. The success arm publishes the SAME fixture against the SAME
// catalog + workspace shape with a generous runtime.timeoutMs, and the build
// completes. Because the two arms differ ONLY in the authored runtime block, the
// fail-vs-succeed outcome is attributable to the entity's authored timeout
// reaching the loader — proving the limit flowed the full publish → pull → build
// path, not that some unrelated error fired.
func TestIntegration_PipelinePlugin_RuntimeTimeoutFlowsToBuild(t *testing.T) {
	// Resolve the committed .wasm fixture BEFORE any chdir (umbrella-root discovery
	// walks up from cwd); a clean skip if the toolchain is absent.
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")

	// The helm PROVIDER-rendering plugin is core infrastructure: its entity ships
	// in the standard catalog and its .wasm is embedded in the binary. It is
	// supplied locally (resolved before any chdir) so the core provider resolves
	// from the embedded core while the registry-published pipeline plugin under
	// test resolves over the wire — the same mix a real workspace runs.
	std := standardCatalogDir(t)

	const pluginName = "patch-server"
	// Plugin identity coordinate <pluginType>/<name>:<version> — the canonical
	// reference shape the recipe pipeline step names; the pull binds
	// <pluginType>/<name> to its OCI location in the registry.
	pluginURI := types.PluginCoordinate(pipelineStepPluginType, pluginName) + ":1.0.0"

	t.Run("tiny_timeout_fails_at_guest_call", func(t *testing.T) {
		host := startRegistry(t, nil)
		// Author spec.runtime.timeoutMs: 1 — an execution ceiling a real guest's
		// Call cannot meet. Indented two spaces under spec.
		publishPluginEntityWithRuntime(t, host, pluginName, "1.0.0", pluginPath,
			"  runtime:\n    timeoutMs: 1\n")
		ws := writePipelinePluginFixtureCatalog(t, host, pluginURI)
		t.Chdir(ws)

		if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), ReleasePath: "release.yaml", CatalogDirs: []string{std}}); err != nil {
			t.Fatalf("registry pull: %v", err)
		}

		err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), "release.yaml", []string{std}, false, os.Stderr, os.Stdout)
		if err == nil {
			t.Fatal("build SUCCEEDED with an entity-authored timeoutMs: 1; the tiny per-call cap did not reach the loader")
		}
		// Attributability: the failure must be the per-call execution ceiling, not
		// any error. The loader names that condition itself — the runtime alone
		// would report only that a module closed, which several unrelated paths
		// also produce — so the phrase matched here is the one the ceiling
		// contributes and nothing else does. It is matched by walking the error
		// chain, because the ceiling trips while the guest's language runtime is
		// initialising and the cancellation is rendered into message text rather
		// than carried as a wrapped error value, so matching on a context error
		// over the chain does not reach it. The top CompilerError flattens its own
		// Error() to its message, so the scan must traverse the chain rather than
		// read err.Error() alone.
		const ceilingPhrase = "ran past its 1ms execution ceiling and was cancelled"
		if !chainContains(err, ceilingPhrase) {
			t.Fatalf("build err chain = %q; want a %q cancellation — the entity's timeoutMs: 1 must surface as a per-call execution-ceiling cancellation at the guest Call (not an unrelated error)",
				chainString(err), ceilingPhrase)
		}
		t.Logf("build halted attributably on the entity-authored timeout cap; chain:\n%s", chainString(err))
	})

	t.Run("generous_timeout_succeeds", func(t *testing.T) {
		host := startRegistry(t, nil)
		// The SAME fixture + catalog + workspace shape, with a generous execution
		// ceiling the guest Call comfortably meets. The only difference from the fail
		// arm is this authored runtime block, so the build completing here vs.
		// halting there is attributable to the authored timeout reaching the loader.
		publishPluginEntityWithRuntime(t, host, pluginName, "1.0.0", pluginPath,
			"  runtime:\n    timeoutMs: 60000\n")
		ws := writePipelinePluginFixtureCatalog(t, host, pluginURI)
		t.Chdir(ws)

		if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), ReleasePath: "release.yaml", CatalogDirs: []string{std}}); err != nil {
			t.Fatalf("registry pull: %v", err)
		}
		if err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), "release.yaml", []string{std}, false, os.Stderr, os.Stdout); err != nil {
			t.Fatalf("build FAILED under a generous entity-authored timeout; the same shape that succeeds with timeoutMs: 60000 must build: %v", err)
		}
	})
}

// chainString renders an error and every error it wraps, one per line, for a
// diagnostic that survives CompilerError flattening its own Error() to the top
// message. (chainContains, the sibling membership check, lives in build_test.go
// and is shared across the package's test binary.)
func chainString(err error) string {
	var b strings.Builder
	for e := err; e != nil; e = errors.Unwrap(e) {
		b.WriteString(e.Error())
		b.WriteString("\n")
	}
	return b.String()
}

// containsRef reports whether any referenced-from entry contains substr.
func containsRef(refs []string, substr string) bool {
	for _, r := range refs {
		if strings.Contains(r, substr) {
			return true
		}
	}
	return false
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
  capability: compose
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
      features:
        root_password:
          type: core/secret-value
          values:
            key: PW
            source: x
`)
	return dir
}

// TestIntegration_RequiredSocket_AuthoredFixture isolates the required-socket
// rule: a fixture Component with a required uses socket, referenced with the
// socket UNBOUND, must fail the registry build with ErrMissingRequiredSocket —
// and with that SPECIFIC sentinel, so a failure on an earlier check (missing
// recipe / values) surfaces as "wrong check", not a false pass.
//
// It reaches the rule through a real registry build, which is what no in-process
// proof of the rule can do: the fixture Component is pulled from the registry, so
// the requirement this asserts on is read off an entity that travelled through
// publication and back rather than one assembled in memory.
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

	err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), "release.yaml", nil, false, os.Stderr, os.Stdout)
	if err == nil {
		t.Fatalf("required-socket: build SUCCEEDED; want failure with ErrMissingRequiredSocket")
	}
	if !errors.Is(err, resolver.ErrMissingRequiredSocket()) {
		t.Fatalf("required-socket: build err = %v; want errors.Is(ErrMissingRequiredSocket) (a different error means the wrong check fired)", err)
	}
}

// TestIntegration_KindCoverage_Aggregate round-trips the published-Aggregate kind
// the catalog-only build does not exercise, directly through the real OCI client,
// asserting it pulls non-empty from the registry. Together with the positive build
// (Component/Recipe/FeatureRecipe/Feature/libraries) this covers every publishable
// kind fetched-from-registry.
func TestIntegration_KindCoverage_Aggregate(t *testing.T) {
	host := startRegistry(t, nil)
	pushCatalog(t, host, standardCatalogDir(t))
	pushCatalog(t, host, authoredFixtureCatalog(t))

	for _, ent := range []struct{ kind, ref string }{
		{"Aggregate", host + "/aggregate/test/app-agg"},
	} {
		repo, err := oci.NewRemoteRepository(ent.ref, &http.Client{})
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
	err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), ReleasePath: "release.yaml"})
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
// with every section that describes the pull rather than the closure removed.
// Everything that IS a pin (per-entity / per-library URI, resolved_version,
// digest, the referenced-from and consumer sets) is captured. Comparing this
// extracted form (not a raw on-disk file diff) is the invariance check:
// gitignored lockfiles have no committed baseline, and a raw diff would trip on
// the waived sections.
//
// The removal routes through lockfile.ParityWaived, the one home for that
// tolerance, rather than zeroing named fields here. That matters for more than
// tidiness: the goldens this feeds are COMMITTED, so a waived section left in
// would write a build-dependent value into version-controlled test data — the
// toolchain stamp carries the compiler version, so cutting a release would have
// to update four goldens, and a golden would fail on any binary whose version
// differs from the one that last regenerated it. A section added to the waiver is
// therefore removed from these goldens automatically.
func canonicalPins(t *testing.T, lockPath string) []byte {
	t.Helper()
	lf, _, err := lockfile.LoadLockfile(lockPath)
	if err != nil {
		t.Fatalf("load lock %s: %v", lockPath, err)
	}
	out, err := yaml.Marshal(lf)
	if err != nil {
		t.Fatalf("marshal pins: %v", err)
	}
	return lockfile.ParityWaived(out)
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
		{"annotation-driven-monitoring", "annotation-driven-monitoring", "release.yaml", nil, true},
		{"mariadb-adminer-helm-chart", "mariadb-adminer", "release-helm-chart.yaml", []string{"catalog-helm-chart"}, true},
		{"mariadb-adminer-helm-synth", "mariadb-adminer", "release-helm-synth.yaml", []string{"catalog-helm-synth"}, true},
		{"cross-boundary-networking-compose", "cross-boundary-networking", "release-compose.yaml", []string{"catalog-compose"}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ws := t.TempDir()
			copyTree(t, filepath.Join(examplesRoot, tc.exampleDir), ws)
			// Drop the example's workspace config: its catalog_dirs are relative to
			// the real example location and do not resolve from the temp copy. We
			// supply the catalog dirs explicitly (absolute) instead — the same
			// local + standard stack the example resolves against in place. The
			// removal is load-bearing for the declared workspace below: a config
			// already present at the declared root is left exactly as written, so
			// leaving the copied one in place would reinstate its unresolvable
			// catalog_dirs.
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
			if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), ReleasePath: tc.release, CatalogDirs: dirs, Upgrade: true}); err != nil {
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
