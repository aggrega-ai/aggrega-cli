// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// The tool protocol reaches the graph by the ordinary derivation: the three
// commands that hand it back declare the flag, and the tool generated from each
// carries that flag as a property. There is no second graph tool, and that
// absence is the point — a hand-written one would be a second serialization of
// the document, which is what the capability exists to remove rather than to
// spread.
//
// This drives the real server over the real command tree, which is why it lives
// here: the package that owns the derivation cannot build a command tree, so a
// test there would assert over a synthesized surface carrying no flags at all.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/mcp"
	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
)

// serveOneToolProtocolRequest drives one request through the real server over
// the live command tree and returns the response's result payload.
func serveOneToolProtocolRequest(t *testing.T, method string, params any) json.RawMessage {
	t.Helper()
	withCLIRegistrations(t, nil)

	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": method, "params": params,
	})
	if err != nil {
		t.Fatalf("frame the request: %v", err)
	}
	var stdout bytes.Buffer
	if err := mcp.Run(context.Background(), mcp.Config{
		Stdin:    bytes.NewReader(append(body, '\n')),
		Stdout:   &stdout,
		Stderr:   io.Discard,
		Commands: commandSurface(newRootCmd()),
	}); err != nil {
		t.Fatalf("serve %s: %v", method, err)
	}

	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	line := strings.TrimSpace(stdout.String())
	if line == "" {
		t.Fatalf("%s produced no response", method)
	}
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("decode the %s response: %v\n%s", method, err, line)
	}
	if len(resp.Error) > 0 {
		t.Fatalf("%s answered with an error envelope: %s", method, resp.Error)
	}
	return resp.Result
}

// TestToolProtocolCarriesTheGraphOnEveryToolThatOffersIt is the tool protocol's
// half of the capability, proved by asking the server what it serves rather than
// by reading the derivation.
//
// The tool names come from the availability record rather than from literals
// here, so a renamed command moves this with it. The property name is derived
// from the flag for the same reason.
func TestToolProtocolCarriesTheGraphOnEveryToolThatOffersIt(t *testing.T) {
	// The two steps a client takes, because the argument schema this asserts over
	// is the one thing discovery deliberately withholds: an argumentless call
	// answers with an index of names and purposes, and naming those back returns
	// the definitions. Two sessions rather than two requests in one, because the
	// second request's arguments are read out of the first one's answer and this
	// transport is handed its whole input up front.
	indexed := serveOneToolProtocolRequest(t, "tools/call", map[string]any{
		"name": "describe-capabilities", "arguments": map[string]any{},
	})

	var index struct {
		StructuredContent struct {
			Index []struct {
				Name string `json:"name"`
			} `json:"index"`
		} `json:"structuredContent"`
	}
	if err := json.Unmarshal(indexed, &index); err != nil {
		t.Fatalf("decode the capability index: %v\n%s", err, indexed)
	}
	named := make([]string, 0, len(index.StructuredContent.Index))
	for _, e := range index.StructuredContent.Index {
		named = append(named, e.Name)
	}
	if len(named) == 0 {
		t.Fatal("the capability index named no tool, so the definitions behind it were never reached")
	}

	result := serveOneToolProtocolRequest(t, "tools/call", map[string]any{
		"name": "describe-capabilities", "arguments": map[string]any{"tools": named},
	})

	var call struct {
		StructuredContent struct {
			Capabilities []struct {
				Name        string          `json:"name"`
				InputSchema json.RawMessage `json:"inputSchema"`
			} `json:"capabilities"`
		} `json:"structuredContent"`
	}
	if err := json.Unmarshal(result, &call); err != nil {
		t.Fatalf("decode the capability listing: %v\n%s", err, result)
	}
	schemas := map[string]string{}
	for _, c := range call.StructuredContent.Capabilities {
		schemas[c.Name] = string(c.InputSchema)
	}
	if len(schemas) == 0 {
		t.Fatal("the server surfaced no capabilities, so nothing below observes the derivation")
	}

	availability := mcp.OperationAvailability()["ir-graph"]
	if !availability.IsExposed() {
		t.Fatal("the tool protocol declares no exposure for the graph capability, so this asserts nothing")
	}

	// The property a client sends is the flag's own words in snake_case, derived
	// rather than spelled, so a renamed flag fails here rather than passing on a
	// literal nobody updated.
	property := `"` + strings.ReplaceAll(flagIRGraph, "-", "_") + `"`

	for _, tool := range availability.Names {
		schema, served := schemas[tool]
		if !served {
			t.Errorf("the tool protocol claims the graph capability as %q, and the server serves no such tool", tool)
			continue
		}
		if !strings.Contains(schema, property) {
			t.Errorf("%s does not accept %s, so the capability it claims is unreachable through it:\n%s",
				tool, property, schema)
		}
	}

	// The bound no longer excuses the pair, which is what makes the availability
	// record above the thing carrying it rather than a row saying nobody decided.
	for _, q := range opsurface.UndecidedPairs() {
		if q.Surface == opsurface.SurfaceMCP && q.ID == "ir-graph" {
			t.Error("the tool protocol still names the graph capability as undecided, and it now serves it")
		}
	}
	for _, q := range opsurface.UndecidedPairs() {
		if q.Surface == opsurface.SurfaceCLI && q.ID == "ir-graph" {
			t.Error("the command line still names the graph capability as undecided, and it now carries it")
		}
	}
}
