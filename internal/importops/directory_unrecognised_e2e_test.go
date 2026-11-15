// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

// End-to-end coverage for the refusal of a source no importer's signatures
// recognise, through the real Import over a workspace whose catalog folder holds no
// importer, so the importers recognition reads are the compiler's own core
// importers and nothing else. The core importers' signatures are read off the
// entities the binary carries, so recognising reaches no registry.
//
// A directory holding only a Langflow export is the case: the Langflow importer's
// signature names no file, so it applies to none of a directory's files, and
// naming the langflow format would be refused for the same reason. So the
// directory's refusal offers importing one of its files on its own, or a format
// whose importer declares file patterns — and the formats it names are held to
// the ones the importers' own declarations say read a directory, computed here
// from those declarations rather than listed.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// langflowExport is a committed Langflow export, relative to this package.
const langflowExport = "../compiler/importersocket/testdata/langflow-flow.json"

// formatsDeclaringFilePatterns returns, in the order the seam lists its
// importers, every format advertised by an importer one of whose recorded
// signatures names a file pattern, without repeats, and every format advertised
// at all.
//
// It reads the signatures off the entities directly rather than through the
// package's own predicate, so the refusal is held to the declarations and not to
// the code under test.
func formatsDeclaringFilePatterns(t *testing.T, rw *workspaceconfig.ResolvedWorkspace) (readers, advertised []string) {
	t.Helper()
	seam, err := buildSeam(rw, (&recordingCascade{}).resolve)
	if err != nil {
		t.Fatalf("build the import's seam: %v", err)
	}
	for _, importer := range seam.Importers {
		formats := catalog.ImporterFormats(importer)
		for _, f := range formats {
			if !slices.Contains(advertised, f) {
				advertised = append(advertised, f)
			}
		}
		if importer.Spec.Importer == nil {
			continue
		}
		declares := false
		for _, sig := range importer.Spec.Importer.Detect {
			if len(sig.Files) > 0 {
				declares = true
			}
		}
		if !declares {
			continue
		}
		for _, f := range formats {
			if !slices.Contains(readers, f) {
				readers = append(readers, f)
			}
		}
	}
	return readers, advertised
}

// aWorkspaceWithAnEmptyCatalogFolder resolves a real workspace, as aWorkspace
// does, whose conventional catalog folder exists and holds nothing: a catalog
// holding no importer, read from a folder, so the importers recognition reads are
// the core importers alone and no published catalog snapshot is asked for.
func aWorkspaceWithAnEmptyCatalogFolder(t *testing.T) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	wsDir := aWorkspaceDir(t)
	if err := os.WriteFile(filepath.Join(wsDir, workspaceconfig.ConfigFilename), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write workspace config: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(wsDir, workspaceconfig.ConventionalCatalogDir), 0o755); err != nil {
		t.Fatalf("create the empty catalog folder: %v", err)
	}
	return resolveWorkspaceAt(t, wsDir)
}

// flagExamples renders formats as the terminal's remedy offers them: each behind
// the flag, the last joined by "or".
func flagExamples(formats []string) string {
	named := make([]string, 0, len(formats))
	for _, f := range formats {
		named = append(named, "--format "+f)
	}
	if len(named) == 1 {
		return "for example " + named[0]
	}
	return "for example " + strings.Join(named[:len(named)-1], ", ") + " or " + named[len(named)-1]
}

// TestADirectoryNoImporterRecognisesIsOfferedWhatCanHelpIt imports, naming no
// format, a directory holding only a Langflow export, and a lone file no
// signature matches, over the core importers alone.
//
// The directory is refused under the unrecognised-import-format condition, and its
// remedy is the directory's: import one of its files on its own, or name a format
// whose importer reads a directory, the formats offered being exactly those
// advertised by an importer whose signatures declare file patterns — never
// langflow, whose importer declares none. The file keeps the file's remedy, naming
// every format the importers advertise.
//
// 🔴 PLANTED DEFECT: in (*UndetectedFormatError).occurrence (detect.go), write
// `if false && e.Directory {`, so a directory is refused in the file's words: the
// directory row reddens on its sentence. In detectFormat, write `if true {` for
// `if declaresFilePatterns(importer) {`, so the offered formats stop being read off
// the declarations: the directory row reddens on the formats it offers.
func TestADirectoryNoImporterRecognisesIsOfferedWhatCanHelpIt(t *testing.T) {
	t.Parallel()
	rw := aWorkspaceWithAnEmptyCatalogFolder(t)
	readers, advertised := formatsDeclaringFilePatterns(t, rw)
	if len(readers) == 0 {
		t.Fatal("no core importer declares a file pattern, so nothing here can show which formats are offered")
	}
	if !slices.Contains(advertised, "langflow") || slices.Contains(readers, "langflow") {
		t.Fatalf("the case rests on the langflow format being advertised by an importer declaring no file pattern; "+
			"advertised %v, declaring patterns %v", advertised, readers)
	}
	export, err := os.ReadFile(langflowExport)
	if err != nil {
		t.Fatalf("read the committed Langflow export: %v", err)
	}

	t.Run("a directory holding a Langflow export", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "flows")
		importopstest.WriteSource(t, filepath.Join(dir, "flow.json"), string(export))

		_, err := importDirectory(t, rw, dir, "flows", "")
		var undetected *UndetectedFormatError
		if !errors.As(err, &undetected) {
			t.Fatalf("the directory was not refused as unrecognised: %T: %v", err, err)
		}
		if !errors.Is(err, ErrUnrecognisedImportFormat()) {
			t.Errorf("the refusal is not the unrecognised-import-format condition: %v", err)
		}
		if short, _ := cerrors.ShortCodeOf(err); short != "E0350" {
			t.Errorf("the refusal carries short code %q, want E0350", short)
		}
		if !undetected.IsDirectory() {
			t.Errorf("the refusal does not say its source is a directory")
		}
		if got := undetected.DirectoryFormats(); !slices.Equal(got, readers) {
			t.Errorf("the refusal offers the formats %v; want exactly those whose importers declare file patterns, %v",
				got, readers)
		}
		want := "no importer recognises the source (directory " + strconv.Quote("flows") + "). Import one of the " +
			"directory's files on its own, or pass --format naming a format whose importer reads a directory through " +
			"the file patterns it declares (" + flagExamples(readers) + ")"
		if undetected.Error() != want {
			t.Errorf("the directory's refusal reads\n  %q\nwant\n  %q", undetected.Error(), want)
		}
	})

	t.Run("a file no signature matches", func(t *testing.T) {
		t.Parallel()
		file := importopstest.WriteSource(t, filepath.Join(t.TempDir(), "notes.json"), `{"unrelated": true}`)

		_, err := importDirectory(t, rw, file, "notes.json", "")
		var undetected *UndetectedFormatError
		if !errors.As(err, &undetected) {
			t.Fatalf("the file was not refused as unrecognised: %T: %v", err, err)
		}
		if undetected.IsDirectory() {
			t.Errorf("the refusal says a file is a directory")
		}
		want := "no importer recognises the source (source " + strconv.Quote("notes.json") + "). Pass --format to " +
			"name it explicitly (" + flagExamples(advertised) + ")"
		if undetected.Error() != want {
			t.Errorf("the file's refusal reads\n  %q\nwant\n  %q", undetected.Error(), want)
		}
	})
}

// TestADirectoryWhoseImportersDeclareNoFilePatternIsOfferedItsFilesAlone holds
// the directory's remedy where no importer the import reads declares a file
// pattern: there is no format to offer, so the refusal says so and offers only
// importing one of the directory's files on its own.
func TestADirectoryWhoseImportersDeclareNoFilePatternIsOfferedItsFilesAlone(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "flows")
	importopstest.WriteSource(t, filepath.Join(dir, "a.probe.json"), importopstest.FilesProbeBody("a"))
	seam, _ := detectionSeamOver(t, importerDeclaring("anyname", []string{"anyname"}))

	_, err := detectFormat(context.Background(), readDirectorySource(t, dir, ""), seam)
	var undetected *UndetectedFormatError
	if !errors.As(err, &undetected) {
		t.Fatalf("the directory was not refused as unrecognised: %v", err)
	}
	if len(undetected.DirectoryFormats()) != 0 {
		t.Errorf("the refusal offers %v, though no importer declares a file pattern", undetected.DirectoryFormats())
	}
	want := `no importer recognises the source (directory "flows"). Import one of the directory's files on its own; ` +
		`no importer in the workspace's catalog declares the file patterns a directory is read through`
	if undetected.Error() != want {
		t.Errorf("the refusal reads\n  %q\nwant\n  %q", undetected.Error(), want)
	}
}
