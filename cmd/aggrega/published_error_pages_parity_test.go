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
// The type token is captured rather than merely skipped, because it is the only
// thing in the pattern that tells the forms apart, and the calibration below
// asserts one needle of each form. Skipping it would leave both needles matching
// under either shape.
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
var declaredIdentifier = regexp.MustCompile(`\b(Err[A-Z][A-Za-z0-9]*)(?:[ \t]+([\w.]+))?\s*(=|\berror\b)`)

// declarationForm records which declaration shapes an identifier was seen in.
//
// Presence alone is not enough for the calibration. The pattern above matches
// both shapes, so an identifier that changed from one to the other would go on
// matching and a presence check would go on passing — leaving the leg of the
// pattern that needle was chosen to exercise unwatched, with nothing going red.
// Recording the shape is what makes that change fail loudly instead.
type declarationForm struct {
	// untyped is `ErrX = …`: no type token between the identifier and the
	// assignment. It is the shape a variable initialised at its declaration
	// takes, and the shape an alias for another package's identity takes.
	untyped bool
	// typed is `ErrX Code = …`: a type token sits between the identifier and
	// the assignment, which is how a typed constant is declared.
	typed bool
}

// declared reports whether the identifier was seen in any declaration form.
func (f declarationForm) declared() bool { return f.untyped || f.typed }

// goSourceCorpus returns every non-test Go file's text in the module, joined, and
// the set of exported error identifiers the module declares.
func goSourceCorpus(t *testing.T) (string, map[string]declarationForm) {
	t.Helper()
	root := moduleRootForParity(t)
	var b strings.Builder
	declared := map[string]declarationForm{}
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
			form := declared[m[1]]
			if m[2] == "" {
				form.untyped = true
			} else {
				form.typed = true
			}
			declared[m[1]] = form
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
	// Leaving two needles of the same form here would restore exactly that blind
	// spot, and it would do it silently, because nothing goes red when a needle
	// changes form — a needle keeps matching under its new shape while the shape it
	// was chosen to represent goes unwatched.
	//
	// The variable-form needle names the one identity that is declared that way and
	// cannot become a coded constant: it wraps another sentinel so a caller may
	// match either identity, and a string constant has nothing to unwrap. That is a
	// property of what the identity has to do rather than a stage it is passing
	// through, so this needle is stable and does not need re-pointing.
	//
	// Each needle asserts the declaration SHAPE, not merely that the identifier is
	// declared. Presence would be satisfied by either shape, so an identity that
	// changed from one to the other would keep the needle passing while the leg of
	// the pattern it was chosen to represent stopped being exercised — the silent
	// degradation these two assertions exist to prevent. Asserting the shape makes
	// that change fail here, beside the reason, rather than nowhere.
	if !declared["ErrCacheMetadataInvalid"].untyped {
		t.Fatal("declaration scan did not see ErrCacheMetadataInvalid declared with no type token between " +
			"the identifier and the assignment, which is the form an initialised variable takes. " +
			"Either the scan has stopped matching that form, or the identity became a typed constant — " +
			"which it cannot be, because it wraps another sentinel so a caller may match either, and a " +
			"string constant has nothing to unwrap. Whichever it is, the variable-form leg of this " +
			"calibration is no longer exercised: fix the scan, or point this needle at another identity " +
			"declared that way.")
	}
	if !declared["ErrUnknownEntityKind"].typed {
		t.Fatal("declaration scan did not see ErrUnknownEntityKind declared with a type token between the " +
			"identifier and the assignment, which is the form a typed constant takes; " +
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
			if !declared[id].declared() {
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
