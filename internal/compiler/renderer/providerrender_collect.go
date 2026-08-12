// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// collectRenderedFiles collects a Provider guest's per-file serialized bytes
// into the map[string][]byte shape [Provider.Render] returns, keyed by path. It
// is a collector, not a serializer: the guest returns each file's serialized
// bytes ([RenderedFile.Content]) and the host writes them verbatim, so the
// on-disk output is byte-identical to what the guest emitted. Determinism is
// the guest's responsibility: identical input must produce identical bytes.
//
// A duplicate path across two files is a guest contract violation the host
// refuses rather than silently letting the second file clobber the first.
func collectRenderedFiles(files []RenderedFile) (map[string][]byte, error) {
	out := make(map[string][]byte, len(files))
	for _, f := range files {
		if _, dup := out[f.Path]; dup {
			return nil, cerrors.New(cerrors.CatalogError, cerrors.SourceLocation{File: f.Path},
				fmt.Sprintf("provider render output declares the path %q more than once", f.Path), nil, nil)
		}
		out[f.Path] = f.Content
	}
	return out, nil
}
