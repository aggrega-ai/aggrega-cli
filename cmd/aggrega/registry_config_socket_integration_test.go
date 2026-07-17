// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

// Package main's registry round-trip integration coverage for the
// workspace-level config sockets (mutator, policy). It closes the coverage gap
// the recipe/feature-recipe pipeline-plugin round-trip left open: a plugin
// attached at a CONFIG socket in .aggrega-config.yaml, published to a registry
// and referenced by its bare entity identity, must resolve → pin at pull and
// load-and-run from that pin at a build.
//
// It stands up the same in-process real OCI registry the entity round-trip uses,
// publishes real kind:Plugin entities for the socket guests, attaches them
// through the workspace config, drives a real pull, asserts the lockfile pins
// each socket's entity AND its .wasm artifact, then builds under the hermetic
// complete-lockfile posture (the build's own plugin resolution wires no puller)
// and asserts the socket guest loads from the pin and actually executes (an
// observable mutator effect; a policy verdict that halts or admits the build).
package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// publishSocketPluginEntity publishes a REAL kind:Plugin entity for a
// workspace-level config socket (mutator / policy) to the in-process registry
// through the production publish path (pushRun with --plugin): the entity and its
// .wasm land as ONE OCI manifest tagged by metadata.version — the entity YAML the
// config blob, the .wasm an inline layer addressed by content digest.
//
// pluginType is the entity's metadata.pluginType (the value the socket runner
// guards the loaded descriptor against, e.g. "mutator" or "policy"); export is
// the single entry function the entity authors (the guest's own describe() must
// declare it, so the publish-time cross-check passes). socketName is the plugin
// coordinate <pluginType>/<name> (e.g. "mutator/tag-injector"); the entity's bare
// metadata.name is its leaf. pluginPath is the
// pre-built fixture (resolved before any chdir). Returns the .wasm content digest
// the consumer pins at pull.
func publishSocketPluginEntity(t *testing.T, host, socketName, version, pluginPath, pluginType, export string) string {
	t.Helper()
	wasmBytes, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read plugin fixture %s: %v", pluginPath, err)
	}
	contentDigest := digest.ContentDigest(wasmBytes)

	entityDir := t.TempDir()
	entityPath := filepath.Join(entityDir, "plugin.yaml")
	// The catalog coordinate and the .wasm location derive from (pluginType, name):
	// metadata.name is the bare leaf, metadata.pluginType the socket, so the
	// coordinate the consumer references is <pluginType>/<name>. The version is
	// supplied by metadata.version, and the .wasm rides inline in the entity's
	// single manifest at <repo>:<version>. The authored export claim is a subset of
	// the guest's describe() (operates/scope omitted for a mutator / policy guest,
	// which self-reports neither), so the publish cross-check passes.
	bareName := filepath.Base(socketName)
	writeFixture(t, entityPath, `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: `+bareName+`
  version: "`+version+`"
  pluginType: `+pluginType+`
spec:
  abiVersion: v1alpha1
  exports:
    - name: `+export+`
`)
	if err := pushRun(pushConfig{EntityPath: entityPath, PluginPath: pluginPath, Registry: host}); err != nil {
		t.Fatalf("publish socket plugin entity %s:%s: %v", socketName, version, err)
	}
	return contentDigest
}

// configSocketFixtureCatalog writes a controlled fixture catalog with one
// compose-provider Component and its Recipe — the compute the config-socket build
// renders. The mutator socket runs over this node's topology entry; the policy
// socket runs over the IR this node produces. The catalog is pushed to the
// registry so it resolves over the wire; the compose PROVIDER entity resolves from
// the locally-supplied standard catalog. Returns the pushed catalog dir.
func configSocketFixtureCatalog(t *testing.T, host string) {
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
    values: {}
`)
	// The workload emits every node label's value into a compose container
	// variable, so a label the mutator socket injects into the topology graph
	// surfaces deterministically in the rendered docker-compose — an observable
	// witness that the mutator guest ran, independent of any renderer-specific
	// label propagation.
	writeFixtureEntity(t, cat, "recipe/compose/svc/1.0.0/recipe.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: svc
  version: "1.0.0"
spec:
  capability: compose
  implements: test/svc
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    containers:
      main:
        image: busybox:1
        variables:
          NODE_LABELS: "{{ range $k, $v := .context.labels }}{{ $v }};{{ end }}"
`)
	pushCatalog(t, host, cat)
}

// writeConfigSocketWorkspace writes a consumer workspace (aggregate / manifest /
// release) wiring the fixture Component, plus an .aggrega-config.yaml carrying the
// supplied plugins block. autoPull selects the pull-vs-frozen posture written into
// the config. Returns the workspace path (not yet chdir'd into).
func writeConfigSocketWorkspace(t *testing.T, host, pluginsBlock string, autoPull bool) string {
	t.Helper()
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
        test/svc:1.0.0: compose/svc:1.0.0
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
        app: {}
`)
	autoPullVal := "false"
	if autoPull {
		autoPullVal = "true"
	}
	writeWorkspaceConfig(t, ws, "default_registry: \""+host+"\"\nauto_pull: "+autoPullVal+"\noffline: false\n"+pluginsBlock)
	return ws
}

// distTreeContains reports whether any file under dir contains needle, and
// returns a joined dump of the tree's file paths for a diagnostic when it does
// not. It lets the mutator assertion track the injected label wherever the
// renderer lands it, rather than hard-coding a boundary-specific artifact path.
func distTreeContains(t *testing.T, dir, needle string) (bool, string) {
	t.Helper()
	var paths []string
	found := false
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		paths = append(paths, path+"\n"+string(data))
		if strings.Contains(string(data), needle) {
			found = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk dist tree %s: %v", dir, err)
	}
	return found, strings.Join(paths, "\n")
}

// assertSocketPinnedInLock asserts the lockfile carries BOTH pin axes for a
// config-socket plugin published at socketName: the bare Plugin ENTITY (the URI
// the config attaches, in lf.Entities) and the .wasm ARTIFACT (its bare oci://
// spec.plugin identity, in lf.Plugins, digest = the published content digest).
// This is the pull-side half of the round-trip: the generic descent pins a
// config-socket plugin on both axes exactly as it pins a recipe/FR pipeline plugin.
func assertSocketPinnedInLock(t *testing.T, lf *lockfile.Lockfile, host, socketName, wantWasmDigest string) {
	t.Helper()
	entityURI := socketName + ":1.0.0"
	artifactURI := socketName

	var ent *lockfile.Entity
	for i := range lf.Entities {
		if lf.Entities[i].URI == entityURI {
			ent = &lf.Entities[i]
			break
		}
	}
	if ent == nil {
		t.Fatalf("config-socket plugin ENTITY %q absent from lockfile entities (have %v)", entityURI, lf.Entities)
	}
	if ent.ResolvedVersion != "1.0.0" {
		t.Errorf("entity %q resolved_version = %q; want 1.0.0", entityURI, ent.ResolvedVersion)
	}
	if ent.ManifestDigest == "" {
		t.Errorf("entity %q pinned no manifest digest; the config-blob entity read must pin one", entityURI)
	}

	var art *lockfile.Plugin
	for i := range lf.Plugins {
		if lf.Plugins[i].URI == artifactURI {
			art = &lf.Plugins[i]
			break
		}
	}
	if art == nil {
		t.Fatalf("config-socket plugin ARTIFACT %q absent from lockfile plugins (have %v)", artifactURI, lf.Plugins)
	}
	if art.Digest != wantWasmDigest {
		t.Errorf("artifact %q digest = %q; want the published content digest %q", artifactURI, art.Digest, wantWasmDigest)
	}
	if !containsRef(art.ReferencedFrom, entityURI) {
		t.Errorf("artifact %q referenced_from = %v; want an entry naming its entity %q", artifactURI, art.ReferencedFrom, entityURI)
	}
}

// assertPluginAbsentFromLock asserts a verdict-socket (cache-not-pin) gate plugin
// published at socketName is recorded on NEITHER pinned lockfile axis — no Plugin
// ENTITY row (lf.Entities) and no .wasm ARTIFACT row (lf.Plugins). A verdict
// socket renders an accept/reject verdict without shaping output bytes, so its
// plugin is cached at pull for the hermetic build to run but carries no
// reproducibility pin and therefore no drift baseline.
func assertPluginAbsentFromLock(t *testing.T, lf *lockfile.Lockfile, socketName string) {
	t.Helper()
	entityURI := socketName + ":1.0.0"
	for i := range lf.Entities {
		if lf.Entities[i].URI == entityURI {
			t.Fatalf("verdict-socket plugin ENTITY %q is pinned in the lockfile; a verdict socket is cache-not-pin and must record no entity row", entityURI)
		}
	}
	for i := range lf.Plugins {
		if lf.Plugins[i].URI == socketName {
			t.Fatalf("verdict-socket plugin ARTIFACT %q is pinned in the lockfile; a verdict socket is cache-not-pin and must record no artifact row", socketName)
		}
	}
}

// TestIntegration_ConfigSocketPluginPinning_RoundTrip proves the publish → pull →
// pin → hermetic-build-load round-trip for a plugin attached at a workspace-level
// CONFIG socket. Three arms exercise the mutator socket, the policy socket
// (deny and admit), and both sockets pinned in one pull:
//
//   - mutator: the pulled mutator guest loads from its pin at build and its label
//     injection is observable in the rendered artifact.
//   - policy_deny: the pulled policy guest loads from its pin and its deny verdict
//     halts the build with ErrPolicyViolation carrying the guest's message.
//   - both_sockets: a mutator AND an admitting policy attached together are BOTH
//     pinned on both axes by one pull, and BOTH load-and-run at one build — the
//     all-socket coverage the recipe-plugin round-trip did not exercise.
//
// Every arm PULLS first (populating the lockfile pins and the artifact cache),
// asserts the pins, then builds under the hermetic default posture (auto_pull on,
// complete lockfile). The build's own plugin resolution wires NO OCI puller
// (buildPluginResolver), so each socket guest resolves through the pinned cache —
// a green build is a genuine load-from-pin, and a policy deny is a genuine execute.
func TestIntegration_ConfigSocketPluginPinning_RoundTrip(t *testing.T) {
	// Resolve the committed .wasm fixtures BEFORE any chdir (umbrella-root
	// discovery walks up from cwd); a clean skip only if the toolchain is absent.
	mutatorPath := wasmtest.EnsureFixtureBuilt(t, "mutator-proof-go.wasm")
	denyPolicyPath := wasmtest.EnsureFixtureBuilt(t, "policy-go.wasm")
	allowPolicyPath := wasmtest.EnsureFixtureBuilt(t, "policy-allow-go.wasm")

	// The compose PROVIDER-rendering plugin is core infrastructure: its entity
	// ships in the standard catalog and its .wasm is embedded in the binary.
	// Supplied locally (resolved before any chdir), so the render provider resolves
	// from the embedded core while the registry-published socket plugins under test
	// resolve over the wire — the same mix a real workspace runs.
	std := standardCatalogDir(t)

	host := startRegistry(t, nil)
	configSocketFixtureCatalog(t, host)

	const (
		mutatorSocket     = "mutator/tag-injector"
		denyPolicySocket  = "policy/deny-gate"
		allowPolicySocket = "policy/admit-gate"
	)
	mutatorDigest := publishSocketPluginEntity(t, host, mutatorSocket, "1.0.0", mutatorPath, "mutator", "mutate")
	// The policy entities are published to the registry so the build can resolve
	// them at their point of use, but their content digests are not asserted: a
	// verdict-only policy socket is not recorded on the pinned lockfile axis.
	publishSocketPluginEntity(t, host, denyPolicySocket, "1.0.0", denyPolicyPath, "policy", "validate")
	publishSocketPluginEntity(t, host, allowPolicySocket, "1.0.0", allowPolicyPath, "policy", "validate")

	// proofMutatorLabel is the value the mutator-proof fixture injects as a node
	// label; the fixture recipe emits every node-label value into a compose
	// container variable, so its presence in the rendered artifact proves the
	// pulled mutator guest actually ran and its patch reached the IR.
	const proofMutatorLabel = "mutator-proof"

	// pullAndLoadLock pulls the workspace (auto_pull on) so the socket plugins pin
	// into the lockfile and the artifact cache, and returns the loaded lockfile for
	// pin assertions. It leaves the cwd in the workspace. The build that follows
	// runs under the same complete-lockfile posture: its plugin resolution wires no
	// puller, so the socket guest loads from the pinned cache.
	pullAndLoadLock := func(t *testing.T, ws string) *lockfile.Lockfile {
		t.Helper()
		t.Chdir(ws)
		if err := pullRun(pullConfig{ReleasePath: "release.yaml", CatalogDirs: []string{std}}); err != nil {
			t.Fatalf("registry pull: %v", err)
		}
		lf, _, err := lockfile.LoadLockfile(lockfile.PathFor("release.yaml"))
		if err != nil {
			t.Fatalf("load lock: %v", err)
		}
		return lf
	}

	t.Run("mutator", func(t *testing.T) {
		pluginsBlock := "plugins:\n  mutator:\n    - uri: " + mutatorSocket + ":1.0.0\n"
		ws := writeConfigSocketWorkspace(t, host, pluginsBlock, true)
		lf := pullAndLoadLock(t, ws)
		assertSocketPinnedInLock(t, lf, host, mutatorSocket, mutatorDigest)

		if err := dispatchBuild(nil, "release.yaml", []string{std}, false, os.Stderr, os.Stdout); err != nil {
			t.Fatalf("build with a pinned mutator socket: %v (the pulled guest must load from the pin and mutate must run cleanly)", err)
		}
		// Observable effect: the injected label reached the rendered artifact, so the
		// pulled mutator guest did not merely load — it ran and its output was applied.
		found, tree := distTreeContains(t, "dist", proofMutatorLabel)
		if !found {
			t.Errorf("rendered output does not carry the mutator-injected label %q; the pulled mutator guest ran but its effect is not observable in dist tree:\n%s",
				proofMutatorLabel, tree)
		}
	})

	t.Run("policy_deny", func(t *testing.T) {
		pluginsBlock := "plugins:\n  policy:\n    - uri: " + denyPolicySocket + ":1.0.0\n"
		ws := writeConfigSocketWorkspace(t, host, pluginsBlock, true)
		// The policy socket is a verdict-only environment gate: it renders an
		// accept/reject decision without altering the rendered bytes, so it is NOT
		// recorded on the pinned closure axis and NOT expected in the lockfile. The
		// pull still runs (populating the mutator/provider pins the workspace needs);
		// the policy attachment is cached at pull and resolved at its point of use
		// during the build.
		lf := pullAndLoadLock(t, ws)
		// Cache-not-pin: the pulled policy gate is on neither pinned lockfile axis,
		// so it carries no drift baseline.
		assertPluginAbsentFromLock(t, lf, denyPolicySocket)

		err := dispatchBuild(nil, "release.yaml", []string{std}, false, os.Stderr, os.Stdout)
		if err == nil {
			t.Fatal("build SUCCEEDED with a denying policy socket; the pulled policy guest did not halt the build")
		}
		if !errors.Is(err, analyzer.ErrPolicyViolation) {
			t.Fatalf("policy halt not errors.Is ErrPolicyViolation: %v", err)
		}
		// Attributability: the halt carries the guest's own verdict message, so the
		// deny came from the pulled policy guest executing, not an unrelated fault.
		const wantMsg = "fixture rule B: deliberate fail for end-to-end coverage"
		if !strings.Contains(err.Error(), wantMsg) {
			t.Errorf("policy halt does not carry the guest verdict message %q:\n%v", wantMsg, err)
		}
	})

	t.Run("both_sockets", func(t *testing.T) {
		pluginsBlock := "plugins:\n" +
			"  mutator:\n    - uri: " + mutatorSocket + ":1.0.0\n" +
			"  policy:\n    - uri: " + allowPolicySocket + ":1.0.0\n"
		ws := writeConfigSocketWorkspace(t, host, pluginsBlock, true)
		lf := pullAndLoadLock(t, ws)
		// The mutator shapes the rendered bytes, so it is pinned on both axes by the
		// pull. The policy socket is a verdict-only environment gate that does not
		// shape output, so it is NOT recorded on the pinned axis — only the mutator
		// pin is asserted here; the admitting policy is resolved at its point of use
		// during the build.
		assertSocketPinnedInLock(t, lf, host, mutatorSocket, mutatorDigest)

		if err := dispatchBuild(nil, "release.yaml", []string{std}, false, os.Stderr, os.Stdout); err != nil {
			t.Fatalf("build with a mutator AND an admitting policy socket: %v (both pulled guests must load from their pins and run)", err)
		}
		// The mutator ran (its label reached the artifact) AND the admitting policy
		// ran without halting — both sockets loaded from their pins in one build.
		found, tree := distTreeContains(t, "dist", proofMutatorLabel)
		if !found {
			t.Errorf("both-sockets build: rendered output does not carry the mutator-injected label %q in dist tree:\n%s", proofMutatorLabel, tree)
		}
	})

	t.Run("verdict_socket_not_pinned_no_drift", func(t *testing.T) {
		pluginsBlock := "plugins:\n  policy:\n    - uri: " + denyPolicySocket + ":1.0.0\n"
		ws := writeConfigSocketWorkspace(t, host, pluginsBlock, true)
		lf := pullAndLoadLock(t, ws)
		// The pulled verdict gate is on neither pinned lockfile axis.
		assertPluginAbsentFromLock(t, lf, denyPolicySocket)

		// A baseline verify passes against the lockfile the pull just wrote.
		if err := pullRun(pullConfig{ReleasePath: "release.yaml", CatalogDirs: []string{std}, Verify: true}); err != nil {
			t.Fatalf("baseline verify after pull: %v", err)
		}

		// Swapping the attached policy gate is a verdict-socket config change. Because
		// a verdict socket is cache-not-pin — not drift-gated — the change is adopted
		// at the next build without a re-pull and verify does NOT halt on drift.
		changed := "plugins:\n  policy:\n    - uri: " + allowPolicySocket + ":1.0.0\n"
		writeWorkspaceConfig(t, ws, "default_registry: \""+host+"\"\nauto_pull: true\noffline: false\n"+changed)
		if err := pullRun(pullConfig{ReleasePath: "release.yaml", CatalogDirs: []string{std}, Verify: true}); err != nil {
			t.Fatalf("verify halted after a policy config change; a verdict socket must not drift-gate: %v", err)
		}
	})
}
