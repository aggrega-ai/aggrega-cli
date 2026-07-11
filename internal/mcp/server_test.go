// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// drive runs the server against a newline-delimited request script and
// returns the parsed responses (one per stdout line), the raw stdout, the
// raw stderr, and the Run error. The bytes.Buffer stdin reaches EOF after
// the script, so Run returns on a clean EOF.
func drive(t *testing.T, requests ...string) (responses []rpcResponse, stdout, stderr string, runErr error) {
	t.Helper()
	in := strings.NewReader(strings.Join(requests, "\n") + "\n")
	var out, errBuf bytes.Buffer
	runErr = Run(Config{Stdin: in, Stdout: &out, Stderr: &errBuf, ServerVersion: "test-version"})
	stdout = out.String()
	stderr = errBuf.String()
	for _, line := range strings.Split(strings.TrimRight(stdout, "\n"), "\n") {
		if line == "" {
			continue
		}
		var resp rpcResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatalf("stdout line is not a valid JSON-RPC frame: %q: %v", line, err)
		}
		responses = append(responses, resp)
	}
	return responses, stdout, stderr, runErr
}

func TestInitializeHandshakeAdvertisesCapabilities(t *testing.T) {
	resps, _, _, err := drive(t,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"probe","version":"0.1"}}}`,
	)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(resps) != 1 {
		t.Fatalf("want 1 response, got %d", len(resps))
	}
	if resps[0].Error != nil {
		t.Fatalf("initialize returned error envelope: %+v", resps[0].Error)
	}
	var res initializeResult
	if err := json.Unmarshal(resps[0].Result, &res); err != nil {
		t.Fatalf("unmarshal initialize result: %v", err)
	}
	if res.ProtocolVersion != protocolVersion {
		t.Errorf("protocolVersion = %q, want %q", res.ProtocolVersion, protocolVersion)
	}
	if res.ServerInfo.Name != serverName {
		t.Errorf("serverInfo.name = %q, want %q", res.ServerInfo.Name, serverName)
	}
	if res.ServerInfo.Version != "test-version" {
		t.Errorf("serverInfo.version = %q, want %q", res.ServerInfo.Version, "test-version")
	}
	if res.Capabilities.Tools == nil {
		t.Error("tools capability not advertised")
	}
	if res.Capabilities.Resources == nil {
		t.Error("resources capability not advertised")
	}
	if res.Capabilities.Prompts == nil {
		t.Error("prompts capability not advertised")
	}
}

func TestUnsupportedProtocolVersionReportsServerRevision(t *testing.T) {
	resps, _, _, err := drive(t,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1999-01-01","capabilities":{},"clientInfo":{"name":"probe"}}}`,
	)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	var res initializeResult
	if err := json.Unmarshal(resps[0].Result, &res); err != nil {
		t.Fatalf("unmarshal initialize result: %v", err)
	}
	if res.ProtocolVersion != protocolVersion {
		t.Errorf("protocolVersion = %q, want server revision %q", res.ProtocolVersion, protocolVersion)
	}
}

func TestListMethodsReturnWellFormedResults(t *testing.T) {
	resps, _, _, err := drive(t,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"resources/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"resources/templates/list"}`,
		`{"jsonrpc":"2.0","id":4,"method":"prompts/list"}`,
	)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(resps) != 4 {
		t.Fatalf("want 4 responses, got %d", len(resps))
	}
	for _, r := range resps {
		if r.Error != nil {
			t.Fatalf("list method returned error: %+v", r.Error)
		}
	}
	// tools/list carries the curated set: every registered tool, with a name,
	// a non-empty description, and a well-formed JSON Schema for its arguments.
	var tl listToolsResult
	if err := json.Unmarshal(resps[0].Result, &tl); err != nil {
		t.Fatalf("unmarshal tools/list: %v", err)
	}
	if len(tl.Tools) != len(toolRegistry) {
		t.Fatalf("tools/list returned %d tools, want the curated %d", len(tl.Tools), len(toolRegistry))
	}
	for i, tool := range tl.Tools {
		if tool.Name != toolRegistry[i].name {
			t.Errorf("tool[%d] name = %q, want %q (registry order)", i, tool.Name, toolRegistry[i].name)
		}
		if tool.Description == "" {
			t.Errorf("tool %q has an empty description", tool.Name)
		}
		var schema map[string]any
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			t.Errorf("tool %q input schema is not valid JSON: %v", tool.Name, err)
		}
		if schema["type"] != "object" {
			t.Errorf("tool %q input schema type = %v, want object", tool.Name, schema["type"])
		}
	}
	if !bytes.Contains(resps[1].Result, []byte(`"resources":[]`)) {
		t.Errorf("resources/list result = %s, want empty array", resps[1].Result)
	}
	// resources/templates/list advertises the documentation resource families —
	// the langref:// and authoring:// URI templates — while resources/list stays
	// empty (those resources are templated, not fixed concrete entries).
	var rt listResourceTemplatesResult
	if err := json.Unmarshal(resps[2].Result, &rt); err != nil {
		t.Fatalf("unmarshal resources/templates/list: %v", err)
	}
	if len(rt.ResourceTemplates) != 2 {
		t.Fatalf("resources/templates/list returned %d templates, want 2: %s", len(rt.ResourceTemplates), resps[2].Result)
	}
	gotTemplates := map[string]bool{}
	for _, tmpl := range rt.ResourceTemplates {
		gotTemplates[tmpl.URITemplate] = true
		if tmpl.Name == "" || tmpl.Description == "" {
			t.Errorf("resource template %q missing name or description", tmpl.URITemplate)
		}
	}
	for _, want := range []string{"langref://{path}", "authoring://{topic}"} {
		if !gotTemplates[want] {
			t.Errorf("resources/templates/list missing template %q: %s", want, resps[2].Result)
		}
	}
	if !bytes.Contains(resps[3].Result, []byte(`"prompts":[]`)) {
		t.Errorf("prompts/list result = %s, want empty array", resps[3].Result)
	}
}

func TestDispatchStubsReturnWellFormedErrors(t *testing.T) {
	resps, _, _, err := drive(t,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"nope","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"resources/read","params":{"uri":"file:///nope"}}`,
		`{"jsonrpc":"2.0","id":3,"method":"does/not/exist"}`,
	)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(resps) != 3 {
		t.Fatalf("want 3 responses, got %d", len(resps))
	}
	if resps[0].Error == nil || resps[0].Error.Code != codeInvalidParams {
		t.Errorf("tools/call unknown-tool error = %+v, want code %d", resps[0].Error, codeInvalidParams)
	}
	if resps[1].Error == nil || resps[1].Error.Code != codeResourceNotFound {
		t.Errorf("resources/read unknown-uri error = %+v, want code %d", resps[1].Error, codeResourceNotFound)
	}
	if resps[2].Error == nil || resps[2].Error.Code != codeMethodNotFound {
		t.Errorf("unknown method error = %+v, want code %d", resps[2].Error, codeMethodNotFound)
	}
}

func TestStdoutCarriesOnlyFramedMessages(t *testing.T) {
	// A notification yields no response; every stdout line must still be a
	// valid JSON-RPC frame with jsonrpc "2.0" and a correlating id — no
	// stray writes, no log leakage.
	resps, stdout, stderr, err := drive(t,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","clientInfo":{"name":"probe"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	// initialize + tools/list produce responses; the notification does not.
	if len(resps) != 2 {
		t.Fatalf("want 2 responses (notification suppressed), got %d:\n%s", len(resps), stdout)
	}
	for _, r := range resps {
		if r.JSONRPC != "2.0" {
			t.Errorf("frame jsonrpc = %q, want 2.0", r.JSONRPC)
		}
		if len(r.ID) == 0 {
			t.Error("frame carries no id")
		}
	}
	// The initialize handshake logs the client identity; it must land on
	// stderr, never stdout.
	if !strings.Contains(stderr, "probe") {
		t.Errorf("client identity log not on stderr; stderr = %q", stderr)
	}
	if strings.Contains(stdout, "probe") {
		t.Errorf("log content leaked onto stdout: %q", stdout)
	}
}

func TestNotificationYieldsNoResponse(t *testing.T) {
	resps, stdout, _, err := drive(t,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
	)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(resps) != 0 {
		t.Fatalf("notification produced %d responses, want 0:\n%s", len(resps), stdout)
	}
}

func TestCleanExitOnStdinEOF(t *testing.T) {
	// Empty input: the reader reaches EOF immediately and Run returns nil.
	var out, errBuf bytes.Buffer
	if err := Run(Config{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errBuf}); err != nil {
		t.Fatalf("Run on empty stdin returned error: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("empty stdin produced stdout: %q", out.String())
	}
}

func TestFinalMessageWithoutTrailingNewline(t *testing.T) {
	// A client may close the stream right after the last message without a
	// trailing newline; that message must still be answered.
	var out, errBuf bytes.Buffer
	in := strings.NewReader(`{"jsonrpc":"2.0","id":7,"method":"ping"}`)
	if err := Run(Config{Stdin: in, Stdout: &out, Stderr: &errBuf}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	var resp rpcResponse
	line := strings.TrimSpace(out.String())
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("ping response not a valid frame: %q: %v", line, err)
	}
	if resp.Error != nil {
		t.Errorf("ping returned error: %+v", resp.Error)
	}
	if string(resp.ID) != "7" {
		t.Errorf("ping response id = %s, want 7", resp.ID)
	}
}

func TestParseErrorYieldsNullIDEnvelope(t *testing.T) {
	resps, _, _, err := drive(t, `{not valid json`)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(resps) != 1 {
		t.Fatalf("want 1 response, got %d", len(resps))
	}
	if resps[0].Error == nil || resps[0].Error.Code != codeParseError {
		t.Errorf("parse-error envelope = %+v, want code %d", resps[0].Error, codeParseError)
	}
	if len(resps[0].ID) != 0 && string(resps[0].ID) != "null" {
		t.Errorf("parse-error id = %s, want null/absent", resps[0].ID)
	}
}
