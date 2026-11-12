// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package wasm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
)

// loadReadSourceProbe builds (or reuses) the read-source-probe fixture and loads
// it through the production Loader with sourceRoot set to root. The fixture's
// probe export reads an index file whose content names a second file, reads that
// named file, and echoes its bytes — or an in-band failure message — as output.
func loadReadSourceProbe(t *testing.T, ctx context.Context, root string) *Plugin {
	t.Helper()
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "read-source-probe-go.wasm")

	loader := &Loader{}
	plugin, err := loader.Load(ctx, PluginSpec{Name: "read-source-probe-go", Path: pluginPath})
	if err != nil {
		t.Fatalf("Load read-source-probe fixture: %v", err)
	}
	t.Cleanup(func() { _ = plugin.Close(ctx) })
	plugin.SetSourceRoot(root)
	return plugin
}

// TestReadSourceProbe_ContentBasedSelection exercises the full guest →
// aggrega_read_source_file host function → acquired-source path: the guest reads
// an index file, and the index's CONTENT selects the second file it reads and
// returns. This proves the content-read callback end-to-end — a real,
// content-dependent selection a guest cannot make from the name-only listing
// alone.
func TestReadSourceProbe_ContentBasedSelection(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.txt"), []byte("chosen.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "chosen.txt"), []byte("the-selected-payload"), 0o644); err != nil {
		t.Fatal(err)
	}

	plugin := loadReadSourceProbe(t, ctx, root)
	out, err := plugin.Call(ctx, "probe", []byte("index.txt"))
	if err != nil {
		t.Fatalf("probe call: %v", err)
	}
	if string(out) != "the-selected-payload" {
		t.Fatalf("guest output = %q; want the content-selected payload", out)
	}
}

// TestReadSourceProbe_SandboxRejectsEscape proves the content-read callback's
// sandbox: when the index file's content names a path escaping the source root,
// the host refuses the read in-band and the guest carries the refusal message
// out — never a trap, never a read outside the acquired source.
func TestReadSourceProbe_SandboxRejectsEscape(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.txt"), []byte("../escape.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	plugin := loadReadSourceProbe(t, ctx, root)
	out, err := plugin.Call(ctx, "probe", []byte("index.txt"))
	if err != nil {
		t.Fatalf("probe call: %v", err)
	}
	// The guest branched on the machine-matchable error code end-to-end: a real
	// host sandbox-escape refusal crossed the ABI as source_escape, the PDK
	// surfaced it as a typed HostError, and the guest matched the code — proving
	// the code identity crosses, not just the prose.
	if !strings.Contains(string(out), "code=source_escape") {
		t.Errorf("guest output = %q; want the coded escape identity code=source_escape", out)
	}
	// The free-form message is preserved beside the code.
	if !strings.Contains(string(out), "escapes the source root") {
		t.Errorf("guest output = %q; want the sandbox refusal message preserved", out)
	}
}
