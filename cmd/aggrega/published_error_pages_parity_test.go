// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The published error pages advertise what a build can tell an author. Nothing
// checked that the advertisement had a producer, and it drifted: entries carried
// message prefixes the compiler never emits, found only because a reader
// happened to notice. These two gates close the mechanisable half of that class.
//
// Both are deliberately narrow. They key on tokens whose presence in the source
// is objectively decidable — a screaming-case prefix is either a substring of
// some Go string literal or it is not; an error identifier is either declared or
// it is not — rather than on message prose, which the pages paraphrase for
// readability and which no substring match can judge. A prose-level check was
// attempted and measured: it flagged 17 of 45 entries, including entries whose
// producer was verified against the running binary, so its precision is unusable
// and it is deliberately not shipped. Reviewer judgement covers the residue.

const publishedErrorPagesDir = "../../../aggrega-docs/language-reference/errors"

// screamingPrefix matches a leading-capitals token used as a message prefix,
// which is how both the category tags and the phantom class the gate exists for
// appear on the page.
var screamingPrefix = regexp.MustCompile(`\b[A-Z][A-Z0-9_]{3,}:`)

// goErrorIdentifier matches an exported error-sentinel identifier as the pages
// name them.
var goErrorIdentifier = regexp.MustCompile(`\bErr[A-Z][A-Za-z0-9]*`)

// declaredIdentifier matches a declaration of such an identifier in Go source,
// in each of the three forms the tree uses: a variable initialised at its
// declaration, an alias for one declared in another package, and a typed constant,
// whose type name sits between the identifier and the assignment.
//
// The optional type token is separated by spaces or tabs rather than by arbitrary
// whitespace, so it cannot span a line ending. A separator that reached across one
// would let a match beginning in the right-hand side of a declaration run on and
// consume the identifier declared on the line below, dropping that identifier from
// the set — a loss the assertions downstream cannot distinguish from a sentinel
// that was genuinely removed.
//
// A second recogniser of the same declaration shapes lives in the sentinel-emission
// assertion under internal/compiler/constraint, and the two are deliberately NOT
// unified. That one parses the syntax tree; this one scans source text. They fail in
// different directions, which is the whole value: this scan reads a shape a tree walk
// cannot express, and that walk reads a shape no pattern can bound. Unifying them
// would replace two independent readings of one population with a single point of
// blindness, so a criterion change must be carried to both sites by hand — which is
// the cost this duplication is knowingly paying.
var declaredIdentifier = regexp.MustCompile(`\b(Err[A-Z][A-Za-z0-9]*)(?:[ \t]+[\w.]+)?\s*(=|\berror\b)`)

// goSourceCorpus returns every non-test Go file's text in the module, joined, and
// the set of exported error identifiers the module declares.
func goSourceCorpus(t *testing.T) (string, map[string]bool) {
	t.Helper()
	root := moduleRootForParity(t)
	var b strings.Builder
	declared := map[string]bool{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "testdata", "node_modules", "dist", "target":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		b.Write(data)
		b.WriteByte('\n')
		for _, m := range declaredIdentifier.FindAllStringSubmatch(string(data), -1) {
			declared[m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk module: %v", err)
	}
	// Calibration. Both assertions below read as "nothing is wrong" against an
	// empty corpus, so the corpus is proven non-empty against known content first.
	if !strings.Contains(b.String(), "CATALOG_ERROR") {
		t.Fatal("source corpus does not contain a known category literal; the walk is broken, not the pages")
	}
	// The declaration scan is calibrated against one identifier of each declared
	// form. A needle of a single form cannot detect a criterion that has stopped
	// seeing another form: the scan would go on matching the needle while
	// under-reporting every identifier declared the other way, and the assertions
	// below would read that silent shortfall as pages naming an absent sentinel.
	if !declared["ErrMalformedRecipeReference"] {
		t.Fatal("declaration scan did not see ErrMalformedRecipeReference, declared as an initialised variable; " +
			"the scan has stopped matching that declaration form — the scan is broken, not the pages")
	}
	if !declared["ErrUnknownEntityKind"] {
		t.Fatal("declaration scan did not see ErrUnknownEntityKind, declared as a typed constant; " +
			"the scan has stopped matching that declaration form — the scan is broken, not the pages")
	}
	return b.String(), declared
}

func moduleRootForParity(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root := filepath.Join(wd, "..", "..")
	if _, serr := os.Stat(filepath.Join(root, "go.mod")); serr != nil {
		t.Fatalf("module root not found at %s: %v", root, serr)
	}
	return root
}

func publishedErrorPages(t *testing.T) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(publishedErrorPagesDir)
	if err != nil {
		t.Skipf("published error pages not present at %s (standalone checkout): %v", publishedErrorPagesDir, err)
	}
	out := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, rerr := os.ReadFile(filepath.Join(publishedErrorPagesDir, e.Name()))
		if rerr != nil {
			t.Fatalf("read %s: %v", e.Name(), rerr)
		}
		out[e.Name()] = string(data)
	}
	if len(out) == 0 {
		t.Fatalf("no published error pages found under %s", publishedErrorPagesDir)
	}
	return out
}

// TestPublishedErrorPages_EveryAdvertisedPrefixHasAProducer is the gate that
// catches the drift that motivated it: a page advertising a message prefix the
// compiler cannot emit. Four such prefixes shipped — each naming a real halt
// under a code the source has never carried — so an author matching on what the
// page showed would have matched nothing.
func TestPublishedErrorPages_EveryAdvertisedPrefixHasAProducer(t *testing.T) {
	t.Parallel()
	pages := publishedErrorPages(t)
	corpus, _ := goSourceCorpus(t)

	// Prose words that happen to be capitalised followed by a colon. They are not
	// message prefixes and are not expected in the source as such.
	prose := map[string]bool{"NOTE:": true, "TODO:": true, "HTTP:": true, "HTTPS:": true}

	seen := 0
	for name, body := range pages {
		for _, tok := range screamingPrefix.FindAllString(body, -1) {
			if prose[tok] {
				continue
			}
			seen++
			// The prefix is advertised without its trailing colon in some source
			// forms (a Category constant's value), so the token is matched bare.
			if !strings.Contains(corpus, strings.TrimSuffix(tok, ":")) {
				t.Errorf("%s advertises the message prefix %q, which appears in no Go source string.\n"+
					"Either the page is advertising a code the compiler cannot emit — correct the page to the "+
					"text the producing site actually formats — or the prefix moved and the page did not follow.",
					name, tok)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no advertised prefix examined; the extraction is broken, not the pages")
	}
}

// TestPublishedErrorPages_EveryNamedSentinelIsDeclared is the identifier half.
// The published sentinel catalogue exists so an integration author can write
// errors.Is against a name; a name that is not declared cannot be imported, so
// the advertisement is unusable rather than merely stale.
func TestPublishedErrorPages_EveryNamedSentinelIsDeclared(t *testing.T) {
	t.Parallel()
	pages := publishedErrorPages(t)
	_, declared := goSourceCorpus(t)

	// A page naming a family by a glob ("the four ErrFetch* values") yields the
	// stem, which is not itself a declared identifier.
	stems := map[string]bool{"ErrFetch": true}

	seen := 0
	for name, body := range pages {
		for _, id := range goErrorIdentifier.FindAllString(body, -1) {
			if stems[id] {
				continue
			}
			seen++
			if !declared[id] {
				t.Errorf("%s names the sentinel %s, which no Go source declares.\n"+
					"Either the sentinel was renamed or removed and the page did not follow, or the page "+
					"advertises a match an integration author cannot compile.", name, id)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no named sentinel examined; the extraction is broken, not the pages")
	}
}
