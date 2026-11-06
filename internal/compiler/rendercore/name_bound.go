// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

import (
	"fmt"
	"unicode/utf8"

	"github.com/aggrega-ai/aggrega-cli/pkg/labels"
)

// ComponentIDLabelKind is what a verdict about the component-id label value
// calls itself. It reaches a reader verbatim in a refusal, so it names the thing
// the reader would look for in the rendered output rather than the function that
// writes it.
const ComponentIDLabelKind = "component-id label value"

// labelValueMaxLength is the longest string Kubernetes accepts as a label value.
// It bounds every label value the provenance label builder writes: the
// component identity, judged by [ComponentIDLabelVerdict], and every other
// provenance value, fitted by [fitProvenanceLabelValue].
//
// It sits here, beside [BuildAggregaProvenanceLabels], because that builder is
// the one place that writes those values into a Kubernetes label — so the rule
// and the write are one thing that cannot drift apart. It is deliberately NOT a
// rule of the compiler's identity package — whose sanitiser cuts at the same
// number privately, as part of its repair — because a rule there would be one target's limit
// applied to every target, including the targets that impose none, and a
// compiler that holds a target's limits is a compiler that has learned something
// about a target it renders for. What the compiler holds instead is whatever a
// guest answers, and this is a guest's answer.
const labelValueMaxLength = 63

// ComponentIDLabelVerdict judges a proposed component identity as the value of
// the component-id label every manifest a Kubernetes-emitting guest renders
// carries.
//
// It answers the three things a label value has to satisfy, and it answers them
// as a constraint rather than as a repair: a length, which a shorter identity
// can meet and which therefore comes back as a bound; and a character set and an
// edge rule, which no shortening repairs and which therefore come back with no
// bound at all. Distinguishing the two is what lets the compiler tell a name it
// should shorten from a name it should stop on.
//
// The bound is measured in bytes because the target counts bytes and because the
// compiler's own shortening cuts by byte, so a bound stated here is a bound the
// compiler can act on directly.
//
// # The LENGTH is measured on what will be stamped
//
// A proposal reaches here in one of two states, and authored says which. An
// identity a person WROTE arrives as they wrote it and is written through
// [labels.EncodeNodeIdentity] where it is stamped, so its length is taken from
// the written form. An identity the compiler COMPOSED was composed through an
// encoding of its own and is stamped exactly as it arrives, so its length is
// taken as it stands; writing it again would spell one identity two ways.
//
// Both states are the same rule: the length judged is the length the label will
// carry. Measuring a written identity as proposed would measure a value shorter
// than the one stamped — the character joining a module's name to a component's
// own is written out rather than dropped, so it costs more written than proposed —
// and an identity just inside the limit would then be accepted and stamped just
// outside it, with nothing reporting that anything was wrong.
//
// The bound on the written path is MEASURED rather than the limit restated,
// because the number reaches an author who acts on it. An author told the limit
// when their identity is written longer than they wrote it would choose a name of
// exactly that length and be refused a second time, so the bound is the greatest
// number of bytes of this identity that still fits once written.
//
// # The character set and the edge rule read the identity AS PROPOSED
//
// Only the length moves to the written form. The two grammar checks below judge
// the identity as it arrives, which is deliberately stricter than the label the
// writing would produce: writing removes a character the target refuses, so an
// identity the grammar checks turn away could in fact have been stamped legally.
//
// The strictness is kept rather than relaxed because relaxing it withdraws a
// refusal, and a refusal withdrawn is a change in what a build accepts — a
// larger question than the length this measures, and one about names that carry
// no composition separator at all. The two live at different widths on purpose,
// and unifying them is not a tidying.
func ComponentIDLabelVerdict(proposed string, authored bool) DerivedNameVerdict {
	if proposed == "" {
		return DerivedNameVerdict{Kind: ComponentIDLabelKind, Usable: true}
	}
	stamped := proposed
	if authored {
		stamped = labels.EncodeNodeIdentity(proposed)
	}
	if n := len(stamped); n > labelValueMaxLength {
		bound := labelValueMaxLength
		reason := fmt.Sprintf("a label value is at most %d characters and this identity is %d",
			labelValueMaxLength, n)
		// Only where writing the identity actually changed it are two lengths worth
		// reporting, and only there is the limit the wrong bound to state. Read from
		// the two values rather than from the flag, so an authored identity that is
		// written as authored — every ordinary name — gets the plainer answer.
		if stamped != proposed {
			bound = labels.NodeIdentityBound(proposed, labelValueMaxLength)
			reason = fmt.Sprintf("a label value is at most %d characters, and this identity of %d is stamped as %d "+
				"because a character it carries is written out rather than dropped, most often the dot joining a "+
				"module's name to a component's own", labelValueMaxLength, len(proposed), n)
		}
		return DerivedNameVerdict{
			Kind:   ComponentIDLabelKind,
			Usable: false,
			Bound:  &bound,
			Reason: reason,
		}
	}
	for _, r := range proposed {
		if isLabelValueRune(r) {
			continue
		}
		return DerivedNameVerdict{
			Kind:   ComponentIDLabelKind,
			Usable: false,
			Reason: fmt.Sprintf("a label value carries only letters, digits, '-', '_' and '.', and this identity carries %q", string(r)),
		}
	}
	first, _ := utf8.DecodeRuneInString(proposed)
	last, _ := utf8.DecodeLastRuneInString(proposed)
	if !isLabelValueAlphanumeric(first) || !isLabelValueAlphanumeric(last) {
		return DerivedNameVerdict{
			Kind:   ComponentIDLabelKind,
			Usable: false,
			Reason: "a label value begins and ends with a letter or a digit, and this identity does not",
		}
	}
	return DerivedNameVerdict{Kind: ComponentIDLabelKind, Usable: true}
}

// isLabelValueRune reports whether a character may appear anywhere inside a
// Kubernetes label value.
func isLabelValueRune(r rune) bool {
	return isLabelValueAlphanumeric(r) || r == '-' || r == '_' || r == '.'
}

// isLabelValueAlphanumeric reports whether a character is one a Kubernetes label
// value may begin or end with: an ASCII letter or digit.
func isLabelValueAlphanumeric(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}
