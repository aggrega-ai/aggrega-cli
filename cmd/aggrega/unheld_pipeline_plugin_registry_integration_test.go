// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build integration

package main

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestIntegration_APullRefusesAPipelinePluginNoRegistryServes drives the pull and
// the build over a workspace whose FeatureRecipe pipeline step names a Plugin that
// only a registry could serve — no catalog directory holds it and the binary does
// not carry it — against a real in-process registry, once with the Plugin
// unpublished and once with it published.
//
// Unpublished, the pull asks the registry for the Plugin's manifest, is told there
// is none, and refuses with the plugin-entity-not-found condition and the sentence
// the build's own load raises, writing neither a lockfile nor a trust record.
// Published, the same pull pins the Plugin's entity and its .wasm and the build
// renders the FeatureRecipe whose pipeline runs the step: the control, which a pull
// refusing every registry Plugin would fail. Each row asserts the registry was asked
// for the manifest, so neither passes without having reached it.
func TestIntegration_APullRefusesAPipelinePluginNoRegistryServes(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")
	const pluginName = "gateway-step"
	artifactURI := types.PluginCoordinate(pipelineStepPluginType, pluginName)
	manifestPath := "/v2/plugin/" + artifactURI + "/manifests/1.0.0"
	if artifactURI+":1.0.0" != unheldPluginEntityURI {
		t.Fatalf("fixture: the published Plugin %s:1.0.0 is not the one the FeatureRecipe names, %s", artifactURI, unheldPluginEntityURI)
	}

	for _, tc := range []struct {
		name    string
		publish bool
	}{
		{name: "the registry does not publish the Plugin"},
		{name: "the registry publishes the Plugin", publish: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var asked atomic.Int32
			host := startRegistry(t, func(h http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == manifestPath {
						asked.Add(1)
					}
					h.ServeHTTP(w, r)
				})
			})
			root, releasePath, _ := unheldPluginWorkspace(t, nil)
			writeWorkspaceConfig(t, root, "workspace:\n  catalog_dirs:\n    - ./catalog\ndefault_registry: \""+host+"\"\noffline: false\nauto_pull: false")
			var wasmDigest string
			if tc.publish {
				wasmDigest = publishPluginEntity(t, host, pluginName, "1.0.0", pluginPath)
			}
			before := asked.Load()

			pullErr := unheldPluginPull(t, root, releasePath)
			if asked.Load() == before {
				t.Errorf("the pull never asked the registry for %s, so this row says nothing about a registry", manifestPath)
			}

			if !tc.publish {
				assertRefusesTheUnheldPlugin(t, "pull", pullErr)
				if printed := formatCompilerError(pullErr); !strings.Contains(printed, unheldPluginNamedBy) {
					t.Errorf("the pull refusal does not name the FeatureRecipe %s whose pipeline names the Plugin:\n%s", unheldPluginNamedBy, printed)
				}
				if _, statErr := os.Stat(lockfile.PathFor(releasePath)); !errors.Is(statErr, fs.ErrNotExist) {
					t.Errorf("the refused pull wrote a lockfile (stat: %v); a refusal writes nothing", statErr)
				}
				if _, statErr := os.Stat(pluginstrust.PathFor(releasePath)); !errors.Is(statErr, fs.ErrNotExist) {
					t.Errorf("the refused pull wrote a trust record (stat: %v); a refusal writes nothing", statErr)
				}
				return
			}

			if pullErr != nil {
				t.Fatalf("control: the pull refused a Plugin the registry publishes:\n%s", formatCompilerError(pullErr))
			}
			lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
			if err != nil {
				t.Fatalf("control: load the lockfile the pull wrote: %v", err)
			}
			var entityPinned, artifactPinned bool
			for _, e := range lf.Entities {
				if e.URI == unheldPluginEntityURI {
					entityPinned = true
				}
			}
			for _, p := range lf.Plugins {
				if p.URI == artifactURI && p.Digest == wasmDigest {
					artifactPinned = true
				}
			}
			if !entityPinned || !artifactPinned {
				t.Errorf("control: the pull must pin the published Plugin on both axes (entity %s: %v; artifact %s at %s: %v); entities=%+v plugins=%+v",
					unheldPluginEntityURI, entityPinned, artifactURI, wasmDigest, artifactPinned, lf.Entities, lf.Plugins)
			}
			if err := unheldPluginBuild(t, root, releasePath); err != nil {
				t.Fatalf("control: the build refused what its pull resolved:\n%s", formatCompilerError(err))
			}
			if rendered := readDistTree(t, filepath.Join(root, "dist")); !strings.Contains(rendered, "control-config") {
				t.Errorf("control: the build did not render the FeatureRecipe whose pipeline runs the step; got:\n%s", rendered)
			}
		})
	}
}
