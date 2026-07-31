// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run . <output-file>")
		os.Exit(2)
	}
	out := os.Args[1]
	content, err := generateFor(filepath.Base(out))
	if err != nil {
		fmt.Fprintf(os.Stderr, "factgen: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(out, content, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "factgen: write %s: %v\n", out, err)
		os.Exit(1)
	}
	report(os.Stdout, out)
}

// targets maps each output file's base name to its renderer. It is the one place
// a target is registered, so nothing that names one — a generate directive, a
// drift gate, a caller passing a path — can name a target the generator does not
// render, and an unregistered base name is refused rather than written.
var targets = map[string]func(ladder, []kind, []tupleShare) ([]byte, error){
	"views_gen.go": func(l ladder, _ []kind, _ []tupleShare) ([]byte, error) {
		return emitViews(l)
	},
	"subjectkinds_gen.go": func(_ ladder, kinds []kind, _ []tupleShare) ([]byte, error) {
		return emitSubjectKinds(kinds)
	},
	"constraint-placement-table.md": emitPlacementTable,
}

// generateFor derives the ladder, the subject-kind set and the tuple shares from
// the registry, and renders one target.
//
// Every derivation runs for every target, so a registry that does not derive —
// an unoccupied rung, a rung chain with an unordered pair in it, a fact naming
// no carrier — halts generation of all of them rather than only of the one that
// happens to read the broken part.
func generateFor(base string) ([]byte, error) {
	render, ok := targets[base]
	if !ok {
		names := make([]string, 0, len(targets))
		for n := range targets {
			names = append(names, n)
		}
		return nil, fmt.Errorf("no target named %q; registered targets are %s",
			base, strings.Join(sorted(names), ", "))
	}
	if err := validateFacts(); err != nil {
		return nil, err
	}
	l, err := deriveLadder()
	if err != nil {
		return nil, err
	}
	kinds, err := deriveKinds()
	if err != nil {
		return nil, err
	}
	return render(l, kinds, deriveTupleShares(facts))
}

// report prints what the run derived, and the facts sharing a rung, a source
// carrier and a subject.
//
// The share list is printed on every run rather than written to a file of its
// own, because it is a prompt to a reader rather than a fact about the tree: a
// share is a candidate for same-semantics-under-a-different-name duplication and
// never proof of it. It is also carried in the placement table, where review
// reaches it without running the generator.
func report(w *os.File, out string) {
	l, err := deriveLadder()
	if err != nil {
		return
	}
	kinds, _ := deriveKinds()
	shares := deriveTupleShares(facts)

	fmt.Fprintf(w, "factgen: wrote %s\n", out)
	fmt.Fprintf(w, "factgen: %d facts over %d rungs, keyed by %d subject kinds\n",
		len(facts), len(l.order), len(kinds))
	if len(shares) == 0 {
		fmt.Fprintln(w, "factgen: no two facts share a (rung, source carrier, subject) tuple")
		return
	}
	fmt.Fprintf(w, "factgen: %d tuple(s) shared by more than one fact — review each, a share is not proof of duplication:\n",
		len(shares))
	for _, s := range shares {
		fmt.Fprintf(w, "factgen:   (%s, %s, %s): %s\n",
			s.Rung, s.SourceField, s.Subject, strings.Join(s.Facts, ", "))
	}
}

func sorted(ss []string) []string {
	out := append([]string(nil), ss...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
