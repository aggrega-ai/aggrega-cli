// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build integration

package main

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// TestIntegration_APluginNamedByItsCoordinateAndItsExecutableKeepTheirOwnPins drives
// a pull, a re-pull and a frozen build of a Release whose FeatureRecipe runs a
// pipeline step published to the in-process registry and names the Plugin by its
// bare coordinate — the same reference the lockfile's plugin section pins the
// Plugin's executable under.
//
// The lockfile records two rows under that one reference: the Plugin entity, pinned
// at the digest of its document, and its executable, pinned at the digest of its
// bytes. The re-pull reads both back as its prior pins, and each must hold its own
// row to its own digest: the entity's pin addresses the entity's slot by its
// version, and the executable's addresses the executable's slot by its content
// digest. Were the two pins one, the entity would be held to the executable's pin
// — read from the executable's slot, or refused against the wrong digest — and the
// re-pull would refuse or record the entity row at another digest.
func TestIntegration_APluginNamedByItsCoordinateAndItsExecutableKeepTheirOwnPins(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")
	std := standardCatalogDir(t)
	f := writeRegistryPluginFixtureNaming(t, startRegistry(t, nil), pluginPath, std, true, true)
	t.Chdir(f.root)
	if f.entityURI != f.artifactURI {
		t.Fatalf("fixture: the step must name the Plugin by the reference its executable is pinned under (%q), got %q",
			f.artifactURI, f.entityURI)
	}

	rows := func(label string, lf *lockfile.Lockfile) (entity, executable string) {
		t.Helper()
		for _, e := range lf.Entities {
			if e.URI == f.entityURI && e.Kind == closure.KindPlugin {
				entity = e.Digest
			}
		}
		for _, p := range lf.Plugins {
			if p.URI == f.artifactURI {
				executable = p.Digest
			}
		}
		if entity == "" || executable == "" {
			t.Fatalf("%s: the lockfile must pin the Plugin entity and its executable under %q (entity %q, executable %q); "+
				"entities=%+v plugins=%+v", label, f.entityURI, entity, executable, lf.Entities, lf.Plugins)
		}
		return entity, executable
	}

	cold := f.pull(t)
	f.assertPluginPinned(t, cold)
	entityDigest, executableDigest := rows("the cold pull", cold)
	if entityDigest == executableDigest {
		t.Fatalf("fixture: the entity's document and the executable digest alike (%s); the two pins cannot be told apart", entityDigest)
	}

	warm := f.pull(t)
	gotEntity, gotExecutable := rows("the re-pull", warm)
	if gotEntity != entityDigest {
		t.Errorf("the re-pull recorded the Plugin entity at %s; the cold pull recorded %s, and the entity did not change", gotEntity, entityDigest)
	}
	if gotExecutable != executableDigest || gotExecutable != f.wasmDigest {
		t.Errorf("the re-pull recorded the executable at %s; the cold pull recorded %s and its bytes digest to %s",
			gotExecutable, executableDigest, f.wasmDigest)
	}

	f.build(t, "the frozen build over the re-pulled lockfile")
}
