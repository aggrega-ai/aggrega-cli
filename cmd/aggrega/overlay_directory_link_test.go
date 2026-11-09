// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestAnOverlayDirectoryTypedThroughALinkIsReadWhereItLeads drives `aggrega plugin
// gen-catalog --from-embedded` through the real command tree with the overlay
// directory typed three ways: as itself (the control), as an absolute link to it,
// and as a relative link to it from the directory the command runs in. A directory
// typed through a link is the directory it leads to, so each run generates one
// entity per embedded guest, each carrying the runtime limit its overlay authored —
// read beneath the link. Walked as the link alone, the directory holds no overlay
// and the command halts, asking for overlays that are there.
func TestAnOverlayDirectoryTypedThroughALinkIsReadWhereItLeads(t *testing.T) {
	uris := embedded.URIs()
	if len(uris) == 0 {
		t.Skip("no embedded guests in this build")
	}
	const pages = 4321
	for _, row := range []struct {
		name    string
		overlay func(base string) string
	}{
		{"control: the overlay directory typed as itself", func(base string) string { return filepath.Join(base, "overlays") }},
		{"the overlay directory typed as an absolute link to it", func(base string) string { return filepath.Join(base, "via") }},
		{"the overlay directory typed as a relative link to it", func(string) string { return "viarel" }},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			t.Setenv("XDG_DATA_HOME", t.TempDir())
			base := t.TempDir()
			ws := filepath.Join(base, "ws")
			writeFixture(t, filepath.Join(ws, workspaceconfig.ConfigFilename), "workspace: {}\noffline: true\n")
			for _, uri := range uris {
				leaf := uri[strings.LastIndex(uri, "/")+1:]
				writeFixture(t, filepath.Join(base, "overlays", leaf+".yaml"),
					"apiVersion: aggrega.ai/v1beta1\nkind: Plugin\nmetadata:\n  name: "+leaf+
						"\nspec:\n  runtime:\n    maxMemoryPages: 4321\n    timeoutMs: 300000\n")
			}
			if err := os.Symlink(filepath.Join(base, "overlays"), filepath.Join(base, "via")); err != nil {
				t.Skipf("symbolic links are not available here: %v", err)
			}
			if err := os.Symlink("overlays", filepath.Join(base, "viarel")); err != nil {
				t.Fatal(err)
			}
			t.Chdir(base)
			out := filepath.Join(base, "out")

			args := []string{"--workspace-root", ws, "plugin", "gen-catalog", "--from-embedded",
				"--overlay", row.overlay(base), "--output-dir", out}
			root := newRootCmd()
			var stdout, stderr bytes.Buffer
			root.SetOut(&stdout)
			root.SetErr(&stderr)
			root.SetIn(strings.NewReader(""))
			root.SetArgs(args)
			if code := executeRoot(context.Background(), root); code != 0 {
				t.Fatalf("`aggrega %s` exited %d; want 0 — the overlays beneath the directory were not read\nstdout:\n%s\nstderr:\n%s",
					strings.Join(args, " "), code, stdout.String(), stderr.String())
			}
			for _, uri := range uris {
				path := filepath.Join(out, filepath.FromSlash(uri), embeddedGuestVersion(t, uri), "plugin.yaml")
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Errorf("no entity generated for %s: %v", uri, err)
					continue
				}
				var entity types.Plugin
				if err := yaml.Unmarshal(raw, &entity); err != nil {
					t.Fatalf("parse %s: %v", path, err)
				}
				if entity.Spec.Runtime == nil || entity.Spec.Runtime.MaxMemoryPages != pages {
					t.Errorf("%s carries runtime %+v; want maxMemoryPages %d from its overlay", uri, entity.Spec.Runtime, pages)
				}
			}
		})
	}
}
