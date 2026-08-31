// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

import (
	"fmt"
	"unicode/utf8"
)

// ComponentIDLabelKind is what a verdict about the component-id label value
// calls itself. It reaches a reader verbatim in a refusal, so it names the thing
// the reader would look for in the rendered output rather than the function that
// writes it.
const ComponentIDLabelKind = "component-id label value"

// componentIDLabelMaxLength is the longest string Kubernetes accepts as a label
// value.
//
// It sits here, beside [BuildAggregaProvenanceLabels], because this is the one
// place that writes a component identity into a Kubernetes label — so the rule
// and the write are one thing that cannot drift apart. It is deliberately NOT in
// the compiler's identity package: a number there would be one target's limit
// applied to every target, including the targets that impose none, and a
// compiler that holds a target's limits is a compiler that has learned something
// about a target it renders for. What the compiler holds instead is whatever a
// guest answers, and this is a guest's answer.
const componentIDLabelMaxLength = 63

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
func ComponentIDLabelVerdict(proposed string) DerivedNameVerdict {
	if proposed == "" {
		return DerivedNameVerdict{Kind: ComponentIDLabelKind, Usable: true}
	}
	if n := len(proposed); n > componentIDLabelMaxLength {
		bound := componentIDLabelMaxLength
		return DerivedNameVerdict{
			Kind:   ComponentIDLabelKind,
			Usable: false,
			Bound:  &bound,
			Reason: fmt.Sprintf("a label value is at most %d characters and this identity is %d",
				componentIDLabelMaxLength, n),
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
