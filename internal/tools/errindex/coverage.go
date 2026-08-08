// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// haltsRelPath is the halting-conditions page, relative to the documentation
// tree root. That page is written for someone authoring entities, so it names
// conditions by what the author did wrong rather than by the identifier a
// program matches — which is why the correspondence between the two has to be
// declared rather than derived from the prose.
const haltsRelPath = "language-reference/errors/fatal-halts.md"

// coverageAnchor matches the declaration an entry carries naming the identities
// it covers:
//
//	<!-- covers: pkg/resolver.ErrParameterMissing, pkg/resolver.ErrParameterContractViolation -->
//
// It is an HTML comment so that it is invisible to a reader of the rendered
// page. The page's audience is an entity author, who never types these
// identifiers and would be reading vocabulary belonging to a different surface.
//
// The declaration sits on the entry it describes rather than in a table
// elsewhere, because a table would be a second copy of the correspondence and
// the copy is what drifts: an entry could be rewritten, split or removed with
// nothing to make the table follow it. Here the correspondence moves with the
// prose it describes or disappears with it.
var coverageAnchor = regexp.MustCompile(`(?s)<!--\s*covers:(.*?)-->`)

// readCoverage returns every handle the page's anchors name, and the number of
// anchors they came from.
//
// The anchor count is returned separately because zero anchors and zero handles
// are different failures: the first means the marker form moved, the second
// means every anchor is empty. Both are reported as instrument failures rather
// than served as "the page covers nothing".
func readCoverage(docsRoot string) ([]string, int, error) {
	page := filepath.Join(docsRoot, haltsRelPath)
	data, err := os.ReadFile(page)
	if err != nil {
		return nil, 0, err
	}
	matches := coverageAnchor.FindAllStringSubmatch(string(data), -1)
	var handles []string
	for _, m := range matches {
		for _, f := range strings.FieldsFunc(m[1], func(r rune) bool {
			return r == ',' || unicode.IsSpace(r)
		}) {
			handles = append(handles, f)
		}
	}
	return handles, len(matches), nil
}

// wellFormedHandle reports whether h is a qualified (package, name) handle.
//
// A bare name is refused rather than resolved. Three names in this module are
// declared independently in two packages each, so a bare name names no single
// identity — and an anchor carrying one would silently claim nothing, which
// fails toward a page that looks covered.
func wellFormedHandle(h string) bool {
	i := strings.LastIndex(h, ".")
	if i <= 0 || i == len(h)-1 {
		return false
	}
	return strings.HasPrefix(h[i+1:], "Err")
}

// resolveCoverage reads the page's anchors and returns the set of identities
// they name, keyed by the handle of the identity that declares each.
//
// It fails on a declaration that resolves to nothing. That direction is graded
// this way because a false correspondence is worse than a missing one: it reads
// as verified, and nothing downstream can tell the two apart. The opposite
// direction — an identity no entry names — is not a defect and is reported by
// reportGaps instead.
func resolveCoverage(ids []Identity, docsRoot string) (map[string]bool, error) {
	anchored, anchors, err := readCoverage(docsRoot)
	if err != nil {
		return nil, err
	}
	if anchors == 0 {
		return nil, fmt.Errorf("no coverage anchors found in %s; the anchor form or the page layout moved",
			filepath.Join(docsRoot, haltsRelPath))
	}
	if len(anchored) == 0 {
		return nil, fmt.Errorf("%d coverage anchors in %s name no identity at all",
			anchors, filepath.Join(docsRoot, haltsRelPath))
	}

	byHandle := make(map[string]Identity, len(ids))
	for _, id := range ids {
		byHandle[id.Handle()] = id
	}

	// Every malformed and every unresolvable handle is collected before any is
	// reported. Failing on the first would hide the rest behind it, so a page
	// with several would need as many runs to repair as it has defects.
	var malformed, unknown []string
	covered := map[string]bool{}
	for _, h := range anchored {
		if !wellFormedHandle(h) {
			malformed = append(malformed, h)
			continue
		}
		id, ok := byHandle[h]
		if !ok {
			unknown = append(unknown, h)
			continue
		}
		// An anchor may name a re-export, which is the same identity under a
		// name a caller was told to match. It is credited to the identity it
		// forwards to, so naming either handle covers the condition once.
		if id.AliasOf != "" {
			covered[id.AliasOf] = true
			continue
		}
		covered[h] = true
	}
	if len(malformed) == 0 && len(unknown) == 0 {
		return covered, nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s declares a correspondence that resolves to nothing", haltsRelPath)
	if len(malformed) > 0 {
		sort.Strings(malformed)
		fmt.Fprintf(&b, "\n  not a qualified package.Name handle: %s", strings.Join(malformed, ", "))
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		fmt.Fprintf(&b, "\n  no such identity is declared: %s", strings.Join(unknown, ", "))
	}
	b.WriteString("\n  an entry naming an identity nothing declares reads as a verified correspondence " +
		"while describing nothing; correct the handle, or remove it and let the identity be reported as uncovered")
	return nil, fmt.Errorf("%s", b.String())
}

// gapsIn returns every declared identity no entry names, and how many declared
// identities were considered.
//
// Re-exports are not counted on either side. A re-export is the same identity
// under a second name, so counting it would report one condition as two gaps
// against a population that never grew.
func gapsIn(ids []Identity, covered map[string]bool) (gaps []Identity, origins int) {
	for _, id := range ids {
		if id.AliasOf != "" {
			continue
		}
		origins++
		if !covered[id.Handle()] {
			gaps = append(gaps, id)
		}
	}
	return gaps, origins
}

// reportGaps writes the gap tier to w: a count always, and the identities
// themselves when list is set.
//
// It never fails. Not every halting condition needs a user-facing entry, so
// failing here would demand one per identity — and a gate that is red on the
// day it ships stays red and gets switched off, which would cost the blocking
// direction in resolveCoverage too.
//
// The enumeration is opt-in because this runs inside the routine build, where
// printing every uncovered identity on every build produces a wall of output
// that is scrolled past rather than read — which costs the count its meaning
// too. The count is the signal that something moved; the list is what you ask
// for once it has.
func reportGaps(w io.Writer, ids []Identity, covered map[string]bool, list bool) {
	gaps, origins := gapsIn(ids, covered)
	if len(gaps) == 0 {
		fmt.Fprintf(w, "WARN: none — all %d declared halting conditions are named by an entry on %s\n",
			origins, haltsRelPath)
		return
	}
	if !list {
		fmt.Fprintf(w, "WARN: %d of %d declared halting conditions have no entry on %s (re-run with -gaps to list them)\n",
			len(gaps), origins, haltsRelPath)
		return
	}
	// The message is printed beside the handle because a bare list of names is
	// not actionable: deciding whether a condition wants a user-facing entry
	// means knowing what it is, and the message is that description, already
	// declared beside the identity.
	fmt.Fprintf(w, "WARN: %d of %d declared halting conditions have no entry on %s:\n",
		len(gaps), origins, haltsRelPath)
	for _, id := range gaps {
		fmt.Fprintf(w, "WARN:   %s: %s\n", id.Handle(), id.Message)
	}
}
