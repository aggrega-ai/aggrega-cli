// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validateops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// kindWrittenAsAList is a document whose kind is written as a list — the one
// fault every row below carries.
const kindWrittenAsAList = "apiVersion: aggrega.ai/v1alpha1\nkind:\n  - Component\nmetadata:\n  name: example/compute/probe\n"

// TestPreambleRefusalNamesTheKindAsWritten holds both validate scopes that read a
// document's kind before anything else to a refusal in the author's words: the
// line, the field and the two shapes, never the decoder's own phrasing.
func TestPreambleRefusalNamesTheKindAsWritten(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	entity := filepath.Join(dir, "kind-list.yaml")
	if err := os.WriteFile(entity, []byte(kindWrittenAsAList), 0o600); err != nil {
		t.Fatal(err)
	}
	wasmPath := filepath.Join(dir, "probe.wasm")
	if err := os.WriteFile(wasmPath, []byte("not a module"), 0o600); err != nil {
		t.Fatal(err)
	}
	const want = "line 3 (kind): a list is written where text is expected"
	cases := []struct {
		name string
		run  func() error
	}{
		{"one entity", func() error {
			_, err := ValidateEntityContent(context.Background(), dir, t.TempDir(), nil, nil, entity, []byte(kindWrittenAsAList), false)
			return err
		}},
		{"a plugin binary against its entity", func() error {
			_, err := ValidatePluginBinary(context.Background(), wasmPath, "", entity, 0)
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("validation panicked: %v", r)
					}
				}()
				err = tc.run()
			}()
			if err == nil {
				t.Fatal("a kind written as a list was accepted")
			}
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not say %q", err, want)
			}
			for _, leak := range []string{"cannot unmarshal", "!!", "unmarshal errors"} {
				if strings.Contains(err.Error(), leak) {
					t.Errorf("error %q carries the decoder's own text %q", err, leak)
				}
			}
		})
	}
}
