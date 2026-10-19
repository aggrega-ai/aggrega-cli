// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package agentframeworkimport

import (
	"encoding/json"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/draftyaml"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/mcpimport"
)

// agent is one declarative agent a source declares, as the draft writes it.
type agent struct {
	// file is the source the agent is read from, empty for a source handed with no
	// name.
	file string
	// name is the agent's name as declared; ref names it in a report, and key is
	// its instance's key in the drafts.
	name, ref, key string
	// displayName and description are the agent's, nil where it states none.
	displayName, description *string
	// model is the model the agent declares; part is the model part its instance
	// is wired to, nil where none.
	model model
	part  *modelPart
	// tools are the agent's tools, in its order.
	tools []*tool
	// applications are the members of the declaration the application reads from
	// it and the draft writes nowhere, by name in sorted order.
	applications []string
	// stated are the values the declaration states that the Release names for
	// review, in the order the draft writes them.
	stated []statedValue
}

// model is the model a declarative agent declares: its id, its provider and the
// API its client sends requests to, and the connection it is reached on.
type model struct {
	// present is set where the agent declares a model.
	present               bool
	id, provider, apiType text
	// connection is the model's connection; options are the names of the options
	// of its requests, in sorted order.
	connection connection
	options    []string
}

// connection is a connection a declarative agent states for its model or for a
// tool: its kind, lowercased, with apikey read as key, as Agent Framework reads
// it; the address it reaches; the key it presents, under the member it is
// written under; and the name of a connection the application resolves.
type connection struct {
	kind     string
	endpoint text
	key      text
	keyField string
	name     text
}

// tool is one entry of a declarative agent's tools.
type tool struct {
	// kind is the tool's kind as declared, and name its name.
	kind, name string
	// description is the tool's, nil where it states none.
	description *string
	// url is the address of an MCP server, for a tool of kind mcp.
	url text
	// connection is the connection the agent reaches the tool on.
	connection connection
	// approval is the tool's approval mode as declared, and allowed the tools of
	// its server the agent may call, for a tool of kind mcp.
	approval string
	allowed  []string
	// key is the instance's key for a tool of kind mcp, which the draft drafts as
	// an instance of the MCP tool; ref names it in a report.
	key, ref string
	// endpoint is the address the instance is reached at, as the draft writes it,
	// and parts the parts of the url it leaves out, in the order the url writes
	// them; left says why no address is written, empty where one is.
	endpoint *string
	parts    []mcpimport.AddressPart
	left     string
}

// mcp reports whether the tool is an MCP server the agent reaches.
func (t *tool) mcp() bool {
	return t.kind == mcpKind
}

// text is one text a declarative agent states: written as a literal, as a
// reference to an environment variable, or as an expression the application
// computes.
type text struct {
	// value is the literal, empty where none is stated.
	value string
	// present is set where the declaration states the member as text.
	present bool
	// variable is the environment variable a reference reads, empty for anything
	// else; expression is set for an expression other than a reference.
	variable   string
	expression bool
}

// literal reports whether the text is a literal holding something.
func (t text) literal() bool {
	return t.present && t.variable == "" && !t.expression && t.value != ""
}

// statedValue is one value a declaration states that the draft names in the
// Release, for review: its field, the text, and what the draft says of it.
type statedValue struct {
	field string
	text  text
	// marked is set for a value the format marks as a credential.
	marked bool
	// emptied is set where the credential decision wrote the value empty.
	emptied bool
}

// environmentReference is Agent Framework's reference to an environment
// variable: an expression, opening with =, that is Env. followed by the
// variable's name.
var environmentReference = regexp.MustCompile(`^=\s*Env\.([A-Za-z_][A-Za-z0-9_]*)\s*$`)

// textOf reads one member of a declaration as text: a string as written, and a
// number or a boolean as written; a string opening with = is an expression,
// one naming an environment variable a reference to it. Anything else states no
// text.
func textOf(raw json.RawMessage) text {
	var s string
	if raw != nil && json.Unmarshal(raw, &s) == nil {
		if strings.HasPrefix(s, "=") {
			if m := environmentReference.FindStringSubmatch(s); m != nil {
				return text{present: true, variable: m[1]}
			}
			return text{present: true, expression: true}
		}
		return text{present: true, value: s}
	}
	if v, ok := draftyaml.ScalarText(raw); ok {
		return text{present: true, value: v}
	}
	return text{}
}

// isPromptAgent reports whether kind declares an agent Agent Framework builds as
// a prompt agent: Prompt in any letter case, or Agent as written, the two
// spellings its loader builds one from.
func isPromptAgent(kind string) bool {
	return strings.EqualFold(kind, Kind) || kind == "Agent"
}

// readAgent reads one source's tree as a declarative agent: nil where it is not
// an object declaring a prompt agent.
func readAgent(file string, tree json.RawMessage) *agent {
	fields := objectOf(tree)
	if fields == nil {
		return nil
	}
	kind, _ := stringOf(fields["kind"])
	if !isPromptAgent(kind) {
		return nil
	}
	a := &agent{file: file}
	a.name, _ = stringOf(fields["name"])
	if s, ok := stringOf(fields["displayName"]); ok && s != "" {
		a.displayName = &s
	}
	if s, ok := stringOf(fields["description"]); ok && s != "" {
		a.description = &s
	}
	a.model = readModel(objectOf(fields["model"]))
	for _, item := range listOf(fields["tools"]) {
		a.tools = append(a.tools, readTool(objectOf(item)))
	}
	for _, key := range sortedKeys(fields) {
		if slices.Contains(applicationMembers, key) {
			a.applications = append(a.applications, key)
		}
	}
	return a
}

// applicationMembers are the members of a declarative agent the application
// reads from it and no key of Component takes.
var applicationMembers = []string{"additionalInstructions", "inputSchema", "instructions", "metadata",
	"outputSchema", "template"}

// readModel reads a declarative agent's model.
func readModel(fields map[string]json.RawMessage) model {
	if fields == nil {
		return model{}
	}
	m := model{present: true}
	m.id = textOf(fields["id"])
	m.provider = textOf(fields["provider"])
	m.apiType = textOf(fields["apiType"])
	m.connection = readConnection(objectOf(fields["connection"]))
	m.options = sortedKeys(objectOf(fields["options"]))
	return m
}

// readConnection reads a connection, as Agent Framework reads one: its kind,
// lowercased, with apikey read as key; its endpoint; for a key connection the
// key it presents, written under key or under apiKey, key read where both are;
// and for a remote or a reference connection the name of the connection the
// application resolves.
func readConnection(fields map[string]json.RawMessage) connection {
	if fields == nil {
		return connection{}
	}
	var c connection
	kind, _ := stringOf(fields["kind"])
	c.kind = strings.ToLower(kind)
	if c.kind == "apikey" {
		c.kind = keyConnection
	}
	c.endpoint = textOf(fields["endpoint"])
	if c.kind == keyConnection {
		for _, field := range []string{"key", "apiKey"} {
			if t := textOf(fields[field]); t.present && (t.value != "" || t.variable != "" || t.expression) {
				c.key, c.keyField = t, field
				break
			}
		}
	}
	c.name = textOf(fields["name"])
	return c
}

// readTool reads one entry of a declarative agent's tools.
func readTool(fields map[string]json.RawMessage) *tool {
	t := &tool{}
	if fields == nil {
		return t
	}
	t.kind, _ = stringOf(fields["kind"])
	t.name, _ = stringOf(fields["name"])
	if s, ok := stringOf(fields["description"]); ok && s != "" {
		t.description = &s
	}
	if !t.mcp() {
		return t
	}
	t.url = textOf(fields["url"])
	t.connection = readConnection(objectOf(fields["connection"]))
	switch raw := fields["approvalMode"]; {
	case objectOf(raw) != nil:
		t.approval, _ = stringOf(objectOf(raw)["kind"])
	default:
		t.approval, _ = stringOf(raw)
	}
	for _, item := range listOf(fields["allowedTools"]) {
		if s, ok := stringOf(item); ok {
			t.allowed = append(t.allowed, s)
		}
	}
	return t
}

// nameOf is the name an agent is keyed and reported by: its name, or, where it
// states none, the name of its file without its extension.
func nameOf(a *agent) string {
	if a.name != "" {
		return a.name
	}
	if a.file == "" {
		return "agent"
	}
	base := path.Base(a.file)
	return strings.TrimSuffix(base, path.Ext(base))
}

// stringOf returns a string member's text.
func stringOf(raw json.RawMessage) (string, bool) {
	var s string
	if raw == nil || json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

// objectOf returns the members of an object, or nil for anything else.
func objectOf(raw json.RawMessage) map[string]json.RawMessage {
	var fields map[string]json.RawMessage
	if raw == nil || json.Unmarshal(raw, &fields) != nil {
		return nil
	}
	return fields
}

// listOf returns the items of a list, or nil for anything else.
func listOf(raw json.RawMessage) []json.RawMessage {
	var items []json.RawMessage
	if raw == nil || json.Unmarshal(raw, &items) != nil {
		return nil
	}
	return items
}

// sortedKeys returns the keys of m in sorted order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
