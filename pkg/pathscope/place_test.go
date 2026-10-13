// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pathscope_test

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/windowshost"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
)

// windowsCannotHoldNames are names Windows cannot hold as written, one per class,
// each with the segment a refusal must name.
var windowsCannotHoldNames = []struct {
	name, segment string
}{
	{"NUL", "NUL"},
	{"aux/x.yaml", "aux"},
	{"Com1.txt", "Com1.txt"},
	{"report.", "report."},
	{"notes /x.yaml", "notes "},
	{`.. \evil.yaml`, ".. "},
}

// climbingNames are names that climb out of their directory, through slashes or
// backslashes, or are absolute or empty — some also holding a segment Windows
// cannot hold, so a climb is shown to be judged before anything else.
var climbingNames = []string{"", "..", "../x.yaml", `..\x.yaml`, "a/../../x.yaml", "/etc/x.yaml", "../NUL", `..\aux\x.yaml`}

// asOnPlatform runs fn with the process answering that it runs on Windows, or
// not, and restores the answer afterwards. A test using it is not parallel.
func asOnPlatform(t *testing.T, windows bool) {
	t.Helper()
	t.Cleanup(windowshost.SetForTest(windows))
}

// TestPlaceBeneathPlacesANameWindowsCannotHoldOffWindows holds every other
// platform to what it did before: a name Windows cannot hold is placed like any
// other, at the root made a directory path joined with the name cleaned as a
// slash-separated name.
//
// 🔴 PLANTED DEFECT: invert the platform condition in PlaceBeneath
// (pkg/pathscope/place.go, `if windowshost.Running()` to
// `if !windowshost.Running()`), and every row here reddens, refused off Windows,
// while TestPlaceBeneathRefusesANameWindowsCannotHoldOnWindows reddens with it.
func TestPlaceBeneathPlacesANameWindowsCannotHoldOffWindows(t *testing.T) {
	asOnPlatform(t, false)
	root := t.TempDir()
	for _, tc := range windowsCannotHoldNames {
		t.Run(tc.name, func(t *testing.T) {
			got, inside, err := pathscope.PlaceBeneath(root, tc.name)
			want := filepath.Join(root, filepath.FromSlash(path.Clean(tc.name)))
			if err != nil || !inside || got != want {
				t.Fatalf("PlaceBeneath(%q, %q) = (%q, %v, %v); want (%q, true, nil) — off Windows a name Windows "+
					"cannot hold is placed as it is named", root, tc.name, got, inside, err, want)
			}
		})
	}
}

// TestPlaceBeneathRefusesANameWindowsCannotHoldOnWindows holds the refusal: where
// the process runs on Windows, each such name is refused with the one condition,
// naming the name and the segment Windows cannot hold, and no path is given.
func TestPlaceBeneathRefusesANameWindowsCannotHoldOnWindows(t *testing.T) {
	asOnPlatform(t, true)
	root := t.TempDir()
	for _, tc := range windowsCannotHoldNames {
		t.Run(tc.name, func(t *testing.T) {
			got, inside, err := pathscope.PlaceBeneath(root, tc.name)
			if !errors.Is(err, pathscope.ErrNameWindowsCannotHold()) {
				t.Fatalf("PlaceBeneath(%q, %q) = (%q, %v, %v); want the halt naming a name Windows cannot hold",
					root, tc.name, got, inside, err)
			}
			if got != "" || inside {
				t.Errorf("a refused name was given a place: (%q, %v)", got, inside)
			}
			msg := err.Error()
			for _, want := range []string{"the name " + strconv.Quote(tc.name), "its segment " + strconv.Quote(tc.segment), root} {
				if !strings.Contains(msg, want) {
					t.Errorf("the refusal %q does not name %s", msg, want)
				}
			}
			if strings.Contains(msg, "escape") || strings.Contains(msg, "traversal") || strings.Contains(msg, "outside") {
				t.Errorf("the refusal %q words a climb; a name Windows cannot hold leaves nothing", msg)
			}
		})
	}
}

// TestPlaceBeneathPlacesAnOrdinaryNameOnEveryPlatform is the positive control for
// the refusal: a name Windows holds is placed with the platform answered either way.
func TestPlaceBeneathPlacesAnOrdinaryNameOnEveryPlatform(t *testing.T) {
	root := t.TempDir()
	for _, windows := range []bool{false, true} {
		asOnPlatform(t, windows)
		for _, name := range []string{"config.yaml", "a/b/c.yaml", "a/../b.yaml", "..config", "console.log", `a\b.yaml`, "./x.yaml"} {
			got, inside, err := pathscope.PlaceBeneath(root, name)
			want := filepath.Join(root, filepath.FromSlash(path.Clean(name)))
			if err != nil || !inside || got != want {
				t.Errorf("windows=%v: PlaceBeneath(%q, %q) = (%q, %v, %v); want (%q, true, nil)",
					windows, root, name, got, inside, err, want)
			}
		}
	}
}

// TestPlaceBeneathJudgesTheClimbFirstOnEveryPlatform holds the climb verdict where
// it was: a name climbing out of its directory reports false and no error whether
// or not the process runs on Windows, a segment Windows cannot hold in it
// notwithstanding, so each writer refuses it in its own words as before.
func TestPlaceBeneathJudgesTheClimbFirstOnEveryPlatform(t *testing.T) {
	root := t.TempDir()
	for _, windows := range []bool{false, true} {
		asOnPlatform(t, windows)
		for _, name := range climbingNames {
			got, inside, err := pathscope.PlaceBeneath(root, name)
			if err != nil || inside || got != "" {
				t.Errorf("windows=%v: PlaceBeneath(%q, %q) = (%q, %v, %v); want (\"\", false, nil) — a climb is a climb "+
					"on every platform", windows, root, name, got, inside, err)
			}
			if pathscope.IsLocalSlash(name) {
				t.Errorf("IsLocalSlash(%q) accepts a name this table says climbs", name)
			}
		}
	}
}

// TestPlaceBeneathMakesTheRootADirectoryPath holds the root a name is joined onto
// to a directory path: a relative root, and the empty one, are made absolute
// against the working directory before the name is placed, so the path given is
// beneath the directory the root names wherever the process runs from.
//
// Not parallel: it moves the process working directory.
func TestPlaceBeneathMakesTheRootADirectoryPath(t *testing.T) {
	asOnPlatform(t, false)
	cwd := t.TempDir()
	t.Chdir(cwd)
	here, err := os.Getwd()
	if err != nil {
		t.Fatalf("read the working directory: %v", err)
	}
	for _, tc := range []struct {
		root, name, want string
	}{
		{"out", "a.yaml", filepath.Join(here, "out", "a.yaml")},
		{"./out/", "a/b.yaml", filepath.Join(here, "out", "a", "b.yaml")},
		{"", "a.yaml", filepath.Join(here, "a.yaml")},
		{".", `\notes.txt`, filepath.Join(here, filepath.FromSlash(`\notes.txt`))},
	} {
		got, inside, err := pathscope.PlaceBeneath(tc.root, tc.name)
		if err != nil || !inside || got != tc.want {
			t.Errorf("PlaceBeneath(%q, %q) = (%q, %v, %v); want (%q, true, nil)", tc.root, tc.name, got, inside, err, tc.want)
		}
		if !filepath.IsAbs(got) {
			t.Errorf("PlaceBeneath(%q, %q) gave %q, which is not a path a write lands at wherever the process runs", tc.root, tc.name, got)
		}
	}
}

// TestDirectoryPathIsAbsoluteAgainstTheWorkingDirectory holds the helper to its
// one answer: an absolute directory comes back cleaned, a relative one and the
// empty one come back absolute against the working directory.
//
// Not parallel: it moves the process working directory.
func TestDirectoryPathIsAbsoluteAgainstTheWorkingDirectory(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	here, err := os.Getwd()
	if err != nil {
		t.Fatalf("read the working directory: %v", err)
	}
	abs := filepath.Join(cwd, "elsewhere")
	for _, tc := range []struct {
		dir, want string
	}{
		{abs, abs},
		{abs + string(filepath.Separator), abs},
		{"drafts", filepath.Join(here, "drafts")},
		{"a/../drafts", filepath.Join(here, "drafts")},
		{".", here},
		{"", here},
	} {
		got, err := pathscope.DirectoryPath(tc.dir)
		if err != nil || got != tc.want {
			t.Errorf("DirectoryPath(%q) = (%q, %v); want (%q, nil)", tc.dir, got, err, tc.want)
		}
	}
}
