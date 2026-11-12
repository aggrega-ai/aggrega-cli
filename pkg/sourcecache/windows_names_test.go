// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package sourcecache_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/windowshost"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcecache"
)

// entriesWindowsCannotHold are archive entry names Windows cannot hold as written:
// a device name, a device name as a directory, and a name ending in a full stop.
var entriesWindowsCannotHold = []string{"NUL", "aux/x.yaml", "report."}

// TestTheLayoutLaysOutANameWindowsCannotHoldOffWindows holds every platform but
// Windows to what the layout did before: an archive carrying an entry Windows
// cannot hold is laid out with that entry at the place it names, beside the
// archive's other files.
//
// 🔴 PLANTED DEFECT: invert the platform condition in pathscope.PlaceBeneath
// (pkg/pathscope/place.go, `if windowshost.Running()` to
// `if !windowshost.Running()`), and every row here reddens, refused off Windows.
//
// Not parallel: it states the platform the process answers it runs on.
func TestTheLayoutLaysOutANameWindowsCannotHoldOffWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("lays out names Windows cannot hold, which is what a Windows machine refuses")
	}
	t.Cleanup(windowshost.SetForTest(false))
	for _, name := range entriesWindowsCannotHold {
		t.Run(name, func(t *testing.T) {
			slot := t.TempDir()
			archive := buildTGZ(t, map[string]string{"inside.yaml": "inside\n", name: "held\n"})
			tree, err := sourcecache.RegenerateExtracted("", "", slot, archive)
			if err != nil {
				t.Fatalf("an archive carrying %q must lay out off Windows, as it always has; got %v", name, err)
			}
			for rel, want := range map[string]string{"inside.yaml": "inside\n", name: "held\n"} {
				got, rerr := os.ReadFile(filepath.Join(tree, filepath.FromSlash(rel)))
				if rerr != nil || string(got) != want {
					t.Errorf("%s in the laid-out tree holds %q (read error %v); want %q", rel, got, rerr, want)
				}
			}
		})
	}
}

// TestTheLayoutRefusesANameWindowsCannotHoldOnWindows holds the refusal: where the
// process runs on Windows, an archive carrying such an entry is refused with the
// one condition, naming the entry, and nothing of the archive is laid out — not
// the entry, not the archive's other files, not the tree.
//
// Not parallel: it states the platform the process answers it runs on.
func TestTheLayoutRefusesANameWindowsCannotHoldOnWindows(t *testing.T) {
	t.Cleanup(windowshost.SetForTest(true))
	for _, name := range append(entriesWindowsCannotHold, `.. \evil.yaml`) {
		t.Run(name, func(t *testing.T) {
			slot := t.TempDir()
			archive := buildTGZ(t, map[string]string{"inside.yaml": "inside\n", name: "held\n"})
			_, err := sourcecache.RegenerateExtracted("", "", slot, archive)
			if !errors.Is(err, pathscope.ErrNameWindowsCannotHold()) {
				t.Fatalf("an archive carrying %q must be refused on Windows naming it; got %v", name, err)
			}
			if !strings.Contains(err.Error(), strconv.Quote(name)) {
				t.Errorf("the refusal %q does not name the entry %q", err.Error(), name)
			}
			if errors.Is(err, sourcecache.ErrFetchIntegrity()) {
				t.Errorf("the refusal %v reads as an integrity failure; a name Windows cannot hold is not one", err)
			}
			if _, statErr := os.Stat(sourcecache.ExtractedTree(slot, digestOf(archive))); !os.IsNotExist(statErr) {
				t.Errorf("the tree was laid out although the archive was refused (stat err = %v)", statErr)
			}
		})
	}
}

// TestTheDocumentLayoutRefusesANameWindowsCannotHoldOnWindows holds a single
// document's name to the refusal an archive entry meets.
//
// Not parallel: it states the platform the process answers it runs on.
func TestTheDocumentLayoutRefusesANameWindowsCannotHoldOnWindows(t *testing.T) {
	t.Cleanup(windowshost.SetForTest(true))
	slot := t.TempDir()
	_, err := sourcecache.RegenerateExtractedDocument("", "", slot, "con.yaml", []byte("kind: Component\n"))
	if !errors.Is(err, pathscope.ErrNameWindowsCannotHold()) {
		t.Fatalf("a document named con.yaml must be refused on Windows; got %v", err)
	}
}
