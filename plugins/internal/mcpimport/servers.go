// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcpimport

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/draftyaml"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/importer"
)

// server is one MCP server a source lists, as the draft writes it: its name,
// how it runs — a command line behind the bridge, an address someone else
// serves it at, or an image — and the environment it is started with, each value
// with what the draft says of it.
type server struct {
	// name is the server's name in the source; ref names it in a report, and key
	// is its instance's key in the drafts.
	name, ref, key string
	// command is the server's command line, the command first, for a server run
	// behind the bridge; nil where it runs otherwise or names no command.
	command []item
	// stdio is set for a server run behind the bridge, whether or not it names a
	// command.
	stdio bool
	// runtime is the bridge image's launcher the command line runs from, npm or
	// pypi, where the source states it; runtimeFrom is set instead where it is to
	// be read off the command the draft writes.
	runtime     string
	runtimeFrom bool
	// endpoint is the address of a server someone else runs, as the draft writes
	// it; nil for a server that runs otherwise.
	endpoint *item
	// image is the image a server published as an image runs from.
	image string
	// env is the environment the server is started with, in the order the source
	// writes it.
	env []namedValue
	// headers are the headers the source sends the server, in the order the source
	// writes them.
	headers []namedValue
	// notCarried names the members of the server the draft writes nowhere.
	notCarried []string
	// left names what the draft leaves for the Release, and notes says what else
	// a reader of the draft needs to know about the server.
	left, notes []string
}

// item is one value the draft writes: its text, and what the draft says of it.
type item struct {
	text string
	// reference is set for a value that is one variable reference and nothing
	// else, which holds no value of its own.
	reference bool
	// class is the credential class the value is located under.
	class importer.CredentialClass
	// unset is set for a value the source leaves for its user to supply, which
	// the draft writes empty; hint names what it is.
	unset bool
	hint  string
}

// namedValue is one variable of a server's environment, or one header it is
// sent: its name, and its value where the source writes one.
type namedValue struct {
	name  string
	value *item
	// hint says, for a value the source does not write, what it leaves in its
	// place.
	hint string
	// emptied is set for a header whose value the empty_credentials setting
	// leaves out.
	emptied bool
}

// clientServers reads the servers an MCP client's configuration lists: the
// members of its top-level mcpServers object, as Claude Code, Claude Desktop and
// Cursor write them, and of its top-level servers object, as Visual Studio Code
// writes them, in that order and each in the order the source writes it. A member
// that is not an object is not a server.
func clientServers(top []importer.ObjectEntry) []server {
	var out []server
	for _, list := range []string{"mcpServers", "servers"} {
		for _, e := range top {
			if e.Key != list {
				continue
			}
			for _, s := range members(e.Value) {
				if fields := importer.ObjectEntries(s.Value); fields != nil {
					out = append(out, clientServer(s.Key, members(s.Value)))
				}
			}
		}
	}
	return out
}

// clientServer reads one server of a client's configuration. A server whose type
// is stdio, or that names no type and carries a command, runs behind the bridge:
// its command and its args are its command line and its env its environment. A
// server whose type is http, streamable-http or sse, or that names no type and no
// command and carries a url, is reached at its url, and sent its headers. Every
// other member — one the server's kind does not read, or one no infrastructure
// key of the tool takes, such as cwd, envFile or auth — is not carried.
func clientServer(name string, fields []importer.ObjectEntry) server {
	s := server{name: name}
	var typ string
	var command, url json.RawMessage
	for _, f := range fields {
		switch f.Key {
		case "type":
			_ = json.Unmarshal(f.Value, &typ)
		case "command":
			command = f.Value
		case "url":
			url = f.Value
		}
	}
	switch typ {
	case "stdio":
		s.stdio = true
	case "http", "streamable-http", "sse":
	default:
		s.stdio = command != nil || url == nil
	}
	for _, f := range fields {
		switch {
		case f.Key == "type":
		case s.stdio && f.Key == "command":
			if text, ok := scalarText(f.Value); ok {
				s.command = append([]item{valueItem(text, importer.CredentialServerMember)}, s.command...)
			} else {
				s.notCarried = append(s.notCarried, f.Key)
			}
		case s.stdio && f.Key == "args":
			var args []json.RawMessage
			if json.Unmarshal(f.Value, &args) != nil {
				s.notCarried = append(s.notCarried, f.Key)
				continue
			}
			for _, a := range args {
				text, ok := scalarText(a)
				if !ok {
					s.command = append(s.command, item{unset: true, hint: "the source writes it as no text"})
					continue
				}
				s.command = append(s.command, valueItem(text, importer.CredentialServerMember))
			}
		case s.stdio && f.Key == "env":
			if importer.ObjectEntries(f.Value) == nil {
				s.notCarried = append(s.notCarried, f.Key)
				continue
			}
			s.env = append(s.env, clientValues(f.Value)...)
		case !s.stdio && f.Key == "url":
			if text, ok := scalarText(f.Value); ok {
				s.endpoint = addressItem(&s, text)
			} else {
				s.notCarried = append(s.notCarried, f.Key)
			}
		case !s.stdio && f.Key == "headers":
			s.headers = append(s.headers, clientValues(f.Value)...)
		default:
			s.notCarried = append(s.notCarried, f.Key)
		}
	}
	if s.stdio {
		s.runtimeFrom = true
		if len(s.command) == 0 {
			s.left = append(s.left, "infra.stdio_command, since the server names no command")
		}
	} else if s.endpoint == nil {
		s.left = append(s.left, "infra.endpoint, since the server names no address")
	}
	return s
}

// clientValues reads the members of a client's env or headers object, each a
// value of an MCP server's configuration: its name, and its value where the
// member is written as a scalar.
func clientValues(raw json.RawMessage) []namedValue {
	var out []namedValue
	for _, m := range members(raw) {
		text, ok := scalarText(m.Value)
		if !ok {
			out = append(out, namedValue{name: m.Key, hint: "the source writes no text for it"})
			continue
		}
		value := valueItem(text, importer.CredentialServerMember)
		out = append(out, namedValue{name: m.Key, value: &value})
	}
	return out
}

// registryServer reads the one server a registry server description describes.
// It is drafted from the first package the description lists, or, where it
// lists none, from its first remote; every other package and remote is named in
// a note, since one instance runs one of them.
func registryServer(top []importer.ObjectEntry) server {
	s := server{}
	var packages, remotes []json.RawMessage
	for _, e := range top {
		switch e.Key {
		case "name":
			_ = json.Unmarshal(e.Value, &s.name)
		case "packages":
			_ = json.Unmarshal(e.Value, &packages)
		case "remotes":
			_ = json.Unmarshal(e.Value, &remotes)
		}
	}
	var others []string
	switch {
	case len(packages) > 0:
		registryPackage(&s, members(packages[0]))
		for _, p := range packages[1:] {
			others = append(others, packageName(members(p)))
		}
		for _, r := range remotes {
			others = append(others, remoteName(members(r)))
		}
	case len(remotes) > 0:
		registryRemote(&s, members(remotes[0]))
		for _, r := range remotes[1:] {
			others = append(others, remoteName(members(r)))
		}
	default:
		s.stdio = true
		s.left = append(s.left, "infra.stdio_command, infra.endpoint or infra.image, since the description lists no "+
			"package and no remote")
	}
	if len(others) > 0 {
		s.notes = append(s.notes, "The description also lists "+strings.Join(others, ", ")+
			"; this instance runs the first it lists.")
	}
	return s
}

// registryPackage reads the package a server is drafted from. An npm or a PyPI
// package runs behind the bridge, from the command line its launcher — the
// runtime hint, or npx for npm and uvx for PyPI — its runtime arguments, the
// package with its version, and its package arguments make, started with its
// environment variables. An OCI package runs from its image. A package of any
// other registry is named in a note, its command line and runtime left for the
// Release, since neither bridge image carries its launcher.
func registryPackage(s *server, fields []importer.ObjectEntry) {
	var registryType, identifier, version, hint string
	var runtimeArgs, packageArgs, env json.RawMessage
	for _, f := range fields {
		switch f.Key {
		case "registryType":
			_ = json.Unmarshal(f.Value, &registryType)
		case "identifier":
			_ = json.Unmarshal(f.Value, &identifier)
		case "version":
			_ = json.Unmarshal(f.Value, &version)
		case "runtimeHint":
			_ = json.Unmarshal(f.Value, &hint)
		case "runtimeArguments":
			runtimeArgs = f.Value
		case "packageArguments":
			packageArgs = f.Value
		case "environmentVariables":
			env = f.Value
		}
	}
	var spec, launcher string
	switch registryType {
	case "npm":
		launcher, spec = "npx", identifier
		if version != "" {
			spec += "@" + version
		}
	case "pypi":
		launcher, spec = "uvx", identifier
		if version != "" {
			spec += "==" + version
		}
	case "oci":
		s.image = identifier
		if transportType(fields) == "stdio" {
			s.notes = append(s.notes, "The image speaks the protocol over stdio, while the realization running an "+
				"image reaches it over HTTP.")
		}
		s.notCarried = append(s.notCarried, startingMembers(fields)...)
		return
	default:
		s.stdio = true
		s.left = append(s.left, "infra.stdio_command and infra.stdio_runtime, since the package is a "+
			strconv.Quote(registryType)+" package, and infra.stdio_runtime takes npm or pypi")
		s.notCarried = append(s.notCarried, startingMembers(fields)...)
		return
	}
	s.stdio, s.runtime = true, registryType
	if hint != "" {
		launcher = hint
	}
	s.command = append(s.command, valueItem(launcher, importer.CredentialServerMember))
	s.command = append(s.command, registryArguments(s, runtimeArgs)...)
	s.command = append(s.command, valueItem(spec, importer.CredentialServerMember))
	s.command = append(s.command, registryArguments(s, packageArgs)...)
	s.env = append(s.env, registryValues(env)...)
}

// registryValues reads a registry server description's list of environment
// variables or headers: each input's name, and its value or, where it writes
// none, its default, of the class the input's isSecret decides; an input writing
// neither leaves its value for the user to supply.
func registryValues(raw json.RawMessage) []namedValue {
	var inputs []json.RawMessage
	_ = json.Unmarshal(raw, &inputs)
	var out []namedValue
	for _, in := range inputs {
		input := registryInput(members(in))
		entry := namedValue{name: input.name}
		if input.value != nil {
			v := valueItem(*input.value, input.class())
			entry.value = &v
		} else {
			entry.hint = "the description leaves it for the user to supply"
		}
		out = append(out, entry)
	}
	return out
}

// registryRemote reads the remote a server is drafted from: its url is the
// address, and its headers, read as registryValues reads them, are sent to the
// server.
func registryRemote(s *server, fields []importer.ObjectEntry) {
	for _, f := range fields {
		switch f.Key {
		case "url":
			var url string
			if json.Unmarshal(f.Value, &url) == nil {
				s.endpoint = addressItem(s, url)
			}
		case "headers":
			s.headers = append(s.headers, registryValues(f.Value)...)
		case "variables":
			var names []string
			for _, v := range members(f.Value) {
				names = append(names, v.Key)
			}
			if len(names) > 0 {
				s.notes = append(s.notes, "The address names the variables "+commentList(names)+
					", which the description leaves for the user to supply.")
			}
		}
	}
	if s.endpoint == nil {
		s.left = append(s.left, "infra.endpoint, since the remote names no address")
	}
}

// registryArguments reads the arguments a package is started with, each as the
// item of the command line it is written as: a positional argument as its value,
// or its default where it writes none; a named argument as its name, followed by
// = and its value or default where it writes either. A positional argument
// writing neither is left for the user to supply, written empty and named as
// left for the Release by the label the description gives it.
func registryArguments(s *server, raw json.RawMessage) []item {
	var args []json.RawMessage
	_ = json.Unmarshal(raw, &args)
	var out []item
	for _, a := range args {
		fields := members(a)
		input := registryInput(fields)
		var kind, name, valueHint string
		for _, f := range fields {
			switch f.Key {
			case "type":
				_ = json.Unmarshal(f.Value, &kind)
			case "name":
				_ = json.Unmarshal(f.Value, &name)
			case "valueHint":
				_ = json.Unmarshal(f.Value, &valueHint)
			}
		}
		switch {
		case kind == "named" && input.value != nil:
			out = append(out, valueItem(name+"="+*input.value, input.class()))
		case kind == "named":
			out = append(out, valueItem(name, importer.CredentialServerMember))
		case input.value != nil:
			out = append(out, valueItem(*input.value, input.class()))
		default:
			out = append(out, item{unset: true, hint: "the description leaves " + commentText(firstOf(valueHint, "it")) +
				" for its user to supply"})
		}
	}
	return out
}

// registryValue is one input of a registry server description — an argument,
// an environment variable or a header — as the draft reads it: its name, its
// value or, where it writes none, its default, and whether the description
// marks it secret.
type registryValue struct {
	name   string
	value  *string
	secret bool
}

// class is the credential class a registry input's value is located under: what
// the description marks secret is marked as a credential, and every other value
// is a member of the server's configuration.
func (v registryValue) class() importer.CredentialClass {
	if v.secret {
		return importer.CredentialMarked
	}
	return importer.CredentialServerMember
}

// registryInput reads one input of a registry server description.
func registryInput(fields []importer.ObjectEntry) registryValue {
	var v registryValue
	var value, def *string
	for _, f := range fields {
		var text string
		switch f.Key {
		case "name":
			_ = json.Unmarshal(f.Value, &v.name)
		case "value":
			if json.Unmarshal(f.Value, &text) == nil {
				value = &text
			}
		case "default":
			if json.Unmarshal(f.Value, &text) == nil {
				def = &text
			}
		case "isSecret":
			_ = json.Unmarshal(f.Value, &v.secret)
		}
	}
	v.value = value
	if v.value == nil {
		v.value = def
	}
	return v
}

// startingMembers names the members of a package that say how it is started —
// its arguments and its environment — where the package writes them, for a
// package the draft writes no command line for.
func startingMembers(fields []importer.ObjectEntry) []string {
	var out []string
	for _, f := range fields {
		switch f.Key {
		case "runtimeArguments", "packageArguments", "environmentVariables":
			out = append(out, f.Key)
		}
	}
	return out
}

// transportType returns the type of a package's transport.
func transportType(fields []importer.ObjectEntry) string {
	var transport struct {
		Type string `json:"type"`
	}
	for _, f := range fields {
		if f.Key == "transport" {
			_ = json.Unmarshal(f.Value, &transport)
		}
	}
	return transport.Type
}

// packageName names a package a description lists, by its registry and its
// identifier.
func packageName(fields []importer.ObjectEntry) string {
	var registryType, identifier string
	for _, f := range fields {
		switch f.Key {
		case "registryType":
			_ = json.Unmarshal(f.Value, &registryType)
		case "identifier":
			_ = json.Unmarshal(f.Value, &identifier)
		}
	}
	return "the " + commentText(registryType) + " package " + commentText(identifier)
}

// remoteName names a remote a description lists, by its transport and never by
// its address, which can carry a key.
func remoteName(fields []importer.ObjectEntry) string {
	var typ string
	for _, f := range fields {
		if f.Key == "type" {
			_ = json.Unmarshal(f.Value, &typ)
		}
	}
	return "a " + commentText(typ) + " remote"
}

// addressItem returns the address a server is reached at as infra.endpoint
// takes it — without any part that carries a key: the user part, the query and
// the fragment are left out — and notes on the server what it leaves out.
func addressItem(s *server, url string) *item {
	address, dropped := endpointOf(url)
	if len(dropped) > 0 {
		s.notes = append(s.notes, "The address's "+strings.Join(dropped, " and ")+" is not written: infra.endpoint "+
			"holds the address without any part that carries a key, and a consumer reading its key from the address "+
			"supplies the whole address on its own connection to the tool.")
	}
	if address == "" {
		return nil
	}
	v := valueItem(address, importer.CredentialServerMember)
	return &v
}

// endpointOf returns url without its user part, its query and its fragment, and
// names each part it left out, in that order.
func endpointOf(url string) (string, []string) {
	var dropped []string
	if at := strings.Index(url, "://"); at >= 0 {
		authority := url[at+3:]
		end := strings.IndexAny(authority, "/?#")
		if end < 0 {
			end = len(authority)
		}
		if user := strings.LastIndex(authority[:end], "@"); user >= 0 {
			url = url[:at+3] + authority[user+1:]
			dropped = append(dropped, "user part")
		}
	}
	if q := strings.IndexByte(url, '?'); q >= 0 {
		fragment := strings.IndexByte(url, '#')
		url = url[:q]
		dropped = append(dropped, "query")
		if fragment > q {
			dropped = append(dropped, "fragment")
		}
	} else if f := strings.IndexByte(url, '#'); f >= 0 {
		url = url[:f]
		dropped = append(dropped, "fragment")
	}
	return url, dropped
}

// valueItem returns the item writing text, of class, marking it a reference
// where it is one.
func valueItem(text string, class importer.CredentialClass) item {
	return item{text: text, reference: isReference(text), class: class}
}

// isReference reports whether text is one variable reference and nothing else —
// ${NAME}, ${env:NAME}, ${input:id}, ${workspaceFolder} and the other forms the
// clients expand — and so holds no value of its own. A reference carrying a
// default, ${NAME:-default}, holds the default, and text holding anything beside
// the reference holds that; neither is a reference here.
func isReference(text string) bool {
	if len(text) < 4 || !strings.HasPrefix(text, "${") || !strings.HasSuffix(text, "}") {
		return false
	}
	inner := text[2 : len(text)-1]
	return !strings.ContainsAny(inner, "${}\n\r") && !strings.Contains(inner, ":-")
}

// scalarText is the drafts' shared reading of a scalar of the parsed tree.
var scalarText = draftyaml.ScalarText

// members returns the members of a JSON object in the order the text writes
// them, each name once: a name written more than once keeps the place it was
// first written at and the value it was last written with, as a JSON reader
// reads it. A value that is not an object has none.
func members(raw json.RawMessage) []importer.ObjectEntry {
	var out []importer.ObjectEntry
	at := map[string]int{}
	for _, e := range importer.ObjectEntries(raw) {
		if i, seen := at[e.Key]; seen {
			out[i].Value = e.Value
			continue
		}
		at[e.Key] = len(out)
		out = append(out, e)
	}
	return out
}

// firstOf returns the first of values that is not empty.
func firstOf(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
