// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package archive provides a strict deterministic .tar.gz writer and
// a sha256 digest helper used by every source-resolver adapter that
// creates an archive (compose-https-fragment, git-repository,
// and helm-https when index-resolved). Archives produced here are
// byte-identical across runs given the same input: zero modification
// time, uid/gid 0/0, fixed mode bits (0644 files, 0755 directories),
// alphabetical entry ordering, no extended attributes, no PAX
// headers beyond what path length requires. Adapters that consume a
// pre-built upstream archive (helm-oci-chart via ORAS; helm-https
// pulling a direct .tgz URL) pin the upstream digest verbatim and
// do not normalise through this package.
package archive

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
)

// Entry is one file in the archive, identified by its relative path
// (forward-slash separated, never absolute) and the bytes to write.
// Mode is intentionally absent: WriteTarGz enforces a fixed mode
// (0644 for files, 0755 for any parent directories synthesized from
// the path) so adapters cannot accidentally introduce per-host or
// per-umask variation.
type Entry struct {
	RelPath string
	Data    []byte
}

// WriteTarGz writes entries to w as a deterministic .tar.gz archive.
// Entries are sorted lexicographically by RelPath; each receives a
// zero modification time, uid/gid 0/0, fixed mode 0644, and no
// extended attributes. Two independent invocations against the same
// entry set produce byte-identical output (verified by the
// determinism test in this package).
//
// Parent directories implied by an entry's RelPath are NOT
// synthesized as separate tar entries: the contract is "one tar
// entry per Entry," and consumers extract via go-standard tar
// readers which create missing parents themselves. Adapters that
// need explicit directory entries should construct them as Entry
// values with a trailing slash on RelPath; the writer recognises the
// trailing slash and emits a directory header with mode 0755.
//
// The function does not enforce uniqueness of RelPath across
// entries. Duplicate paths are written in sort order and the
// lexicographically-last entry "wins" on extraction (standard tar
// behavior). Callers SHOULD dedupe upstream; the test suite pins the
// duplicate disposition so future readers can rely on it.
func WriteTarGz(w io.Writer, entries []Entry) error {
	sorted := make([]Entry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].RelPath < sorted[j].RelPath })

	gz := gzip.NewWriter(w)
	gz.ModTime = zeroTime()
	gz.OS = 255

	tw := tar.NewWriter(gz)
	for _, e := range sorted {
		if err := writeEntry(tw, e); err != nil {
			_ = tw.Close()
			_ = gz.Close()
			return err
		}
	}
	if err := tw.Close(); err != nil {
		_ = gz.Close()
		return fmt.Errorf("archive: tar close: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("archive: gzip close: %w", err)
	}
	return nil
}

func writeEntry(tw *tar.Writer, e Entry) error {
	if strings.HasPrefix(e.RelPath, "/") {
		return fmt.Errorf("archive: entry path must be relative: %s", e.RelPath)
	}
	if strings.HasSuffix(e.RelPath, "/") {
		hdr := &tar.Header{
			Name:     e.RelPath,
			Mode:     0o755,
			Typeflag: tar.TypeDir,
			ModTime:  zeroTime(),
			Uid:      0, Gid: 0,
			Uname: "", Gname: "",
			Format: tar.FormatPAX,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return fmt.Errorf("archive: tar header %q: %w", e.RelPath, err)
		}
		return nil
	}
	hdr := &tar.Header{
		Name:     e.RelPath,
		Mode:     0o644,
		Size:     int64(len(e.Data)),
		Typeflag: tar.TypeReg,
		ModTime:  zeroTime(),
		Uid:      0, Gid: 0,
		Uname: "", Gname: "",
		Format: tar.FormatPAX,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("archive: tar header %q: %w", e.RelPath, err)
	}
	if _, err := tw.Write(e.Data); err != nil {
		return fmt.Errorf("archive: tar body %q: %w", e.RelPath, err)
	}
	return nil
}

// Digest returns the canonical `sha256:<hex>` digest string for the
// given archive bytes. Centralised so every adapter passes the
// identical formatting through to internal/metadata and
// internal/cacheslot; the prefix shape ("sha256:") is load-bearing
// for lockfile-entry equality and for ErrFetchIntegrity comparisons.
func Digest(archiveBytes []byte) string {
	return digest.ContentDigest(archiveBytes)
}

// WalkDirectory walks the directory dir, beneath root, and returns an
// Entry slice ready to feed into WriteTarGz. The resulting RelPaths are
// forward-slash separated relative paths from dir; dir itself is not
// included as an entry. Files unreadable by the current process
// surface as errors. Returned entries are unsorted — WriteTarGz sorts
// them.
//
// root is the tree dir was fetched as — the checkout of a repository —
// and it is a root of its own: dir itself, where it is a link, and every
// link the walk meets beneath it are judged against root
// ([readscope.CheckFetchedRead]) before anything is read through them.
// A link leading out of root is not read: the walk halts with the read
// hold's refusal, naming the link and where it leads, so nothing from
// outside the fetched tree reaches an archive. A link leading inside
// root is archived as what it leads to — a file's bytes under the
// link's name, and a directory walked where it leads, its entries named
// beneath the link ([pathscope.WalkDir]); dir itself being such a link
// is walked where it leads the same way. A directory link leading back
// into a directory the walk is already reading through it — a link
// round a loop — halts the walk, because the tree it stands for has no
// end. A link leading nowhere is read as the missing file it names, and
// fails the walk.
//
// rootName is how a refusal names root — the repository and revision the
// tree was fetched as — since root is a checkout removed once the walk's
// caller halts; a refusal names a link by its place beneath root.
func WalkDirectory(root, rootName, dir string) ([]Entry, error) {
	dir = filepath.Clean(dir)
	if err := readscope.CheckFetchedRead(root, rootName, dir); err != nil {
		return nil, err
	}
	top, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	w := &treeWalk{root: root, rootName: rootName}
	if err := w.walk(dir, "", []os.FileInfo{top}); err != nil {
		return nil, err
	}
	return w.entries, nil
}

// treeWalk is one archiving walk of a fetched tree.
type treeWalk struct {
	root     string
	rootName string
	entries  []Entry
}

// walk adds an entry for everything beneath dir, each named beneath prefix, and
// holds every link it meets to the root the tree was fetched as. entered holds the
// directories the walk is reading dir through — the one it started at and every
// directory a link led it into on the way here — so a link leading back into one of
// them is found to be a loop rather than walked without end.
func (w *treeWalk) walk(dir, prefix string, entered []os.FileInfo) error {
	return pathscope.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == dir {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = joinSlash(prefix, filepath.ToSlash(rel))
		if d.Type()&fs.ModeSymlink != 0 {
			return w.link(path, rel, entered)
		}
		if d.IsDir() {
			w.entries = append(w.entries, Entry{RelPath: rel + "/"})
			return nil
		}
		return w.file(path, rel)
	})
}

// link archives the link at path, named rel, as what it leads to, once the one
// judgement of where a path leads has found that inside the fetched tree.
func (w *treeWalk) link(path, rel string, entered []os.FileInfo) error {
	if err := readscope.CheckFetchedRead(w.root, w.rootName, path); err != nil {
		return err
	}
	target, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("archive: read %s: %w", path, err)
	}
	if !target.IsDir() {
		return w.file(path, rel)
	}
	for _, seen := range entered {
		if os.SameFile(seen, target) {
			return fmt.Errorf("archive: the link %s leads back into a directory this walk is already reading "+
				"through it, so the tree it stands for has no end to archive", path)
		}
	}
	w.entries = append(w.entries, Entry{RelPath: rel + "/"})
	return w.walk(path, rel, append(entered[:len(entered):len(entered)], target))
}

// file archives the file at path, named rel.
func (w *treeWalk) file(path, rel string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("archive: read %s: %w", path, err)
	}
	w.entries = append(w.entries, Entry{RelPath: rel, Data: data})
	return nil
}

// joinSlash joins a slash-separated name beneath prefix, which is empty at the top
// of a walk.
func joinSlash(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "/" + name
}

// zeroTime returns the canonical Unix epoch used for every header
// (1970-01-01 UTC). The fixed timestamp is load-bearing for the
// determinism contract — varying mtimes are the most common source
// of tar non-determinism.
func zeroTime() time.Time { return time.Unix(0, 0).UTC() }
