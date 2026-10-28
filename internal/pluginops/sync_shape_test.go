// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSyncRefusesAPluginEntityOfTheWrongShapeInTheAuthorsWords holds the refusal
// of a Plugin entity whose field is written in the wrong shape — read before the
// .wasm is looked at — to the line, the field and the two shapes. One fault per
// row.
func TestSyncRefusesAPluginEntityOfTheWrongShapeInTheAuthorsWords(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{"memory pages written as text",
			"apiVersion: aggrega.ai/v1alpha1\nkind: Plugin\nmetadata:\n  name: probe\n  version: \"1.0.0\"\n  pluginType: provider\nspec:\n  runtime:\n    maxMemoryPages: lots\n",
			"line 9 (spec.runtime.maxMemoryPages): text `lots` is written where a number is expected"},
		{"metadata written as a list",
			"apiVersion: aggrega.ai/v1alpha1\nkind: Plugin\nmetadata:\n  - probe\n",
			"line 4 (metadata): a list is written where"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			entity := filepath.Join(dir, "plugin.yaml")
			wasmPath := filepath.Join(dir, "plugin.wasm")
			if err := os.WriteFile(entity, []byte(tc.doc), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(wasmPath, []byte("not a module"), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Sync(context.Background(), SyncOptions{EntityPath: entity, PluginPath: wasmPath})
			if err == nil {
				t.Fatal("an entity of the wrong shape was accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not say %q", err, tc.want)
			}
			for _, leak := range []string{"cannot unmarshal", "uint32", "types.", "!!"} {
				if strings.Contains(err.Error(), leak) {
					t.Errorf("error %q carries %q, which names nothing the author wrote", err, leak)
				}
			}
		})
	}
}
