// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// driveDiscover drives a real server/discover request through the message
// loop and returns the result as it appears on the wire.
func driveDiscover(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	resps, _, _, err := drive(t,
		`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":`+perRequestParams("2026-07-28")+`}`,
	)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(resps) != 1 {
		t.Fatalf("want 1 response, got %d", len(resps))
	}
	return wireFields(t, resps[0])
}

func TestDiscoverAdvertisesTheRevisionsAndTheServerIdentity(t *testing.T) {
	fields := driveDiscover(t)

	supported, ok := fields["supportedVersions"]
	if !ok {
		t.Fatalf("discovery carries no supportedVersions: %v", fields)
	}
	if want := []string{"2026-07-28", "2025-06-18"}; !equalStrings(wireStrings(t, supported), want) {
		t.Errorf("supportedVersions = %s, want exactly %v", supported, want)
	}

	var resultType string
	if raw, ok := fields["resultType"]; !ok {
		t.Error("discovery carries no resultType")
	} else if err := json.Unmarshal(raw, &resultType); err != nil {
		t.Errorf("resultType is not a JSON string: %s: %v", raw, err)
	} else if resultType != "complete" {
		t.Errorf("resultType = %q, want %q", resultType, "complete")
	}

	// The identity travels under the revision's own reserved name, inside the
	// result's metadata, and carries the same two facts the handshake reports
	// in its serverInfo block.
	var meta map[string]json.RawMessage
	if raw, ok := fields["_meta"]; !ok {
		t.Error("discovery carries no _meta")
	} else if err := json.Unmarshal(raw, &meta); err != nil {
		t.Errorf("_meta is not a JSON object: %s: %v", raw, err)
	}
	identity, ok := meta[wireServerInfoKey]
	if !ok {
		t.Fatalf("_meta carries no %q: %v", wireServerInfoKey, meta)
	}
	var reported map[string]string
	if err := json.Unmarshal(identity, &reported); err != nil {
		t.Fatalf("the server identity is not an object of strings: %s: %v", identity, err)
	}
	if reported["name"] != serverName {
		t.Errorf("identity name = %q, want %q", reported["name"], serverName)
	}
	if reported["version"] != "test-version" {
		t.Errorf("identity version = %q, want the version the run was built with", reported["version"])
	}

	// Discovery is a result a client may cache, so it states for how long and
	// to whom. The window is content the binary carries, so it is public.
	var window int64
	if raw, ok := fields["ttlMs"]; !ok {
		t.Error("discovery carries no ttlMs")
	} else if err := json.Unmarshal(raw, &window); err != nil {
		t.Errorf("ttlMs is not a JSON number: %s: %v", raw, err)
	} else if window < 0 {
		t.Errorf("ttlMs = %d, want a window of at least zero", window)
	}
	var scope string
	if raw, ok := fields["cacheScope"]; !ok {
		t.Error("discovery carries no cacheScope")
	} else if err := json.Unmarshal(raw, &scope); err != nil {
		t.Errorf("cacheScope is not a JSON string: %s: %v", raw, err)
	} else if scope != "public" {
		t.Errorf("cacheScope = %q, want %q for content the binary carries", scope, "public")
	}

	// The capability set is the same one the handshake advertises, so a client
	// learns the same surface whichever revision it speaks.
	var capabilities map[string]json.RawMessage
	if raw, ok := fields["capabilities"]; !ok {
		t.Error("discovery carries no capabilities")
	} else if err := json.Unmarshal(raw, &capabilities); err != nil {
		t.Errorf("capabilities is not a JSON object: %s: %v", raw, err)
	}
	for _, group := range []string{"tools", "resources", "prompts"} {
		if _, ok := capabilities[group]; !ok {
			t.Errorf("capabilities do not advertise %q: %v", group, capabilities)
		}
	}
}

// TestDiscoverGuidanceMatchesTheHandshakeGuidanceByte compares the two
// revisions' connect-time guidance as it leaves the server rather than as it
// is declared, because what a client is told is the wire value. Both are read
// out of encoded results in one run and compared to each other; neither side
// is compared against the constant, so a change that moved only one of them
// fails here.
func TestDiscoverGuidanceMatchesTheHandshakeGuidanceByte(t *testing.T) {
	resps, _, _, err := drive(t,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"probe","version":"0.1"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"server/discover","params":`+perRequestParams("2026-07-28")+`}`,
	)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(resps) != 2 {
		t.Fatalf("want 2 responses, got %d", len(resps))
	}

	fromHandshake := wireGuidance(t, wireFields(t, responseFor(t, resps, 1)))
	fromDiscovery := wireGuidance(t, wireFields(t, responseFor(t, resps, 2)))

	if strings.TrimSpace(fromDiscovery) == "" {
		t.Fatal("discovery carries empty guidance")
	}
	if fromDiscovery != fromHandshake {
		t.Errorf("the two revisions hand a client different guidance:\ndiscovery: %d bytes\nhandshake: %d bytes",
			len(fromDiscovery), len(fromHandshake))
	}
}

// wireGuidance reads the connect-time guidance out of an encoded result.
func wireGuidance(t *testing.T, fields map[string]json.RawMessage) string {
	t.Helper()
	raw, ok := fields["instructions"]
	if !ok {
		t.Fatalf("result carries no instructions key on the wire: %v", fields)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("instructions is not a JSON string: %s: %v", raw, err)
	}
	return value
}
