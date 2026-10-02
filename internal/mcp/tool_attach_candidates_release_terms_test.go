// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/serve"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// attachVariantFeature is a Feature document applying to every node labelled a
// stateful server, which the graph node the proofs below address is.
func attachVariantFeature(name string) string {
	return "apiVersion: aggrega.ai/v1alpha1\nkind: Feature\nmetadata:\n  name: " + name +
		"\n  version: \"1.0.0\"\nspec:\n  appliesTo: component.aggrega.ai/type=stateful-server\n"
}

// attachVariantToolWorkspace copies the end-to-end fixture into a fresh workspace,
// adds a second Release identical to its first, and gives each Release catalog
// directories of its own: release.yaml reads catalog/ and features-a/, and
// release-b.yaml reads catalog/ and features-b/. features-a/ declares
// example/variant-tls and example/only-a, features-b/ its own example/variant-tls,
// and beside-feature.yaml declares example/beside under no catalog directory. It
// returns the root.
func attachVariantToolWorkspace(t *testing.T) string {
	t.Helper()
	src, _, releasePath := fullstackWorkspace(t)
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(src)); err != nil {
		t.Fatalf("copy the fixture workspace: %v", err)
	}
	release, err := os.ReadFile(releasePath)
	if err != nil {
		t.Fatalf("read the fixture's Release: %v", err)
	}
	config := "workspace:\n  catalog_dirs:\n    - catalog\n  releases:\n" +
		"    release.yaml:\n      catalog_dirs:\n        - catalog\n        - features-a\n" +
		"    release-b.yaml:\n      catalog_dirs:\n        - catalog\n        - features-b\n"
	for rel, body := range map[string]string{
		"release-b.yaml":               string(release),
		"features-a/tls.yaml":          attachVariantFeature("example/variant-tls"),
		"features-a/only-a.yaml":       attachVariantFeature("example/only-a"),
		"features-b/tls.yaml":          attachVariantFeature("example/variant-tls"),
		"beside-feature.yaml":          attachVariantFeature("example/beside"),
		workspaceconfig.ConfigFilename: config,
	} {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return root
}

// TestAttachCandidatesForAGraphNodeOffersWhatItsReleaseReaches is the tool
// protocol's half of the proof that what may attach to a node on a Release's graph
// is drawn in that Release's terms — the computation after the graph is the one the
// daemon runs, so the two surfaces answer one set.
//
// Every row names the Features it must offer and the ones it must not, by name and
// by the file declaring them: each Release offers its own example/variant-tls and
// not the other's, example/only-a only for the Release whose directory declares it,
// and example/beside — under no catalog directory — for every Release. The supplied
// label set names no Release and offers every variant, as it always did.
//
// Planted defect: draw the workspace documents without the Release's reach and
// release-b.yaml's row offers features-a/'s two Features.
func TestAttachCandidatesForAGraphNodeOffersWhatItsReleaseReaches(t *testing.T) {
	t.Parallel()
	root := attachVariantToolWorkspace(t)
	s := &server{out: io.Discard, logw: io.Discard, serverVersion: "test", startDir: root}

	cases := []struct {
		name          string
		args          map[string]any
		offered, kept []string
	}{
		{
			name: "the node on release.yaml's graph",
			args: map[string]any{"graph_node": "data.main-db", "release": "release.yaml"},
			offered: []string{"example/beside [beside-feature.yaml]", "example/only-a [features-a/only-a.yaml]",
				"example/variant-tls [features-a/tls.yaml]"},
			kept: []string{"example/variant-tls [features-b/tls.yaml]"},
		},
		{
			name:    "the node on release-b.yaml's graph",
			args:    map[string]any{"graph_node": "data.main-db", "release": "release-b.yaml"},
			offered: []string{"example/beside [beside-feature.yaml]", "example/variant-tls [features-b/tls.yaml]"},
			kept:    []string{"example/only-a [features-a/only-a.yaml]", "example/variant-tls [features-a/tls.yaml]"},
		},
		{
			name: "the same labels supplied, naming no Release",
			args: map[string]any{"labels": map[string]string{"component.aggrega.ai/type": "stateful-server"}},
			offered: []string{"example/beside [beside-feature.yaml]", "example/only-a [features-a/only-a.yaml]",
				"example/variant-tls [features-a/tls.yaml]", "example/variant-tls [features-b/tls.yaml]"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := map[string]any{"workspace_root": root}
			for k, v := range tc.args {
				args[k] = v
			}
			var got attachCandidatesPayload
			decodeStructured(t, callDiscoveryTool(t, s, "attach-candidates", args), &got)
			lines := candidateFiles(got.Candidates)
			for _, want := range tc.offered {
				if !slices.Contains(lines, want) {
					t.Errorf("%s is not offered; candidates: %q", want, lines)
				}
			}
			for _, unwanted := range tc.kept {
				if slices.Contains(lines, unwanted) {
					t.Errorf("%s is offered, though the Release named does not reach it; candidates: %q", unwanted, lines)
				}
			}
		})
	}
}

// candidateFiles renders a candidate list as "name [file]" lines.
func candidateFiles(cands []serve.AttachCandidate) []string {
	out := make([]string, 0, len(cands))
	for _, c := range cands {
		out = append(out, c.Name+" ["+c.File+"]")
	}
	return out
}
