// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// Every request in this file is spelled out in the protocol's own literal wire
// names for the same reason revision_test.go gives: a request composed from the
// constants the server reads it through would agree with the server whatever
// those constants said.

// perRequestRequest builds one request in the per-request revision — the two
// metadata fields that revision requires, plus whatever arguments the method
// itself needs.
func perRequestRequest(id int, method, args string) string {
	params := `{"_meta":{"` + wireProtocolVersionKey + `":"2026-07-28","` +
		wireClientCapabilitiesKey + `":{}}`
	if args != "" {
		params += "," + args
	}
	params += `}`
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":%q,"params":%s}`, id, method, params)
}

// wireString reads one wire value as a string, reporting the method it belongs
// to so a failure names the result that lost it.
func wireString(t *testing.T, method string, fields map[string]json.RawMessage, key string) (string, bool) {
	t.Helper()
	raw, ok := fields[key]
	if !ok {
		return "", false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Errorf("%s: %s is not a JSON string: %s: %v", method, key, raw, err)
		return "", false
	}
	return value, true
}

// assertCarriesTheEnvelope checks the two things the per-request revision asks
// of every result it defines: what kind of result it is, and who produced it.
// Every message names the method, because a grid whose failure says only that
// a field went missing leaves the reader to find out from which result.
func assertCarriesTheEnvelope(t *testing.T, method string, fields map[string]json.RawMessage) {
	t.Helper()

	if resultType, ok := wireString(t, method, fields, "resultType"); !ok {
		t.Errorf("%s: the result carries no resultType", method)
	} else if resultType != "complete" {
		t.Errorf("%s: resultType = %q, want %q", method, resultType, "complete")
	}

	raw, ok := fields["_meta"]
	if !ok {
		t.Errorf("%s: the result carries no _meta", method)
		return
	}
	var meta map[string]json.RawMessage
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Errorf("%s: _meta is not a JSON object: %s: %v", method, raw, err)
		return
	}
	identity, ok := meta[wireServerInfoKey]
	if !ok {
		t.Errorf("%s: _meta carries no %q", method, wireServerInfoKey)
		return
	}
	var reported map[string]string
	if err := json.Unmarshal(identity, &reported); err != nil {
		t.Errorf("%s: the server identity is not an object of strings: %s: %v", method, identity, err)
		return
	}
	if reported["name"] != serverName {
		t.Errorf("%s: identity name = %q, want %q", method, reported["name"], serverName)
	}
	if reported["version"] != "test-version" {
		t.Errorf("%s: identity version = %q, want the version the run was built with", method, reported["version"])
	}
}

// assertCarriesAFreshnessHint checks the two fields a cacheable result adds.
// It reads the window as a raw number rather than through a pointer, so a
// window of zero is distinguishable from a window that was dropped.
func assertCarriesAFreshnessHint(t *testing.T, method string, fields map[string]json.RawMessage, wantScope string, wantWindow int64) {
	t.Helper()

	if raw, ok := fields["ttlMs"]; !ok {
		t.Errorf("%s: the result carries no ttlMs", method)
	} else {
		var window int64
		if err := json.Unmarshal(raw, &window); err != nil {
			t.Errorf("%s: ttlMs is not a JSON number: %s: %v", method, raw, err)
		} else if window < 0 {
			t.Errorf("%s: ttlMs = %d, want a window of at least zero", method, window)
		} else if window != wantWindow {
			t.Errorf("%s: ttlMs = %d, want %d", method, window, wantWindow)
		}
	}

	if scope, ok := wireString(t, method, fields, "cacheScope"); !ok {
		t.Errorf("%s: the result carries no cacheScope", method)
	} else if scope != wantScope {
		t.Errorf("%s: cacheScope = %q, want %q", method, scope, wantScope)
	}
}

// assertCarriesNoFreshnessHint is the other half of the same contract: a result
// the revision does not make cacheable states nothing about freshness, rather
// than stating a window nobody defined for it.
func assertCarriesNoFreshnessHint(t *testing.T, method string, fields map[string]json.RawMessage) {
	t.Helper()
	for _, key := range []string{"ttlMs", "cacheScope"} {
		if raw, present := fields[key]; present {
			t.Errorf("%s: the result carries %s = %s, but this revision does not make it cacheable",
				method, key, raw)
		}
	}
}

// TestEveryPerRequestResultCarriesTheEnvelope drives one request per method the
// per-request revision defines, through the message loop, and reads every field
// out of the encoded result rather than off a struct.
//
// The rows are the methods crossed with whether that revision makes the result
// cacheable — the two shapes it defines — so the pair that carries only the
// envelope is asserted to carry no freshness hint as much as the rest are
// asserted to carry one. Each row is a subtest named for its method, and every
// message inside it names the method too, so a result that loses a field says
// which result lost it.
//
// The revision serves ping and initialize under neither shape: it defines
// neither method. They are asserted where their absence belongs, in
// TestHandshakeResultsCarryNoPerRequestEnvelope.
func TestEveryPerRequestResultCarriesTheEnvelope(t *testing.T) {
	// The window is spelled as the number a client reads off the wire rather
	// than computed from the constant the server states it with: a value derived
	// from that constant would agree with the server whatever it said, which is
	// the same reason the wire names in this file are literal.
	const publicWindow = int64(3_600_000)

	for _, row := range []struct {
		method string
		// args is the method's own params, merged beside the metadata.
		args string
		// cacheable says whether this revision makes the result a cacheable one.
		cacheable bool
	}{
		{method: "server/discover", cacheable: true},
		{method: "tools/list", cacheable: true},
		{method: "resources/list", cacheable: true},
		{method: "resources/templates/list", cacheable: true},
		{method: "prompts/list", cacheable: true},
		{method: "resources/read", args: `"uri":"schema://component"`, cacheable: true},
		{method: "prompts/get", args: `"name":"developer-authoring"`},
		{method: "tools/call", args: `"name":"describe-capabilities","arguments":{}`},
	} {
		t.Run(row.method, func(t *testing.T) {
			resps, _, _, err := drive(t, perRequestRequest(1, row.method, row.args))
			if err != nil {
				t.Fatalf("%s: Run returned error: %v", row.method, err)
			}
			if len(resps) != 1 {
				t.Fatalf("%s: want 1 response, got %d", row.method, len(resps))
			}
			if resps[0].Error != nil {
				t.Fatalf("%s: returned an error envelope: %+v", row.method, resps[0].Error)
			}
			fields := wireFields(t, resps[0])

			assertCarriesTheEnvelope(t, row.method, fields)
			if row.cacheable {
				// Every method here but resources/read reads content the binary
				// carries; the row above asks resources/read for a schema, which
				// is the same.
				assertCarriesAFreshnessHint(t, row.method, fields, "public", publicWindow)
			} else {
				assertCarriesNoFreshnessHint(t, row.method, fields)
			}
		})
	}
}

// readFixtureServer builds a server whose workspace holds one catalog entity and
// whose documentation-bundle and help-rendering seams are both substituted, so a
// run over it can read all four resource families without reaching a network or a
// real catalog.
func readFixtureServer(t *testing.T) *server {
	t.Helper()
	s, ws := coldDocsWorkspace(t)
	body := "apiVersion: aggrega.ai/v1alpha1\n" +
		"kind: Component\n" +
		"metadata:\n" +
		"  name: core/compute/api-server\n" +
		"  version: \"1.0.0\"\n" +
		"  labels:\n" +
		"    component.aggrega.ai/type: \"server\"\n" +
		"spec:\n" +
		"  inject: [\"env\"]\n" +
		"  schema:\n" +
		"    infra:\n" +
		"      port: { type: integer, default: 8080 }\n" +
		"    provides:\n" +
		"      host: { type: string, required: true }\n"
	// The entity sits in the conventional catalog directory rather than loose at
	// the workspace root, which is the shape a reader is told to author: the
	// search stack reads that directory without being told to, and a root holding
	// entities directly is read only when the reader lists it themselves.
	catalogDir := filepath.Join(ws, workspaceconfig.ConventionalCatalogDir)
	if err := os.MkdirAll(catalogDir, 0o755); err != nil {
		t.Fatalf("mkdir the conventional catalog directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(catalogDir, "component.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}
	s.renderHelp = func(path []string) (string, bool) {
		return "USAGE: aggrega " + strings.Join(path, " "), true
	}
	return s
}

// TestReadFreshnessIsChosenByTheFamilyAndNotTheMethod is the heterogeneous case:
// one method, four families, and a hint that follows where the content came
// from rather than which method was called.
//
// Three families are content the process cannot see change while it lives — the
// schemas and the command tree compiled into the binary, and the documentation
// bundle pinned at the start of the run — so each is public and keeps for a long
// window. The catalog family is read out of the caller's own workspace, so it is
// private and stale on arrival. A single hint on this method would be wrong for
// one of those two groups whichever one it named.
func TestReadFreshnessIsChosenByTheFamilyAndNotTheMethod(t *testing.T) {
	// Literal for the reason given at the other table: a window computed from
	// the server's own constant cannot disagree with it.
	const publicWindow = int64(3_600_000)

	rows := []struct {
		uri        string
		wantScope  string
		wantWindow int64
	}{
		{uri: "schema://component", wantScope: "public", wantWindow: publicWindow},
		{uri: "langref://entities/component", wantScope: "public", wantWindow: publicWindow},
		{uri: "authoring://getting-started", wantScope: "public", wantWindow: publicWindow},
		{uri: "cli://plugin/gen-catalog", wantScope: "public", wantWindow: publicWindow},
		{uri: "catalog://component/core/compute/api-server", wantScope: "private", wantWindow: 0},
	}

	s := readFixtureServer(t)
	s.serverVersion = "test-version"
	requests := make([]string, 0, len(rows))
	for i, row := range rows {
		requests = append(requests, perRequestRequest(i+1, "resources/read", `"uri":`+jsonString(row.uri)))
	}
	resps := runServer(t, s, requests...)
	if len(resps) != len(rows) {
		t.Fatalf("want %d responses, got %d", len(rows), len(resps))
	}

	for i, row := range rows {
		// The row's request was sent under id i+1, which is how its answer is
		// found: the requests are answered concurrently, so where the answer
		// landed in the batch says nothing about which row it belongs to.
		resp := responseFor(t, resps, i+1)
		if resp.Error != nil {
			t.Fatalf("resources/read %s returned an error envelope: %+v", row.uri, resp.Error)
		}
		fields := wireFields(t, resp)
		assertCarriesTheEnvelope(t, "resources/read "+row.uri, fields)
		assertCarriesAFreshnessHint(t, "resources/read "+row.uri, fields, row.wantScope, row.wantWindow)
	}
}

// jsonString quotes a URI as a JSON string value. Every URI in this file is a
// plain token with nothing to escape, so quoting is the whole of it and the
// request text stays readable as the literal a client would send.
func jsonString(s string) string { return `"` + s + `"` }

// TestReadNotFoundCodeIsTheOneTheRequestsRevisionUses crosses the two revisions
// against the paths that can fail to resolve a URI: the split that finds no
// scheme, the branch that recognises none, and each of the four families' own
// misses. The per-request revision retired the code its predecessors used for
// this, so every one of those paths must answer the invalid-params code to a
// request speaking it.
//
// Only the per-request column is driven here. The handshake column is the
// control and it is already asserted, per family, by the existing not-found
// assertions in server_test.go, resource_more_test.go and resource_docs_test.go,
// which drive the same paths in that revision and pin the retired code — so a
// change that moved both columns together would still redden.
func TestReadNotFoundCodeIsTheOneTheRequestsRevisionUses(t *testing.T) {
	s := readFixtureServer(t)

	rows := []struct {
		name string
		uri  string
	}{
		{name: "no scheme at all", uri: "not-a-uri"},
		{name: "a scheme no family claims", uri: "file:///nope"},
		{name: "schema family, unknown kind", uri: "schema://does-not-exist"},
		{name: "documentation family, no page named", uri: "langref://"},
		{name: "catalog family, absent entity", uri: "catalog://component/core/compute/absent"},
		{name: "cli family, a segment that is not a command", uri: "cli://-rf"},
	}

	requests := make([]string, 0, len(rows))
	for i, row := range rows {
		requests = append(requests, perRequestRequest(i+1, "resources/read", `"uri":`+jsonString(row.uri)))
	}
	resps := runServer(t, s, requests...)
	if len(resps) != len(rows) {
		t.Fatalf("want %d responses, got %d", len(rows), len(resps))
	}

	for i, row := range rows {
		// The row's request was sent under id i+1, which is how its answer is
		// found: the requests are answered concurrently, so where the answer
		// landed in the batch says nothing about which row it belongs to.
		resp := responseFor(t, resps, i+1)
		if resp.Error == nil {
			t.Errorf("%s: %s was answered rather than refused: %s", row.name, row.uri, resp.Result)
			continue
		}
		if got := resp.Error.Code; got != codeInvalidParams {
			t.Errorf("%s: %s refused with %d, want %d in this revision",
				row.name, row.uri, got, codeInvalidParams)
		}
		if !strings.Contains(resp.Error.Message, row.uri) {
			t.Errorf("%s: the refusal does not name the URI it could not resolve: %q",
				row.name, resp.Error.Message)
		}
	}
}

// TestPerRequestToolsListIsByteStableAcrossRuns proves the decoration did not
// cost the tools listing its stability: two runs of one session shape produce
// the same bytes, envelope and freshness hint included. A hint computed from a
// clock, or an identity that varied per run, would fail here.
//
// The handshake revision's listing is pinned by
// TestCommandToolDiscoveryIsByteStable; this is the other revision's, which
// carries strictly more and therefore has strictly more that could move.
func TestPerRequestToolsListIsByteStableAcrossRuns(t *testing.T) {
	render := func() string {
		resps, _, _, err := drive(t, perRequestRequest(1, "tools/list", ""))
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
		if len(resps) != 1 || resps[0].Error != nil {
			t.Fatalf("tools/list did not answer cleanly: %+v", resps)
		}
		return string(resps[0].Result)
	}
	first := render()
	if second := render(); second != first {
		t.Errorf("tools/list is not byte-stable across runs:\nfirst:  %s\nsecond: %s", first, second)
	}
}
