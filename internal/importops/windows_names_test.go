// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/internal/windowshost"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
)

// draftsWindowsCannotHold are drafted paths Windows cannot hold as written: a
// device name, a device name as a directory, and a name ending in a full stop.
var draftsWindowsCannotHold = []string{"NUL", "aux/x.yaml", "report."}

// TestImportDraftsANameWindowsCannotHoldOffWindows holds every platform but Windows
// to what an import did before: a draft Windows cannot hold is judged contained and
// written at the path it names.
//
// 🔴 PLANTED DEFECT: invert the platform condition in pathscope.PlaceBeneath
// (pkg/pathscope/place.go, `if windowshost.Running()` to
// `if !windowshost.Running()`), and every row here reddens, refused off Windows.
//
// Not parallel: it states the platform the process answers it runs on.
func TestImportDraftsANameWindowsCannotHoldOffWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("writes names Windows cannot hold, which is what a Windows machine refuses")
	}
	t.Cleanup(windowshost.SetForTest(false))
	for _, path := range draftsWindowsCannotHold {
		t.Run(path, func(t *testing.T) {
			out := t.TempDir()
			files := []abi.ScaffoldFile{{Path: path, Content: "drafted\n"}}
			if err := checkFiles(out, out, files); err != nil {
				t.Fatalf("checkFiles(%q) refused a draft off Windows, which it never has: %v", path, err)
			}
			written, err := writeFiles(out, files)
			if err != nil || len(written) != 1 || written[0] != path {
				t.Fatalf("writeFiles(%q) = (%v, %v); want ([%q], nil)", path, written, err, path)
			}
			got, rerr := os.ReadFile(filepath.Join(out, filepath.FromSlash(path)))
			if rerr != nil || string(got) != "drafted\n" {
				t.Errorf("%s holds %q (read error %v); want the drafted content", path, got, rerr)
			}
		})
	}
}

// TestImportRefusesADraftWindowsCannotHoldOnWindows holds the refusal: where the
// process runs on Windows, a set of drafts carrying one such path is refused with
// the one condition, naming the path, and nothing of the set is written — the
// contained draft beside it included.
//
// Not parallel: it states the platform the process answers it runs on.
func TestImportRefusesADraftWindowsCannotHoldOnWindows(t *testing.T) {
	t.Cleanup(windowshost.SetForTest(true))
	for _, path := range append(draftsWindowsCannotHold, `.. \evil.yaml`) {
		t.Run(path, func(t *testing.T) {
			out := t.TempDir()
			files := []abi.ScaffoldFile{{Path: "contained.yaml", Content: "first"}, {Path: path, Content: "second"}}
			err := checkFiles(out, out, files)
			if !errors.Is(err, pathscope.ErrNameWindowsCannotHold()) {
				t.Fatalf("checkFiles refused %q with %v; want the halt naming a draft Windows cannot hold", path, err)
			}
			var escaping *EscapingPathError
			if errors.As(err, &escaping) {
				t.Errorf("the refusal %v reads as a draft leaving the output directory; it leaves nothing", err)
			}
			if !strings.Contains(err.Error(), strconv.Quote(path)) {
				t.Errorf("the refusal %q does not name the draft %q", err.Error(), path)
			}
			if inside, rerr := os.ReadDir(out); rerr != nil || len(inside) != 0 {
				t.Errorf("a refused set must write nothing; the output directory holds %v (read error %v)", inside, rerr)
			}
		})
	}
}

// TestImportOnWindowsRefusesADraftTheImporterNamedForADevice drives the refusal
// through the real guest, under both deliveries: the demo importer names its
// drafted file from the namespace its attachment hands it, so a namespace of
// "aux/x" makes the PLUGIN return "aux/x-demo.yaml". Where the process runs on
// Windows, a write and a dry run alike are refused naming it, and nothing is
// written; the control row, off Windows, drafts it as it always has.
//
// Not parallel: it points the shared import seam and states the platform.
func TestImportOnWindowsRefusesADraftTheImporterNamedForADevice(t *testing.T) {
	pointSeamAtDemoGuestAttachedBy(t, 0)
	const drafted = "aux/x-demo.yaml"
	for _, row := range []struct {
		name    string
		windows bool
		dryRun  bool
	}{
		{name: "a write, off Windows"},
		{name: "a write, on Windows", windows: true},
		{name: "a dry run, on Windows", windows: true, dryRun: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			if !row.windows && runtime.GOOS == "windows" {
				t.Skip("writes a name Windows cannot hold, which is what a Windows machine refuses")
			}
			t.Cleanup(windowshost.SetForTest(row.windows))
			rw := aWorkspaceAttaching(t, importopstest.Attachment{
				URI:      demoImporterIdentity,
				Settings: map[string]any{"target_namespace": "aux/x"},
			})
			out := filepath.Join(rw.Root(), "drafts")
			res, err := Import(context.Background(), rw, Options{
				SourcePath:    writtenSource(t, "demo source"),
				SourceName:    "demo-source.txt",
				Format:        "demo",
				OutputDir:     out,
				Delivery:      authoredcontent.DeliveryFor(row.dryRun),
				ResolvePlugin: noopResolver,
			})
			if !row.windows {
				if err != nil {
					t.Fatalf("off Windows the draft %q is written as it always was; got %v", drafted, err)
				}
				if len(res.Written) != 2 || res.Written[0] != drafted || res.Written[1] != RecordFileName {
					t.Fatalf("the import wrote %v; want [%q %q], the draft and its record", res.Written, drafted,
						RecordFileName)
				}
				return
			}
			if !errors.Is(err, pathscope.ErrNameWindowsCannotHold()) {
				t.Fatalf("on Windows the draft %q must be refused naming it; got %v (answer %+v)", drafted, err, res.Files)
			}
			if !strings.Contains(err.Error(), strconv.Quote(drafted)) {
				t.Errorf("the refusal %q does not name the draft %q", err.Error(), drafted)
			}
			if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
				t.Errorf("the output directory exists after the import was refused (stat err = %v)", statErr)
			}
		})
	}
}

// TestADraftBeneathANamedDirectoryLandsBeneathItsDirectoryPath holds the import's
// destination to a directory path: a directory named relative is made absolute
// against the working directory before any draft is placed beneath it, so the
// directory the result reports and the directory a draft opening with a separator
// lands beneath are one.
//
// Not parallel: it moves the process working directory.
func TestADraftBeneathANamedDirectoryLandsBeneathItsDirectoryPath(t *testing.T) {
	t.Cleanup(windowshost.SetForTest(false))
	cwd := t.TempDir()
	t.Chdir(cwd)
	here, err := os.Getwd()
	if err != nil {
		t.Fatalf("read the working directory: %v", err)
	}
	outputDir, err := outputDirFor(nil, "drafts")
	if err != nil || outputDir != filepath.Join(here, "drafts") {
		t.Fatalf("outputDirFor(%q) = (%q, %v); want (%q, nil)", "drafts", outputDir, err, filepath.Join(here, "drafts"))
	}
	files := []abi.ScaffoldFile{{Path: `\notes.txt`, Content: "noted\n"}}
	if err := checkFiles(here, outputDir, files); err != nil {
		t.Fatalf("checkFiles: %v", err)
	}
	if _, err := writeFiles(outputDir, files); err != nil {
		t.Fatalf("writeFiles: %v", err)
	}
	want := filepath.Join(outputDir, filepath.FromSlash(`\notes.txt`))
	if got, rerr := os.ReadFile(want); rerr != nil || string(got) != "noted\n" {
		t.Errorf("the draft is not beneath the directory the result reports, at %s: %q, %v", want, got, rerr)
	}
}
