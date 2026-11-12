// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package sourcecache_test

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcecache"
)

// TestOnWindowsTheLayoutRefusesANameWindowsCannotHold runs on a Windows machine,
// with nothing standing in for the platform: an archive carrying an entry named
// after a device, one with a device name as its directory, and one whose first
// segment is two dots and a space is refused naming the entry, and nothing of it
// is written — not inside the tree, and not beside it, where Windows would put a
// segment of two dots and a space if it read it as the parent directory.
//
// 🔴 PLANTED DEFECT, run on Windows: disable the Windows-only branch in
// pathscope.PlaceBeneath (pkg/pathscope/place.go, `if windowshost.Running()` to
// `if false`), and every row reddens.
func TestOnWindowsTheLayoutRefusesANameWindowsCannotHold(t *testing.T) {
	for _, name := range []string{"NUL", "aux/x.yaml", `.. \evil.yaml`} {
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			slot := filepath.Join(parent, "slot")
			archive := buildTGZ(t, map[string]string{"inside.yaml": "inside\n", name: "held\n"})
			_, err := sourcecache.RegenerateExtracted("", "", slot, archive)
			if !errors.Is(err, pathscope.ErrNameWindowsCannotHold()) {
				t.Fatalf("an archive carrying %q must be refused on Windows naming it; got %v", name, err)
			}
			if !strings.Contains(err.Error(), strconv.Quote(name)) {
				t.Errorf("the refusal %q does not name the entry %q", err.Error(), name)
			}
			for _, p := range []string{
				sourcecache.ExtractedTree(slot, digestOf(archive)),
				filepath.Join(sourcecache.ExtractedDir(slot), "evil.yaml"),
				filepath.Join(slot, "evil.yaml"),
				filepath.Join(parent, "evil.yaml"),
			} {
				if _, statErr := os.Stat(p); !os.IsNotExist(statErr) {
					t.Errorf("%s exists although the archive was refused (stat err = %v)", p, statErr)
				}
			}
		})
	}
}
