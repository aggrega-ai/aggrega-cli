// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package docsplice replaces a delimited region of a markdown page with
// generated content, preserving every byte outside the region.
//
// It is the one home for the generated-region convention, so a page's markers
// mean the same thing to every generator that writes one. A region is fenced by
// a matching pair of HTML comments carrying the region's id:
//
//	<!-- BEGIN GENERATED: <id> -->
//	…generated content…
//	<!-- END GENERATED: <id> -->
//
// The markers are HTML comments so they render as nothing, which lets a page mix
// curated prose and generated content without the seams showing.
//
// Splicing is idempotent: the canonical block form puts the begin marker, the
// content and the end marker each on its own line, so splicing identical content
// a second time reproduces the same bytes. That is what lets a drift check
// regenerate into a throwaway copy and compare, rather than having to model what
// a generator would have written.
package docsplice

import (
	"fmt"
	"os"
	"strings"
)

// beginMarker and endMarker are the format strings for the HTML-comment
// delimiters that fence a generated region. The %s is the region id.
const (
	beginMarker = "<!-- BEGIN GENERATED: %s -->"
	endMarker   = "<!-- END GENERATED: %s -->"
)

// Splice replaces the region delimited by id's markers within content with
// inner, preserving everything outside the markers verbatim. When the markers
// are absent the block is appended after the existing content, so a page can be
// grown a region without hand-placing one.
//
// Unbalanced markers, or an end marker preceding its begin marker, are errors
// rather than a best-effort splice: a half-marked page is an authoring mistake,
// and writing generated content into an unknown position would be worse than
// refusing.
func Splice(content, id, inner string) (string, error) {
	begin := fmt.Sprintf(beginMarker, id)
	end := fmt.Sprintf(endMarker, id)
	block := begin + "\n" + inner + "\n" + end

	bi := strings.Index(content, begin)
	ei := strings.Index(content, end)

	switch {
	case bi == -1 && ei == -1:
		if strings.TrimSpace(content) == "" {
			return block + "\n", nil
		}
		return strings.TrimRight(content, "\n") + "\n\n" + block + "\n", nil
	case bi == -1 || ei == -1:
		return "", fmt.Errorf("unbalanced markers for id %q", id)
	case ei < bi:
		return "", fmt.Errorf("end marker precedes begin marker for id %q", id)
	default:
		after := ei + len(end)
		return content[:bi] + block + content[after:], nil
	}
}

// WriteFile splices inner into the region id of content and writes the result to
// path, but only when the result differs from content. It reports whether the
// file was written.
//
// Skipping an identical write is what keeps an unchanged page's modification
// time stable, so a generator run over an unchanged source leaves the tree
// exactly as it found it and a freshness stamp keyed on the page does not
// invalidate itself.
func WriteFile(path, content, id, inner string) (bool, error) {
	next, err := Splice(content, id, inner)
	if err != nil {
		return false, fmt.Errorf("splice %s (%s): %w", path, id, err)
	}
	if next == content {
		return false, nil
	}
	if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}
