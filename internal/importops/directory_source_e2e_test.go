// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

// End-to-end coverage for a source that is a directory, through a real catalog
// folder: the files-probe importer — a guest kept beside these tests that declares
// two file patterns and reports back exactly what it was handed — the demo
// importer, which declares none, and the JSON parser, each a Plugin entity
// generated from its built module with the module beside it. Every case runs the
// REAL seam: Import builds it from the workspace's own catalog and configuration,
// and nothing below points that variable.
//
// What the importer was handed is read off the importer's own report, so a case
// cannot pass on what the host meant to send: the names, the bytes, the order and
// the whole input payload are what the guest received.
//
// 🔴 PLANTED DEFECT: make selectsFile (internal/importops/detect.go) select every
// file — replace `if len(sig.Files) > 0 && filesMatch(sig.Files, name) {` with
// `if true {` — and TestADirectoryImportHandsTheImporterExactlyTheFilesItsPatternsSelect
// reddens on every row: the selection takes the file no pattern names, which the
// case has made unreadable, and the import is refused reading it.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// directoryImportWorkspace lays the files-probe importer, the demo importer and
// the JSON parser into a scratch workspace's conventional catalog folder, writes
// its configuration carrying the attachments attach returns for the probe as laid
// (attach may be nil), and resolves it.
//
// No YAML parser is laid, and that is deliberate: the demo importer's signature
// reads a source as YAML and names no file, so a recognition over a directory that
// tried it against any file would stop for want of a parser. A directory recognised
// over this folder is therefore proof that a signature naming no file applies to
// none of a directory's files.
func directoryImportWorkspace(t *testing.T,
	attach func(probe importopstest.LaidPlugin) []importopstest.Attachment) (*workspaceconfig.ResolvedWorkspace,
	importopstest.LaidPlugin) {
	t.Helper()
	wsDir := aWorkspaceDir(t)
	catalogDir := filepath.Join(wsDir, workspaceconfig.ConventionalCatalogDir)
	probe := importopstest.LayFilesProbeImporter(t, wsDir, catalogDir)
	importopstest.LayDemoImporter(t, wsDir, catalogDir)
	importopstest.LayJSONParser(t, wsDir, catalogDir)
	var attachments []importopstest.Attachment
	if attach != nil {
		attachments = attach(probe)
	}
	importopstest.WriteConfiguration(t, wsDir, attachments...)
	rw := resolveWorkspaceAt(t, wsDir)
	if _, err := os.Stat(filepath.Join(rw.Root(), workspaceconfig.ConfigFilename)); err != nil {
		t.Fatalf("the workspace resolved at %s, which is not the directory its configuration was written in: %v",
			rw.Root(), err)
	}
	return rw, probe
}

// importDirectory runs Import over the source at path, named name, through rw,
// reporting rather than writing.
func importDirectory(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, path, name, format string) (Result, error) {
	t.Helper()
	return Import(context.Background(), rw, Options{
		SourcePath:    path,
		SourceName:    name,
		Format:        format,
		Delivery:      authoredcontent.DeliveryFor(true),
		ResolvePlugin: (&recordingCascade{}).resolve,
	})
}

// probeReportOf reads the files-probe importer's report off an import's
// diagnostics.
func probeReportOf(t *testing.T, res Result) importopstest.FilesProbeReport {
	t.Helper()
	messages := make([]string, 0, len(res.Diagnostics))
	for _, d := range res.Diagnostics {
		messages = append(messages, d.Message)
	}
	return importopstest.ReadFilesProbeReport(t, messages)
}

// makeUnreadable takes every permission off the file at path for the rest of the
// test and holds the test to it: a file the case relies on never being opened has
// to be one whose opening would fail. Where it can still be read — a run with the
// privileges to read anything — the case fails rather than passing on a file it
// cannot tell was left alone.
func makeUnreadable(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0); err != nil {
		t.Fatalf("take the permissions off %s: %v", path, err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
	if _, err := os.ReadFile(path); err == nil {
		t.Fatalf("%s is still readable with no permissions, so nothing here can show it was never opened", path)
	}
}

// skipWhereNoFileIsUnreadable skips a case that needs a file whose opening fails,
// on the one platform where taking a file's permission bits away leaves it
// readable.
func skipWhereNoFileIsUnreadable(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows reads a file whatever its permission bits say, so no file here can be made unreadable")
	}
}

// probeDirectory lays the directory clause 3's first case names at dir: two files
// the probe's patterns select, of one base name in two subdirectories, and a third
// no pattern selects, made unreadable. It returns the sources the importer is to be
// handed, in order.
func probeDirectory(t *testing.T, dir string) []abi.ImportSource {
	t.Helper()
	a := importopstest.FilesProbeBody("a")
	b := importopstest.FilesProbeBody("b")
	importopstest.WriteSource(t, filepath.Join(dir, "a", "x.probe.json"), a)
	importopstest.WriteSource(t, filepath.Join(dir, "b", "x.probe.json"), b)
	makeUnreadable(t, importopstest.WriteSource(t, filepath.Join(dir, "notes.txt"), "notes"))
	return []abi.ImportSource{{Name: "a/x.probe.json", Bytes: []byte(a)}, {Name: "b/x.probe.json", Bytes: []byte(b)}}
}

// assertHandedExactly holds the probe's report to the sources want, in order, with
// empty primary bytes, and holds the whole input payload the importer received to
// the one those sources, the format and the directory's name encode — so nothing
// else rode along. Every directory these cases import is named flows.
func assertHandedExactly(t *testing.T, res Result, want []abi.ImportSource) {
	t.Helper()
	report := probeReportOf(t, res)
	if len(report.Primary) != 0 {
		t.Errorf("a directory import handed the importer primary bytes %q; want none", report.Primary)
	}
	if !slices.EqualFunc(report.Sources, want, func(a, b abi.ImportSource) bool {
		return a.Name == b.Name && string(a.Bytes) == string(b.Bytes)
	}) {
		t.Fatalf("the importer was handed %s; want exactly %s", sourceNames(report.Sources), sourceNames(want))
	}
	for _, s := range report.Sources {
		if strings.Contains(s.Name, `\`) {
			t.Errorf("the source name %q carries a host separator", s.Name)
		}
	}
	payload, err := abi.ImportInput{Format: importopstest.FilesProbeFormat, Name: "flows", Sources: want}.EncodeCBOR()
	if err != nil {
		t.Fatalf("encode the expected input: %v", err)
	}
	if string(report.Input) != string(payload) {
		t.Errorf("the importer received an input payload of %d bytes that is not the one the format, the "+
			"directory's name and those sources encode (%d bytes)", len(report.Input), len(payload))
	}
}

// TestADirectoryImportHandsTheImporterTheDirectorysName imports a directory
// through the real Import and reads off the input payload the files-probe
// importer received the name the request carries: the directory's own base name,
// whole — a name holding a full stop keeps it — however the path to it is spelled,
// and whatever name the caller received the source under, with empty primary
// bytes beside it. The directory is named, inside a temporary directory of the
// test's own, so the name read is the one laid down.
//
// 🔴 PLANTED DEFECT: in primaryName (internal/importops/source.go), return "" for
// a directory source: every row reads an empty name, and the case fails.
func TestADirectoryImportHandsTheImporterTheDirectorysName(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		scenario string
		spell    func(dir string) string
		name     string
	}{
		{"the directory's path", func(dir string) string { return dir }, "my.project"},
		{"the path ending in a separator", func(dir string) string { return dir + string(filepath.Separator) },
			"my.project/"},
		{"the path ending in the current directory", func(dir string) string {
			return dir + string(filepath.Separator) + "."
		}, "."},
	} {
		t.Run(row.scenario, func(t *testing.T) {
			t.Parallel()
			rw, _ := directoryImportWorkspace(t, nil)
			dir := filepath.Join(t.TempDir(), "my.project")
			importopstest.WriteSource(t, filepath.Join(dir, "a", "x.probe.json"), importopstest.FilesProbeBody("a"))
			importopstest.WriteSource(t, filepath.Join(dir, "b", "x.probe.json"), importopstest.FilesProbeBody("b"))

			res, err := importDirectory(t, rw, row.spell(dir), row.name, importopstest.FilesProbeFormat)
			if err != nil {
				t.Fatalf("the directory import was refused: %v", err)
			}
			report := probeReportOf(t, res)
			var in abi.ImportInput
			if err := in.DecodeCBOR(report.Input); err != nil {
				t.Fatalf("decode the input payload the importer received: %v", err)
			}
			if in.Name != "my.project" {
				t.Errorf("the importer was handed the name %q; want the directory's, %q", in.Name, "my.project")
			}
			if len(in.Bytes) != 0 || len(in.Sources) != 2 {
				t.Errorf("the importer was handed %d primary bytes and %d named sources; want none and the "+
					"directory's two selected files", len(in.Bytes), len(in.Sources))
			}
		})
	}
}

// sourceNames lists named sources by name, for a failure message.
func sourceNames(sources []abi.ImportSource) []string {
	names := make([]string, 0, len(sources))
	for _, s := range sources {
		names = append(names, s.Name)
	}
	return names
}

// TestADirectoryImportHandsTheImporterExactlyTheFilesItsPatternsSelect is clause
// 3's first case, for a directory inside the workspace and one outside it: the
// importer receives the two files its patterns select, named by their paths in the
// directory, in that order, with empty primary bytes, and the third file — which no
// pattern selects and which the case made unreadable — is never opened, since
// opening it would have failed the import.
func TestADirectoryImportHandsTheImporterExactlyTheFilesItsPatternsSelect(t *testing.T) {
	t.Parallel()
	skipWhereNoFileIsUnreadable(t)
	for _, placement := range []struct {
		name string
		dir  func(t *testing.T, root string) string
	}{
		{"a directory inside the workspace", func(_ *testing.T, root string) string { return filepath.Join(root, "flows") }},
		{"a directory outside the workspace", func(t *testing.T, _ string) string { return filepath.Join(t.TempDir(), "flows") }},
	} {
		t.Run(placement.name, func(t *testing.T) {
			t.Parallel()
			rw, _ := directoryImportWorkspace(t, nil)
			dir := placement.dir(t, rw.Root())
			want := probeDirectory(t, dir)

			res, err := importDirectory(t, rw, dir, "flows", importopstest.FilesProbeFormat)
			if err != nil {
				t.Fatalf("the directory import was refused: %v", err)
			}
			if res.Format != importopstest.FilesProbeFormat {
				t.Errorf("the import ran as the %q format, want %q", res.Format, importopstest.FilesProbeFormat)
			}
			assertHandedExactly(t, res, want)
		})
	}
}

// TestADirectoryImportRecognisesItsFormatFromTheFilesItHolds is clause 3's second
// case: the same directory, no format named, is recognised as the files-probe
// format from the files the probe's patterns select, and the importer is handed
// exactly what it is handed when the format is named.
//
// The folder also holds the demo importer, whose signature names no file and reads
// a source as YAML, and no YAML parser — so the recognition succeeding is proof that
// a signature naming no file was tried against none of the directory's files.
func TestADirectoryImportRecognisesItsFormatFromTheFilesItHolds(t *testing.T) {
	t.Parallel()
	skipWhereNoFileIsUnreadable(t)
	rw, _ := directoryImportWorkspace(t, nil)
	dir := filepath.Join(rw.Root(), "flows")
	want := probeDirectory(t, dir)

	res, err := importDirectory(t, rw, dir, "flows", "")
	if err != nil {
		t.Fatalf("the directory was not recognised: %v", err)
	}
	if res.Format != importopstest.FilesProbeFormat {
		t.Errorf("the directory was recognised as the %q format, want %q", res.Format, importopstest.FilesProbeFormat)
	}
	assertHandedExactly(t, res, want)
}

// TestADirectoryLinkLeadingOutIsRefusedBeforeAnythingIsRead is clause 3's third
// case: a link inside the directory leading out of it is refused under the
// workspace's link condition, naming the link by its place in the directory, and
// nothing is read — the file sorting ahead of the link is unreadable, so a read
// made before the links were judged would have been refused for that file
// instead.
//
// The rows are a link to a file outside the workspace, a link to a file elsewhere
// inside the same workspace — refused because the directory named is the root of
// its own reads wherever it lies — and a link to a directory outside it, each with
// the format named and with it recognised.
func TestADirectoryLinkLeadingOutIsRefusedBeforeAnythingIsRead(t *testing.T) {
	t.Parallel()
	skipWhereNoFileIsUnreadable(t)
	for _, row := range []struct {
		name string
		// lay puts the link down in dir, inside the workspace rooted at root, and
		// returns its name in the directory.
		lay func(t *testing.T, root, dir string) string
	}{
		{"a link to a file outside the workspace", func(t *testing.T, _, dir string) string {
			target := importopstest.WriteSource(t, filepath.Join(t.TempDir(), "x.probe.json"),
				importopstest.FilesProbeBody("outside"))
			mustSymlink(t, target, filepath.Join(dir, "b", "x.probe.json"))
			return "b/x.probe.json"
		}},
		{"a link to a file elsewhere in the workspace", func(t *testing.T, root, dir string) string {
			target := importopstest.WriteSource(t, filepath.Join(root, "elsewhere", "x.probe.json"),
				importopstest.FilesProbeBody("elsewhere"))
			mustSymlink(t, target, filepath.Join(dir, "b", "x.probe.json"))
			return "b/x.probe.json"
		}},
		{"a link to a directory outside the workspace", func(t *testing.T, _, dir string) string {
			target := filepath.Join(t.TempDir(), "more")
			importopstest.WriteSource(t, filepath.Join(target, "y.probe.json"), importopstest.FilesProbeBody("more"))
			mustSymlink(t, target, filepath.Join(dir, "more"))
			return "more"
		}},
	} {
		for _, format := range []string{importopstest.FilesProbeFormat, ""} {
			label := "the format named"
			if format == "" {
				label = "the format recognised"
			}
			t.Run(row.name+", "+label, func(t *testing.T) {
				t.Parallel()
				rw, _ := directoryImportWorkspace(t, nil)
				dir := filepath.Join(rw.Root(), "flows")
				makeUnreadable(t, importopstest.WriteSource(t, filepath.Join(dir, "a", "x.probe.json"),
					importopstest.FilesProbeBody("a")))
				link := row.lay(t, rw.Root(), dir)

				res, err := importDirectory(t, rw, dir, "flows", format)
				if err == nil {
					t.Fatalf("a link leading out of the directory was read through: %+v", res.Diagnostics)
				}
				var readErr *SourceReadError
				if errors.As(err, &readErr) {
					t.Fatalf("the import read a file before judging the links: %v", err)
				}
				var linked *DirectoryLinkError
				if !errors.As(err, &linked) {
					t.Fatalf("the refusal is not the directory link refusal: %T: %v", err, err)
				}
				if linked.Link != link {
					t.Errorf("the refusal names the link %q, want %q", linked.Link, link)
				}
				if !errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
					t.Errorf("the refusal is not the read-leads-out-of-workspace condition: %v", err)
				}
				if short, _ := cerrors.ShortCodeOf(err); short != "E0321" {
					t.Errorf("the refusal carries short code %q, want E0321", short)
				}
				if !strings.Contains(err.Error(), "A directory you import is read only from inside itself") {
					t.Errorf("the refusal does not carry the remedy an imported directory has: %v", err)
				}
			})
		}
	}
}

// mustSymlink makes link a symbolic link to target, creating the directory holding
// it, and skips the case where this platform will not make one.
func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatalf("create the directory for %s: %v", link, err)
	}
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("this Windows account may not make a symbolic link: %v", err)
		}
		t.Fatalf("make the link %s: %v", link, err)
	}
}

// TestADirectoryIsRefusedForAnImporterDeclaringNoFilePatterns is clause 3's fourth
// case: the demo importer declares no file pattern, so a directory named for its
// format is refused under its own condition, naming the format and the importer,
// before any file is opened — the one file the directory holds is unreadable.
func TestADirectoryIsRefusedForAnImporterDeclaringNoFilePatterns(t *testing.T) {
	t.Parallel()
	skipWhereNoFileIsUnreadable(t)
	rw, _ := directoryImportWorkspace(t, nil)
	dir := filepath.Join(rw.Root(), "flows")
	makeUnreadable(t, importopstest.WriteSource(t, filepath.Join(dir, "x.probe.json"), importopstest.FilesProbeBody("x")))

	_, err := importDirectory(t, rw, dir, "flows", "demo")
	if !errors.Is(err, ErrImporterDeclaresNoFilePatterns()) {
		t.Fatalf("the directory was not refused as one the importer cannot read: %v", err)
	}
	if short, _ := cerrors.ShortCodeOf(err); short != "E0341" {
		t.Errorf("the refusal carries short code %q, want E0341", short)
	}
	for _, want := range []string{
		"the importer serving the source's format declares no file patterns, so it reads one file and cannot read a " +
			"directory",
		`format "demo"`,
		`importer "importer/` + importopstest.DemoImporterName + `"`,
		`directory "flows"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not carry %q: %v", want, err)
		}
	}
}

// TestADirectoryHoldingNoFileThePatternsSelectIsRefused is the case beside the
// fourth: the files-probe importer declares patterns and the directory holds no
// file they select, so it is refused under its own condition naming the patterns,
// rather than handing the importer nothing.
func TestADirectoryHoldingNoFileThePatternsSelectIsRefused(t *testing.T) {
	t.Parallel()
	rw, _ := directoryImportWorkspace(t, nil)
	dir := filepath.Join(rw.Root(), "flows")
	importopstest.WriteSource(t, filepath.Join(dir, "notes.txt"), "notes")
	importopstest.WriteSource(t, filepath.Join(dir, "flow.json"), importopstest.FilesProbeBody("unnamed"))

	_, err := importDirectory(t, rw, dir, "flows", importopstest.FilesProbeFormat)
	if !errors.Is(err, ErrImportDirectoryMatchedNothing()) {
		t.Fatalf("the directory was not refused as one holding nothing the importer reads: %v", err)
	}
	if short, _ := cerrors.ShortCodeOf(err); short != "E0342" {
		t.Errorf("the refusal carries short code %q, want E0342", short)
	}
	for _, want := range []string{
		"no file in the directory matches the file patterns the importer serving the source's format declares",
		`file patterns "*.probe.json", "probe/*.json"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not carry %q: %v", want, err)
		}
	}
}

// TestADirectoryAndAnAttachmentsSourcesMeetUnderOneListOfNames is clause 3's fifth
// case and its control. The attachment configuring the probe names a file in its
// with.sources, handed on under its base name: a directory file of the same name is
// refused under its own condition, naming the name and the with.sources file, and a
// directory file of another name reaches the importer beside the attachment's, in
// one list sorted by name.
func TestADirectoryAndAnAttachmentsSourcesMeetUnderOneListOfNames(t *testing.T) {
	t.Parallel()
	attached := importopstest.WriteSource(t, filepath.Join(t.TempDir(), "elsewhere", "top.probe.json"),
		importopstest.FilesProbeBody("attached"))
	attach := func(probe importopstest.LaidPlugin) []importopstest.Attachment {
		return []importopstest.Attachment{{URI: probe.Identity, Sources: []string{attached}}}
	}

	t.Run("a directory file of the attachment source's name is refused", func(t *testing.T) {
		t.Parallel()
		rw, _ := directoryImportWorkspace(t, attach)
		dir := filepath.Join(rw.Root(), "flows")
		importopstest.WriteSource(t, filepath.Join(dir, "top.probe.json"), importopstest.FilesProbeBody("dir"))

		_, err := importDirectory(t, rw, dir, "flows", importopstest.FilesProbeFormat)
		if !errors.Is(err, ErrImportSourceNameCollision()) {
			t.Fatalf("a name both sources carry was not refused: %v", err)
		}
		if short, _ := cerrors.ShortCodeOf(err); short != "E0343" {
			t.Errorf("the refusal carries short code %q, want E0343", short)
		}
		for _, want := range []string{
			"a file read from the directory and a file named in the importer's `with.sources` have the same name",
			`name "top.probe.json"`,
			"the with.sources file " + `"` + attached + `"`,
		} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not carry %q: %v", want, err)
			}
		}
	})

	t.Run("control: a directory file of another name reaches the importer beside it", func(t *testing.T) {
		t.Parallel()
		rw, _ := directoryImportWorkspace(t, attach)
		dir := filepath.Join(rw.Root(), "flows")
		body := importopstest.FilesProbeBody("dir")
		importopstest.WriteSource(t, filepath.Join(dir, "sub", "top.probe.json"), body)

		res, err := importDirectory(t, rw, dir, "flows", importopstest.FilesProbeFormat)
		if err != nil {
			t.Fatalf("a directory file and an attachment source of different names were refused: %v", err)
		}
		assertHandedExactly(t, res, []abi.ImportSource{
			{Name: "sub/top.probe.json", Bytes: []byte(body)},
			{Name: "top.probe.json", Bytes: []byte(importopstest.FilesProbeBody("attached"))},
		})
	})
}

// TestASingleFileImportHandsTheFileWholeAsBefore holds a single file's import to
// the payload it crosses with: the file's bytes as the primary bytes,
// under the file's base name, named sources only where the attachment configuring
// the importer names some, under their base names — compared byte for byte with the
// input the format, the file and those sources encode — for the format named and
// recognised.
func TestASingleFileImportHandsTheFileWholeAsBefore(t *testing.T) {
	t.Parallel()
	body := importopstest.FilesProbeBody("single")
	attachedBody := importopstest.FilesProbeBody("attached")
	attached := importopstest.WriteSource(t, filepath.Join(t.TempDir(), "with", "extra.json"), attachedBody)

	for _, row := range []struct {
		name    string
		format  string
		attach  func(importopstest.LaidPlugin) []importopstest.Attachment
		sources []abi.ImportSource
	}{
		{name: "the format named", format: importopstest.FilesProbeFormat},
		{name: "the format recognised"},
		{name: "an attachment naming a source", format: importopstest.FilesProbeFormat,
			attach: func(probe importopstest.LaidPlugin) []importopstest.Attachment {
				return []importopstest.Attachment{{URI: probe.Identity, Sources: []string{attached}}}
			},
			sources: []abi.ImportSource{{Name: "extra.json", Bytes: []byte(attachedBody)}}},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			rw, _ := directoryImportWorkspace(t, row.attach)
			if row.format == "" {
				// The demo importer's signature names no file, so it applies to a single
				// file whatever its name and reads it as YAML: a recognition of one file
				// parses it in YAML too, as it always has.
				importopstest.LayYAMLParser(t, rw.Root(), filepath.Join(rw.Root(), workspaceconfig.ConventionalCatalogDir))
			}
			source := importopstest.WriteSource(t, filepath.Join(t.TempDir(), "flow.probe.json"), body)

			res, err := importDirectory(t, rw, source, "flow.probe.json", row.format)
			if err != nil {
				t.Fatalf("the single-file import was refused: %v", err)
			}
			report := probeReportOf(t, res)
			if string(report.Primary) != body {
				t.Errorf("the importer was handed primary bytes %q; want the file's own %q", report.Primary, body)
			}
			payload, err := abi.ImportInput{Format: importopstest.FilesProbeFormat, Bytes: []byte(body),
				Name: "flow.probe.json", Sources: row.sources}.EncodeCBOR()
			if err != nil {
				t.Fatalf("encode the expected input: %v", err)
			}
			if string(report.Input) != string(payload) {
				t.Errorf("the importer received an input payload that is not the one the format, the file and the "+
					"attachment's sources encode: got %d bytes, want %d; sources %s", len(report.Input), len(payload),
					sourceNames(report.Sources))
			}
		})
	}
}
