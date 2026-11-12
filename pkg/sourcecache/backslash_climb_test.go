// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package sourcecache_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/sourcecache"
)

// TestRegenerateExtractedRefusesAnEntryClimbingThroughBackslashes drives the
// traversal refusal through the exported layout step. An archive entry is laid
// out at a host path joined from its name, and a Windows join reads a backslash
// as a separator, so an entry climbing out through backslashes would be written
// beside the tree there. It is refused as an integrity failure on every platform,
// before anything is written.
//
// The last row climbs only once its name is cleaned as a slash-separated name —
// which is the spelling the layout joins — so it holds the layout to the cleaned
// reading as well as to the name as written. The positive control is the same
// archive without the climbing entry, laid out, so each refusal is the climbing
// entry's and not the archive's.
func TestRegenerateExtractedRefusesAnEntryClimbingThroughBackslashes(t *testing.T) {
	t.Parallel()
	inside := map[string]string{"inside.yaml": "inside\n"}
	if _, err := sourcecache.RegenerateExtracted("", "", t.TempDir(), buildTGZ(t, inside)); err != nil {
		t.Fatalf("the archive without the climbing entry must lay out; got %v", err)
	}
	for _, name := range []string{
		`..\escaped.yaml`,
		`..\..\escaped.yaml`,
		`a\..\..\escaped.yaml`,
		`a\b/../..\escaped.yaml`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			slot := t.TempDir()
			archive := buildTGZ(t, map[string]string{"inside.yaml": "inside\n", name: "escaped\n"})
			_, err := sourcecache.RegenerateExtracted("", "", slot, archive)
			if !errors.Is(err, sourcecache.ErrFetchIntegrity()) {
				t.Fatalf("an entry climbing out through a backslash must be refused as an integrity failure; got %v", err)
			}
			for _, p := range []string{
				filepath.Join(slot, "escaped.yaml"),
				filepath.Join(sourcecache.ExtractedDir(slot), "escaped.yaml"),
				sourcecache.ExtractedTree(slot, digestOf(archive)),
			} {
				if _, statErr := os.Stat(p); !os.IsNotExist(statErr) {
					t.Errorf("%s exists although the archive was refused (stat err = %v)", p, statErr)
				}
			}
		})
	}
}

// TestRegenerateExtractedDocumentRefusesANameClimbingThroughBackslashes pins that
// a single document's name is held to the same backslash reading an archive
// entry is.
func TestRegenerateExtractedDocumentRefusesANameClimbingThroughBackslashes(t *testing.T) {
	t.Parallel()
	slot := t.TempDir()
	_, err := sourcecache.RegenerateExtractedDocument("", "", slot, `..\entity.yaml`, []byte("kind: Component\n"))
	if !errors.Is(err, sourcecache.ErrFetchIntegrity()) {
		t.Fatalf("a document name climbing out through a backslash must be refused as an integrity failure; got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(sourcecache.ExtractedDir(slot), "entity.yaml")); !os.IsNotExist(statErr) {
		t.Errorf("the document was written beside the tree (stat err = %v)", statErr)
	}
}
