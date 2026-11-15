// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// probeSignature is a signature selecting the files named like *.probe.json, read
// as JSON and asking for the probe's kind.
var probeSignature = types.PluginDetectSignature{
	Syntax: "json", Files: []string{"*.probe.json"},
	Match: []types.PluginPathMatch{{Path: "kind", Is: types.IsString, Equals: importopstest.FilesProbeKind}},
}

// readDirectorySource reads the directory at dir as an import of the workspace
// whose build writes to buildOutput would, failing the test where it cannot. An
// empty buildOutput leaves nothing out of the listing.
func readDirectorySource(t *testing.T, dir, buildOutput string) *Source {
	t.Helper()
	src, err := readSource(dir, "flows", buildOutput)
	if err != nil {
		t.Fatalf("read the directory source: %v", err)
	}
	if !src.directory {
		t.Fatalf("%s was not read as a directory", dir)
	}
	return src
}

// entryNames lists a source's entries by name, in the order it holds them.
func entryNames(src *Source) []string {
	names := make([]string, 0, len(src.entries))
	for _, e := range src.entries {
		names = append(names, e.name)
	}
	return names
}

// TestADirectoryIsListedByNameInByteOrder holds the listing's names and order: each
// file named by its slash-separated path inside the directory, never carrying the
// directory's own name, sorted byte by byte over the whole name — which puts a.json
// ahead of a/b.json, the reverse of the order a walk meets them in, since a walk
// enters the directory a before it reaches the file a.json beside it.
func TestADirectoryIsListedByNameInByteOrder(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "flows")
	for _, name := range []string{"z/y/x.json", "a/b.probe.json", "a.probe.json", "m.txt"} {
		importopstest.WriteSource(t, filepath.Join(dir, filepath.FromSlash(name)), "{}")
	}
	src := readDirectorySource(t, dir, "")
	if want := []string{"a.probe.json", "a/b.probe.json", "m.txt", "z/y/x.json"}; !slices.Equal(entryNames(src), want) {
		t.Fatalf("the directory is listed as %v; want %v", entryNames(src), want)
	}
	for _, e := range src.entries {
		if e.read {
			t.Errorf("listing the directory read %s; a listing opens no file", e.name)
		}
	}
}

// TestASelectionReadsWhatItsPatternsNameAndJudgesOnlyTheLinksItWouldReadThrough
// drives the directory selection over a directory holding every kind of entry the
// listing meets, one per row of what it hands on:
//
//   - a file a pattern selects, read under its own name;
//   - a link to a file a pattern selects, inside the directory, read where it leads
//     under the link's own name;
//   - a link to a directory inside the directory, never entered, so the file beneath
//     it reaches the importer once, under its real place;
//   - a link leading nowhere, passed over;
//   - a link to a file outside the directory that no pattern selects, neither judged
//     nor read, so it refuses nothing.
func TestASelectionReadsWhatItsPatternsNameAndJudgesOnlyTheLinksItWouldReadThrough(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "flows")
	outside := importopstest.WriteSource(t, filepath.Join(t.TempDir(), "secret.txt"), "secret")
	importopstest.WriteSource(t, filepath.Join(dir, "a.probe.json"), importopstest.FilesProbeBody("a"))
	importopstest.WriteSource(t, filepath.Join(dir, "kept", "b.probe.json"), importopstest.FilesProbeBody("b"))
	importopstest.WriteSource(t, filepath.Join(dir, "real", "c.probe.json"), importopstest.FilesProbeBody("c"))
	mustSymlink(t, filepath.Join(dir, "kept", "b.probe.json"), filepath.Join(dir, "linked.probe.json"))
	mustSymlink(t, filepath.Join(dir, "real"), filepath.Join(dir, "alias"))
	mustSymlink(t, filepath.Join(dir, "nowhere.probe.json"), filepath.Join(dir, "gone.probe.json"))
	mustSymlink(t, outside, filepath.Join(dir, "notes.txt"))

	src := readDirectorySource(t, dir, "")
	if slices.Contains(entryNames(src), "gone.probe.json") {
		t.Errorf("a link leading nowhere was listed: %v", entryNames(src))
	}
	importer := importerDeclaring("probe", []string{"probe"}, probeSignature)
	primary, sources, err := src.inputsFor(importer, "probe")
	if err != nil {
		t.Fatalf("the selection was refused: %v", err)
	}
	if len(primary) != 0 {
		t.Errorf("a directory handed primary bytes %q; want none", primary)
	}
	want := map[string]string{
		"a.probe.json":      importopstest.FilesProbeBody("a"),
		"kept/b.probe.json": importopstest.FilesProbeBody("b"),
		"linked.probe.json": importopstest.FilesProbeBody("b"),
		"real/c.probe.json": importopstest.FilesProbeBody("c"),
	}
	if got := sourceNames(sources); !slices.Equal(got, []string{"a.probe.json", "kept/b.probe.json",
		"linked.probe.json", "real/c.probe.json"}) {
		t.Fatalf("the selection handed on %v", got)
	}
	for _, s := range sources {
		if string(s.Bytes) != want[s.Name] {
			t.Errorf("%s carries %q; want %q", s.Name, s.Bytes, want[s.Name])
		}
	}
	for _, e := range src.entries {
		if e.name == "notes.txt" && (e.judged || e.read) {
			t.Errorf("a link to a file no pattern selects was judged (%v) or read (%v)", e.judged, e.read)
		}
	}
}

// TestASignatureNamingNoFileOpensNothingInADirectory holds recognition over a
// directory to the files a signature's patterns name: an importer whose signature
// names no file is tried against none of the directory's files, so nothing is
// parsed and nothing is read, and the directory is not recognised.
func TestASignatureNamingNoFileOpensNothingInADirectory(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "flows")
	importopstest.WriteSource(t, filepath.Join(dir, "a.probe.json"), importopstest.FilesProbeBody("a"))
	seam, counting := detectionSeamOver(t, importerDeclaring("anyname", []string{"anyname"},
		types.PluginDetectSignature{Syntax: "json", Match: []types.PluginPathMatch{{Path: "kind", Is: types.IsPresent}}}))

	src := readDirectorySource(t, dir, "")
	_, err := detectFormat(context.Background(), src, seam)
	var undetected *UndetectedFormatError
	if !errors.As(err, &undetected) {
		t.Fatalf("a directory was recognised by a signature naming no file: %v", err)
	}
	if counting.parsed["json"] != 0 {
		t.Errorf("a file of the directory was parsed %d time(s) for a signature naming no file", counting.parsed["json"])
	}
	for _, e := range src.entries {
		if e.read {
			t.Errorf("%s was read for a signature naming no file", e.name)
		}
	}
}

// TestADirectoryFileIsParsedOncePerSyntax holds a directory's recognition to
// reading and parsing each file once per syntax, however many importers' signatures
// select it: two importers whose signatures both select one file, the first asking
// for another kind, parse it once between them.
func TestADirectoryFileIsParsedOncePerSyntax(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "flows")
	importopstest.WriteSource(t, filepath.Join(dir, "a.probe.json"), importopstest.FilesProbeBody("a"))
	other := probeSignature
	other.Match = []types.PluginPathMatch{{Path: "kind", Is: types.IsString, Equals: "Other"}}
	seam, counting := detectionSeamOver(t,
		importerDeclaring("other", []string{"other"}, other),
		importerDeclaring("probe", []string{"probe"}, probeSignature))

	got, err := detectFormat(context.Background(), readDirectorySource(t, dir, ""), seam)
	if err != nil || got != "probe" {
		t.Fatalf("the directory was recognised as %q (err %v); want probe", got, err)
	}
	if counting.parsed["json"] != 1 {
		t.Errorf("the one file was parsed %d times as json; want once", counting.parsed["json"])
	}
}

// TestASourceThatCannotBeReadIsRefusedNamingThePlaceRead holds the read refusal
// every surface words by its type: a path naming nothing is refused naming the path
// and carrying the read's own error, and its text is the command line's.
func TestASourceThatCannotBeReadIsRefusedNamingThePlaceRead(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "flow.json")
	_, err := readSource(missing, "flow.json", "")
	var readErr *SourceReadError
	if !errors.As(err, &readErr) {
		t.Fatalf("a path naming nothing was not refused as a source that cannot be read: %T: %v", err, err)
	}
	if readErr.Path != missing {
		t.Errorf("the refusal names %q; want the path read, %q", readErr.Path, missing)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the refusal does not carry the read's own error: %v", err)
	}
	if want := `read source "` + missing + `": `; !strings.HasPrefix(err.Error(), want) {
		t.Errorf("the refusal reads %q; want it to open %q", err.Error(), want)
	}
}
