// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package adkimport

import (
	"bytes"
	"encoding/json"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/draftyaml"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/importer"
)

// application is one agent tree an import reads: the configuration of its root
// agent, and every agent the tree's configurations name, read once each.
type application struct {
	// file is the root configuration's slash-separated name, empty for a source
	// handed with no name.
	file string
	// prefix opens the reference of each agent and tool of the tree in a report:
	// the root configuration's directory and a #, where the import reads more than
	// one tree.
	prefix string
	// root is the tree's root agent; agents are every agent the tree reads, the
	// root first, in the order ADK builds them: each agent, then its sub-agents in
	// the order its configuration lists them.
	root   *agent
	agents []*agent
	// files are the configurations read, in the order the tree reads them.
	files []string
	// key is the instance's key in the draft.
	key string
	// part is the model the instance is wired to, nil where none; modeled is the
	// agent whose model it serves.
	part    *modelPart
	modeled *agent
	// stated are the values the tree's configurations hand their tools and their
	// model requests, each written in the Release, in the order the draft writes
	// them.
	stated []statedValue
}

// agent is one agent a configuration declares.
type agent struct {
	// file is the configuration the agent is read from, empty for a source handed
	// with no name.
	file string
	// parent is the agent whose sub_agents names this one, nil for a root.
	parent *agent
	// name is the agent's name as declared; ref names it in a report.
	name, ref string
	// class is agent_class as declared, empty where the configuration states none.
	class string
	// description is the agent's description, nil where it states none.
	description *string
	// model is the model the agent names; modelCode is the code reference its
	// model_code names, which builds its model in the application's code.
	model, modelCode string
	// tools are the tools the agent names, in its order.
	tools []tool
	// subs are the agent's sub_agents entries, in its order.
	subs []*subAgent
	// requests are the members of the HTTP options its generate_content_config
	// hands its model requests, by name in sorted order, written under
	// requestsKey, as the configuration spells it.
	requests    []member
	requestsKey string
}

// summary summarises an agent's declaration for its decision: its class, its
// model and how many tools and sub-agents it names.
func (a *agent) summary() string {
	parts := []string{"class " + a.className()}
	switch {
	case a.model != "":
		parts = append(parts, "model "+a.model)
	case a.modelCode != "":
		parts = append(parts, "model_code "+a.modelCode)
	}
	if n := len(a.tools); n > 0 {
		parts = append(parts, count(n, "tool"))
	}
	if n := len(a.subs); n > 0 {
		parts = append(parts, count(n, "sub-agent"))
	}
	return strings.Join(parts, ", ")
}

// className is the agent class the agent is built as: its agent_class, or
// LlmAgent, which ADK builds where the configuration names none.
func (a *agent) className() string {
	if a.class == "" {
		return "LlmAgent"
	}
	return a.class
}

// deprecated names the class ADK deprecates the agent's class in favour of, and
// the class it deprecates, empty where ADK deprecates neither.
func (a *agent) deprecated() string {
	return deprecatedClasses[a.class]
}

// subAgent is one entry of an agent's sub_agents: a configuration it names by
// config_path, read where the attachment's with.sources hands it, or an agent
// the application's code builds, named by code.
type subAgent struct {
	// configPath is the entry's config_path as written; path is the name of the
	// source it reads: the last segment of the path, the name with.sources hands a
	// file under.
	configPath, path string
	// code is the entry's code reference.
	code string
	// agent is the agent read from the configuration, nil where none is.
	agent *agent
	// why says why no agent is read, in words following "the sub-agent", empty
	// where one is; notHanded is set where the reason is that the import was not
	// handed the configuration, which the draft then says how to hand.
	why       string
	notHanded bool
}

// label names the entry in a report: the agent read, or what the entry names.
func (s *subAgent) label() string {
	switch {
	case s.agent != nil:
		return s.agent.ref
	case s.code != "":
		return s.code
	}
	return s.configPath
}

// tool is one entry of an agent's tools: the tool's name and the arguments its
// configuration hands it, by name in sorted order.
type tool struct {
	name string
	args []member
}

// own reports whether the tool is one of ADK's own: named bare, as ADK names
// the tools it ships, or by a name inside ADK's own package. Any other tool is
// code the application defines.
func (t tool) own() bool {
	return !strings.Contains(t.name, ".") || strings.HasPrefix(t.name, adkPackage)
}

// member is one member of a tool's args or of an agent's HTTP options: its name
// and its text, and whether the text is a value that can carry a key.
type member struct {
	name, text string
	// located is set for a string, an object or a list, written as its text; a
	// number, a boolean and null carry no key.
	located bool
}

// statedValue is one value a tree's configurations state that the draft writes
// in the Release: the agent stating it, the field it is written under, and the
// member.
type statedValue struct {
	agent *agent
	field string
	member
	// emptied is set where the credential decision wrote the text empty.
	emptied bool
}

// deprecatedClasses are the spellings of agent_class naming an agent class ADK
// deprecates in favour of Workflow, each mapped to the class it names.
var deprecatedClasses = map[string]string{
	"LoopAgent":                                          "LoopAgent",
	"google.adk.agents.LoopAgent":                        "LoopAgent",
	"google.adk.agents.loop_agent.LoopAgent":             "LoopAgent",
	"ParallelAgent":                                      "ParallelAgent",
	"google.adk.agents.ParallelAgent":                    "ParallelAgent",
	"google.adk.agents.parallel_agent.ParallelAgent":     "ParallelAgent",
	"SequentialAgent":                                    "SequentialAgent",
	"google.adk.agents.SequentialAgent":                  "SequentialAgent",
	"google.adk.agents.sequential_agent.SequentialAgent": "SequentialAgent",
}

// adkPackage opens the name of every tool ADK ships, where a configuration
// names one by its full name.
const adkPackage = "google.adk."

// reader reads an import's sources as agent configurations: each source parsed
// at most once, through parse, and a refusal reported once.
type reader struct {
	in      aggrega.ImportInput
	parse   func(syntax string, source []byte) (json.RawMessage, error)
	sources []aggrega.ImportSource
	trees   map[int]json.RawMessage
	failed  map[int]bool
	diags   []aggrega.Diagnostic
}

// newReader indexes an import's sources: the primary bytes, where the request
// carries any, beside its named sources.
func newReader(in aggrega.ImportInput, parse func(syntax string, source []byte) (json.RawMessage, error)) *reader {
	r := &reader{in: in, parse: parse, trees: map[int]json.RawMessage{}, failed: map[int]bool{}}
	if len(in.Bytes) > 0 {
		r.sources = append(r.sources, aggrega.ImportSource{Bytes: in.Bytes})
	}
	r.sources = append(r.sources, in.Sources...)
	return r
}

// tree returns the parsed tree of the source at index i, parsing it once; a
// source the parser refuses is reported under parse-failed once and yields nil.
func (r *reader) tree(i int) json.RawMessage {
	if t, ok := r.trees[i]; ok || r.failed[i] {
		return t
	}
	t, err := r.parse(Syntax, r.sources[i].Bytes)
	if err != nil {
		r.failed[i] = true
		r.diags = append(r.diags, importer.ParseFailed(importer.Named(r.in, r.sources[i]), r.in.Format, err))
		return nil
	}
	r.trees[i] = t
	return t
}

// index returns the index of the named source name, -1 where none is.
func (r *reader) index(name string) int {
	return slices.IndexFunc(r.sources, func(s aggrega.ImportSource) bool { return s.Name != "" && s.Name == name })
}

// roots returns the indexes of the sources read as root configurations, in the
// order the import was handed them: the primary bytes — a configuration imported
// on its own, whatever its name — and every named source called RootFile, the
// one file a directory hands the importer. Every other named source is one the
// attachment's with.sources hands, read only as a configuration a root's tree
// names, so a request holding no root reads no tree.
func (r *reader) roots() []int {
	var out []int
	for i, src := range r.sources {
		if src.Name == "" || path.Base(src.Name) == RootFile {
			out = append(out, i)
		}
	}
	return out
}

// handedName is the name of the source a resolved config_path reads: its last
// segment, the name the attachment's with.sources hands a file under. A
// directory hands the importer no file but root_agent.yaml, so a sub-agent's
// configuration is read only where with.sources hands it.
func handedName(p string) string {
	return path.Base(p)
}

// readApplication reads the agent tree whose root configuration is the source at
// index i: the root agent, then each sub-agent its configurations name, depth
// first in the order each lists them. It returns nil where the root
// configuration does not parse or declares no agent.
func (r *reader) readApplication(i int) *application {
	tree := r.tree(i)
	fields := objectOf(tree)
	if fields == nil {
		return nil
	}
	app := &application{file: r.sources[i].Name}
	read := map[string]bool{}
	var walk func(parent *agent, file string, fields map[string]json.RawMessage) *agent
	walk = func(parent *agent, file string, fields map[string]json.RawMessage) *agent {
		a := readAgent(file, fields)
		a.parent = parent
		app.agents = append(app.agents, a)
		app.files = append(app.files, file)
		for _, item := range listOf(fields["sub_agents"]) {
			s := &subAgent{}
			a.subs = append(a.subs, s)
			s.configPath, s.code = entryOf(item)
			switch {
			case s.configPath == "" && s.code != "":
				s.why = "is built by the application's code at " + s.code + ", which the import does not read"
				continue
			case s.configPath == "":
				s.why = "names neither a configuration under config_path nor code"
				continue
			}
			p, why := resolve(file, s.configPath)
			if why != "" {
				s.why = why
				continue
			}
			s.path = handedName(p)
			if read[s.path] {
				s.why = "names " + s.configPath + ", which this tree reads already"
				continue
			}
			j := r.index(s.path)
			if j < 0 {
				s.why = "names " + s.configPath + ", which the import was not handed"
				s.notHanded = true
				continue
			}
			sub := objectOf(r.tree(j))
			if sub == nil {
				if !r.failed[j] {
					s.why = "names " + s.path + ", which declares no agent"
				} else {
					s.why = "names " + s.path + ", which the parser refuses"
				}
				continue
			}
			read[s.path] = true
			s.agent = walk(a, s.path, sub)
		}
		return a
	}
	read[app.file] = true
	app.root = walk(nil, app.file, fields)
	return app
}

// resolve resolves a config_path against the configuration naming it, as ADK
// does: from that configuration's directory, refusing an absolute path and one
// that leads out of that directory, which ADK refuses too. why says why a path
// is refused, empty where it resolves.
func resolve(file, configPath string) (string, string) {
	if path.IsAbs(configPath) {
		return "", "names the absolute path " + configPath + ", which ADK refuses"
	}
	dir := path.Dir(file)
	if file == "" {
		dir = "."
	}
	p := path.Join(dir, configPath)
	rel := p
	if dir != "." {
		rel = strings.TrimPrefix(p, dir+"/")
		if rel == p {
			rel = ".."
		}
	}
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", "names " + configPath + ", which leads out of the directory of the configuration naming it, " +
			"which ADK refuses"
	}
	return p, ""
}

// entryOf reads one sub_agents entry: an object naming a configuration under
// config_path or an agent of the application's code under code, or a string,
// which ADK reads as a config_path.
func entryOf(item json.RawMessage) (configPath, code string) {
	if text, ok := textValue(item); ok {
		return text, ""
	}
	fields := objectOf(item)
	configPath, _ = textValue(fields["config_path"])
	code, _ = textValue(fields["code"])
	return configPath, code
}

// readAgent reads one agent's configuration: its name, class and description;
// the model it names, or the code its model_code names; its tools; and the HTTP
// options its generate_content_config hands its model requests.
func readAgent(file string, fields map[string]json.RawMessage) *agent {
	a := &agent{file: file}
	a.name, _ = textValue(fields["name"])
	a.class, _ = textValue(fields["agent_class"])
	if text, ok := textValue(fields["description"]); ok && text != "" {
		a.description = &text
	}
	a.model, _ = textValue(fields["model"])
	if code := objectOf(fields["model_code"]); code != nil {
		a.modelCode, _ = textValue(code["name"])
	} else if text, ok := textValue(fields["model_code"]); ok {
		a.modelCode = text
	}
	for _, item := range listOf(fields["tools"]) {
		t := tool{}
		if text, ok := textValue(item); ok {
			t.name = text
		} else {
			entry := objectOf(item)
			t.name, _ = textValue(entry["name"])
			t.args = membersOf(objectOf(entry["args"]))
		}
		a.tools = append(a.tools, t)
	}
	config := objectOf(fields["generate_content_config"])
	for _, key := range []string{"http_options", "httpOptions"} {
		if options := objectOf(config[key]); options != nil {
			a.requests, a.requestsKey = membersOf(options), key
			break
		}
	}
	return a
}

// membersOf returns the members of an object, by name in sorted order, each with
// its text.
func membersOf(fields map[string]json.RawMessage) []member {
	var out []member
	for _, name := range sortedKeys(fields) {
		text, located := memberText(fields[name])
		out = append(out, member{name: name, text: text, located: located})
	}
	return out
}

// memberText is the text a member is written as: a string's text, a number's or
// a boolean's as written, null as null, and an object or a list as its JSON
// text. It reports whether the text is one that can carry a key — a string's, an
// object's or a list's.
func memberText(raw json.RawMessage) (string, bool) {
	if text, ok := textValue(raw); ok {
		return text, true
	}
	if text, ok := draftyaml.ScalarText(raw); ok {
		return text, false
	}
	if string(bytes.TrimSpace(raw)) == "null" {
		return "null", false
	}
	var compact bytes.Buffer
	if json.Compact(&compact, raw) != nil {
		return string(raw), true
	}
	return compact.String(), true
}

// textValue returns a string member's text.
func textValue(raw json.RawMessage) (string, bool) {
	var text string
	if raw == nil || json.Unmarshal(raw, &text) != nil {
		return "", false
	}
	return text, true
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

// count writes n things, the noun made plural where n is not one.
func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}
