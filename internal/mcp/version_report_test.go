// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// The versions the tests below hand a server. Both are shaped like no release,
// so nothing reports them by coincidence and no release has to touch this file.
const (
	reportServerVersionProbe = "0.0.0-server-version-probe"
	reportBundleVersionProbe = "0.0.0-bundle-version-probe"
)

// TestTheVersionLineNamesBothVersionsAndSaysWhenTheyDiffer walks the four
// shapes a launch reports its versions in: a server with no version of its own,
// which no launch of the binary is and which says nothing; no bundle; a bundle
// of the server's own version; and a bundle of another. Only the last carries
// the sentence saying the two come from different releases, and there the line
// is that sentence exactly, so the capability answer and the connect-time
// guidance say it in the same words.
//
// 🔴 PLANTED DEFECT: replace the comparison `bundleVersion == serverVersion` in
// releaseDifference with `true`, and the differing row fails twice — its line is
// empty and it carries no difference sentence.
func TestTheVersionLineNamesBothVersionsAndSaysWhenTheyDiffer(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                  string
		server, bundle        string
		wantLine, wantDiffers string
	}{
		{name: "a server reporting no version", server: "", bundle: reportBundleVersionProbe},
		{name: "no bundle", server: reportServerVersionProbe,
			wantLine: "This server runs aggrega " + reportServerVersionProbe + "."},
		{name: "a bundle of the server's own version", server: reportServerVersionProbe, bundle: reportServerVersionProbe,
			wantLine: "This server runs aggrega " + reportServerVersionProbe +
				", launched by the desktop bundle of the same version, " + reportServerVersionProbe + "."},
		{name: "a bundle of another version", server: reportServerVersionProbe, bundle: reportBundleVersionProbe,
			wantLine: "This server runs aggrega " + reportServerVersionProbe + ", launched by the desktop bundle " +
				"version " + reportBundleVersionProbe + ": the aggrega executable and the bundle come from " +
				"different releases.",
			wantDiffers: "This server runs aggrega " + reportServerVersionProbe + ", launched by the desktop bundle " +
				"version " + reportBundleVersionProbe + ": the aggrega executable and the bundle come from " +
				"different releases."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := versionLine(tc.server, tc.bundle); got != tc.wantLine {
				t.Errorf("versionLine(%q, %q) = %q; want %q", tc.server, tc.bundle, got, tc.wantLine)
			}
			if got := releaseDifference(tc.server, tc.bundle); got != tc.wantDiffers {
				t.Errorf("releaseDifference(%q, %q) = %q; want %q", tc.server, tc.bundle, got, tc.wantDiffers)
			}
		})
	}
}

// TestOnlyTheArgumentlessCapabilityAnswerReportsTheVersions drives the
// capability tool on a server launched by a bundle of another release, and holds
// the versions to the one answer that says what the server is: the argumentless
// index carries server_version and bundle_version and opens its text with the
// line naming them, while a kind-narrowed index and a call naming tools carry
// neither key and no such line. A server no bundle launched carries
// server_version alone.
func TestOnlyTheArgumentlessCapabilityAnswerReportsTheVersions(t *testing.T) {
	t.Parallel()
	launched := &server{out: io.Discard, logw: io.Discard, serverVersion: reportServerVersionProbe,
		bundleVersion: reportBundleVersionProbe, launchRoot: t.TempDir()}
	unbundled := &server{out: io.Discard, logw: io.Discard, serverVersion: reportServerVersionProbe,
		launchRoot: t.TempDir()}

	keysOf := func(t *testing.T, res *callToolResult) map[string]json.RawMessage {
		t.Helper()
		if res.IsError {
			t.Fatalf("describe-capabilities reported error: %s", res.Content[0].Text)
		}
		var keys map[string]json.RawMessage
		if err := json.Unmarshal(res.StructuredContent, &keys); err != nil {
			t.Fatalf("decode describe-capabilities payload: %v", err)
		}
		return keys
	}

	index := callTool(t, launched, describeCapabilitiesName, describeCapabilitiesArgs{})
	keys := keysOf(t, index)
	if got := string(keys["server_version"]); got != `"`+reportServerVersionProbe+`"` {
		t.Errorf("the argumentless index carries server_version %s; want %q", got, reportServerVersionProbe)
	}
	if got := string(keys["bundle_version"]); got != `"`+reportBundleVersionProbe+`"` {
		t.Errorf("the argumentless index carries bundle_version %s; want %q", got, reportBundleVersionProbe)
	}
	if first, _, _ := strings.Cut(index.Content[0].Text, "\n"); first !=
		versionLine(reportServerVersionProbe, reportBundleVersionProbe) {
		t.Errorf("the argumentless index's text opens with %q; want the line naming both versions", first)
	}

	unbundledKeys := keysOf(t, callTool(t, unbundled, describeCapabilitiesName, describeCapabilitiesArgs{}))
	if _, carried := unbundledKeys["bundle_version"]; carried {
		t.Errorf("a server no bundle launched carries bundle_version %s", unbundledKeys["bundle_version"])
	}
	if _, carried := unbundledKeys["server_version"]; !carried {
		t.Error("a server no bundle launched carries no server_version")
	}

	for _, narrowed := range []struct {
		name string
		args describeCapabilitiesArgs
	}{
		{name: "narrowed to a kind", args: describeCapabilitiesArgs{Kind: "release"}},
		{name: "naming tools", args: describeCapabilitiesArgs{Tools: []string{describeCapabilitiesName}}},
	} {
		res := callTool(t, launched, describeCapabilitiesName, narrowed.args)
		keys := keysOf(t, res)
		for _, key := range []string{"server_version", "bundle_version"} {
			if raw, carried := keys[key]; carried {
				t.Errorf("an answer %s carries %s %s; only the argumentless index reports the versions",
					narrowed.name, key, raw)
			}
		}
		if strings.Contains(res.Content[0].Text, "This server runs aggrega") {
			t.Errorf("an answer %s names the versions in its text:\n%s", narrowed.name, res.Content[0].Text)
		}
	}
}

// TestBothRevisionsOpenTheirGuidanceWithTheReleaseDifference holds the
// connect-time guidance to the one sentence: a server launched by a bundle of
// another release opens the guidance it hands both protocol revisions — the
// handshake's instructions and server/discover's — with releaseDifference's
// sentence, followed by exactly the guidance a server with no bundle hands; one
// launched by a bundle of its own release hands that guidance unchanged.
func TestBothRevisionsOpenTheirGuidanceWithTheReleaseDifference(t *testing.T) {
	t.Parallel()
	build := func(bundle string) *server {
		return &server{out: io.Discard, logw: io.Discard, serverVersion: reportServerVersionProbe,
			bundleVersion: bundle, launchRoot: t.TempDir()}
	}
	guidance := func(t *testing.T, s *server) (handshake, discovered string) {
		t.Helper()
		initialized, rpcErr := s.handleInitialize(nil)
		if rpcErr != nil {
			t.Fatalf("initialize: %+v", rpcErr)
		}
		answered, rpcErr := s.handleDiscover()
		if rpcErr != nil {
			t.Fatalf("server/discover: %+v", rpcErr)
		}
		return initialized.(initializeResult).Instructions, answered.(discoverResult).Instructions
	}

	plain, _ := guidance(t, build(""))
	if plain != instructionsFor(transportStdio) {
		t.Fatalf("a server no bundle launched hands guidance other than the transport's own")
	}
	want := releaseDifference(reportServerVersionProbe, reportBundleVersionProbe) + "\n\n" + plain

	for _, tc := range []struct {
		name, bundle, want string
	}{
		{name: "a bundle of another release", bundle: reportBundleVersionProbe, want: want},
		{name: "a bundle of the server's own release", bundle: reportServerVersionProbe, want: plain},
	} {
		handshake, discovered := guidance(t, build(tc.bundle))
		if handshake != tc.want {
			t.Errorf("%s: the handshake's instructions are\n%s\nwant\n%s", tc.name, handshake, tc.want)
		}
		if discovered != handshake {
			t.Errorf("%s: server/discover hands guidance the handshake does not", tc.name)
		}
	}
}
