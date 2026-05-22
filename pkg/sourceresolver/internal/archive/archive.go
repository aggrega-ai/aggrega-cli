// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package archive provides a strict deterministic .tar.gz writer and
// a sha256 digest helper used by every source-resolver adapter that
// creates an archive (compose-https-fragment, terraform-module-git,
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
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Entry is one file in the archive, identified by its relative path
// (forward-slash separated, never absolute) and the bytes to write.
// Mode is intentionally absent: WriteTarGz enforces a fixed mode
// (0644 for files, 0755 for any parent directories synthesised from
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
// synthesised as separate tar entries: the contract is "one tar
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
	sum := sha256.Sum256(archiveBytes)
	return fmt.Sprintf("sha256:%x", sum[:])
}

// WalkDirectory walks a filesystem directory rooted at rootPath and
// returns an Entry slice ready to feed into WriteTarGz. The
// resulting RelPaths are forward-slash separated relative paths from
// rootPath; the root directory itself is not included as an entry.
// Symbolic links are followed; files unreadable by the current
// process surface as errors. Returned entries are unsorted —
// WriteTarGz sorts them.
func WalkDirectory(rootPath string) ([]Entry, error) {
	var entries []Entry
	root := filepath.Clean(rootPath)
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if info.IsDir() {
			entries = append(entries, Entry{RelPath: rel + "/"})
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("archive: read %s: %w", path, err)
		}
		entries = append(entries, Entry{RelPath: rel, Data: data})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// zeroTime returns the canonical Unix epoch used for every header
// (1970-01-01 UTC). The fixed timestamp is load-bearing for the
// determinism contract — varying mtimes are the most common source
// of tar non-determinism.
func zeroTime() time.Time { return time.Unix(0, 0).UTC() }
