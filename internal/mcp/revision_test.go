// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// Every request and every wire name in this file is spelled out literally
// rather than composed from the constants the server reads them through. A
// request built from those constants would agree with the server whatever
// they said, so the literal text is the independent statement of the
// contract — the same reason the instructions assertion decodes by literal
// key rather than through the struct that wrote it.
const (
	wireProtocolVersionKey    = "io.modelcontextprotocol/protocolVersion"
	wireClientCapabilitiesKey = "io.modelcontextprotocol/clientCapabilities"
	wireServerInfoKey         = "io.modelcontextprotocol/serverInfo"
)

// perRequestParams builds the params of a request speaking the per-request
// revision at the named version.
func perRequestParams(version string) string {
	return `{"_meta":{"` + wireProtocolVersionKey + `":"` + version + `","` +
		wireClientCapabilitiesKey + `":{}}}`
}

// wireFields decodes one response's result into its literal wire names, so an
// assertion reads what a client receives rather than what a Go struct would
// accept back.
func wireFields(t *testing.T, resp rpcResponse) map[string]json.RawMessage {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("request returned an error envelope: %+v", resp.Error)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(resp.Result, &fields); err != nil {
		t.Fatalf("result is not a JSON object: %s: %v", resp.Result, err)
	}
	return fields
}

// wireStrings decodes a raw wire value as a list of strings.
func wireStrings(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("value is not a JSON string array: %s: %v", raw, err)
	}
	return out
}

func TestRequestNamingAnUnspokenRevisionIsRefused(t *testing.T) {
	resps, _, _, err := drive(t,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":`+perRequestParams("2025-11-25")+`}`,
	)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(resps) != 1 {
		t.Fatalf("want 1 response, got %d", len(resps))
	}
	if resps[0].Error == nil {
		t.Fatalf("a revision the server does not speak was answered, not refused: %s", resps[0].Result)
	}
	if resps[0].Error.Code != -32022 {
		t.Errorf("refusal code = %d, want -32022", resps[0].Error.Code)
	}
	var data struct {
		Supported []string `json:"supported"`
		Requested string   `json:"requested"`
	}
	if err := json.Unmarshal(resps[0].Error.Data, &data); err != nil {
		t.Fatalf("refusal data is not the shape the revision fixes: %s: %v", resps[0].Error.Data, err)
	}
	if want := []string{"2026-07-28", "2025-06-18"}; !equalStrings(data.Supported, want) {
		t.Errorf("data.supported = %v, want %v", data.Supported, want)
	}
	if data.Requested != "2025-11-25" {
		t.Errorf("data.requested = %q, want the revision the request named", data.Requested)
	}
}

// TestPerRequestMetadataGrid crosses the two required fields against each
// other. The two single-field rows are the refusals the revision requires;
// the row carrying both is the control that keeps them from passing on a
// server that refuses everything, and the row carrying neither is the control
// that keeps them from passing on a server that has stopped telling the two
// revisions apart.
func TestPerRequestMetadataGrid(t *testing.T) {
	both := perRequestParams("2026-07-28")
	versionOnly := `{"_meta":{"` + wireProtocolVersionKey + `":"2026-07-28"}}`
	capabilitiesOnly := `{"_meta":{"` + wireClientCapabilitiesKey + `":{}}}`

	for _, row := range []struct {
		name string
		// params is the request's params, empty for a request that carries none.
		params string
		// wantCode is the refusal code, zero when the row must be answered.
		wantCode int
		// wantNames is the wire name the refusal must name.
		wantNames string
	}{
		{name: "both fields present", params: both},
		{name: "no metadata at all is the handshake revision"},
		{
			name:      "protocol version missing",
			params:    capabilitiesOnly,
			wantCode:  -32602,
			wantNames: wireProtocolVersionKey,
		},
		{
			name:      "client capabilities missing",
			params:    versionOnly,
			wantCode:  -32602,
			wantNames: wireClientCapabilitiesKey,
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			request := `{"jsonrpc":"2.0","id":1,"method":"tools/list"`
			if row.params != "" {
				request += `,"params":` + row.params
			}
			request += `}`

			resps, _, _, err := drive(t, request)
			if err != nil {
				t.Fatalf("Run returned error: %v", err)
			}
			if len(resps) != 1 {
				t.Fatalf("want 1 response, got %d", len(resps))
			}
			if row.wantCode == 0 {
				if resps[0].Error != nil {
					t.Fatalf("a well-formed request was refused: %+v", resps[0].Error)
				}
				return
			}
			if resps[0].Error == nil {
				t.Fatalf("a request missing %s was answered rather than refused", row.wantNames)
			}
			if resps[0].Error.Code != row.wantCode {
				t.Errorf("refusal code = %d, want %d", resps[0].Error.Code, row.wantCode)
			}
			if !strings.Contains(resps[0].Error.Message, row.wantNames) {
				t.Errorf("refusal message %q does not name the missing field %q",
					resps[0].Error.Message, row.wantNames)
			}
		})
	}
}

// TestHandshakeResultsCarryNoPerRequestEnvelope pins the half of the contract
// that is an absence: a request in the handshake revision is answered exactly
// as it was before the other revision existed, so neither field the
// per-request revision adds may appear on its results.
func TestHandshakeResultsCarryNoPerRequestEnvelope(t *testing.T) {
	resps, _, _, err := drive(t,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"probe","version":"0.1"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"ping"}`,
	)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(resps) != 3 {
		t.Fatalf("want 3 responses, got %d", len(resps))
	}
	for i, resp := range resps {
		fields := wireFields(t, resp)
		for _, absent := range []string{"resultType", "_meta"} {
			if raw, present := fields[absent]; present {
				t.Errorf("response %d carries %q = %s on a handshake-revision result", i+1, absent, raw)
			}
		}
	}
}

// TestDiscoverIsNotOfferedToTheHandshakeRevision pins the other side of the
// same line: server/discover belongs to the per-request revision, so a request
// that did not claim that revision is told the server does not serve the
// method — the same answer, from the same place, as a method nobody has ever
// defined.
func TestDiscoverIsNotOfferedToTheHandshakeRevision(t *testing.T) {
	resps, _, _, err := drive(t,
		`{"jsonrpc":"2.0","id":1,"method":"server/discover"}`,
	)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(resps) != 1 {
		t.Fatalf("want 1 response, got %d", len(resps))
	}
	if resps[0].Error == nil {
		t.Fatal("discovery was served to a request that did not claim the revision it belongs to")
	}
	if resps[0].Error.Code != codeMethodNotFound {
		t.Errorf("code = %d, want %d", resps[0].Error.Code, codeMethodNotFound)
	}
	if want := "method not found: server/discover"; resps[0].Error.Message != want {
		t.Errorf("message = %q, want %q", resps[0].Error.Message, want)
	}
}
