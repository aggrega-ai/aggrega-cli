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
// capability tool on servers launched by a bundle of another release, by a
// bundle of the server's own release and by no bundle, and holds the versions to
// the one answer that says what the server is: the argumentless index carries
// server_version, bundle_version where a bundle launched the server, and
// version_summary, the line its text opens with — the line naming both, saying
// so where the two come from different releases. A kind-narrowed index and a
// call naming tools carry none of the three keys and no such line.
//
// 🔴 PLANTED DEFECT: in describeCapabilitiesInvoke, set VersionSummary from
// releaseDifference rather than versionLine, and the rows of a bundle of the
// server's own release and of no bundle fail: their version_summary is absent,
// and their text opens with the index rather than the line. Set it to any fixed
// string instead, and every row fails on the field.
func TestOnlyTheArgumentlessCapabilityAnswerReportsTheVersions(t *testing.T) {
	t.Parallel()
	launch := func(t *testing.T, bundle string) *server {
		t.Helper()
		return &server{out: io.Discard, logw: io.Discard, serverVersion: reportServerVersionProbe,
			bundleVersion: bundle, launchRoot: t.TempDir()}
	}

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
	// stringOf decodes one key of the payload as a JSON string, reporting
	// whether the payload carries the key at all.
	stringOf := func(t *testing.T, keys map[string]json.RawMessage, key string) (string, bool) {
		t.Helper()
		raw, carried := keys[key]
		if !carried {
			return "", false
		}
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			t.Fatalf("the payload's %s is %s, not a JSON string: %v", key, raw, err)
		}
		return s, true
	}

	for _, tc := range []struct {
		name   string
		bundle string
		// differs is whether the line must say the two come from different
		// releases.
		differs bool
	}{
		{name: "a bundle of another release", bundle: reportBundleVersionProbe, differs: true},
		{name: "a bundle of the server's own release", bundle: reportServerVersionProbe},
		{name: "no bundle"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			index := callTool(t, launch(t, tc.bundle), describeCapabilitiesName, describeCapabilitiesArgs{})
			keys := keysOf(t, index)
			if got, _ := stringOf(t, keys, "server_version"); got != reportServerVersionProbe {
				t.Errorf("the argumentless index carries server_version %q; want %q", got, reportServerVersionProbe)
			}
			switch got, carried := stringOf(t, keys, "bundle_version"); {
			case tc.bundle == "" && carried:
				t.Errorf("a server no bundle launched carries bundle_version %q", got)
			case tc.bundle != "" && got != tc.bundle:
				t.Errorf("the argumentless index carries bundle_version %q; want %q", got, tc.bundle)
			}

			want := versionLine(reportServerVersionProbe, tc.bundle)
			summary, carried := stringOf(t, keys, "version_summary")
			if !carried || summary != want {
				t.Errorf("the argumentless index carries version_summary %q (carried: %v); want the version line %q",
					summary, carried, want)
			}
			if first, _, _ := strings.Cut(index.Content[0].Text, "\n"); first != summary {
				t.Errorf("the argumentless index's text opens with %q; want the line its version_summary carries, %q",
					first, summary)
			}
			if says := strings.Contains(summary, "come from different releases"); says != tc.differs {
				t.Errorf("version_summary %q says the two come from different releases: %v; want %v",
					summary, says, tc.differs)
			}
		})
	}

	launched := launch(t, reportBundleVersionProbe)
	for _, narrowed := range []struct {
		name string
		args describeCapabilitiesArgs
	}{
		{name: "narrowed to a kind", args: describeCapabilitiesArgs{Kind: "release"}},
		{name: "naming tools", args: describeCapabilitiesArgs{Tools: []string{describeCapabilitiesName}}},
	} {
		res := callTool(t, launched, describeCapabilitiesName, narrowed.args)
		keys := keysOf(t, res)
		for _, key := range []string{"server_version", "bundle_version", "version_summary"} {
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
