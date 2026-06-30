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
// plain-YAML entity (Component / Feature / Provider / Aggregate).
func IsTarGzBundle(b []byte) bool {
	return len(b) >= 2 && b[0] == 0x1f && b[1] == 0x8b
}

// EntityYAMLFromBundle extracts the entity YAML — the root `recipe.yaml` anchor —
// from a Recipe / FeatureRecipe tar.gz bundle produced by Bundle. The bundle
// carries the recipe's per-version directory (the entity file plus any sibling
// source files); the entity file is the root `recipe.yaml`. It is the inverse of
// Bundle's tar.gz arm, used to fold a registry-pulled recipe into the catalog
// (the catalog holds the parsed recipe; the lockfile-drift pin stays over the
// whole bundle blob). Returns an error when the bundle has no root recipe.yaml.
func EntityYAMLFromBundle(blob []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(blob))
	if err != nil {
		return nil, fmt.Errorf("oci: unbundle gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, nerr := tr.Next()
		if nerr == io.EOF {
			break
		}
		if nerr != nil {
			return nil, fmt.Errorf("oci: unbundle tar: %w", nerr)
		}
		if hdr.Name == "recipe.yaml" {
			data, rerr := io.ReadAll(tr)
			if rerr != nil {
				return nil, fmt.Errorf("oci: unbundle read recipe.yaml: %w", rerr)
			}
			return data, nil
		}
	}
	return nil, fmt.Errorf("oci: bundle has no recipe.yaml entity anchor")
}

// Bundle reads the entity at entityPath and returns the bundled
// bytes plus the canonical OCI mediatype for that kind.
//
// Per-kind dispatch (matching the kind strings pkg/catalog/catalog.go
// switches on):
//
//   - Component / Feature / Provider / Aggregate / SchemaLibrary /
//     MappingLibrary / Plugin → bare YAML, no archive; bytes are
//     the file contents unchanged.
//   - Recipe / FeatureRecipe → tar.gz containing the YAML plus any
//     co-located source: directories, $import:-referenced files in
//     the same directory tree, and inline artifacts: payloads. File
//     entries within the archive are sorted lexicographically and
//     carry zeroed timestamps so two independent Bundle invocations
//     against the same input produce byte-identical archives.
//
// For every publishable kind that sits in a per-version subdirectory,
// Bundle first invokes validateEntityVersionPathSubdir on the
// entityPath; a mismatch between the path's version segment (the entity
// file's parent directory) and the entity's metadata.version returns
// ErrEntityVersionPathMismatch wrapped with both values. Plugin is
// the exception: it has no per-version subdirectory — its version comes
// from metadata.version and its repository location is the socket-path
// (wasm/<socket>/<name>) — so the per-version guard does not apply to it.
func Bundle(kind, entityPath string) ([]byte, string, error) {
	mediaType, err := MediaTypeForKind(kind)
	if err != nil {
		return nil, "", err
	}

	// Every publishable kind except Plugin sits in a per-version
	// subdirectory whose name equals the entity's metadata.version. The
	// entity file (component.yaml, recipe.yaml, library.yaml, …) is the
	// parent-dir anchor for those kinds. Plugin authoring carries no
	// version subdir, so it is excluded from the guard.
	if kind != "Plugin" {
		if err := validateEntityVersionPathSubdir(entityPath); err != nil {
			return nil, "", err
		}
	}

	switch kind {
	case "Component", "Feature", "Provider", "Aggregate", "SchemaLibrary", "MappingLibrary", "Plugin":
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
		// for the documented kind set. Maintenance note: a new
		// publishable kind needs both a kindMediaTypes entry and a
		// matching case here. Falling through to the default would
		// silently bundle the new kind under whichever shape its
		// dispatch branch picks; explicit cases prevent that drift.
		return nil, "", ErrUnknownEntityKind
	}
}

// validateEntityVersionPathSubdir enforces the catalog-side authoring
// discipline for every publishable entity kind:
//
//	<kind>/<namespace>/<name>/<version>/<entity>.yaml
//
// where the path's <version> segment (the parent directory of the
// entity file — component.yaml, recipe.yaml, library.yaml, …) MUST
// equal the entity's metadata.version field. Mismatch returns
// ErrEntityVersionPathMismatch wrapped with both values so the operator
// can correct the path or the metadata as appropriate.
func validateEntityVersionPathSubdir(entityPath string) error {
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
			entityPath, parentVersion, envelope.Metadata.Version, ErrEntityVersionPathMismatch)
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
