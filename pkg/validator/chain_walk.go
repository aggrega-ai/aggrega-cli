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

// ExtendsChainSpec carries everything an extends chain walk needs that differs
// between the kinds that declare extends. The walk itself — the cycle rule, the
// depth bound, and the order the two are decided in — is the same for all of
// them and lives in [ResolveExtendsChain].
//
// Key and Name are separate because they answer different questions, and for
// the kinds an author addresses by name the two answers coincide. Key decides
// what makes two links the SAME link; Name decides what a diagnostic CALLS a
// link. A Manifest is the case that separates them: two Manifest files may
// legally declare one metadata.name, so a chain that folded them into one link
// would report a loop that is not there — while a diagnostic naming the file
// would hand the author a path the machine composed rather than the name they
// wrote.
type ExtendsChainSpec[T any] struct {
	// Key reports what makes two links the same link. Reaching a key already
	// in the chain is what a cycle is.
	Key func(T) string
	// Name reports what a diagnostic calls a link. Optional: nil means Key
	// answers for both, which is right wherever a link's identity is the name
	// its author wrote.
	Name func(T) string
	// ParentLink reports the reference a link extends. An empty reference ends
	// the chain.
	ParentLink func(T) string
	// Lookup resolves a reference to the link it names, reporting false when
	// nothing answers to it. A lookup that knows WHY a reference did not
	// resolve records that itself and reads it back in MissingParent, which is
	// where the reason reaches the reader.
	Lookup func(string) (T, bool)
	// MissingParent supplies the two halves of the unresolvable-parent
	// diagnostic that depend on where this kind's references resolve: what the
	// reference failed to name, and where the author should look instead.
	// Optional: nil means the catalog wording, which is right for every kind
	// whose extends addresses a catalog entity.
	MissingParent func(from, link string) (constraint, suggestion string)
}

// name reports what a diagnostic calls v, falling back to the chain-membership
// key where the spec declares no separate name.
func (s ExtendsChainSpec[T]) name(v T) string {
	if s.Name != nil {
		return s.Name(v)
	}
	return s.Key(v)
}

// ResolveExtendsChain follows an extends chain from start and returns it in
// walk order — start first, then its parent, then its parent's parent — or the
// failure for the one structural fault that stopped it, reported against start.
//
// It is the single home for the extends walk and for the three diagnostics that
// walk can produce. Every path that follows an extends chain calls it, so the
// cycle rule, the depth bound, the identity the visited set is keyed on and the
// words each failure is reported in are decided once rather than per kind.
//
// # Why the visited set is keyed on the RESOLVED link
//
// A parent link and a link's own key are written in two different vocabularies:
// an author may pin a version in the link (`core/base:1.0.0`) while the entity
// names itself without one (`core/base`), and a Manifest's reference may be a
// relative path to a file the Manifest names differently again. Keying the
// visited set on the reference text would therefore compare values that never
// match, and the walk would take a full extra lap around a cycle before
// recognising it — which both delays detection past the depth bound and names
// the wrong edge when it does fire. So the walk resolves each reference first
// and asks whether the link it reached is already in the chain, which is one
// question in one vocabulary.
//
// # Why the depth bound is applied AFTER the walk
//
// The two safeguards answer different questions and must not preempt each
// other. Testing the bound inside the loop lets a long cycle trip it before it
// closes, and the author is then told to shorten a chain whose length is not
// the problem — advice that cannot fix their files. Because the visited set
// bounds the walk on its own, the length can be judged once the shape is known:
// a chain that closes on itself is a cycle at any length, and only a chain that
// ends cleanly can be too deep.
func ResolveExtendsChain[T any](kind envelope.Kind, start T, spec ExtendsChainSpec[T]) ([]T, *ValidationError) {
	chain, fault := walkExtendsChain(start, spec)
	if fault != nil {
		return nil, chainFaultError(kind, spec.name(start), fault, spec.MissingParent)
	}
	return chain, nil
}

// walkExtendsChain is the walk itself, kept separate from the diagnostic so the
// two halves can be read independently. It is unexported, and
// ResolveExtendsChain is the only way to reach it, so no caller can walk a
// chain and then report its fault in words of its own.
func walkExtendsChain[T any](start T, spec ExtendsChainSpec[T]) ([]T, *chainFault) {
	chain := []T{start}
	visited := map[string]bool{spec.Key(start): true}
	current := start

	for {
		link := spec.ParentLink(current)
		if link == "" {
			break
		}
		parent, ok := spec.Lookup(link)
		if !ok {
			return nil, &chainFault{kind: chainFaultMissingParent, from: spec.name(current), link: link}
		}
		key := spec.Key(parent)
		if visited[key] {
			return nil, &chainFault{kind: chainFaultCycle, from: spec.name(current), link: link}
		}
		visited[key] = true
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
// It is the single home for the three diagnostics, so every path that walks an
// extends chain reports the same words for the same failure. Each message
// supplies only what the condition's own registered sentence cannot carry: the
// edge at fault for a cycle and an unresolvable parent, and the bound against
// the length reached for a chain that is too deep.
//
// missingParent is the one part of the wording that cannot be shared, because
// an extends reference does not resolve against the same thing for every kind:
// a catalog entity's names another catalog entity, while a Manifest's names a
// file beside the Release. Sending an author to the wrong one is advice that
// cannot fix their files, so the two sentences that depend on it are supplied
// by the caller and everything else about the failure — the condition, the
// field path, the entity it is reported against — stays here. A nil
// missingParent takes the catalog wording.
func chainFaultError(kind envelope.Kind, name string, f *chainFault, missingParent func(from, link string) (string, string)) *ValidationError {
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
		constraint := fmt.Sprintf("%s extends %s, which no catalog entity provides", f.from, f.link)
		suggestion := "Verify the extends reference names an entity present in the catalog"
		if missingParent != nil {
			constraint, suggestion = missingParent(f.from, f.link)
		}
		return NewValidationError(kind, name, ValidationDetail{
			FieldPath:  ".spec.extends",
			Constraint: constraint,
			Suggestion: suggestion,
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
