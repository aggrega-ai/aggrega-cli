// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"slices"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The conformance rules, expressed as constraint declarations. See checks.go for
// what a declaration here is.
//
// Four declarations rather than one, because there are four contracts: a
// component type declares one over each of its two configuration pillars, and a
// dependency socket declares one over each of the two pillars a connection into
// it may carry. Each contract is separately authored and separately omittable,
// each measures a payload written on its own document by its own hand, and a
// rule reading one for another would report against a contract nobody wrote for
// it.
//
// All four measure through one evaluation, so an authored contract is
// interpreted identically wherever the payload originates and whichever surface
// asks. What each declaration contributes is the pair — which contract, and
// which payload — and the subject the finding is attributed to.

// pillarObjections measures one payload against one pillar of a resolved
// contract and raises an objection per independent violation.
//
// heldBack, where a rule supplies one, is asked for the path of each finding that
// refuses an undeclared property and adds its notes, captioned, after the finding's
// own: the held version of the Component that declares the property and the
// constraints that keep the instance below it. A rule whose contract belongs to no
// versioned Component passes none.
//
// The detail is the path the violation landed on, which leads with the pillar
// name, the constraint keyword that failed, and why — with the reason withheld
// where the contract declares the leaf sensitive, because that text quotes the
// value and a diagnostic is read by anyone watching a build. The path, the
// keyword and the pillar survive the withholding, so a reader still learns which
// leaf failed and on what.
//
// Each objection is positioned at that path rather than at the site holding the
// payload, and this is the one place any rule in this package does so. What
// qualifies these four is that their first detail value is an authored path
// relative to the subject — the key inside the payload the contract measured.
// Every other rule here leads with something that is not a place in the document
// at all, such as the component type or the name of a mechanism, or with a name
// the subject's own locator already ends with; positioning on either would name
// a key no file contains. Keeping the choice at one site is what stops it being
// made per rule, where the value happens to look path-like often enough to be
// appended by mistake.
func pillarObjections(
	s constraint.Subject,
	pillar string,
	contract compactform.Schema,
	payload types.OrderedMap[any],
	parameters map[string]any,
	heldBack func(path string) []constraint.HeldBackNote,
) []constraint.Objection {
	findings := pillarConformanceFindings(contract, pillar, payload, parameters)
	if len(findings) == 0 {
		return nil
	}
	out := make([]constraint.Objection, 0, len(findings))
	for _, f := range findings {
		detail := []constraint.DetailValue{constraint.Uncaptioned(f.keyword), constraint.Uncaptioned(f.reason)}
		if heldBack != nil && refusesUndeclaredProperty(f) {
			for _, note := range heldBack(f.path) {
				detail = append(detail, constraint.Captioned(note.Caption, note.Value))
			}
		}
		out = append(out, s.RaiseAtWith(ErrSchemaConformance(), f.path, detail...))
	}
	return out
}

// componentValuesConformToTheType reports each way a component's authored
// configuration fails the contract its component type declares over it.
//
// The contract is closed: a key it does not declare is an authoring error rather
// than an extension point, because the component type's own contract is the sole
// authoring surface for what may be written there. It is also default-aware — a
// key the contract declares required and defaults is satisfied by the default —
// and it evaluates conditional constraints against the resolved parameter
// context, which is why the rule reads that context rather than judging without
// one.
//
// A component type declaring no contract for this pillar has nothing to measure
// against, and neither has a type whose contract did not resolve; both arrive as
// the same absence and both end the rule. What that absence means where the
// catalog holds no entry at all is a separate rule's question, which is why
// presence is settled first.
//
// An empty payload is measured rather than skipped. Once the type declares a
// contract, a component that wrote nothing is the maximal violation of a key
// that contract declares required, not a case with nothing to check.
var componentValuesConformToTheType = constraint.Check[
	constraint.NormalizedView,
	constraint.NodeSubject,
]{
	Sentinel: ErrSchemaConformance(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.NormalizedView,
		s constraint.NodeSubject,
	) []constraint.Objection {
		componentType, present := v.InstanceComponentType(s.NodeID())
		if !present || !v.CatalogComponentPresent(componentType) {
			return nil
		}
		contract, resolved := v.CompiledComponentSchema(componentType)
		if !resolved {
			return nil
		}
		return pillarObjections(s, "values", contract,
			v.AuthoredValuesPillar(s.NodeID(), "values"),
			v.ResolvedParameterContext(s.SourceFile()),
			func(path string) []constraint.HeldBackNote { return v.HeldBackDetail(componentType, "values", path) })
	},
}

// componentInfraConformsToTheType reports each way the physical sizing a
// Release wrote for a component fails the contract its component type
// declares over it.
//
// It is the operator-side counterpart of the rule beside it, over the other
// pillar and the other document, and it is closed the same way: a key an
// operator sets must be one the component type declares, because that type's
// contract is the correct home for every such key.
//
// What is opted into is the component type's CONTRACT, never the Release's
// content, which is the same standing the pillar beside it has. Once the type
// declares a contract for this pillar, the sizing is measured for every
// component the Release wrote an entry against, including an entry carrying
// no sizing at all: an empty payload is the maximal violation of a key the
// contract declares required, not a case with nothing to check. A required key
// exists precisely to catch the entry that wrote nothing, so a rule that skipped
// it would be inert in the one case it was declared for.
//
// The components it measures are the ones the Release wrote entries against,
// read as authorship rather than as content, which is the standing the per-edge
// rule below has for the same reason. Every field of such an entry is optional,
// so an entry with none set carries nothing at all and the sizing fact reads its
// zero value for it — the same zero it reads for a component the Release
// wrote no entry against. The two need opposite treatment: an entry that writes
// nothing is the maximal violation of a required key, while a component this
// Release never placed is not its to answer for. Measuring the second would
// report a sizing fault against a document whose fault is placement, and it
// would do so before the composition is flattened — displacing the report a
// component nothing placed already earns, which names the two ways to place it
// and is what its author can act on. It would also make which diagnostic that
// author reads depend on whether the component's type happens to declare a
// required sizing key, for one and the same authoring mistake.
var componentInfraConformsToTheType = constraint.Check[
	constraint.NormalizedView,
	constraint.NodeSubject,
]{
	Sentinel: ErrSchemaConformance(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.NormalizedView,
		s constraint.NodeSubject,
	) []constraint.Objection {
		if !v.ReleaseComponentEntryPresent(s.NodeID()) {
			return nil
		}
		componentType, present := v.InstanceComponentType(s.NodeID())
		if !present || !v.CatalogComponentPresent(componentType) {
			return nil
		}
		contract, resolved := v.CompiledComponentSchema(componentType)
		if !resolved {
			return nil
		}
		return pillarObjections(s, "infra", contract,
			v.ReleaseComponentInfra(s.NodeID()),
			v.ResolvedParameterContext(s.SourceFile()),
			func(path string) []constraint.HeldBackNote { return v.HeldBackDetail(componentType, "infra", path) })
	},
}

// connectionValuesConformToTheSocket reports each way the configuration a
// component's own dependency block wrote for one connection fails the contract
// the socket declares over it.
//
// What is opted into is the SOCKET's contract, never the connection's content.
// Once a socket declares one, every connection the component wrote on that
// socket is measured against it, including a connection that wrote nothing —
// which is the maximal violation of a key the contract declares required rather
// than an exemption from it.
//
// The rule is asked of connections this document wrote, which is why it reads
// the entries the author wrote rather than judging every connection the build
// draws. A connection another document created carries no configuration from
// this one, and measuring its absence against this socket's contract would
// refuse a document that wrote nothing wrong.
//
// A connection whose alias names nothing is not a connection. The short
// authoring form gives each connection the target's own name as its alias, so a
// list item the author left blank identifies nothing, populates nothing, and
// carries no configuration to measure — and locating a finding at an empty alias
// would tell the author about a connection instead of about the list entry they
// left empty.
var connectionValuesConformToTheSocket = constraint.Check[
	constraint.NormalizedView,
	constraint.EdgeSubject,
]{
	Sentinel: ErrSchemaConformance(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.NormalizedView,
		s constraint.EdgeSubject,
	) []constraint.Objection {
		if !CompactItemNamesTarget(s.AliasName()) {
			return nil
		}
		if !slices.Contains(
			v.AuthoredUsesSocketEntryAliases(s.NodeID(), s.SocketName()), s.AliasName()) {
			return nil
		}
		componentType, present := v.InstanceComponentType(s.NodeID())
		if !present || !v.CatalogComponentPresent(componentType) {
			return nil
		}
		contract, resolved := v.CompiledSocketValueSchema(componentType, s.SocketName())
		if !resolved {
			return nil
		}
		return pillarObjections(s, "values", contract,
			v.PerEdgeValues(s.NodeID(), s.SocketName(), s.AliasName()),
			v.ResolvedParameterContext(s.SourceFile()), nil)
	},
}

// connectionInfraConformsToTheSocket reports each way the physical sizing a
// Release wrote for one connection fails the contract the socket declares
// over it.
//
// It is the operator-side counterpart of the rule beside it, over the other
// pillar and the other document, and the socket declares a separate contract for
// each — so a socket may constrain one pillar and say nothing about the other.
//
// It is invoked once per entry the Release's per-edge surface writes, which is
// what positions a finding at the entry the operator wrote — inside the boundary
// it sits in, under the keys reaching the component there — rather than at the
// consuming component's own dependency block, a path a Release placing its
// components in boundaries does not contain. The entries are read as authorship
// rather than as content. Both fields of such an entry are optional, so an entry
// with neither set carries nothing at all and every content fact reads its zero
// value for it; it is still an entry the operator wrote, and it is the maximal
// violation of a key the socket's contract declares required. A connection the
// Release wrote no entry for is not this document's to answer for, and no entry
// for it is enumerated.
//
// It measures the entries written against a component the Aggregate declares,
// whose type and contracts are known while the two documents are read. An entry
// against a component a sub-module contributes ends the rule here: that
// component's type is known only once the composition is flattened, where its
// per-edge sizing is judged with the module's other connections.
var connectionInfraConformsToTheSocket = constraint.Check[
	constraint.NormalizedView,
	constraint.ReleaseEdgeConfigurationSubject,
]{
	Sentinel: ErrSchemaConformance(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.NormalizedView,
		s constraint.ReleaseEdgeConfigurationSubject,
	) []constraint.Objection {
		node, socket, alias := s.NodeID(), s.SocketName(), s.AliasName()
		componentType, present := v.InstanceComponentType(node)
		if !present || !v.CatalogComponentPresent(componentType) {
			return nil
		}
		contract, resolved := v.CompiledSocketInfraSchema(componentType, socket)
		if !resolved {
			return nil
		}
		return pillarObjections(s, "infra", contract,
			v.ReleasePerEdgeInfra(node, socket, alias),
			v.ResolvedParameterContext(s.SourceFile()), nil)
	},
}
