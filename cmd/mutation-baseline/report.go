// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// PackageReport aggregates outcomes for one package.
type PackageReport struct {
	Package    string
	Total      int
	Killed     int
	Survived   int
	Equivalent int
	Score      float64 // killed / (killed + survived); 0 when both are 0.
}

// AggregateByPackage groups results by package and computes per-package
// scores. The score denominator excludes equivalent mutants — those
// don't represent detectable behavior changes.
func AggregateByPackage(results []Result, aggregaCliRoot string) map[string]*PackageReport {
	reports := map[string]*PackageReport{}
	for _, r := range results {
		pkg := packageRelPath(r.Mutation.File, aggregaCliRoot)
		rep, ok := reports[pkg]
		if !ok {
			rep = &PackageReport{Package: pkg}
			reports[pkg] = rep
		}
		rep.Total++
		switch r.Outcome {
		case OutcomeKilled:
			rep.Killed++
		case OutcomeSurvived:
			rep.Survived++
		case OutcomeEquivalent:
			rep.Equivalent++
		}
	}
	for _, rep := range reports {
		nonEquivalent := rep.Killed + rep.Survived
		if nonEquivalent > 0 {
			rep.Score = float64(rep.Killed) / float64(nonEquivalent)
		}
	}
	return reports
}

// packageRelPath returns the package path relative to the aggrega-cli
// module root, suitable for table column display. Falls back to the
// directory name when the file is outside the module tree.
func packageRelPath(file, aggregaCliRoot string) string {
	idx := strings.Index(file, aggregaCliRoot)
	if idx < 0 {
		return file
	}
	rel := file[idx+len(aggregaCliRoot):]
	rel = strings.TrimPrefix(rel, "/")
	// Drop the trailing filename component.
	if slash := strings.LastIndex(rel, "/"); slash >= 0 {
		rel = rel[:slash]
	}
	return rel
}

// WriteMarkdown emits the baseline report to w. The report carries:
//
//   - A header (date, commit hash).
//   - A per-package score table.
//   - A per-package surviving-mutations enumeration (file:line, operator,
//     original→mutated).
//   - Two operator-authored template sections ("Acceptable survivors" and
//     "Actionable survivors") for the operator to fill in after review.
func WriteMarkdown(w io.Writer, results []Result, aggregaCliRoot, commit, date string) error {
	reports := AggregateByPackage(results, aggregaCliRoot)
	pkgs := sortedKeys(reports)

	if _, err := fmt.Fprintf(w, "# Mutation Baseline\n\n"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Established: %s  \n", date); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Compiler commit: `%s`\n\n", commit); err != nil {
		return err
	}

	// Totals.
	var totK, totS, totE int
	for _, r := range reports {
		totK += r.Killed
		totS += r.Survived
		totE += r.Equivalent
	}
	totalScore := 0.0
	if totK+totS > 0 {
		totalScore = float64(totK) / float64(totK+totS)
	}
	if _, err := fmt.Fprintf(w, "Overall: %d killed, %d survived, %d equivalent — score **%.1f%%**\n\n",
		totK, totS, totE, totalScore*100); err != nil {
		return err
	}

	if _, err := fmt.Fprintln(w, "## Per-package mutation scores"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, ""); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| Package | Mutants | Killed | Survived | Equivalent | Score |"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "|---|---|---|---|---|---|"); err != nil {
		return err
	}
	for _, pkg := range pkgs {
		r := reports[pkg]
		scoreStr := "—"
		if r.Killed+r.Survived > 0 {
			scoreStr = fmt.Sprintf("%.1f%%", r.Score*100)
		}
		if _, err := fmt.Fprintf(w, "| `%s` | %d | %d | %d | %d | %s |\n",
			r.Package, r.Total, r.Killed, r.Survived, r.Equivalent, scoreStr); err != nil {
			return err
		}
	}

	// Surviving mutations grouped by package.
	survByPkg := map[string][]Result{}
	for _, r := range results {
		if r.Outcome != OutcomeSurvived {
			continue
		}
		pkg := packageRelPath(r.Mutation.File, aggregaCliRoot)
		survByPkg[pkg] = append(survByPkg[pkg], r)
	}

	if _, err := fmt.Fprintln(w, "\n## Surviving mutations"); err != nil {
		return err
	}
	if len(survByPkg) == 0 {
		if _, err := fmt.Fprintln(w, "\n_None — every mutation was killed by the test suite._"); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintln(w, ""); err != nil {
			return err
		}
		for _, pkg := range sortedKeys(survByPkg) {
			if _, err := fmt.Fprintf(w, "### `%s`\n\n", pkg); err != nil {
				return err
			}
			for _, r := range survByPkg[pkg] {
				m := r.Mutation
				rel := relativeFile(m.File, aggregaCliRoot)
				if _, err := fmt.Fprintf(w, "- `%s:%d:%d` — %s: `%s` → `%s`\n",
					rel, m.Position.Line, m.Position.Column,
					m.Operator, m.Original, m.Replacement); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintln(w, ""); err != nil {
				return err
			}
		}
	}

	if _, err := fmt.Fprintln(w, "## Acceptable survivors"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "\n_Operator-authored. List which surviving mutations are known-acceptable and why (e.g., defensive guards whose alternative-branch behavior is exercised at a higher integration layer)._"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "## Actionable survivors"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "\n_Operator-authored. List which surviving mutations indicate test gaps and need follow-on test additions._"); err != nil {
		return err
	}
	return nil
}

// relativeFile returns the file path relative to aggregaCliRoot for
// concise display in the report.
func relativeFile(file, root string) string {
	idx := strings.Index(file, root)
	if idx < 0 {
		return file
	}
	rel := file[idx+len(root):]
	return strings.TrimPrefix(rel, "/")
}

// sortedKeys returns the keys of any map[string]T in lexicographic order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
