// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

// The build tool reaches the halt over a cluster-scoped name exactly as the command
// line's build does: one boundary, two components of one chart placed in two
// namespaces, each writing one ClusterRole and the one CustomResourceDefinition
// under one name. The answer refuses with the condition the halt carries, naming
// each name in the key space of its kind alone.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestBuildToolRefusesOneClusterScopedNameFromTwoNamespaces calls the build tool the
// way an assistant calls it — the Release and the workspace named, the dependencies
// resolved first — over a copy of the end-to-end fixture whose configuration names
// its catalog directory and records the pins of its local chart sources at pull.
func TestBuildToolRefusesOneClusterScopedNameFromTwoNamespaces(t *testing.T) {
	t.Parallel()

	src := filepath.Join(moduleRoot(t), "internal", "compiler", "testdata", "helm-cluster-scoped-names")
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(src)); err != nil {
		t.Fatalf("copy the fixture workspace: %v", err)
	}
	config := "workspace:\n  catalog_dirs:\n    - catalog\npin_sources_at_pull: true\n"
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(config), 0o644); err != nil {
		t.Fatalf("write the workspace configuration: %v", err)
	}

	res := callTool(t, newTestServer(t), "build", buildArgs{
		Release:       filepath.Join(root, "release-two-namespaces.yaml"),
		WorkspaceRoot: root,
		AutoPull:      true,
	})
	text := res.Content[0].Text
	if !res.IsError {
		t.Fatalf("the build tool built two components writing one ClusterRole and one CustomResourceDefinition "+
			"into one output:\n%s", text)
	}
	entry := unmarshalBuildPayload(t, res).Refusal
	if entry == nil {
		t.Fatalf("the refusal carries no structured entry:\n%s", text)
	}
	code, _ := cerrors.CodeOf(renderer.ErrDerivedKeyCollision())
	shortCode, _ := cerrors.ShortCodeOf(renderer.ErrDerivedKeyCollision())
	if entry.Code != code || entry.ShortCode != shortCode {
		t.Errorf("the entry names %q / %q; the condition is %q / %q", entry.Code, entry.ShortCode, code, shortCode)
	}
	if entry.Severity != "halt" {
		t.Errorf("the entry's severity is %q; the finding that stopped a build is at the halting tier", entry.Severity)
	}
	for _, want := range []string{
		`"shared-reader"`, `key space "resource-names:ClusterRole"`,
		`"widgets.example.com"`, `key space "resource-names:CustomResourceDefinition"`,
		`derived by component "store"`, `derived by component "edge"`,
	} {
		if !strings.Contains(entry.Message, want) || !strings.Contains(text, want) {
			t.Errorf("the refusal does not carry %q in both its entry and its text:\nentry: %s\ntext:\n%s",
				want, entry.Message, text)
		}
	}
	if strings.Contains(entry.Message, `"resource-names:ClusterRole--`) {
		t.Errorf("the refusal names a ClusterRole space carrying a namespace:\n%s", entry.Message)
	}
}
