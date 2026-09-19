// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"testing"
)

// Which arguments a call may send is a property of how the caller reached the
// server, and the argument schema is what a caller reads before it composes
// anything. So the two surfaces that advertise a schema are walked on both
// transports, and every schema they carry is walked rather than a chosen few: a
// tool whose schema still offered an argument the transport refuses would send
// its caller to compose a call that cannot succeed, and one tool is enough for
// that.
//
// The walk is structural. It reports a name declared as a property or demanded
// as required, at any depth, so a schema that buried one where the projection
// does not reach is named here rather than shipped.

// advertisement is one tool as one surface advertised it.
type advertisement struct {
	// surface names where the definition was read, so a failure says which
	// listing carries the defect rather than only which tool.
	surface string
	tool    string
	schema  json.RawMessage
	// description is read alongside the schema because the two travel in one
	// answer: a caller reads both before it composes anything, so a sentence that
	// disagrees with the schema beside it is only visible where both are held
	// together.
	description string
}

// advertisedOverHTTP reads every argument schema a caller reaching the listener
// is offered: the default listing, and the unscoped long tail behind the
// capability tool. Both are driven as a client drives them.
func advertisedOverHTTP(t *testing.T, f *httpFixture) []advertisement {
	t.Helper()

	var out []advertisement

	listed := f.post(t, modernHeaders("tools/list", ""), modernRequest(t, 1, "tools/list", nil))
	if listed.frame.Error != nil {
		t.Fatalf("tools/list was answered with an error envelope: %s", listed.frame.Error.Message)
	}
	var listing listToolsResult
	if err := json.Unmarshal(listed.frame.Result, &listing); err != nil {
		t.Fatalf("the tools/list result is not a listing: %v", err)
	}
	for _, entry := range listing.Tools {
		out = append(out, advertisement{
			surface: "tools/list", tool: entry.Name,
			schema: entry.InputSchema, description: entry.Description,
		})
	}

	// The capability tool answers discovery with an index carrying no schemas, so
	// the schemas are read where a caller reads them: by naming every tool the
	// index reported and taking back the definitions.
	indexed := f.post(t, modernHeaders("tools/call", describeCapabilitiesName),
		modernRequest(t, 2, "tools/call", map[string]any{
			"name": describeCapabilitiesName, "arguments": map[string]any{},
		}))
	if indexed.frame.Error != nil {
		t.Fatalf("the capability tool was answered with an error envelope: %s", indexed.frame.Error.Message)
	}
	named := indexedNamesFrom(t, indexed.frame.Result)
	if len(named) == 0 {
		t.Fatal("the capability index was empty, so the schemas behind it were never reached")
	}
	defined := f.post(t, modernHeaders("tools/call", describeCapabilitiesName),
		modernRequest(t, 3, "tools/call", map[string]any{
			"name": describeCapabilitiesName, "arguments": map[string]any{"tools": named},
		}))
	if defined.frame.Error != nil {
		t.Fatalf("the capability tool was answered with an error envelope: %s", defined.frame.Error.Message)
	}
	out = append(out, capabilitiesFrom(t, defined.frame.Result)...)
	return out
}

// indexedNamesFrom reads the tool names out of a capability index carried in a
// tools/call result frame.
func indexedNamesFrom(t *testing.T, result json.RawMessage) []string {
	t.Helper()
	var call callToolResult
	if err := json.Unmarshal(result, &call); err != nil {
		t.Fatalf("the capability result is not a tool result: %v", err)
	}
	if call.IsError {
		t.Fatalf("the capability tool reported a failure: %s", call.Content[0].Text)
	}
	var payload describeCapabilitiesResult
	if err := json.Unmarshal(call.StructuredContent, &payload); err != nil {
		t.Fatalf("the capability payload does not decode: %v", err)
	}
	return capabilityNames(payload)
}

// advertisedOverStdio reads the same two surfaces on the transport whose caller
// launched this process, through the same dispatch a client drives.
func advertisedOverStdio(t *testing.T, s *server) []advertisement {
	t.Helper()

	var out []advertisement

	res, rpcErr := s.handleToolsList(context.Background())
	if rpcErr != nil {
		t.Fatalf("tools/list returned a protocol error: %s", rpcErr.Message)
	}
	listing, ok := res.(listToolsResult)
	if !ok {
		t.Fatalf("tools/list returned %T, not a listing", res)
	}
	for _, entry := range listing.Tools {
		out = append(out, advertisement{
			surface: "tools/list", tool: entry.Name,
			schema: entry.InputSchema, description: entry.Description,
		})
	}

	// The same two steps a caller takes: read the index, then name what it
	// reported to get the definitions the index deliberately omits.
	indexed := callDiscoveryTool(t, s, describeCapabilitiesName, map[string]any{})
	if indexed.IsError {
		t.Fatalf("the capability tool reported a failure: %s", indexed.Content[0].Text)
	}
	var index describeCapabilitiesResult
	if err := json.Unmarshal(indexed.StructuredContent, &index); err != nil {
		t.Fatalf("the capability payload does not decode: %v", err)
	}
	named := capabilityNames(index)
	if len(named) == 0 {
		t.Fatal("the capability index was empty, so the schemas behind it were never reached")
	}
	defined := callDiscoveryTool(t, s, describeCapabilitiesName, map[string]any{"tools": named})
	if defined.IsError {
		t.Fatalf("the capability tool reported a failure: %s", defined.Content[0].Text)
	}
	out = append(out, capabilitiesFromStructured(t, defined.StructuredContent)...)
	return out
}

// capabilitiesFrom reads the capability listing out of a tools/call result
// frame, which is the shape the listener answers with.
func capabilitiesFrom(t *testing.T, result json.RawMessage) []advertisement {
	t.Helper()
	var call callToolResult
	if err := json.Unmarshal(result, &call); err != nil {
		t.Fatalf("the capability result is not a tool result: %v", err)
	}
	if call.IsError {
		t.Fatalf("the capability tool reported a failure: %s", call.Content[0].Text)
	}
	return capabilitiesFromStructured(t, call.StructuredContent)
}

// capabilitiesFromStructured reads the surfaced definitions out of the
// capability tool's machine-readable payload.
func capabilitiesFromStructured(t *testing.T, structured json.RawMessage) []advertisement {
	t.Helper()
	if len(structured) == 0 {
		t.Fatal("the capability tool returned no structured payload to read")
	}
	var payload describeCapabilitiesResult
	if err := json.Unmarshal(structured, &payload); err != nil {
		t.Fatalf("the capability payload does not decode: %v", err)
	}
	out := make([]advertisement, 0, len(payload.Capabilities))
	for _, c := range payload.Capabilities {
		out = append(out, advertisement{
			surface: describeCapabilitiesName, tool: c.Name,
			schema: c.InputSchema, description: c.Description,
		})
	}
	return out
}

// workspaceArgumentSites reports every place a schema declares one of names as a
// property or demands it as required, naming the path so a failure says where.
// It walks the whole document rather than its leading object, so a name a
// subschema carries is reported too.
func workspaceArgumentSites(t *testing.T, schema json.RawMessage, names []string) []string {
	t.Helper()
	var doc any
	if err := json.Unmarshal(schema, &doc); err != nil {
		t.Fatalf("an advertised schema does not parse as JSON: %v", err)
	}

	var found []string
	var walk func(path string, node any, parentKey string)
	walk = func(path string, node any, parentKey string) {
		switch v := node.(type) {
		case map[string]any:
			keys := make([]string, 0, len(v))
			for k := range v {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if parentKey == "properties" && slices.Contains(names, k) {
					found = append(found, path+"/"+k+" declared")
				}
				walk(path+"/"+k, v[k], k)
			}
		case []any:
			for i, e := range v {
				if s, ok := e.(string); ok && parentKey == "required" && slices.Contains(names, s) {
					found = append(found, fmt.Sprintf("%s[%d] required as %s", path, i, s))
				}
				walk(fmt.Sprintf("%s[%d]", path, i), e, parentKey)
			}
		}
	}
	walk("", doc, "")
	sort.Strings(found)
	return found
}

// toolsCarryingAWorkspaceArgument names the tools whose advertised schemas carry
// one, deduplicated across the surfaces that advertised them and sorted so the
// set is comparable.
func toolsCarryingAWorkspaceArgument(t *testing.T, advertised []advertisement) []string {
	t.Helper()
	seen := map[string]bool{}
	for _, a := range advertised {
		if len(workspaceArgumentSites(t, a.schema, workspaceSelectionArguments)) > 0 {
			seen[a.tool] = true
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// advertisedToolNames names every tool the walked surfaces offered, so the two
// transports can be compared on population as well as on content.
func advertisedToolNames(advertised []advertisement) []string {
	seen := map[string]bool{}
	for _, a := range advertised {
		seen[a.tool] = true
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// TestAdvertisedSchemasCarryTheArgumentsTheTransportAccepts walks every schema
// both surfaces advertise, on both transports.
//
// The two rows are one assertion read in both directions. The listener row is
// the property under test: no schema offers an argument its own call surface
// refuses. The standard-streams row is the control that keeps the listener row
// from passing vacuously — the same walk over the same code path must find those
// arguments where they are still accepted, so a walk that had stopped seeing
// them fails here instead of reporting the listener clean.
func TestAdvertisedSchemasCarryTheArgumentsTheTransportAccepts(t *testing.T) {
	overHTTP := advertisedOverHTTP(t, newHTTPFixture(t, defaultTransportConfig(), nil))
	overStdio := advertisedOverStdio(t, newCommandToolServer(t, nil))

	for _, tc := range []struct {
		name string
		// advertised is every definition that transport offered.
		advertised []advertisement
		// offersWorkspaceArguments is whether a caller on that transport may name
		// a workspace, and so whether the schemas may say it can.
		offersWorkspaceArguments bool
	}{
		{"reached over a listener", overHTTP, false},
		{"reached over the process's own streams", overStdio, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if len(tc.advertised) == 0 {
				t.Fatal("no schema was advertised at all; the walk measured nothing")
			}
			for _, surface := range []string{"tools/list", describeCapabilitiesName} {
				if !slices.ContainsFunc(tc.advertised, func(a advertisement) bool { return a.surface == surface }) {
					t.Fatalf("%s advertised nothing, so the walk did not reach it", surface)
				}
			}

			carrying := toolsCarryingAWorkspaceArgument(t, tc.advertised)
			if !tc.offersWorkspaceArguments {
				for _, a := range tc.advertised {
					for _, site := range workspaceArgumentSites(t, a.schema, workspaceSelectionArguments) {
						t.Errorf("%s advertises %s with %s, which a call on this transport is refused for sending",
							a.surface, a.tool, site)
					}
				}
				return
			}
			if len(carrying) == 0 {
				t.Fatal("no advertised schema carries an argument that names a workspace, " +
					"so the walk proves nothing about the transport that withholds them")
			}
			t.Logf("%d tool(s) advertise an argument that names a workspace: %v", len(carrying), carrying)
		})
	}

	// A transport withholds arguments from the tools it offers, and withholds
	// exactly one whole class of tool: the ones this server does not resolve the
	// arguments of, which are composed away over HTTP rather than filtered. Every
	// tool outside that class must still be offered, because a projection that
	// dropped one would satisfy every assertion above by advertising less.
	onlyAtTheKeyboard := toolsReachingTheHostUnconfined(overStdio)
	if len(onlyAtTheKeyboard) == 0 {
		t.Fatal("no tool is offered only to a caller at this machine's keyboard, so the exclusion below " +
			"excludes nothing and would hide a tool dropped for any other reason")
	}
	var offeredToBoth []string
	for _, name := range advertisedToolNames(overStdio) {
		if !containsStr(onlyAtTheKeyboard, name) {
			offeredToBoth = append(offeredToBoth, name)
		}
	}
	missing := missingFrom(offeredToBoth, advertisedToolNames(overHTTP))
	if len(missing) > 0 {
		t.Errorf("a caller reaching the listener is not offered %v at all", missing)
	}
}

// TestEveryToolDeclaringAWorkspaceArgumentStillAdvertisesItOverStdio is the
// other half of the control above: not merely that some schema still carries
// one, but that every schema which declares one does.
//
// It names the difference rather than counting the two sides, because two sets
// both short by one still agree on length.
func TestEveryToolDeclaringAWorkspaceArgumentStillAdvertisesItOverStdio(t *testing.T) {
	s := newCommandToolServer(t, nil)

	declaring := map[string]bool{}
	for _, spec := range registeredToolSpecs(s) {
		if len(workspaceArgumentSites(t, json.RawMessage(spec.inputSchema), workspaceSelectionArguments)) > 0 {
			declaring[spec.name] = true
		}
	}
	if len(declaring) == 0 {
		t.Fatal("no registered tool declares an argument that names a workspace; the assertion measured nothing")
	}

	advertised := advertisedOverStdio(t, s)
	offered := advertisedToolNames(advertised)
	carrying := map[string]bool{}
	for _, name := range toolsCarryingAWorkspaceArgument(t, advertised) {
		carrying[name] = true
	}

	var dropped []string
	for name := range declaring {
		// A tool these surfaces never offer cannot be measured here; the
		// population comparison in the walk above is what covers those.
		if !carrying[name] && slices.Contains(offered, name) {
			dropped = append(dropped, name)
		}
	}
	sort.Strings(dropped)
	if len(dropped) > 0 {
		t.Errorf("%v declare an argument that names a workspace and no longer advertise it, "+
			"so a caller that may send it is not told so", dropped)
	}
}

// onTheWire renders schema bytes as the protocol encoder renders them, so two
// advertisements are compared as a caller receives them rather than as they
// happen to be spelled in the process that produced them.
func onTheWire(t *testing.T, schema json.RawMessage) string {
	t.Helper()
	encoded, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("an advertised schema does not encode: %v", err)
	}
	return string(encoded)
}

// TestASchemaWithNoWorkspaceArgumentIsAdvertisedAsAuthored pins the bytes the
// withholding does not touch.
//
// Identical input produces identical output here as everywhere else, and a
// schema with nothing to withhold is the case where that is checkable against a
// second source: what the other transport advertises. A projection that rebuilt
// every schema through an unordered map would pass every assertion above and
// fail this one.
func TestASchemaWithNoWorkspaceArgumentIsAdvertisedAsAuthored(t *testing.T) {
	overHTTP := advertisedOverHTTP(t, newHTTPFixture(t, defaultTransportConfig(), nil))
	overStdio := advertisedOverStdio(t, newCommandToolServer(t, nil))

	authored := map[string]json.RawMessage{}
	for _, a := range overStdio {
		if len(workspaceArgumentSites(t, a.schema, workspaceSelectionArguments)) == 0 {
			authored[a.surface+"/"+a.tool] = a.schema
		}
	}
	if len(authored) == 0 {
		t.Fatal("every advertised schema carries an argument that names a workspace; " +
			"the assertion has no untouched schema to measure")
	}

	compared := 0
	for _, a := range overHTTP {
		want, ok := authored[a.surface+"/"+a.tool]
		if !ok {
			continue
		}
		compared++
		if got := onTheWire(t, a.schema); got != onTheWire(t, want) {
			t.Errorf("%s advertises %s differently on the two transports though it withholds nothing from it:\n"+
				"  over a listener: %s\n  over the process's own streams: %s",
				a.surface, a.tool, got, onTheWire(t, want))
		}
	}
	if compared == 0 {
		t.Fatal("no schema was compared across the two transports; the assertion measured nothing")
	}
	t.Logf("compared %d untouched advertisement(s) across the two transports", compared)
}

// TestSchemaWithoutPropertiesRemovesWhatItNamesAndNothingElse measures the
// projection itself, on the shapes the surface walk above cannot reach because
// no tool is written in them today — a required list naming a withheld property,
// and a list the removal empties.
//
// A schema that names nothing to remove is the positive control: without it
// every row would pass against a projection that returned an empty object.
func TestSchemaWithoutPropertiesRemovesWhatItNamesAndNothingElse(t *testing.T) {
	const both = `{"type":"object","properties":{"release":{"type":"string"},` +
		`"workspace_root":{"type":"string"},"catalog_dirs":{"type":"array"}}}`

	for _, tc := range []struct {
		// scenario says what the row is about, so a failure reads as a rule.
		scenario string
		schema   string
		names    []string
		want     string
	}{
		{
			scenario: "a declared property is removed",
			schema:   `{"type":"object","properties":{"release":{"type":"string"},"workspace_root":{"type":"string"}}}`,
			names:    []string{"workspace_root"},
			want:     `{"type":"object","properties":{"release":{"type":"string"}}}`,
		},
		{
			scenario: "the keys that stay keep the order they were written in",
			schema: `{"type":"object","properties":{"zeta":{"type":"string"},` +
				`"workspace_root":{"type":"string"},"alpha":{"type":"string"}},"additionalProperties":false}`,
			names: []string{"workspace_root"},
			want: `{"type":"object","properties":{"zeta":{"type":"string"},"alpha":{"type":"string"}},` +
				`"additionalProperties":false}`,
		},
		{
			scenario: "a removed property leaves the list of what is required",
			schema: `{"type":"object","properties":{"release":{"type":"string"},` +
				`"workspace_root":{"type":"string"}},"required":["release","workspace_root"]}`,
			names: []string{"workspace_root"},
			want:  `{"type":"object","properties":{"release":{"type":"string"}},"required":["release"]}`,
		},
		{
			scenario: "a list of what is required that the removal empties is dropped",
			schema:   `{"type":"object","properties":{"workspace_root":{"type":"string"}},"required":["workspace_root"]}`,
			names:    []string{"workspace_root"},
			want:     `{"type":"object","properties":{}}`,
		},
		{
			scenario: "both names go at once",
			schema:   both,
			names:    []string{"workspace_root", "catalog_dirs"},
			want:     `{"type":"object","properties":{"release":{"type":"string"}}}`,
		},
		{
			// The bound the projection states: it reads the object the schema is,
			// and a subschema's own declarations are not that object's.
			scenario: "a name a subschema declares is left where it is",
			schema: `{"type":"object","properties":{"nested":{"type":"object",` +
				`"properties":{"workspace_root":{"type":"string"}}}}}`,
			names: []string{"workspace_root"},
			want: `{"type":"object","properties":{"nested":{"type":"object",` +
				`"properties":{"workspace_root":{"type":"string"}}}}}`,
		},
		{
			// The positive control. Byte-identical rather than merely equivalent:
			// a schema with nothing to remove is handed back, not rebuilt.
			scenario: "a schema naming none of them comes back as its own bytes",
			schema:   "{\n  \"type\": \"object\",\n  \"properties\": {\n    \"release\": {\"type\": \"string\"}\n  }\n}",
			names:    []string{"workspace_root", "catalog_dirs"},
			want:     "{\n  \"type\": \"object\",\n  \"properties\": {\n    \"release\": {\"type\": \"string\"}\n  }\n}",
		},
		{
			scenario: "a document this server cannot read is not rewritten",
			schema:   `{"type":"object",`,
			names:    []string{"workspace_root"},
			want:     `{"type":"object",`,
		},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			if got := schemaWithoutProperties(tc.schema, tc.names); got != tc.want {
				t.Errorf("the projection produced\n  %s\nwant\n  %s", got, tc.want)
			}
		})
	}
}

// TestTheSameSessionAdvertisesTheSameBytesTwice pins the other half of that
// contract: the bytes are a property of the session, not of when it was asked.
func TestTheSameSessionAdvertisesTheSameBytesTwice(t *testing.T) {
	f := newHTTPFixture(t, defaultTransportConfig(), nil)

	first, second := advertisedOverHTTP(t, f), advertisedOverHTTP(t, f)
	if len(first) != len(second) {
		t.Fatalf("one session advertised %d definition(s) and then %d", len(first), len(second))
	}
	for i := range first {
		if first[i].tool != second[i].tool || first[i].surface != second[i].surface {
			t.Fatalf("one session advertised %s/%s and then %s/%s in the same position",
				first[i].surface, first[i].tool, second[i].surface, second[i].tool)
		}
		if onTheWire(t, first[i].schema) != onTheWire(t, second[i].schema) {
			t.Errorf("%s advertises %s with different bytes on a second reading:\n  %s\n  %s",
				first[i].surface, first[i].tool,
				onTheWire(t, first[i].schema), onTheWire(t, second[i].schema))
		}
	}
}
