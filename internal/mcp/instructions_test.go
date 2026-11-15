// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// instructionsOnTheWire reads the guidance out of an encoded result under the
// literal protocol key, reporting whether the key was there at all.
//
// It decodes into a generic object keyed by that literal name rather than into
// the result type. Unmarshalling into the result type would read the field back
// through the same struct tag that wrote it, so a mistagged field would
// round-trip cleanly and every assertion below would pass while no client ever
// received the value. The literal key is an independent statement of the wire
// contract.
func instructionsOnTheWire(t *testing.T, result json.RawMessage) (value string, present bool) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(result, &fields); err != nil {
		t.Fatalf("unmarshal result: %v", err)
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

// wireInstructions drives a real initialize request through the server's message
// loop and returns the instructions value as it appears on the wire, plus
// whether the key was present at all.
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
	return instructionsOnTheWire(t, resps[0].Result)
}

func TestInitializeCarriesInstructionsOnTheWire(t *testing.T) {
	value, present := wireInstructions(t)
	if !present {
		t.Fatal("initialize result carries no instructions key on the wire")
	}
	if strings.TrimSpace(value) == "" {
		t.Error("instructions is present but empty")
	}
	if value != instructionsFor(transportStdio) {
		t.Error("the instructions on the wire are not the guidance this transport composes")
	}
}

// TestBothRevisionsServeOneTransportTheSameGuidance is the property the two
// revisions have always had, asserted now that the guidance is composed rather
// than read from one constant.
//
// It compares the two revisions' answers AGAINST EACH OTHER rather than either
// against a value the package holds. That is the whole point: while both routes
// read one constant the property held for free and an assertion against the
// constant said nothing about the two agreeing. Composition removes the free
// half — a change routing one route through the composer and leaving the other
// reading something else would leave the handshake and the per-request revision
// answering differently about one server, and only a comparison between them
// would see it.
//
// Both transports are driven, because the composer gives each its own text and a
// defect in one is invisible from the other.
func TestBothRevisionsServeOneTransportTheSameGuidance(t *testing.T) {
	t.Run("standard input and output", func(t *testing.T) {
		resps, _, _, err := drive(t,
			`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"`+
				handshakeRevision+`","capabilities":{},"clientInfo":{"name":"probe","version":"0.1"}}}`,
			`{"jsonrpc":"2.0","id":2,"method":"server/discover","params":`+
				perRequestParams(perRequestRevision)+`}`,
		)
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
		handshake := responseFor(t, resps, 1)
		perRequest := responseFor(t, resps, 2)
		if handshake.Error != nil {
			t.Fatalf("initialize returned error envelope: %+v", handshake.Error)
		}
		if perRequest.Error != nil {
			t.Fatalf("server/discover returned error envelope: %+v", perRequest.Error)
		}
		compareRevisions(t, handshake.Result, perRequest.Result)
	})

	t.Run("HTTP", func(t *testing.T) {
		f := newHTTPFixture(t, defaultTransportConfig(), nil)

		handshake := f.post(t, nil,
			`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"`+
				handshakeRevision+`","capabilities":{},"clientInfo":{"name":"probe"}}}`)
		if handshake.status != http.StatusOK {
			t.Fatalf("initialize status = %d, want 200 (body %q)", handshake.status, handshake.raw)
		}
		perRequest := f.post(t, modernHeaders("server/discover", ""),
			modernRequest(t, 2, "server/discover", nil))
		if perRequest.status != http.StatusOK {
			t.Fatalf("server/discover status = %d, want 200 (body %q)", perRequest.status, perRequest.raw)
		}
		if handshake.frame.Error != nil {
			t.Fatalf("initialize returned error envelope: %+v", handshake.frame.Error)
		}
		if perRequest.frame.Error != nil {
			t.Fatalf("server/discover returned error envelope: %+v", perRequest.frame.Error)
		}
		compareRevisions(t, handshake.frame.Result, perRequest.frame.Result)
	})
}

// compareRevisions holds one revision's guidance against the other's, byte for
// byte.
//
// The presence and non-emptiness checks are the control the comparison needs: a
// server that omitted the field on both routes, or carried an empty string on
// both, would satisfy a bare equality perfectly.
func compareRevisions(t *testing.T, handshake, perRequest json.RawMessage) {
	t.Helper()
	fromHandshake, ok := instructionsOnTheWire(t, handshake)
	if !ok {
		t.Fatal("the handshake revision's result carries no instructions key on the wire")
	}
	fromPerRequest, ok := instructionsOnTheWire(t, perRequest)
	if !ok {
		t.Fatal("the per-request revision's result carries no instructions key on the wire")
	}
	if strings.TrimSpace(fromHandshake) == "" || strings.TrimSpace(fromPerRequest) == "" {
		t.Fatal("one of the two revisions served empty guidance, so an equality between them " +
			"would hold over nothing")
	}
	if fromHandshake != fromPerRequest {
		t.Errorf("the two revisions serve different guidance for one server:\n"+
			"handshake (%d bytes):\n%s\nper-request (%d bytes):\n%s",
			len(fromHandshake), fromHandshake, len(fromPerRequest), fromPerRequest)
	}
}

// TestTheGuidanceDiffersByTransport is the other half of the comparison above.
//
// Without it the equality assertions would be satisfied by a composer that
// collapsed back into one text for both transports — which is precisely the
// change this surface must not silently absorb, because a caller reaching a
// listener would then be told it may repair a workspace it cannot reach.
func TestTheGuidanceDiffersByTransport(t *testing.T) {
	overStdio := instructionsFor(transportStdio)
	overHTTP := instructionsFor(transportHTTP)
	if overStdio == overHTTP {
		t.Fatal("both transports compose the same guidance, so a caller reaching a listener is " +
			"told the same thing about an empty workspace as one at this machine's keyboard")
	}
	if !strings.HasPrefix(overStdio, instructionsBody) || !strings.HasPrefix(overHTTP, instructionsBody) {
		t.Error("a transport's guidance does not open with the body both share, so the two " +
			"differ in more than the paragraph that is meant to differ")
	}
}

// TestTheGuidanceSaysWhereLocalEntitiesAreRead pins the half of the empty-workspace
// paragraph that tells an assistant what an empty listing means it must DO, rather
// than only that the listing was empty: the directory an entity is read from with
// no configuration at all, and the settings key that names any other one.
//
// The oracle is the resolver's own exported spellings, never a second copy of the
// words held here. That is what makes the check able to disagree with this
// package: a rename at the resolver moves this assertion with it and fails any
// guidance still naming the old one, which is exactly the drift a literal here
// could not see.
//
// 🔴 THE DIRECTORY IS ASSERTED INSIDE A PHRASE, AND A BARE SUBSTRING CHECK FOR IT
// WOULD BE VACUOUS. Its spelling is an ordinary word the shared body already uses
// several times over, so `Contains(guidance, ConventionalCatalogDir)` passes on
// every build whether or not the closing paragraph names a directory at all. The
// settings key needs no such treatment: it appears nowhere else in the guidance.
//
// Both transports are asserted, because the two are composed separately and a
// caller reaching a listener needs the directory named as much as one at the
// keyboard does — it is what makes the report it is told to file actionable.
func TestTheGuidanceSaysWhereLocalEntitiesAreRead(t *testing.T) {
	wanted := []string{
		workspaceconfig.ConventionalCatalogDir + " directory at the workspace root",
		workspaceconfig.CatalogDirsSetting,
	}
	for _, transport := range []transportMode{transportStdio, transportHTTP} {
		guidance := instructionsFor(transport)
		for _, want := range wanted {
			t.Run(transportName(transport)+"/"+want, func(t *testing.T) {
				if !strings.Contains(guidance, want) {
					t.Errorf("the guidance served over %s does not say %q, so an assistant is told "+
						"the workspace is empty without being told where an entity has to sit to be "+
						"read", transportName(transport), want)
				}
			})
		}
	}
}

// toolsNamedInInstructions is the set of statically declared tools the shared
// body points a connecting assistant at. Each entry must both appear in the text
// and resolve in the tool registry.
//
// The two directions catch opposite drifts. Registry-side: renaming or removing
// a tool without updating the text would leave the instructions teaching a
// capability a client cannot call, which is worse than omitting it. Text-side: a
// name dropped from the prose while it stays in this table would leave the table
// asserting nothing, so the table cannot quietly rot into a list of tools the
// text no longer mentions.
var toolsNamedInInstructions = []string{
	"get-schema",
	"draft",
	"describe",
	"validate",
	"build",
	"explain-error",
	describeCapabilitiesName,
}

func TestInstructionsNameOnlyRealTools(t *testing.T) {
	for _, name := range toolsNamedInInstructions {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(instructionsBody, name) {
				t.Errorf("instructions do not mention tool %q", name)
			}
			if _, ok := lookupTool(name); !ok {
				t.Errorf("instructions point at tool %q, which no registry resolves", name)
			}
		})
	}
}

// staticToolsNamedPerTransport is what the closing paragraph adds, per
// transport, restricted to the tools this package declares statically. A tool
// named here must appear in that transport's guidance and resolve in a registry.
var staticToolsNamedPerTransport = map[transportMode][]string{
	transportStdio: {"list-entities", describeCapabilitiesName},
	transportHTTP:  {"list-entities"},
}

func TestTheClosingParagraphNamesOnlyRealTools(t *testing.T) {
	for transport, names := range staticToolsNamedPerTransport {
		guidance := instructionsFor(transport)
		for _, name := range names {
			t.Run(transportName(transport)+"/"+name, func(t *testing.T) {
				if !strings.Contains(guidance, name) {
					t.Errorf("the guidance served over %s does not mention tool %q",
						transportName(transport), name)
				}
				if _, ok := lookupTool(name); !ok {
					t.Errorf("the guidance points at tool %q, which no registry resolves", name)
				}
			})
		}
	}
}

// TestTheListenerGuidanceNamesNoToolThatRunsACommand is the anti-decoration
// assertion, written as a class property rather than as a list of names.
//
// A session reached over HTTP is composed with no command tools at all, so every
// generated tool is absent from that caller's listing and refused if called. A
// name from that family in the listener's guidance would therefore be a
// capability the text sends a caller after and the server cannot serve, which is
// worse than saying nothing — and a rule written over the prefix keeps holding
// for the next command that grows a tool, which a list of names does not.
//
// The standard-streams row is the control. The same scan over the transport that
// does carry those tools must find at least one, so a build whose closing
// paragraph named none anywhere would fail here rather than reporting the
// listener clean.
func TestTheListenerGuidanceNamesNoToolThatRunsACommand(t *testing.T) {
	overHTTP := namedCommandTools(instructionsFor(transportHTTP))
	if len(overHTTP) > 0 {
		t.Errorf("the guidance served over HTTP names %v, which run an aggrega command in a "+
			"separate process and are offered to no caller reaching a listener", overHTTP)
	}
	overStdio := namedCommandTools(instructionsFor(transportStdio))
	if len(overStdio) == 0 {
		t.Fatal("the same scan over the guidance served to a caller at this machine's keyboard " +
			"found none either, so the assertion above would pass against a build whose guidance " +
			"names no such tool on any transport")
	}
	t.Logf("the guidance served over standard input and output names %v", overStdio)
}

// namedCommandTools reports every generated-command-tool name the given text
// mentions, read off the prefix that family carries.
func namedCommandTools(text string) []string {
	var found []string
	for _, field := range strings.FieldsFunc(text, func(r rune) bool {
		return r != '-' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9')
	}) {
		if strings.HasPrefix(field, commandToolPrefix) && !containsStr(found, field) {
			found = append(found, field)
		}
	}
	return found
}

func TestInstructionsNameEveryReadableResourceScheme(t *testing.T) {
	for _, scheme := range []string{schemeSchema, schemeLangref, schemeAuthoring, schemeCatalog, schemeCli} {
		t.Run(scheme, func(t *testing.T) {
			if !strings.Contains(instructionsBody, scheme+"://") {
				t.Errorf("instructions do not point at the %s:// resource family", scheme)
			}
		})
	}
}
