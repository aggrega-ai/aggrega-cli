// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package agentframeworkimport is the Agent Framework importer: it reads a
// Microsoft Agent Framework declarative agent — the YAML file an application
// built on Agent Framework loads an agent from — and drafts an Aggregate holding
// one instance of the standard library's Agent Framework agent,
// core/ai-agent/agent-framework, per declarative agent, wired to the model its
// declaration names and to each MCP server it reaches, and a Release beside it
// naming what the Release supplies.
//
// The importer guest (plugins/agent-framework-importer) holds the descriptor,
// the signature a declarative agent is recognised by and the export wiring;
// everything it does with a source is here, so it compiles and is tested on the
// host toolchain as well.
//
// # What a declarative agent declares
//
// A declarative agent is a YAML document of kind Prompt naming the agent (name,
// displayName, description), its instructions and the schema of its output; its
// model — the model's id, the provider serving it, the API its client sends
// requests to (apiType), the options of its requests and the connection it is
// reached on, a key connection presenting a key, a remote connection reaching an
// endpoint, a reference connection naming one the application resolves, or an
// anonymous one presenting nothing; and its tools, each of a kind: a function
// whose code the application binds, a tool the model's provider runs, or an MCP
// server reached at its url, with the approval mode its calls take and the tools
// of the server the agent may call. A member written as =Env.NAME is read by
// Agent Framework from the environment variable NAME, and any other member
// opening with = is an expression the application computes. Agent Framework
// builds a prompt agent from a document whose kind is Prompt, in any letter case,
// or Agent.
//
// The importer reads declarative agents and nothing else. A single file is read
// as one declarative agent, and each file the attachment's with.sources hands is
// read as another. A directory is never handed to the importer: its signature
// names no file, so a directory import naming no format never recognises the
// format, and one naming it is refused, since the importer declares no file of a
// directory to read. The application's code — the program loading each
// declarative agent and the functions it binds to its tools — is never among the
// sources. The application loads its declarative agents itself, from the image
// the Release runs, so the draft carries only what the runtime Component takes.
//
// # What it drafts
//
// Each declarative agent is an instance of core/ai-agent/agent-framework, keyed
// by its name and carrying its displayName and its description. Its
// instructions, the schemas of its input and its output, its template, its
// metadata and the options of its model requests are the application's, read
// from the declarative agent, and are named in a comment above the instance.
//
// A model whose provider is OpenAI and which names an id is wired, on the
// instance's model socket, to an instance of the catalog's OpenAI Component,
// core/ai-model/openai, serving that id: the socket admits a model serving the
// OpenAI API, whose address, model name and key the runtime delivers in the
// variables Agent Framework's OpenAI clients read. Agents naming one model at one
// address share one instance. Where the request's catalog view holds no
// Component of that name, one is drafted under the target namespace,
// <target_namespace>/ai-model/openai, and its decision says so. An id written as
// a reference leaves the model's name for the author, naming the variable; an
// endpoint the connection states is written as the model instance's
// infra.endpoint in the Release, and one written as a reference is named beside
// it. A model of any other provider, one naming its provider as a reference or
// none — Agent Framework then uses the default provider the application sets —
// and one naming no id is wired to nothing, and the draft says why. An anonymous
// connection is written as the connection's unauthenticated value.
//
// Each tool of kind mcp is drafted as the MCP importer drafts a server someone
// else runs: an instance of core/ai-tool/mcp, keyed by the tool's name and
// carrying its description, whose Release entry holds its url as infra.endpoint
// without any part that carries a key — each part it leaves out, the url's user
// part, its query and its fragment, written in the comment above the entry —
// wired to the agent's instance on its tools socket. Its approval mode and the
// tools it allows are the application's,
// named in a comment above it. A tool of any other kind is reported in a decision
// choosing nothing.
//
// The Release, release.yaml beside aggregate.yaml, names the Aggregate and
// leaves for the Release what no declarative agent states: each instance's image,
// which runs the application and holds its declarative agents, and the
// credential each presents to its model and to each MCP server whose connection
// presents a key. The Release's environment and its Manifest are left unset, each
// named in a comment, since no source names either and the import drafts no
// Manifest.
//
// # Credential values
//
// A key connection's key — written under key or apiKey, of the model or of an
// MCP server — is the one member Agent Framework marks as a credential: its
// loader reads it alone with its value kept out of its own logs. Each one a
// declarative agent writes as a literal is written into a comment above the
// agent's entry of the Release, for the author to attach where the Component
// takes it, and handed to the plugin kit's credential decision,
// importer.CarryCredentials, as a value its format marks as a credential; an MCP
// server's url, written as its instance's infra.endpoint, and each part of it
// the endpoint leaves out, as members of an MCP server's configuration; and a
// model's endpoint as an input its format marks as no credential. By default
// each is carried as written and named in a credential-carried warning by its
// agent and its field — a part of a url by the url's field and the part's name —
// never by its value; where the empty_credentials setting asks for its class,
// each is written with no value and reported under credential-emptied, a part of
// a url named alone in the comment. A value written as a
// reference to an environment variable holds none, so it is named by its
// variable whatever the setting says, and a value written as any other
// expression is named as one and never written.
package agentframeworkimport

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/mcpimport"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/importer"
)

// Format is the one format the importer serves: an Agent Framework declarative
// agent.
const Format = "agent-framework"

// Syntax is the syntactic format a declarative agent is parsed as.
const Syntax = "yaml"

// Component is the catalog Component each declarative agent is drafted as an
// instance of.
const Component = "core/ai-agent/agent-framework"

// Kind is the kind a declarative agent states, as the importer's signature
// recognises it.
const Kind = "Prompt"

// mcpKind is the kind of a tool that is an MCP server.
const mcpKind = "mcp"

// keyConnection is the kind of a connection presenting a key.
const keyConnection = "key"

// The provider whose models are wired, the catalog Component serving them, and
// the API that Component's model.aggrega.ai/api label names, which the
// runtime's model socket selects a model by.
const (
	openAIProvider = "OpenAI"
	modelComponent = "core/ai-model/openai"
	modelAPI       = "openai"
)

// The sockets of Component a model and an MCP server are wired to.
const (
	modelSocket = "model"
	toolsSocket = "tools"
)

// where completes each credential report, naming where the value is written.
const where = "in the Release the draft writes"

// emptiedNote says of a value the credential decision wrote empty that it is.
const emptiedNote = "its value left out, as the empty_credentials setting asks"

// modelPart is one model a declarative agent is wired to: an instance of the
// OpenAI Component serving its id.
type modelPart struct {
	// key is the instance's key; ref names it in a report.
	key, ref string
	// component is the Component it is an instance of; drafted is set where that
	// Component is drafted rather than referenced.
	component string
	drafted   bool
	// id is the model's id, and endpointText the endpoint its connection states;
	// endpoint is the endpoint as the draft writes it, nil where it writes none.
	id, endpointText text
	endpoint         *string
	// node names the agent a report on the endpoint names.
	node string
	// agents are the agents wired to it, by ref.
	agents []string
}

// Import turns one import request into the drafts: each source — the request's
// primary bytes, where it carries any, and each named source — is parsed once
// through parse, the host's parser, and read as a declarative agent. A source the
// parser refuses is reported under parse-failed, which halts the import, and a
// source declaring no prompt agent under no-nodes; the import carries on with the
// other sources. A request naming a format other than Format reads nothing.
//
// Where any source declares an agent, it returns the drafted Aggregate and
// Release and any drafted model Component, as files and entities, and a decision
// per agent, model and tool; where the empty_credentials setting holds a value it
// does not take, it returns that halt and drafts nothing. Given one request and
// one parser, it returns byte-identical output.
func Import(in aggrega.ImportInput, parse func(syntax string, source []byte) (json.RawMessage, error)) aggrega.ImportOutput {
	var out aggrega.ImportOutput
	if in.Format != Format {
		return out
	}
	var sources []aggrega.ImportSource
	if len(in.Bytes) > 0 {
		sources = append(sources, aggrega.ImportSource{Bytes: in.Bytes})
	}
	sources = append(sources, in.Sources...)
	var agents []*agent
	for _, src := range sources {
		tree, err := parse(Syntax, src.Bytes)
		if err != nil {
			out.Diagnostics = append(out.Diagnostics, importer.ParseFailed(src, in.Format, err))
			continue
		}
		a := readAgent(src.Name, tree)
		if a == nil {
			out.Diagnostics = append(out.Diagnostics, importer.NoNodes(src, in.Format))
			continue
		}
		agents = append(agents, a)
	}
	if len(agents) == 0 {
		return out
	}

	used := map[string]bool{}
	for _, a := range agents {
		a.ref = nameOf(a)
		if len(agents) > 1 && a.file != "" {
			a.ref = a.file + "#" + nameOf(a)
		}
		a.key = importer.UniqueKey(used, nameOf(a))
	}
	parts := wire(in, agents, used)
	for _, a := range agents {
		for _, t := range a.tools {
			if !t.mcp() {
				continue
			}
			t.ref = a.ref + "#" + firstOf(t.name, mcpKind)
			t.key = importer.UniqueKey(used, firstOf(t.name, mcpKind))
			address(t)
		}
		a.stated = statedValues(a)
	}

	written, credentialDiags, err := importer.CarryCredentials(in.Settings, draftedValues(agents, parts), where,
		located(agents, parts))
	if err != nil {
		out.Error = "agent-framework importer: write the drafted values: " + err.Error()
		return out
	}
	out.Diagnostics = append(out.Diagnostics, credentialDiags...)
	if written == nil {
		return out
	}
	if err := readBack(agents, parts, written); err != nil {
		out.Error = "agent-framework importer: read the drafted values back: " + err.Error()
		return out
	}

	out.Decisions = decisions(agents)
	namespace := importer.TargetNamespace(in.Settings)
	var entities []aggrega.EmittedEntity
	for _, a := range agents {
		entities = append(entities, aggrega.EmittedEntity{Kind: "Component", Name: Component, Mode: "reference",
			SourceRef: a.ref, DecisionRef: "decision:" + a.ref})
	}
	drafted := false
	for _, p := range parts {
		mode, body := "reference", ""
		if p.drafted {
			if drafted {
				continue
			}
			drafted = true
			mode, body = "draft", renderModel(p.component)
		}
		entities = append(entities, aggrega.EmittedEntity{Kind: "Component", Name: p.component, Mode: mode, Body: body,
			SourceRef: p.ref, DecisionRef: "decision:" + p.ref})
	}
	for _, a := range agents {
		for _, t := range a.tools {
			if t.mcp() {
				entities = append(entities, aggrega.EmittedEntity{Kind: "Component", Name: mcpimport.Component,
					Mode: "reference", SourceRef: t.ref, DecisionRef: "decision:" + t.ref})
			}
		}
	}

	name := namespace + "/flow"
	aggregate := renderAggregate(name, agents, parts)
	release := renderRelease(name, agents, parts)
	out.Entities = append(entities,
		aggrega.EmittedEntity{Kind: "Aggregate", Name: name, Mode: "draft", Body: aggregate},
		aggrega.EmittedEntity{Kind: "Release", Name: name, Mode: "draft", Body: release})
	out.Files = importer.Render(in.Settings, out.Entities, out.Decisions)
	if slices.ContainsFunc(out.Files, func(f aggrega.ScaffoldFile) bool { return f.Path == "aggregate.yaml" }) {
		out.Files = append(out.Files, aggrega.ScaffoldFile{Path: "release.yaml", Content: release})
	}
	return out
}

// wire makes the model part each agent whose model is wired is wired to, keyed
// after every agent: one part per model id and endpoint, shared by every agent
// naming both. A part's Component is the catalog's OpenAI Component where the
// request's catalog view holds it, and a Component drafted under the target
// namespace otherwise.
func wire(in aggrega.ImportInput, agents []*agent, used map[string]bool) []*modelPart {
	held := false
	if in.Catalog != nil {
		held = slices.ContainsFunc(in.Catalog.Components, func(c aggrega.CatalogComponent) bool {
			return c.Name == modelComponent
		})
	}
	component := modelComponent
	if !held {
		component = importer.TargetNamespace(in.Settings) + "/ai-model/" + modelAPI
	}
	var parts []*modelPart
	shared := map[string]*modelPart{}
	refs := map[string]bool{}
	for _, a := range agents {
		if unwiredWhy(a) != "" {
			continue
		}
		m := a.model
		id := textKey(m.id) + "\x00" + textKey(m.connection.endpoint)
		p := shared[id]
		if p == nil {
			p = &modelPart{ref: modelRef(m.id), component: component, drafted: !held, id: m.id,
				endpointText: m.connection.endpoint, node: a.ref, key: importer.UniqueKey(used, modelName(m.id))}
			if refs[p.ref] {
				// One id served at two addresses is two parts, told apart in a report by their keys.
				p.ref += "#" + p.key
			}
			refs[p.ref] = true
			if m.connection.endpoint.literal() {
				e := m.connection.endpoint.value
				p.endpoint = &e
			}
			shared[id] = p
			parts = append(parts, p)
		}
		p.agents = append(p.agents, a.ref)
		a.part = p
	}
	return parts
}

// textKey writes a text so two texts stating the same thing write the same key.
func textKey(t text) string {
	switch {
	case t.variable != "":
		return "=Env." + t.variable
	case t.expression:
		return "="
	}
	return t.value
}

// modelName is the name a model part is keyed by: its id, or, for an id written
// as a reference, the variable it reads.
func modelName(id text) string {
	switch {
	case id.variable != "":
		return id.variable
	case id.expression:
		return modelAPI + "-model"
	}
	return id.value
}

// modelRef names a model part in a report: its id, or the reference it is read
// from.
func modelRef(id text) string {
	switch {
	case id.variable != "":
		return "=Env." + id.variable
	case id.expression:
		return "the model the application computes"
	}
	return id.value
}

// unwiredWhy says why an agent's model is wired to nothing, in words following
// "since", empty where it is wired. A text read from the source is written
// through the comment writer, since the Aggregate writes the reason into a
// comment.
func unwiredWhy(a *agent) string {
	m := a.model
	switch {
	case !m.present:
		return "the declarative agent declares no model, so the application's code supplies its chat client"
	case m.provider.variable != "":
		return "its model names its provider through the environment variable " + commentText(m.provider.variable) +
			", which the import does not read"
	case m.provider.expression:
		return "its model names its provider through an expression the application computes, which the import " +
			"does not evaluate"
	case !m.provider.present || m.provider.value == "":
		return "its model names no provider, so Agent Framework uses the default provider the application sets, " +
			"which the import does not read"
	case m.provider.value != openAIProvider:
		return "its model's provider is " + commentText(m.provider.value) + ", while the " + modelSocket + " socket of " +
			Component + " admits a model serving the OpenAI API, whose address, model name and key the runtime " +
			"delivers in the variables Agent Framework's OpenAI clients read, so the import wires a model of the " +
			openAIProvider + " provider alone"
	case !m.id.present || (m.id.variable == "" && !m.id.expression && m.id.value == ""):
		return "its model names no id, so the application's code supplies its chat client"
	}
	return ""
}

// address writes the address an MCP server is reached at as its instance's
// infra.endpoint takes it — as the MCP importer writes a remote server's — and
// keeps the parts of its url it leaves out, each as the url writes it, since a
// literal url holds no reference of the format's inside it; and, where no
// address is written, why.
func address(t *tool) {
	switch {
	case t.url.variable != "":
		t.left = "infra.endpoint, which the declarative agent reads from the environment variable " + t.url.variable
		return
	case t.url.expression:
		t.left = "infra.endpoint, which the declarative agent computes with an expression the import does not evaluate"
		return
	case !t.url.literal():
		t.left = "infra.endpoint, since the tool names no url"
		return
	}
	endpoint, parts := mcpimport.RemoteEndpoint(t.url.value)
	t.parts = parts
	if endpoint == "" {
		t.left = "infra.endpoint, since the url holds no address"
		return
	}
	t.endpoint = &endpoint
}

// statedValues returns the values an agent's declaration states for its
// connections that the draft names in the Release for review, in the order it
// writes them: its model's endpoint where its model is wired to nothing, its
// model's key, then each MCP server's key, in the order it lists its tools.
func statedValues(a *agent) []statedValue {
	var out []statedValue
	c := a.model.connection
	if a.part == nil && c.endpoint.present {
		out = append(out, statedValue{field: "model.connection.endpoint", text: c.endpoint})
	}
	if c.keyField != "" {
		out = append(out, statedValue{field: "model.connection." + c.keyField, text: c.key, marked: true})
	}
	for _, t := range a.tools {
		if t.mcp() && t.connection.keyField != "" {
			out = append(out, statedValue{field: "tools." + firstOf(t.name, mcpKind) + ".connection." +
				t.connection.keyField, text: t.connection.key, marked: true})
		}
	}
	return out
}

// draftedValues writes, as one JSON document, the values the credential decision
// judges: under each agent's key, the text of each value it states, in the order
// the draft writes them, empty for one that is no literal; under each model
// part's and each MCP server's key, the endpoint the draft writes, null where it
// writes none, and under each MCP server's key the text of each part of its url
// the endpoint leaves out, in the order the url writes them.
func draftedValues(agents []*agent, parts []*modelPart) []byte {
	doc := map[string]map[string]any{}
	for _, a := range agents {
		texts := make([]string, 0, len(a.stated))
		for _, v := range a.stated {
			texts = append(texts, literalText(v.text))
		}
		doc[a.key] = map[string]any{"stated": texts}
		for _, t := range a.tools {
			if t.mcp() {
				address := make([]string, 0, len(t.parts))
				for _, p := range t.parts {
					address = append(address, p.Text)
				}
				doc[t.key] = map[string]any{"endpoint": t.endpoint, "address": address}
			}
		}
	}
	for _, p := range parts {
		doc[p.key] = map[string]any{"endpoint": p.endpoint}
	}
	raw, _ := json.Marshal(doc)
	return raw
}

// literalText is a text's literal, empty for a text that is none.
func literalText(t text) string {
	if t.literal() {
		return t.value
	}
	return ""
}

// located returns every value of draftedValues the credential decision judges,
// agent by agent — its stated values, its model's endpoint where it is the first
// agent wired to the part, then each MCP server's address and the parts of its
// url the address leaves out — each named by its agent and its field: a key a
// connection presents as a value its format marks as a credential, an MCP
// server's address and each part of its url, named as
// mcpimport.AddressPartCredential names one, as a member of an MCP server's
// configuration, and every other value as an input its format marks as no
// credential.
func located(agents []*agent, parts []*modelPart) []importer.Credential {
	var out []importer.Credential
	for _, a := range agents {
		for j, v := range a.stated {
			if !v.text.literal() {
				continue
			}
			class := importer.CredentialUnmarked
			if v.marked {
				class = importer.CredentialMarked
			}
			out = append(out, importer.Credential{Node: a.ref, Field: v.field,
				Pointer: importer.Pointer(a.key, "stated", strconv.Itoa(j)), Class: class})
		}
		if a.part != nil && a.part.node == a.ref && a.part.endpoint != nil {
			out = append(out, importer.Credential{Node: a.ref, Field: "model.connection.endpoint",
				Pointer: importer.Pointer(a.part.key, "endpoint"), Class: importer.CredentialUnmarked})
		}
		for _, t := range a.tools {
			if !t.mcp() {
				continue
			}
			field := "tools." + firstOf(t.name, mcpKind) + ".url"
			if t.endpoint != nil {
				out = append(out, importer.Credential{Node: a.ref, Field: field,
					Pointer: importer.Pointer(t.key, "endpoint"), Class: importer.CredentialServerMember})
			}
			for j, p := range t.parts {
				if p.Text != "" {
					out = append(out, mcpimport.AddressPartCredential(a.ref, field,
						importer.Pointer(t.key, "address", strconv.Itoa(j)), p))
				}
			}
		}
	}
	return out
}

// readBack reads the values the credential decision wrote empty back onto the
// agents, the model parts and the MCP servers: a stated value that held
// something and is written empty is written with no value, an endpoint that
// held something and is written empty is left for the Release, and a part of an
// MCP server's url that held something and is written empty is left out, named
// alone. A value the decision carried is left exactly as the declaration writes
// it.
func readBack(agents []*agent, parts []*modelPart, written []byte) error {
	var values map[string]struct {
		Stated   []string `json:"stated"`
		Endpoint *string  `json:"endpoint"`
		Address  []string `json:"address"`
	}
	if err := json.Unmarshal(written, &values); err != nil {
		return err
	}
	emptied := func(key string, held *string) bool {
		got := values[key].Endpoint
		return held != nil && *held != "" && got != nil && *got == ""
	}
	for _, a := range agents {
		got := values[a.key].Stated
		for j := range a.stated {
			v := &a.stated[j]
			if v.text.literal() && j < len(got) && got[j] == "" {
				v.text.value, v.emptied = "", true
			}
		}
		for _, t := range a.tools {
			if !t.mcp() {
				continue
			}
			if emptied(t.key, t.endpoint) {
				t.endpoint = nil
				t.left = "infra.endpoint, " + emptiedNote
			}
			address := values[t.key].Address
			for j := range t.parts {
				if t.parts[j].Text != "" && j < len(address) && address[j] == "" {
					t.parts[j].Emptied = true
				}
			}
		}
	}
	for _, p := range parts {
		if emptied(p.key, p.endpoint) {
			p.endpoint = nil
		}
	}
	return nil
}

// decisions returns, agent by agent, a decision for the agent, for the model
// part it is wired to, the part where it is first wired, or for its model where
// it declares one wired to nothing; then for each of its tools, in its order.
func decisions(agents []*agent) []aggrega.ImportDecision {
	var out []aggrega.ImportDecision
	decided := map[*modelPart]bool{}
	for _, a := range agents {
		out = append(out, agentDecision(a))
		switch p := a.part; {
		case p != nil && !decided[p]:
			decided[p] = true
			out = append(out, partDecision(p))
		case p == nil && a.model.present:
			out = append(out, aggrega.ImportDecision{SourceNode: aggrega.DecisionSourceNode{ID: a.ref + "#model",
				Type: "model", ParamsSummary: modelSummary(a.model)},
				Rationale: "the model is wired to nothing, since " + unwiredWhy(a)})
		}
		for _, t := range a.tools {
			out = append(out, toolDecision(a, t))
		}
	}
	return out
}

// agentDecision is the decision for one agent: an instance of Component, wired
// to its model part where it has one, and why not where it has none.
func agentDecision(a *agent) aggrega.ImportDecision {
	d := aggrega.ImportDecision{SourceNode: aggrega.DecisionSourceNode{ID: a.ref, Type: "agent",
		ParamsSummary: agentSummary(a)}}
	d.Chosen = &aggrega.DecisionChoice{Target: Component, Mode: "reference", Tier: "NONE"}
	d.Rationale = "an Agent Framework declarative agent is an instance of " + Component + ", carrying its " +
		"displayName and its description, run by the application that loads it"
	if a.part != nil {
		d.Rationale += "; its model " + a.part.ref + " is wired to " + a.part.key + " on its " + modelSocket + " socket"
	} else {
		d.Rationale += "; its " + modelSocket + " socket is left for the author, since " + unwiredWhy(a)
	}
	return d
}

// agentSummary summarises an agent's declaration for its decision: its model and
// how many tools it names.
func agentSummary(a *agent) string {
	summary := "no model"
	if a.model.present {
		summary = modelSummary(a.model)
	}
	switch n := len(a.tools); n {
	case 0:
	case 1:
		summary += ", 1 tool"
	default:
		summary += ", " + strconv.Itoa(n) + " tools"
	}
	return summary
}

// modelSummary summarises a model: its provider, its id and its API, each as
// written or as the reference it is read from.
func modelSummary(m model) string {
	var parts []string
	for _, f := range []struct {
		name string
		t    text
	}{{"provider", m.provider}, {"model", m.id}, {"apiType", m.apiType}} {
		if f.t.present {
			parts = append(parts, f.name+" "+modelRef(f.t))
		}
	}
	if len(parts) == 0 {
		return "a model naming no provider and no id"
	}
	return strings.Join(parts, ", ")
}

// partDecision is the decision for one model part: a reference to the catalog's
// OpenAI Component, or that Component drafted where the catalog holds none.
func partDecision(p *modelPart) aggrega.ImportDecision {
	d := aggrega.ImportDecision{SourceNode: aggrega.DecisionSourceNode{ID: p.ref, Type: "model",
		ParamsSummary: "provider " + openAIProvider + ", model " + p.ref}}
	why := "the " + modelSocket + " socket of " + Component + " admits a model serving the OpenAI API"
	if p.drafted {
		d.Chosen = &aggrega.DecisionChoice{Target: p.component, Mode: "draft", Tier: "NONE"}
		d.Rationale = why + ", and the catalog holds no " + modelComponent + ", so a Component for it is drafted as " +
			p.component + ", serving " + p.ref + " to " + strings.Join(p.agents, ", ")
	} else {
		d.Chosen = &aggrega.DecisionChoice{Target: p.component, Mode: "reference", Tier: "NONE"}
		d.Rationale = why + ", and the catalog holds " + p.component + ", the Component of the " + openAIProvider +
			" provider, so " + p.key + " is an instance of it serving " + p.ref + " to " + strings.Join(p.agents, ", ")
	}
	if p.id.variable != "" {
		d.Rationale += "; its model's name is left for the author, since the declarative agent reads its id from the " +
			"environment variable " + p.id.variable
	} else if p.id.expression {
		d.Rationale += "; its model's name is left for the author, since the declarative agent computes its id with " +
			"an expression the import does not evaluate"
	}
	return d
}

// toolDecision is the decision for one tool: an instance of the MCP tool for an
// MCP server, and no choice for any other, saying why.
func toolDecision(a *agent, t *tool) aggrega.ImportDecision {
	id := a.ref + "#" + firstOf(t.name, firstOf(t.kind, "tool"))
	d := aggrega.ImportDecision{SourceNode: aggrega.DecisionSourceNode{ID: id, Type: "tool",
		ParamsSummary: "kind " + firstOf(t.kind, "none") + ", used by " + a.ref}}
	switch t.kind {
	case mcpKind:
		d.SourceNode.ID = t.ref
		d.Chosen = &aggrega.DecisionChoice{Target: mcpimport.Component, Mode: "reference", Tier: "NONE"}
		d.Rationale = "an MCP server the agent reaches at its url is an instance of " + mcpimport.Component +
			", drafted as a server someone else runs, wired to " + a.key + " on its " + toolsSocket + " socket; " +
			"its approval mode and the tools it allows are the application's, read from the declarative agent"
	case "function":
		d.Rationale = "a function tool runs code the application binds to it, which the import does not read, so it " +
			"becomes no Component"
	case "web_search", "file_search", "code_interpreter":
		d.Rationale = "a tool Agent Framework hands the model's provider to run, so it becomes no Component"
	default:
		d.Rationale = "a tool of a kind the import drafts no Component for, which the application configures from " +
			"the declarative agent"
	}
	return d
}

// firstOf returns s, or fallback where s is empty.
func firstOf(s, fallback string) string {
	if s != "" {
		return s
	}
	return fallback
}
