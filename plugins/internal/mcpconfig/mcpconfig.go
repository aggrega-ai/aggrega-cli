// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package mcpconfig reads the server configuration a flow's MCP tool node
// carries — the address of a server reached over HTTP and the headers sent to
// it, or the command starting a server and the arguments and environment it is
// started with — for the importer guests built on the plugin kit, which write
// those values empty before a flow is carried into a draft, since a key can sit
// in any of them. It is the one place the importers walk such a configuration,
// so the Langflow and the Flowise importer empty the same members of it.
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
	// Name is the header's or the environment variable's name, the position of
	// an argument in args written in decimal, and empty for the address, the
	// command, and args written whole.
	Name string
	// Tokens are the JSON Pointer (RFC 6901) reference tokens addressing the
	// value beneath the configuration.
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
		if e.Key != "headers" {
			continue
		}
		for _, h := range importer.ObjectEntries(e.Value) {
			out = append(out, Member{Kind: Header, Name: h.Key, Tokens: []string{"headers", h.Key}, Value: h.Value})
		}
		var items []json.RawMessage
		if json.Unmarshal(e.Value, &items) != nil {
			continue
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
			out = append(out, Member{Kind: Header, Name: name, Tokens: []string{"headers", strconv.Itoa(j), "value"},
				Value: item.Value})
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
		if e.Key != "args" {
			continue
		}
		var args []json.RawMessage
		if json.Unmarshal(e.Value, &args) != nil {
			out = append(out, Member{Kind: Argument, Tokens: []string{"args"}, Value: e.Value})
			continue
		}
		for j, a := range args {
			out = append(out, Member{Kind: Argument, Name: strconv.Itoa(j), Tokens: []string{"args", strconv.Itoa(j)},
				Value: a})
		}
	}
	return out
}

// Blank reports whether a value is written empty already: an empty string, or
// null.
func Blank(value json.RawMessage) bool {
	s := string(value)
	return s == `""` || s == "null"
}
