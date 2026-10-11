// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

// The build tool reaches the halt on an omits entry that left nothing out exactly
// as the command line's build does: the Recipe's entry, made active by the Release,
// names a controller its chart does not render, and the answer refuses with the
// condition the halt carries, naming the node, the document, the entry's position
// and what it resolved to.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestBuildToolRefusesAnOmitsEntryThatLeftNothingOut calls the build tool the way
// an assistant calls it — the Release and the workspace named, the dependencies
// resolved first — over a copy of the end-to-end omits fixture whose configuration
// names its catalog directory and records the pins of its local chart sources at
// pull.
func TestBuildToolRefusesAnOmitsEntryThatLeftNothingOut(t *testing.T) {
	t.Parallel()

	src := filepath.Join(moduleRoot(t), "internal", "compiler", "testdata", "helm-omits")
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(src)); err != nil {
		t.Fatalf("copy the fixture workspace: %v", err)
	}
	config := "workspace:\n  catalog_dirs:\n    - catalog\npin_sources_at_pull: true\n"
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(config), 0o644); err != nil {
		t.Fatalf("write the workspace configuration: %v", err)
	}

	res := callTool(t, newTestServer(t), "build", buildArgs{
		Release:       filepath.Join(root, "release-stale.yaml"),
		WorkspaceRoot: root,
		AutoPull:      true,
	})
	text := res.Content[0].Text
	if !res.IsError {
		t.Fatalf("the build tool built a Recipe whose active omits entry names nothing its chart renders:\n%s", text)
	}
	entry := unmarshalBuildPayload(t, res).Refusal
	if entry == nil {
		t.Fatalf("the refusal carries no structured entry:\n%s", text)
	}
	code, _ := cerrors.CodeOf(renderer.ErrOmitMatchedNothing())
	shortCode, _ := cerrors.ShortCodeOf(renderer.ErrOmitMatchedNothing())
	if entry.Code != code || entry.ShortCode != shortCode {
		t.Errorf("the entry names %q / %q; the condition is %q / %q", entry.Code, entry.ShortCode, code, shortCode)
	}
	if entry.Severity != "halt" {
		t.Errorf("the entry's severity is %q; the finding that stopped a build is at the halting tier", entry.Severity)
	}
	for _, want := range []string{
		renderer.ErrOmitMatchedNothing().Error(),
		`node "app"`, `"omits-app" omits[0]`, `kind "Deployment"`, `name "app-missing"`,
	} {
		if !strings.Contains(entry.Message, want) || !strings.Contains(text, want) {
			t.Errorf("the refusal does not carry %q in both its entry and its text:\nentry: %s\ntext:\n%s",
				want, entry.Message, text)
		}
	}
}
