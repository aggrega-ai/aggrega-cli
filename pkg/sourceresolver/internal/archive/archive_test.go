// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package archive_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/internal/archive"
)

func TestWriteTarGzDeterministic(t *testing.T) {
	t.Parallel()
	entries := []archive.Entry{
		{RelPath: "b.yaml", Data: []byte("two\n")},
		{RelPath: "a.yaml", Data: []byte("one\n")},
		{RelPath: "c.yaml", Data: []byte("three\n")},
	}
	var buf1, buf2 bytes.Buffer
	if err := archive.WriteTarGz(&buf1, entries); err != nil {
		t.Fatalf("WriteTarGz: %v", err)
	}
	if err := archive.WriteTarGz(&buf2, entries); err != nil {
		t.Fatalf("WriteTarGz: %v", err)
	}
	if !bytes.Equal(buf1.Bytes(), buf2.Bytes()) {
		t.Errorf("two WriteTarGz invocations produced different bytes; len1=%d len2=%d",
			buf1.Len(), buf2.Len())
	}
}

func TestWriteTarGzAlphabeticalOrder(t *testing.T) {
	t.Parallel()
	entries := []archive.Entry{
		{RelPath: "z.yaml", Data: []byte("z\n")},
		{RelPath: "a.yaml", Data: []byte("a\n")},
		{RelPath: "m.yaml", Data: []byte("m\n")},
	}
	var buf bytes.Buffer
	if err := archive.WriteTarGz(&buf, entries); err != nil {
		t.Fatalf("WriteTarGz: %v", err)
	}
	got := readTarGzNames(t, buf.Bytes())
	want := []string{"a.yaml", "m.yaml", "z.yaml"}
	if !equalStrings(got, want) {
		t.Errorf("entry order = %v; want alphabetical %v", got, want)
	}
}

func TestWriteTarGzFixedModeBits(t *testing.T) {
	t.Parallel()
	entries := []archive.Entry{
		{RelPath: "dir/", Data: nil},
		{RelPath: "dir/file.yaml", Data: []byte("body\n")},
	}
	var buf bytes.Buffer
	if err := archive.WriteTarGz(&buf, entries); err != nil {
		t.Fatalf("WriteTarGz: %v", err)
	}
	headers := readTarGzHeaders(t, buf.Bytes())
	for _, h := range headers {
		switch h.Typeflag {
		case tar.TypeReg:
			if h.Mode != 0o644 {
				t.Errorf("file %s mode = %o; want 0644", h.Name, h.Mode)
			}
		case tar.TypeDir:
			if h.Mode != 0o755 {
				t.Errorf("dir %s mode = %o; want 0755", h.Name, h.Mode)
			}
		}
		if h.Uid != 0 || h.Gid != 0 {
			t.Errorf("%s uid/gid = %d/%d; want 0/0", h.Name, h.Uid, h.Gid)
		}
		if !h.ModTime.Equal(epoch()) {
			t.Errorf("%s mtime = %v; want unix-epoch", h.Name, h.ModTime)
		}
	}
}

func TestWriteTarGzRejectsAbsolutePath(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	err := archive.WriteTarGz(&buf, []archive.Entry{{RelPath: "/absolute/path.yaml", Data: []byte("x")}})
	if err == nil {
		t.Fatal("expected error on absolute path; got nil")
	}
	if !strings.Contains(err.Error(), "relative") {
		t.Errorf("error should mention relative-path requirement; got %v", err)
	}
}

func TestDigestStablePrefix(t *testing.T) {
	t.Parallel()
	d := archive.Digest([]byte("hello\n"))
	if !strings.HasPrefix(d, "sha256:") {
		t.Errorf("digest must start with sha256: prefix; got %q", d)
	}
	if len(d) != len("sha256:")+64 {
		t.Errorf("digest hex length = %d; want 64 (256-bit)", len(d)-len("sha256:"))
	}
	if archive.Digest([]byte("hello\n")) != d {
		t.Errorf("Digest must be deterministic; got two different results")
	}
}

func TestWalkDirectoryRoundTrip(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	must(t, os.WriteFile(filepath.Join(root, "a.yaml"), []byte("a\n"), 0o644))
	must(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, "sub", "b.yaml"), []byte("b\n"), 0o644))

	entries, err := archive.WalkDirectory(root, root)
	if err != nil {
		t.Fatalf("WalkDirectory: %v", err)
	}
	names := make(map[string]bool, len(entries))
	for _, e := range entries {
		names[e.RelPath] = true
	}
	wantNames := []string{"a.yaml", "sub/", "sub/b.yaml"}
	for _, w := range wantNames {
		if !names[w] {
			t.Errorf("missing entry %q; got %v", w, names)
		}
	}
}

func readTarGzNames(t *testing.T, b []byte) []string {
	t.Helper()
	headers := readTarGzHeaders(t, b)
	out := make([]string, len(headers))
	for i, h := range headers {
		out[i] = h.Name
	}
	return out
}

func readTarGzHeaders(t *testing.T, b []byte) []*tar.Header {
	t.Helper()
	gr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	var headers []*tar.Header
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar.Next: %v", err)
		}
		headers = append(headers, h)
	}
	return headers
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func epoch() time.Time { return time.Unix(0, 0).UTC() }
