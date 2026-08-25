// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"go/ast"
	"io"
	"sort"
	"strings"
)

// collectRaiseSites records, into raised, the handle of every identity this
// file's production code invokes.
//
// An identity is reached through the accessor that publishes it, so invoking
// that accessor is the one thing every raise has in common: the value has to be
// produced before it can be returned, wrapped, joined, composed into a message,
// or bound into a table something else returns. That is why this counts
// INVOCATIONS and asks nothing about where the invocation sits.
//
// 🔴 The syntactic position is deliberately not read, and this is the whole
// design. A raise detector that recognises shapes — a return statement, a
// wrapping call, a value inside a function body — misses the sentinel bound
// into a package-level table that a shared dispatcher later returns, and a
// condition raised that way is as live as any other. Reading the position at
// all is what creates that blind spot, so nothing here reads it: every
// invocation counts, wherever the expression stands.
//
// Two occurrences are excluded, and each is excluded because it is a different
// act rather than because of where it sits:
//
//   - An accessor's own declaration. `func ErrX() error { return errX }`
//     publishes the identity and raises nothing; the re-export shape
//     `func ErrX() error { return other.ErrY() }` is an invocation, and
//     counting it would make every re-export a raise of its origin, so every
//     re-exported condition would read as raised whether or not anything ever
//     reaches it.
//   - An argument to a matching call — `errors.Is(err, ErrX())` and its As
//     counterpart. Matching a condition is the opposite of raising one: it asks
//     whether something else already did. A condition only ever matched is a
//     check that can never succeed, which is precisely what this tool exists to
//     surface rather than to hide.
//
// # Guest entry points are read here, and are not read for declarations
//
// [scan] skips a WebAssembly guest's entry point when reading declarations,
// because what such a file declares is unreachable by an ordinary import and so
// publishes nothing a caller can match. That reasoning does not carry over to
// raising. A guest's entry point is production code that runs, and a condition
// declared in the shared core beside it and raised only from the guest is
// raised — so excluding those files here would drop a live condition off the
// reference for no better reason than where its one raise site happens to sit.
// The two questions get the two file sets they each need.
func collectRaiseSites(raised map[string]bool, file *ast.File, pkgDir string, imports map[string]string) {
	// Marked on the way down, read on the way back into the subtree: ast.Inspect
	// visits a call before its arguments, so a matching call can disqualify the
	// invocations it encloses before they are reached.
	matched := map[ast.Node]bool{}

	for _, decl := range file.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok {
			if _, _, isAccessor := accessor(fd, imports); isAccessor {
				continue
			}
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if matchesRatherThanRaises(call) {
				for _, arg := range call.Args {
					if inner, isCall := arg.(*ast.CallExpr); isCall {
						matched[inner] = true
					}
				}
				return true
			}
			if matched[call] {
				return true
			}
			if h, isAccessorCall := invokedHandle(call, pkgDir, imports); isAccessorCall {
				raised[h] = true
			}
			return true
		})
	}
}

// matchesRatherThanRaises reports whether call asks whether an error already
// carries a condition, rather than producing one.
//
// It recognises the two standard-library matchers by their selector alone —
// `<something>.Is(…)` and `<something>.As(…)` — and does not check which package
// the selector names. Reading the package would mean resolving an import alias
// for a rule that gains nothing from it: no shape in this module raises a
// condition through a call named Is or As, so the selector is already decisive,
// and a check that also matched the package would silently stop excluding
// anything the day the standard library is imported under a name this tool did
// not anticipate.
//
// Only the DIRECT arguments of such a call are disqualified. A deeper
// expression is left alone, so an invocation nested inside something that
// happens to be passed to a matcher still counts as the raise it is.
func matchesRatherThanRaises(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if sel.Sel.Name != "Is" && sel.Sel.Name != "As" {
		return false
	}
	_, ok = sel.X.(*ast.Ident)
	return ok
}

// invokedHandle returns the (package, name) handle the call invokes, for the two
// forms an accessor is reached by:
//
//	ErrX()          // declared in this package
//	otherpkg.ErrX() // declared in another package of this module
//
// The handle is package-qualified in both, which is what keeps two same-named
// conditions in different packages apart. A bare-name key would merge them, and
// the merge is invisible: one condition's raise site would silently vouch for
// the other, and a condition nothing raises would read as raised.
//
// The empty argument list is checked because that is an accessor's signature. It
// is what separates `ErrX()` from a same-prefixed helper that takes arguments,
// and it costs nothing: a call this rejects names no identity anyway, because
// the join against the declared accessors would drop it.
//
// A selector whose qualifier resolves to no in-module import yields nothing, so
// a standard-library or third-party error value spelled the same way — there are
// several — cannot enter the raised set.
func invokedHandle(call *ast.CallExpr, pkgDir string, imports map[string]string) (string, bool) {
	if len(call.Args) != 0 {
		return "", false
	}
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		if !strings.HasPrefix(fun.Name, "Err") {
			return "", false
		}
		return pkgDir + "." + fun.Name, true
	case *ast.SelectorExpr:
		if !strings.HasPrefix(fun.Sel.Name, "Err") {
			return "", false
		}
		qualifier, ok := fun.X.(*ast.Ident)
		if !ok {
			return "", false
		}
		dir, ok := imports[qualifier.Name]
		if !ok {
			return "", false
		}
		return dir + "." + fun.Sel.Name, true
	}
	return "", false
}

// admit partitions the declared identities into the ones the published reference
// carries and the ones it does not.
//
// The rule is [Identity.admitted] and it is applied here, once, so that every
// page rendered from the declared set carries the same corpus. A renderer
// applying the rule for itself would be a second copy of it, and two copies of
// an admission rule disagree the day one of them gains a case.
//
// Both halves are returned. The excluded half is not a remainder to be
// discarded: it is the report, and a caller that took only the published set
// would have nothing left to name the exclusions with — which is the silent
// exclusion this whole mechanism exists to prevent.
//
// Order is preserved from the input, which [scan] has already sorted by package
// then name, so both halves are a function of the tree alone.
func admit(ids []Identity) (published, excluded []Identity) {
	for _, id := range ids {
		if id.admitted() {
			published = append(published, id)
			continue
		}
		excluded = append(excluded, id)
	}
	return published, excluded
}

// declaredAheadIn returns every identity carrying a declared-ahead note, in the
// order given.
//
// Re-exports are dropped. A re-export reports the note of the identity it
// forwards to, so keeping it would name one condition twice under a population
// that never grew — the same reason the gap tier counts origins only.
func declaredAheadIn(ids []Identity) []Identity {
	var out []Identity
	for _, id := range ids {
		if id.AliasOf != "" || id.DeclaredAhead == "" {
			continue
		}
		out = append(out, id)
	}
	return out
}

// reportUnpublished writes the admission tier to w: every declared condition the
// published reference does not carry, and every condition it carries only
// because the registration names the work that will raise it.
//
// # Both lists are named in full, on every run
//
// Not counted, and not behind a flag. The defect this tier exists to repair is
// that a condition could be registered, raised by nothing and documented by
// nothing, and be invisible in all three places at once — so an exclusion that
// reported only a number would move the invisibility rather than remove it.
// Naming them is affordable because the rule keeps the list short by
// construction: a condition leaves it by acquiring a raise site or a note, and
// both are things somebody writes.
//
// The declared-ahead list is printed for the same reason and is not a
// suppression list. A condition on it IS published, carrying its note; naming it
// here is what keeps the set of deliberately-unraised conditions countable
// instead of letting a note become a way to stop being counted.
//
// # A documented condition that is not published is called out separately
//
// The halting-conditions page declares which conditions it documents, and that
// corpus is the page's own rather than the declared set. So a condition can be
// excluded from the reference while an entry on that page still teaches its
// authoring fix — an entry describing a check the build cannot perform. It is
// reported rather than refused: refusing would take the page's entry down with
// it, and an author who hit the behaviour the entry describes needs that entry
// more than the reference needs to be tidy. The repair is to raise the
// condition, note it, or remove it and its entry together, and naming it here is
// what makes the choice visible.
//
// # Why this never fails the run
//
// It reports and does not change the exit code, for the reason the gap and claim
// tiers do not: a check that is red on the day it ships stays red and gets
// switched off. The admission rule itself is not advisory — an excluded
// condition is off the page whatever anyone does about this report — so the
// enforcement is structural and this is the enumeration beside it.
func reportUnpublished(w io.Writer, ids []Identity, covered map[string]bool) {
	_, excluded := admit(ids)
	origins := make([]Identity, 0, len(excluded))
	for _, id := range excluded {
		// A re-export is the same condition under a second name and is admitted
		// or excluded with its origin, so naming it here would report one
		// condition as two exclusions.
		if id.AliasOf != "" {
			continue
		}
		origins = append(origins, id)
	}

	if len(origins) == 0 {
		fmt.Fprintf(w, "WARN: none — every declared halting condition is raised, or names the work that will raise it\n")
	} else {
		fmt.Fprintf(w, "WARN: %d declared halting %s raised by nothing and %s no work that will, "+
			"so the published reference does not carry %s:\n",
			len(origins),
			plural(len(origins), "condition is", "conditions are"),
			plural(len(origins), "names", "name"),
			plural(len(origins), "it", "them"))
		for _, id := range origins {
			fmt.Fprintf(w, "WARN:   %s (%s): %s\n", id.Handle(), id.ShortCode, id.Message)
		}
	}

	// Sorted by handle rather than left in package order, because this list is
	// read against itself between runs and an order that moves for an unrelated
	// reason makes two runs incomparable.
	var documented []string
	for _, id := range origins {
		if covered[id.Handle()] {
			documented = append(documented, id.Handle())
		}
	}
	if len(documented) > 0 {
		sort.Strings(documented)
		fmt.Fprintf(w, "WARN:   of those, %d %s documented by an entry on %s that teaches an authoring fix for a "+
			"check the build cannot perform: %s\n",
			len(documented), plural(len(documented), "is", "are"), haltsRelPath, strings.Join(documented, ", "))
	}

	ahead := declaredAheadIn(ids)
	if len(ahead) == 0 {
		return
	}
	fmt.Fprintf(w, "WARN: %d declared halting %s published as declared-ahead, "+
		"carrying the work that will raise %s:\n",
		len(ahead),
		plural(len(ahead), "condition is", "conditions are"),
		plural(len(ahead), "it", "them"))
	for _, id := range ahead {
		fmt.Fprintf(w, "WARN:   %s (%s): %s\n", id.Handle(), id.ShortCode, id.DeclaredAhead)
	}
}

// plural picks the verb form for n, so a report line reads as English for a
// single item as well as for several. A report nobody can read comfortably is a
// report that stops being read.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
