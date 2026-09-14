// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Command pictographcensus reports pictographic characters that appear inside a
// Go comment on a surface Go publishes, and exits non-zero when it finds any.
//
// A doc comment above an exported declaration is rendered by the module proxy's
// documentation site, by `go doc`, and by an editor's hover. A pictograph placed
// there as an emphasis marker for whoever edits the file next is not addressed to
// that reader, and the reader has no way to tell the two audiences apart. So the
// class is barred from the published surface, and this command is what makes the
// bar checkable rather than remembered.
//
// It reads comments and nothing else. The Go parser hands back comment nodes as
// a distinct part of the syntax tree, so a string literal, a rune literal, an
// identifier and every other token are structurally out of reach — a check built
// on a text pattern cannot make that distinction, and gets it wrong in both
// directions: it fires on a string that happens to carry a comment marker and a
// pictograph on one line, and it misses a block comment's continuation lines,
// which open with an asterisk rather than a comment marker.
//
// Run `pictographcensus -h` for the matched codepoint class, the corpus, and
// what is deliberately left alone.
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Exit codes. Separated from an ordinary hit because they answer different
// questions: 1 says the content is wrong, 2 says the check could not be made.
const (
	exitClean   = 0
	exitHits    = 1
	exitBroken  = 2
	toolName    = "pictographcensus"
	corpusPub   = "published"
	corpusEvery = "all"
)

func main() {
	corpus := flag.String("corpus", corpusPub, "which files to inspect: \"published\" (the gate's corpus) or \"all\" (every Go file, for a census only)")
	list := flag.Bool("list", false, "print the files that would be inspected and exit, so a zero-hit result can be read against a corpus size")
	only := flag.String("only", "", "restrict the match to one codepoint, given as hex without a prefix (for example 26A0); a census aid, not the gate")
	flag.Usage = usage
	flag.Parse()

	if flag.NArg() == 0 {
		fmt.Fprintf(os.Stderr, "%s: no path given — name the files or directories to inspect\n", toolName)
		fmt.Fprintf(os.Stderr, "       Inspecting nothing would exit clean, which is indistinguishable\n")
		fmt.Fprintf(os.Stderr, "       from a corpus with no hits in it. Run with -h for the contract.\n")
		os.Exit(exitBroken)
	}

	if *corpus != corpusPub && *corpus != corpusEvery {
		fmt.Fprintf(os.Stderr, "%s: unknown -corpus %q — expected %q or %q\n", toolName, *corpus, corpusPub, corpusEvery)
		os.Exit(exitBroken)
	}

	var wanted rune = -1
	if *only != "" {
		v, err := strconv.ParseUint(strings.TrimPrefix(strings.TrimPrefix(*only, "U+"), "u+"), 16, 32)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: -only %q is not a hex codepoint\n", toolName, *only)
			os.Exit(exitBroken)
		}
		wanted = rune(v)
	}

	files, err := collect(flag.Args(), *corpus == corpusEvery)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", toolName, err)
		os.Exit(exitBroken)
	}

	if *list {
		for _, f := range files {
			fmt.Println(f)
		}
		return
	}

	hits := 0
	unreadable := 0
	for _, path := range files {
		found, err := scan(path, wanted)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %s: could not be read, so it was NOT inspected: %v\n", toolName, path, err)
			unreadable++
			continue
		}
		for _, h := range found {
			fmt.Println(h)
			hits++
		}
	}

	if unreadable > 0 {
		fmt.Fprintf(os.Stderr, "%s: %d file(s) went uninspected — this run carries no verdict for them\n", toolName, unreadable)
		os.Exit(exitBroken)
	}
	if hits > 0 {
		os.Exit(exitHits)
	}
	os.Exit(exitClean)
}

func usage() {
	out := flag.CommandLine.Output()
	fmt.Fprintf(out, `%s — find pictographs inside Go comments on a published surface.

Usage:
  %s [flags] <path> ...

Each path is a Go file or a directory, which is walked for Go files. Naming no
path at all is an error rather than an empty clean run.

Output: one line per hit, "<file>:<line>:<col>: U+XXXX <char> in a comment".
Exit 0 no hits, 1 one or more hits, 2 the check could not be made.

WHAT IT MATCHES — the pictograph class, by codepoint range:

  U+1F000-U+1FAFF  the emoji planes, from the tile and card symbols through the
                   emoticons, transport, supplemental pictographs and the
                   extended-A block.
  U+2600-U+27BF    Miscellaneous Symbols and Dingbats — the warning sign, the
                   check and cross marks, the weather and zodiac symbols.
  U+2B00-U+2BFF    Miscellaneous Symbols and Arrows — the heavy arrows, the
                   filled stars, the large geometric shapes.
  U+FE0F           the variation selector that asks for emoji presentation, and
  U+200D           the zero-width joiner that binds an emoji sequence. Both are
                   INVISIBLE, so a hit on either is reported by name: they reach
                   a file only as part of a pictograph, and one left behind when
                   its neighbour was deleted is an orphan nobody can see.

WHAT IT DELIBERATELY DOES NOT MATCH:

  Every string literal, rune literal, identifier and any other token. Only
  comment nodes are read, so a product output string carrying a check mark — the
  build progress checklist is one — is out of reach by construction rather than
  by an exception anyone has to maintain.

  Typography and notation, which are load-bearing in constraint prose and cost
  the reader nothing: the em dash, the ellipsis, the section sign, the arrows at
  U+2190-U+21FF, the mathematical operators at U+2200-U+22FF, plus-minus,
  multiplication sign, the box-drawing set at U+2500-U+257F, the block elements
  and the geometric shapes at U+2580-U+25FF, the technical symbols at
  U+2300-U+23FF, and the copyright, registered and trademark signs. A general
  ban on non-ASCII was considered and rejected.

  Test data. A test that proves supplementary-plane or surrogate-pair handling
  has to carry the character it is about; a check that refused it would break the
  only tests proving that handling works.

THE CORPUS, stated rather than inherited from another check:

  A Go file is inspected when its name does not end _test.go and its path holds
  no segment named internal, testdata, vendor or node_modules, and no leading
  segment named tools or scripts.

  _test.go and internal/    Go publishes neither, and the emphasis marker is
                            sanctioned in both.
  testdata/ and vendor/     the Go toolchain never compiles either, and testdata
                            is where a Unicode fixture legitimately lives.
  tools/ and scripts/       private tooling trees of this repository, which no
                            release publishes.

  -corpus all lifts every one of those exclusions. It is a census aid for
  answering "where does this character appear at all" and is never the gate.

FLAGS:
`, toolName, toolName)
	flag.PrintDefaults()
}

// collect expands the given paths into the set of files to inspect, in a stable
// order. A named file that is out of corpus is dropped silently — callers hand
// over whole change sets — but a named path that does not exist is an error,
// because naming it asserts an intent to inspect it.
func collect(args []string, every bool) ([]string, error) {
	seen := make(map[string]bool)
	var out []string
	add := func(p string) {
		if !every && !inCorpus(p) {
			return
		}
		if every && !strings.HasSuffix(p, ".go") {
			return
		}
		if seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, arg := range args {
		info, err := os.Stat(arg)
		if err != nil {
			return nil, fmt.Errorf("path not found: %s", arg)
		}
		if !info.IsDir() {
			add(arg)
			continue
		}
		err = filepath.WalkDir(arg, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == ".git" {
					return fs.SkipDir
				}
				return nil
			}
			add(p)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}

// inCorpus decides whether a path is a surface Go publishes. It is the one home
// for that decision: a caller handing over a change set passes whatever changed
// and lets this function narrow it, rather than keeping a second copy of the
// predicate that agrees today and drifts later.
func inCorpus(path string) bool {
	if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
		return false
	}
	segments := strings.Split(filepath.ToSlash(filepath.Clean(path)), "/")
	for i, seg := range segments {
		switch seg {
		case "internal", "testdata", "vendor", "node_modules":
			return false
		case "tools", "scripts":
			// Anchored at the front, so a published package that happens to hold
			// a directory of this name stays in the corpus while the two private
			// tooling trees of this repository stay out. A leading "." from a
			// path written as ./pkg/... is not a segment of its own for this
			// purpose.
			if i == 0 || (i == 1 && segments[0] == ".") {
				return false
			}
		}
	}
	return true
}

// scan parses one file and returns a report line for every matching rune found
// inside a comment. A file that parses only partially still yields the comments
// the parser recovered, with the shortfall stated on stderr: a syntax error is
// the compiler's finding to report, not this one's, and failing here would turn
// every mid-edit save into a refusal.
func scan(path string, wanted rune) ([]string, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	file, perr := parser.ParseFile(fset, path, src, parser.ParseComments|parser.AllErrors)
	if file == nil {
		return nil, fmt.Errorf("did not parse: %w", perr)
	}
	if perr != nil {
		fmt.Fprintf(os.Stderr, "%s: %s: did not parse cleanly, so comment recovery may be partial: %v\n", toolName, path, perr)
	}

	var out []string
	for _, group := range file.Comments {
		for _, comment := range group.List {
			out = append(out, scanComment(fset, comment, wanted)...)
		}
	}
	return out, nil
}

func scanComment(fset *token.FileSet, comment *ast.Comment, wanted rune) []string {
	var out []string
	for offset, r := range comment.Text {
		if wanted >= 0 {
			if r != wanted {
				continue
			}
		} else if !isPictograph(r) {
			continue
		}
		pos := fset.Position(comment.Slash + token.Pos(offset))
		out = append(out, fmt.Sprintf("%s:%d:%d: U+%04X %s in a comment", pos.Filename, pos.Line, pos.Column, r, render(r)))
	}
	return out
}

// isPictograph reports whether a rune belongs to the barred class. The ranges
// are enumerated rather than derived from a Unicode property because the
// standard library carries no emoji property table, and a hand-drawn class has
// to be readable to be arguable — the help text lists the same ranges with the
// reason each is in or out.
func isPictograph(r rune) bool {
	switch {
	case r >= 0x1F000 && r <= 0x1FAFF:
		return true
	case r >= 0x2600 && r <= 0x27BF:
		return true
	case r >= 0x2B00 && r <= 0x2BFF:
		return true
	case r == 0xFE0F, r == 0x200D:
		return true
	}
	return false
}

// render describes a rune for a report line. The two invisible members of the
// class get a name, because printing them shows the reader nothing at all and a
// report nobody can read is a report nobody acts on.
func render(r rune) string {
	switch r {
	case 0xFE0F:
		return "(variation selector for emoji presentation, invisible)"
	case 0x200D:
		return "(zero-width joiner, invisible)"
	}
	return string(r)
}
