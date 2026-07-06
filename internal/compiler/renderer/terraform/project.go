// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !wasip1

package terraform

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// ProjectTerraformSource reads a Terraform module's files from the local source
// at compile time and packs them into a SourceProjection the host hands the
// terraform provider Wasm guest, so the guest reconstructs the module in memory
// and touches no filesystem. The path is the already-acquired module directory:
// a lockfile-pinned remote module is cloned, content-addressed, and extracted to
// its module subtree at lock time, and a local module is used verbatim — either
// way this projection walks a directory of module files.
//
// A directory is walked recursively in the host's deterministic order
// (filepath.WalkDir visits lexically per directory), and every regular file is
// packed as a SourceFile keyed by its module-root-relative, slash-separated
// path. No extension filter is applied: a module carries .tf, .tf.json, .tftpl,
// and other supporting files, all of which vendor verbatim. A non-directory path
// (a single source file rather than a module directory) is read as one file
// keyed by its base name, matching the flat single-file source shape other
// providers accept.
func ProjectTerraformSource(path string) (*renderer.SourceProjection, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, fmt.Sprintf("terraform: stat module source %q", path))
	}

	if !info.IsDir() {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, fmt.Sprintf("terraform: read module source file %q", path))
		}
		return &renderer.SourceProjection{
			Files: []renderer.SourceFile{{Path: filepath.Base(path), Content: data}},
		}, nil
	}

	var files []renderer.SourceFile
	walkErr := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(path, p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files = append(files, renderer.SourceFile{Path: filepath.ToSlash(rel), Content: data})
		return nil
	})
	if walkErr != nil {
		return nil, cerrors.Wrap(walkErr, cerrors.CatalogError, cerrors.SourceLocation{File: path}, fmt.Sprintf("terraform: project module source from %q", path))
	}

	return &renderer.SourceProjection{Files: files}, nil
}
