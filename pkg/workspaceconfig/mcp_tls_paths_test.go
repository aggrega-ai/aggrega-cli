// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig_test

import (
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestResolveMCPTLSPathsAnchorToTheirDeclaringFile pins that the listener's
// certificate and key are declaring-file-relative, on the same terms as every
// other path the cascade carries.
//
// The three readings below are the three a reader can write, and they are driven
// through Resolve rather than through the raw loader because anchoring is the
// cascade's work and the loader performs none of it.
//
//   - A relative path declared in the global layer anchors to that file's
//     directory, and a nearer layer that says nothing about it does NOT re-anchor
//     the absolute path already carried — the idempotence the per-layer
//     resolution rests on.
//   - A relative path declared in the workspace layer anchors to the workspace.
//   - An absolute path passes through whichever layer declared it.
//
// A planted defect is visible either way round: drop the certificate line from
// the resolution and the global-declared path reads back as the relative string
// the file carried; anchor it unconditionally rather than only when relative and
// the idempotence row reads back a path under the workspace root instead of
// under the global file's directory.
func TestResolveMCPTLSPathsAnchorToTheirDeclaringFile(t *testing.T) {
	globalDir := t.TempDir() // sets env, so not parallel
	globalPath := filepath.Join(globalDir, "global.yaml")
	write(t, globalPath, "mcp:\n  tls:\n    cert_file: ./global.pem\n    key_file: ./global.key\n")
	t.Setenv("AGGREGA_CONFIG", globalPath)

	t.Run("a global declaration anchors to the global file and a silent layer leaves it alone", func(t *testing.T) {
		root := t.TempDir()
		write(t, filepath.Join(root, ".aggrega-config.yaml"), "workspace: {}\n")

		rw, err := workspaceconfig.Resolve(root, "")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		tls := rw.Config().MCP.TLS
		if got, want := tls.CertFile, filepath.Join(globalDir, "global.pem"); got != want {
			t.Errorf("mcp.tls.cert_file: got %q, want %q (the declaring file is the global one, and the "+
				"workspace layer declared nothing to re-anchor it with)", got, want)
		}
		if got, want := tls.KeyFile, filepath.Join(globalDir, "global.key"); got != want {
			t.Errorf("mcp.tls.key_file: got %q, want %q", got, want)
		}
	})

	t.Run("a workspace declaration anchors to the workspace and an absolute path passes through", func(t *testing.T) {
		root := t.TempDir()
		absoluteKey := filepath.Join(root, "elsewhere", "server.key")
		write(t, filepath.Join(root, ".aggrega-config.yaml"),
			"workspace: {}\nmcp:\n  tls:\n    cert_file: ./server.pem\n    key_file: "+absoluteKey+"\n")

		rw, err := workspaceconfig.Resolve(root, "")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		tls := rw.Config().MCP.TLS
		if got, want := tls.CertFile, filepath.Join(root, "server.pem"); got != want {
			t.Errorf("mcp.tls.cert_file: got %q, want %q (relative, declared by the workspace file)", got, want)
		}
		if got := tls.KeyFile; got != absoluteKey {
			t.Errorf("mcp.tls.key_file: got %q, want %q (absolute, unchanged)", got, absoluteKey)
		}
	})
}
