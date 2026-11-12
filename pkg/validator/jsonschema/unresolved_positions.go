// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package jsonschema

import (
	"sort"
	"strings"
)

// propertiesSegment is the pointer step from a schema node to one of its
// declared properties. It is written once and read by the pointer builder
// and the pointer reader alike, so a pointer this package composes and a
// pointer it takes apart cannot disagree about their shape.
const propertiesSegment = "properties"

// unresolvedPosition records a position whose schema cannot be checked on
// its own, together with where that position sits in the schema document.
//
// A position is checked on its own so that a value failing there is named
// at its own place rather than at whichever of its siblings a map iteration
// reached first. Standing a position up on its own means resolving it as
// though it were a whole document, and a reference resolves then only if it
// names something the position still carries — which, after the definitions
// in scope are carried onto it, means a name in those definitions and
// nothing else. A reference pointing at a declared property, or into
// another document, or through a definition into somewhere outside the
// definitions, names something the standalone position does not have.
//
// Such a position cannot be stood up, so it is recorded here and checked
// against the whole document instead, where the reference does resolve. The
// pointer is what makes that check attributable: a failure reported against
// the whole document says where in the schema it happened, and a location
// at or beneath this pointer is a failure at this position.
type unresolvedPosition struct {
	// instancePath is the dot-delimited position in the instance, the same
	// path space every finding uses.
	instancePath string

	// pointer is the position's own location in the schema document,
	// recorded as authored rather than as substituted: a reference is
	// followed during the check, so the location chain passes through the
	// position before it reaches whatever the reference named, and only the
	// authored location is common to both.
	pointer string
}

// childPointer appends one declared-property step to a schema-document
// pointer.
//
// The two characters that mean something inside a pointer segment are
// escaped, in the order the pointer syntax requires: the escape character
// first, so that escaping the separator afterwards cannot be re-read as an
// escaped escape.
func childPointer(pointer, property string) string {
	escaped := strings.ReplaceAll(property, "~", "~0")
	escaped = strings.ReplaceAll(escaped, "/", "~1")
	return pointer + "/" + propertiesSegment + "/" + escaped
}

// recordUnresolvedPosition notes a position the construction sweep could
// not stand up on its own, so the per-call check knows to reach it through
// the whole document.
//
// The instance root is never recorded: its own resolution failure is
// propagated to the caller as a malformed schema rather than tolerated, so
// a root entry could only ever be unreachable.
func (a *googleAdapter) recordUnresolvedPosition(instancePath, pointer string) {
	if instancePath == "" {
		return
	}
	a.unresolved = append(a.unresolved, unresolvedPosition{
		instancePath: instancePath,
		pointer:      pointer,
	})
}

// isUnresolvedPosition reports whether the instance path names a position
// the construction sweep recorded rather than indexed.
//
// The walk asks this to tell a declared position it cannot check from a key
// the schema never declared, which look identical from the index alone —
// both are simply absent from it — and which a closed parent must treat as
// opposites.
//
// The scan is linear over a list that is empty for every schema whose
// references name definitions, and short otherwise, so it costs nothing
// where nothing is recorded and stays proportional to what is.
func (a *googleAdapter) isUnresolvedPosition(instancePath string) bool {
	for _, position := range a.unresolved {
		if position.instancePath == instancePath {
			return true
		}
	}
	return false
}

// schemaLocations returns the locations a schema library's failure names,
// outermost first, as the pointers they are written as.
//
// The chain is the library's account of how it reached the failing
// constraint. Reading the whole chain rather than its last segment is what
// makes it usable here: a reference moves the chain to the referenced
// location, so the last segment names where the constraint is *declared*
// while the segment before the move names where the value being checked
// actually sits. Only the whole chain carries both.
//
// A segment is recognised on the same terms the message shaper recognises
// one — an opening word followed by a location carrying no space — so the
// two read the same chain, and text that merely contains the opening word
// stops the walk in both.
func schemaLocations(err error) []string {
	if err == nil {
		return nil
	}
	message := err.Error()
	var locations []string
	for strings.HasPrefix(message, validatingPrefix) {
		rest := message[len(validatingPrefix):]
		separator := strings.Index(rest, ": ")
		if separator < 0 {
			break
		}
		location := rest[:separator]
		if strings.ContainsRune(location, ' ') {
			break
		}
		locations = append(locations, location)
		message = rest[separator+2:]
	}
	return locations
}

// instancePathFromPointer maps a location in the schema document to the
// position in the instance whose value the constraint there applies to.
//
// Only a declared-property step moves the instance position. Every other
// step — a combinator branch, an element position, a definition, a
// conditional arm — narrows which schema applies without changing which
// value is being checked, so those steps are passed over.
func instancePathFromPointer(pointer string) string {
	segments := strings.Split(pointer, "/")
	out := make([]string, 0, len(segments)/2)
	for i := 0; i < len(segments); i++ {
		if segments[i] == propertiesSegment && i+1 < len(segments) {
			out = append(out, segments[i+1])
			i++
		}
	}
	return strings.Join(out, ".")
}

// isPointerUnder reports whether location is pointer itself or a step
// beneath it. The separator is required so that a sibling sharing a textual
// prefix does not match.
func isPointerUnder(location, pointer string) bool {
	return location == pointer || strings.HasPrefix(location, pointer+"/")
}

// checkUnresolvedPositions checks the positions that could not be stood up
// on their own, by checking the whole document once and keeping only what
// the check says happened at one of them.
//
// Checking the whole document is the only way to reach these positions at
// all: their references resolve against the document and against nothing
// smaller. Keeping only the failures beneath a recorded position is what
// stops that check from reporting a second time on everything the walk
// already covered, and it cannot report a first time on anything the walk
// covered under a recorded position either — a position that could not be
// stood up was not descended into, so its whole subtree is the walk's blind
// spot and none of it is checked twice.
//
// Two limits, both stated rather than hidden. A schema library stops at the
// first failure it finds, so a document failing at several of these
// positions is named for one of them, and which one is the library's
// choice; a document whose only failure is at one of them is always named
// for it, which is the property that stops such a document passing. And the
// failure is reported at the position the location chain reached, which is
// as deep as the chain goes before the reference moved it — the position
// itself where the reference sits at the position, deeper where it sits
// below one.
func (a *googleAdapter) checkUnresolvedPositions(instance any, rpt *Report) {
	if len(a.unresolved) == 0 {
		return
	}
	document, ok := a.subResolved[""]
	if !ok {
		return
	}
	err := document.Validate(instance)
	if err == nil {
		return
	}

	locations := schemaLocations(err)
	for _, position := range a.unresolved {
		deepest := locationBeforeMove(locations, position.pointer)
		if deepest == "" {
			continue
		}
		// Nothing inside such a position was built, so a declaration of
		// sensitivity there is read off the failure's own location chain.
		rpt.append(a.libraryFinding(err, document, instance,
			instancePathFromPointer(deepest), a.subDefinition[position.instancePath]))
		return
	}
}

// locationBeforeMove returns the deepest location the chain reaches at or
// beneath pointer before a reference moves it elsewhere, or "" where the chain
// never reaches pointer.
//
// Each step of a chain is beneath the one before it until a reference is
// followed, and then the next location is wherever the reference points. So
// the walk starts at the first location at or beneath pointer and stops at the
// first step that is not beneath its predecessor. Taking the longest location
// beneath pointer instead would be wrong exactly where it matters: a reference
// pointing at a sibling inside the same recorded position moves the chain to a
// location that is still beneath pointer, and naming it reports the referenced
// field — which may hold a valid value — rather than the field carrying the
// reference.
func locationBeforeMove(locations []string, pointer string) string {
	deepest := ""
	for _, location := range locations {
		if deepest == "" {
			if isPointerUnder(location, pointer) {
				deepest = location
			}
			continue
		}
		if !isPointerUnder(location, deepest) {
			break
		}
		deepest = location
	}
	return deepest
}

// sortUnresolvedPositions orders the recorded positions by their location
// in the schema document, so that whichever of them a failure is attributed
// to does not depend on the order the construction sweep happened to visit
// them in.
func (a *googleAdapter) sortUnresolvedPositions() {
	sort.Slice(a.unresolved, func(i, j int) bool {
		return a.unresolved[i].pointer < a.unresolved[j].pointer
	})
}
