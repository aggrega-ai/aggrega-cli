// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package crewaiimport is the CrewAI importer: it reads the agents and the tasks
// a CrewAI project declares in its configuration files and drafts an Aggregate
// holding one instance of the standard library's CrewAI agent,
// core/ai-agent/crewai, per agent, each wired to the model its configuration
// names, and a Release beside it naming what the Release supplies.
//
// The importer guest (plugins/crewai-importer) holds the descriptor, the
// signatures a project is recognised by and the export wiring; everything it
// does with a source is here, so it compiles and is tested on the host toolchain
// as well.
//
// # What a project declares
//
// CrewAI keeps a crew's behaviour in a program and declares its agents and tasks
// in configuration files, in one of two layouts. A classic project declares its
// agents in config/agents.yaml and its tasks in config/tasks.yaml, each keyed by
// name. A project in the JSON-first layout declares its crew in crew.jsonc — the
// agents it lists by name, its tasks, and the crew's own settings — and each
// agent in agents/<name>.jsonc, both written in JSON with comments — or, spelled
// crew.json and agents/<name>.json, in strict JSON, which is read the same way;
// where a crew or an agent is written in both spellings, the .jsonc file is the
// one read, as CrewAI reads it, and the other is named as not read. An agent is
// declared with its role, its goal and its backstory; whether it may delegate
// work (allow_delegation) and narrate its reasoning (verbose), written at the top
// of its declaration or under settings; the model it runs on (llm), written as
// provider/model or as an object naming the model; and the tools it uses, by
// name.
//
// The importer reads those declarations and nothing else. The program — the
// crew.py of a classic project, a flow's main.py, a tool a project defines in
// Python — is never among the sources: a directory is handed to the importer as
// the files its signatures name, which a program's files are not. What the
// program does is the author's to bring, in the image the Release runs.
//
// # What it drafts
//
// Each agent is an instance of core/ai-agent/crewai, its role, goal and
// backstory written as declared, allow_delegation as declared — false where it
// states none — and log_level DEBUG where it is verbose and INFO otherwise: at
// DEBUG the agent narrates each step of its reasoning, and INFO reports progress
// and problems only. An agent whose llm names a provider and a model is wired,
// on its model socket, to an instance of the catalog's Component for that
// provider — core/ai-model/<provider>, an underscore in the provider's name read
// as a hyphen, and azure read as azure-openai — serving that model; agents naming
// one provider and model share one instance. Where the workspace's catalog holds
// no Component of that name, one is drafted under the target namespace,
// <target_namespace>/ai-model/<provider>, and its decision says so. An agent
// whose llm names no provider, or that declares none, is wired to nothing, and
// the draft says why.
//
// A task is program logic the crew runs, and a tool is code the program defines
// or loads: neither becomes a Component. Each is reported in its own decision,
// and a tool whose name matches the name of a tool Component the catalog holds
// carries that match as a hint.
//
// The Aggregate and the Release are named after the crew, as importer.DraftName
// names a draft: after the name crew.jsonc gives it, where every crew read is
// named alike, and otherwise after the directory or the file the import was
// handed. A classic project's files give the crew no name — its program does —
// so a draft read from one is named after the directory or the file it was
// read from.
//
// The Release, release.yaml beside aggregate.yaml, names the Aggregate and
// leaves for the Release what no declaration states: each agent's image, which
// runs the crew's program, and the credential each agent presents to its model.
// Each model has an entry of its own, so the Release places it with the agents.
// The Release's environment and its Manifest are left unset, each named in a
// comment, since no source names either and the import drafts no Manifest.
//
// # Credential values
//
// An llm written as an object can carry a key — under api_key, in an address, in
// headers. Each member of such an object other than the one naming the model is
// written into a comment above the model's entry of the Release, for the author
// to place where the Component takes it, and each that is not a number or a
// boolean is handed to the plugin kit's credential decision,
// importer.CarryCredentials, as an input its format marks as no credential that
// can carry one — CrewAI marks none, and a member's name is not read for what it
// means. By default each is carried as written and named in a credential-carried
// warning by its agent and its member, never by its value; where the
// empty_credentials setting is all, each is written empty and reported under
// credential-emptied. Every other member the draft does not carry is named,
// never valued.
package crewaiimport

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/importer"
)

// Format is the one format the importer serves: a CrewAI project's declarations.
const Format = "crewai"

// The syntaxes a project's files are parsed as: a classic project's YAML, and the
// JSON with comments of the JSON-first layout.
const (
	SyntaxYAML  = "yaml"
	SyntaxJSONC = "jsonc"
)

// The files a project declares its agents and tasks in, as the importer's
// signatures name them: a classic project's two YAML files, and the JSON-first
// layout's crew and agent files, written with comments as .jsonc or in strict
// JSON as .json. CrewAI reads the .jsonc file of a crew or an agent where both
// spellings are present, and so does the importer.
const (
	AgentsFile     = "config/agents.yaml"
	TasksFile      = "config/tasks.yaml"
	CrewFile       = "crew.jsonc"
	AgentFiles     = "agents/*.jsonc"
	CrewJSONFile   = "crew.json"
	AgentJSONFiles = "agents/*.json"
)

// Component is the catalog Component each agent is drafted as an instance of.
const Component = "core/ai-agent/crewai"

// modelSocket is the socket of Component an agent's model is wired to.
const modelSocket = "model"

// where completes each credential report, naming where the value is written.
const where = "in the Release the draft writes"

// toolCategory is the category of the catalog Components a tool's name is
// matched against for a hint.
const toolCategory = "ai-tool"

// categoryLabel is the label a catalog Component's category is read from.
const categoryLabel = "component.aggrega.ai/category"

// providerNames are the providers whose catalog Component's name is not the
// provider's own.
var providerNames = map[string]string{"azure": "azure-openai"}

// modelPart is one model an agent is wired to: an instance of the Component its
// provider maps to, serving its model.
type modelPart struct {
	// key is the instance's key; ref names it in a report.
	key, ref string
	// component is the Component it is an instance of; drafted is set where that
	// Component is drafted rather than referenced.
	component string
	drafted   bool
	// provider and model are the llm's.
	provider, model string
	// stated are the members of the llm object it was declared with, where it was
	// declared as one; such a part is the one agent's own.
	stated []member
	// agents are the agents wired to it, by ref.
	agents []string
}

// Import turns one import request into the drafts: the request's sources — its
// named sources, or its primary bytes — are grouped into crews by the project
// directory they sit in, and each is parsed once through parse, the host's
// parser, in the syntax its file is written in. A source the parser refuses is
// reported under parse-failed, which halts the import, and a crew declaring no
// agent under no-nodes; the import carries on with the other crews. A request
// naming a format other than Format reads nothing.
//
// Where any crew declares an agent, it returns the drafted Aggregate and Release
// and any drafted model Component, as files and entities, and a decision per
// agent, model, tool and task; where the empty_credentials setting holds a value
// it does not take, it returns that halt and drafts nothing. Given one request
// and one parser, it returns byte-identical output.
func Import(in aggrega.ImportInput, parse func(syntax string, source []byte) (json.RawMessage, error)) aggrega.ImportOutput {
	var out aggrega.ImportOutput
	crews, diags := readCrews(in, parse)
	out.Diagnostics = append(out.Diagnostics, diags...)

	var read []*crew
	for _, c := range crews {
		if !slices.ContainsFunc(c.agents, func(a *agent) bool { return !a.missing }) {
			src := aggrega.ImportSource{}
			if len(c.files) > 0 {
				src.Name = c.files[0]
			}
			out.Diagnostics = append(out.Diagnostics, importer.NoNodes(importer.Named(in, src), in.Format))
			continue
		}
		read = append(read, c)
	}
	if len(read) == 0 {
		return out
	}

	used := map[string]bool{}
	for _, c := range read {
		if len(read) > 1 && c.dir != "" {
			c.prefix = c.dir + "#"
		}
		for _, a := range c.agents {
			a.ref = c.prefix + a.name
			if !a.missing {
				a.key = importer.UniqueKey(used, a.name)
			}
		}
	}
	parts := wire(in, read, used)

	written, credentialDiags, err := importer.CarryCredentials(in.Settings, draftedValues(parts), where, located(parts))
	if err != nil {
		out.Error = "crewai importer: write the drafted values: " + err.Error()
		return out
	}
	out.Diagnostics = append(out.Diagnostics, credentialDiags...)
	if written == nil {
		return out
	}
	if err := readBack(parts, written); err != nil {
		out.Error = "crewai importer: read the drafted values back: " + err.Error()
		return out
	}

	out.Decisions = decisions(in, read, parts)
	var entities []aggrega.EmittedEntity
	for _, c := range read {
		for _, a := range c.agents {
			if a.missing {
				continue
			}
			entities = append(entities, aggrega.EmittedEntity{Kind: "Component", Name: Component, Mode: "reference",
				SourceRef: a.ref, DecisionRef: "decision:" + a.ref})
		}
	}
	draftedNames := map[string]bool{}
	for _, p := range parts {
		mode, body := "reference", ""
		if p.drafted {
			if draftedNames[p.component] {
				continue
			}
			draftedNames[p.component] = true
			mode, body = "draft", renderModel(p.component)
		}
		entities = append(entities, aggrega.EmittedEntity{Kind: "Component", Name: p.component, Mode: mode, Body: body,
			SourceRef: p.ref, DecisionRef: "decision:" + p.ref})
	}

	titles := make([]string, 0, len(read))
	for _, c := range read {
		titles = append(titles, c.name)
	}
	name := importer.DraftName(in, titles...)
	aggregate := renderAggregate(name, read, parts)
	release := renderRelease(name, read, parts)
	out.Entities = append(entities,
		aggrega.EmittedEntity{Kind: "Aggregate", Name: name, Mode: "draft", Body: aggregate},
		aggrega.EmittedEntity{Kind: "Release", Name: name, Mode: "draft", Body: release})
	out.Files = importer.Render(in.Settings, out.Entities, out.Decisions)
	if slices.ContainsFunc(out.Files, func(f aggrega.ScaffoldFile) bool { return f.Path == "aggregate.yaml" }) {
		out.Files = append(out.Files, aggrega.ScaffoldFile{Path: "release.yaml", Content: release})
	}
	return out
}

// wire makes the model part each agent naming a provider and a model is wired to,
// keying each after every agent: one part per provider and model an llm string
// names, shared by every agent naming it, and one per llm object, its agent's
// own. A part's Component is the catalog's Component for its provider where the
// request's catalog view holds it, and a Component drafted under the target
// namespace otherwise.
func wire(in aggrega.ImportInput, read []*crew, used map[string]bool) []*modelPart {
	held := map[string]bool{}
	if in.Catalog != nil {
		for _, c := range in.Catalog.Components {
			held[c.Name] = true
		}
	}
	namespace := importer.TargetNamespace(in.Settings)
	var parts []*modelPart
	shared := map[string]*modelPart{}
	for _, c := range read {
		for _, a := range c.agents {
			if a.missing || a.llm.why != "" {
				continue
			}
			id := a.llm.provider + "/" + a.llm.model
			p := shared[id]
			if p == nil || a.llm.object {
				name := providerComponent(a.llm.provider)
				p = &modelPart{ref: a.llm.text, component: name, provider: a.llm.provider, model: a.llm.model,
					key: importer.UniqueKey(used, a.llm.model)}
				if a.llm.object {
					p.ref = a.ref + "#llm"
					p.stated = a.llm.members
				} else {
					shared[id] = p
				}
				if !held[name] {
					p.component = namespace + "/ai-model/" + importer.UniqueKey(map[string]bool{}, a.llm.provider)
					p.drafted = true
				}
				parts = append(parts, p)
			}
			p.agents = append(p.agents, a.ref)
			a.part = p
		}
	}
	return parts
}

// providerComponent is the name of the catalog Component a provider's models
// are served by: core/ai-model/ followed by the provider's name, lowercased, an
// underscore read as a hyphen, and a provider providerNames lists by its
// Component's name.
func providerComponent(provider string) string {
	name := strings.ReplaceAll(strings.ToLower(provider), "_", "-")
	if alias, ok := providerNames[name]; ok {
		name = alias
	}
	return "core/ai-model/" + name
}

// draftedValues writes, as one JSON document, the values the credential decision
// judges: under each model part's key, the text of each member of the llm object
// it was declared with, in the order the draft writes them.
func draftedValues(parts []*modelPart) []byte {
	doc := map[string]map[string][]string{}
	for _, p := range parts {
		texts := make([]string, 0, len(p.stated))
		for _, m := range p.stated {
			texts = append(texts, m.text)
		}
		doc[p.key] = map[string][]string{"stated": texts}
	}
	raw, _ := json.Marshal(doc)
	return raw
}

// located returns every value of draftedValues the credential decision judges,
// in the order the draft writes them: each member of an llm object that is not a
// number or a boolean and holds something, named by its agent and its member, as
// an input its format marks as no credential.
func located(parts []*modelPart) []importer.Credential {
	var out []importer.Credential
	for _, p := range parts {
		for j, m := range p.stated {
			if !m.located || m.text == "" {
				continue
			}
			out = append(out, importer.Credential{Node: p.agents[0], Field: "llm." + m.name,
				Pointer: importer.Pointer(p.key, "stated", strconv.Itoa(j)), Class: importer.CredentialUnmarked})
		}
	}
	return out
}

// readBack reads the values the credential decision wrote empty back onto the
// parts: a member that held something and is written empty is written with no
// value. A member the decision carried is left exactly as the source writes it.
func readBack(parts []*modelPart, written []byte) error {
	var values map[string]struct {
		Stated []string `json:"stated"`
	}
	if err := json.Unmarshal(written, &values); err != nil {
		return err
	}
	for _, p := range parts {
		got := values[p.key].Stated
		for j := range p.stated {
			m := &p.stated[j]
			if m.located && m.text != "" && j < len(got) && got[j] == "" {
				m.text, m.emptied = "", true
			}
		}
	}
	return nil
}

// decisions returns a decision per agent, model part, tool and task, crew by
// crew in the order the declarations write them: each agent and the part it is
// wired to, the part where it is first wired; then the crew's tools, each once,
// in the order its agents name them; then its tasks.
func decisions(in aggrega.ImportInput, read []*crew, parts []*modelPart) []aggrega.ImportDecision {
	var out []aggrega.ImportDecision
	decided := map[*modelPart]bool{}
	for _, c := range read {
		for _, a := range c.agents {
			out = append(out, agentDecision(a))
			if a.part != nil && !decided[a.part] {
				decided[a.part] = true
				out = append(out, partDecision(a.part))
			}
		}
		var tools []string
		users := map[string][]string{}
		for _, a := range c.agents {
			for _, t := range a.tools {
				if !slices.Contains(tools, t) {
					tools = append(tools, t)
				}
				users[t] = append(users[t], a.ref)
			}
		}
		for _, t := range tools {
			out = append(out, toolDecision(in, c, t, users[t]))
		}
		for _, t := range c.tasks {
			out = append(out, taskDecision(c, t))
		}
	}
	return out
}

// agentDecision is the decision for one agent: an instance of Component, wired
// to its model part where it has one, and why not where it has none. An agent
// crew.jsonc names whose file the import was not handed records no choice.
func agentDecision(a *agent) aggrega.ImportDecision {
	d := aggrega.ImportDecision{SourceNode: aggrega.DecisionSourceNode{ID: a.ref, Type: "agent",
		ParamsSummary: agentSummary(a)}}
	if a.missing {
		d.Rationale = "the crew lists the agent " + a.name + ", whose declaration agents/" + a.name + ".jsonc the " +
			"import was not handed, so it is not drafted"
		return d
	}
	d.Chosen = &aggrega.DecisionChoice{Target: Component, Mode: "reference", Tier: "NONE"}
	d.Rationale = "a CrewAI agent is an instance of " + Component + " carrying the role, the goal and the backstory " +
		"it is declared with"
	if a.part != nil {
		d.Rationale += "; its llm " + a.llm.text + " is wired to " + a.part.key + " on its " + modelSocket + " socket"
	} else {
		d.Rationale += "; its " + modelSocket + " socket is left for the author, since its llm " + a.llm.why
	}
	return d
}

// agentSummary summarises an agent's declaration for its decision: its llm and
// how many tools it names.
func agentSummary(a *agent) string {
	if a.missing {
		return "not declared in the files handed"
	}
	summary := "no llm"
	if a.llm.text != "" {
		summary = "llm " + a.llm.text
	}
	switch len(a.tools) {
	case 0:
	case 1:
		summary += ", 1 tool"
	default:
		summary += ", " + strconv.Itoa(len(a.tools)) + " tools"
	}
	return summary
}

// partDecision is the decision for one model part: a reference to the catalog's
// Component for its provider, or that Component drafted where the catalog holds
// none.
func partDecision(p *modelPart) aggrega.ImportDecision {
	d := aggrega.ImportDecision{SourceNode: aggrega.DecisionSourceNode{ID: p.ref, Type: "llm",
		ParamsSummary: "provider " + p.provider + ", model " + p.model}}
	if p.drafted {
		d.Chosen = &aggrega.DecisionChoice{Target: p.component, Mode: "draft", Tier: "NONE"}
		d.Rationale = "the catalog holds no " + providerComponent(p.provider) + ", the Component of the " + p.provider +
			" provider, so a Component for it is drafted as " + p.component + ", serving " + p.model + " to " +
			strings.Join(p.agents, ", ")
		return d
	}
	d.Chosen = &aggrega.DecisionChoice{Target: p.component, Mode: "reference", Tier: "NONE"}
	d.Rationale = "the catalog holds " + p.component + ", the Component of the " + p.provider + " provider, so " +
		p.key + " is an instance of it serving " + p.model + " to " + strings.Join(p.agents, ", ")
	return d
}

// toolDecision is the decision for one tool a crew's agents name: no choice,
// since a tool is code the crew's program defines or loads, with a hint where a
// tool Component of the catalog view matches its name.
func toolDecision(in aggrega.ImportInput, c *crew, tool string, users []string) aggrega.ImportDecision {
	d := aggrega.ImportDecision{SourceNode: aggrega.DecisionSourceNode{ID: c.prefix + tool, Type: "tool",
		ParamsSummary: "used by " + strings.Join(users, ", ")},
		Rationale: "a tool is code the crew's program defines or loads, which the import does not read, so it " +
			"becomes no Component"}
	if hint := toolHint(in, tool); hint != "" {
		d.SwapHint = hint
		d.Rationale += "; " + hint
	}
	return d
}

// toolHint names the tool Component of the catalog view whose name matches the
// tool's, if one does: the last segment of its name, its hyphens dropped, equal
// to the tool's name lowercased, with what precedes a colon, every hyphen and
// underscore, and a trailing "tool" dropped — search_tool and SearchTool both
// match core/ai-tool/search. Where several match, the first by name is named.
func toolHint(in aggrega.ImportInput, tool string) string {
	if in.Catalog == nil {
		return ""
	}
	want := strings.ToLower(tool[strings.LastIndex(tool, ":")+1:])
	want = strings.NewReplacer("-", "", "_", "").Replace(want)
	want = strings.TrimSuffix(want, "tool")
	if want == "" {
		return ""
	}
	var names []string
	for _, c := range in.Catalog.Components {
		if c.Labels[categoryLabel] != toolCategory {
			continue
		}
		last := c.Name[strings.LastIndex(c.Name, "/")+1:]
		if strings.ReplaceAll(last, "-", "") == want {
			names = append(names, c.Name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	slices.Sort(names)
	return "hint: the catalog holds " + names[0] + ", whose name matches the tool's"
}

// taskDecision is the decision for one task: no choice, since a task is program
// logic the crew runs.
func taskDecision(c *crew, t task) aggrega.ImportDecision {
	summary := "assigned to no agent"
	if t.agent != "" {
		summary = "assigned to " + t.agent
	}
	return aggrega.ImportDecision{SourceNode: aggrega.DecisionSourceNode{ID: c.prefix + t.name, Type: "task",
		ParamsSummary: summary},
		Rationale: "a task is program logic the crew runs — its description, its expected output and the agent it " +
			"is assigned to — so it becomes no Component"}
}
