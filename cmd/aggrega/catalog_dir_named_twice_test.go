// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// namedTwiceComponent is the one Component the fixture's catalog variant carries,
// with marker as its image default so two files declaring the identity differ.
func namedTwiceComponent(marker string) string {
	return `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values:
      image: { type: string, default: "` + marker + `" }
`
}

// namedTwiceWorkspace lays down a workspace whose Release builds against the
// catalog directory `variant` — a Component, its compose Recipe, and the compose
// provider — and a second directory, `other`, holding a different file that
// declares the same Component identity. Nothing sits at the conventional `catalog`
// directory, so the stack holds only what each row names. config is the
// workspace configuration file's content.
func namedTwiceWorkspace(t *testing.T, root, config string) {
	t.Helper()
	writeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename), config)
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: named-twice
  version: "1.0.0"
spec:
  components:
    only:
      type: core/server
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: named-twice-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/server: server-compose-recipe:1.0.0
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: named-twice-release
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        only:
          infra:
            replicas: 1
`)
	variant := filepath.Join(root, "variant")
	writeFixture(t, filepath.Join(variant, "components", "server.yaml"), namedTwiceComponent("from-variant"))
	seedEmbeddedPlugins(t, filepath.Join(variant, "providers"), "provider/compose")
	writeFixture(t, filepath.Join(variant, "recipes", "compose", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server-compose-recipe
  version: "1.0.0"
spec:
  capability: compose
  implements: core/server
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    containers:
      server:
        image: {{ .values.image }}
        ports:
          web: 8080
    services:
      svc:
        type: ClusterIP
        ports:
          web: 8080
`)
	writeFixture(t, filepath.Join(root, "other", "components", "server.yaml"), namedTwiceComponent("from-other"))
}

// TestACatalogDirectoryNamedTwiceIsReadOnce drives `aggrega pull` through the real
// command tree for a Release whose catalog directory is named more than once, and
// reads both streams for the notice that a file is shadowed.
//
// A directory named twice is one directory: a flag naming one the configuration
// already lists, a configuration listing one twice, a flag spelling it another way
// or through a link — each is read once, so no file in it is reported as shadowed
// by itself. The last row is the control the others need: two DIFFERENT files
// declaring one identity are an override the reader asked for, and it is still
// announced, once, naming the file that lost. Without it, a pull that had stopped
// announcing shadows altogether would pass every other row.
func TestACatalogDirectoryNamedTwiceIsReadOnce(t *testing.T) {
	// Not parallel: the rows move the process working directory, which a relative
	// --catalog is resolved against.
	const workspaceList = "workspace:\n  catalog_dirs:\n    - variant\n"
	for _, tc := range []struct {
		name   string
		config string
		// catalog is the --catalog value, empty for none; "@link" names a link to
		// the variant directory the row creates.
		catalog string
		// shadowed is the workspace-relative file the one shadow notice must name,
		// empty where no notice may be printed at all.
		shadowed string
	}{
		{name: "a flag naming the directory the workspace configuration lists",
			config: workspaceList, catalog: "variant"},
		{name: "a flag spelling that directory another way",
			config: workspaceList, catalog: "./variant/"},
		{name: "a flag naming the directory the Release's own entry lists",
			config: "workspace:\n  releases:\n    release.yaml:\n      catalog_dirs:\n        - variant\n", catalog: "variant"},
		{name: "a configuration listing the directory twice",
			config: "workspace:\n  catalog_dirs:\n    - variant\n    - ./variant\n"},
		{name: "a flag naming the directory through a link",
			config: workspaceList, catalog: "@link"},
		{name: "control: a different file declaring the same identity is still shadowed",
			config: workspaceList, catalog: "other", shadowed: filepath.Join("variant", "components", "server.yaml")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			namedTwiceWorkspace(t, root, tc.config)
			t.Chdir(root)

			args := []string{"--workspace-root", root, "pull", "--release", "release.yaml"}
			switch tc.catalog {
			case "":
			case "@link":
				link := filepath.Join(root, "variant-link")
				if err := os.Symlink(filepath.Join(root, "variant"), link); err != nil {
					t.Skipf("this file system cannot hold a symbolic link: %v", err)
				}
				args = append(args, "--catalog", link)
			default:
				args = append(args, "--catalog", tc.catalog)
			}

			var out string
			var err error
			stderr := captureStderr(t, func() { out, err = runAggrega(t, args...) })
			if err != nil {
				t.Fatalf("pull: %v\noutput:\n%s\nstderr:\n%s", err, out, stderr)
			}
			if _, statErr := os.Stat(filepath.Join(root, "release.lock")); statErr != nil {
				t.Fatalf("the pull wrote no lockfile, so its silence proves nothing: %v\noutput:\n%s\nstderr:\n%s", statErr, out, stderr)
			}

			var notices []string
			for _, line := range strings.Split(out+"\n"+stderr, "\n") {
				if strings.Contains(line, "shadowed") {
					notices = append(notices, line)
				}
			}
			if tc.shadowed == "" {
				if len(notices) != 0 {
					t.Errorf("a directory named twice was read twice; the pull printed:\n%s", strings.Join(notices, "\n"))
				}
				return
			}
			if len(notices) != 1 || !strings.Contains(notices[0], string(filepath.Separator)+tc.shadowed) {
				t.Errorf("want exactly one shadow notice naming %s; the pull printed:\n%s\nstderr:\n%s",
					tc.shadowed, strings.Join(notices, "\n"), stderr)
			}
		})
	}
}
