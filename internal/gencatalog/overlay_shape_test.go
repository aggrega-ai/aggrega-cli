// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package gencatalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOverlayOfTheWrongShapeIsRefusedInTheAuthorsWords holds the refusal of an
// authored overlay whose field is written in the wrong shape to the line, the
// field and the two shapes, never the decoder's own phrasing or the program's
// types.
func TestOverlayOfTheWrongShapeIsRefusedInTheAuthorsWords(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "probe.yaml")
	doc := "apiVersion: aggrega.ai/v1beta1\nkind: Plugin\nmetadata:\n  name: probe\nspec:\n  runtime:\n    maxMemoryPages: lots\n"
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := loadOverlayFile(path)
	if err == nil {
		t.Fatal("an overlay of the wrong shape was accepted")
	}
	const want = "line 7 (spec.runtime.maxMemoryPages): text `lots` is written where a number is expected"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not say %q", err, want)
	}
	for _, leak := range []string{"cannot unmarshal", "uint32", "!!"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("error %q carries %q, which names nothing the author wrote", err, leak)
		}
	}
}
