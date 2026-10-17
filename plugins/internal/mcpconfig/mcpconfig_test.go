// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

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
// that is not an object, or that carries no value, holds none, and one whose
// key is not a string is named as the key is written; a configuration that is
// not an object, and args written null, hold none.
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
