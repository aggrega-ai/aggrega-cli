// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"strings"
)

// chain is the stage set in containment order, root first, once the declared
// Extends relation has been checked to be a single chain.
type chain struct {
	order []stageDecl
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

// tupleShare is a group of facts sharing a stage, a source carrier and a subject
// key.
//
// A share is not proof of duplication and the generator does not treat it as
// one. Same-name duplication is closed, but by two mechanisms rather than one:
// the language rejects two declarations of a name at one stage, or at two stages
// with differing signatures, while the case it admits — two stages and an
// identical signature, which a method set may carry through embedding — is
// refused by validateFacts. Same semantics under two different names is not
// closed and there is no mechanism for it; printing the shares is a mitigation
// that turns an invisible risk into a reviewable list.
type tupleShare struct {
	Stage       string
	SourceField string
	Subject     string
	Facts       []string
}

// deriveChain checks the declared stage relation and returns the stage set in
// containment order together with the facts landing on each stage.
//
// It refuses, rather than repairing, four shapes. A stage nothing occupies,
// since a stage exists if and only if a fact lands there. A fact naming an
// undeclared stage. More than one root, or a stage two others extend — either of
// which means two stages no containment relation orders, and an unordered pair
// is exactly what makes the earliest stage a fact is complete at un-computable.
// And a cycle.
//
// It also refuses a host-power declaration that is missing or that names a power
// the host does not offer. Both refusals are what make the slot a declaration
// rather than a convention: the language cannot tell an omitted slice from a
// deliberately empty one, and a value converted into the power type from an
// arbitrary string would otherwise reduce the closed set to a spelling.
//
// # The bound on what these refusals establish, and the coarseness is honest
//
// All four refusals are structural: each reads the declared relation and the
// fact set and nothing else. The property that justifies a stage is not
// structural but temporal — some set of facts is complete here and was not
// complete before — and the registry records no moment at which anything
// happens, so nothing here can evaluate it. A stage occupied by one genuine fact
// and linearly related to its neighbour therefore derives cleanly even when that
// fact completes at the very moment its predecessor's does.
//
// So what the derivation establishes is that the chain is well-formed, never
// that each stage in it earns its place. Whether a stage earns its place is a
// reader's judgement, and it is named as one here rather than left to the
// silence: a derivation that refuses four shapes strictly, and with messages
// that explain themselves, reads as having covered the question it is in fact
// silent on. Well-formedness is the strongest claim this code supports, which is
// why it is the claim made rather than a weaker one dressed up.
func deriveChain() (chain, error) {
	byName := make(map[string]stageDecl, len(stages))
	for _, s := range stages {
		if s.Name == "" {
			return chain{}, fmt.Errorf("a stage is declared with no name")
		}
		if _, dup := byName[s.Name]; dup {
			return chain{}, fmt.Errorf("stage %q is declared twice", s.Name)
		}
		byName[s.Name] = s
	}

	if err := validateStageHostPowers(stages); err != nil {
		return chain{}, err
	}

	occupants := make(map[string][]factDecl, len(stages))
	for _, f := range facts {
		if _, ok := byName[f.Stage]; !ok {
			return chain{}, fmt.Errorf("fact %q names stage %q, which is not declared", f.Name, f.Stage)
		}
		occupants[f.Stage] = append(occupants[f.Stage], f)
	}
	for _, s := range stages {
		if len(occupants[s.Name]) == 0 {
			return chain{}, fmt.Errorf(
				"stage %q is declared but no fact lands there; a stage exists only where a fact does, "+
					"so either declare the fact or remove the stage", s.Name)
		}
	}

	var roots []string
	extendedBy := make(map[string][]string, len(stages))
	for _, s := range stages {
		if s.Extends == "" {
			roots = append(roots, s.Name)
			continue
		}
		if _, ok := byName[s.Extends]; !ok {
			return chain{}, fmt.Errorf("stage %q extends %q, which is not declared", s.Name, s.Extends)
		}
		if s.Extends == s.Name {
			return chain{}, fmt.Errorf("stage %q extends itself", s.Name)
		}
		extendedBy[s.Extends] = append(extendedBy[s.Extends], s.Name)
	}
	if len(roots) != 1 {
		return chain{}, fmt.Errorf(
			"the stage chain is not linear: %d stages extend nothing (%s), so no containment relation "+
				"orders them and the earliest stage a fact is complete at is not computable",
			len(roots), strings.Join(roots, ", "))
	}
	for extended, extenders := range extendedBy {
		if len(extenders) > 1 {
			return chain{}, fmt.Errorf(
				"the stage chain is not linear: stages %s all extend %q, so no containment relation "+
					"orders them against each other and the earliest stage a fact is complete at is "+
					"not computable", strings.Join(extenders, ", "), extended)
		}
	}

	order := make([]stageDecl, 0, len(stages))
	seen := make(map[string]bool, len(stages))
	current := roots[0]
	for {
		if seen[current] {
			return chain{}, fmt.Errorf("the stage chain cycles at %q", current)
		}
		seen[current] = true
		order = append(order, byName[current])
		next := extendedBy[current]
		if len(next) == 0 {
			break
		}
		current = next[0]
	}
	if len(order) != len(stages) {
		unreached := make([]string, 0, len(stages)-len(order))
		for _, s := range stages {
			if !seen[s.Name] {
				unreached = append(unreached, s.Name)
			}
		}
		return chain{}, fmt.Errorf(
			"the stage chain does not reach every stage: %s sit outside it, so they are ordered "+
				"against nothing", strings.Join(unreached, ", "))
	}

	return chain{order: order, facts: occupants}, nil
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

// deriveTupleShares returns the groups of facts sharing a stage, a source
// carrier and a subject key, in the order the group's first fact is declared.
func deriveTupleShares(fs []factDecl) []tupleShare {
	var out []tupleShare
	index := make(map[string]int)
	for _, f := range fs {
		subject := kind{Coordinates: f.Subject}.Display()
		key := strings.Join([]string{f.Stage, f.SourceField, subject}, "\x00")
		if i, ok := index[key]; ok {
			out[i].Facts = append(out[i].Facts, f.Name)
			continue
		}
		index[key] = len(out)
		out = append(out, tupleShare{
			Stage:       f.Stage,
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

// validateStageHostPowers checks each stage's host-power declaration.
//
// It takes the stage set as an argument rather than reading the registry, so
// that the refusals can be shown to fire: a refusal nothing exercises is
// indistinguishable from a refusal that never triggers, and both of these guard
// a declaration a reviewer is meant to trust.
//
// Two shapes are refused, and neither is available to the language. An omitted
// declaration cannot be told from a deliberately empty one — a nil slice and an
// empty slice are both length zero at every site that reads them — so the
// omission is refused here and the empty case is said by name. And a value
// converted into the power type from an arbitrary string type-checks, so a
// declaration naming a power the host does not offer would reduce the closed set
// to whatever anyone spelled.
func validateStageHostPowers(ss []stageDecl) error {
	offered := make(map[hostPower]bool, len(hostPowers))
	for _, p := range hostPowers {
		offered[p] = true
	}
	for _, s := range ss {
		if s.HostPowers == nil {
			return fmt.Errorf(
				"stage %q omits its host-power declaration; a stage whose fact production exercises "+
					"none says so by declaring noHostPowers, because an omission and a deliberate "+
					"emptiness must not read the same to a reviewer", s.Name)
		}
		named := make(map[hostPower]bool, len(s.HostPowers))
		for _, p := range s.HostPowers {
			if !offered[p] {
				return fmt.Errorf(
					"stage %q declares host power %q, which is not one the host offers; the set is "+
						"closed so that a declaration is checkable against a vocabulary rather than "+
						"against a spelling", s.Name, p)
			}
			if named[p] {
				return fmt.Errorf("stage %q declares host power %q twice", s.Name, p)
			}
			named[p] = true
		}
	}
	return nil
}

// validateFacts checks the per-fact declaration shape the emitters rely on.
//
// It deliberately does NOT check for a duplicate fact name at one stage, and the
// omission is load-bearing rather than an oversight. Because the accessor's
// method name is derived from the fact name, two declarations of one name at one
// stage emit two methods of one name on one interface, which the Go compiler
// refuses. Checking it here would shadow that, so the strongest available
// enforcement would never fire and the language's guarantee would quietly become
// this function's instead.
//
// It DOES refuse one fact name at two stages, because the language provably does
// not. Measured against the compiler rather than reasoned about: with the two
// declarations at one stage the build fails with a duplicate method; with them at
// two stages and differing signatures the build fails as well, because an
// explicitly declared method conflicts with an embedded one it disagrees with;
// but with them at two stages and an identical signature the build SUCCEEDS,
// since a method set may overlap through embedding when the signatures match.
// That last case is exactly the placement error the registry exists to prevent —
// one fact claiming two different earliest-complete points — so it is refused
// here. Do not remove this on the reasoning that the compiler covers it; it was
// checked, and it does not.
func validateFacts() error {
	stageOf := make(map[string]string, len(facts))
	for _, f := range facts {
		if prior, seen := stageOf[f.Name]; seen && prior != f.Stage {
			return fmt.Errorf(
				"fact %q is declared at two stages, %q and %q; a fact has exactly one earliest stage "+
					"at which it is complete, and two would make the placement of every check reading "+
					"it ambiguous", f.Name, prior, f.Stage)
		}
		stageOf[f.Name] = f.Stage
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

// viewName derives a stage's view interface name.
func viewName(stage string) string { return pascal(stage) + "View" }

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
