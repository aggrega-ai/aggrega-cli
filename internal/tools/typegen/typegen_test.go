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

// codecTargetPaths maps each codec target's output basename to the directory
// (relative to this test package) holding the committed generated file. The
// regen-drift gate reads the committed file there and byte-compares it to a
// fresh generation.
var codecTargetPaths = map[string][]string{
	"frp_gen.go": {"..", "..", "abi", "frpcodec"},
}

// TestRegenClean_Codec is the regen-drift gate for the multi-type codec
// targets: re-running the generator for each codec target must byte-match the
// committed generated file. A failure means the schema or the generator
// changed without regenerating — run `go generate` for the target package and
// commit. This extends the entity regen-drift contract to the codec spine so
// the reflect-free codec self-verifies determinism alongside the entity types.
func TestRegenClean_Codec(t *testing.T) {
	for outFile := range codecTargets {
		t.Run(outFile, func(t *testing.T) {
			got, err := generateFor(outFile)
			if err != nil {
				t.Fatalf("generate codec %s: %v", outFile, err)
			}
			dir, ok := codecTargetPaths[outFile]
			if !ok {
				t.Fatalf("no committed path registered for codec target %q", outFile)
			}
			path := filepath.Join(append(append([]string{}, dir...), outFile)...)
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read committed %s: %v", path, err)
			}
			if string(got) != string(want) {
				t.Errorf("%s is stale: generator output differs from the committed file.\n"+
					"Run `go generate` for the target package and commit the result.", outFile)
			}
		})
	}
}
