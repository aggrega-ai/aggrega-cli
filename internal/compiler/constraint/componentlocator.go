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
// Hand-assembled locator builders elsewhere in the compiler name a component's
// site per violation class, and they name the top-level shape only: a component
// in a subsystem the author declared gets no locator from them. So they do not
// agree with this rule, and the disagreement is carried deliberately rather
// than overlooked — each is left in place until the check that uses it is
// written against this framework, at which point it retires onto this one and
// its share of the disagreement retires with it. Their number is deliberately
// not stated: it falls by one with each such check, and a stated count would go
// stale on the next one while still reading as current.
func componentAuthoredSegments(componentName, subsystemPath string, synthesizedSubsystem bool) []string {
	return scopedComponentSegments(
		subsystemNesting,
		ComponentSite(componentName, subsystemPath, synthesizedSubsystem),
	)
}

// ComponentSite returns the site a component instance's own block was written
// at: the scope the author declared it in, and the name the author wrote.
//
// It is exported because the pipeline needs the same answer. An element the
// author wrote inside a component's block — an attachment, and every leaf of
// that attachment's configuration — is written at the component's own site, so
// whatever supplies such an element's site has to produce exactly what this rule
// produces or a locator will name one place for the element and another for the
// component holding it. Deriving it twice is what makes that possible, so it is
// derived once here and read from there.
//
// The synthesized scope is discarded here rather than at each reader, for the
// reason the rule above gives: a scope normalization generated is not a scope
// any file spells, so a component under one names the top-level shape the author
// did write. Keying on the provenance flag is what keeps a scope the author
// declares and happens to name the same as the synthesized one from being
// mistaken for it.
func ComponentSite(componentName, subsystemPath string, synthesizedSubsystem bool) AuthoredSite {
	if synthesizedSubsystem {
		subsystemPath = ""
	}
	return AuthoredSite{ScopePath: subsystemPath, Component: componentName}
}

// scopedComponentSegments returns the segments naming a component's own block
// inside a scope of the given axis: the scope level by level under the
// container's own key, then the component.
//
// It is the shape of [componentAuthoredSegments] with the axis lifted out,
// because the two authoring documents nest components the same way and differ
// only in what the nesting is spelled with. Lifting it is what keeps one rule
// where there would otherwise be two — a second copy would agree the day it was
// written and be free to drift afterwards, with nothing comparing them.
//
// An empty scope path names the top-level shape, which is where the discarded
// synthesized case lands: a scope normalization generated is not a scope any
// file spells, so a component under one is named at the site the author did
// write. The collapse happens at the caller that knows the flag, so this rule
// answers one question — which key does the file contain — rather than two.
func scopedComponentSegments(nesting scopeNesting, site AuthoredSite) []string {
	if site.ScopePath != "" {
		return nestedScopeSegments(nesting, site.ScopePath, authoredKeyComponents, site.Component)
	}
	return []string{authoredKeySpec, authoredKeyComponents, site.Component}
}

// authoredSiteSegments returns the segments naming the site an entry was
// written at, on the nesting axis the entry's own document nests with.
//
// Two shapes, and the second is the one a component-rooted path cannot express.
// An entry keyed under a component names that component's block inside its
// scope, which is the shape every component-inner element already takes. An
// entry written scope-wide names the scope and stops: it reaches every
// component the scope holds, so there is no component to name, and naming one
// would point at a key the file does not contain and at an edit that would not
// fix the finding — the scope's own block is the one place anyone can change it.
//
// A scope-wide entry with no scope to name yields no segments, which is the
// mechanical form of not inventing a locator: the shape requires a scope, and a
// consumer handed nothing falls back to file-level positioning rather than to a
// path that resolves nowhere.
func authoredSiteSegments(nesting scopeNesting, site AuthoredSite, tail ...string) []string {
	var base []string
	if site.Component != "" {
		base = scopedComponentSegments(nesting, site)
	} else {
		base = nestedScopeSegments(nesting, site.ScopePath)
	}
	if len(base) == 0 {
		return nil
	}
	out := make([]string, 0, len(base)+len(tail))
	out = append(out, base...)
	return append(out, tail...)
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
