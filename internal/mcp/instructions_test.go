// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// wireInstructions drives a real initialize request through the server's message
// loop and returns the instructions value as it appears on the wire, plus
// whether the key was present at all.
//
// It decodes the result into a generic object keyed by the literal wire name
// rather than into initializeResult. Unmarshalling into initializeResult would
// read the field back through the same struct tag that wrote it, so a mistagged
// field would round-trip cleanly and the assertion would pass while no client
// ever received the value. The literal key is an independent statement of the
// wire contract.
func wireInstructions(t *testing.T) (value string, present bool) {
	t.Helper()
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
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(resps[0].Result, &fields); err != nil {
		t.Fatalf("unmarshal initialize result: %v", err)
	}
	raw, ok := fields["instructions"]
	if !ok {
		return "", false
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("instructions is not a JSON string: %s: %v", raw, err)
	}
	return value, true
}

func TestInitializeCarriesInstructionsOnTheWire(t *testing.T) {
	value, present := wireInstructions(t)
	if !present {
		t.Fatal("initialize result carries no instructions key on the wire")
	}
	if strings.TrimSpace(value) == "" {
		t.Error("instructions is present but empty")
	}
	if value != serverInstructions {
		t.Error("the instructions on the wire are not the server's instructions text")
	}
}

// toolsNamedInInstructions is the set of tools the instructions text points a
// connecting assistant at. Each entry must both appear in the text and resolve
// in the tool registry.
//
// The two directions catch opposite drifts. Registry-side: renaming or removing
// a tool without updating the text would leave the instructions teaching a
// capability a client cannot call, which is worse than omitting it. Text-side: a
// name dropped from the prose while it stays in this table would leave the table
// asserting nothing, so the table cannot quietly rot into a list of tools the
// text no longer mentions.
var toolsNamedInInstructions = []string{
	"get-schema",
	"scaffold",
	"describe",
	"validate",
	"build",
	"explain-error",
	describeCapabilitiesName,
}

func TestInstructionsNameOnlyRealTools(t *testing.T) {
	for _, name := range toolsNamedInInstructions {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(serverInstructions, name) {
				t.Errorf("instructions do not mention tool %q", name)
			}
			if _, ok := lookupTool(name); !ok {
				t.Errorf("instructions point at tool %q, which no registry resolves", name)
			}
		})
	}
}

func TestInstructionsNameEveryReadableResourceScheme(t *testing.T) {
	for _, scheme := range []string{schemeSchema, schemeLangref, schemeAuthoring, schemeCatalog, schemeCli} {
		t.Run(scheme, func(t *testing.T) {
			if !strings.Contains(serverInstructions, scheme+"://") {
				t.Errorf("instructions do not point at the %s:// resource family", scheme)
			}
		})
	}
}
