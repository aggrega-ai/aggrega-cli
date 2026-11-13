// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package langgraphimport

import (
	"bytes"
	"encoding/json"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/draftyaml"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// application is one LangGraph application an import reads: its configuration,
// and the graphs, the checkpointer, the store and the environment it declares.
type application struct {
	// file is the configuration's slash-separated name, empty for a source handed
	// with no name; dir is the directory it sits in.
	file, dir string
	// prefix opens the reference of each graph, checkpointer and store of the
	// application in a report: its directory and a #, where the import reads more
	// than one application.
	prefix string
	// graphs are the graphs the configuration declares, in the order it writes
	// them.
	graphs []*graph
	// checkpointer is the checkpointer the configuration names; store the store it
	// configures, nil where it configures none.
	checkpointer checkpointer
	store        *store
	// env is the environment the configuration states.
	env environment
	// built names the members of the configuration langgraph build reads to build
	// its server image, and others the members the draft does not carry for
	// another reason, each in the order the configuration writes them.
	built, others []string
	// part is the datastore the application's graphs are wired to, nil where none.
	part *datastorePart
}

// name names the application's configuration in a report: its name, or "the
// configuration" for one handed with no name.
func (a *application) name() string {
	if a.file == "" {
		return "the configuration"
	}
	return a.file
}

// graph is one graph a configuration declares.
type graph struct {
	// id is the graph's id, its key under graphs.
	id string
	// ref names the graph in a report; key is its instance's key in the draft.
	ref, key string
	// entrypoint is the import path of the graph in the application's code, as the
	// configuration writes it; empty where the entry names none, and why says why.
	entrypoint, why string
	// description is the graph's description, nil where the configuration gives
	// none.
	description *string
}

// summary summarises a graph's declaration for its decision.
func (g *graph) summary() string {
	if g.entrypoint == "" {
		return "no import path"
	}
	return "import path " + g.entrypoint
}

// checkpointer is the checkpointer a configuration names: the backend LangGraph
// reads from it, as the configuration writes it, and why it names no datastore.
type checkpointer struct {
	// backend is the backend LangGraph reads: the configuration's checkpointer.backend,
	// or defaultBackend where it names none; stated is the backend as written,
	// empty where the configuration writes none.
	backend, stated string
	// path is the import path of a custom checkpointer, as written.
	path string
	// members names the members of checkpointer the draft does not carry, by name
	// in sorted order.
	members []string
	// why says why the checkpointer names no datastore, in words following "the
	// checkpointer", empty where it names one.
	why string
}

// summary summarises a checkpointer naming no datastore for its decision.
func (c checkpointer) summary() string {
	if c.stated == "" {
		return "no backend"
	}
	return "backend " + c.stated
}

// store is the store a configuration configures: the names of its members, by
// name in sorted order.
type store struct {
	members []string
}

// summary summarises a store for its decision: the members it configures.
func (s *store) summary() string {
	if len(s.members) == 0 {
		return "no member"
	}
	return strings.Join(s.members, ", ")
}

// environment is the environment a configuration's env states.
type environment struct {
	// entries are the variables an env written as an object names, in the order it
	// writes them.
	entries []envEntry
	// file is the environment file an env written as a path names, as written;
	// read is the name of the source it was read from, empty where the import was
	// not handed it.
	file, read string
	// names are the variables the file names, in its order, and skipped the
	// numbers of its lines naming none.
	names   []string
	skipped []int
	// note says why an env that is neither an object nor a path states nothing.
	note string
}

// envEntry is one variable an env written as an object names, with its value.
type envEntry struct {
	name string
	// text is the value's text: a string's, a number's or a boolean's as written,
	// and an object's or a list's as its JSON text. located is set where the value
	// holds something, which the credential decision then judges, and emptied
	// where it wrote the value empty.
	text             string
	located, emptied bool
}

// The members of a configuration langgraph build reads to build its server
// image, which the draft does not carry: the image the Release names is the
// application's own.
var buildMembers = []string{
	"_INTERNAL_docker_tag", "api_version", "base_image", "dependencies", "dockerfile_lines", "image_distro",
	"keep_pkg_tools", "node_version", "pip_config_file", "pip_installer", "python_version", "source",
}

// readApplications reads an import's sources: every source named like an
// environment file — one the attachment's with.sources hands, since a directory
// hands none — is kept aside, and every other is a configuration, parsed once
// through parse as JSON and read. Each configuration's environment file is then
// read, where the import was handed it, for its variables' names. A
// configuration the parser refuses is reported under parse-failed and read no
// further. It returns the applications in the order their configuration was
// handed, the names of the environment files no configuration names, in the
// order handed, and the diagnostics the reading raised.
func readApplications(in aggrega.ImportInput, parse func(syntax string, source []byte) (json.RawMessage, error)) (
	[]*application, []string, []aggrega.Diagnostic) {
	var apps []*application
	var diags []aggrega.Diagnostic
	envFiles := map[string][]byte{}
	var envOrder []string
	for _, src := range sourcesOf(in) {
		if src.Name != "" && isEnvFile(src.Name) {
			envFiles[src.Name] = src.Bytes
			envOrder = append(envOrder, src.Name)
			continue
		}
		if _, err := parse(Syntax, src.Bytes); err != nil {
			diags = append(diags, importer.ParseFailed(importer.Named(in, src), in.Format, err))
			continue
		}
		a := &application{file: src.Name, dir: dirOf(src.Name)}
		if in.Format == Format {
			a.read(src.Bytes)
		}
		apps = append(apps, a)
	}
	named := map[string]bool{}
	for _, a := range apps {
		if a.env.file == "" {
			continue
		}
		if name, ok := a.envSource(envFiles); ok {
			a.env.read = name
			a.env.names, a.env.skipped = variableNames(envFiles[name])
			named[name] = true
		}
	}
	var unread []string
	for _, name := range envOrder {
		if !named[name] {
			unread = append(unread, name)
		}
	}
	return apps, unread, diags
}

// sourcesOf returns the sources an import request carries: its primary bytes,
// where it carries any, as a source with no name — a configuration imported as a
// file — followed by its named sources — a directory's files and those the
// attachment configuring the importer names under with.sources. A file imported
// with an attachment naming sources is handed as both, and its environment file
// may be among the named ones, so the primary bytes are read beside them. A
// request carrying neither is read as one empty source.
func sourcesOf(in aggrega.ImportInput) []aggrega.ImportSource {
	if len(in.Bytes) == 0 {
		return importer.Sources(in)
	}
	return append([]aggrega.ImportSource{{Bytes: in.Bytes}}, in.Sources...)
}

// isEnvFile reports whether a slash-separated source name is named like an
// environment file: its last segment matches EnvFiles.
func isEnvFile(name string) bool {
	ok, err := path.Match(EnvFiles, name[strings.LastIndex(name, "/")+1:])
	return err == nil && ok
}

// dirOf returns the directory a slash-separated source name sits in, empty for
// one at the top or handed with no name.
func dirOf(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[:i]
	}
	return ""
}

// byteOrderMark is the UTF-8 byte-order mark a text may open with, which a JSON
// reader reads from after.
const byteOrderMark = "\xef\xbb\xbf"

// read reads a configuration's text, which the parser has accepted as JSON: its
// members in the order the text writes them, each member's last writing read as
// its value, as a JSON reader reads one written twice.
func (a *application) read(text []byte) {
	text = bytes.TrimPrefix(text, []byte(byteOrderMark))
	for _, e := range members(text) {
		switch {
		case e.Key == "graphs":
			a.readGraphs(e.Value)
		case e.Key == "checkpointer":
			a.checkpointer = readCheckpointer(e.Value)
		case e.Key == "store":
			if string(bytes.TrimSpace(e.Value)) != "null" {
				a.store = &store{members: sortedKeys(objectOf(e.Value))}
			}
		case e.Key == "env":
			a.env = readEnvironment(e.Value)
		case e.Key == "$schema":
		case slices.Contains(buildMembers, e.Key):
			a.built = append(a.built, e.Key)
		default:
			a.others = append(a.others, e.Key)
		}
	}
	if a.checkpointer.backend == "" && a.checkpointer.why == "" {
		a.checkpointer.backend = defaultBackend
	}
}

// readGraphs reads the graphs a configuration's graphs object declares, in the
// order it writes them: each id with the import path its entry names — the entry
// itself where it is a string, its path where it is an object — and the
// description an object gives.
func (a *application) readGraphs(raw json.RawMessage) {
	for _, e := range members(raw) {
		g := &graph{id: e.Key}
		if text, ok := textValue(e.Value); ok {
			g.entrypoint = text
		} else if fields := objectOf(e.Value); fields != nil {
			if text, ok := textValue(fields["path"]); ok {
				g.entrypoint = text
			}
			if text, ok := textValue(fields["description"]); ok {
				g.description = &text
			}
			if g.entrypoint == "" {
				g.why = "is an object naming no import path under path"
			}
		} else {
			g.why = "is neither an import path nor an object naming one under path"
		}
		if g.entrypoint == "" && g.why == "" {
			g.why = "names an empty import path"
		}
		a.graphs = append(a.graphs, g)
	}
}

// readCheckpointer reads a configuration's checkpointer: the backend it names —
// default where it names none, as LangGraph reads it — the path of a custom
// checkpointer, and the names of its other members. A checkpointer that is
// null is read as one naming nothing.
func readCheckpointer(raw json.RawMessage) checkpointer {
	if string(bytes.TrimSpace(raw)) == "null" {
		return checkpointer{backend: defaultBackend}
	}
	fields := objectOf(raw)
	if fields == nil {
		return checkpointer{why: "is not an object, so it names no backend"}
	}
	c := checkpointer{backend: defaultBackend}
	if b, ok := fields["backend"]; ok {
		text, isText := textValue(b)
		switch {
		case !isText:
			c.stated = strings.TrimSpace(string(b))
			c.why = "names its backend as something other than a text, so it names no datastore"
		case text == "custom":
			c.stated, c.backend = text, text
			c.why = "is custom: a checkpointer the application's code defines"
		default:
			c.stated, c.backend = text, text
			if _, mapped := backends[text]; !mapped {
				c.why = "names the backend " + draftyaml.Comment(text) + ", which the import maps to no datastore"
			}
		}
	}
	if p, ok := textValue(fields["path"]); ok {
		c.path = p
		if c.backend == "custom" {
			c.why += " at " + draftyaml.Comment(p)
		}
	}
	for _, key := range sortedKeys(fields) {
		if key != "backend" && key != "path" {
			c.members = append(c.members, key)
		}
	}
	return c
}

// readEnvironment reads a configuration's env: the variables an object names, in
// the order it writes them, each with its value's text; or the path a string
// names. Anything else states no environment, and its note says why.
func readEnvironment(raw json.RawMessage) environment {
	if text, ok := textValue(raw); ok {
		if text == "" {
			return environment{note: "env names an empty path, so it states no environment"}
		}
		return environment{file: text}
	}
	if string(bytes.TrimSpace(raw)) == "null" {
		return environment{}
	}
	if objectOf(raw) == nil {
		return environment{note: "env is neither an object nor a path, so it states no environment"}
	}
	var env environment
	for _, e := range members(raw) {
		entry := envEntry{name: e.Key}
		switch v := bytes.TrimSpace(e.Value); {
		case string(v) == "null":
		default:
			entry.text = valueText(e.Value)
			entry.located = entry.text != ""
		}
		env.entries = append(env.entries, entry)
	}
	return env
}

// envSource returns the name of the source the application's environment file
// was read from: the one named by the last segment of the path its env names, as
// the importer's with.sources hands a file, under its base name. It reports false
// where the import was not handed it — always for a directory import, which hands
// no environment file.
func (a *application) envSource(envFiles map[string][]byte) (string, bool) {
	file := strings.ReplaceAll(a.env.file, "\\", "/")
	base := file[strings.LastIndex(file, "/")+1:]
	if _, ok := envFiles[base]; ok {
		return base, true
	}
	return "", false
}

// variableNames reads the names of the variables an environment file of
// KEY=VALUE lines names, in the order it names them, and the numbers of its
// lines naming none — a line that is not blank, not a comment, and not NAME=
// followed by a value, the NAME optionally opened by export. A value is never
// read: a value opened by a quote that its line does not close continues on the
// lines up to the one closing it, and those lines name nothing. A name named
// twice is kept once, at its first line.
func variableNames(text []byte) (names []string, skipped []int) {
	lines := strings.Split(strings.TrimPrefix(string(text), byteOrderMark), "\n")
	seen := map[string]bool{}
	open := byte(0)
	for i, line := range lines {
		line = strings.TrimRight(line, "\r")
		if open != 0 {
			if closes(line, open) {
				open = 0
			}
			continue
		}
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if rest, ok := strings.CutPrefix(t, "export "); ok {
			t = strings.TrimSpace(rest)
		}
		name, value, found := strings.Cut(t, "=")
		name = strings.TrimSpace(name)
		if !found || !variableName(name) {
			skipped = append(skipped, i+1)
			continue
		}
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
		value = strings.TrimSpace(value)
		if value != "" && (value[0] == '"' || value[0] == '\'') && !closes(value[1:], value[0]) {
			open = value[0]
		}
	}
	return names, skipped
}

// closes reports whether text holds the quote that closes a quoted value: the
// quote character, not preceded by a backslash where it is a double quote.
func closes(text string, quote byte) bool {
	for i := 0; i < len(text); i++ {
		switch {
		case quote == '"' && text[i] == '\\':
			i++
		case text[i] == quote:
			return true
		}
	}
	return false
}

// variableName reports whether name is written as a variable's name: a letter or
// an underscore followed by letters, digits, underscores, dots or hyphens.
func variableName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		letter := r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if i == 0 && !letter {
			return false
		}
		if !letter && !(r >= '0' && r <= '9') && r != '.' && r != '-' {
			return false
		}
	}
	return true
}

// members returns the members of a JSON object in the order the text writes
// them, through the plugin kit's order-preserving reader, since the parser's
// tree writes them in sorted order; a member the text writes more than once is
// kept at its first place with its last value.
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

// valueText is the text a value is written as: a string's text, a number's or a
// boolean's as written, and an object or a list as its JSON text.
func valueText(raw json.RawMessage) string {
	if text, ok := draftyaml.ScalarText(raw); ok {
		return text
	}
	var compact bytes.Buffer
	if json.Compact(&compact, raw) != nil {
		return string(raw)
	}
	return compact.String()
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

// sortedKeys returns the keys of m in sorted order.
func sortedKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// lineNumbers names lines by their numbers: "line 8", or "lines 2, 3".
func lineNumbers(numbers []int) string {
	out := make([]string, 0, len(numbers))
	for _, n := range numbers {
		out = append(out, strconv.Itoa(n))
	}
	if len(out) == 1 {
		return "line " + out[0]
	}
	return "lines " + strings.Join(out, ", ")
}
