// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
)

// What a caller reaching a listener is listed, held byte for byte.
//
// A generated command tool takes the class its command reaches rather than the
// class of the operation it carries, and a listener is composed with no such
// tool at all. So nothing a caller over HTTP reads may move when a command's
// class does, and that is a claim about bytes rather than about which names are
// listed: a listing that kept every name and changed one description, one
// schema or one index line would satisfy a name check and still have moved.
// These goldens are what a listener answers, recorded from a running server, and
// each listing below is compared with them whole.
//
// One value in every answer is masked, and only one: the server's own identity
// stamp in the result's metadata, which carries the version of the binary that
// answered. Two builds always differ there and nothing about a listing is said
// by it, so both sides are brought to one spelling of it before they are
// compared — and the mask is asserted to have fired exactly once on each side,
// so it can neither hide a second difference nor quietly match nothing.
//
// A legitimate change to anything a listener lists regenerates these goldens in
// the same change, and the golden diff under review is the gate: regenerate with
// AGGREGA_UPDATE_GOLDEN=1.

// listenerListing is one listing a caller over HTTP reads: tools/list, or the
// capability tool called with one set of arguments.
type listenerListing struct {
	// name is the listing's golden file stem.
	name string

	// arguments is what the capability tool is sent; nil asks tools/list.
	arguments map[string]any
}

// listenerListings is the four listings a caller arriving over HTTP reads: the
// tool list, the capability index unscoped and narrowed to a kind, and the
// definitions of the build and the pull — the tool whose class a command
// declaring its own would be the one to disturb, and the tool a workspace
// withdrawal takes out.
func listenerListings() []listenerListing {
	return []listenerListing{
		{name: "tools-list"},
		{name: "index", arguments: map[string]any{}},
		{name: "index-kind-release", arguments: map[string]any{"kind": "release"}},
		{name: "definitions-build-pull", arguments: map[string]any{"tools": []string{"build", "pull"}}},
	}
}

// read answers the listing to this credential and returns the body as it came
// off the wire.
func (l listenerListing) read(t *testing.T, f *scopeGridFixture, credential string) []byte {
	t.Helper()
	var got answer
	if l.arguments == nil {
		headers := modernHeaders("tools/list", "")
		headers[headerAuthorization] = credential
		got = f.send(t, http.MethodPost, endpointPath, headers, modernRequest(t, 1, "tools/list", nil))
	} else {
		got = f.callTool(t, credential, describeCapabilitiesName, l.arguments)
	}
	if got.status != http.StatusOK {
		t.Fatalf("%s was answered %d (body %q)", l.name, got.status, got.raw)
	}
	return []byte(got.raw)
}

// serverIdentityStamp is the identity every result carries in its metadata: the
// server's name, and the version of the binary that answered where it has one.
var serverIdentityStamp = regexp.MustCompile(
	`"io\.modelcontextprotocol/serverInfo":\{"name":"aggrega"(?:,"version":"[^"]*")?\}`)

// withoutServerVersion brings the identity stamp to one spelling, after
// asserting the body carries it exactly once.
func withoutServerVersion(t *testing.T, body []byte, what string) []byte {
	t.Helper()
	if n := len(serverIdentityStamp.FindAllIndex(body, -1)); n != 1 {
		t.Fatalf("%s carries the server identity stamp %d time(s), want exactly 1: the mask would then hide "+
			"more than the stamp or nothing at all", what, n)
	}
	return serverIdentityStamp.ReplaceAll(body, []byte(`"io.modelcontextprotocol/serverInfo":{"name":"aggrega"}`))
}

// firstDifference renders where two bodies part, with the surrounding bytes of
// each, so a failure names the moved text rather than two lengths.
func firstDifference(want, got []byte) string {
	at := 0
	for at < len(want) && at < len(got) && want[at] == got[at] {
		at++
	}
	window := func(b []byte) string {
		from, to := max(at-120, 0), min(at+120, len(b))
		return string(b[from:to])
	}
	return "at byte " + strconv.Itoa(at) + "\n  golden: …" + window(want) + "…\n  served: …" + window(got) + "…"
}

// TestAListenerListsTheSameBytesWhateverACommandDeclares compares the four
// listings a caller over HTTP reads with their goldens — under a credential
// carrying every published scope and under one carrying the read scope alone, on
// a listener carrying every kind of reach a scope admits and on one whose
// mcp.deny_reach names workspace — and holds the one admission a workspace-wide
// build class would disturb: a build that asks for no dependency fill, called
// with the read scope alone, still runs.
//
// The two credentials are compared with one golden per listener and listing,
// because a listing reads what this server carries and never what a credential
// holds: the goldens were recorded under both, and both answered the same bytes.
//
// 🔴 PLANTED DEFECT: raise the build operation's own class in the operation set
// (internal/opsurface/opsurface.go, the "build" row, ReachNone → ReachWorkspace).
// The listener that withdraws workspace reach then lists no build — its tool
// list, both indexes and the definitions differ from their goldens — and the
// admission row is refused for want of the workspace-write scope. That is what
// shows the comparison and the row can fail: the class a command declares for
// itself never reaches them, while the operation's class does.
func TestAListenerListsTheSameBytesWhateverACommandDeclares(t *testing.T) {
	update := os.Getenv("AGGREGA_UPDATE_GOLDEN") != ""

	for _, listener := range []struct {
		name string
		deny []string
	}{
		{name: "carried"},
		{name: "deny-workspace", deny: []string{string(opsurface.ReachWorkspace)}},
	} {
		t.Run(listener.name, func(t *testing.T) {
			f := newScopeGridFixtureDenying(t, false, listener.deny)

			for _, credential := range []struct {
				name   string
				scopes []string
				// writesGolden is the one credential whose answer regenerates a
				// golden; the other is always compared with it, so a listing that
				// came to depend on the credential fails even while regenerating.
				writesGolden bool
			}{
				{name: "every published scope", scopes: everyPublishedScope, writesGolden: true},
				{name: "the read scope alone", scopes: []string{scopeRead}},
			} {
				bearerToken := f.grantOf(t, credential.scopes...)
				for _, listing := range listenerListings() {
					t.Run(credential.name+"/"+listing.name, func(t *testing.T) {
						golden := filepath.Join("testdata", "listener-listings", listener.name+"--"+listing.name+".json")
						served := listing.read(t, f, bearerToken)

						if update && credential.writesGolden {
							if err := os.WriteFile(golden, served, 0o644); err != nil {
								t.Fatalf("write golden %s: %v", golden, err)
							}
							t.Logf("updated golden %s (%d bytes)", golden, len(served))
						}
						recorded, err := os.ReadFile(golden)
						if err != nil {
							t.Fatalf("read golden %s (regenerate with AGGREGA_UPDATE_GOLDEN=1): %v", golden, err)
						}

						want := withoutServerVersion(t, recorded, "the golden "+golden)
						got := withoutServerVersion(t, served, "the "+listing.name+" answer")
						if !bytes.Equal(want, got) {
							t.Errorf("%s over a listener moved from what it answered before, %s. A legitimate change "+
								"regenerates the golden in the same change, and the golden diff in review is the gate:\n%s",
								listing.name, golden, firstDifference(want, got))
						}
					})
				}
			}

			if listener.deny != nil {
				return
			}
			t.Run("a build asking for no fill, called with the read scope alone", func(t *testing.T) {
				got := f.callTool(t, f.grantOf(t, scopeRead), "build", map[string]any{"release": "release.yaml"})
				wantTheCallRan(t, "build", got)
			})
		})
	}
}
