// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/ociref"
)

func TestSplitVersionedURI(t *testing.T) {
	t.Parallel()

	cases := []struct {
		uri         string
		wantBase    string
		wantVersion string
		wantHasVer  bool
	}{
		{"core/api-server:1.0.0", "core/api-server", "1.0.0", true},
		{"core/api-server:^1.0.0", "core/api-server", "^1.0.0", true},
		{"core/api-server", "core/api-server", "", false},
		// This pair differs ONLY in hasVersion: a trailing colon is a present
		// but empty version, an absent colon is no version at all. A caller
		// reading base and version alone cannot tell them apart.
		{"core/api-server:", "core/api-server", "", true},
		{"hub.aggrega.ai/component/core/api-server:1.0.0", "hub.aggrega.ai/component/core/api-server", "1.0.0", true},
		{"hub.aggrega.ai/component/core/api-server", "hub.aggrega.ai/component/core/api-server", "", false},
		// A registry host carrying a PORT puts a colon before the first slash,
		// which is the one input shape on which this rule and the OCI tag rule
		// disagree. This rule takes the last colon anywhere, so the port colon
		// wins when nothing follows it; the OCI rule confines its search past
		// the last slash and reports no version at all. Pinned here because the
		// two functions take the same argument and return the same shape, so a
		// caller that reached for the wrong one would still compile.
		{"localhost:5000/core/api-server", "localhost", "5000/core/api-server", true},
		// The same host-and-port reference WITH a version tag, where the two
		// rules agree: the last colon anywhere and the last colon after the
		// last slash are the same colon. Kept as the realistic full shape, and
		// deliberately not the guard above, since it discriminates nothing.
		{"localhost:5000/core/api-server:1.0.0", "localhost:5000/core/api-server", "1.0.0", true},
		// A caret range on a registry-qualified base. Both are reachable
		// together — a reference may be written against a registry host and
		// still pin a range rather than an exact version — and the pairing is
		// where a one-character move of the base/version boundary would change
		// which entity is selected without failing anything. The caret survives
		// into the version segment whole, port or no port, because a version
		// tag always supplies a colon later in the string than the host's.
		{"hub.aggrega.ai/component/core/api-server:^1.0.0", "hub.aggrega.ai/component/core/api-server", "^1.0.0", true},
		{"localhost:5000/core/api-server:^1.0.0", "localhost:5000/core/api-server", "^1.0.0", true},
	}
	for _, tc := range cases {
		t.Run(tc.uri, func(t *testing.T) {
			base, version, hasVer := SplitVersionedURI(tc.uri)
			if base != tc.wantBase || version != tc.wantVersion || hasVer != tc.wantHasVer {
				t.Errorf("SplitVersionedURI(%q) = (%q, %q, %v); want (%q, %q, %v)",
					tc.uri, base, version, hasVer, tc.wantBase, tc.wantVersion, tc.wantHasVer)
			}
		})
	}
}

// TestSplitVersionedURIIsNotTheOCITagRule asserts the two grammars are distinct
// on the input that separates them. It is a deliberate cross-check rather than a
// duplicate of the table above: the two rules accept the same argument and return
// the same shape, so substituting one for the other compiles cleanly and changes
// the answer only on a reference carrying a colon before a slash — which is every
// registry-qualified reference written against a host with a port. A table that
// only pins this rule's own answers would still pass if some caller quietly
// reached for the other one, so this test names both and asserts they differ.
func TestSplitVersionedURIIsNotTheOCITagRule(t *testing.T) {
	t.Parallel()

	const hostAndPort = "localhost:5000/core/api-server"

	base, version, hasVer := SplitVersionedURI(hostAndPort)
	ociBase, ociVersion, ociHasVer := ociref.SplitVersionTag(hostAndPort)

	if base == ociBase && version == ociVersion && hasVer == ociHasVer {
		t.Fatalf("the catalog-reference split and the OCI tag split agree on %q — "+
			"(%q, %q, %v); they are separate rules for separate input shapes and "+
			"must not have been unified", hostAndPort, base, version, hasVer)
	}
	if base != "localhost" || version != "5000/core/api-server" || !hasVer {
		t.Errorf("SplitVersionedURI(%q) = (%q, %q, %v); want (%q, %q, %v)",
			hostAndPort, base, version, hasVer, "localhost", "5000/core/api-server", true)
	}
	if ociBase != hostAndPort || ociVersion != "" || ociHasVer {
		t.Errorf("ociref.SplitVersionTag(%q) = (%q, %q, %v); want (%q, %q, %v)",
			hostAndPort, ociBase, ociVersion, ociHasVer, hostAndPort, "", false)
	}
}
