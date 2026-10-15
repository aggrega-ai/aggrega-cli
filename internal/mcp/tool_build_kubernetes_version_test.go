// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

// The build tool reaches the halt over a Kubernetes version the chart cannot be
// rendered for exactly as the command line's build does: a boundary names
// aggrega.ai/kubernetes-version "one.two", which the version parser refuses. The
// answer refuses with the condition the halt carries, naming the value, the node
// and its boundary.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestBuildToolRefusesAKubernetesVersionTheParserRefuses calls the build tool the
// way an assistant calls it — the Release and the workspace named, the
// dependencies resolved first — over a copy of the end-to-end fixture whose
// configuration names its catalog directory and records the pins of its local
// chart source at pull.
func TestBuildToolRefusesAKubernetesVersionTheParserRefuses(t *testing.T) {
	t.Parallel()

	src := filepath.Join(moduleRoot(t), "internal", "compiler", "testdata", "helm-kubernetes-version")
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(src)); err != nil {
		t.Fatalf("copy the fixture workspace: %v", err)
	}
	config := "workspace:\n  catalog_dirs:\n    - catalog\npin_sources_at_pull: true\n"
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(config), 0o644); err != nil {
		t.Fatalf("write the workspace configuration: %v", err)
	}

	res := callTool(t, newTestServer(t), "build", buildArgs{
		Release:       filepath.Join(root, "release-malformed.yaml"),
		WorkspaceRoot: root,
		AutoPull:      true,
	})
	text := res.Content[0].Text
	if !res.IsError {
		t.Fatalf("the build tool rendered a chart for the unparsable Kubernetes version \"one.two\":\n%s", text)
	}
	entry := unmarshalBuildPayload(t, res).Refusal
	if entry == nil {
		t.Fatalf("the refusal carries no structured entry:\n%s", text)
	}
	code, _ := cerrors.CodeOf(wasm.ErrPluginValueNotRepresentable())
	shortCode, _ := cerrors.ShortCodeOf(wasm.ErrPluginValueNotRepresentable())
	if entry.Code != code || entry.ShortCode != shortCode {
		t.Errorf("the entry names %q / %q; the condition is %q / %q", entry.Code, entry.ShortCode, code, shortCode)
	}
	if entry.Severity != "halt" {
		t.Errorf("the entry's severity is %q; the finding that stopped a build is at the halting tier", entry.Severity)
	}
	for _, want := range []string{
		`node "plain" in boundary "broken"`,
		`annotation aggrega.ai/kubernetes-version: "one.two" is not a Kubernetes version a chart can be rendered for`,
	} {
		if !strings.Contains(entry.Message, want) || !strings.Contains(text, want) {
			t.Errorf("the refusal does not carry %q in both its entry and its text:\nentry: %s\ntext:\n%s",
				want, entry.Message, text)
		}
	}
}
