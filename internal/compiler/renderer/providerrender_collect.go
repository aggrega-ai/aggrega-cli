// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

// collectRenderedFiles collects a Provider guest's per-file serialized bytes
// into a map keyed by path. It is a collector, not a serializer: the guest
// returns each file's serialized bytes ([RenderedFile.Content]) and the host
// writes them verbatim, so the on-disk output is byte-identical to what the
// guest emitted. Determinism is the guest's responsibility: identical input must
// produce identical bytes.
//
// A duplicate path across two files is a guest contract violation it refuses
// rather than silently letting the second file clobber the first, and it refuses
// through [DuplicateRenderPathErr] so the wording is the one every surface that
// detects the duplicate reports.
//
// It has no path in it: nothing in a build calls it, and the shape it collects
// into is not the shape [Provider.Render] returns. The refusal above is
// therefore a property of this helper rather than one a guest can reach here,
// and the reachable surfaces are the per-node render and the assembly of a
// dispatch group, each of which raises the same condition.
func collectRenderedFiles(files []RenderedFile) (map[string][]byte, error) {
	out := make(map[string][]byte, len(files))
	for _, f := range files {
		if _, dup := out[f.Path]; dup {
			return nil, DuplicateRenderPathErr(f.Path, "")
		}
		out[f.Path] = f.Content
	}
	return out, nil
}
