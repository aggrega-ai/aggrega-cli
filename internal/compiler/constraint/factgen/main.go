// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/docsplice"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run . <output-file>")
		os.Exit(2)
	}
	out := os.Args[1]
	base := filepath.Base(out)

	// A splice target refreshes one marked region of a page that is otherwise
	// hand-authored, so it reads the file it is about to write. A whole-file
	// target does not read at all. The two paths are kept separate rather than
	// generalised into one: the whole-file path already has a drift gate over
	// three committed artifacts, and giving it a second responsibility is how a
	// working gate acquires a regression.
	if region, ok := spliceTargets[base]; ok {
		if err := spliceInto(out, region); err != nil {
			fmt.Fprintf(os.Stderr, "factgen: %v\n", err)
			os.Exit(1)
		}
		report(os.Stdout, out)
		return
	}

	content, err := generateFor(base)
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

// spliceTargets maps an output file's base name to the id of the generated region
// this generator owns within it, alongside the renderer for that region.
//
// It is separate from targets because the output is a region rather than a file:
// the page's curated prose is preserved and only the fenced region is rewritten.
// As with targets, it is the one place such a target is registered, so nothing
// naming one — a drift gate, a caller passing a path — can name a region the
// generator does not render.
var spliceTargets = map[string]spliceTarget{
	"constraint-framework.md": {
		region: "constraint-subject-kinds",
		render: emitSubjectKindsRegion,
	},
}

// spliceTarget is one generated region: the id fencing it in the page, and the
// renderer producing its contents.
type spliceTarget struct {
	region string
	render func(chain, []kind, []tupleShare) ([]byte, error)
}

// spliceInto refreshes one generated region of the page at path.
//
// The page must already exist and must already carry the region's markers. That
// is deliberate: a splice target refreshes a region a document author declared,
// so creating the page — or appending a region to a page that does not fence one
// — would be this generator deciding where its output belongs in someone else's
// document.
func spliceInto(path string, t spliceTarget) error {
	existing, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	content := string(existing)

	if !strings.Contains(content, fmt.Sprintf("<!-- BEGIN GENERATED: %s -->", t.region)) {
		return fmt.Errorf("%s declares no %q region: add the markers to the page first",
			filepath.Base(path), t.region)
	}

	inner, err := renderRegion(t.render)
	if err != nil {
		return err
	}
	if _, err := docsplice.WriteFile(path, content, t.region, string(inner)); err != nil {
		return err
	}
	return nil
}

// renderRegion runs the same validations and derivations every whole-file target
// runs before rendering, so a registry that does not derive halts a region
// refresh exactly as it halts a file generation.
func renderRegion(render func(chain, []kind, []tupleShare) ([]byte, error)) ([]byte, error) {
	if err := validateFacts(); err != nil {
		return nil, err
	}
	c, err := deriveChain()
	if err != nil {
		return nil, err
	}
	kinds, err := deriveKinds()
	if err != nil {
		return nil, err
	}
	return render(c, kinds, deriveTupleShares(facts))
}

// targets maps each output file's base name to its renderer. It is the one place
// a target is registered, so nothing that names one — a generate directive, a
// drift gate, a caller passing a path — can name a target the generator does not
// render, and an unregistered base name is refused rather than written.
var targets = map[string]func(chain, []kind, []tupleShare) ([]byte, error){
	"views_gen.go": func(c chain, _ []kind, _ []tupleShare) ([]byte, error) {
		return emitViews(c)
	},
	"subjectkinds_gen.go": func(_ chain, kinds []kind, _ []tupleShare) ([]byte, error) {
		return emitSubjectKinds(kinds)
	},
	"constraint-placement-table.md": emitPlacementTable,
}

// generateFor derives the stage chain, the subject-kind set and the tuple shares
// from the registry, and renders one target.
//
// Every derivation runs for every target, so a registry that does not derive —
// an unoccupied stage, a stage chain with an unordered pair in it, a fact naming
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
	c, err := deriveChain()
	if err != nil {
		return nil, err
	}
	kinds, err := deriveKinds()
	if err != nil {
		return nil, err
	}
	return render(c, kinds, deriveTupleShares(facts))
}

// report prints what the run derived, and the facts sharing a stage, a source
// carrier and a subject.
//
// The share list is printed on every run rather than written to a file of its
// own, because it is a prompt to a reader rather than a fact about the tree: a
// share is a candidate for same-semantics-under-a-different-name duplication and
// never proof of it. It is also carried in the placement table, where review
// reaches it without running the generator.
func report(w *os.File, out string) {
	c, err := deriveChain()
	if err != nil {
		return
	}
	kinds, _ := deriveKinds()
	shares := deriveTupleShares(facts)

	fmt.Fprintf(w, "factgen: wrote %s\n", out)
	fmt.Fprintf(w, "factgen: %d facts over %d stages, keyed by %d subject kinds\n",
		len(facts), len(c.order), len(kinds))
	if len(shares) == 0 {
		fmt.Fprintln(w, "factgen: no two facts share a (stage, source carrier, subject) tuple")
		return
	}
	fmt.Fprintf(w, "factgen: %d tuple(s) shared by more than one fact — review each, a share is not proof of duplication:\n",
		len(shares))
	for _, s := range shares {
		fmt.Fprintf(w, "factgen:   (%s, %s, %s): %s\n",
			s.Stage, s.SourceField, s.Subject, strings.Join(s.Facts, ", "))
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
