// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// This test reads catalog entities from the aggrega-hub repository checked out
// beside this one, as the compose-bridge end-to-end test does.

package compiler_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// bridgeLayoutEdit rewrites one file of the compose-bridge fixture, named by its
// path relative to the fixture root; a file the fixture does not hold is created
// from the empty string.
type bridgeLayoutEdit struct {
	path string
	edit func(string) string
}

// replaceOnce returns an edit replacing the one occurrence of old with new, and
// fails the test where the fixture does not carry old exactly once, so an edit
// that no longer applies is reported rather than building the fixture unedited.
func replaceOnce(t *testing.T, old, new string) func(string) string {
	return func(src string) string {
		t.Helper()
		if n := strings.Count(src, old); n != 1 {
			t.Fatalf("the fixture carries %q %d time(s), want 1", old, n)
		}
		return strings.Replace(src, old, new, 1)
	}
}

// buildComposeBridgeLayout copies the compose-bridge fixture into a temporary
// tree, applies the edits, and builds it through the real pipeline with the
// shipped bridge Feature and FeatureRecipe. It returns the build's error and,
// where the build completed, every compose file it wrote, parsed and keyed by
// path.
func buildComposeBridgeLayout(t *testing.T, edits ...bridgeLayoutEdit) (map[string]composeFileShape, error) {
	t.Helper()
	root := t.TempDir()
	copyDirTree(t, "testdata/compose-bridge", root)
	for _, e := range edits {
		path := filepath.Join(root, e.path)
		src, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			t.Fatalf("read %s: %v", e.path, err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create the directory of %s: %v", e.path, err)
		}
		if err := os.WriteFile(path, []byte(e.edit(string(src))), 0o644); err != nil {
			t.Fatalf("write %s: %v", e.path, err)
		}
	}
	result, err := pipelinetest.BuildE(t, pipelinetest.Input{
		ReleasePath: filepath.Join(root, "release.yaml"),
		CatalogPaths: []string{
			filepath.Join(root, "catalog", "base"),
			"../../../aggrega-hub/aggrega-catalog/feature/core/compose-bridge/1.0.0/feature.yaml",
			"../../../aggrega-hub/aggrega-catalog/feature-recipe/compose/bridge",
			"../../../aggrega-hub/aggrega-catalog/schemalibrary/base-components/1.0.0/library.yaml",
		},
	})
	if err != nil {
		return nil, err
	}
	files := map[string]composeFileShape{}
	for path, body := range result.Artifacts {
		if !strings.HasSuffix(path, "docker-compose.yaml") {
			continue
		}
		var shape composeFileShape
		if err := yaml.Unmarshal(body, &shape); err != nil {
			t.Fatalf("parse %s: %v\n%s", path, err, body)
		}
		files[path] = shape
	}
	return files, nil
}

// apiEnvironment returns the environment the api service carries in the one
// compose file holding it.
func apiEnvironment(t *testing.T, files map[string]composeFileShape) map[string]string {
	t.Helper()
	for _, shape := range files {
		if svc, ok := shape.Services["api"]; ok {
			return svc.Environment
		}
	}
	t.Fatalf("no compose file holds the api service; files: %v", sortedComposePaths(files))
	return nil
}

func sortedComposePaths(files map[string]composeFileShape) []string {
	out := make([]string, 0, len(files))
	for p := range files {
		out = append(out, p)
	}
	return out
}

// probeEchoFeature and probeEchoFeatureRecipe are a Feature and a boundary-scope
// compose FeatureRecipe that publish one value and deliver nothing: a boundary
// attachment that is not the bridge.
const probeEchoFeature = `apiVersion: aggrega.ai/v1beta1
kind: Feature
metadata:
  name: probe/echo
  version: 1.0.0
  description: A boundary attachment that delivers nothing.
spec:
  schema:
    values:
      $ref: "BaseComponents#/schemas/BaseFeatureParameter"
`

const probeEchoFeatureRecipe = `apiVersion: aggrega.ai/v1beta1
kind: FeatureRecipe
metadata:
  name: probe-echo-fr
  version: 1.0.0
  description: A boundary-scope compose FeatureRecipe that delivers nothing.
spec:
  capability: compose
  implements: probe/echo:1.0.0
  executionScope: boundary
  provides:
    note: "{{ .boundary.path }}"
`

// TestComposeBridge_Layouts drives the shipped bridge FeatureRecipe through the
// real pipeline over the layouts its templates decide between, and reads each
// consumer's delivered environment and the gateway's configuration.
//
//   - Two bridged edges to targets on one port, each connection delivering an
//     address variable and a port variable: the address variables become
//     "aggrega-gw" and the port variables the listen port, the second edge's
//     reassigned from portBase — and from the portBase the Release sets.
//   - Both ends of the edges assembled into a file that is not the gateway's own,
//     while the gateway's own file holds a component: nothing is rewritten and
//     the gateway is idle, because it relays only inside its own file.
//   - A boundary-scope compose attachment that is not the bridge, in the bridge's
//     place: nothing is rewritten. Beside the bridge: the build completes and the
//     bridge's rewrites stand.
func TestComposeBridge_Layouts(t *testing.T) {
	t.Parallel()

	hostAndPort := func(t *testing.T) bridgeLayoutEdit {
		return bridgeLayoutEdit{"aggregate.yaml", func(src string) string {
			src = replaceOnce(t, "                - in: DB_HOST\n                  from: host\n",
				"                - in: DB_HOST\n                  from: host\n                - in: DB_PORT\n                  from: port\n")(src)
			return replaceOnce(t, "                - in: CACHE_HOST\n                  from: host\n",
				"                - in: CACHE_HOST\n                  from: host\n                - in: CACHE_PORT\n                  from: port\n")(src)
		}}
	}
	echoInPlaceOfBridge := func(t *testing.T) []bridgeLayoutEdit {
		return []bridgeLayoutEdit{
			{"catalog/base/features/echo.yaml", func(string) string { return probeEchoFeature }},
			{"catalog/base/feature-recipes/echo.yaml", func(string) string { return probeEchoFeatureRecipe }},
			{"release.yaml", replaceOnce(t, "          type: core/compose-bridge:1.0.0\n", "          type: probe/echo:1.0.0\n")},
			{"manifest.yaml", replaceOnce(t, "        core/compose-bridge:1.0.0: compose-bridge-fr:1.0.0\n",
				"        probe/echo:1.0.0: probe-echo-fr:1.0.0\n")},
		}
	}
	echoBesideBridge := func(t *testing.T) []bridgeLayoutEdit {
		return []bridgeLayoutEdit{
			{"catalog/base/features/echo.yaml", func(string) string { return probeEchoFeature }},
			{"catalog/base/feature-recipes/echo.yaml", func(string) string { return probeEchoFeatureRecipe }},
			{"release.yaml", replaceOnce(t, "          type: core/compose-bridge:1.0.0\n",
				"          type: core/compose-bridge:1.0.0\n        echo:\n          type: probe/echo:1.0.0\n")},
			{"manifest.yaml", replaceOnce(t, "        core/compose-bridge:1.0.0: compose-bridge-fr:1.0.0\n",
				"        core/compose-bridge:1.0.0: compose-bridge-fr:1.0.0\n        probe/echo:1.0.0: probe-echo-fr:1.0.0\n")},
		}
	}

	for _, tc := range []struct {
		name  string
		edits func(t *testing.T) []bridgeLayoutEdit
		// wantEnv is the api service's environment.
		wantEnv map[string]string
		// wantListen is every listen port the gateway's configuration carries, in
		// order; empty where the gateway is idle and carries no configuration.
		wantListen []string
	}{
		{
			name:       "host and port variables, two edges colliding on one port",
			edits:      func(t *testing.T) []bridgeLayoutEdit { return []bridgeLayoutEdit{hostAndPort(t)} },
			wantEnv:    map[string]string{"CACHE_HOST": "aggrega-gw", "CACHE_PORT": "5432", "DB_HOST": "aggrega-gw", "DB_PORT": "10000"},
			wantListen: []string{"listen 5432;", "listen 10000;"},
		},
		{
			name: "the Release's portBase moves the reassigned port",
			edits: func(t *testing.T) []bridgeLayoutEdit {
				return []bridgeLayoutEdit{hostAndPort(t), {"release.yaml", replaceOnce(t,
					"          type: core/compose-bridge:1.0.0\n",
					"          type: core/compose-bridge:1.0.0\n          infra:\n            portBase: 12000\n")}}
			},
			wantEnv:    map[string]string{"CACHE_HOST": "aggrega-gw", "CACHE_PORT": "5432", "DB_HOST": "aggrega-gw", "DB_PORT": "12000"},
			wantListen: []string{"listen 5432;", "listen 12000;"},
		},
		{
			name: "both ends in a file that is not the gateway's own",
			edits: func(t *testing.T) []bridgeLayoutEdit {
				return []bridgeLayoutEdit{{"release.yaml", func(src string) string {
					for _, child := range []string{"services-net", "data-net"} {
						anchor := "        " + child + ":\n          directory: \"{{ .parent_boundary.directory }}\"\n"
						src = replaceOnce(t, anchor, anchor+"          subDirectory: \"elsewhere\"\n")(src)
					}
					// cache moves under the bridge boundary itself, so the gateway's own
					// file holds a component.
					src = replaceOnce(t, "            db: {}\n            cache: {}\n", "            db: {}\n")(src)
					return replaceOnce(t, "      boundaries:\n        services-net:\n",
						"      components:\n        cache: {}\n      boundaries:\n        services-net:\n")(src)
				}}}
			},
			wantEnv: map[string]string{"CACHE_HOST": "cache", "DB_HOST": "db"},
		},
		{
			name:    "a boundary attachment that is not the bridge, in its place",
			edits:   echoInPlaceOfBridge,
			wantEnv: map[string]string{"CACHE_HOST": "cache", "DB_HOST": "db"},
		},
		{
			name:       "a boundary attachment that is not the bridge, beside it",
			edits:      echoBesideBridge,
			wantEnv:    map[string]string{"CACHE_HOST": "aggrega-gw:5432", "DB_HOST": "aggrega-gw:10000"},
			wantListen: []string{"listen 5432;", "listen 10000;"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			files, err := buildComposeBridgeLayout(t, tc.edits(t)...)
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			env := apiEnvironment(t, files)
			for k, want := range tc.wantEnv {
				if got := env[k]; got != want {
					t.Errorf("api %s = %q, want %q (environment %v)", k, got, want, env)
				}
			}
			const gatewayFile = "dist/cluster/compose/docker-compose.yaml"
			gateway, ok := files[gatewayFile]
			if !ok {
				t.Fatalf("no compose file at %s; files: %v", gatewayFile, sortedComposePaths(files))
			}
			config, hasConfig := gateway.Configs["aggrega-gw-nginx"]
			if len(tc.wantListen) == 0 {
				if hasConfig {
					t.Errorf("the gateway carries a configuration where nothing is bridged: %v", config)
				}
				return
			}
			body, _ := config.(map[string]any)
			content, _ := body["content"].(string)
			var listens []string
			for _, line := range strings.Split(content, "\n") {
				if line = strings.TrimSpace(line); strings.HasPrefix(line, "listen ") {
					listens = append(listens, line)
				}
			}
			if strings.Join(listens, " ") != strings.Join(tc.wantListen, " ") {
				t.Errorf("gateway listens on %v, want %v\n%s", listens, tc.wantListen, content)
			}
		})
	}
}

// TestComposeBridge_ASecondMappingOnTheRewrittenKeyHalts attaches, beside the
// bridge, a boundary attachment whose injectMapping writes the variable the
// bridge rewrites. The bridge's rewrite is an injectMapping delivery like any
// other, so the two meet on one key and the build halts naming both.
func TestComposeBridge_ASecondMappingOnTheRewrittenKeyHalts(t *testing.T) {
	t.Parallel()
	writer := strings.Replace(probeEchoFeatureRecipe, "  provides:\n    note: \"{{ .boundary.path }}\"\n",
		"  injectMapping: |\n    env: {DB_HOST: \"written-by-writer\"}\n", 1)
	_, err := buildComposeBridgeLayout(t,
		bridgeLayoutEdit{"catalog/base/features/echo.yaml", func(string) string { return probeEchoFeature }},
		bridgeLayoutEdit{"catalog/base/feature-recipes/echo.yaml", func(string) string { return writer }},
		bridgeLayoutEdit{"release.yaml", replaceOnce(t, "          type: core/compose-bridge:1.0.0\n",
			"          type: core/compose-bridge:1.0.0\n        writer:\n          type: probe/echo:1.0.0\n")},
		bridgeLayoutEdit{"manifest.yaml", replaceOnce(t, "        core/compose-bridge:1.0.0: compose-bridge-fr:1.0.0\n",
			"        core/compose-bridge:1.0.0: compose-bridge-fr:1.0.0\n        probe/echo:1.0.0: probe-echo-fr:1.0.0\n")},
	)
	if err == nil {
		t.Fatal("the build completed; want the halt on a key two attachments' mappings write")
	}
	if !errors.Is(err, renderer.ErrAttachmentInjectMappingKeyCollision()) {
		t.Fatalf("got %v, want the injectMapping key collision", err)
	}
	findings := pipelinetest.Findings(err, renderer.ErrAttachmentInjectMappingKeyCollision())
	if len(findings) == 0 {
		t.Fatalf("no finding carries the injectMapping key collision: %v", err)
	}
	text := findings[0].Error()
	for _, want := range []string{`"DB_HOST"`, `"bridge"`, `"writer"`} {
		if !strings.Contains(text, want) {
			t.Errorf("the finding does not name %s:\n%s", want, text)
		}
	}
}
