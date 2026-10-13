// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package artifactout

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/dryrunreport"
	"github.com/aggrega-ai/aggrega-cli/internal/windowshost"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
)

// TestARenderedNameWindowsCannotHoldIsRefusedOnWindowsAndWrittenElsewhere drives
// the rendered-artifact write over a set carrying a name Windows cannot hold, with
// the process answering both ways: on Windows the set is refused with the one
// condition, naming the name, and nothing of it is written; anywhere else it is
// written as it always was.
//
// Not parallel: it states the platform the process answers it runs on.
func TestARenderedNameWindowsCannotHoldIsRefusedOnWindowsAndWrittenElsewhere(t *testing.T) {
	const name = "boundaries/prod/aux.yaml"
	files := map[string][]byte{"boundaries/prod/app.yaml": []byte("app\n"), name: []byte("aux\n")}

	t.Run("on Windows", func(t *testing.T) {
		t.Cleanup(windowshost.SetForTest(true))
		ws := t.TempDir()
		out := filepath.Join(ws, DefaultOutputDir)
		err := WriteBuildOutput(ws, out, files)
		if !errors.Is(err, pathscope.ErrNameWindowsCannotHold()) {
			t.Fatalf("WriteBuildOutput refused with %v; want the halt naming a name Windows cannot hold", err)
		}
		if !strings.Contains(err.Error(), strconv.Quote(name)) {
			t.Errorf("the refusal %q does not name %q", err.Error(), name)
		}
		if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
			t.Errorf("the output directory exists after the set was refused (stat err = %v)", statErr)
		}
	})
	t.Run("anywhere else", func(t *testing.T) {
		if windowshost.Running() {
			t.Skip("writes a name Windows cannot hold, which is what a Windows machine refuses")
		}
		t.Cleanup(windowshost.SetForTest(false))
		ws := t.TempDir()
		out := filepath.Join(ws, DefaultOutputDir)
		if err := WriteBuildOutput(ws, out, files); err != nil {
			t.Fatalf("WriteBuildOutput refused a name off Windows, which it never has: %v", err)
		}
		if got, rerr := os.ReadFile(filepath.Join(out, filepath.FromSlash(name))); rerr != nil || string(got) != "aux\n" {
			t.Errorf("%s holds %q (read error %v); want the rendered bytes", name, got, rerr)
		}
	})
}

// TestTheDryRunReportNamesThePathAWriteWrites holds the report to the write, for
// an output directory spelled absolute and for one named relative to the working
// directory: each path the report names is the file the write of the same set to
// the same directory put the same bytes at, and the write put no file the report
// does not name.
//
// The expected paths are read back from the tree the write left rather than
// computed, so the test does not restate how a name is placed: a report spelling a
// path any other way than the write places it — from the directory as handed, say,
// where the write makes it a directory path first — names a file that is not
// there.
//
// Not parallel: one row runs in a working directory of its own.
func TestTheDryRunReportNamesThePathAWriteWrites(t *testing.T) {
	// Each name's bytes are distinct, so the file holding them identifies the name.
	files := map[string][]byte{
		"boundaries/prod/app.yaml": []byte("a"),
		"a/../b.yaml":              []byte("b"),
		`c\d.yaml`:                 []byte("c"),
		"..config":                 []byte("d"),
	}
	rows := []struct {
		name string
		// outDir returns the output directory as the report and the write are handed
		// it, for a workspace at ws whose output directory is ws/dist.
		outDir func(t *testing.T, ws string) string
	}{
		{
			name:   "an absolute output directory",
			outDir: func(t *testing.T, ws string) string { return filepath.Join(ws, DefaultOutputDir) },
		},
		{
			name: "an output directory named relative to the working directory",
			outDir: func(t *testing.T, ws string) string {
				t.Chdir(ws)
				return DefaultOutputDir
			},
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ws := t.TempDir()
			out := row.outDir(t, ws)
			var report bytes.Buffer
			if err := WriteDryRunReport(&report, out, files); err != nil {
				t.Fatalf("WriteDryRunReport(%q): %v", out, err)
			}
			if err := WriteAll(ws, out, files); err != nil {
				t.Fatalf("WriteAll(%q): %v", out, err)
			}
			writtenAt := map[string]string{}
			walkErr := filepath.WalkDir(filepath.Join(ws, DefaultOutputDir), func(p string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}
				content, rerr := os.ReadFile(p)
				if rerr != nil {
					return rerr
				}
				writtenAt[string(content)] = p
				return nil
			})
			if walkErr != nil {
				t.Fatalf("reading back the written tree: %v", walkErr)
			}
			if len(writtenAt) != len(files) {
				t.Fatalf("the write left %d files for %d names: %v", len(writtenAt), len(files), writtenAt)
			}
			var want []dryrunreport.File
			for _, name := range sortedNames(files) {
				p, ok := writtenAt[string(files[name])]
				if !ok {
					t.Fatalf("the write put no file holding the bytes of %q: %v", name, writtenAt)
				}
				want = append(want, dryrunreport.File{Path: p, Content: files[name]})
			}
			var expected bytes.Buffer
			dryrunreport.Write(&expected, want)
			if report.String() != expected.String() {
				t.Errorf("the dry-run report names other paths than the write wrote:\nreport:\n%s\nwritten:\n%s", report.String(), expected.String())
			}
		})
	}
}

// TestTheDryRunReportRefusesWhatAWriteRefusesAndPrintsNothing drives the report
// over a set holding one name a write refuses beside one it accepts: the report
// returns the write's refusal and prints nothing, rather than reporting a write
// that would not happen.
//
// Not parallel: one row states the platform the process answers it runs on.
func TestTheDryRunReportRefusesWhatAWriteRefusesAndPrintsNothing(t *testing.T) {
	rows := []struct {
		name    string
		windows bool
		refused string
		// is reports whether err is the refusal the row's name draws.
		is func(err error) bool
	}{
		{
			name:    "a name escaping the output directory",
			refused: "z/../../escape.yaml",
			is: func(err error) bool {
				return err != nil && strings.Contains(err.Error(), "escapes the output root")
			},
		},
		{
			name:    "on Windows, a name Windows cannot hold",
			windows: true,
			refused: "boundaries/prod/aux.yaml",
			is: func(err error) bool {
				return errors.Is(err, pathscope.ErrNameWindowsCannotHold())
			},
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			if row.windows {
				t.Cleanup(windowshost.SetForTest(true))
			}
			// The accepted name sorts ahead of the refused one, so a report printing
			// as it placed would already have printed it.
			files := map[string][]byte{"boundaries/prod/app.yaml": []byte("app\n"), row.refused: []byte("x\n")}
			var report bytes.Buffer
			err := WriteDryRunReport(&report, t.TempDir(), files)
			if !row.is(err) {
				t.Fatalf("WriteDryRunReport returned %v; want the refusal of %q", err, row.refused)
			}
			if !strings.Contains(err.Error(), strconv.Quote(row.refused)) {
				t.Errorf("the refusal %q does not name %q", err.Error(), row.refused)
			}
			if report.Len() != 0 {
				t.Errorf("the report printed %q for a set it refused; want nothing", report.String())
			}
		})
	}
}
