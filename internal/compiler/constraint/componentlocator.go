// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

// componentAuthoredSegments returns the locator segments naming where a
// component instance was authored, or no segments at all when its authored site
// is one the dotted form cannot express.
//
// The rule turns on provenance and never on the path string, and that is the
// whole of it. Normalization folds an Aggregate's top-level components into a
// synthesized subsystem, so a component the author wrote at the top level
// arrives carrying a subsystem path it was never authored under; naming that
// subsystem would point at a location the source file does not contain. Such a
// component therefore names the site the author did write, under
// .spec.components. A component in a subsystem the author actually declared
// yields no segments: its authored site sits under .spec.subsystems, a shape
// the dotted form cannot express, and an empty locator routes the consumer to
// file-level positioning rather than to a key that is not there.
//
// Because the discriminator is the synthesized flag, a subsystem the author
// declares and happens to name the same as the synthesized one is not mistaken
// for it. A rule keyed on the path string could not tell the two apart, and it
// would discard real provenance for every top-level component in the tree.
//
// componentName is the name the author wrote. Deriving it from a compilation
// product's node identifier belongs to the traversal that holds the node, not
// to a provenance rule: the identifier's qualification records how the node was
// reached, which is a different question from what the author typed.
//
// Three hand-assembled locator builders elsewhere in the compiler implement
// this same rule per violation class, and one of them is the shape above. They
// are left in place until the checks that use them are written against this
// framework, at which point they retire onto this one.
func componentAuthoredSegments(componentName, subsystemPath string, synthesizedSubsystem bool) []string {
	if subsystemPath != "" && !synthesizedSubsystem {
		return nil
	}
	return []string{"spec", "components", componentName}
}

// newComponentNodeSubject is the node subject of a component instance, with the
// provenance rule in [componentAuthoredSegments] applied. It composes rather
// than formats: the segments go through the same constructor and the same
// locator assembly every other kind uses.
//
// node and componentName are both taken and neither is derived from the other,
// which is the same distinction [NodeInstance] draws: node is the identifier
// every keyed fact takes and records how the instance was reached, while
// componentName is what the author typed and is what a locator may name. A
// coordinate and a locator segment answer different questions here, so supplying
// one value for both would make a check reading its own identifier disagree with
// the facts.
func newComponentNodeSubject(
	sourceFile, node, componentName, subsystemPath string,
	synthesizedSubsystem bool,
) NodeSubject {
	return newNodeSubject(
		sourceFile, node,
		componentAuthoredSegments(componentName, subsystemPath, synthesizedSubsystem)...,
	)
}
