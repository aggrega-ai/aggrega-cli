// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

package main

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/mcpconfig"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/importer"
)

// credentialsOf locates every value an n8n workflow holds that can carry a
// credential, each of its class, every class every time:
//
//   - marked: the identifier of each stored credential a node selects, at
//     credentials.<type>.id — the name beside it is not located, since n8n
//     importing the workflow on another instance resolves a credential it does
//     not hold by its type and name — and each parameter n8n marks as a
//     password, wherever among the node's parameters it sits, as passwordParams
//     names them;
//   - an MCP server's members: the address of the MCP server an MCP client node
//     calls, which can carry a key;
//   - unmarked: the values of the headers and the query parameters a node sends
//     with a request the workflow composes, as requestInputs names them, a
//     header's or a parameter's name not located, since a name carries no
//     secret.
//
// A credential selected by its name alone, the form older exports write, names a
// credential and holds none, and is not located. Each value is addressed from
// the root of the export and named by its node's name and the field it sits in.
// Which of them is written empty is the empty_credentials setting's, which the
// plugin kit reads.
func credentialsOf(tree json.RawMessage) []importer.Credential {
	var out []importer.Credential
	for _, n := range workflowOf(tree).nodes {
		at := func(tokens ...string) []string {
			return append([]string{"nodes", strconv.Itoa(n.position)}, tokens...)
		}
		for _, c := range importer.ObjectEntries(n.credentials) {
			var ref struct {
				ID json.RawMessage `json:"id"`
			}
			if json.Unmarshal(c.Value, &ref) == nil && ref.ID != nil {
				out = append(out, importer.Credential{Node: n.name, Field: "credentials." + c.Key + ".id",
					Pointer: importer.Pointer(at("credentials", c.Key, "id")...), Class: importer.CredentialMarked})
			}
		}
		password := passwordParams(n.typ)
		if len(password) > 0 {
			out = append(out, passwordValues(n.name, n.parameters, password, []string{"parameters"},
				at("parameters"))...)
		}
		if mcpClients[n.typ] {
			for _, e := range importer.ObjectEntries(n.parameters) {
				if e.Key == "endpointUrl" || e.Key == "sseEndpoint" {
					out = append(out, importer.Credential{Node: n.name, Field: "parameters." + e.Key,
						Pointer: importer.Pointer(at("parameters", e.Key)...), Detail: serverAddressDetail,
						Class: importer.CredentialServerMember})
				}
			}
		}
		for _, c := range requestValues(n, at) {
			c.Class = importer.CredentialUnmarked
			out = append(out, c)
		}
	}
	return out
}

// serverAddressDetail says why the address an MCP client node calls is written
// empty.
const serverAddressDetail = "it is the address of an MCP server, which can carry a key"

// mcpClients are the n8n node types that call an MCP server at an address their
// parameters hold: the MCP client tool an agent calls, the MCP client node, and
// the client of a server n8n's registry lists.
var mcpClients = map[string]bool{
	aiPackage + mcpClientTool:           true,
	aiPackage + "mcpClient":             true,
	aiPackage + "mcpRegistryClientTool": true,
}

// mcpClientTool is the name of n8n's MCP client tool node.
const mcpClientTool = "mcpClientTool"

// passwordValues locates, at any depth of the parameters raw holds, every
// member whose name password lists and whose value is a text holding something:
// field names the member and the members on the way, and tokens address it from
// the root of the export.
func passwordValues(node string, raw json.RawMessage, password map[string]bool, field, tokens []string) []importer.Credential {
	var out []importer.Credential
	if entries := importer.ObjectEntries(raw); entries != nil {
		for _, e := range entries {
			f := append(append([]string{}, field...), e.Key)
			t := append(append([]string{}, tokens...), e.Key)
			var text string
			if password[e.Key] && json.Unmarshal(e.Value, &text) == nil {
				if text != "" {
					out = append(out, importer.Credential{Node: node, Field: strings.Join(f, "."),
						Pointer: importer.Pointer(t...), Detail: passwordDetail, Class: importer.CredentialMarked})
				}
				continue
			}
			out = append(out, passwordValues(node, e.Value, password, f, t)...)
		}
		return out
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}
	for j, item := range items {
		index := strconv.Itoa(j)
		out = append(out, passwordValues(node, item, password, append(append([]string{}, field...), index),
			append(append([]string{}, tokens...), index))...)
	}
	return out
}

// passwordDetail says why a parameter n8n marks as a password is written empty.
const passwordDetail = "n8n marks it as a password"

// passwordParams returns the names of the parameters n8n marks as passwords on a
// node of type typ, nil where it marks none.
func passwordParams(typ string) map[string]bool {
	pkg, name := baseName(typ)
	if pkg != basePackage {
		return nil
	}
	return passwordParameters[name]
}

// passwordParameters are, by the name of an n8n base node, the parameters its
// definition marks as passwords — typeOptions.password — wherever among its
// parameters they sit, read off n8n's own node definitions. n8n's AI nodes mark
// none; a node used as an agent's tool takes its base node's parameters.
var passwordParameters = map[string]map[string]bool{
	"box":                        {"password": true},
	"ciscoWebex":                 {"password": true},
	"citrixAdc":                  {"password": true},
	"crypto":                     {"secret": true, "privateKey": true},
	"databricks":                 {"pageToken": true},
	"discourse":                  {"password": true},
	"elasticSecurity":            {"apiToken": true, "password": true, "apiKeyId": true, "apiKeySecret": true},
	"extractFromFile":            {"password": true},
	"gSuiteAdmin":                {"password": true},
	"haloPSA":                    {"password": true},
	"jira":                       {"password": true},
	"jwt":                        {"token": true},
	"magento2":                   {"password": true},
	"mattermost":                 {"password": true},
	"metabase":                   {"password": true},
	"microsoftEntra":             {"password": true},
	"nextCloud":                  {"password": true},
	"okta":                       {"password": true},
	"readPDF":                    {"password": true},
	"serviceNow":                 {"user_password": true},
	"splunk":                     {"password": true},
	"stripe":                     {"token": true},
	"uptimeRobot":                {"password": true, "http_password": true},
	"venafiTlsProtectDatacenter": {"password": true, "KeystorePassword": true},
	"wooCommerce":                {"password": true},
	"wordpress":                  {"password": true},
	"zoom":                       {"password": true},
	"zulip":                      {"password": true},
	"awsCognito":                 {"temporaryPasswordOptions": true},
}

// requestInput is one parameter of a node holding the headers or the query
// parameters of a request the workflow composes: its name, and how its values
// are held — as rows of a name and a value inside a list named rows, or as JSON
// text.
type requestInput struct {
	param  string
	rows   string
	text   bool
	member string
}

// requestInputs are, by node type, the parameters holding the headers and the
// query parameters of a request the workflow composes, read off n8n's own node
// definitions: the HTTP Request node in its current and its earlier versions —
// and so the same node used as an agent's tool — the AI HTTP request tool, the
// GraphQL node's headers and the Facebook Graph API node's query parameters,
// which it keeps among its options. n8n marks none of them as a credential, and a
// request's authorization can be written in any of them.
var requestInputs = map[string][]requestInput{
	basePackage + "httpRequest": {
		{param: "headerParameters", rows: "parameters", member: "header"},
		{param: "queryParameters", rows: "parameters", member: "query parameter"},
		{param: "jsonHeaders", text: true, member: "header"},
		{param: "jsonQuery", text: true, member: "query parameter"},
		{param: "headerParametersUi", rows: "parameter", member: "header"},
		{param: "queryParametersUi", rows: "parameter", member: "query parameter"},
		{param: "headerParametersJson", text: true, member: "header"},
		{param: "queryParametersJson", text: true, member: "query parameter"},
	},
	aiPackage + "toolHttpRequest": {
		{param: "parametersHeaders", rows: "values", member: "header"},
		{param: "parametersQuery", rows: "values", member: "query parameter"},
		{param: "jsonHeaders", text: true, member: "header"},
		{param: "jsonQuery", text: true, member: "query parameter"},
	},
	basePackage + "graphql": {
		{param: "headerParametersUi", rows: "parameter", member: "header"},
	},
	basePackage + "facebookGraphApi": {
		{param: "options.queryParameters", rows: "parameter", member: "query parameter"},
		{param: "options.queryParametersJson", text: true, member: "query parameter"},
	},
}

// requestInputsOf returns the request inputs of a node of type typ: those of its
// type, or, for a base node used as an agent's tool, those of the base node.
func requestInputsOf(typ string) []requestInput {
	if inputs, ok := requestInputs[typ]; ok {
		return inputs
	}
	pkg, name := baseName(typ)
	return requestInputs[pkg+name]
}

// requestValues locates, among a node's parameters, the value of every header
// and query parameter requestInputs names for its type: in a list of rows, each
// row's value, named by the row's name; held as JSON text reading as an object,
// the text written anew with every value emptied and each name kept, reported
// once with the names it holds; held as any other text — an expression n8n
// evaluates when the workflow runs, say — the whole text.
func requestValues(n workflowNode, at func(tokens ...string) []string) []importer.Credential {
	var out []importer.Credential
	for _, in := range requestInputsOf(n.typ) {
		path := strings.Split(in.param, ".")
		raw := memberAt(n.parameters, path)
		if raw == nil {
			continue
		}
		field := "parameters." + in.param
		tokens := at(append([]string{"parameters"}, path...)...)
		if !in.text {
			out = append(out, rowValues(n.name, raw, in, field, tokens)...)
			continue
		}
		if c, ok := textValues(n.name, raw, in, field, tokens); ok {
			out = append(out, c)
		}
	}
	return out
}

// rowValues locates the value of each row of a list of rows of a name and a
// value, held at rows inside raw: each row whose value holds something, named by
// the row's name.
func rowValues(node string, raw json.RawMessage, in requestInput, field string, tokens []string) []importer.Credential {
	var rows []json.RawMessage
	if json.Unmarshal(memberAt(raw, []string{in.rows}), &rows) != nil {
		return nil
	}
	var out []importer.Credential
	for j, row := range rows {
		var r struct {
			Name  json.RawMessage `json:"name"`
			Value json.RawMessage `json:"value"`
		}
		if json.Unmarshal(row, &r) != nil || r.Value == nil || mcpconfig.Blank(r.Value) {
			continue
		}
		name := string(r.Name)
		var s string
		if json.Unmarshal(r.Name, &s) == nil {
			name = s
		}
		out = append(out, importer.Credential{Node: node, Field: field + "." + name,
			Pointer: importer.Pointer(append(append([]string{}, tokens...), in.rows, strconv.Itoa(j), "value")...),
			Detail:  unmarkedDetail(in.member)})
	}
	return out
}

// textValues locates what JSON text holding headers or query parameters holds:
// an object of names to values is written anew with every value that holds
// something emptied and every name kept; any other text — an expression, or
// text that does not read as such an object — is written empty whole. It
// reports false where the text holds nothing to write empty.
func textValues(node string, raw json.RawMessage, in requestInput, field string, tokens []string) (importer.Credential, bool) {
	pointer := importer.Pointer(tokens...)
	var text string
	if json.Unmarshal(raw, &text) != nil || strings.TrimSpace(text) == "" {
		return importer.Credential{}, false
	}
	var object map[string]json.RawMessage
	if strings.HasPrefix(text, "=") || json.Unmarshal([]byte(text), &object) != nil || object == nil {
		return importer.Credential{Node: node, Field: field, Pointer: pointer,
			Detail: "it does not read as " + in.member + "s, so what is inside it cannot be located, and " +
				unmarkedDetail(in.member)}, true
	}
	var edits []importer.Edit
	var names []string
	for _, m := range mcpconfig.HeaderValues(json.RawMessage(text)) {
		if mcpconfig.Blank(m.Value) {
			continue
		}
		edits = append(edits, importer.Edit{Pointer: importer.Pointer(m.Tokens...), Value: json.RawMessage(`""`)})
		names = append(names, m.Name)
	}
	if len(edits) == 0 {
		return importer.Credential{}, false
	}
	rewritten, err := importer.Rewrite([]byte(text), edits)
	if err != nil {
		return importer.Credential{Node: node, Field: field, Pointer: pointer,
			Detail: "what is inside it cannot be located in its text, and " + unmarkedDetail(in.member)}, true
	}
	return importer.Credential{Node: node, Field: field, Pointer: pointer, Value: jsonText(string(rewritten)),
		Detail: in.member + " values (" + strings.Join(names, ", ") + ")"}, true
}

// unmarkedDetail says why the value of a header or a query parameter is written
// empty: member says which of the two it is.
func unmarkedDetail(member string) string {
	return "a " + member + " can carry a credential, though n8n marks it as none"
}

// memberAt returns the value the members path name, one inside the other, hold
// inside the JSON object raw, nil where one of them is not there.
func memberAt(raw json.RawMessage, path []string) json.RawMessage {
	for _, name := range path {
		var found json.RawMessage
		for _, e := range importer.ObjectEntries(raw) {
			if e.Key == name {
				found = e.Value
				break
			}
		}
		if found == nil {
			return nil
		}
		raw = found
	}
	return raw
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
