// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// maxInheritanceDepth bounds how far an extends chain may run. It is a
// sanity guard on authoring rather than a termination safeguard: the walk
// below terminates on its own, because every step records the entity it
// reached and stops the moment it reaches one twice.
const maxInheritanceDepth = 10

// chainFaultKind names which of the three chain-structural failures a walk
// stopped on. Each carries its own condition and its own remedy, so they are
// distinguished at the point of detection rather than at the point of
// reporting.
type chainFaultKind int

const (
	// chainFaultCycle: following the chain reached an entity already in it.
	chainFaultCycle chainFaultKind = iota
	// chainFaultDepth: the chain terminated cleanly but runs past the bound.
	chainFaultDepth
	// chainFaultMissingParent: a link named an entity the lookup cannot serve.
	chainFaultMissingParent
)

// chainFault is what a failed chain walk reports: which failure, and the
// coordinates a reader needs to act on it.
//
// From and Link are the two halves of one edge — the entity that declares the
// offending extends, and the reference it declares. Naming the edge rather than
// one endpoint is what makes a cycle actionable: the entity a resolution was
// requested for is rarely the one carrying the link that closes the loop, so a
// diagnostic naming only the former tells an author to look in the wrong file.
type chainFault struct {
	kind chainFaultKind
	// from is the entity whose extends declaration is at fault. Empty for a
	// depth fault, which is a property of the chain rather than of one edge.
	from string
	// link is the extends reference declared on from. Empty for a depth fault.
	link string
	// depth is the number of entities the chain reached. Set for a depth fault
	// and zero otherwise.
	depth int
}

// walkExtendsChain follows an extends chain from start and returns it in walk
// order — start first, then its parent, then its parent's parent — or the one
// structural fault that stopped it.
//
// It is the single home for the extends walk. Every entity path that follows an
// extends chain calls it, so the cycle rule, the depth bound and the identity
// the visited set is keyed on are decided once. Three parameters carry
// everything that differs between those paths: identity reports the entity's
// own name, parentLink reports the reference it extends (empty terminates the
// walk), and lookup resolves a reference to the entity it names.
//
// # Why the visited set is keyed on the RESOLVED entity
//
// A parent link and an entity's own name are written in two different
// vocabularies: an author may pin a version in the link (`core/base:1.0.0`)
// while the entity names itself without one (`core/base`). Keying the visited
// set on the link would therefore compare values that never match, and the walk
// would take a full extra lap around a cycle before recognising it — which both
// delays detection past the depth bound and names the wrong edge when it does
// fire. So the walk resolves each link first and asks whether the entity it
// reached is already in the chain, which is one question in one vocabulary.
//
// # Why the depth bound is applied AFTER the walk
//
// The two safeguards answer different questions and must not preempt each
// other. Testing the bound inside the loop lets a long cycle trip it before it
// closes, and the author is then told to shorten a chain whose length is not
// the problem — advice that cannot fix their catalog. Because the visited set
// bounds the walk on its own, the length can be judged once the shape is known:
// a chain that closes on itself is a cycle at any length, and only a chain that
// ends cleanly can be too deep.
func walkExtendsChain[T any](
	start T,
	identity func(T) string,
	parentLink func(T) string,
	lookup func(string) (T, bool),
) ([]T, *chainFault) {
	chain := []T{start}
	visited := map[string]bool{identity(start): true}
	current := start

	for {
		link := parentLink(current)
		if link == "" {
			break
		}
		parent, ok := lookup(link)
		if !ok {
			return nil, &chainFault{kind: chainFaultMissingParent, from: identity(current), link: link}
		}
		name := identity(parent)
		if visited[name] {
			return nil, &chainFault{kind: chainFaultCycle, from: identity(current), link: link}
		}
		visited[name] = true
		chain = append(chain, parent)
		current = parent
	}

	if len(chain) > maxInheritanceDepth {
		return nil, &chainFault{kind: chainFaultDepth, depth: len(chain)}
	}
	return chain, nil
}

// chainFaultError renders a chain fault as the validation failure reported
// against the entity the resolution was requested for.
//
// It is the single home for the three diagnostics, so every entity path that
// walks an extends chain reports the same words for the same failure. Each
// message supplies only what the condition's own registered sentence cannot
// carry: the edge at fault for a cycle and an unresolvable parent, and the
// bound against the length reached for a chain that is too deep.
func chainFaultError(kind envelope.Kind, name string, f *chainFault) *ValidationError {
	switch f.kind {
	case chainFaultCycle:
		return NewValidationError(kind, name, ValidationDetail{
			FieldPath:  ".spec.extends",
			Constraint: fmt.Sprintf("%s extends %s, which is already in the chain", f.from, f.link),
			Suggestion: fmt.Sprintf("Remove the extends on %s, or point it at an entity outside the chain", f.from),
			Err:        ErrCyclicExtension(),
		})
	case chainFaultDepth:
		return NewValidationError(kind, name, ValidationDetail{
			FieldPath:  ".spec.extends",
			Constraint: fmt.Sprintf("the bound is %d and this chain reaches %d", maxInheritanceDepth, f.depth),
			Suggestion: "Shorten the chain, or extend a base that sits closer to its root",
			Err:        ErrExtendsChainTooDeep(),
		})
	case chainFaultMissingParent:
		return NewValidationError(kind, name, ValidationDetail{
			FieldPath:  ".spec.extends",
			Constraint: fmt.Sprintf("%s extends %s, which no catalog entity provides", f.from, f.link),
			Suggestion: "Verify the extends reference names an entity present in the catalog",
			Err:        ErrInheritanceMerge(),
		})
	}
	// Unreachable through walkExtendsChain, which is the only producer of a
	// chainFault and sets one of the three kinds above. It panics rather than
	// returning a failure carrying no condition, because a kind added to the
	// enumeration without a diagnostic here is a defect in the compiler that
	// would otherwise surface as an unattributed halt.
	panic(fmt.Sprintf("validator: extends-chain fault kind %d carries no diagnostic", f.kind))
}
