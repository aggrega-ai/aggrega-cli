// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cacheops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/externalinputs"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/cacheslot"
)

// TestTheCacheInventoryListsAClassRootThatIsALinkWhereItLeads drives the cache
// inventory every cache status, prune and clear reads, over a workspace whose
// source-artifact and external-input class roots are the directories themselves
// (the control) and links to directories elsewhere inside the workspace, and over
// a compiled-module cache that is its directory or a link the machine's owner
// placed.
//
// The read hold admits a class root leading inside the workspace, and every write
// and read of the cache goes through it, so the inventory lists what is there: the
// slot, the blob and the compiled module, each named as in the control. Walked as
// the link alone, the source-artifact class lists nothing, and the external-input
// store and the compiled-module cache list one element named "." — the class root
// taken for a file.
func TestTheCacheInventoryListsAClassRootThatIsALinkWhereItLeads(t *testing.T) {
	const (
		slotLabel   = "git-repository/example/abc"
		module      = "wazero-v1-amd64-linux/module"
		blobLabelOf = "sha256/"
	)
	blob := strings.Repeat("b", 64)
	put := func(t *testing.T, path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := func(t *testing.T, target, at string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, at); err != nil {
			t.Skipf("symbolic links are not available here: %v", err)
		}
	}
	for _, row := range []struct {
		name   string
		linked bool
	}{
		{"control: every class root the directory itself", false},
		{"class roots that are links to directories elsewhere inside the workspace", true},
	} {
		t.Run(row.name, func(t *testing.T) {
			base := t.TempDir()
			ws := filepath.Join(base, "ws")
			sources := cacheslot.SourceArtifactsRoot(ws, testCacheDir)
			inputs := externalinputs.StoreRoot(ws, testCacheDir)
			compileDir := filepath.Join(base, "wasm-compile")
			sourcesAt, inputsAt, compileAt := sources, inputs, compileDir
			if row.linked {
				sourcesAt = filepath.Join(ws, "kept", "source-artifacts")
				inputsAt = filepath.Join(ws, "kept", "inputs")
				compileAt = filepath.Join(base, "compile-kept")
			}
			put(t, filepath.Join(sourcesAt, filepath.FromSlash(slotLabel), "metadata.yaml"),
				"digest: sha256:"+strings.Repeat("a", 64)+"\nurl: https://example.invalid/repo.git\n")
			put(t, filepath.Join(inputsAt, "sha256", blob), "input bytes")
			put(t, filepath.Join(compileAt, filepath.FromSlash(module)), strings.Repeat("m", 64))
			if row.linked {
				link(t, filepath.Join("..", "..", "kept", "source-artifacts"), sources)
				link(t, filepath.Join("..", "..", "kept", "inputs"), inputs)
				link(t, compileAt, compileDir)
			}

			classes, err := Collect(t.Context(), Locations{WorkspaceRoot: ws, CacheDir: testCacheDir, CompileDir: compileDir})
			if err != nil {
				t.Fatalf("Collect: %v", err)
			}
			want := map[string]string{
				"workspace source-artifact cache": slotLabel,
				"workspace external-input store":  blobLabelOf + blob,
				CompiledModuleClassName:           module,
			}
			for _, cl := range classes {
				label, ok := want[cl.Name]
				if !ok {
					continue
				}
				delete(want, cl.Name)
				var got []string
				for _, el := range cl.Elements {
					got = append(got, el.Label)
				}
				if len(got) != 1 || got[0] != label {
					t.Errorf("%s lists %q; want [%s]", cl.Name, got, label)
				}
			}
			for name := range want {
				t.Errorf("the class %q was not enumerated", name)
			}
		})
	}
}
