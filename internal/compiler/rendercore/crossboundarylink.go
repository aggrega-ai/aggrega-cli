// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

import (
	"crypto/sha256"
	"encoding/hex"
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
