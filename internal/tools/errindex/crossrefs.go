// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// titleAfterLink and titleBeforeLink match the two orders a page names a section
// of another page in: the link and then the title it means, or the title and then
// the link, joined by a word that says the title is inside it.
//
//	see [component.md](../entities/component.md) "Ad-hoc sockets"
//	"Binding-map-overlap lint" in [warnings.md](./warnings.md)
//
// The second form requires the joining word rather than accepting bare
// adjacency. A quoted string standing next to a link is a shape ordinary prose
// takes for other reasons — a quoted value, a quoted message — and a check that
// read every one of those as a section reference would refuse correct writing.
var (
	titleAfterLink  = regexp.MustCompile(`\]\(([^)\s]+\.md[^)\s]*)\)\s+"([^"]+)"`)
	titleBeforeLink = regexp.MustCompile(`"([^"]+)"\s+(?:in|of|on|under|at)\s+\[[^\]]*\]\(([^)\s]+\.md[^)\s]*)\)`)
)

// skippedDirs are the build outputs and installed trees under the documentation
// root. They carry copies of the published pages, so reading them would report
// one reference several times and report findings against a path nobody edits.
var skippedDirs = []string{"node_modules", "dist", ".aggrega"}

// crossref is one prose reference naming a section of another page by its title:
// where it was written, what it points at, and the title it names.
type crossref struct {
	From  string // the referring page, relative to the documentation root
	Line  int
	Page  string // the referenced page, relative to the documentation root
	Title string
}

// crossrefsIn returns every prose reference that names a section by title and
// touches the published error reference — one written on a page of that
// reference, or one pointing into it from anywhere in the documentation tree.
//
// Both directions are read because the failure runs in both. A title written on
// one of these pages goes stale when the page it names is edited; a title written
// elsewhere and pointing here goes stale when an entry on this page is renamed,
// and that direction is the one nothing else could catch — the reference is prose
// rather than a link, so no link checker sees it, and the page being renamed
// carries no record that anything names its headings.
//
// The scope stops there rather than covering the whole tree. A reference between
// two pages that have nothing to do with the error reference is a real thing to
// check and is not this gate's to judge: a gate refusing on a page outside the
// surface it is named over surprises whoever edits that page, and the surprise is
// what gets a gate switched off.
func crossrefsIn(docsRoot string) ([]crossref, error) {
	var out []crossref
	err := filepath.WalkDir(docsRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if slices.Contains(skippedDirs, d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		rel, err := filepath.Rel(docsRoot, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		fenced := false
		for i, line := range strings.Split(string(data), "\n") {
			if t := strings.TrimSpace(line); strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
				fenced = !fenced
				continue
			}
			if fenced {
				continue
			}
			for _, m := range titleAfterLink.FindAllStringSubmatch(line, -1) {
				out = appendCrossref(out, rel, i+1, m[1], m[2])
			}
			for _, m := range titleBeforeLink.FindAllStringSubmatch(line, -1) {
				out = appendCrossref(out, rel, i+1, m[2], m[1])
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// appendCrossref resolves a reference's target against the page that wrote it and
// keeps the reference when either end sits in the published error reference.
//
// The fragment is dropped: a reference carrying one already resolves through the
// anchor, and what is being checked here is the title beside it.
func appendCrossref(out []crossref, from string, line int, target, title string) []crossref {
	if i := strings.IndexAny(target, "#?"); i >= 0 {
		target = target[:i]
	}
	if target == "" {
		return out
	}
	page := path.Clean(path.Join(path.Dir(from), target))
	if path.Dir(from) != errorsRelDir && path.Dir(page) != errorsRelDir {
		return out
	}
	return append(out, crossref{From: from, Line: line, Page: page, Title: title})
}

// headingsIn returns every heading on a page, as the words each is made of.
//
// The words rather than the text, because a heading and a reference to it are
// written by different hands and typography drifts between them: a backtick, an
// emphasis marker or a dash of one width against another. [proseWords] is the
// same normalisation the entry claims use, so one notion of "the same text"
// serves every comparison this generator makes.
func headingsIn(page string) ([][]string, error) {
	data, err := os.ReadFile(page)
	if err != nil {
		return nil, err
	}
	var out [][]string
	fenced := false
	for _, line := range strings.Split(string(data), "\n") {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		if level := headingLevel(line); level > 0 {
			out = append(out, proseWords(line[level:]))
		}
	}
	return out, nil
}

// reportCrossrefs writes the cross-reference tier to w and returns how many prose
// references name a section that does not exist, alongside how many were
// resolved. A non-zero shortfall is a refusal, and the caller exits non-zero on
// it.
//
// A reference of this shape is invisible to every other check in the build. It is
// prose rather than a link, so a link checker does not see it; the title it names
// is a heading on another page, so nothing on the referring page changes when that
// heading is reworded. Renaming an entry therefore leaves a sentence quoting a
// section that no longer exists, and the sentence goes on reading as a working
// pointer.
//
// The comparison is the whole title against a whole heading rather than a
// containment test. A title names one section, so a heading that merely contains
// the title's words is a different section — and admitting it would let a
// reference to a renamed entry resolve against whichever surviving heading
// happened to share its opening words, which is worse than not checking, because
// it reads as verified.
func reportCrossrefs(w io.Writer, docsRoot string, refs []crossref, list bool) (unresolved, considered int) {
	if len(refs) == 0 {
		fmt.Fprintf(w, "REFUSED: no prose reference naming a section by title touches %s; "+
			"the reference form or the page layout moved\n", errorsRelDir)
		return 1, 0
	}

	// Headings are read once per referenced page. Several references commonly
	// name sections of one page, and re-reading it per reference would make the
	// tier's cost a function of how much the pages cite each other.
	headings := map[string][][]string{}
	var broken []string
	for _, r := range refs {
		hs, seen := headings[r.Page]
		if !seen {
			var err error
			hs, err = headingsIn(filepath.Join(docsRoot, filepath.FromSlash(r.Page)))
			if err != nil {
				// A reference whose target cannot be read is reported rather than
				// skipped: the title resolves to nothing, which is the finding,
				// and skipping it would report a corpus smaller than the page
				// declares while looking clean.
				hs = nil
			}
			headings[r.Page] = hs
		}
		want := proseWords(r.Title)
		if len(want) > 0 && slices.ContainsFunc(hs, func(h []string) bool { return slices.Equal(h, want) }) {
			continue
		}
		broken = append(broken, fmt.Sprintf("%s:%d names %q in %s, which no heading there matches",
			r.From, r.Line, r.Title, r.Page))
	}

	if len(broken) == 0 {
		fmt.Fprintf(w, "crossrefs: all %d prose references naming a section of %s by title resolve to a "+
			"heading that exists\n", len(refs), errorsRelDir)
		return 0, len(refs)
	}
	if !list {
		fmt.Fprintf(w, "REFUSED: %d of %d prose references naming a section of %s by title resolve to no "+
			"heading (re-run with -crossrefs to list them)\n", len(broken), len(refs), errorsRelDir)
		return len(broken), len(refs)
	}
	fmt.Fprintf(w, "REFUSED: %d of %d prose references naming a section of %s by title resolve to no heading:\n",
		len(broken), len(refs), errorsRelDir)
	for _, b := range broken {
		fmt.Fprintf(w, "REFUSED:   %s\n", b)
	}
	return len(broken), len(refs)
}
