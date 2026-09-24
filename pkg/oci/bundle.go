// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// IsTarGzBundle reports whether b begins with the gzip magic — i.e. it is a
// Recipe / FeatureRecipe tar.gz bundle (Bundle's tar.gz arm) rather than a
// plain-YAML entity (Component / Feature / Aggregate).
//
// It answers a question about the PUBLISHED SHAPE and nothing else. A consumer
// that holds bytes from a coordinate and wants the entity inside them calls
// [EntityYAML], which asks this and unwraps in one step; there is no second
// place to write that pair, because the unwrapper is unexported.
func IsTarGzBundle(b []byte) bool {
	return len(b) >= 2 && b[0] == 0x1f && b[1] == 0x8b
}

// EntityYAML returns the entity document carried by bytes published at a
// registry coordinate, whatever shape they were published in: an archive is
// unwrapped to the document inside it, and a document is returned unchanged.
//
// # Why this is the only entry, and the unwrapper is not
//
// Which kinds publish as an archive is a property of [Bundle]'s dispatch, not
// of any consumer, so a consumer that asks "is this one of those" has taken on
// a fact it cannot keep current — and every consumer that fails to ask reads an
// archive as a document, which a YAML decode answers with an empty envelope
// rather than a refusal. Routing the question through one function makes the
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
	if !IsTarGzBundle(published) {
		return published, nil
	}
	return entityYAMLFromBundle(published)
}

// entityYAMLFromBundle extracts the entity YAML from a tar.gz bundle produced by
// Bundle's archive arm. The bundle carries the entity's directory — the entity
// file plus any co-located source — and this is the inverse of that
// arm, used to fold a registry-pulled entity into the catalog (the catalog holds
// the parsed entity; the lockfile-drift pin stays over the whole bundle blob).
//
// # The entity is located by what the document IS, never by what it is called
//
// The bundle is built from a directory and no file in it is privileged, so the
// entity file's name is whatever its author chose; a name this function matched
// on would be a convention nothing imposes, and an entity file named anything
// else would bundle cleanly and never unbundle. The anchor is therefore the
// preamble: the one document at the bundle ROOT whose kind is a kind this
// transport publishes, read through MediaTypeForKind so the kind set has a
// single home rather than a second list here.
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
// documents. The last is the case that matters most: two same-version entities
// authored in one directory bundle to byte-identical archives, so each of their
// coordinates carries the same bytes, and returning either document would hand
// a consumer the other entity under the name it asked for — a substitution
// nothing downstream could detect.
func entityYAMLFromBundle(blob []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(blob))
	if err != nil {
		return nil, fmt.Errorf("oci: unbundle gzip: %v: %w", err, ErrBundleUnreadable())
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	var rootEntries []string
	var found []string
	var entity []byte
	for {
		hdr, nerr := tr.Next()
		if nerr == io.EOF {
			break
		}
		if nerr != nil {
			return nil, fmt.Errorf("oci: unbundle tar: %v: %w", nerr, ErrBundleUnreadable())
		}
		if hdr.Typeflag != tar.TypeReg || strings.Contains(hdr.Name, "/") {
			continue
		}
		rootEntries = append(rootEntries, hdr.Name)
		data, rerr := io.ReadAll(tr)
		if rerr != nil {
			return nil, fmt.Errorf("read %s from the published bundle: %v: %w", hdr.Name, rerr, ErrBundleUnreadable())
		}
		if !declaresPublishableKind(data) {
			continue
		}
		found = append(found, hdr.Name)
		if entity == nil {
			entity = data
		}
	}

	// Sorted so a diagnostic is a function of the bundle's contents rather than
	// of the order the archive happened to store them in.
	sort.Strings(rootEntries)
	sort.Strings(found)
	switch len(found) {
	case 1:
		return entity, nil
	case 0:
		return nil, fmt.Errorf("bundle root carries %s: %w",
			describeRootEntries(rootEntries), ErrBundleNoEntityDocument())
	default:
		return nil, fmt.Errorf("bundle root carries %d entity documents (%s): %w",
			len(found), strings.Join(found, ", "), ErrBundleAmbiguousEntityDocument())
	}
}

// declaresPublishableKind reports whether data is an entity document of a kind
// this transport publishes.
//
// The kind set is read from the mediatype table through MediaTypeForKind rather
// than listed here, so a kind added to that table becomes locatable inside a
// bundle without a second list to update — and a document declaring some other
// `kind:`, such as a Kubernetes manifest shipped as co-located source, is not a
// candidate.
//
// The header decode is local rather than shared with the catalog's own kind peek:
// the catalog package consumes this one, so the dependency cannot run the other
// way. What single-sourcing buys here is the kind SET, and that is what the
// MediaTypeForKind call reads.
func declaresPublishableKind(data []byte) bool {
	var hdr struct {
		Kind string `yaml:"kind"`
	}
	if err := yaml.Unmarshal(data, &hdr); err != nil {
		return false
	}
	_, err := MediaTypeForKind(hdr.Kind)
	return err == nil
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

// Bundle reads the entity at entityPath and returns the bundled
// bytes plus the canonical OCI mediatype for that kind.
//
// Per-kind dispatch (matching the kind strings pkg/catalog/catalog.go
// switches on):
//
//   - Component / Feature / Aggregate / SchemaLibrary /
//     MappingLibrary / WorkloadLibrary / ArtifactsLibrary / Plugin → bare
//     YAML, no archive; bytes are the file contents unchanged. A library
//     kind is self-contained: every fragment it publishes — a schema, a
//     mapping, a workload body, an artifact file body — is authored inline
//     in the entity, so there is no co-located source to collect and a file
//     sitting beside the entity is not part of the artifact.
//   - Recipe / FeatureRecipe → tar.gz containing the YAML plus any
//     co-located source: directories, $import:-referenced files in
//     the same directory tree, and inline artifacts: payloads. File
//     entries within the archive are sorted lexicographically and
//     carry zeroed timestamps so two independent Bundle invocations
//     against the same input produce byte-identical archives.
//
// Bundle judges nothing about where the entity sits. The name of the entity's
// directory is a catalog convention and no input to the artifact, and whether
// the directory belongs to the entity alone is decided before Bundle is reached,
// by the publication that calls it.
func Bundle(kind, entityPath string) ([]byte, string, error) {
	mediaType, err := MediaTypeForKind(kind)
	if err != nil {
		return nil, "", err
	}

	switch kind {
	case "Component", "Feature", "Aggregate", "SchemaLibrary", "MappingLibrary", "WorkloadLibrary", "ArtifactsLibrary", "Plugin":
		data, err := os.ReadFile(entityPath)
		if err != nil {
			return nil, "", fmt.Errorf("oci: bundle read %s: %w", entityPath, err)
		}
		return data, mediaType, nil

	case "Recipe", "FeatureRecipe":
		archive, err := bundleTarGzDirectory(filepath.Dir(entityPath))
		if err != nil {
			return nil, "", fmt.Errorf("oci: bundle %s as tar.gz: %w", entityPath, err)
		}
		return archive, mediaType, nil

	default:
		// A new publishable kind needs BOTH a kindMediaTypes entry and a
		// matching case above, and the two are easy to get half-right: a kind
		// with a mediatype and no case passes the gate at the top of Bundle and
		// then lands here, so it is publishable by the mediatype table and
		// unbundleable in fact. That happened once, while a comment here
		// asserted this arm was unreachable — an assertion nothing checked. It
		// is checked now: TestBundleCoversEveryMediaTypeKind bundles every kind
		// MediaTypeKinds enumerates and fails on this sentinel, so the gap reds
		// the build instead of waiting for a push. Which arm a new kind joins is
		// still a judgement — bare YAML for a self-contained entity, tar.gz for
		// one whose directory carries co-located source.
		return nil, "", ErrUnknownEntityKind()
	}
}

// bundleTarGzDirectory walks dirPath (a Recipe / FeatureRecipe
// directory) and returns the contents packed into a deterministic
// tar.gz archive. Every non-hidden file reachable from dirPath is
// included: the top-level YAML the
// directory is named for, plus any co-located source/, mappings/,
// artifacts/ subtrees. The archive entries are sorted
// lexicographically by their relative path; every entry carries a
// zeroed modification time so two independent invocations against
// the same input produce byte-identical archive bytes.
//
// Hidden files (those whose basename starts with ".") are skipped —
// they're typically editor temp files, .gitignore, or .DS_Store
// entries that the operator does not intend to publish.
func bundleTarGzDirectory(dirPath string) ([]byte, error) {
	entries, err := collectArchiveEntries(dirPath)
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
			Mode: int64(e.mode.Perm()),
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

func collectArchiveEntries(dirPath string) ([]archiveEntry, error) {
	var entries []archiveEntry
	err := filepath.WalkDir(dirPath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		rel, err := filepath.Rel(dirPath, path)
		if err != nil {
			return fmt.Errorf("oci: rel path %s: %w", path, err)
		}
		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("oci: stat %s: %w", path, err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("oci: read %s: %w", path, err)
		}
		entries = append(entries, archiveEntry{
			relPath: filepath.ToSlash(rel),
			mode:    info.Mode(),
			data:    data,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}
