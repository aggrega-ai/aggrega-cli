// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package exampleops

import (
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathsegment"
)

// Form is which of the scaffold capability's forms a request asks for.
type Form int

const (
	// FormDraft drafts a starter document for an entity kind.
	FormDraft Form = iota
	// FormList lists the runnable examples the documentation publishes.
	FormList
	// FormWrite writes one runnable example into a new directory.
	FormWrite
)

// RequestForm returns which form a request carrying the scaffold capability's
// fields by name asks for — kind and name for drafting a starter document, list
// for listing the examples, example and dir for writing one — and refuses a
// request combining fields of two forms.
//
// It is the one statement of those combinations for every surface that takes
// the capability as named fields — the assistant tool and the editor daemon's
// method — so a request combining them is refused in the same words on both.
// The fields are named as both surfaces name them. A value of only white space
// is taken as absent, as both surfaces take a blank kind.
//
// A request carrying none of the fields is the drafting form: what it lacks is
// the kind, and each surface refuses that in words of its own.
func RequestForm(kind, name, example, dir string, list bool) (Form, error) {
	kind, name = strings.TrimSpace(kind), strings.TrimSpace(name)
	example, dir = strings.TrimSpace(example), strings.TrimSpace(dir)
	switch {
	case kind != "" && example != "":
		return FormDraft, errors.New("`kind` and `example` ask for two different things — a starter document " +
			"drafted for an entity kind, and a runnable example written into a new directory; send one of them")
	case list && example != "":
		return FormDraft, errors.New("`list` lists the runnable examples and writes nothing, and `example` names " +
			"one to write; send `list` alone to list them, or `example` alone to write one")
	case list && kind != "":
		return FormDraft, errors.New("`list` lists the runnable examples, and `kind` names an entity kind to " +
			"draft a starter document for; send one of them")
	case name != "" && (example != "" || list):
		return FormDraft, errors.New("`name` seeds the name of a drafted starter document and is sent only with " +
			"`kind`; the directory an example is written into is named by `dir`")
	case dir != "" && example == "":
		return FormDraft, errors.New("`dir` names the new directory a runnable example is written into, and is " +
			"sent only with `example`")
	case list:
		return FormList, nil
	case example != "":
		return FormWrite, nil
	}
	return FormDraft, nil
}

// CheckName refuses a name that cannot name an example: one that is not a single
// directory name. An example is named by its directory, as the list of examples
// prints it, so a name with a separator, a dot entry or an empty one names none,
// and is refused as a page the bundle does not carry (docscache.ErrPageNotFound).
//
// A surface that composes a destination from the name — the directory named
// after the example, where none was given — asks this first, so a name that would
// lead a path anywhere is refused as a name rather than judged as a path.
func CheckName(name string) error {
	if pathsegment.Valid(name) {
		return nil
	}
	return fmt.Errorf("%q is not an example name — an example is named by its one directory name, "+
		"as the list of examples prints it: %w", name, docscache.ErrPageNotFound())
}

// Note returns the sentence telling a reader that the examples are read from an
// earlier version's documentation, because none is published for the version
// asked for, and the empty string where the version read is the one asked for.
func (e *Examples) Note() string {
	if e.Version == e.Requested {
		return ""
	}
	return fmt.Sprintf("no documentation is published for %s; the examples are read from the documentation "+
		"published for %s, the newest before it", e.Requested, e.Version)
}

// ListReport returns the list of examples as a reader is shown it: a header row,
// then one row per example with its name, its level and the first sentence of its
// page, in columns.
func ListReport(examples []Example) string {
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "EXAMPLE\tLEVEL\tDESCRIPTION")
	for _, e := range examples {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", e.Name, e.Level, e.Summary)
	}
	_ = tw.Flush()
	return b.String()
}

// Report returns the account of an example written out as a reader is shown it:
// where it went and how many files, then the commands to run next — changing into
// the directory, then the lines its page gives under its Build heading — and the
// page to read. Where the page gives no such lines, the account sends the reader
// to the page instead.
//
// The directory is named as w.Dir names it, so a surface that resolved the
// directory a request named sets Dir back to the request's own spelling first.
func (w Written) Report() string {
	var b strings.Builder
	files := "files"
	if len(w.Files) == 1 {
		files = "file"
	}
	fmt.Fprintf(&b, "Wrote the example %s into %s (%d %s).\n\nNext:\n\n  cd %s\n",
		w.Name, w.Dir, len(w.Files), files, w.Dir)
	if len(w.Next) == 0 {
		fmt.Fprintf(&b, "\nIts page, %s/README.md, says how to build it.\n", w.Dir)
		return b.String()
	}
	for _, line := range w.Next {
		if line == "" {
			b.WriteString("\n")
			continue
		}
		fmt.Fprintf(&b, "  %s\n", line)
	}
	fmt.Fprintf(&b, "\nIts page, %s/README.md, explains what it shows.\n", w.Dir)
	return b.String()
}
