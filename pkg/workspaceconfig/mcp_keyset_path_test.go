// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package workspaceconfig_test

import (
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestResolveMCPKeySetPathAnchorsToItsDeclaringFile pins that the file holding
// the keys a listener verifies tokens against is declaring-file-relative, on
// the same terms as the listener's certificate and key and as every other path
// the cascade carries.
//
// It is driven through Resolve rather than through the raw loader because
// anchoring is the cascade's work and the loader performs none of it.
//
// A planted defect is visible either way round: drop the key-set line from the
// resolution and the global-declared path reads back as the relative string the
// file carried; anchor it unconditionally rather than only when relative and
// the absolute row reads back a path under the workspace root.
func TestResolveMCPKeySetPathAnchorsToItsDeclaringFile(t *testing.T) {
	globalDir := t.TempDir() // sets env, so not parallel
	globalPath := filepath.Join(globalDir, "global.yaml")
	write(t, globalPath, "mcp:\n  authorization:\n    jwks_file: ./global-keys.json\n")
	t.Setenv("AGGREGA_CONFIG", globalPath)

	t.Run("a global declaration anchors to the global file and a silent layer leaves it alone", func(t *testing.T) {
		root := t.TempDir()
		write(t, filepath.Join(root, ".aggrega-config.yaml"), "workspace: {}\n")

		rw, err := workspaceconfig.Resolve(root, "")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		got := rw.Config().MCP.Authorization.JWKSFile
		if want := filepath.Join(globalDir, "global-keys.json"); got != want {
			t.Errorf("mcp.authorization.jwks_file: got %q, want %q (the declaring file is the global one, "+
				"and the workspace layer declared nothing to re-anchor it with)", got, want)
		}
	})

	t.Run("a workspace declaration anchors to the workspace", func(t *testing.T) {
		root := t.TempDir()
		write(t, filepath.Join(root, ".aggrega-config.yaml"),
			"workspace: {}\nmcp:\n  authorization:\n    jwks_file: ./keys.json\n")

		rw, err := workspaceconfig.Resolve(root, "")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		got := rw.Config().MCP.Authorization.JWKSFile
		if want := filepath.Join(root, "keys.json"); got != want {
			t.Errorf("mcp.authorization.jwks_file: got %q, want %q (relative, declared by the workspace file)",
				got, want)
		}
	})

	t.Run("an absolute path passes through", func(t *testing.T) {
		root := t.TempDir()
		absolute := filepath.Join(root, "elsewhere", "keys.json")
		write(t, filepath.Join(root, ".aggrega-config.yaml"),
			"workspace: {}\nmcp:\n  authorization:\n    jwks_file: "+absolute+"\n")

		rw, err := workspaceconfig.Resolve(root, "")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got := rw.Config().MCP.Authorization.JWKSFile; got != absolute {
			t.Errorf("mcp.authorization.jwks_file: got %q, want %q (absolute, unchanged)", got, absolute)
		}
	})
}
