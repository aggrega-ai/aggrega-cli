// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/sourcewalk"
)

// A published catalog snapshot is a whole catalog tree published as one
// artifact: one gzipped tar, the layer of one manifest whose artifact type and
// layer media type are both oci.MediaTypeCatalogSnapshot, published under
// SnapshotRepository beneath a registry's location prefix. A registry that
// publishes catalog entities one by one can publish their tree this way too, and
// a reader that names no catalog folder reads it in their place — fetched, held
// to its signature, kept in the content cache and laid out as a folder there.
//
// This file is the one statement of the archive's layout. A publisher that cannot
// call this code writes the same bytes by the same rule, and a test holds it to
// the writer here:
//
//   - Every regular file beneath the tree, at any depth, is one entry, named by its
//     path relative to the tree's root with forward slashes. Nothing else is an
//     entry: no directory, no link — a tree holding a link, a device or any other
//     file that is not regular is refused rather than packed — and no header beside
//     the files' own.
//   - Entries are in ascending order of their names compared byte by byte.
//   - Each entry is a ustar header: the name, split into prefix and name at the
//     last `/` that leaves both fields large enough where it is longer than 100
//     bytes; mode 0644; owner and group 0, with no owner or group name; size; a
//     modification time of 0; type `0`; the checksum ustar defines. A name may
//     hold printable ASCII only, and one that cannot be split so is refused.
//   - Each entry's content follows its header, padded with zero bytes to a whole
//     512-byte block, and the archive ends with two zero blocks.
//   - The tar stream is gzipped with no file name and a modification time of 0.
//
// The tar stream is the archive's identity: the same tree always yields the same
// tar bytes. The compressed bytes are what a registry stores and a reader's pin
// names, and they are stable for one compressor, so republishing an unchanged
// tree with the same publisher publishes the same digest again.

// SnapshotRepository is the repository, beneath a registry's location prefix, a
// catalog snapshot is published in.
const SnapshotRepository = "catalog/snapshot"

// SnapshotLatestTag is the tag naming the newest snapshot a registry publishes.
const SnapshotLatestTag = "latest"

// SnapshotReference returns the reference of the catalog snapshot published
// beneath the registry location prefix: the registry's host, followed by the
// repository path it carries, if any.
func SnapshotReference(registryPrefix string) string {
	return registryPrefix + "/" + SnapshotRepository
}

// snapshotFileMode is the mode every entry of a snapshot carries, whatever the
// file's permission bits on disk: a checkout writes files by its own umask, so no
// mode read from disk is carried.
const snapshotFileMode = 0o644

// WriteSnapshot writes the published catalog snapshot of the catalog tree at dir
// to w: the tar stream WriteSnapshotTar writes, gzipped with no file name and a
// modification time of 0.
func WriteSnapshot(w io.Writer, dir string) error {
	gz := gzip.NewWriter(w)
	if err := WriteSnapshotTar(gz, dir); err != nil {
		return err
	}
	return gz.Close()
}

// WriteSnapshotTar writes the tar stream of the catalog snapshot of the tree at
// dir to w, by the layout this file states. It reads every regular file beneath
// dir and refuses, before writing anything, a tree holding a link or any other
// file that is not regular, and a file whose name the layout cannot carry.
//
// It is the reference the layout is held to: the same tree yields the same bytes
// whatever order the file system lists it in, and a publisher writing the
// archive by other means writes these bytes for the same tree.
func WriteSnapshotTar(w io.Writer, dir string) error {
	names, err := snapshotEntryNames(dir)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(w)
	epoch := time.Unix(0, 0)
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			return fmt.Errorf("read %s for the catalog snapshot: %w", name, err)
		}
		hdr := &tar.Header{
			Typeflag: tar.TypeReg,
			Name:     name,
			Mode:     snapshotFileMode,
			Size:     int64(len(data)),
			ModTime:  epoch,
			Format:   tar.FormatUSTAR,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return fmt.Errorf("the catalog file %s cannot be carried by a catalog snapshot: %w", name, err)
		}
		if _, err := tw.Write(data); err != nil {
			return fmt.Errorf("write %s into the catalog snapshot: %w", name, err)
		}
	}
	return tw.Close()
}

// snapshotEntryNames returns the name of every entry the snapshot of the tree at
// dir carries, in the layout's order, refusing a tree holding anything that is
// neither a directory nor a regular file, and a name holding anything but
// printable ASCII.
func snapshotEntryNames(dir string) ([]string, error) {
	var names []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
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
		rel = filepath.ToSlash(rel)
		if err := sourcewalk.RefuseLink(p, rel); err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return nil
		case !d.Type().IsRegular():
			return fmt.Errorf("the catalog tree holds %s, which is not a regular file, and a catalog snapshot carries "+
				"regular files only", rel)
		}
		for i := 0; i < len(rel); i++ {
			if c := rel[i]; c < 0x20 || c > 0x7e {
				return fmt.Errorf("the catalog file %q has a name holding a character other than printable ASCII, "+
					"which a catalog snapshot cannot carry", rel)
			}
		}
		names = append(names, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	return names, nil
}
