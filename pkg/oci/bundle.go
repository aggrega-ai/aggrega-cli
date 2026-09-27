// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/sourcewalk"
)

// IsTarGzBundle reports whether b begins with the gzip magic — i.e. it is the
// archive Bundle packs for an entity that carries files beside its document,
// rather than an entity document published on its own.
//
// It answers a question about the PUBLISHED SHAPE and nothing else. A consumer
// that holds bytes from a coordinate and wants the entity inside them calls
// [EntityYAML], or [EntityFile] for the file name as well, which ask this and
// unwrap in one step; there is no second place to write that pair, because the
// unwrapper is unexported.
func IsTarGzBundle(b []byte) bool {
	return len(b) >= 2 && b[0] == 0x1f && b[1] == 0x8b
}

// EntityYAML returns the entity document carried by bytes published at a
// registry coordinate, whatever shape they were published in: an archive is
// unwrapped to the document inside it, and a document is returned unchanged.
//
// # Why this is the only entry, and the unwrapper is not
//
// Whether an entity publishes as an archive is decided by what the entity
// declares when it is published, not by its kind and not by any consumer, so a
// consumer that tries to predict it has taken on a fact it cannot know — and
// every consumer that fails to ask reads an archive as a document, which a YAML
// decode answers with an empty envelope rather than a refusal. Routing the question through one function makes the
// answer a property of this package: a caller states what it HAS (bytes from a
// coordinate) and is handed what it NEEDS, with no shape test of its own.
//
// The blob stays the caller's to keep. A content pin commits to the whole
// published artifact, so a consumer that both pins and parses holds the bytes
// for the pin and passes them through here for the parse; this returns the
// document and never rewrites what the caller pinned.
//
// An archive that does not unpack returns [ErrBundleUnreadable], one carrying no
// locatable entity document returns [ErrBundleNoEntityDocument], and one
// carrying more than one returns [ErrBundleAmbiguousEntityDocument] — never an
// empty document, because a consumer cannot tell an empty answer from an absent
// one.
func EntityYAML(published []byte) ([]byte, error) {
	_, document, err := EntityFile(published)
	return document, err
}

// EntityFile returns the entity document carried by bytes published at a
// registry coordinate together with the name of the file that document is once
// those bytes are laid out in a directory: the entity's own file name at the
// root of an archive, and SingleDocumentFileName for a document published on
// its own, which is then the whole of what the coordinate carries.
//
// It is what a consumer reads that lays a published entity out on disk and hands
// the entity's parse the document's path there, so a path the entity declares
// relative to its own document resolves inside what was published — exactly as
// it resolves beside the entity in a catalog directory. The document is located
// by the same rule [EntityYAML] applies and fails in the same named ways, so the
// two can never disagree about which file an archive's entity is.
func EntityFile(published []byte) (string, []byte, error) {
	if !IsTarGzBundle(published) {
		return SingleDocumentFileName, published, nil
	}
	return entityFileInBundle(published)
}

// SingleDocumentFileName is the file a document published on its own is laid
// out as: the name a generic registry client writes such a publication under,
// which is the title every single-document publication is stamped with.
const SingleDocumentFileName = titleStemEntity + ".yaml"

// entityFileInBundle extracts the entity YAML, and the name it is carried under,
// from a tar.gz bundle produced by Bundle for an entity carrying files beside its
// document. The bundle carries the entity file plus the files the entity
// declares, at their places in its directory, and this is the inverse of that
// packing, used to fold a registry-pulled entity into the catalog (the catalog
// holds the parsed entity; the lockfile-drift pin stays over the whole bundle
// blob).
//
// # The entity is located by what the document IS, never by what it is called
//
// No file in the bundle is marked as the entity's, so the entity file's name is
// whatever its author chose; a name this function matched on would be a
// convention nothing imposes, and an entity file named anything else would
// bundle cleanly and never unbundle. The anchor is therefore the preamble: the
// one document at the bundle ROOT whose kind is a kind this transport publishes,
// read through IsPublishableKind so the kind set has a single home rather than a
// second list here.
//
// The rule cannot instead be parameterised by the kind the caller is looking
// for. A consumer reads an entity's kind FROM the entity, which is inside the
// bundle, so at the moment the bundle must be opened there is no kind to
// parameterise with — the anchor has to be derivable from the artifact alone.
//
// Two bounds fall out of that and are deliberate. Only the root is considered:
// a Recipe's co-located source may carry whole entity documents of its own, and
// those are payload rather than a second anchor. And a root document that
// declares no publishable kind is not a candidate at all, which is what keeps a
// compose fragment or a Kubernetes manifest sitting beside the entity from being
// mistaken for it.
//
// # Every failure is named rather than guessed at
//
// An archive that does not unpack returns ErrBundleUnreadable, with the archive
// error that stopped it carried in the message rather than as a second wrapped
// error, so the failure keeps a single chain and whoever renders it meets the
// whole sentence a caller composes around it. No candidate returns
// ErrBundleNoEntityDocument listing the root entries that were present; more
// than one returns ErrBundleAmbiguousEntityDocument naming the colliding
// documents. The last is the case that matters most: an archive packed by
// another client from a directory holding two same-version entities could sit at
// each of their coordinates as the same bytes, and returning either document
// would hand a consumer the other entity under the name it asked for — a
// substitution nothing downstream could detect.
func entityFileInBundle(blob []byte) (string, []byte, error) {
	entries, err := archiveEntries(blob)
	if err != nil {
		return "", nil, err
	}

	var rootEntries []string
	var found []string
	var entity []byte
	for _, e := range entries {
		if strings.Contains(e.relPath, "/") {
			continue
		}
		rootEntries = append(rootEntries, e.relPath)
		if !declaresPublishableKind(e.data) {
			continue
		}
		found = append(found, e.relPath)
		if entity == nil {
			entity = e.data
		}
	}

	// Sorted so a diagnostic is a function of the bundle's contents rather than
	// of the order the archive happened to store them in.
	sort.Strings(rootEntries)
	sort.Strings(found)
	switch len(found) {
	case 1:
		return found[0], entity, nil
	case 0:
		return "", nil, fmt.Errorf("bundle root carries %s: %w",
			describeRootEntries(rootEntries), ErrBundleNoEntityDocument())
	default:
		return "", nil, fmt.Errorf("bundle root carries %d entity documents (%s): %w",
			len(found), strings.Join(found, ", "), ErrBundleAmbiguousEntityDocument())
	}
}

// declaresPublishableKind reports whether data is an entity document of a kind
// this transport publishes.
//
// The kind set is read from the mediatype table through IsPublishableKind rather
// than listed here, so a kind added to that table becomes locatable inside a
// bundle without a second list to update — and a document declaring some other
// `kind:`, such as a Kubernetes manifest shipped as co-located source, is not a
// candidate.
//
// The header decode is local rather than shared with the catalog's own kind peek:
// the catalog package consumes this one, so the dependency cannot run the other
// way. What single-sourcing buys here is the kind SET, and that is what the
// IsPublishableKind call reads.
func declaresPublishableKind(data []byte) bool {
	var hdr struct {
		Kind string `yaml:"kind"`
	}
	if err := yaml.Unmarshal(data, &hdr); err != nil {
		return false
	}
	return IsPublishableKind(hdr.Kind)
}

// archiveEntries reads every regular file an archive carries, in the order the
// archive stores them: its path, its permission bits and its bytes. It is the one
// reading of a published archive's entries, so the entity located inside one and
// the digest a lockfile records for one are taken from the same entries.
//
// An archive that does not unpack returns ErrBundleUnreadable, with the archive
// error that stopped it carried in the message rather than as a second wrapped
// error, so the failure keeps a single chain.
func archiveEntries(blob []byte) ([]archiveEntry, error) {
	gz, err := gzip.NewReader(bytes.NewReader(blob))
	if err != nil {
		return nil, fmt.Errorf("unbundle gzip: %v: %w", err, ErrBundleUnreadable())
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	var entries []archiveEntry
	for {
		hdr, nerr := tr.Next()
		if nerr == io.EOF {
			break
		}
		if nerr != nil {
			return nil, fmt.Errorf("unbundle tar: %v: %w", nerr, ErrBundleUnreadable())
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		data, rerr := io.ReadAll(tr)
		if rerr != nil {
			return nil, fmt.Errorf("read %s from the published bundle: %v: %w", hdr.Name, rerr, ErrBundleUnreadable())
		}
		entries = append(entries, archiveEntry{relPath: hdr.Name, mode: os.FileMode(hdr.Mode).Perm(), data: data})
	}
	return entries, nil
}

// describeRootEntries renders the root listing carried on the no-entity refusal.
// The listing is what tells an author whether the entity document is missing
// from the artifact or merely not recognised as one, which a bare count cannot.
func describeRootEntries(names []string) string {
	if len(names) == 0 {
		return "no files at all"
	}
	return fmt.Sprintf("no entity document among %s", strings.Join(names, ", "))
}

// Bundle returns the bytes a publication of the entity at entityPath carries, and
// the media type they publish under, given carried: the files the entity declares
// beside its document, each a slash-separated path relative to the directory
// holding the document.
//
// # The shape follows what is carried, and nothing else
//
// With nothing carried the bytes are the entity document, unchanged, under the
// kind's document media type. With anything carried they are a gzipped tar
// holding the document at its root under its own file name and each carried file
// at its path, under the kind's archive media type. So the kind decides only
// which pair of media types applies: an entity of any kind publishes as its
// document exactly when it declares nothing beside it, and a document published
// alone is byte-identical to the file it was authored as.
//
// # It reads only what it is named
//
// Which files an entity declares is decided by the entity's own declarations,
// read where its document is parsed, and handed in here. A file sitting in the
// entity's directory that no declaration names is never opened, so it neither
// travels nor changes the shape; and two entities sharing a directory could only
// ever carry the files each declares. Bundle judges nothing about where the entity
// sits either: the name of its directory is a catalog convention and no input to
// the artifact, and whether the directory belongs to the entity alone is decided
// before Bundle is reached, by the publication that calls it.
//
// A carried path must be local to the entity's directory — relative, cleaned, and
// not climbing out of it. The declarations that hand it in hold every content path
// inside that directory already, so one that is not is a fault in the caller and
// is refused rather than packed. The document's own name among them is packed
// once.
//
// Entries are sorted by path and carry a zeroed modification time and a mode of
// 0644, or 0755 for a file with any execute bit set. A checkout writes files by
// its own umask while version control records only whether a file is executable,
// so the mode is normalised to exactly that: two checkouts of one commit publish
// identical entries, and two invocations over the same files produce identical
// archives on one machine. The compressed bytes are not an identity across
// machines, and nothing compares them as one: what a lockfile records for a
// publication is [CarriedDigest] over its entries.
func Bundle(kind, entityPath string, carried []string) ([]byte, string, error) {
	mediaType, err := MediaTypeFor(kind, !publishesAlone(carried))
	if err != nil {
		return nil, "", err
	}
	if publishesAlone(carried) {
		data, err := os.ReadFile(entityPath)
		if err != nil {
			return nil, "", fmt.Errorf("oci: bundle read %s: %w", entityPath, err)
		}
		return data, mediaType, nil
	}
	archive, err := packArchive(entityPath, carried)
	if err != nil {
		return nil, "", fmt.Errorf("oci: bundle %s as tar.gz: %w", entityPath, err)
	}
	return archive, mediaType, nil
}

// publishesAlone reports whether an entity carrying carried beside its document
// publishes as that document alone. It is the one statement of which shape a
// publication takes, read by the bundle that packs it and by the digest a
// lockfile records for it, so the two cannot come to disagree about the shape.
func publishesAlone(carried []string) bool {
	return len(carried) == 0
}

// packArchive packs the entity document at entityPath and the carried files into
// a deterministic tar.gz archive, each at its path relative to the document's
// directory.
func packArchive(entityPath string, carried []string) ([]byte, error) {
	entries, err := collectArchiveEntries(entityPath, carried, nil)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].relPath < entries[j].relPath
	})

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	zero := time.Unix(0, 0).UTC()
	for _, e := range entries {
		hdr := &tar.Header{
			Name: e.relPath,
			Mode: int64(archiveMode(e.mode)),
			Size: int64(len(e.data)),
			// Zero modification time for deterministic output across
			// runs against the same directory contents.
			ModTime: zero,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, fmt.Errorf("oci: tar header %s: %w", e.relPath, err)
		}
		if _, err := tw.Write(e.data); err != nil {
			return nil, fmt.Errorf("oci: tar write %s: %w", e.relPath, err)
		}
	}

	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("oci: tar close: %w", err)
	}
	if err := gw.Close(); err != nil {
		return nil, fmt.Errorf("oci: gzip close: %w", err)
	}
	return buf.Bytes(), nil
}

type archiveEntry struct {
	relPath string
	mode    os.FileMode
	data    []byte
}

// collectArchiveEntries reads the entity document and every carried file, each
// once, the document first, so a packed archive is the same one whether the files
// were reached one by one or by walking their directory.
//
// A symbolic link is refused rather than read, wherever it sits among them: its
// own permission bits would be packed as the entry's mode and the bytes it points
// to — from wherever it points — under its name. The walk that names the carried
// set already refuses one; refusing here as well keeps the archive from carrying
// a link whatever list it is handed.
//
// supplied holds bytes to use in place of what is on disk, keyed by carried path:
// a caller that already holds the document it parsed, or that asks what a file
// would carry once written, names the bytes here. A supplied file keeps the mode
// it has on disk, and takes 0644 where it is not on disk yet.
func collectArchiveEntries(entityPath string, carried []string, supplied map[string][]byte) ([]archiveEntry, error) {
	dir := filepath.Dir(entityPath)
	names := append([]string{filepath.Base(entityPath)}, carried...)
	seen := make(map[string]bool, len(names))
	entries := make([]archiveEntry, 0, len(names))
	for _, rel := range names {
		if !filepath.IsLocal(filepath.FromSlash(rel)) || path.Clean(rel) != rel {
			return nil, fmt.Errorf("oci: carried path %q does not name a file inside %s", rel, dir)
		}
		if seen[rel] {
			continue
		}
		seen[rel] = true
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := sourcewalk.RefuseLink(full, rel); err != nil {
			return nil, err
		}
		if data, ok := supplied[rel]; ok {
			mode := os.FileMode(0o644)
			if info, err := os.Lstat(full); err == nil {
				mode = info.Mode()
			} else if !errors.Is(err, fs.ErrNotExist) {
				return nil, fmt.Errorf("oci: stat %s: %w", full, err)
			}
			entries = append(entries, archiveEntry{relPath: rel, mode: mode, data: data})
			continue
		}
		info, err := os.Lstat(full)
		if err != nil {
			return nil, fmt.Errorf("oci: stat %s: %w", full, err)
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return nil, fmt.Errorf("oci: read %s: %w", full, err)
		}
		entries = append(entries, archiveEntry{relPath: rel, mode: info.Mode(), data: data})
	}
	return entries, nil
}
