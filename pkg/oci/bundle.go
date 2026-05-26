// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Bundle reads the entity at entityPath and returns the bundled
// bytes plus the canonical OCI mediatype for that kind.
//
// Per-kind dispatch (matching the kind strings pkg/catalog/catalog.go
// switches on):
//
//   - Component / Feature / Provider / SchemaLibrary / MappingLibrary
//     → bare YAML, no archive; bytes are the file contents unchanged.
//   - Recipe / FeatureRecipe → tar.gz containing the YAML plus any
//     co-located source: directories, $import:-referenced files in
//     the same directory tree, and inline artifacts: payloads. File
//     entries within the archive are sorted lexicographically and
//     carry zeroed timestamps so two independent Bundle invocations
//     against the same input produce byte-identical archives.
//
// Aggregate is NOT a bundled kind (Aggregates live in the workspace's
// source-control system, not as OCI-published catalog content);
// passing kind="Aggregate" returns ErrUnknownEntityKind.
//
// For libraries (SchemaLibrary, MappingLibrary), Bundle first invokes
// validateLibraryPerVersionSubdir on the entityPath; mismatch
// between the path's version segment and the entity's metadata.version
// returns ErrLibraryVersionMismatch wrapped with both values.
func Bundle(kind, entityPath string) ([]byte, string, error) {
	mediaType, err := MediaTypeForKind(kind)
	if err != nil {
		return nil, "", err
	}

	switch kind {
	case "Component", "Feature", "Provider":
		data, err := os.ReadFile(entityPath)
		if err != nil {
			return nil, "", fmt.Errorf("oci: bundle read %s: %w", entityPath, err)
		}
		return data, mediaType, nil

	case "SchemaLibrary", "MappingLibrary":
		if err := validateLibraryPerVersionSubdir(entityPath); err != nil {
			return nil, "", err
		}
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
		// MediaTypeForKind at the top of Bundle filters unknown
		// kinds before this switch runs, so default is unreachable
		// for the documented kind set. Maintenance note: if the
		// mediatype registry grows to include a new kind (e.g., if
		// Aggregate ever becomes OCI-publishable), add a matching
		// case here. Falling through to the default would silently
		// bundle the new kind under whichever shape its dispatch
		// branch picks; explicit cases prevent that drift.
		return nil, "", ErrUnknownEntityKind
	}
}

// validateLibraryPerVersionSubdir enforces the catalog-side authoring
// discipline for SchemaLibrary and MappingLibrary entities:
//
//	<kind>/<namespace>/<name>/<version>/library.yaml
//
// where the path's <version> segment (the parent directory of the
// .yaml file) MUST equal the entity's metadata.version field.
// Mismatch returns ErrLibraryVersionMismatch wrapped with both
// values so the operator can correct the path or the metadata as
// appropriate.
func validateLibraryPerVersionSubdir(entityPath string) error {
	parentVersion := filepath.Base(filepath.Dir(entityPath))

	data, err := os.ReadFile(entityPath)
	if err != nil {
		return fmt.Errorf("oci: read library %s for per-version validation: %w", entityPath, err)
	}

	var envelope struct {
		Metadata struct {
			Version string `yaml:"version"`
		} `yaml:"metadata"`
	}
	if err := yaml.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("oci: parse library %s for per-version validation: %w", entityPath, err)
	}

	if envelope.Metadata.Version != parentVersion {
		return fmt.Errorf("oci: %s: path version %q != metadata.version %q: %w",
			entityPath, parentVersion, envelope.Metadata.Version, ErrLibraryVersionMismatch)
	}
	return nil
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

