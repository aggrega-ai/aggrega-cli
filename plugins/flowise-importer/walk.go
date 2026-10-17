// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

package main

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/mcpconfig"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/importer"
)

// walkTree lifts the nodes and edges out of the canonical tree of a Flowise
// flow. It reads the already-parsed tree the host returned — it runs no grammar
// of its own. Flowise keeps its graph flat at the top level. An unrecognised
// shape yields no nodes, which the importer surfaces as a diagnostic rather than
// a silent empty import.
func walkTree(tree json.RawMessage) ([]importer.Node, []importer.Edge) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(tree, &top); err != nil {
		return nil, nil
	}
	return importer.NodeList(top["nodes"], liftNode), importer.EdgeList(top["edges"])
}

// liftNode reads one element of a Flowise node list: the node's type from its
// data's name, its credential parameters from its inputParams, and its value
// fields from its inputs, less the inputs another node connects into and the
// credentials. A note pinned to the canvas is not lifted.
func liftNode(id, outerType string, data map[string]json.RawMessage) (importer.Node, bool) {
	if isCanvasNote(outerType) {
		return importer.Node{}, false
	}
	var typ string
	_ = json.Unmarshal(data["name"], &typ)
	secrets := credentialParams(data["inputParams"])
	fields := subtract(subtract(orderedKeys(data["inputs"]), anchorNames(data["inputAnchors"])), secrets)
	return importer.Node{ID: id, Type: typ, Fields: fields, Secrets: secrets}, true
}

// isCanvasNote reports whether a node's outer type marks it as a note an author
// pinned to the flow's canvas — Flowise's stickyNote. A note documents the flow and
// runs nothing, so it is not lifted as a node: lifting one would draft a component
// for every note. An edge naming a note is then dropped, as is any edge naming a
// node that was not lifted.
func isCanvasNote(outerType string) bool {
	return outerType == "stickyNote"
}

// credentialParams returns the names of a flowise node's credential parameters,
// in the order its inputParams list them. Flowise declares a node's credential as
// an input parameter of type credential, and keeps the credential itself out of
// the node's inputs, so the parameter is the only place the node says it needs
// one. Only each parameter's name and type are read; an entry that does not
// decode is skipped without affecting the others.
func credentialParams(raw json.RawMessage) []string {
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		var param struct {
			Name string `json:"name"`
			Type string `json:"type"`
		}
		if err := json.Unmarshal(entry, &param); err != nil {
			continue
		}
		if param.Type == "credential" && param.Name != "" {
			names = append(names, param.Name)
		}
	}
	return names
}

// anchorNames returns the names a flowise node's inputAnchors declare. An anchor
// is where another node connects in — its entry in the node's inputs holds a
// reference to that node, which the edge between the two already records — so
// an anchor is a connection rather than a parameter field.
func anchorNames(raw json.RawMessage) []string {
	var anchors []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &anchors); err != nil {
		return nil
	}
	names := make([]string, 0, len(anchors))
	for _, a := range anchors {
		if a.Name != "" {
			names = append(names, a.Name)
		}
	}
	return names
}

// orderedKeys returns the keys of a JSON object in the order they appear in the
// canonical tree. The host's JSON parser sorts object keys, so this order is
// deterministic across runs of the same source.
func orderedKeys(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	entries := importer.ObjectEntries(raw)
	if entries == nil {
		return nil
	}
	keys := make([]string, 0, len(entries))
	for _, e := range entries {
		keys = append(keys, e.Key)
	}
	return keys
}

// chatflowUUID is the shape of a chatflow identifier Flowise accepts: a UUID.
var chatflowUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// chatflowID returns the identifier a Flowise export gives its chatflow: the
// string at its top-level id where it is a UUID, the only identifier Flowise
// gives a chatflow, and empty otherwise. An export of the editor's canvas
// carries none.
func chatflowID(tree json.RawMessage) string {
	var top struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(tree, &top); err != nil {
		return ""
	}
	var id string
	if err := json.Unmarshal(top.ID, &id); err != nil || !chatflowUUID.MatchString(id) {
		return ""
	}
	return id
}

// credentialsOf locates every credential value a Flowise flow holds, by the
// marks Flowise itself writes: the credential a node keeps at data.credential —
// the stored credential its parameter of type credential selects; the value in
// data.inputs of each parameter its inputParams declare of type password; the
// credential an agentflow keeps inside a configuration object among its inputs —
// directly, or in a list of them, as an agent keeps each tool's configuration —
// at credential or FLOWISE_CREDENTIAL_ID, that second name also read at the top
// of data.inputs; and the header and environment values, the address, the
// command and the arguments of the server configuration of a Custom MCP tool,
// its mcpServerConfig input, wherever among the inputs it sits. Each is
// addressed from the root of the export, and named by its node's id and the
// field it sits in.
func credentialsOf(tree json.RawMessage) []importer.Credential {
	var top struct {
		Nodes []json.RawMessage `json:"nodes"`
	}
	if err := json.Unmarshal(tree, &top); err != nil {
		return nil
	}
	var out []importer.Credential
	for i, raw := range top.Nodes {
		var node struct {
			ID   string                     `json:"id"`
			Data map[string]json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(raw, &node); err != nil {
			continue
		}
		at := func(tokens ...string) string {
			return importer.Pointer(append([]string{"nodes", strconv.Itoa(i), "data"}, tokens...)...)
		}
		if _, ok := node.Data["credential"]; ok {
			out = append(out, importer.Credential{Node: node.ID, Field: "credential", Pointer: at("credential")})
		}
		inputs := importer.ObjectEntries(node.Data["inputs"])
		password := map[string]bool{}
		for _, name := range passwordParams(node.Data["inputParams"]) {
			password[name] = true
		}
		for _, in := range inputs {
			switch {
			case password[in.Key]:
				out = append(out, importer.Credential{Node: node.ID, Field: in.Key, Pointer: at("inputs", in.Key)})
			case in.Key == credentialIDInput:
				out = append(out, importer.Credential{Node: node.ID, Field: in.Key, Pointer: at("inputs", in.Key)})
			case in.Key == serverConfigInput:
				out = append(out, serverConfigCredentials(node.ID, in.Value, []string{in.Key},
					[]string{"nodes", strconv.Itoa(i), "data", "inputs", in.Key})...)
				continue
			}
			out = append(out, nestedCredentials(node.ID, in.Value, []string{in.Key},
				[]string{"nodes", strconv.Itoa(i), "data", "inputs", in.Key})...)
		}
	}
	return out
}

// nestedCredentials locates the credentials inside one of a node's inputs: at
// credential or FLOWISE_CREDENTIAL_ID in a configuration object, and in the
// server configuration of a Custom MCP tool, at any depth of the objects and
// lists the input holds. field is the input's name and the members on the way,
// and tokens the reference tokens addressing the input from the root of the
// export.
func nestedCredentials(nodeID string, raw json.RawMessage, field, tokens []string) []importer.Credential {
	var out []importer.Credential
	if entries := importer.ObjectEntries(raw); entries != nil {
		for _, e := range entries {
			f := append(append([]string{}, field...), e.Key)
			t := append(append([]string{}, tokens...), e.Key)
			switch e.Key {
			case "credential", credentialIDInput:
				out = append(out, importer.Credential{Node: nodeID, Field: strings.Join(f, "."), Pointer: importer.Pointer(t...)})
			case serverConfigInput:
				out = append(out, serverConfigCredentials(nodeID, e.Value, f, t)...)
			default:
				out = append(out, nestedCredentials(nodeID, e.Value, f, t)...)
			}
		}
		return out
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}
	for j, item := range items {
		index := strconv.Itoa(j)
		out = append(out, nestedCredentials(nodeID, item, append(append([]string{}, field...), index),
			append(append([]string{}, tokens...), index))...)
	}
	return out
}

// serverConfigInput is the input Flowise's Custom MCP tool keeps its server
// configuration in: JSON text, or an object, holding the server's url and the
// headers sent to it, or the command starting it, the arguments and the
// environment it is started with.
const serverConfigInput = "mcpServerConfig"

// serverConfigCredentials locates what a Custom MCP tool's server configuration
// holds of a request's credentials, the server's address and how it is started
// — every header value, every environment value, the url, the command and each
// argument — and returns one credential carrying the configuration with those
// values written empty and every other part kept, or none where the
// configuration holds none of them. A configuration written as text that does
// not read as a JSON object, which Flowise also accepts in the looser forms its
// own reader repairs, is emptied whole, since what is in it cannot be located.
func serverConfigCredentials(nodeID string, raw json.RawMessage, field, tokens []string) []importer.Credential {
	name := strings.Join(field, ".")
	pointer := importer.Pointer(tokens...)
	var text string
	written := raw
	if json.Unmarshal(raw, &text) == nil {
		if strings.TrimSpace(text) == "" {
			return nil
		}
		written = json.RawMessage(text)
	}
	var config map[string]json.RawMessage
	if json.Unmarshal(written, &config) != nil || config == nil {
		return []importer.Credential{{Node: nodeID, Field: name, Pointer: pointer,
			Detail: "it does not read as a JSON object, so nothing inside it can be located"}}
	}
	edits, what := serverConfigEdits(written)
	if len(edits) == 0 {
		return nil
	}
	rewritten, err := importer.Rewrite(written, edits)
	if err != nil {
		return []importer.Credential{{Node: nodeID, Field: name, Pointer: pointer,
			Detail: "what is inside it cannot be located in its text"}}
	}
	value := json.RawMessage(rewritten)
	if text != "" {
		value = jsonText(string(rewritten))
	}
	return []importer.Credential{{Node: nodeID, Field: name, Pointer: pointer, Value: value, Detail: what}}
}

// serverConfigEdits returns, for a server configuration read as a JSON object,
// the edits writing empty each header value, each environment value, the url,
// the command and each argument, as mcpconfig reads such a configuration, and
// says which of them it found, always in that order. A command or an argument
// already empty, or null, is left as written. The command and args members
// stay, their values written empty, so the configuration still reads as
// starting a server of its own.
func serverConfigEdits(config json.RawMessage) ([]importer.Edit, string) {
	empty := json.RawMessage(`""`)
	var edits []importer.Edit
	present := map[mcpconfig.Kind]bool{}
	for _, m := range mcpconfig.Members(config) {
		if (m.Kind == mcpconfig.Command || m.Kind == mcpconfig.Argument) && mcpconfig.Blank(m.Value) {
			continue
		}
		edits = append(edits, importer.Edit{Pointer: importer.Pointer(m.Tokens...), Value: empty})
		present[m.Kind] = true
	}
	var found []string
	for _, f := range []struct {
		kind mcpconfig.Kind
		what string
	}{{mcpconfig.Header, "header values"}, {mcpconfig.Env, "environment values"}, {mcpconfig.Address, "server address"},
		{mcpconfig.Command, "command"}, {mcpconfig.Argument, "arguments"}} {
		if present[f.kind] {
			found = append(found, f.what)
		}
	}
	switch len(found) {
	case 0:
		return edits, ""
	case 1:
		return edits, found[0]
	default:
		return edits, strings.Join(found[:len(found)-1], ", ") + " and " + found[len(found)-1]
	}
}

// jsonText writes s as a JSON string, its characters as they are wherever JSON
// allows it.
func jsonText(s string) json.RawMessage {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return json.RawMessage(strings.TrimSuffix(b.String(), "\n"))
}

// credentialIDInput is the name Flowise keeps a selected credential's
// identifier under among a node's inputs, or inside a configuration object
// among them.
const credentialIDInput = "FLOWISE_CREDENTIAL_ID"

// passwordParams returns the names of a flowise node's input parameters of type
// password, in the order its inputParams list them.
func passwordParams(raw json.RawMessage) []string {
	var entries []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.Type == "password" && e.Name != "" {
			names = append(names, e.Name)
		}
	}
	return names
}

// subtract returns the elements of a not present in b, preserving a's order.
func subtract(a, b []string) []string {
	drop := make(map[string]struct{}, len(b))
	for _, s := range b {
		drop[s] = struct{}{}
	}
	var out []string
	for _, s := range a {
		if _, ok := drop[s]; !ok {
			out = append(out, s)
		}
	}
	return out
}
