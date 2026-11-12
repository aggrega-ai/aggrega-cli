// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package docscache_test

// Writing one directory of a cached bundle out into a new directory: what is
// written, and the refusals that leave nothing behind.

import (
	"archive/tar"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/docsbundle/docsbundletest"
	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// exampleDir is the bundle directory these cases extract.
const exampleDir = "language-reference/examples/demo"

// openedBundle caches a bundle of members and opens it for reading.
func openedBundle(t *testing.T, members []docsbundletest.Member) *docscache.Bundle {
	t.Helper()
	reg := docsbundletest.NewRegistry()
	reg.Publish(t, testVersion, docsbundletest.Bundle(t, members), nil)
	root := t.TempDir()
	if _, err := docscache.ResolveAndCache(context.Background(), reg, testRef, testVersion, root, nil); err != nil {
		t.Fatalf("ResolveAndCache: %v", err)
	}
	b, err := docscache.Open(root, testRef, testVersion, admitAll)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return b
}

// demoMembers is a well-formed example beside a neighbouring one, with extra
// appended after it.
func demoMembers(extra ...docsbundletest.Member) []docsbundletest.Member {
	return append([]docsbundletest.Member{
		{Name: "language-reference/examples/", Typeflag: tar.TypeDir},
		{Name: exampleDir + "/", Typeflag: tar.TypeDir},
		{Name: exampleDir + "/README.md", Body: "# Demo\n"},
		{Name: exampleDir + "/aggregate.yaml", Body: "kind: Aggregate\n"},
		{Name: exampleDir + "/scripts/", Typeflag: tar.TypeDir},
		{Name: exampleDir + "/scripts/run.sh", Body: "#!/bin/sh\n", Mode: 0o755},
		{Name: "language-reference/examples/other/README.md", Body: "# Other\n"},
	}, extra...)
}

// entriesOf returns the names in dir, or none where it does not exist.
func entriesOf(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// TestScaffoldExampleExtractWritesTheDirectoryAndNothingElse is the positive
// control every refusal below is read against: the directory's own files, under
// a destination whose parents did not exist, with the execute bit kept, and none
// of the neighbouring example.
func TestScaffoldExampleExtractWritesTheDirectoryAndNothingElse(t *testing.T) {
	t.Parallel()
	b := openedBundle(t, demoMembers())
	dest := filepath.Join(t.TempDir(), "nested", "demo")

	written, err := b.ExtractDirectory(exampleDir, dest)
	if err != nil {
		t.Fatalf("ExtractDirectory: %v", err)
	}
	if want := []string{"README.md", "aggregate.yaml", "scripts/run.sh"}; !slices.Equal(written, want) {
		t.Errorf("wrote %v, want %v", written, want)
	}
	for name, body := range map[string]string{"README.md": "# Demo\n", "aggregate.yaml": "kind: Aggregate\n"} {
		got, err := os.ReadFile(filepath.Join(dest, name))
		if err != nil || string(got) != body {
			t.Errorf("%s holds %q (%v), want %q", name, got, err, body)
		}
	}
	info, err := os.Stat(filepath.Join(dest, "scripts", "run.sh"))
	if err != nil {
		t.Fatalf("the nested file was not written: %v", err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("an executable member was written with mode %v", info.Mode().Perm())
	}
	if got := entriesOf(t, filepath.Dir(dest)); !slices.Equal(got, []string{"demo"}) {
		t.Errorf("beside the destination stand %v, want only it: a staging directory was left behind", got)
	}
	if _, err := os.Stat(filepath.Join(dest, "other")); err == nil {
		t.Error("the neighbouring example was written too")
	}
}

// TestScaffoldExampleExtractRefusesAMemberLeavingItsPlace pins the refusal of a
// member whose name climbs — out of the bundle, out of the directory extracted,
// or from the top of the filesystem — with the integrity condition, and that
// nothing at all is written for it.
func TestScaffoldExampleExtractRefusesAMemberLeavingItsPlace(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		member string
	}{
		{"climbing out of the bundle", exampleDir + "/../../../../escape.txt"},
		{"climbing out of the directory into a neighbour", exampleDir + "/../other/planted.txt"},
		{"climbing from a directory elsewhere in the bundle", "authoring/../../escape.txt"},
		{"absolute", "/tmp/escape.txt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := openedBundle(t, demoMembers(docsbundletest.Member{Name: tc.member, Body: "planted\n"}))
			parent := t.TempDir()
			dest := filepath.Join(parent, "demo")

			_, err := b.ExtractDirectory(exampleDir, dest)
			if !errors.Is(err, oci.ErrFetchIntegrity()) {
				t.Fatalf("a member named %q answered %v, want the integrity refusal", tc.member, err)
			}
			if got := entriesOf(t, parent); len(got) != 0 {
				t.Errorf("a refused extraction left %v behind", got)
			}
		})
	}
}

// TestScaffoldExampleExtractRefusesALink pins that a link beneath the directory —
// whichever way it points — is refused rather than followed or recreated.
func TestScaffoldExampleExtractRefusesALink(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		member docsbundletest.Member
	}{
		{"symbolic, pointing out", docsbundletest.Member{Name: exampleDir + "/out", Typeflag: tar.TypeSymlink, Linkname: "../../../../.."}},
		{"symbolic, pointing inside", docsbundletest.Member{Name: exampleDir + "/again", Typeflag: tar.TypeSymlink, Linkname: "README.md"}},
		{"hard", docsbundletest.Member{Name: exampleDir + "/hard", Typeflag: tar.TypeLink, Linkname: exampleDir + "/README.md"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := openedBundle(t, demoMembers(tc.member))
			parent := t.TempDir()

			_, err := b.ExtractDirectory(exampleDir, filepath.Join(parent, "demo"))
			if !errors.Is(err, oci.ErrFetchIntegrity()) {
				t.Fatalf("a link member answered %v, want the integrity refusal", err)
			}
			if got := entriesOf(t, parent); len(got) != 0 {
				t.Errorf("a refused extraction left %v behind", got)
			}
		})
	}
}

// TestScaffoldExampleExtractRefusesAnOccupiedDestination pins that a destination
// holding anything is refused and left exactly as it was, and that an empty
// directory is not occupied.
func TestScaffoldExampleExtractRefusesAnOccupiedDestination(t *testing.T) {
	t.Parallel()
	b := openedBundle(t, demoMembers())

	occupied := t.TempDir()
	if err := os.WriteFile(filepath.Join(occupied, "mine.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := b.ExtractDirectory(exampleDir, occupied); err == nil {
		t.Fatal("a directory holding a file was written into")
	}
	if got := entriesOf(t, occupied); !slices.Equal(got, []string{"mine.txt"}) {
		t.Errorf("the occupied directory now holds %v", got)
	}

	file := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := b.ExtractDirectory(exampleDir, file); err == nil {
		t.Fatal("a file standing at the destination was replaced")
	}

	empty := t.TempDir()
	if _, err := b.ExtractDirectory(exampleDir, empty); err != nil {
		t.Fatalf("an empty directory was refused: %v", err)
	}
	if got, want := entriesOf(t, empty), []string{"README.md", "aggregate.yaml", "scripts"}; !slices.Equal(got, want) {
		t.Errorf("the empty directory now holds %v, want %v: nothing written, or a staging directory left behind", got, want)
	}
}

// TestScaffoldExampleExtractRefusesADirectoryTheBundleDoesNotCarry pins that a
// name naming nothing is the missing-page condition and writes nothing.
func TestScaffoldExampleExtractRefusesADirectoryTheBundleDoesNotCarry(t *testing.T) {
	t.Parallel()
	b := openedBundle(t, demoMembers())
	parent := t.TempDir()
	_, err := b.ExtractDirectory("language-reference/examples/absent", filepath.Join(parent, "absent"))
	if !errors.Is(err, docscache.ErrPageNotFound()) {
		t.Fatalf("answered %v, want the missing-page condition", err)
	}
	if got := entriesOf(t, parent); len(got) != 0 {
		t.Errorf("a refused extraction left %v behind", got)
	}
}
