// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcpconfig

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestMembersReadsEveryValueAServerConfigurationHolds holds Members to the
// configurations the Langflow and Flowise importers meet — a server reached at
// an address with the headers sent to it, written as an object or as a list of
// key and value items, and a server started by a command with its arguments, a
// list or a single text, and its environment — each value returned with its
// kind, its name and the reference tokens addressing it, by kind in a fixed
// order whatever order the configuration writes its members in. A header item
// that is not an object is returned whole, named by its position, one carrying
// a key and no value holds none, and one whose key is not a string is named as
// the key is written; a configuration that is not an object, and args written
// null, hold none.
func TestMembersReadsEveryValueAServerConfigurationHolds(t *testing.T) {
	t.Parallel()
	m := func(kind Kind, name string, value string, tokens ...string) Member {
		return Member{Kind: kind, Name: name, Tokens: tokens, Value: json.RawMessage(value)}
	}
	for _, tc := range []struct {
		name   string
		config string
		want   []Member
	}{
		{
			name:   "an address with headers as an object, its members written last to first",
			config: `{"url":"https://a.example/mcp","env":{"TOKEN":"t"},"headers":{"Authorization":"Bearer b","X-Empty":""}}`,
			want: []Member{
				m(Header, "Authorization", `"Bearer b"`, "headers", "Authorization"),
				m(Header, "X-Empty", `""`, "headers", "X-Empty"),
				m(Env, "TOKEN", `"t"`, "env", "TOKEN"),
				m(Address, "", `"https://a.example/mcp"`, "url"),
			},
		},
		{
			name: "headers as a list of key and value items",
			config: `{"headers":[{"key":"Authorization","value":"Bearer b"},"not an item",{"key":"X-No-Value"},` +
				`{"key":7,"value":"v"},{"key":"X-Null","value":null}]}`,
			want: []Member{
				m(Header, "Authorization", `"Bearer b"`, "headers", "0", "value"),
				m(Header, "1", `"not an item"`, "headers", "1"),
				m(Header, "7", `"v"`, "headers", "3", "value"),
				m(Header, "X-Null", `null`, "headers", "4", "value"),
			},
		},
		{
			name:   "a command with its arguments as a list",
			config: `{"args":["-y","--api-key=k",""],"command":"npx","env":{"API_KEY":"e"}}`,
			want: []Member{
				m(Env, "API_KEY", `"e"`, "env", "API_KEY"),
				m(Command, "", `"npx"`, "command"),
				m(Argument, "0", `"-y"`, "args", "0"),
				m(Argument, "1", `"--api-key=k"`, "args", "1"),
				m(Argument, "2", `""`, "args", "2"),
			},
		},
		{
			name:   "arguments as one text",
			config: `{"command":"uvx","args":"--token=k"}`,
			want:   []Member{m(Command, "", `"uvx"`, "command"), m(Argument, "", `"--token=k"`, "args")},
		},
		{name: "arguments written null", config: `{"args":null}`},
		{name: "a configuration that is not an object", config: `"https://a.example/mcp"`},
		{name: "no configuration", config: ``},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Members(json.RawMessage(tc.config))
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Members(%s) =\n%+v\nwant\n%+v", tc.config, got, tc.want)
			}
		})
	}
}

// TestFieldMembersReadsAServerWrittenInFieldsOfANodesOwn holds FieldMembers to
// the layout an MCP tool node writes its server in when it carries it as fields
// of its own template, each holding its value in a value member: the address in
// sse_url, the headers in headers_input — a list of key and value items, or an
// object — the environment in env — a list of entries, or one text — and the
// whole command line in command. Each value is returned with its kind, its name
// and the reference tokens addressing it beneath the template, by kind in a
// fixed order whatever order the template writes its fields in; the template's
// other fields are not read. A field holding no value member, a field that is
// not an object, env written null, a template that is not an object, and the
// members of a configuration object, which are not fields, hold none.
func TestFieldMembersReadsAServerWrittenInFieldsOfANodesOwn(t *testing.T) {
	t.Parallel()
	m := func(kind Kind, name string, value string, tokens ...string) Member {
		return Member{Kind: kind, Name: name, Tokens: tokens, Value: json.RawMessage(value)}
	}
	for _, tc := range []struct {
		name     string
		template string
		want     []Member
	}{
		{
			name: "an address with headers and environment entries as lists, its fields written last to first",
			template: `{"code":{"type":"code","value":"class C"},"mode":{"type":"tab","value":"SSE"},` +
				`"command":{"type":"str","value":"uvx mcp-server-fetch --api-key=k"},` +
				`"env":{"type":"str","list":true,"value":["DEBUG=true","API_KEY=e",""]},` +
				`"sse_url":{"type":"str","value":"http://h.example/sse?key=a"},` +
				`"headers_input":{"type":"table","value":[{"key":"Authorization","value":"Bearer b"},{"key":"X-No-Value"}]},` +
				`"tool":{"type":"str","value":"fetch"}}`,
			want: []Member{
				m(Header, "Authorization", `"Bearer b"`, "headers_input", "value", "0", "value"),
				m(Env, "0", `"DEBUG=true"`, "env", "value", "0"),
				m(Env, "1", `"API_KEY=e"`, "env", "value", "1"),
				m(Env, "2", `""`, "env", "value", "2"),
				m(Address, "", `"http://h.example/sse?key=a"`, "sse_url", "value"),
				m(Command, "", `"uvx mcp-server-fetch --api-key=k"`, "command", "value"),
			},
		},
		{
			name:     "headers as an object and the environment as one text",
			template: `{"headers_input":{"value":{"X-Api-Key":"k"}},"env":{"value":"TOKEN=t"}}`,
			want: []Member{
				m(Header, "X-Api-Key", `"k"`, "headers_input", "value", "X-Api-Key"),
				m(Env, "", `"TOKEN=t"`, "env", "value"),
			},
		},
		{
			name:     "a field written twice",
			template: `{"sse_url":{"value":"http://a.example/sse"},"sse_url":{"value":"http://b.example/sse"}}`,
			want: []Member{
				m(Address, "", `"http://a.example/sse"`, "sse_url", "value"),
				m(Address, "", `"http://b.example/sse"`, "sse_url", "value"),
			},
		},
		{
			name:     "env written null, a field holding no value member, and a field that is not an object",
			template: `{"env":{"value":null},"sse_url":{"type":"str"},"command":"uvx"}`,
		},
		{
			name:     "the members of a configuration object",
			template: `{"url":"http://a.example/mcp","headers":{"Authorization":"Bearer b"},"args":["--api-key=k"]}`,
		},
		{name: "a template that is not an object", template: `[{"sse_url":{"value":"http://a.example/sse"}}]`},
		{name: "no template", template: ``},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := FieldMembers(json.RawMessage(tc.template))
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("FieldMembers(%s) =\n%+v\nwant\n%+v", tc.template, got, tc.want)
			}
		})
	}
}

// TestHeaderValuesReadsEveryShapeAFlowWritesHeadersIn holds HeaderValues to
// each shape a flow keeps headers in — an object of names to values, rows of a
// key and a value, objects merged into the headers whole, and items of any
// other kind — each value named by its header, and its tokens beneath the ones
// given. A row carrying a key and no value, and an item that is null, empty
// text, an empty object or an empty list, hold none; headers that are neither an
// object nor a list hold none.
func TestHeaderValuesReadsEveryShapeAFlowWritesHeadersIn(t *testing.T) {
	t.Parallel()
	m := func(name string, value string, tokens ...string) Member {
		return Member{Kind: Header, Name: name, Tokens: tokens, Value: json.RawMessage(value)}
	}
	for _, tc := range []struct {
		name, headers string
		want          []Member
	}{
		{name: "an object", headers: `{"Authorization":"Bearer b","X-Empty":""}`,
			want: []Member{m("Authorization", `"Bearer b"`, "h", "value", "Authorization"),
				m("X-Empty", `""`, "h", "value", "X-Empty")}},
		{name: "rows of a key and a value", headers: `[{"key":"User-Agent","value":"langflow"},{"key":"X-Api-Key","value":null}]`,
			want: []Member{m("User-Agent", `"langflow"`, "h", "value", "0", "value"),
				m("X-Api-Key", `null`, "h", "value", "1", "value")}},
		{name: "objects merged whole", headers: `[{"Authorization":"Bearer b","X-Trace":"t"}]`,
			want: []Member{m("Authorization", `"Bearer b"`, "h", "value", "0", "Authorization"),
				m("X-Trace", `"t"`, "h", "value", "0", "X-Trace")}},
		{name: "items of another kind", headers: `["Authorization: Bearer b",7,null,"",{},[],{"key":"X-Name-Only"}]`,
			want: []Member{m("0", `"Authorization: Bearer b"`, "h", "value", "0"), m("1", `7`, "h", "value", "1")}},
		{name: "a text", headers: `"Authorization: Bearer b"`},
		{name: "null", headers: `null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := HeaderValues(json.RawMessage(tc.headers), "h", "value")
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("HeaderValues(%s) =\n%+v\nwant\n%+v", tc.headers, got, tc.want)
			}
		})
	}
}

// TestBlankReadsAValueWrittenEmpty pins which values a configuration writes
// empty already.
func TestBlankReadsAValueWrittenEmpty(t *testing.T) {
	t.Parallel()
	for value, want := range map[string]bool{`""`: true, `null`: true, `" "`: false, `"x"`: false, `[]`: false} {
		if got := Blank(json.RawMessage(value)); got != want {
			t.Errorf("Blank(%s) = %v, want %v", value, got, want)
		}
	}
}
