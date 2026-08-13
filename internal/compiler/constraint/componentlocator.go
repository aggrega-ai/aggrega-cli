// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

// componentAuthoredSegments returns the locator segments naming where a
// component instance was authored.
//
// The rule turns on provenance and never on the path string, and that is the
// whole of it. Normalization folds an Aggregate's top-level components into a
// synthesized subsystem, so a component the author wrote at the top level
// arrives carrying a subsystem path it was never authored under; naming that
// subsystem would point at a location the source file does not contain. Such a
// component therefore names the site the author did write, under
// .spec.components. A component in a subsystem the author actually declared
// names that subsystem, level by level under the container's own key, and then
// the component: the authored document spells a nested scope that way, and
// joining the path in as one segment would name a key no file contains. Both
// arms name a site the author wrote, and neither invents one.
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
// Three hand-assembled locator builders elsewhere in the compiler name a
// component's site per violation class, and they name the top-level shape only:
// a component in a subsystem the author declared gets no locator from them. So
// they no longer agree with this rule, and the disagreement is carried
// deliberately rather than overlooked — they are left in place until the checks
// that use them are written against this framework, at which point they retire
// onto this one and the disagreement retires with them.
func componentAuthoredSegments(componentName, subsystemPath string, synthesizedSubsystem bool) []string {
	if subsystemPath != "" && !synthesizedSubsystem {
		return nestedScopeSegments(subsystemNesting, subsystemPath, authoredKeyComponents, componentName)
	}
	return []string{authoredKeySpec, authoredKeyComponents, componentName}
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
