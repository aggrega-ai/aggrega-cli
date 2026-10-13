// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package importops

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
)

// TestOnWindowsAnImportRefusesADraftWindowsCannotHold runs on a Windows machine,
// with nothing standing in for the platform: a set of drafts carrying one named
// after a device, one with a device name as its directory, or one whose first
// segment is two dots and a space is refused naming that draft, and nothing of the
// set is written — not inside the output directory, and not beside it, where
// Windows would put a segment of two dots and a space if it read it as the parent
// directory.
//
// 🔴 PLANTED DEFECT, run on Windows: disable the Windows-only branch in
// pathscope.PlaceBeneath (pkg/pathscope/place.go, `if windowshost.Running()` to
// `if false`), and every row reddens.
func TestOnWindowsAnImportRefusesADraftWindowsCannotHold(t *testing.T) {
	for _, path := range []string{"NUL", "aux/x.yaml", `.. \evil.yaml`} {
		t.Run(path, func(t *testing.T) {
			parent := t.TempDir()
			out := filepath.Join(parent, "out")
			if err := os.MkdirAll(out, 0o755); err != nil {
				t.Fatalf("mkdir output directory: %v", err)
			}
			files := []abi.ScaffoldFile{{Path: "contained.yaml", Content: "first"}, {Path: path, Content: "second"}}
			err := checkFiles(parent, out, files)
			if !errors.Is(err, pathscope.ErrNameWindowsCannotHold()) {
				t.Fatalf("checkFiles refused %q with %v; want the halt naming a draft Windows cannot hold", path, err)
			}
			if !strings.Contains(err.Error(), strconv.Quote(path)) {
				t.Errorf("the refusal %q does not name the draft %q", err.Error(), path)
			}
			if inside, rerr := os.ReadDir(out); rerr != nil || len(inside) != 0 {
				t.Errorf("a refused set must write nothing; the output directory holds %v (read error %v)", inside, rerr)
			}
			if _, statErr := os.Stat(filepath.Join(parent, "evil.yaml")); !os.IsNotExist(statErr) {
				t.Errorf("a draft was written beside the output directory (stat err = %v)", statErr)
			}
		})
	}
}

// TestOnWindowsADraftBeneathABareDriveLandsBeneathTheCurrentDirectory runs on a
// Windows machine: an output directory named as the working drive alone, such as
// "D:", is the current directory on that drive, so a draft whose path opens with a
// separator is written beneath the current directory and nothing is written at the
// top of the drive.
//
// 🔴 PLANTED DEFECT, run on Windows: have pathscope.DirectoryPath
// (pkg/pathscope/place.go) return dir unchanged, and the draft is written at the
// top of the drive instead, and this test reddens.
//
// Not parallel: it moves the process working directory.
func TestOnWindowsADraftBeneathABareDriveLandsBeneathTheCurrentDirectory(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	here, err := os.Getwd()
	if err != nil {
		t.Fatalf("read the working directory: %v", err)
	}
	drive := filepath.VolumeName(here)
	if len(drive) != 2 || drive[1] != ':' {
		t.Fatalf("the working directory %s is not on a drive named by a letter; this row needs one", here)
	}
	const draft = `\aggrega-bare-drive-probe.yaml`
	atTheTop := drive + draft
	if _, statErr := os.Stat(atTheTop); !os.IsNotExist(statErr) {
		t.Fatalf("%s exists before the run, so this row could not tell a write there from what was there (stat err = %v)",
			atTheTop, statErr)
	}

	outputDir, err := outputDirFor(nil, drive)
	if err != nil {
		t.Fatalf("outputDirFor(%q): %v", drive, err)
	}
	if !strings.EqualFold(outputDir, here) {
		t.Errorf("outputDirFor(%q) = %q; want the current directory on that drive, %s", drive, outputDir, here)
	}
	files := []abi.ScaffoldFile{{Path: draft, Content: "probe\n"}}
	if err := checkFiles(here, outputDir, files); err != nil {
		t.Fatalf("checkFiles: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(atTheTop) })
	if _, err := writeFiles(outputDir, files); err != nil {
		t.Fatalf("writeFiles: %v", err)
	}
	if got, rerr := os.ReadFile(filepath.Join(here, filepath.Base(filepath.FromSlash(draft)))); rerr != nil || string(got) != "probe\n" {
		t.Errorf("the draft is not beneath the current directory %s: %q, %v", here, got, rerr)
	}
	if _, statErr := os.Stat(atTheTop); !os.IsNotExist(statErr) {
		t.Errorf("%s was written at the top of the drive (stat err = %v)", atTheTop, statErr)
	}
}
