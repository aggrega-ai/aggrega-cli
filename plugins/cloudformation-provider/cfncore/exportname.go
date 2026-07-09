// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cfncore

import "strings"

// exportNamePrefix is the fixed leading segment stamped on every assembled
// export name, so a name this core produces is recognisable as Aggrega-authored
// and never collides with an unrelated export in the same account and region.
const exportNamePrefix = "aggrega"

// emptySegmentPlaceholder is the deterministic stand-in for a segment whose
// input is the empty string. An absent deployment identity, boundary, component,
// or provides key still yields a well-formed, stable export name rather than a
// name carrying an empty run between separators.
const emptySegmentPlaceholder = "none"

// ExportName builds the deterministic CloudFormation export name for one
// cross-stack value: aggrega-<release>-<boundary>-<component>-<providesKey>.
// The four caller-supplied segments carry, in order, the deployment/environment
// identity, the boundary path (a boundary is one stack), the component identity,
// and the provides key whose value the export publishes.
//
// Each segment is sanitised to the characters a CloudFormation export name
// admits — ASCII letters, digits, and the hyphen — with every other character
// mapped to a hyphen, and an empty segment rendered as a fixed placeholder, so
// the name is well-formed and byte-stable across runs of identical input. The
// name is a deterministic pointer string: it is assembled ahead of time from the
// build's own identity and never resolved against any AWS state. Uniqueness
// within an account and region follows by construction from the caller scoping
// the name with the boundary and component identity.
func ExportName(release, boundary, component, providesKey string) string {
	return strings.Join([]string{
		exportNamePrefix,
		sanitizeExportSegment(release),
		sanitizeExportSegment(boundary),
		sanitizeExportSegment(component),
		sanitizeExportSegment(providesKey),
	}, "-")
}

// StackName builds the deterministic CloudFormation stack name for one
// boundary: aggrega-<release>-<boundary>. A boundary assembles into one stack,
// so the boundary path scopes the name; the deployment/environment identity
// leads it. Each segment is sanitised to the ASCII letters, digits, and hyphen a
// stack name admits, with an empty segment rendered as a fixed placeholder, so
// the name is well-formed and byte-stable across runs of identical input. The
// name is a deterministic pointer string: it is assembled ahead of time from the
// build's own identity and never resolved against any AWS state — a cross-stack
// read names the producer stack by this pointer, and the runtime environment
// resolves it at deploy time.
func StackName(release, boundary string) string {
	return strings.Join([]string{
		exportNamePrefix,
		sanitizeExportSegment(release),
		sanitizeExportSegment(boundary),
	}, "-")
}

// sanitizeExportSegment maps one export-name segment to the CloudFormation
// export-name character set: ASCII letters, digits, and the hyphen survive, and
// every other rune becomes a hyphen. An empty input yields the fixed empty
// segment placeholder. A non-empty input never maps to the placeholder, so a
// segment authored as the literal placeholder and an absent segment stay
// distinguishable only where the caller supplies a non-empty value.
func sanitizeExportSegment(s string) string {
	if s == "" {
		return emptySegmentPlaceholder
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			return r
		default:
			return '-'
		}
	}, s)
}
