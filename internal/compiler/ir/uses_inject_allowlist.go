// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// ErrUsesInjectForbidden reports an `inject:` block on a `uses:` edge that the
// socket's declaration does not permit. One sentinel covers both refusals,
// because they are one rule read on two inputs — the socket says which delivery
// mechanisms an edge wired through it may dictate, and the edge named one it does
// not say:
//
//   - the socket declares an `inject:` allowlist and the edge dictates a mechanism
//     that is not a member of it;
//   - the socket declares no allowlist, or an empty one, so it permits no
//     dictation at all and the edge dictates something. An entity whose delivery
//     is opaque declares its socket without `inject:` and the consumer takes
//     whatever the producer's Recipe emits — the Black-Box pattern.
//
// The two carry different advice, which is what tells an author whether to widen
// an allowlist or to stop dictating; the condition they report is the same one,
// so splitting the sentinel would ask a programmatic consumer to match two values
// for one question.
//
// A socket the declaring entity never declared is OUT of scope and reaches no
// refusal here. An ad-hoc socket has no `usesSchema` entry, so it carries no
// allowlist to be outside of and makes no Black-Box promise to keep: what such a
// socket gives up is the declaration's guarantees, not the author's ability to
// route what they invented. The build's other rules still bound what the edge may
// carry.
//
// Per the Aggrega architectural invariant set (Invariant 4): policy violations
// produce Loud Guardrails — the build halts naming the socket, the offending
// mechanism, and what the socket does permit — never a silent drop of the
// dictation.
func ErrUsesInjectForbidden() error { return errUsesInjectForbidden }

var errUsesInjectForbidden = cerrors.Register("uses-inject-forbidden", cerrors.Meta{
	ShortCode: "E0015",
	Message:   "uses-edge inject dictates a delivery mechanism the socket does not permit",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// The surfaces a dependency socket can be declared on, named as the author reads
// them. They travel into a diagnostic's caption, so an author is told which
// entity's declaration was quoted rather than being left to work it out from the
// identity alone.
const (
	surfaceComponentType = "declared on the component type"
	surfaceFeatureType   = "declared on the feature type"
)

// CarriedConnectionDeliveryIsSocketPermitted reports each delivery mechanism a
// connection dictates that the socket it is wired into does not permit, wherever
// that connection was written.
//
// It is invoked once per connection together with the surface that drew it,
// because the delivery it reads is what an AUTHOR wrote on one connection: a
// node, a socket and an alias name up to three separately authored blocks at
// once, and a rule invoked once for the three would read one of them and report
// the other two as approved. The delivery is read as authored rather than as the
// graph stores it, because the conversion into the graph drops a payload it
// cannot express as one string per delivery key — so a rule reading the converted
// form would see a connection as dictating nothing when its payload was dropped.
//
// # Which entity's declaration is quoted
//
// The socket is read on whichever entity declared it, and the surface that drew
// the connection is what picks between them: a connection an attachment's own
// dependency block drew participates in the attached Feature's socket
// declarations, and every other connection in the consuming component type's.
// Consulting the component alone would permit every attachment-drawn dictation
// without reading the declaration that governs it.
//
// The Feature type is the one the attachment RESOLVED to rather than the one
// stamped on the connection when it was drawn. The resolved type is the
// composition's single answer to what an attachment is, which every rule over a
// Feature's declarations at this stage reads, and a second reading taken from
// the connection would be free to disagree with it.
//
// A socket the declaring entity never declared is out of scope and reaches no
// refusal: an ad-hoc socket carries no allowlist to be outside of and makes no
// Black-Box promise to keep. What such a socket gives up is the declaration's
// guarantees, not the author's ability to route what they invented.
//
// # What each occurrence says
//
// The two refusals carry the same condition and different advice, because the
// remedy differs between them and no single registered sentence could be true of
// both: a socket declaring no allowlist permits no dictation at all, while one
// declaring an allowlist permits what it lists. The detail names the mechanism
// the sentence refers to, then the socket, then — where there is one — what that
// socket does permit, then the entity whose declaration was quoted.
//
// Findings come back in the order the composition drew the connections and then
// in the order the author wrote the mechanisms, so identical input yields an
// identical sequence.
var CarriedConnectionDeliveryIsSocketPermitted = constraint.Check[
	constraint.GraphView,
	constraint.CarriedConnectionSubject,
]{
	Sentinel: ErrUsesInjectForbidden(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.GraphView,
		s constraint.CarriedConnectionSubject,
	) []constraint.Objection {
		dictated := v.CarriedConnectionAuthoredDelivery(
			s.NodeID(), s.CarrierKind(), s.CarrierAttachment(),
			s.SocketName(), s.AliasName(), s.TargetNodeID())
		if len(dictated) == 0 {
			return nil
		}

		// The declaring entity is resolved here rather than in a helper: the
		// facts a check reads are derived from its own body, so a helper handed
		// the view would take this rule's reads out of reach of the reading that
		// holds it to the narrowest view its predicate allows.
		var surface, owner string
		var allowed []string
		if s.CarrierKind() == constraint.CarrierAttachment {
			owner = v.AttachmentResolvedType(s.NodeID(), s.CarrierAttachment())
			if !slices.Contains(v.AttachmentDeclaredSocketNames(owner), s.SocketName()) {
				return nil
			}
			surface = surfaceFeatureType
			allowed = v.AttachmentSocketInjectMechanismNames(owner, s.SocketName())
		} else {
			componentType, present := v.FlattenedNodeComponentType(s.NodeID())
			if !present || !slices.Contains(v.DeclaredSocketNames(componentType), s.SocketName()) {
				return nil
			}
			surface, owner = surfaceComponentType, componentType
			allowed = v.SocketInjectMechanismNames(componentType, s.SocketName())
		}

		var out []constraint.Objection
		for _, payloads := range dictated {
			mechanisms := payloads.Keys()
			if len(mechanisms) == 0 {
				continue
			}
			// An absent allowlist and an explicitly empty one are the same
			// declaration: the socket permits no dictation. Reading them apart
			// would make an empty list mean something no authoring surface
			// documents.
			if len(allowed) == 0 {
				out = append(out, s.RaiseWithAdvice(
					ErrUsesInjectForbidden(),
					"The socket declares no delivery allowlist, so it permits no dictation at all — "+
						"the entity that declared it delivers the dependency's values itself. Remove the "+
						"inject block from this connection and read the values from the connection's "+
						"provides in the consuming Recipe, or declare the mechanisms the socket permits "+
						"in its usesSchema entry.",
					constraint.Uncaptioned(strings.Join(mechanisms, ", ")),
					constraint.Captioned("on socket", s.SocketName()),
					constraint.Captioned(surface, owner),
				))
				continue
			}
			for _, mechanism := range mechanisms {
				if slices.Contains(allowed, mechanism) {
					continue
				}
				out = append(out, s.RaiseWithAdvice(
					ErrUsesInjectForbidden(),
					"Dictate one of the mechanisms the socket permits, or widen that socket's inject "+
						"allowlist in the declaring entity's usesSchema entry.",
					constraint.Uncaptioned(mechanism),
					constraint.Captioned("on socket", s.SocketName()),
					constraint.Captioned("which permits", strings.Join(allowed, ", ")),
					constraint.Captioned(surface, owner),
				))
			}
		}
		return out
	},
}
