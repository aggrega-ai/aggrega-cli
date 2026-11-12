// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package artifactout

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteAllRefusesANameClimbingThroughBackslashes drives the containment
// refusal through the writer's own entry point. A rendered name is joined onto
// the output root in the host's spelling, and a Windows join reads a backslash as
// a separator, so a name climbing out through backslashes would be written
// outside the output root there. It is refused on every platform, before
// anything is written — the contained file beside it included.
//
// The last row climbs only once cleaned as a slash-separated name, which is the
// spelling this writer joins, so it holds the writer to the cleaned reading as
// well as to the name as written.
func TestWriteAllRefusesANameClimbingThroughBackslashes(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		`..\escaped.yaml`,
		`..\..\escaped.yaml`,
		`a\..\..\escaped.yaml`,
		`a\b/../..\escaped.yaml`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ws := t.TempDir()
			out := filepath.Join(ws, "dist")
			err := WriteAll(ws, out, map[string][]byte{
				"kept.yaml": []byte("kept"),
				name:        []byte("escaped"),
			})
			if err == nil {
				t.Fatalf("WriteAll accepted %q; a name climbing out through a backslash must be refused on every platform", name)
			}
			if !strings.Contains(err.Error(), "escapes the output root") {
				t.Fatalf("refusal = %v; want the refusal naming the output root", err)
			}
			for _, p := range []string{
				filepath.Join(out, "kept.yaml"),
				filepath.Join(ws, "escaped.yaml"),
				filepath.Join(filepath.Dir(ws), "escaped.yaml"),
			} {
				if _, statErr := os.Stat(p); !os.IsNotExist(statErr) {
					t.Errorf("%s exists after the set was refused (stat err = %v)", p, statErr)
				}
			}
		})
	}
}

// TestWriteAllWritesANameHoldingAContainedBackslash is the positive control for
// the refusal above: a backslash between ordinary parts climbs nowhere, so the
// name is written, at the place this host's join gives it.
func TestWriteAllWritesANameHoldingAContainedBackslash(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	const name = `a\b.yaml`
	if err := WriteAll(ws, ws, map[string][]byte{name: []byte("kept")}); err != nil {
		t.Fatalf("WriteAll(%q): %v", name, err)
	}
	got, err := os.ReadFile(filepath.Join(ws, filepath.FromSlash(name)))
	if err != nil || string(got) != "kept" {
		t.Fatalf("read back %q: %q, %v; want the written content", name, got, err)
	}
}
