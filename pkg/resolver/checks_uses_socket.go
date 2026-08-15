// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"slices"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// The socket-name rules, expressed as constraint declarations. See checks.go for
// what a declaration here is and why these are unexported.
//
// One rule spans four authoring surfaces and is declared four times, once per
// subject kind, because an attachment is written at four different scopes and a
// finding has to name the one the author wrote. The predicate itself is shared,
// so the four cannot disagree about what a valid socket name is; what differs
// between them is only which surface's facts supply the attachment's type and
// the sockets it wires.
//
// The component's own dependency block is deliberately absent from that set. An
// author naming a socket their component type declares no entry for is authoring
// the product accepts, so there is no rule over it to declare — what remains on
// that surface is the catalog-view classification, which is its own rule with
// its own sentinel.

// attachmentSocketObjections is the comparison at the heart of the attachment
// socket rule: an attachment may wire only a socket the Feature it attaches
// declares.
//
// A Feature declaring no socket at all is the most constrained answer rather
// than an absent one, so every socket such an attachment wires is refused. That
// is the same comparison as the general case — a name absent from the declared
// set — which is why one expression covers both and neither can drift from the
// other.
//
// It takes the two name sets rather than the view they were read from, and that
// is a constraint of the declaration rather than a style: a rule's point in the
// compilation is computed from the facts its predicate is READ to call, so a
// rule handing its view to a helper puts its own reads where that computation
// cannot follow them and gets no verdict at all. Each declaration below
// therefore reads its own facts in its own body and passes the values here.
//
// The detail is the Feature type, the socket the attachment wired, and then the
// socket names that Feature declares, in author order — the alternatives an
// author picks from, carried as values so a reader of the objection has them
// without a view to read them from.
func attachmentSocketObjections(
	s constraint.Subject,
	featureType string,
	wired []string,
	declared []string,
) []constraint.Objection {
	var out []constraint.Objection
	for _, socket := range wired {
		if slices.Contains(declared, socket) {
			continue
		}
		out = append(out, s.Raise(ErrUnknownUsesSocket(),
			append([]string{featureType, socket}, declared...)...))
	}
	return out
}

// componentAttachmentSocketDeclared reports each socket an attachment written on
// a component wires that the Feature it attaches does not declare.
//
// It is the per-component surface of the attachment socket rule. An attachment
// the application does not declare under this name on this component ends the
// rule here: the Release may declare one under the same name, and that surface
// is a separate declaration with its own rule, so reading one for the other
// would measure a Release-authored attachment's sockets against an application
// attachment's type.
var componentAttachmentSocketDeclared = constraint.Check[
	constraint.NormalizedView,
	constraint.NodeAttachmentSubject,
]{
	Sentinel: ErrUnknownUsesSocket(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.NormalizedView,
		s constraint.NodeAttachmentSubject,
	) []constraint.Objection {
		featureType, written := v.AuthoredAttachmentType(s.NodeID(), s.AttachmentName())
		if !written {
			return nil
		}
		wired := v.AttachmentAuthoredUsesSocketNames(s.NodeID(), s.AttachmentName())
		if len(wired) == 0 || !v.CatalogFeaturePresent(featureType) {
			return nil
		}
		return attachmentSocketObjections(s, featureType, wired,
			v.AttachmentDeclaredSocketNames(featureType))
	},
}

// releaseComponentAttachmentSocketDeclared reports each socket an attachment the
// deployment writes against a named component wires that the Feature it attaches
// does not declare.
//
// It is the deployment-side twin of the per-component surface. Adding an
// attachment a component's own document never declared is supported there, so
// the rule has to be asked of the deployment's own declaration rather than
// inferred from the application's.
var releaseComponentAttachmentSocketDeclared = constraint.Check[
	constraint.NormalizedView,
	constraint.NodeAttachmentSubject,
]{
	Sentinel: ErrUnknownUsesSocket(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.NormalizedView,
		s constraint.NodeAttachmentSubject,
	) []constraint.Objection {
		featureType, written := v.ReleaseAttachmentType(s.NodeID(), s.AttachmentName())
		if !written {
			return nil
		}
		wired := v.ReleaseAttachmentAuthoredUsesSocketNames(s.NodeID(), s.AttachmentName())
		if len(wired) == 0 || !v.CatalogFeaturePresent(featureType) {
			return nil
		}
		return attachmentSocketObjections(s, featureType, wired,
			v.AttachmentDeclaredSocketNames(featureType))
	},
}

// subsystemAttachmentSocketDeclared reports each socket an attachment written at
// an application scope wires that the Feature it attaches does not declare.
//
// It is keyed by the scope rather than by a component because that is where the
// author wrote it: one such attachment reaches every component the scope holds,
// so a rule keyed per component would ask the same question once per component
// and never at all for a scope holding none — silence a reader cannot tell from
// approval.
var subsystemAttachmentSocketDeclared = constraint.Check[
	constraint.NormalizedView,
	constraint.SubsystemAttachmentSubject,
]{
	Sentinel: ErrUnknownUsesSocket(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.NormalizedView,
		s constraint.SubsystemAttachmentSubject,
	) []constraint.Objection {
		declaration, written := v.SubsystemAttachmentDeclaration(s.SubsystemPath(), s.AttachmentName())
		if !written {
			return nil
		}
		if len(declaration.AuthoredSocketNames) == 0 ||
			!v.CatalogFeaturePresent(declaration.FeatureType) {
			return nil
		}
		return attachmentSocketObjections(s, declaration.FeatureType,
			declaration.AuthoredSocketNames,
			v.AttachmentDeclaredSocketNames(declaration.FeatureType))
	},
}

// boundaryAttachmentSocketDeclared reports each socket an attachment written at
// a deployment scope wires that the Feature it attaches does not declare.
//
// It is the deployment-side twin of the application-scope surface, keyed by that
// scope for the same reason: the attachment is authored once and contributes
// many times, and a scope holding no component would otherwise never be asked.
var boundaryAttachmentSocketDeclared = constraint.Check[
	constraint.NormalizedView,
	constraint.BoundaryAttachmentSubject,
]{
	Sentinel: ErrUnknownUsesSocket(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.NormalizedView,
		s constraint.BoundaryAttachmentSubject,
	) []constraint.Objection {
		declaration, written := v.BoundaryAttachmentDeclaration(s.BoundaryPath(), s.AttachmentName())
		if !written {
			return nil
		}
		if len(declaration.AuthoredSocketNames) == 0 ||
			!v.CatalogFeaturePresent(declaration.FeatureType) {
			return nil
		}
		return attachmentSocketObjections(s, declaration.FeatureType,
			declaration.AuthoredSocketNames,
			v.AttachmentDeclaredSocketNames(declaration.FeatureType))
	},
}

// releaseEdgeSocketDeclared reports each socket a deployment's own edge surface
// writes on a component that the component's type does not declare.
//
// The rule survives the relaxation on the component's own dependency block, and
// the asymmetry is the point: an application author owns their application's
// topology and may name a socket the catalog did not anticipate, while the
// deployment surface wires and configures within an architecture it did not
// author. Letting it name a socket nothing declares would let it introduce
// topology the application never declared.
//
// It is invoked once per component rather than once per socket, because the
// socket axis enumerates what a component type DECLARES and the socket this
// rule refuses is by definition not among them — so a per-socket invocation
// would never be presented with its own subject. The set the deployment wrote is
// read as a fact instead, and the rule compares the two.
//
// A component whose type the application does not declare ends the rule: its
// document is loaded only once the composition is flattened, so there is no type
// here to read a declared socket set from. A type the catalog holds no entry for
// ends it too — the declared set reads empty for it, and refusing every socket
// wired on a type nobody found would report the wrong thing. What that absence
// means is a separate rule's question.
//
// The detail is the component type, the socket the deployment wrote, and then
// the socket names that type declares, in author order.
var releaseEdgeSocketDeclared = constraint.Check[
	constraint.NormalizedView,
	constraint.NodeSubject,
]{
	Sentinel: ErrUnknownUsesSocket(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.NormalizedView,
		s constraint.NodeSubject,
	) []constraint.Objection {
		wired := v.ReleaseAuthoredUsesSocketConnections(s.NodeID())
		if wired.Len() == 0 {
			return nil
		}
		componentType, present := v.InstanceComponentType(s.NodeID())
		if !present || !v.CatalogComponentPresent(componentType) {
			return nil
		}
		declared := v.DeclaredSocketNames(componentType)
		var out []constraint.Objection
		for socket := range wired.All() {
			if slices.Contains(declared, socket) {
				continue
			}
			out = append(out, s.Raise(ErrUnknownUsesSocket(),
				append([]string{componentType, socket}, declared...)...))
		}
		return out
	},
}
