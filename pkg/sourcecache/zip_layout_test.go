// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package sourcecache_test

import (
	"archive/zip"
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/sourcecache"
)

// zipMember is one entry of a test zip archive. A name ending in a slash is a
// directory entry; a non-zero mode overrides the entry's permission bits and file
// type.
type zipMember struct {
	name string
	body string
	mode fs.FileMode
}

// buildZip writes a zip archive holding members in the order given, each stored as
// its own entry, and returns the bytes.
func buildZip(t *testing.T, members ...zipMember) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, m := range members {
		hdr := &zip.FileHeader{Name: m.name, Method: zip.Store}
		if m.mode != 0 {
			hdr.SetMode(m.mode)
		}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatalf("zip header %q: %v", m.name, err)
		}
		if _, err := w.Write([]byte(m.body)); err != nil {
			t.Fatalf("zip body %q: %v", m.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// treeListing returns every path beneath dir, slash-separated and sorted.
func treeListing(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == dir {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	slices.Sort(out)
	return out
}

func zipSlot(t *testing.T) (root, slot string) {
	t.Helper()
	root = t.TempDir()
	slot = filepath.Join(root, ".aggrega", "cache", "slot")
	return root, slot
}

func TestRegenerateExtractedZipLaysOutFilesAndDirectories(t *testing.T) {
	t.Parallel()
	root, slot := zipSlot(t)
	archive := buildZip(t,
		zipMember{name: "main.tf", body: "resource {}\n"},
		zipMember{name: "modules/"},
		zipMember{name: "modules/vpc/main.tf", body: "variable {}\n"},
	)
	tree, err := sourcecache.RegenerateExtractedZip(root, ".aggrega/cache", slot, archive)
	if err != nil {
		t.Fatalf("RegenerateExtractedZip: %v", err)
	}
	want := []string{"main.tf", "modules", "modules/vpc", "modules/vpc/main.tf"}
	if got := treeListing(t, tree); !slices.Equal(got, want) {
		t.Fatalf("tree = %v; want %v", got, want)
	}
	for name, body := range map[string]string{"main.tf": "resource {}\n", "modules/vpc/main.tf": "variable {}\n"} {
		got, err := os.ReadFile(filepath.Join(tree, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if string(got) != body {
			t.Errorf("%s = %q; want %q", name, got, body)
		}
		if runtime.GOOS != "windows" {
			fi, err := os.Stat(filepath.Join(tree, filepath.FromSlash(name)))
			if err != nil {
				t.Fatalf("stat %s: %v", name, err)
			}
			if fi.Mode().Perm() != 0o644 {
				t.Errorf("%s mode = %v; want 0644", name, fi.Mode().Perm())
			}
		}
	}
}

func TestRegenerateExtractedZipRefusesAnEscapingEntry(t *testing.T) {
	t.Parallel()
	root, slot := zipSlot(t)
	archive := buildZip(t,
		zipMember{name: "main.tf", body: "resource {}\n"},
		zipMember{name: "../x.tf", body: "escape\n"},
	)
	_, err := sourcecache.RegenerateExtractedZip(root, ".aggrega/cache", slot, archive)
	if !errors.Is(err, sourcecache.ErrFetchIntegrity()) {
		t.Fatalf("escaping entry: err = %v; want ErrFetchIntegrity", err)
	}
	if _, serr := os.Stat(sourcecache.ExtractedDir(slot)); !errors.Is(serr, fs.ErrNotExist) {
		t.Errorf("a refused archive left %s behind (stat err = %v)", sourcecache.ExtractedDir(slot), serr)
	}
	if _, serr := os.Stat(filepath.Join(slot, "..", "x.tf")); !errors.Is(serr, fs.ErrNotExist) {
		t.Errorf("the escaping entry was written (stat err = %v)", serr)
	}
}

func TestRegenerateExtractedZipLaysOutNoLinkEntry(t *testing.T) {
	t.Parallel()
	root, slot := zipSlot(t)
	archive := buildZip(t,
		zipMember{name: "main.tf", body: "resource {}\n"},
		zipMember{name: "link.tf", body: "main.tf", mode: fs.ModeSymlink | 0o777},
		zipMember{name: "outputs.tf", body: "output {}\n"},
	)
	tree, err := sourcecache.RegenerateExtractedZip(root, ".aggrega/cache", slot, archive)
	if err != nil {
		t.Fatalf("RegenerateExtractedZip: %v", err)
	}
	want := []string{"main.tf", "outputs.tf"}
	if got := treeListing(t, tree); !slices.Equal(got, want) {
		t.Fatalf("tree = %v; want %v", got, want)
	}
}

func TestCheckZipLayoutFindsAnImpliedDirectory(t *testing.T) {
	t.Parallel()
	archive := buildZip(t, zipMember{name: "modules/vpc/main.tf", body: "variable {}\n"})
	if err := sourcecache.CheckZipLayout(archive, "modules/vpc"); err != nil {
		t.Fatalf("an implied directory: err = %v; want nil", err)
	}
	if err := sourcecache.CheckZipLayout(archive, "modules"); err != nil {
		t.Fatalf("an implied parent directory: err = %v; want nil", err)
	}
	if err := sourcecache.CheckZipLayout(archive, ""); err != nil {
		t.Fatalf("no module_subdir: err = %v; want nil", err)
	}
}

func TestCheckZipLayoutRefusesAnAbsentSubdirectory(t *testing.T) {
	t.Parallel()
	archive := buildZip(t,
		zipMember{name: "main.tf", body: "resource {}\n"},
		zipMember{name: "modules/vpc/main.tf", body: "variable {}\n"},
	)
	for _, subdir := range []string{"modules/none", "main.tf"} {
		err := sourcecache.CheckZipLayout(archive, subdir)
		if !errors.Is(err, sourcecache.ErrFetchNotFound()) {
			t.Errorf("module_subdir %q: err = %v; want ErrFetchNotFound", subdir, err)
			continue
		}
		if !strings.Contains(err.Error(), `"`+subdir+`"`) {
			t.Errorf("module_subdir %q: error %q does not name the value", subdir, err)
		}
	}
}

func TestCheckZipLayoutRefusesACorruptArchive(t *testing.T) {
	t.Parallel()
	err := sourcecache.CheckZipLayout([]byte("not a zip"), "")
	if !errors.Is(err, sourcecache.ErrFetchIntegrity()) {
		t.Fatalf("corrupt archive: err = %v; want ErrFetchIntegrity", err)
	}
}

func TestCheckZipLayoutRefusesAnEscapingEntry(t *testing.T) {
	t.Parallel()
	archive := buildZip(t, zipMember{name: "../x.tf", body: "escape\n"})
	err := sourcecache.CheckZipLayout(archive, "")
	if !errors.Is(err, sourcecache.ErrFetchIntegrity()) {
		t.Fatalf("escaping entry: err = %v; want ErrFetchIntegrity", err)
	}
}

func TestCheckZipLayoutRefusesAFileAndDirectorySharingAPlace(t *testing.T) {
	t.Parallel()
	archive := buildZip(t,
		zipMember{name: "a", body: "file\n"},
		zipMember{name: "a/b", body: "nested\n"},
	)
	err := sourcecache.CheckZipLayout(archive, "")
	if !errors.Is(err, sourcecache.ErrFetchIntegrity()) {
		t.Fatalf("file and directory in one place: err = %v; want ErrFetchIntegrity", err)
	}
	if !strings.Contains(err.Error(), `"a"`) {
		t.Errorf("error %q does not name the conflicting path", err)
	}
}

// zipWithWrongChecksum is a zip package whose one entry is stored with a checksum
// its body does not have.
func zipWithWrongChecksum(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	body := []byte("resource {}\n")
	w, err := zw.CreateRaw(&zip.FileHeader{
		Name: "main.tf", Method: zip.Store, CRC32: 0xdeadbeef,
		CompressedSize64: uint64(len(body)), UncompressedSize64: uint64(len(body)),
	})
	if err != nil {
		t.Fatalf("zip header: %v", err)
	}
	if _, err := w.Write(body); err != nil {
		t.Fatalf("zip body: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func TestCheckZipLayoutRefusesAnEntryWhoseChecksumFails(t *testing.T) {
	t.Parallel()
	err := sourcecache.CheckZipLayout(zipWithWrongChecksum(t), "")
	if !errors.Is(err, sourcecache.ErrFetchIntegrity()) {
		t.Fatalf("CheckZipLayout = %v; want ErrFetchIntegrity", err)
	}
}

func TestRegenerateExtractedZipRefusesAnEntryWhoseChecksumFails(t *testing.T) {
	t.Parallel()
	root, slot := zipSlot(t)
	_, err := sourcecache.RegenerateExtractedZip(root, ".aggrega/cache", slot, zipWithWrongChecksum(t))
	if !errors.Is(err, sourcecache.ErrFetchIntegrity()) {
		t.Fatalf("RegenerateExtractedZip = %v; want ErrFetchIntegrity", err)
	}
}
