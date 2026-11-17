// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package rendercore

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
)

// CrossBoundaryLinkSentinelPrefix is the fixed leading run every cross-boundary
// link placeholder carries. It is domain-neutral and recognizable, so a scan of
// assembled output can detect a placeholder that survived resolution: a
// placeholder is a build-internal marker that must never reach shipped output,
// and a surviving one is a defect the build halts on rather than emits.
const CrossBoundaryLinkSentinelPrefix = "aggrega-cross-boundary-link-"

// CrossBoundaryLinkSentinel builds the deterministic placeholder token for one
// cross-boundary link injection. It is the generic, provider-agnostic host
// mechanism for deferring a cross-boundary consumed value to provider-native
// reference wiring at the post-assembly link pass: the host plants a
// domain-neutral placeholder at render time, and a provider replaces it
// with its own provider-native cross-reference once every dispatch group has
// assembled. The placeholder and the deferral are provider-agnostic — any
// provider that emits native cross-references reuses this mechanism
// unchanged, with the reference fill the only provider-specific step.
//
// The token is the value the host stamps into a consuming node's injected input
// in place of the delivered scalar, so the consumer renders the placeholder into
// its output where the delivered value would have gone. A provider locates
// the placeholder in the consuming endpoint's assembled output and replaces it
// with the provider-native cross-reference.
//
// The token is a stable hash of the link's own identity — the consuming node,
// the consumed node, the socket the edge is bound under, and the consumed
// output key the injection carries — prefixed with the recognizable
// placeholder prefix. Both the stamping host and the resolving provider
// compute it from the same identity, so the token one side plants is the token
// the other side searches for. It carries no domain vocabulary and no value
// content: it is a whole-scalar marker, not a formatted reference.
func CrossBoundaryLinkSentinel(source, target, socket, providesKey string) string {
	sum := sha256.Sum256([]byte(source + "\x1f" + target + "\x1f" + socket + "\x1f" + providesKey))
	return CrossBoundaryLinkSentinelPrefix + hex.EncodeToString(sum[:])
}

// crossBoundaryLinkReadMarker follows the shared prefix in a read placeholder and
// in no delivery placeholder, whose prefix is followed by hexadecimal digits
// alone — so the two forms can never be mistaken for one another.
const crossBoundaryLinkReadMarker = "read-"

// CrossBoundaryLinkReadPlaceholder builds the deterministic placeholder the host
// stamps in place of one provides value a consuming node's templates can read
// directly off a cross-boundary link, rather than receive on the edge's delivery
// pipe.
//
// It is distinct from [CrossBoundaryLinkSentinel] for the same link and key, and
// the distinction is what makes the host's check of a read possible: the host
// evaluates a template once over these placeholders and once over the real values,
// writes each placeholder in the first output back to its real value, and the two
// must then agree byte for byte. A delivered placeholder sitting in the same output
// is left alone by that write-back because it is a different string. Once the check
// passes, each read placeholder in the output is rewritten to the delivery
// placeholder for the same key, which is the one form a provider locates — so this
// form never leaves the host.
//
// It shares [CrossBoundaryLinkSentinelPrefix], so one that survived the host's own
// rewrite would still be caught by the scan for unresolved placeholders.
func CrossBoundaryLinkReadPlaceholder(source, target, socket, providesKey string) string {
	sum := sha256.Sum256([]byte("read\x1f" + source + "\x1f" + target + "\x1f" + socket + "\x1f" + providesKey))
	return CrossBoundaryLinkSentinelPrefix + crossBoundaryLinkReadMarker + hex.EncodeToString(sum[:])
}

// LinkInjectedKeys returns the provides keys a cross-boundary link's inject
// mapping delivers, under every mechanism it names — env, file, arg and any
// mechanism of the author's naming alike, because the host stamps a placeholder
// for a key under each. Each key is listed once and the list is sorted, so what a
// provider derives from it is byte-identical across runs of identical input.
func LinkInjectedKeys(edge LinkEdge) []string {
	seen := map[string]struct{}{}
	for _, keymap := range edge.Inject.All() {
		for _, providesKey := range keymap.All() {
			seen[providesKey] = struct{}{}
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
