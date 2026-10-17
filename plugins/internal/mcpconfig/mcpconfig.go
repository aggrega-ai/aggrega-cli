// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package mcpconfig reads the server configuration a flow's MCP tool node
// carries — the address of a server reached over HTTP and the headers sent to
// it, or the command starting a server and the arguments and environment it is
// started with — for the importer guests built on the plugin kit, which write
// those values empty before a flow is carried into a draft, since a key can sit
// in any of them. It reads such a configuration written as one object
// ([Members]) and written as fields of the node's own ([FieldMembers]). It is
// the one place the importers walk such a configuration, so the Langflow and
// the Flowise importer empty the same members of it, in either layout.
package mcpconfig

import (
	"encoding/json"
	"strconv"

	"github.com/aggrega-ai/aggrega-pdk-go/pkg/importer"
)

// Kind is what a member of a server configuration is.
type Kind int

// The kinds of member a server configuration holds a value in, in the order
// [Members] returns them.
const (
	// Header is the value of a header sent to the server.
	Header Kind = iota
	// Env is the value of an environment variable the server is started with.
	Env
	// Address is the server's address, its url.
	Address
	// Command is the command starting the server.
	Command
	// Argument is an argument the server is started with: one element of args,
	// or the whole of args where it is not a list.
	Argument
)

// Member is one value a server configuration holds.
type Member struct {
	Kind Kind
	// Name is the header's or the environment variable's name, the position
	// written in decimal of an argument in args or of an entry in an env written
	// as a list, and empty for the address, the command, and args or env written
	// whole.
	Name string
	// Tokens are the JSON Pointer (RFC 6901) reference tokens addressing the
	// value beneath the configuration, or beneath the template for a member
	// [FieldMembers] returns.
	Tokens []string
	// Value is the value as the configuration writes it.
	Value json.RawMessage
}

// Members returns every value the server configuration config holds, a JSON
// object, by kind in the order of [Kind] and, within a kind, in the order the
// configuration writes them, each repetition of a member included:
//
//   - each header value, the headers written as an object of names to values,
//     or as a list of items each carrying a key and a value — an item carrying
//     no value member, or that is not an object, holds none, and an item's key
//     that is not a string is named as it is written;
//   - each environment value, the env written as an object of names to values;
//   - the url;
//   - the command;
//   - each element of args where it is a list, and the whole of args where it
//     is not.
//
// A configuration that is not a JSON object holds no member.
func Members(config json.RawMessage) []Member {
	entries := importer.ObjectEntries(config)
	var out []Member
	for _, e := range entries {
		if e.Key == "headers" {
			out = append(out, headerMembers(e.Value, "headers")...)
		}
	}
	for _, e := range entries {
		if e.Key != "env" {
			continue
		}
		for _, v := range importer.ObjectEntries(e.Value) {
			out = append(out, Member{Kind: Env, Name: v.Key, Tokens: []string{"env", v.Key}, Value: v.Value})
		}
	}
	for _, e := range entries {
		if e.Key == "url" {
			out = append(out, Member{Kind: Address, Tokens: []string{"url"}, Value: e.Value})
		}
	}
	for _, e := range entries {
		if e.Key == "command" {
			out = append(out, Member{Kind: Command, Tokens: []string{"command"}, Value: e.Value})
		}
	}
	for _, e := range entries {
		if e.Key == "args" {
			out = append(out, listMembers(Argument, e.Value, "args")...)
		}
	}
	return out
}

// FieldMembers returns every value the server configuration holds that an MCP
// tool node writes as fields of its own template rather than as one
// configuration object — as a Langflow MCP Tools node carrying no mcp_server
// field does, each field an object holding its value in a value member. It
// returns them by kind in the order of [Kind] and, within a kind, in the order
// the template writes them, each repetition of a field or of its value member
// included:
//
//   - each header value of headers_input, read as [Members] reads headers;
//   - each entry of env where it is a list — a text naming a variable and
//     its value — and the whole of env where it is not;
//   - sse_url, the address;
//   - command, the whole command line, the arguments it starts the server
//     with included, since the layout holds no field of arguments.
//
// Each member's tokens address its value beneath the template. A template that
// is not a JSON object holds no member, and a field holding no value member
// holds none.
func FieldMembers(template json.RawMessage) []Member {
	entries := importer.ObjectEntries(template)
	// values returns the value member of each field named name.
	values := func(name string) []json.RawMessage {
		var out []json.RawMessage
		for _, e := range entries {
			if e.Key != name {
				continue
			}
			for _, v := range importer.ObjectEntries(e.Value) {
				if v.Key == "value" {
					out = append(out, v.Value)
				}
			}
		}
		return out
	}
	var out []Member
	for _, v := range values("headers_input") {
		out = append(out, headerMembers(v, "headers_input", "value")...)
	}
	for _, v := range values("env") {
		out = append(out, listMembers(Env, v, "env", "value")...)
	}
	for _, v := range values("sse_url") {
		out = append(out, Member{Kind: Address, Tokens: []string{"sse_url", "value"}, Value: v})
	}
	for _, v := range values("command") {
		out = append(out, Member{Kind: Command, Tokens: []string{"command", "value"}, Value: v})
	}
	return out
}

// headerMembers returns each header value of headers, addressed beneath the
// tokens at: the headers written as an object of names to values, or as a list
// of items each carrying a key and a value — an item carrying no value member,
// or that is not an object, holds none, and an item's key that is not a string
// is named as it is written.
func headerMembers(headers json.RawMessage, at ...string) []Member {
	var out []Member
	for _, h := range importer.ObjectEntries(headers) {
		out = append(out, Member{Kind: Header, Name: h.Key, Tokens: beneath(at, h.Key), Value: h.Value})
	}
	var items []json.RawMessage
	if json.Unmarshal(headers, &items) != nil {
		return out
	}
	for j, raw := range items {
		var item struct {
			Key   json.RawMessage `json:"key"`
			Value json.RawMessage `json:"value"`
		}
		if json.Unmarshal(raw, &item) != nil || item.Value == nil {
			continue
		}
		name := string(item.Key)
		var key string
		if json.Unmarshal(item.Key, &key) == nil {
			name = key
		}
		out = append(out, Member{Kind: Header, Name: name, Tokens: beneath(at, strconv.Itoa(j), "value"),
			Value: item.Value})
	}
	return out
}

// listMembers returns, as members of kind addressed beneath the tokens at, each
// element of value where it is a list, named by its position, and the whole of
// value where it is not; null holds none.
func listMembers(kind Kind, value json.RawMessage, at ...string) []Member {
	var list []json.RawMessage
	if json.Unmarshal(value, &list) != nil {
		return []Member{{Kind: kind, Tokens: beneath(at), Value: value}}
	}
	var out []Member
	for j, v := range list {
		out = append(out, Member{Kind: kind, Name: strconv.Itoa(j), Tokens: beneath(at, strconv.Itoa(j)), Value: v})
	}
	return out
}

// beneath returns the tokens at followed by more, in a slice of their own.
func beneath(at []string, more ...string) []string {
	return append(append(make([]string, 0, len(at)+len(more)), at...), more...)
}

// Blank reports whether a value is written empty already: an empty string, or
// null.
func Blank(value json.RawMessage) bool {
	s := string(value)
	return s == `""` || s == "null"
}
