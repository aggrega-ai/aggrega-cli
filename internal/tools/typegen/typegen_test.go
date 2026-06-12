// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRegenClean is the regen-drift gate: for every registered kind, the
// generator's output must byte-match the committed <kind>_gen.go in pkg/types.
// A failure means the schema (or the generator) changed without regenerating —
// run `go generate ./pkg/types/...` and commit. One command covers every kind,
// so kind-batch migrations and CI self-verify determinism without bespoke steps.
func TestRegenClean(t *testing.T) {
	for outFile, src := range kindSources {
		t.Run(outFile, func(t *testing.T) {
			got, err := generate(src.typeName, src.schema)
			if err != nil {
				t.Fatalf("generate %s: %v", src.typeName, err)
			}
			path := filepath.Join("..", "..", "..", "pkg", "types", outFile)
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read committed %s: %v", path, err)
			}
			if string(got) != string(want) {
				t.Errorf("%s is stale: generator output differs from the committed file.\n"+
					"Run `go generate ./pkg/types/...` and commit the result.", outFile)
			}
		})
	}
}
