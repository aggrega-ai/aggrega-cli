// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// errorsRelDir is the published error reference, relative to the documentation
// tree root. Every page in it is read, rather than a list of page names, for the
// reason the directory comparison this check runs beside is written that way: a
// named list has to be extended in step with the pages, and the failure of that
// is silent, because an unread page reads exactly like a current one.
const errorsRelDir = "language-reference/errors"

// transcriptInfo is the fence info string that marks a block as a reproduction
// of what a build prints, as opposed to a sample of something a reader writes.
//
// The distinction is the whole basis of this check. A fenced block of YAML or Go
// is authored input or calling code, and holding it to the messages the module
// registers would be meaningless; a text block is a transcript, and a transcript
// is either what the build prints or a description of output that no longer
// exists.
const transcriptInfo = "text"

// unregisteredAnchor matches the declaration a reproduction carries when it
// reproduces something no halting condition registers:
//
//	<!-- unregistered-output: lint text, which no halting condition registers -->
//
// It is an HTML comment for the reason the coverage declaration is one: it is
// invisible to a reader of the rendered page, whose subject is the product rather
// than which of the module's registries describes a given line.
//
// Two classes need it, and neither is a defect. A page teaching the SHAPE of
// output reproduces a grammar with metavariables in it — a category, a short code
// and a message standing in for whichever condition was met — which quotes no
// sentence because it quotes no condition. And a page documenting warnings and
// lint reproduces a surface the halting-condition registry does not describe at
// all, since a warning is not a halt.
//
// The declaration carries a reason rather than being a bare marker, so the class
// is stated where the exemption is taken. A bare marker would let two unlike
// exemptions accumulate under one spelling with nothing to tell them apart, and
// the count of them in the verdict would stop meaning anything.
var unregisteredAnchor = regexp.MustCompile(`(?s)<!--\s*unregistered-output:(.*?)-->`)

// transcript is one reproduction of emitted output on a published page: where it
// sits, so a finding can be opened, the text it carries, and the declaration it
// carries where it reproduces something no condition registers.
type transcript struct {
	Page     string
	Line     int
	Text     string
	Declared bool
	Reason   string
}

// transcriptsIn returns every reproduction of emitted output across the
// published error reference, in page order within pages read in name order.
//
// The ordering is a function of the tree rather than of a directory read, so a
// verdict enumerating findings enumerates them the same way on every run and two
// runs can be compared against each other.
func transcriptsIn(docsRoot string) ([]transcript, error) {
	dir := filepath.Join(docsRoot, errorsRelDir)
	names, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var pages []string
	for _, n := range names {
		if !n.IsDir() && strings.HasSuffix(n.Name(), ".md") {
			pages = append(pages, n.Name())
		}
	}
	sort.Strings(pages)

	var out []transcript
	for _, page := range pages {
		data, err := os.ReadFile(filepath.Join(dir, page))
		if err != nil {
			return nil, err
		}
		lines := strings.Split(string(data), "\n")
		var body strings.Builder
		// Fence state is held as two values rather than as a sentinel packed into
		// the line number, so "inside a block" and "inside a block that is a
		// transcript" stay separable. A single value conflating them reads the
		// closing fence of a code sample as the opening of another block, which is
		// correct only by accident of what a closing fence's info string happens to
		// be.
		inFence, isTranscript, openedAt := false, false, 0
		// A declaration binds to the next reproduction and is cleared by any other
		// content, so it can only ever describe the block it sits above. A blank
		// line does not clear it, because a comment separated from its block by one
		// is the ordinary way to write both.
		declared, reason := false, ""
		for i, line := range lines {
			t := strings.TrimSpace(line)
			if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
				if inFence {
					if isTranscript {
						out = append(out, transcript{
							Page: page, Line: openedAt, Text: body.String(),
							Declared: declared, Reason: reason,
						})
						body.Reset()
					}
					inFence, isTranscript = false, false
					declared, reason = false, ""
					continue
				}
				// A fence's info string decides whether the block that follows
				// reproduces emitted output. A block of YAML or Go is authored
				// input or calling code, so it is walked past — but its fence is
				// still tracked, because an unbalanced walk would read the prose
				// between two code samples as though it were inside one.
				info := strings.TrimLeft(strings.TrimLeft(t, "`~"), " \t")
				inFence, isTranscript, openedAt = true, info == transcriptInfo, i+1
				continue
			}
			if inFence {
				if isTranscript {
					body.WriteString(line)
					body.WriteByte('\n')
				}
				continue
			}
			if m := unregisteredAnchor.FindStringSubmatch(line); m != nil {
				declared, reason = true, strings.TrimSpace(m[1])
				continue
			}
			if t != "" {
				declared, reason = false, ""
			}
		}
		// A block left open at the end of a page means the fences do not balance,
		// which would make every later reading of that page wrong. It is reported
		// as the malformed page it is rather than closed silently at the last
		// line.
		if inFence {
			return nil, fmt.Errorf("%s: a fenced block opened at line %d is never closed, "+
				"so the page's blocks cannot be read", filepath.Join(errorsRelDir, page), openedAt)
		}
	}
	return out, nil
}

// attributed reports whether t reproduces output the module can actually produce:
// whether some declared condition's registered message appears in it as a
// contiguous run of words.
//
// The comparison is [quotesRun] over [proseWords], the same pair the entry-claims
// tier uses, so a transcript and an entry are held to one notion of quoting. A
// second notion would agree today and diverge on the first message whose
// typography moved.
//
// One registered message is enough. A transcript frequently reproduces a whole
// report — a heading line, the findings collected beneath it, and the framing a
// category adds — and requiring every line of it to resolve would refuse the
// framing text, which no condition registers and which is exactly what makes the
// transcript worth printing.
func attributed(ids []Identity, t transcript) bool {
	have := proseWords(t.Text)
	for _, id := range ids {
		want := proseWords(id.Message)
		if len(want) == 0 {
			continue
		}
		if quotesRun(have, want) {
			return true
		}
	}
	return false
}

// reportTranscripts writes the transcript tier to w and returns how many
// reproductions of emitted output carry no text the module registers, alongside
// how many were read. A non-zero shortfall is a refusal, and the caller exits
// non-zero on it.
//
// This tier reaches what neither the generated regions nor the entry claims can.
// A splice writes only between its markers, so every page outside one — and every
// byte of a page carrying no region at all — is an input to BOTH sides of a
// regenerate-and-compare check and can never produce a difference however wrong
// it is. The warnings page is the whole of that class: it carries no generated
// region, so the directory comparison copies it into both sides and compares it
// against itself. Its transcripts were found wrong twice, and both times only
// because a wording change happened to be the work in hand.
//
// It refuses rather than reports for the reason the coverage tier does: a
// transcript quoting text no build prints teaches a reader to search their output
// for a line that does not exist, and a figure that only reported would let the
// pages regrow that one block at a time with every run green while it happened.
//
// A reproduction of something no halting condition registers takes the exemption
// [unregisteredAnchor] describes, and the exemption is held from both ends. A
// declaration carrying no reason is refused, so the class is always stated; and a
// declaration on a reproduction that DOES quote a registered sentence is refused
// too, so the exempt set stays exactly the set that needs to be exempt. Without
// that second refusal the declarations accumulate over blocks that no longer need
// them, and the count of them in the verdict — which is what says how much of the
// corpus is held — stops describing anything.
//
// The enumeration is opt-in, as the other tiers' are, because this runs inside
// the routine build where a wall of output is scrolled past rather than read.
func reportTranscripts(w io.Writer, ids []Identity, ts []transcript, list bool) (unattributed, considered int) {
	// No transcripts at all is an instrument failure rather than a clean page.
	// The published reference teaches by reproducing output, so a run finding
	// none means the fence form or the directory layout moved — and "0 of 0"
	// reads exactly like a corpus with nothing wrong in it.
	if len(ts) == 0 {
		fmt.Fprintf(w, "REFUSED: no reproduction of emitted output found under %s; "+
			"the fence form or the page layout moved\n", errorsRelDir)
		return 1, 0
	}

	// Three faults, collected before any is reported so a corpus with several
	// needs one run to repair rather than as many runs as it has faults.
	var orphans, stale, blank []transcript
	held := 0
	for _, t := range ts {
		switch {
		case t.Declared && t.Reason == "":
			blank = append(blank, t)
		case t.Declared && attributed(ids, t):
			stale = append(stale, t)
		case t.Declared:
			// Declared and genuinely unheld: counted, never refused.
		case !attributed(ids, t):
			orphans = append(orphans, t)
		default:
			held++
		}
	}

	faults := len(orphans) + len(stale) + len(blank)
	if faults == 0 {
		fmt.Fprintf(w, "transcripts: %d of %d reproductions of emitted output under %s quote text a declared "+
			"condition registers, the other %d declaring that they reproduce output no condition registers\n",
			held, len(ts), errorsRelDir, len(ts)-held)
		return 0, len(ts)
	}
	if !list {
		fmt.Fprintf(w, "REFUSED: %d of %d reproductions of emitted output under %s are not held to the "+
			"conditions this module declares (re-run with -transcripts to list them)\n",
			faults, len(ts), errorsRelDir)
		return faults, len(ts)
	}
	fmt.Fprintf(w, "REFUSED: %d of %d reproductions of emitted output under %s are not held to the "+
		"conditions this module declares:\n", faults, len(ts), errorsRelDir)
	// The position is printed rather than the text, because the text is what the
	// reader is being sent to open, and reproducing it here would put a second
	// copy of a wrong transcript into the build log. Each fault says which of the
	// three it is, because they are repaired differently.
	for _, t := range orphans {
		fmt.Fprintf(w, "REFUSED:   %s:%d quotes no sentence any condition registers, and declares no reason\n",
			filepath.Join(errorsRelDir, t.Page), t.Line)
	}
	for _, t := range stale {
		fmt.Fprintf(w, "REFUSED:   %s:%d declares its output unregistered while quoting a sentence a "+
			"condition registers; drop the declaration\n", filepath.Join(errorsRelDir, t.Page), t.Line)
	}
	for _, t := range blank {
		fmt.Fprintf(w, "REFUSED:   %s:%d declares its output unregistered but says nothing; name the surface "+
			"it reproduces\n", filepath.Join(errorsRelDir, t.Page), t.Line)
	}
	return faults, len(ts)
}
