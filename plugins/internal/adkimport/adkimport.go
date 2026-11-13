// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package adkimport is the Google ADK importer: it reads the agents declared in
// the Agent Config files of an application built on Google's Agent Development
// Kit (ADK) — a root_agent.yaml and the configurations its sub-agents name — and
// drafts an Aggregate holding one instance of the standard library's
// ADK agent, core/ai-agent/google-adk, per agent tree, wired to the Gemini model
// the tree names where ADK serves it as one, and a Release beside it naming what
// the Release supplies.
//
// The importer guest (plugins/google-adk-importer) holds the descriptor, the
// signatures a configuration is recognised by and the export wiring; everything
// it does with a source is here, so it compiles and is tested on the host
// toolchain as well.
//
// # What a configuration declares
//
// ADK states that its Agent Config is experimental. An agent's model is a Gemini
// model by default; ADK serves some other providers' models by name through
// classes of its own, such as claude-sonnet-4-5 and gpt-4o, and others through a
// LiteLLM model string. An application declared this way keeps each agent in a
// YAML file: its name, its class (agent_class, LlmAgent where it names none), its
// description and its instruction; the model it asks for (model) or the code
// building its model (model_code); its tools, each by name with the arguments
// its configuration hands it (args); the options of its model requests
// (generate_content_config); and its sub-agents (sub_agents), each the path of
// another configuration (config_path), resolved from the directory of the
// configuration naming it, or an agent the application's code builds (code).
// ADK's scaffold, adk create --type=config, writes root_agent.yaml, the
// configuration of the root agent, and an environment file beside it.
//
// The importer reads those configurations and nothing else. A directory is handed
// to the importer as its root_agent.yaml files alone; the configuration a
// sub-agent's config_path names is read only where the attachment's with.sources
// hands it — under the last segment of the path — and a configuration the import
// was not handed is named as not handed, with how to hand it. The application's
// code — the tools, callbacks and schemas a configuration names by their code,
// and any agent named by code — is never among the sources, and neither is the
// environment file; the importer reads neither. A source with.sources hands is
// read only as a configuration a root's tree names, so a request holding no root
// configuration reads no tree. The application loads its configurations itself,
// from the image the Release runs, so the draft carries only what the runtime
// Component takes.
//
// # What it drafts
//
// ADK serves an application from its root agent, the agents its configurations
// name built inside it, and core/ai-agent/google-adk runs one application: so
// each agent tree — each root_agent.yaml, or a configuration handed with no
// name — is one instance, keyed by its root agent's name and carrying its
// description. Every other agent of the tree is reported in a decision choosing
// nothing, as is a sub-agent the application's code builds, one whose
// configuration the import was not handed, and one ADK refuses. An agent whose
// class ADK deprecates in favour of Workflow — LoopAgent, ParallelAgent and
// SequentialAgent — is reported as deprecated.
//
// The first agent of a tree, root first, naming a model or the code building
// one decides the instance's model. A model it names whose requests ADK sends to
// the Gemini API — a model name of its Gemini class other than a Vertex AI
// resource name, or one of its Gemma class, as ADK resolves a model string, which
// servedBy mirrors — is wired, on the instance's model socket, to an instance of
// the catalog's Gemini Component,
// core/ai-model/gemini, serving the model name that class is handed: the
// Component of ADK's default provider, named by the provider as every envelope
// importer names one, and the one the socket admits; trees naming one model
// share one instance. Where the request's catalog view holds no Component of
// that name, one is drafted under the target namespace,
// <target_namespace>/ai-model/gemini, and its decision says so. A model ADK
// serves any other way — a LiteLLM model string or a Vertex AI resource name
// among them — is wired to nothing: the socket takes a Gemini model and the
// agent reads no model name from its environment, so the author wires one and
// names it in the agent's configuration, and the comment above the instance and
// the root agent's decision name the model and the family ADK serves it
// through. The models other agents of the tree ask for are reported with the
// family ADK serves each through, where the tree's model is wired: one whose
// requests ADK sends to the Gemini API is sent with the one key the connection
// delivers. A tree whose model the application's code builds, or naming none, is
// wired to nothing too, and the draft says why; the socket is required, so a
// build halts naming it until one is wired. Whether the application reaches a
// model of ADK's Gemini class through the Gemini API or through Vertex AI is
// chosen by its environment, which the import does not read, while ADK's Gemma
// class supports the Gemini API only; the socket admits the Gemini API alone,
// and the model's decision says so.
//
// Each tool is reported in a decision choosing nothing: one ADK ships runs inside
// the application, and any other is code the application defines.
//
// The Aggregate and the Release are named after the application, as
// importer.DraftName names a draft: after the name its root agent's
// configuration declares, where every tree read declares the same one, and
// otherwise after the directory or the file the import was handed.
//
// The Release, release.yaml beside aggregate.yaml, names the Aggregate and leaves
// for the Release what no configuration states: each instance's image, which runs
// the application and holds its configurations, and the credential it presents to
// its model. Each model has an entry of its own, so the Release places it with
// the instances. The Release's environment and its Manifest are left unset, each
// named in a comment, since no source names either and the import drafts no
// Manifest.
//
// # Credential values
//
// A configuration can hand a key to a tool, in its args, or to its model
// requests, in the HTTP options of its generate_content_config — their headers,
// their address, the arguments of their client. Each member of a tool's args and
// of those HTTP options is written into a comment above the instance's entry of
// the Release, for the author to review, and each that is a string, an object or
// a list is handed to the plugin kit's credential decision,
// importer.CarryCredentials, as an input its format marks as no credential that
// can carry one: ADK's schema marks no field as a credential, and a member's name
// is not read for what it means. By default each is carried as written and named
// in a credential-carried warning by its agent and its field, never by its value;
// where the empty_credentials setting is all, each is written with no value and
// reported under credential-emptied. ADK expands no variable reference in a
// configuration, so every value is read as the literal it is.
package adkimport

import (
	"encoding/json"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// Format is the one format the importer serves: an ADK application's Agent
// Config files.
const Format = "google-adk"

// Syntax is the syntactic format a configuration is parsed as.
const Syntax = "yaml"

// RootFile is the name of the root agent's configuration, which ADK's scaffold
// writes and ADK loads an application from, and the name the importer's
// signatures recognise a configuration by.
const RootFile = "root_agent.yaml"

// Component is the catalog Component each agent tree is drafted as an instance
// of.
const Component = "core/ai-agent/google-adk"

// The model provider ADK uses by default, and the catalog Component serving it,
// named by the provider.
const (
	provider       = "gemini"
	modelComponent = "core/ai-model/" + provider
)

// handHint says how to hand the importer a sub-agent's configuration it was not
// handed.
const handHint = "Name the file under the Google ADK importer's with.sources to have it read"

// modelSocket is the socket of Component a tree's model is wired to.
const modelSocket = "model"

// where completes each credential report, naming where the value is written.
const where = "in the Release the draft writes"

// modelPart is one model a tree is wired to: an instance of the Gemini
// Component serving its model.
type modelPart struct {
	// key is the instance's key; ref names it in a report.
	key, ref string
	// component is the Component it is an instance of; drafted is set where that
	// Component is drafted rather than referenced.
	component string
	drafted   bool
	// model is the model it serves: the name ADK hands the class serving it, and
	// family the family it is named by; geminiAPIAlone is set where that class
	// reaches the Gemini API alone, never Vertex AI.
	model, family  string
	geminiAPIAlone bool
	// agents are the agents whose model it serves, by ref.
	agents []string
}

// Import turns one import request into the drafts: the request's sources — its
// primary bytes, where it carries any, and its named sources — are read from
// each root configuration, each source parsed at most once through parse, the
// host's parser. A source the parser refuses is reported under parse-failed,
// which halts the import, and a root configuration declaring no agent under
// no-nodes; the import carries on with the other trees. A request holding no
// root configuration — only sources with.sources hands — reports each under
// no-nodes and drafts nothing. A request naming a
// format other than Format reads nothing.
//
// Where any tree declares an agent, it returns the drafted Aggregate and Release
// and any drafted model Component, as files and entities, and a decision per
// agent, sub-agent read or not, model and tool; where the empty_credentials
// setting holds a value it does not take, it returns that halt and drafts
// nothing. Given one request and one parser, it returns byte-identical output.
func Import(in aggrega.ImportInput, parse func(syntax string, source []byte) (json.RawMessage, error)) aggrega.ImportOutput {
	var out aggrega.ImportOutput
	if in.Format != Format {
		return out
	}
	r := newReader(in, parse)
	roots := r.roots()
	if len(roots) == 0 {
		// Every source is one with.sources hands, read only as a configuration a
		// root names; with no root, each is reported as holding nothing drafted.
		for _, src := range r.sources {
			out.Diagnostics = append(out.Diagnostics, importer.NoNodes(importer.Named(in, src), in.Format))
		}
		return out
	}
	var apps []*application
	for _, i := range roots {
		app := r.readApplication(i)
		if app == nil {
			if !r.failed[i] {
				r.diags = append(r.diags, importer.NoNodes(importer.Named(in, r.sources[i]), in.Format))
			}
			continue
		}
		apps = append(apps, app)
	}
	out.Diagnostics = append(out.Diagnostics, r.diags...)
	if len(apps) == 0 {
		return out
	}

	used := map[string]bool{}
	for _, app := range apps {
		if dir := path.Dir(app.file); len(apps) > 1 && app.file != "" && dir != "." {
			app.prefix = dir + "#"
		}
		for _, a := range app.agents {
			a.ref = app.prefix + nameOf(a)
		}
		app.key = importer.UniqueKey(used, nameOf(app.root))
	}
	parts := wire(in, apps, used)

	for _, app := range apps {
		app.stated = statedValues(app)
	}
	written, credentialDiags, err := importer.CarryCredentials(in.Settings, draftedValues(apps), where, located(apps))
	if err != nil {
		out.Error = "google-adk importer: write the drafted values: " + err.Error()
		return out
	}
	out.Diagnostics = append(out.Diagnostics, credentialDiags...)
	if written == nil {
		return out
	}
	if err := readBack(apps, written); err != nil {
		out.Error = "google-adk importer: read the drafted values back: " + err.Error()
		return out
	}

	out.Decisions = decisions(apps)
	var entities []aggrega.EmittedEntity
	for _, app := range apps {
		entities = append(entities, aggrega.EmittedEntity{Kind: "Component", Name: Component, Mode: "reference",
			SourceRef: app.root.ref, DecisionRef: "decision:" + app.root.ref})
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

	titles := make([]string, 0, len(apps))
	for _, app := range apps {
		titles = append(titles, app.root.name)
	}
	name := importer.DraftName(in, titles...)
	aggregate := renderAggregate(name, apps, parts, unread(r, apps))
	release := renderRelease(name, apps, parts)
	out.Entities = append(entities,
		aggrega.EmittedEntity{Kind: "Aggregate", Name: name, Mode: "draft", Body: aggregate},
		aggrega.EmittedEntity{Kind: "Release", Name: name, Mode: "draft", Body: release})
	out.Files = importer.Render(in.Settings, out.Entities, out.Decisions)
	if slices.ContainsFunc(out.Files, func(f aggrega.ScaffoldFile) bool { return f.Path == "aggregate.yaml" }) {
		out.Files = append(out.Files, aggrega.ScaffoldFile{Path: "release.yaml", Content: release})
	}
	return out
}

// nameOf is the name an agent is keyed and reported by: its name, or, where its
// configuration states none, the name of its file without its extension.
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

// modelAgent returns the agent of a tree deciding its model: the first, root
// first in the order ADK builds them, naming a model or the code building one;
// nil where none does.
func modelAgent(app *application) *agent {
	for _, a := range app.agents {
		if a.model != "" || a.modelCode != "" {
			return a
		}
	}
	return nil
}

// wire makes the model part each tree naming a model whose requests ADK sends to
// the Gemini API is wired to, keyed after every tree's instance: one
// part per model, serving the model name that class is handed and shared by
// every tree whose model it is. A part's Component is the catalog's Gemini
// Component where the request's catalog view holds it, and a Component drafted
// under the target namespace otherwise. A tree naming a model ADK serves any
// other way has no part: Component takes a Gemini model on its model socket, so
// the author wires one.
func wire(in aggrega.ImportInput, apps []*application, used map[string]bool) []*modelPart {
	held := false
	if in.Catalog != nil {
		held = slices.ContainsFunc(in.Catalog.Components, func(c aggrega.CatalogComponent) bool {
			return c.Name == modelComponent
		})
	}
	component := modelComponent
	if !held {
		component = importer.TargetNamespace(in.Settings) + "/ai-model/" + provider
	}
	var parts []*modelPart
	shared := map[string]*modelPart{}
	for _, app := range apps {
		a := modelAgent(app)
		app.modeled = a
		if a == nil || a.model == "" {
			continue
		}
		s := servedBy(a.model)
		if !s.geminiAPI {
			continue
		}
		p := shared[s.name]
		if p == nil {
			p = &modelPart{ref: app.prefix + a.model, component: component, drafted: !held, model: s.name,
				family: s.family, geminiAPIAlone: s.geminiAPIAlone, key: importer.UniqueKey(used, s.name)}
			shared[s.name] = p
			parts = append(parts, p)
		}
		p.agents = append(p.agents, a.ref)
		app.part = p
	}
	return parts
}

// statedValues returns the values a tree's configurations hand their tools and
// their model requests, in the order the draft writes them: agent by agent, root
// first, each tool's args in the order the agent lists its tools, then the HTTP
// options of its model requests.
func statedValues(app *application) []statedValue {
	var out []statedValue
	for _, a := range app.agents {
		for _, t := range a.tools {
			for _, m := range t.args {
				out = append(out, statedValue{agent: a, field: "tools." + t.name + ".args." + m.name, member: m})
			}
		}
		for _, m := range a.requests {
			out = append(out, statedValue{agent: a, field: "generate_content_config." + a.requestsKey + "." + m.name,
				member: m})
		}
	}
	return out
}

// draftedValues writes, as one JSON document, the values the credential decision
// judges: under each instance's key, the text of each value its tree states, in
// the order the draft writes them.
func draftedValues(apps []*application) []byte {
	doc := map[string]map[string][]string{}
	for _, app := range apps {
		texts := make([]string, 0, len(app.stated))
		for _, v := range app.stated {
			texts = append(texts, v.text)
		}
		doc[app.key] = map[string][]string{"stated": texts}
	}
	raw, _ := json.Marshal(doc)
	return raw
}

// located returns every value of draftedValues the credential decision judges,
// in the order the draft writes them: each that is a string, an object or a list
// and holds something, named by its agent and its field, as an input its format
// marks as no credential.
func located(apps []*application) []importer.Credential {
	var out []importer.Credential
	for _, app := range apps {
		for j, v := range app.stated {
			if !v.located || v.text == "" {
				continue
			}
			out = append(out, importer.Credential{Node: v.agent.ref, Field: v.field,
				Pointer: importer.Pointer(app.key, "stated", strconv.Itoa(j)), Class: importer.CredentialUnmarked})
		}
	}
	return out
}

// readBack reads the values the credential decision wrote empty back onto the
// trees: a value that held something and is written empty is written with no
// value. A value the decision carried is left exactly as its configuration
// writes it.
func readBack(apps []*application, written []byte) error {
	var values map[string]struct {
		Stated []string `json:"stated"`
	}
	if err := json.Unmarshal(written, &values); err != nil {
		return err
	}
	for _, app := range apps {
		got := values[app.key].Stated
		for j := range app.stated {
			v := &app.stated[j]
			if v.located && v.text != "" && j < len(got) && got[j] == "" {
				v.text, v.emptied = "", true
			}
		}
	}
	return nil
}

// unread returns the named sources no tree reads, in the order the import was
// handed them.
func unread(r *reader, apps []*application) []string {
	read := map[string]bool{}
	for _, app := range apps {
		for _, f := range app.files {
			read[f] = true
		}
	}
	var out []string
	for _, src := range r.sources {
		if src.Name != "" && !read[src.Name] {
			out = append(out, src.Name)
		}
	}
	return out
}

// decisions returns, tree by tree, a decision for the root agent and the model
// part it is wired to, the part where it is first wired; then for every
// sub_agents entry of the tree, read or not, each agent's entries in the order
// it lists them and each read agent's own before the next entry; then for each
// tool the tree's agents name, once each, in the order they name them.
func decisions(apps []*application) []aggrega.ImportDecision {
	var out []aggrega.ImportDecision
	decided := map[*modelPart]bool{}
	for _, app := range apps {
		out = append(out, rootDecision(app))
		if p := app.part; p != nil && !decided[p] {
			decided[p] = true
			out = append(out, partDecision(p))
		}
		var visit func(a *agent)
		visit = func(a *agent) {
			for _, s := range a.subs {
				if s.agent != nil {
					out = append(out, subDecision(app, s.agent))
					visit(s.agent)
					continue
				}
				d := aggrega.ImportDecision{SourceNode: aggrega.DecisionSourceNode{
					ID: app.prefix + s.label(), Type: "agent", ParamsSummary: "named by " + a.ref},
					Rationale: "the sub-agent " + s.why + ", so it becomes no Component"}
				if s.notHanded {
					d.Rationale += "; " + strings.ToLower(handHint[:1]) + handHint[1:]
				}
				out = append(out, d)
			}
		}
		visit(app.root)
		var tools []tool
		users := map[string][]string{}
		for _, a := range app.agents {
			for _, t := range a.tools {
				if !slices.ContainsFunc(tools, func(u tool) bool { return u.name == t.name }) {
					tools = append(tools, t)
				}
				if !slices.Contains(users[t.name], a.ref) {
					users[t.name] = append(users[t.name], a.ref)
				}
			}
		}
		for _, t := range tools {
			out = append(out, toolDecision(app, t, users[t.name]))
		}
	}
	return out
}

// rootDecision is the decision for a tree's root agent: an instance of
// Component, wired to its model part where it has one, and why not where it has
// none.
func rootDecision(app *application) aggrega.ImportDecision {
	a := app.root
	d := aggrega.ImportDecision{SourceNode: aggrega.DecisionSourceNode{ID: a.ref, Type: "agent",
		ParamsSummary: a.summary()}}
	d.Chosen = &aggrega.DecisionChoice{Target: Component, Mode: "reference", Tier: "NONE"}
	d.Rationale = "an ADK application is served from its root agent, every agent its configurations name built " +
		"inside it, so the tree of " + a.ref + " is one instance of " + Component + ", carrying its description"
	if app.part != nil {
		d.Rationale += "; the model " + app.part.model + ", which " + app.modeled.ref + " names, is wired to " +
			app.part.key + " on its " + modelSocket + " socket"
	} else {
		d.Rationale += "; its " + modelSocket + " socket is left for the author, since " + unwiredWhy(app) +
			"; the build halts naming the socket until one is wired"
	}
	if note := deprecationNote(a); note != "" {
		d.Rationale += "; " + note
	}
	return d
}

// unwiredWhy says why a tree's model is wired to nothing, in words following
// "since": the model its deciding agent names is one whose requests ADK does not
// send to the Gemini API, naming the family ADK serves it through, while Component
// takes a Gemini model and reads no model name from its environment, so the
// author wires one and names it in the agent's configuration; the application's
// code builds it; or no agent names one.
func unwiredWhy(app *application) string {
	a := app.modeled
	switch {
	case a != nil && a.model != "":
		return "the model " + commentText(a.model) + ", which " + commentText(a.ref) + " names, is one ADK serves " +
			"through " + familyOf(a.model) + ", not through the Gemini API, while " + Component + " takes a Gemini " +
			"model on its " + modelSocket + " socket and reads no model name from its environment, so the author " +
			"wires a Gemini model there and names that model in " + commentText(a.ref) + "'s configuration"
	case a != nil:
		return "the model of " + a.ref + " is built by the application's code at " + a.modelCode +
			", which the import does not read"
	}
	return "no agent of the tree names a model, so ADK uses its default model"
}

// deprecationNote says that ADK deprecates an agent's class, empty where it does
// not.
func deprecationNote(a *agent) string {
	if class := a.deprecated(); class != "" {
		return "ADK deprecates " + class + " in favour of Workflow"
	}
	return ""
}

// partDecision is the decision for one model part: a reference to the catalog's
// Gemini Component, or that Component drafted where the catalog holds none. Only
// a model whose requests ADK sends to the Gemini API has a part. Where its class
// reaches the Gemini API alone, the decision says so; otherwise, that the
// application's environment chooses between the Gemini API and Vertex AI.
func partDecision(p *modelPart) aggrega.ImportDecision {
	d := aggrega.ImportDecision{SourceNode: aggrega.DecisionSourceNode{ID: p.ref, Type: "model",
		ParamsSummary: "provider " + provider + ", model " + p.model}}
	every := "ADK serves the model " + p.model + " through its " + p.family + " class, which sends its requests " +
		"to the Gemini API, so it is drafted as an instance of the Gemini Component; "
	backend := "; the application reaches Gemini through the Gemini API or through Vertex AI as its environment " +
		"chooses, which the import does not read, and the " + modelSocket + " socket of " + Component +
		" admits the Gemini API alone"
	if p.geminiAPIAlone {
		backend = "; ADK's " + p.family + " class supports the Gemini API only, not Vertex AI, and the " +
			modelSocket + " socket of " + Component + " admits the Gemini API alone"
	}
	if p.drafted {
		d.Chosen = &aggrega.DecisionChoice{Target: p.component, Mode: "draft", Tier: "NONE"}
		d.Rationale = every + "the catalog holds no " + modelComponent + ", so a Component for it is drafted as " +
			p.component + ", serving " + p.model + " to " + strings.Join(p.agents, ", ") + backend
		return d
	}
	d.Chosen = &aggrega.DecisionChoice{Target: p.component, Mode: "reference", Tier: "NONE"}
	d.Rationale = every + "the catalog holds " + p.component + ", so " + p.key + " is an instance of it serving " +
		p.model + " to " + strings.Join(p.agents, ", ") + backend
	return d
}

// subDecision is the decision for an agent of a tree other than its root: no
// choice, since ADK builds it inside the application its root's instance runs,
// with the model it asks for and its deprecated class where it has either.
func subDecision(app *application, a *agent) aggrega.ImportDecision {
	d := aggrega.ImportDecision{SourceNode: aggrega.DecisionSourceNode{ID: a.ref, Type: "agent",
		ParamsSummary: a.summary()},
		Rationale: "a sub-agent of " + a.parent.ref + ", which ADK builds inside the application the instance of " +
			app.root.ref + " runs, so it becomes no instance of its own"}
	switch {
	case asksOtherModel(app, a) && app.part == nil:
		// The instance has no model connection until the author wires one, so the
		// decision names none.
		d.Rationale += "; it asks for the model " + a.model + ", which ADK serves through " + familyOf(a.model)
	case asksOtherModel(app, a) && servedBy(a.model).geminiAPI:
		d.Rationale += "; it asks for the model " + a.model + ", which ADK serves through its " + familyOf(a.model) +
			" class, sending its requests to the Gemini API with the key the instance's " + modelSocket +
			" connection delivers"
	case asksOtherModel(app, a):
		d.Rationale += "; it asks for the model " + a.model + ", which ADK serves through " + familyOf(a.model) +
			", not through the instance's " + modelSocket + " connection"
	case a.modelCode != "" && a != app.modeled:
		d.Rationale += "; its model is built by the application's code at " + a.modelCode + ", which the import does " +
			"not read"
	}
	if note := deprecationNote(a); note != "" {
		d.Rationale += "; " + note
	}
	return d
}

// asksOtherModel reports whether an agent of a tree names a model other than
// the one the tree's instance is wired to serve, compared by the model name ADK
// hands the class serving each.
func asksOtherModel(app *application, a *agent) bool {
	return a.model != "" && a != app.modeled && (app.part == nil || servedBy(a.model).name != app.part.model)
}

// toolDecision is the decision for one tool a tree's agents name: no choice,
// since ADK runs a tool inside the application, either one ADK ships or code the
// application defines.
func toolDecision(app *application, t tool, users []string) aggrega.ImportDecision {
	d := aggrega.ImportDecision{SourceNode: aggrega.DecisionSourceNode{ID: app.prefix + t.name, Type: "tool",
		ParamsSummary: "used by " + strings.Join(users, ", ")}}
	if t.own() {
		d.Rationale = "a tool ADK ships, which runs inside the application, so it becomes no Component"
	} else {
		d.Rationale = "code the application defines, which the import does not read, so it becomes no Component"
	}
	return d
}
