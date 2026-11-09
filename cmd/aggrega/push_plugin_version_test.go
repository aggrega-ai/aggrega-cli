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
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// versionedStepEntity renders the Plugin entity the frp-step-go fixture agrees
// with (export "step", operates patch, scope own) at the given metadata.version.
// withABI false omits spec.abiVersion, a field the Plugin schema requires, and
// changes nothing else.
func versionedStepEntity(version string, withABI bool) string {
	abi := "  abiVersion: v1alpha1\n"
	if !withABI {
		abi = ""
	}
	return fmt.Sprintf(`apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata:
  name: patch
  pluginType: pipeline-step
  version: %q
spec:
%s  pipelineStep:
    capabilities:
      - helm
  exports:
    - name: step
      operates: patch
      scope: own
`, version, abi)
}

// stepGuest returns the frp-step-go fixture reporting version through
// describe(): the unstamped fixture where version is the one it reports by
// default, and a build stamped with version otherwise.
func stepGuest(t *testing.T, version string) string {
	t.Helper()
	if version == "1.0.0" {
		return wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")
	}
	return wasmtest.EnsureStampedFixtureBuilt(t, "frp-step-go.wasm", version)
}

// publishBothWays publishes entityYAML over the guest at pluginPath through the
// real publication path twice — as a dry run, and for real into a fresh
// in-memory registry — and returns each outcome with the store the real
// publication wrote to. Each publication reads its entity from a directory of
// its own, so the directory rule never decides an outcome here.
func publishBothWays(t *testing.T, entityYAML, pluginPath string) (dryErr, realErr error, store *memory.Store) {
	t.Helper()
	publish := func(dryRun bool, target *memory.Store) error {
		root := t.TempDir()
		entityPath := filepath.Join(root, "plugin.yaml")
		writeEntityFixture(t, entityPath, entityYAML)
		cfg := pushConfig{
			Workspace:  resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
			EntityPath: entityPath,
			PluginPath: pluginPath,
			Registry:   "hub.aggrega.ai",
			DryRun:     dryRun,
			Out:        &bytes.Buffer{},
		}
		if target != nil {
			cfg.PushTarget = target
		}
		return pushRun(t.Context(), cfg)
	}
	store = memory.New()
	return publish(true, nil), publish(false, store), store
}

// requireNothingLanded fails the test when a halted publication left the version
// tag in the store it wrote to.
func requireNothingLanded(t *testing.T, store *memory.Store, version string) {
	t.Helper()
	if _, exists, err := oci.Resolve(context.Background(), store, version); err != nil || exists {
		t.Errorf("a refused publication must land nothing, but tag %q resolves (exists=%v err=%v)", version, exists, err)
	}
}

// requireLanded fails the test when an accepted publication did not land its
// version tag.
func requireLanded(t *testing.T, store *memory.Store, version string) {
	t.Helper()
	if _, exists, err := oci.Resolve(context.Background(), store, version); err != nil || !exists {
		t.Errorf("an accepted publication must land tag %q (exists=%v err=%v)", version, exists, err)
	}
}

// TestPushPluginReadsTheEntityAgainstItsSchema proves a Plugin entity is read
// against its kind's schema at publication, in a dry run and in a real
// publication alike, before its .wasm is judged: a metadata.version that is not a
// semantic version is refused with the schema's own condition naming the field,
// and so is a missing required field, which shows the whole schema runs rather
// than a version rule of its own. Each guest reports exactly the version its
// entity records, so the version cross-check has nothing to refuse and the schema
// is the only thing that can.
func TestPushPluginReadsTheEntityAgainstItsSchema(t *testing.T) {
	for _, tc := range []struct {
		name         string
		version      string
		withABI      bool
		refused      bool
		namesInError string
	}{
		{name: "a bare commit is not a semantic version", version: "415148e", withABI: true, refused: true, namesInError: "metadata.version"},
		// The schema is read before the build-marker refusal could run, so this form
		// meets the schema's condition, not the marker's.
		{name: "a bare commit carrying the build marker", version: "415148e-dirty", withABI: true, refused: true, namesInError: "metadata.version"},
		{name: "a required field is missing", version: "1.0.0", withABI: false, refused: true, namesInError: "spec.abiVersion"},
		{name: "a release version publishes", version: "1.0.0", withABI: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			guest := stepGuest(t, tc.version)
			dryErr, realErr, store := publishBothWays(t, versionedStepEntity(tc.version, tc.withABI), guest)
			for _, outcome := range []struct {
				label string
				err   error
			}{{"dry run", dryErr}, {"publication", realErr}} {
				if !tc.refused {
					if outcome.err != nil {
						t.Fatalf("%s: an entity its schema accepts must publish; got %v", outcome.label, outcome.err)
					}
					continue
				}
				if !errors.Is(outcome.err, envelope.ErrEntityEnvelope()) {
					t.Fatalf("%s: want the entity's schema condition, got %v", outcome.label, outcome.err)
				}
				if !strings.Contains(outcome.err.Error(), tc.namesInError) {
					t.Errorf("%s: the refusal should name %s; got %v", outcome.label, tc.namesInError, outcome.err)
				}
			}
			if tc.refused {
				requireNothingLanded(t, store, tc.version)
			} else {
				requireLanded(t, store, tc.version)
			}
		})
	}
}

// requireNamesTheTaggedBuild fails the test when a publication refusal over the
// version a plugin reports about its own build does not direct the author to a
// build of the commit the release's tag names — the repair every such refusal
// shares — or when it still directs writing the reported version into the entity,
// by hand or by regenerating the entity from the build.
func requireNamesTheTaggedBuild(t *testing.T, label string, err error) {
	t.Helper()
	msg := err.Error()
	for _, want := range []string{"the commit that release's tag names", "tag that commit"} {
		if !strings.Contains(msg, want) {
			t.Errorf("%s: the refusal should name the tagged build (%q); got %v", label, want, err)
		}
	}
	for _, never := range []string{"the version the rebuilt plugin reports", "the guest's build version", "gen-catalog"} {
		if strings.Contains(msg, never) {
			t.Errorf("%s: the refusal must not direct writing the plugin's version into the entity (%q); got %v", label, never, err)
		}
	}
}

// TestPushPluginVersionMismatchNamesTheTaggedBuild proves publication, dry run
// and real, refuses an entity whose metadata.version differs from the version its
// guest reports about its own build, in either direction, and that the refusal
// directs the author to a build of the commit the release's tag names rather than
// to writing the guest's version into the entity — which, for a guest built from
// any other commit, would publish a development build under it. The agreeing row
// is the table's positive control.
func TestPushPluginVersionMismatchNamesTheTaggedBuild(t *testing.T) {
	for _, tc := range []struct {
		name          string
		entityVersion string
		guestVersion  string
		refused       bool
	}{
		{name: "a development build under an entity recording the release", entityVersion: "1.0.0", guestVersion: "1.0.0-3-g1a2b3c4", refused: true},
		{name: "a release build under an entity recording a development version", entityVersion: "1.0.0-3-g1a2b3c4", guestVersion: "1.0.0", refused: true},
		{name: "a release build under an entity recording the release", entityVersion: "1.0.0", guestVersion: "1.0.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			guest := stepGuest(t, tc.guestVersion)
			dryErr, realErr, store := publishBothWays(t, versionedStepEntity(tc.entityVersion, true), guest)
			for _, outcome := range []struct {
				label string
				err   error
			}{{"dry run", dryErr}, {"publication", realErr}} {
				if !tc.refused {
					if outcome.err != nil {
						t.Fatalf("%s: an entity recording the version its guest reports must publish; got %v", outcome.label, outcome.err)
					}
					continue
				}
				if !errors.Is(outcome.err, wasm.ErrWasmPluginVersionMismatch()) {
					t.Fatalf("%s: want the version-mismatch condition, got %v", outcome.label, outcome.err)
				}
				requireNamesTheTaggedBuild(t, outcome.label, outcome.err)
			}
			if tc.refused {
				requireNothingLanded(t, store, tc.entityVersion)
			} else {
				requireLanded(t, store, tc.entityVersion)
			}
		})
	}
}

// TestPushPluginRefusesABuildFromUncommittedChanges proves publication, dry run
// and real, refuses a .wasm whose self-reported version carries the mark a build
// from a working tree with uncommitted changes is stamped with, naming the
// version and directing the author to a build of the commit the release's tag
// names — and that it refuses nothing else: the same guest built from a clean
// commit, a clean development version and a prerelease all publish.
//
// The row whose entity records the clean version while its guest is marked pins
// the order: the marker is judged ahead of the version equality, so the guest is
// refused as the build no commit holds rather than as a version disagreement.
func TestPushPluginRefusesABuildFromUncommittedChanges(t *testing.T) {
	for _, tc := range []struct {
		name          string
		entityVersion string
		guestVersion  string
		refused       bool
	}{
		{name: "at a tag with uncommitted changes", entityVersion: "1.0.0-dirty", guestVersion: "1.0.0-dirty", refused: true},
		{name: "past a tag with uncommitted changes", entityVersion: "0.7.3-19-gc7e21762-dirty", guestVersion: "0.7.3-19-gc7e21762-dirty", refused: true},
		{name: "a marked guest under an entity recording the clean version", entityVersion: "1.0.0", guestVersion: "1.0.0-dirty", refused: true},
		{name: "the same guest built from a clean commit", entityVersion: "1.0.0", guestVersion: "1.0.0"},
		{name: "a clean development version", entityVersion: "1.0.0-3-g1a2b3c4", guestVersion: "1.0.0-3-g1a2b3c4"},
		{name: "a prerelease", entityVersion: "1.0.0-rc.1", guestVersion: "1.0.0-rc.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			guest := stepGuest(t, tc.guestVersion)
			dryErr, realErr, store := publishBothWays(t, versionedStepEntity(tc.entityVersion, true), guest)
			for _, outcome := range []struct {
				label string
				err   error
			}{{"dry run", dryErr}, {"publication", realErr}} {
				if !tc.refused {
					if outcome.err != nil {
						t.Fatalf("%s: a guest reporting %q must publish; got %v", outcome.label, tc.guestVersion, outcome.err)
					}
					continue
				}
				if !errors.Is(outcome.err, wasm.ErrPluginBuiltFromUncommittedChanges()) {
					t.Fatalf("%s: want the uncommitted-changes condition, got %v", outcome.label, outcome.err)
				}
				if errors.Is(outcome.err, wasm.ErrWasmPluginVersionMismatch()) {
					t.Errorf("%s: the marker must be judged ahead of the version equality; got %v", outcome.label, outcome.err)
				}
				if !strings.Contains(outcome.err.Error(), tc.guestVersion) {
					t.Errorf("%s: the refusal should name the version %q; got %v", outcome.label, tc.guestVersion, outcome.err)
				}
				requireNamesTheTaggedBuild(t, outcome.label, outcome.err)
			}
			if tc.refused {
				requireNothingLanded(t, store, tc.entityVersion)
			} else {
				requireLanded(t, store, tc.entityVersion)
			}
		})
	}
}

// requirePublishedUnlessUncommitted is the outcome a publication of an embedded
// guest must have once every cross-check the calling test targets has passed.
//
// An embedded guest reports whatever version the tree it was built from gave it,
// so the same test meets a clean version in a clean checkout and a marked one in
// a working tree with uncommitted changes. The marker is judged after the
// capability, command and host-function cross-checks, so meeting its refusal is
// itself proof those passed; any other failure is the test's own. It reports
// whether the publication went through, so a caller asserting on its output does
// so only when there is output to assert on.
func requirePublishedUnlessUncommitted(t *testing.T, guestVersion string, err error) bool {
	t.Helper()
	if wasm.BuiltFromUncommittedChanges(guestVersion) {
		if !errors.Is(err, wasm.ErrPluginBuiltFromUncommittedChanges()) {
			t.Fatalf("the guest reports %q, a build from a working tree with uncommitted changes, so every cross-check "+
				"must pass and publication then refuse it as such; got %v", guestVersion, err)
		}
		return false
	}
	if err != nil {
		t.Fatalf("the guest reports the clean version %q, so publication must accept it; got %v", guestVersion, err)
	}
	return true
}
