// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"strings"
)

// ladder is the rung set in containment order, root first, once the declared
// Extends relation has been checked to be a single chain.
type ladder struct {
	order []rungDecl
	facts map[string][]factDecl
}

// kind is one derived subject kind: the coordinate list some fact is keyed by.
type kind struct {
	Coordinates []string
	Facts       []string
}

// Display renders the kind the way the placement table and the generated
// documentation name it: a bare coordinate when there is one, a parenthesized
// tuple when there are several.
func (k kind) Display() string {
	if len(k.Coordinates) == 1 {
		return k.Coordinates[0]
	}
	return "(" + strings.Join(k.Coordinates, ", ") + ")"
}

// GoName is the kind's generated constant name.
func (k kind) GoName() string {
	var b strings.Builder
	b.WriteString("Subject")
	for _, c := range k.Coordinates {
		b.WriteString(pascal(c))
	}
	return b.String()
}

// Traversal reports how the framework reaches instances of this kind.
//
// It is derived rather than declared. A kind rooted at a document is reached by
// the push model, because documents are read one at a time and the catalog is
// read lazily, so no point in the compilation holds a materialised document set
// to walk. Every other kind is reached by walking a collection that does exist.
func (k kind) Traversal() string {
	if k.Coordinates[0] == "document" {
		return "push"
	}
	return "pull"
}

// tupleShare is a group of facts sharing a rung, a source carrier and a subject
// key.
//
// A share is not proof of duplication and the generator does not treat it as
// one. Same-name duplication is closed by the Go compiler, which will not accept
// two same-named methods on one interface. Same semantics under two different
// names is not closed and there is no mechanism for it; printing the shares is a
// mitigation that turns an invisible risk into a reviewable list.
type tupleShare struct {
	Rung        string
	SourceField string
	Subject     string
	Facts       []string
}

// deriveLadder checks the declared rung relation and returns the rung set in
// containment order together with the facts landing on each rung.
//
// It refuses, rather than repairing, four shapes. A rung nothing occupies, since
// a rung exists if and only if a fact lands there. A fact naming an undeclared
// rung. More than one root, or a rung two others extend — either of which means
// two rungs no containment relation orders, and an unordered pair is exactly
// what makes the earliest rung a fact is complete at un-computable. And a cycle.
func deriveLadder() (ladder, error) {
	byName := make(map[string]rungDecl, len(rungs))
	for _, r := range rungs {
		if r.Name == "" {
			return ladder{}, fmt.Errorf("a rung is declared with no name")
		}
		if _, dup := byName[r.Name]; dup {
			return ladder{}, fmt.Errorf("rung %q is declared twice", r.Name)
		}
		byName[r.Name] = r
	}

	occupants := make(map[string][]factDecl, len(rungs))
	for _, f := range facts {
		if _, ok := byName[f.Rung]; !ok {
			return ladder{}, fmt.Errorf("fact %q names rung %q, which is not declared", f.Name, f.Rung)
		}
		occupants[f.Rung] = append(occupants[f.Rung], f)
	}
	for _, r := range rungs {
		if len(occupants[r.Name]) == 0 {
			return ladder{}, fmt.Errorf(
				"rung %q is declared but no fact lands there; a rung exists only where a fact does, "+
					"so either declare the fact or remove the rung", r.Name)
		}
	}

	var roots []string
	extendedBy := make(map[string][]string, len(rungs))
	for _, r := range rungs {
		if r.Extends == "" {
			roots = append(roots, r.Name)
			continue
		}
		if _, ok := byName[r.Extends]; !ok {
			return ladder{}, fmt.Errorf("rung %q extends %q, which is not declared", r.Name, r.Extends)
		}
		if r.Extends == r.Name {
			return ladder{}, fmt.Errorf("rung %q extends itself", r.Name)
		}
		extendedBy[r.Extends] = append(extendedBy[r.Extends], r.Name)
	}
	if len(roots) != 1 {
		return ladder{}, fmt.Errorf(
			"the rung chain is not linear: %d rungs extend nothing (%s), so no containment relation "+
				"orders them and the earliest rung a fact is complete at is not computable",
			len(roots), strings.Join(roots, ", "))
	}
	for extended, extenders := range extendedBy {
		if len(extenders) > 1 {
			return ladder{}, fmt.Errorf(
				"the rung chain is not linear: rungs %s all extend %q, so no containment relation "+
					"orders them against each other and the earliest rung a fact is complete at is "+
					"not computable", strings.Join(extenders, ", "), extended)
		}
	}

	order := make([]rungDecl, 0, len(rungs))
	seen := make(map[string]bool, len(rungs))
	current := roots[0]
	for {
		if seen[current] {
			return ladder{}, fmt.Errorf("the rung chain cycles at %q", current)
		}
		seen[current] = true
		order = append(order, byName[current])
		next := extendedBy[current]
		if len(next) == 0 {
			break
		}
		current = next[0]
	}
	if len(order) != len(rungs) {
		unreached := make([]string, 0, len(rungs)-len(order))
		for _, r := range rungs {
			if !seen[r.Name] {
				unreached = append(unreached, r.Name)
			}
		}
		return ladder{}, fmt.Errorf(
			"the rung chain does not reach every rung: %s sit outside it, so they are ordered "+
				"against nothing", strings.Join(unreached, ", "))
	}

	return ladder{order: order, facts: occupants}, nil
}

// deriveKinds returns the subject kinds, in the order their first keying fact is
// declared. A kind exists if and only if some fact is keyed by it; the count is
// whatever the registry says.
func deriveKinds() ([]kind, error) {
	var out []kind
	index := make(map[string]int)
	for _, f := range facts {
		if len(f.Subject) == 0 {
			return nil, fmt.Errorf("fact %q declares no subject key", f.Name)
		}
		key := strings.Join(f.Subject, "\x00")
		if i, ok := index[key]; ok {
			out[i].Facts = append(out[i].Facts, f.Name)
			continue
		}
		index[key] = len(out)
		out = append(out, kind{Coordinates: f.Subject, Facts: []string{f.Name}})
	}
	return out, nil
}

// deriveTupleShares returns the groups of facts sharing a rung, a source carrier
// and a subject key, in the order the group's first fact is declared.
func deriveTupleShares(fs []factDecl) []tupleShare {
	var out []tupleShare
	index := make(map[string]int)
	for _, f := range fs {
		subject := kind{Coordinates: f.Subject}.Display()
		key := strings.Join([]string{f.Rung, f.SourceField, subject}, "\x00")
		if i, ok := index[key]; ok {
			out[i].Facts = append(out[i].Facts, f.Name)
			continue
		}
		index[key] = len(out)
		out = append(out, tupleShare{
			Rung:        f.Rung,
			SourceField: f.SourceField,
			Subject:     subject,
			Facts:       []string{f.Name},
		})
	}
	shared := out[:0]
	for _, g := range out {
		if len(g.Facts) > 1 {
			shared = append(shared, g)
		}
	}
	return shared
}

// validateFacts checks the per-fact declaration shape the emitters rely on.
//
// It deliberately does NOT check for a duplicate fact name at one rung, and the
// omission is load-bearing rather than an oversight. Because the accessor's
// method name is derived from the fact name, two declarations of one name at one
// rung emit two methods of one name on one interface, which the Go compiler
// refuses. Checking it here would shadow that, so the strongest available
// enforcement would never fire and the language's guarantee would quietly become
// this function's instead.
//
// It DOES refuse one fact name at two rungs, because the language provably does
// not. Measured against the compiler rather than reasoned about: with the two
// declarations at one rung the build fails with a duplicate method; with them at
// two rungs and differing signatures the build fails as well, because an
// explicitly declared method conflicts with an embedded one it disagrees with;
// but with them at two rungs and an identical signature the build SUCCEEDS,
// since a method set may overlap through embedding when the signatures match.
// That last case is exactly the placement error the registry exists to prevent —
// one fact claiming two different earliest-complete points — so it is refused
// here. Do not remove this on the reasoning that the compiler covers it; it was
// checked, and it does not.
func validateFacts() error {
	rungOf := make(map[string]string, len(facts))
	for _, f := range facts {
		if prior, seen := rungOf[f.Name]; seen && prior != f.Rung {
			return fmt.Errorf(
				"fact %q is declared at two rungs, %q and %q; a fact has exactly one earliest rung "+
					"at which it is complete, and two would make the placement of every check reading "+
					"it ambiguous", f.Name, prior, f.Rung)
		}
		rungOf[f.Name] = f.Rung
	}
	for _, f := range facts {
		switch {
		case f.Name == "":
			return fmt.Errorf("a fact is declared with no name")
		case f.SourceField == "":
			return fmt.Errorf("fact %q names no source carrier, so it cannot be checked against its "+
				"near-duplicates", f.Name)
		case f.Returns == "":
			return fmt.Errorf("fact %q declares no result", f.Name)
		case f.Doc == "":
			return fmt.Errorf("fact %q carries no documentation", f.Name)
		}
		if f.Name != kebab(f.Name) {
			return fmt.Errorf("fact name %q is not in kebab-case", f.Name)
		}
		for _, p := range f.Params {
			if p.Name == "" || p.Type == "" {
				return fmt.Errorf("fact %q declares an incomplete argument", f.Name)
			}
		}
	}
	return nil
}

// accessorName derives a fact's Go method name from its single name.
//
// The derivation is what makes duplicate declaration a compile error rather than
// a convention: because the method name is a function of the fact name, two
// declarations of one fact name emit two methods of one name on one interface,
// which does not compile. A separately declared method name would let the two
// diverge and the language would have nothing to object to.
func accessorName(factName string) string {
	var b strings.Builder
	for _, seg := range strings.Split(factName, "-") {
		b.WriteString(pascal(seg))
	}
	return b.String()
}

// viewName derives a rung's view interface name.
func viewName(rung string) string { return pascal(rung) + "View" }

func pascal(s string) string {
	parts := strings.Split(s, "-")
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]))
		b.WriteString(p[1:])
	}
	return b.String()
}

func kebab(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-':
			b.WriteByte(c)
		case c >= 'A' && c <= 'Z':
			b.WriteByte(c + ('a' - 'A'))
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}
