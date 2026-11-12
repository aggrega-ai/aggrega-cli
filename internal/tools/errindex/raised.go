// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"io"
	"sort"
	"strings"
)

// collectRaiseSites records what this file's production code reaches: into
// raised, the handle of every identity it invokes an accessor for; into named,
// every package-local name it mentions, qualified by the package that mentions
// it.
//
// # A raise has two spellings, and only one of them is the accessor
//
// An identity is published by an accessor, and a caller in ANOTHER package can
// reach it no other way — so from outside, invoking the accessor is what every
// raise has in common. Inside the declaring package it is the opposite: the
// constructor a raise goes through takes the registered identity, the accessor
// yields a plain error, and passing the accessor there does not type-check. What
// the raise names is the package-local binding the registration produced, and
// the accessor is not written at the site at all.
//
// So a detector reading invocations alone is blind to every condition raised
// where it is declared, which is most of them. Both spellings are read: the
// invocation directly, and the mention resolved afterwards by [scan], which
// joins named against the binding each accessor's own declaration returns. The
// binding is never guessed from a lowercase-twin spelling — that rule agrees
// with this tree today, and the day it stops agreeing every raise through the
// renamed binding would go uncounted while the report still read as clean.
//
// The mention side is recorded here and resolved there because a binding is
// package-scoped while the accessor publishing it may be declared in a different
// file of that package. Resolving per file would make the answer depend on the
// order the walk happened to reach the two, which is a result about the
// filesystem rather than about the tree.
//
// 🔴 The syntactic position is deliberately not read, and this is the whole
// design. A raise detector that recognises shapes — a return statement, a
// wrapping call, a value inside a function body — misses the sentinel bound
// into a package-level table that a shared dispatcher later returns, and a
// condition raised that way is as live as any other. Reading the position at
// all is what creates that blind spot, so nothing here reads it: every
// occurrence counts, wherever the expression stands.
//
// # What is excluded, and why each exclusion is load-bearing
//
// Each is excluded because it is a different act rather than because of where it
// sits, and the first two are what keep this tool able to report anything:
//
//   - An accessor's own declaration. `func ErrX() error { return errX }`
//     publishes the identity and raises nothing. Under the invocation reading
//     it is the re-export shape `func ErrX() error { return other.ErrY() }`
//     that would otherwise make every re-export a raise of its origin; under
//     the mention reading it is worse, because EVERY accessor names its own
//     binding, so crediting it would report every accessor-published condition
//     as raised and the unraised report could never again have a member.
//   - A value specification's own names. `var errX = Register(…)` binds the very
//     name the accessor publishes, so reading the name side of a declaration
//     would credit every registration with raising itself — the same erasure
//     arriving from the other half of the declaration. Only the values are
//     read. Every other binding position is excluded with it, for the narrower
//     reason that a name being declared is not a name being used: a parameter,
//     a short variable declaration, a type, a label, an import alias.
//   - A name after a dot. A binding is unexported and package-scoped, so a
//     selected name is a field or a method and never the binding; reading one
//     as a binding would credit a rule to a struct field that happens to share
//     the name.
//   - An argument to a matching call — `errors.Is(err, ErrX())`, its bare
//     `errors.Is(err, errX)` counterpart, and their As siblings. Matching a
//     condition is the opposite of raising one: it asks whether something else
//     already did. A condition only ever matched is a check that can never
//     succeed, which is precisely what this tool exists to surface rather than
//     to hide. Both argument spellings are disqualified, because the mention
//     reading is what makes the bare one reachable at all.
//
// The cost of the mention reading, stated rather than left to be discovered: a
// local variable shadowing a binding's name, in the binding's own package, would
// be read as naming it. The binding positions above remove the declarations that
// introduce such a local, so what is left is a use of the shadow, and the name
// has to match a registration's local name exactly for the join to reach it.
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
func collectRaiseSites(raised, named map[string]bool, file *ast.File, pkgDir string, imports map[string]string) {
	// Marked on the way down, read on the way back into the subtree: ast.Inspect
	// visits a node before its children, so a matching call can disqualify the
	// occurrences it encloses, and a declaration can disqualify the name it
	// binds, before either is reached.
	matched := map[ast.Node]bool{}
	bound := map[*ast.Ident]bool{}

	for _, decl := range file.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok {
			if _, _, isAccessor := accessor(fd, imports); isAccessor {
				continue
			}
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CallExpr:
				if matchesRatherThanRaises(x) {
					for _, arg := range x.Args {
						matched[arg] = true
					}
					return true
				}
				if matched[x] {
					return true
				}
				if h, isAccessorCall := invokedHandle(x, pkgDir, imports); isAccessorCall {
					raised[h] = true
				}
			case *ast.Ident:
				if matched[x] || bound[x] {
					return true
				}
				named[pkgDir+"."+x.Name] = true
			case *ast.SelectorExpr:
				bound[x.Sel] = true
			case *ast.ValueSpec:
				for _, name := range x.Names {
					bound[name] = true
				}
			case *ast.Field:
				for _, name := range x.Names {
					bound[name] = true
				}
			case *ast.AssignStmt:
				if x.Tok != token.DEFINE {
					return true
				}
				for _, lhs := range x.Lhs {
					if id, isIdent := lhs.(*ast.Ident); isIdent {
						bound[id] = true
					}
				}
			case *ast.FuncDecl:
				bound[x.Name] = true
			case *ast.TypeSpec:
				bound[x.Name] = true
			case *ast.ImportSpec:
				if x.Name != nil {
					bound[x.Name] = true
				}
			case *ast.LabeledStmt:
				bound[x.Label] = true
			case *ast.BranchStmt:
				if x.Label != nil {
					bound[x.Label] = true
				}
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
// Only the DIRECT arguments of such a call are disqualified, and every spelling
// of one is: an invocation, and a bare mention of a package-local binding. The
// bare spelling is the one a caller inside the declaring package writes, so
// disqualifying only the invocation would let a condition that is exclusively
// matched there read as raised. A deeper expression is left alone, so an
// occurrence nested inside something that happens to be passed to a matcher
// still counts as the raise it is.
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
