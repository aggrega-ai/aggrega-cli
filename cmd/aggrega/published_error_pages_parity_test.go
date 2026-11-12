// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/docsplice"
	"github.com/aggrega-ai/aggrega-cli/internal/haltref"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
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
// in each of the two forms the tree uses: an accessor, which takes nothing and
// returns an error, and a variable or constant bound directly to a value. An
// accessor forwarding to another package's identity is the same shape as one
// returning its own, so both are read as accessors — which is right, since a
// forwarding name is matchable exactly as a declaring one is.
//
// The `() error` token is captured rather than merely skipped, because it is the
// only thing in the pattern that tells the forms apart, and the calibration below
// asserts one needle of each form. Skipping it would leave both needles matching
// under either shape.
//
// Each alternative is separated by spaces or tabs rather than by arbitrary
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
var declaredIdentifier = regexp.MustCompile(`\b(Err[A-Z][A-Za-z0-9]*)(?:\(\)[ \t]+(error)\b|[ \t]*=)`)

// declarationForm records which declaration shapes an identifier was seen in.
//
// Presence alone is not enough for the calibration. The pattern above matches
// both shapes, so an identifier that changed from one to the other would go on
// matching and a presence check would go on passing — leaving the leg of the
// pattern that needle was chosen to exercise unwatched, with nothing going red.
// Recording the shape is what makes that change fail loudly instead.
type declarationForm struct {
	// accessor is `ErrX() error`: the identifier is declared as a function
	// taking nothing and returning an error. It is the shape an identity takes
	// when the value itself is sealed and reached only through the name that
	// publishes it.
	accessor bool
	// variable is `ErrX = …`: the identifier is bound directly to a value, with
	// no parameter list and no result between it and the assignment.
	variable bool
}

// declared reports whether the identifier was seen in any declaration form.
func (f declarationForm) declared() bool { return f.accessor || f.variable }

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
				form.variable = true
			} else {
				form.accessor = true
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
	// cannot become an accessor over a registration: it wraps another sentinel so a
	// caller may match either identity, and it registers no message of its own for
	// a registration to carry. That is a property of what the identity has to do
	// rather than a stage it is passing through, so this needle is stable and does
	// not need re-pointing.
	//
	// Each needle asserts the declaration SHAPE, not merely that the identifier is
	// declared. Presence would be satisfied by either shape, so an identity that
	// changed from one to the other would keep the needle passing while the leg of
	// the pattern it was chosen to represent stopped being exercised — the silent
	// degradation these two assertions exist to prevent. Asserting the shape makes
	// that change fail here, beside the reason, rather than nowhere.
	if !declared["ErrCacheMetadataInvalid"].variable {
		t.Fatal("declaration scan did not see ErrCacheMetadataInvalid declared with the identifier bound " +
			"directly to a value, which is the form an initialised variable takes. " +
			"Either the scan has stopped matching that form, or the identity became an accessor — " +
			"which it cannot be, because it wraps another sentinel so a caller may match either, and it " +
			"registers no message of its own. Whichever it is, the variable-form leg of this " +
			"calibration is no longer exercised: fix the scan, or point this needle at another identity " +
			"declared that way.")
	}
	if !declared["ErrUnknownEntityKind"].accessor {
		t.Fatal("declaration scan did not see ErrUnknownEntityKind declared as a function taking nothing " +
			"and returning an error, which is the form an accessor over a sealed identity takes; " +
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

// shortCodeToken matches a short code as the published region writes one.
var shortCodeToken = regexp.MustCompile(`\bE[0-9]{4}\b`)

// TestPublishedErrorPages_TheHaltRegionResolvesThroughTheReader is the parity
// between the side that WRITES the code-to-entry correspondence onto the
// halting-conditions page and the side that READS it back to answer a code.
//
// The two are separate programs: a generator renders the region from the
// module's declarations, and the resolver looks a code up in a served copy of
// the page. A drift gate holds the region current with the declarations, and
// nothing held the reader to the region — so a row shape either side stopped
// agreeing on would have left every lookup answering "no entry documents this",
// which is a legitimate answer and therefore silent.
//
// The reference page's own reader is held the same way, in the second half
// below: it is a separate correspondence with a separate row shape, and it is
// what answers a code the binary's declarations do not resolve.
//
// This runs against the committed page rather than a fixture, which is the whole
// point: a fixture is written by whoever writes the test, so it agrees with the
// reader by construction. The pages sit outside the module, so a change to one
// does not by itself re-run this — the drift gate is what watches the page's
// contents, and this watches whether the reader can still read it.
func TestPublishedErrorPages_TheHaltRegionResolvesThroughTheReader(t *testing.T) {
	t.Parallel()
	pages := publishedErrorPages(t)

	name := filepath.Base(haltref.PagePath)
	body, ok := pages[name]
	if !ok {
		t.Fatalf("the published error pages carry no %s, which is the page a code resolves against", name)
	}
	region, ok := docsplice.Region(body, haltref.RegionID)
	if !ok {
		t.Fatalf("%s carries no %q region, so nothing pairs a code with the entry documenting it",
			name, haltref.RegionID)
	}

	codes := shortCodeToken.FindAllString(region, -1)
	if len(codes) == 0 {
		t.Fatal("the region names no short code; the extraction is broken, not the page")
	}

	// The reference page is the other half of the same correspondence, and it is
	// what answers a code this binary's own declarations do not resolve. Both
	// routes are exercised, and both against the committed pages.
	refName := filepath.Base(haltref.ReferencePagePath)
	reference, ok := pages[refName]
	if !ok {
		t.Fatalf("the published error pages carry no %s, which is what resolves a code the registry does not", refName)
	}

	seen := map[string]bool{}
	for _, code := range codes {
		if seen[code] {
			continue
		}
		seen[code] = true

		// The code the page publishes resolves to a condition the product
		// declares, from the binary's own registry or from the published
		// reference. A row for a condition neither carries would send a reader to
		// an entry for a halt no build can report.
		condition, rerr := haltref.Resolve(code)
		if rerr != nil {
			condition, rerr = haltref.ResolveIn([]byte(reference), code)
			if rerr != nil {
				t.Errorf("%s carries a row for %s, and neither this binary nor %s resolves it: %v",
					name, code, refName, rerr)
				continue
			}
		}
		if condition.ShortCode != code {
			t.Errorf("%s resolved to the condition %s, which reports the short code %s",
				code, condition.Code, condition.ShortCode)
		}
		if condition.Code == "" || condition.Message == "" {
			t.Errorf("%s resolved to a condition carrying no descriptive code or no sentence: %+v", code, condition)
		}

		// And the row resolves through the reader to prose that exists.
		entries, eerr := haltref.Entries([]byte(body), code)
		if eerr != nil {
			t.Errorf("%s pairs %s with an entry the reader cannot reach: %v", name, code, eerr)
			continue
		}
		for _, e := range entries {
			if !strings.Contains(e.Body, e.Heading) {
				t.Errorf("the entry resolved for %s does not carry its own heading %q", code, e.Heading)
			}
			if len(strings.TrimSpace(strings.TrimPrefix(e.Body, "#"))) <= len(e.Heading)+1 {
				t.Errorf("the entry resolved for %s under %q carries no prose beneath its heading", code, e.Heading)
			}
		}
	}

	// The reference reader is exercised directly, over the committed reference
	// page, rather than counted by how often the loop above happened to fall
	// through to it.
	//
	// Counting is what this used to do, and it stopped being able to measure
	// anything: every code the halting-conditions page names resolves from the
	// binary's own registry, so the fallback is never reached from that page and
	// the count is structurally zero. A zero there says nothing about whether the
	// reader still works — which is the property worth holding, because the
	// generator writes the reference's correspondence and this reader parses it
	// back, and a row shape either side stopped agreeing on would leave every
	// fallback lookup answering "nothing is registered under this": a legitimate
	// answer, and therefore silent.
	//
	// Every short code the reference names is in exactly one of two states, and
	// the pair is asserted as a partition rather than as two separate facts. A
	// live condition is a row this reader resolves. A retired number is a note
	// beneath the table and the retirement record holds it. A code in neither
	// state is a line on the page standing for nothing; a code in both is a
	// number published as live and recorded as spent at the same time.
	refRegion, ok := docsplice.Region(reference, haltref.ReferenceRegionID)
	if !ok {
		t.Fatalf("%s carries no %q region, so the reader that answers a code the registry does not "+
			"has nothing to resolve against", refName, haltref.ReferenceRegionID)
	}
	refCodes := shortCodeToken.FindAllString(refRegion, -1)
	if len(refCodes) == 0 {
		t.Fatal("the reference region names no short code; the extraction is broken, not the page")
	}
	var live, spent int
	seenRef := map[string]bool{}
	for _, code := range refCodes {
		if seenRef[code] {
			continue
		}
		seenRef[code] = true

		published, perr := haltref.ResolveIn([]byte(reference), code)
		_, retired := cerrors.RetiredShortCode(code)
		switch {
		case perr != nil && !retired:
			t.Errorf("%s names %s and neither resolves it as a condition nor records it as retired, so "+
				"the line stands for nothing a reader can look up: %v", refName, code, perr)
			continue
		case perr == nil && retired:
			t.Errorf("%s publishes %s as a live condition while the retirement record holds it as spent; "+
				"a reader is told a number both names something and never will again", refName, code)
			continue
		case retired:
			spent++
			continue
		}
		live++
		if published.ShortCode != code {
			t.Errorf("%s read out of %s reports the short code %s", code, refName, published.ShortCode)
		}
		if published.Code == "" || published.Message == "" {
			t.Errorf("%s read out of %s carries no descriptive code or no sentence: %+v", code, refName, published)
		}
	}
	// Both arms have to have run. A partition whose every code fell on one side
	// leaves the other arm asserting nothing, and the page carries both today.
	if live == 0 || spent == 0 {
		t.Errorf("the reference resolved %d live condition(s) and %d retired number(s); one arm of the "+
			"partition was never exercised, so its assertions say nothing", live, spent)
	}
	// Calibration for that reader: a code the reference does not name is reported
	// as naming nothing rather than answered, so the rows above mean what they say
	// and not that the reader answers for whatever it is handed.
	var unknown *haltref.UnknownCodeError
	if _, err := haltref.ResolveIn([]byte(reference), "E0000"); !errors.As(err, &unknown) {
		t.Errorf("a code %s does not name was not reported as unknown by the reader: %v", refName, err)
	}

	// Calibration. Every assertion above passes over a page the reader silently
	// resolves nothing from, so the negative direction is asserted too: a code the
	// region does not name is reported as undocumented rather than answered.
	// E0000 can never be registered — numbers are handed out from E0001 upward —
	// so it is the one value guaranteed absent from any region.
	var undocumented *haltref.UndocumentedConditionError
	if _, err := haltref.Entries([]byte(body), "E0000"); !errors.As(err, &undocumented) {
		t.Errorf("a code the region does not name was not reported as undocumented: %v", err)
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
