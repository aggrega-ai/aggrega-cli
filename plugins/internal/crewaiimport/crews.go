// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package crewaiimport

import (
	"bytes"
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/draftyaml"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/yamlorder"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/importer"
)

// declaration is what one source declares, read off its name — or, for a source
// handed with no name, off its shape.
type declaration int

const (
	declaresNothing declaration = iota
	// declaresAgents is a classic project's config/agents.yaml.
	declaresAgents
	// declaresTasks is a classic project's config/tasks.yaml.
	declaresTasks
	// declaresCrew is a project's crew.jsonc.
	declaresCrew
	// declaresAgent is one agent's agents/<name>.jsonc.
	declaresAgent
)

// crew is one CrewAI crew an import reads: the declaration files of one project
// directory, and the agents and the tasks they declare, in the order they
// declare them.
type crew struct {
	// dir is the project directory the crew's files sit in, slash-separated; empty
	// for a source handed with no name, or sitting at the top of the directory.
	dir string
	// prefix opens the reference of each agent, tool and task of the crew in a
	// report: its directory and a #, where the import reads more than one crew.
	prefix string
	// files are the declaration files read, in the order the import was handed
	// them.
	files []string
	// agents and tasks are the crew's, in the order its files declare them.
	agents []*agent
	tasks  []task
	// listed are the agents crew.jsonc names, in its order.
	listed []string
	// name is the crew's name as crew.jsonc writes it at its top-level name, the
	// name CrewAI gives the crew; empty where it writes none, as a classic
	// project's files never do.
	name string
	// shadowed names the .json files the crew's files declare that the import
	// does not read, since the .jsonc file of the same crew or agent was handed.
	shadowed []string
	// others names the members of crew.jsonc the draft does not read.
	others []string
	// notes are what the reading found and the draft cannot write, each named in a
	// comment.
	notes []string
}

// agent is one agent a crew declares.
type agent struct {
	// name is the agent's name: its key in config/agents.yaml, or the name of its
	// agents/<name>.jsonc file.
	name string
	// file is the declaration file the agent is read from.
	file string
	// ref names the agent in a report; key is its instance's key in the draft.
	ref, key string
	// role, goal and backstory are the texts the agent is declared with; nil where
	// the declaration states none.
	role, goal, backstory *string
	// delegation is allow_delegation as declared, false where the declaration
	// states none; verbose likewise for verbose. Each note says why a value that
	// is not true or false is not carried.
	delegation, verbose         bool
	delegationNote, verboseNote string
	// llm is the model the agent declares.
	llm llm
	// tools are the tools the agent declares, by name.
	tools []string
	// others names the members of the declaration the draft does not carry.
	others []string
	// missing is set for an agent crew.jsonc names whose file the import was not
	// handed.
	missing bool
	// part is the model part the agent is wired to, nil where none.
	part *modelPart
}

// llm is the model an agent declares under llm: a provider and a model, read off
// a provider/model string or the model an llm object names, or why neither is.
type llm struct {
	// text is the llm as declared: the string, or the model an object names.
	text string
	// provider and model are text split at its first slash.
	provider, model string
	// object is set for an llm written as an object; members are its members other
	// than the one naming the model, by name in sorted order.
	object  bool
	members []member
	// why says why the agent's model is not wired, empty where it is.
	why string
}

// member is one member of an llm object the draft does not write as a value:
// its name and its text, and whether the text is a value that can carry a key.
type member struct {
	name, text string
	// located is set for a string, an object or a list, written as its text; a
	// number or a boolean carries no key.
	located bool
	// emptied is set where the credential decision wrote the text empty.
	emptied bool
}

// task is one task a crew declares: its name and the agent it is assigned to.
type task struct {
	name, agent string
}

// agentMembers are the members of an agent's declaration the draft writes, and
// the ones it reads to report: every other member is named as not carried.
var agentMembers = []string{"role", "goal", "backstory", "allow_delegation", "verbose", "llm", "tools", "settings"}

// readCrews groups an import's sources into crews and reads each: every source is
// parsed once, through parse, in the syntax its name gives it — YAML for a
// classic project's files, JSON with comments for the others, strict JSON being
// read the same way — or, for a source handed with no name, in the syntax its
// shape reads in. A crew.json or an agent's .json file the import was also handed
// the .jsonc spelling of is not read, as CrewAI does not read it, and is named. A
// source the parser refuses is reported under parse-failed and read no further.
// It returns the crews in the order their first file was handed, and the
// diagnostics the reading raised.
func readCrews(in aggrega.ImportInput, parse func(syntax string, source []byte) (json.RawMessage, error)) (
	[]*crew, []aggrega.Diagnostic) {
	var crews []*crew
	var diags []aggrega.Diagnostic
	byDir := map[string]*crew{}
	crewOf := func(dir string) *crew {
		c := byDir[dir]
		if c == nil {
			c = &crew{dir: dir}
			byDir[dir] = c
			crews = append(crews, c)
		}
		return c
	}
	sources := importer.Sources(in)
	handed := map[string]bool{}
	for _, src := range sources {
		handed[src.Name] = true
	}
	for _, src := range sources {
		kind, dir := declarationOf(src.Name)
		if (kind == declaresCrew || kind == declaresAgent) && strings.HasSuffix(src.Name, ".json") && handed[src.Name+"c"] {
			c := crewOf(dir)
			c.shadowed = append(c.shadowed, src.Name)
			continue
		}
		var tree json.RawMessage
		var err error
		if kind == declaresNothing {
			kind, tree, err = shapeOf(src.Bytes, parse)
		} else {
			syntax := SyntaxJSONC
			if kind == declaresAgents || kind == declaresTasks {
				syntax = SyntaxYAML
			}
			tree, err = parse(syntax, src.Bytes)
		}
		if err != nil {
			diags = append(diags, importer.ParseFailed(importer.Named(in, src), in.Format, err))
			continue
		}
		c := crewOf(dir)
		if src.Name != "" {
			c.files = append(c.files, src.Name)
		}
		if in.Format != Format {
			continue
		}
		switch kind {
		case declaresAgents:
			c.readAgents(tree, src)
		case declaresTasks:
			c.readTasks(tree, src.Bytes)
		case declaresCrew:
			c.readCrew(tree)
		case declaresAgent:
			c.agents = append(c.agents, readAgent(agentName(src.Name), src.Name, objectOf(tree)))
		}
	}
	for _, c := range crews {
		c.orderListed()
	}
	return crews, diags
}

// declarationOf reads what a source declares off its slash-separated name, and
// the project directory it sits in: a file named agents.yaml or tasks.yaml —
// under config/ in a classic project, or under its base name where an
// attachment names it — sits in the directory above config/; crew.jsonc, or
// crew.json, in its own directory; and any other .jsonc or .json file is an agent,
// in the directory above agents/. A source handed with no name declares what its
// shape says.
func declarationOf(name string) (declaration, string) {
	if name == "" {
		return declaresNothing, ""
	}
	segments := strings.Split(name, "/")
	base, parent := segments[len(segments)-1], segments[:len(segments)-1]
	above := func(dir string) string {
		if len(parent) > 0 && parent[len(parent)-1] == dir {
			return strings.Join(parent[:len(parent)-1], "/")
		}
		return strings.Join(parent, "/")
	}
	switch {
	case base == "agents.yaml":
		return declaresAgents, above("config")
	case base == "tasks.yaml":
		return declaresTasks, above("config")
	case base == "crew.jsonc" || base == "crew.json":
		return declaresCrew, strings.Join(parent, "/")
	case strings.HasSuffix(base, ".jsonc") || strings.HasSuffix(base, ".json"):
		return declaresAgent, above("agents")
	}
	return declaresNothing, strings.Join(parent, "/")
}

// shapeOf reads what a source handed with no name declares off its shape, and
// returns its tree: read as JSON with comments, an object holding a list at
// agents is a crew.jsonc and one holding role an agent's file; read as YAML, a
// mapping holding an entry with role is a config/agents.yaml and one holding an
// entry with description or expected_output a config/tasks.yaml. A source that
// reads in neither syntax is refused with the reason of the syntax its first
// character opens — an object's brace JSON with comments, anything else YAML.
func shapeOf(source []byte, parse func(syntax string, source []byte) (json.RawMessage, error)) (
	declaration, json.RawMessage, error) {
	jsoncTree, jsoncErr := parse(SyntaxJSONC, source)
	if jsoncErr == nil {
		top := objectOf(jsoncTree)
		if _, ok := listOf(top["agents"]); ok {
			return declaresCrew, jsoncTree, nil
		}
		if _, ok := top["role"]; ok {
			return declaresAgent, jsoncTree, nil
		}
	}
	yamlTree, yamlErr := parse(SyntaxYAML, source)
	if yamlErr != nil {
		if jsoncErr != nil && bytes.HasPrefix(bytes.TrimSpace(source), []byte("{")) {
			return declaresNothing, nil, jsoncErr
		}
		return declaresNothing, nil, yamlErr
	}
	for _, entry := range objectOf(yamlTree) {
		fields := objectOf(entry)
		if _, ok := fields["role"]; ok {
			return declaresAgents, yamlTree, nil
		}
	}
	for _, entry := range objectOf(yamlTree) {
		fields := objectOf(entry)
		_, described := fields["description"]
		_, expected := fields["expected_output"]
		if described || expected {
			return declaresTasks, yamlTree, nil
		}
	}
	return declaresNothing, yamlTree, nil
}

// agentName is the name of the agent an agents/<name>.jsonc or agents/<name>.json
// file declares, and agent for one handed with no name, which names none.
func agentName(file string) string {
	if file == "" {
		return "agent"
	}
	base := file[strings.LastIndex(file, "/")+1:]
	if name, ok := strings.CutSuffix(base, ".jsonc"); ok {
		return name
	}
	return strings.TrimSuffix(base, ".json")
}

// readAgents reads the agents a config/agents.yaml declares, in the order its
// text writes them, each from its entry. An entry that is not a mapping is no
// agent, and is named in a note.
func (c *crew) readAgents(tree json.RawMessage, src aggrega.ImportSource) {
	top := objectOf(tree)
	for _, name := range yamlorder.KeyOrder(src.Bytes, nil, sortedKeys(top)) {
		fields := objectOf(top[name])
		if fields == nil {
			c.notes = append(c.notes, "the entry "+draftyaml.Comment(name)+" of "+draftyaml.Comment(nameOr(src.Name))+
				" is not a mapping, so it declares no agent")
			continue
		}
		c.agents = append(c.agents, readAgent(name, src.Name, fields))
	}
}

// readTasks reads the tasks a config/tasks.yaml declares, in the order its text
// writes them, each with the agent its entry assigns it to.
func (c *crew) readTasks(tree json.RawMessage, text []byte) {
	top := objectOf(tree)
	for _, name := range yamlorder.KeyOrder(text, nil, sortedKeys(top)) {
		assigned, _ := draftyaml.ScalarText(objectOf(top[name])["agent"])
		c.tasks = append(c.tasks, task{name: name, agent: assigned})
	}
}

// readCrew reads a crew.jsonc: the crew's name, at its top-level name, where
// written as a scalar; the agents its agents list names, in its order; its
// tasks, in their order, each by its name — or its place in the list where it
// names none — with the agent it is assigned to; and the names of its other
// members, which the draft does not read.
func (c *crew) readCrew(tree json.RawMessage) {
	top := objectOf(tree)
	crewName, named := draftyaml.ScalarText(top["name"])
	if named && crewName != "" {
		c.name = crewName
	}
	if items, ok := listOf(top["agents"]); ok {
		for _, item := range items {
			if name, ok := draftyaml.ScalarText(item); ok {
				c.listed = append(c.listed, name)
			}
		}
	}
	if items, ok := listOf(top["tasks"]); ok {
		for i, item := range items {
			fields := objectOf(item)
			name, ok := draftyaml.ScalarText(fields["name"])
			if !ok || name == "" {
				name = "tasks." + strconv.Itoa(i)
			}
			assigned, _ := draftyaml.ScalarText(fields["agent"])
			c.tasks = append(c.tasks, task{name: name, agent: assigned})
		}
	}
	for _, member := range sortedKeys(top) {
		if member != "agents" && member != "tasks" && (member != "name" || !named) {
			c.others = append(c.others, member)
		}
	}
}

// orderListed puts the agents crew.jsonc names first, in its order, followed by
// every other agent the crew's files declare, in the order the import was handed
// them; an agent it names whose file the import was not handed is kept in its
// place, as missing.
func (c *crew) orderListed() {
	if len(c.listed) == 0 {
		return
	}
	var ordered []*agent
	taken := map[*agent]bool{}
	for _, name := range c.listed {
		i := slices.IndexFunc(c.agents, func(a *agent) bool { return a.name == name && !taken[a] })
		if i < 0 {
			if !slices.ContainsFunc(ordered, func(a *agent) bool { return a.name == name }) {
				ordered = append(ordered, &agent{name: name, missing: true})
			}
			continue
		}
		taken[c.agents[i]] = true
		ordered = append(ordered, c.agents[i])
	}
	for _, a := range c.agents {
		if !taken[a] {
			ordered = append(ordered, a)
		}
	}
	c.agents = ordered
}

// readAgent reads one agent's declaration: its role, goal and backstory; its
// allow_delegation and verbose, each read at the top of the declaration or under
// settings, settings winning, as CrewAI reads them; its llm; its tools, by name;
// and the names of its other members.
func readAgent(name, file string, fields map[string]json.RawMessage) *agent {
	a := &agent{name: name, file: file}
	a.role, a.goal, a.backstory = textOf(fields["role"]), textOf(fields["goal"]), textOf(fields["backstory"])
	settings := objectOf(fields["settings"])
	option := func(key string) (json.RawMessage, string) {
		if raw, ok := settings[key]; ok {
			return raw, "settings." + key
		}
		return fields[key], key
	}
	if raw, at := option("allow_delegation"); raw != nil {
		if !boolOf(raw, &a.delegation) {
			a.delegationNote = at + ", since it is not true or false"
		}
	}
	if raw, at := option("verbose"); raw != nil {
		if !boolOf(raw, &a.verbose) {
			a.verboseNote = at + ", since it is not true or false"
		}
	}
	a.llm = readLLM(fields["llm"])
	if items, ok := listOf(fields["tools"]); ok {
		for _, item := range items {
			a.tools = append(a.tools, toolName(item))
		}
	}
	for _, key := range sortedKeys(fields) {
		if !slices.Contains(agentMembers, key) {
			a.others = append(a.others, key)
		}
	}
	for _, key := range sortedKeys(settings) {
		if key != "allow_delegation" && key != "verbose" {
			a.others = append(a.others, "settings."+key)
		}
	}
	return a
}

// readLLM reads an agent's llm: a string naming a provider and a model across its
// first slash, or an object naming its model under model, model_name or
// deployment_name — the members CrewAI reads a model from — with every other
// member kept to be named where the draft states it. Anything else, an llm
// naming no provider, and one the declaration leaves out, say why, in words that
// follow "its llm".
func readLLM(raw json.RawMessage) llm {
	if raw == nil || string(raw) == "null" {
		return llm{why: "is not declared, so CrewAI reads the model from the environment its program runs in"}
	}
	var l llm
	if text, ok := textValue(raw); ok {
		l.text = text
	} else if fields := objectOf(raw); fields != nil {
		if _, ok := fields["python"]; ok {
			return llm{why: "is a reference into the crew's program, which the import does not read"}
		}
		l.object = true
		named := ""
		for _, key := range []string{"model", "model_name", "deployment_name"} {
			if text, ok := textValue(fields[key]); ok && text != "" {
				l.text, named = text, key
				break
			}
		}
		for _, key := range sortedKeys(fields) {
			if key == named {
				continue
			}
			text, located := memberText(fields[key])
			l.members = append(l.members, member{name: key, text: text, located: located})
		}
		if named == "" {
			l.why = "is an object naming no model under model, model_name or deployment_name"
			return l
		}
	} else {
		return llm{why: "is neither a model's name nor an object naming one"}
	}
	provider, model, found := strings.Cut(l.text, "/")
	if !found || provider == "" || model == "" {
		l.why = "names no provider before a slash, so it names either a model whose provider CrewAI resolves when " +
			"the program runs or an llm the program defines"
		return l
	}
	l.provider, l.model = provider, model
	return l
}

// toolName is the name a tool entry of an agent is reported by: a string as it
// is, and an object by its name member, or as a tool object where it has none.
func toolName(raw json.RawMessage) string {
	if text, ok := textValue(raw); ok {
		return text
	}
	if name, ok := textValue(objectOf(raw)["name"]); ok && name != "" {
		return name
	}
	return "a tool object"
}

// memberText is the text an llm object's member is written as: a string's text,
// a number's or a boolean's as written, and an object or a list as its JSON text.
// It reports whether the text is one that can carry a key — every text but a
// number's or a boolean's.
func memberText(raw json.RawMessage) (string, bool) {
	if text, ok := textValue(raw); ok {
		return text, true
	}
	if text, ok := draftyaml.ScalarText(raw); ok {
		return text, false
	}
	var compact bytes.Buffer
	if json.Compact(&compact, raw) != nil {
		return string(raw), true
	}
	return compact.String(), true
}

// textOf returns a member's text — a string's, or a number's or a boolean's as
// written — or nil where the member is absent or holds no scalar.
func textOf(raw json.RawMessage) *string {
	if text, ok := draftyaml.ScalarText(raw); ok {
		return &text
	}
	return nil
}

// textValue returns a string member's text.
func textValue(raw json.RawMessage) (string, bool) {
	var text string
	if raw == nil || json.Unmarshal(raw, &text) != nil {
		return "", false
	}
	return text, true
}

// boolOf reads a boolean member into v, reporting whether it is one.
func boolOf(raw json.RawMessage, v *bool) bool {
	return json.Unmarshal(raw, v) == nil && (string(bytes.TrimSpace(raw)) == "true" ||
		string(bytes.TrimSpace(raw)) == "false")
}

// objectOf returns the members of an object, or nil for anything else.
func objectOf(raw json.RawMessage) map[string]json.RawMessage {
	var fields map[string]json.RawMessage
	if raw == nil || json.Unmarshal(raw, &fields) != nil {
		return nil
	}
	return fields
}

// listOf returns the items of a list, reporting whether raw is one.
func listOf(raw json.RawMessage) ([]json.RawMessage, bool) {
	var items []json.RawMessage
	if raw == nil || json.Unmarshal(raw, &items) != nil || items == nil {
		return nil, false
	}
	return items, true
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

// nameOr returns a source's name, or "the source" for one handed with no name.
func nameOr(name string) string {
	if name == "" {
		return "the source"
	}
	return name
}
