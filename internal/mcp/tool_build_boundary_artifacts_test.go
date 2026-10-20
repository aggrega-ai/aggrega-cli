// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

// The build tool reaches the refusal of a Recipe declaring files for its
// boundary's shared output on a provider that does not accept them exactly as the
// command line's build does: the compose provider declares no such claim, and the
// answer refuses with the condition the refusal carries, naming the provider and
// the node.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestBuildToolRefusesBoundaryArtifactsOnAProviderThatDoesNotAcceptThem calls the
// build tool the way an assistant calls it over a copy of the compose fixture
// whose Recipe declares a boundary block.
func TestBuildToolRefusesBoundaryArtifactsOnAProviderThatDoesNotAcceptThem(t *testing.T) {
	t.Parallel()

	src := filepath.Join(moduleRoot(t), "internal", "compiler", "testdata", "compose-boundary-artifacts")
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(src)); err != nil {
		t.Fatalf("copy the fixture workspace: %v", err)
	}
	config := "workspace:\n  catalog_dirs:\n    - catalog\n"
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(config), 0o644); err != nil {
		t.Fatalf("write the workspace configuration: %v", err)
	}

	res := callTool(t, newTestServer(t), "build", buildArgs{
		Release:       filepath.Join(root, "release.yaml"),
		WorkspaceRoot: root,
		AutoPull:      true,
	})
	text := res.Content[0].Text
	if !res.IsError {
		t.Fatalf("the build tool built a compose Recipe declaring files for its boundary's shared output:\n%s", text)
	}
	entry := unmarshalBuildPayload(t, res).Refusal
	if entry == nil {
		t.Fatalf("the refusal carries no structured entry:\n%s", text)
	}
	code, _ := cerrors.CodeOf(renderer.ErrUnsupportedBoundaryArtifacts())
	shortCode, _ := cerrors.ShortCodeOf(renderer.ErrUnsupportedBoundaryArtifacts())
	if entry.Code != code || entry.ShortCode != shortCode {
		t.Errorf("the entry names %q / %q; the condition is %q / %q", entry.Code, entry.ShortCode, code, shortCode)
	}
	if entry.Severity != "halt" {
		t.Errorf("the entry's severity is %q; the finding that stopped a build is at the halting tier", entry.Severity)
	}
	for _, want := range []string{
		renderer.ErrUnsupportedBoundaryArtifacts().Error() + ` ("compose"`, `node "app"`,
	} {
		if !strings.Contains(entry.Message, want) || !strings.Contains(text, want) {
			t.Errorf("the refusal does not carry %q in both its entry and its text:\nentry: %s\ntext:\n%s",
				want, entry.Message, text)
		}
	}
}
