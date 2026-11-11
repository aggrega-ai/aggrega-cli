// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package composeimport

import (
	"encoding/json"
	"path"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/draftyaml"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/yamlorder"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/importer"
)

// project is one Compose project an import reads: the Compose file Compose
// reads in a directory, with the override file Compose merges over it, and the
// services and models the two hold, merged as Compose merges them.
type project struct {
	// files are the project's files in the order they are merged: the Compose
	// file, then its override file where the directory holds one.
	files []aggrega.ImportSource
	// notRead names the files of the directory Compose does not read, since
	// another file of the same kind precedes them.
	notRead []string
	// name is the project's name as its top-level name member writes it, the
	// last file to write one winning as Compose merges it; empty where none does.
	name string
	// services and models are the project's, in the order the files write them.
	services []*service
	models   []*model
	// others names the top-level members the draft does not read.
	others []string
}

// service is one service of a project, its files merged, as the draft reads it.
type service struct {
	// name is the service's name in the source; ref names it in a report, and
	// key is its instance's key in the drafts.
	name, ref, key string
	// image is the image the service runs, as written; empty where it names
	// none.
	image string
	// build is set for a service that builds its image.
	build bool
	// ports, expose and volumes are as the source writes them, merged.
	ports   []port
	expose  []string
	volumes []volume
	// env is the service's environment, in the order the source writes it.
	env []envVar
	// dependsOn names the services it depends on, and models the models it
	// uses, each once, sorted.
	dependsOn, models []string
	// modelVars holds, for each model a long-form entry of models names, the
	// variables the entry names the model's address and identifier under, in
	// the order the entries are first read.
	modelVars []modelVars
	// others names the members of the service the draft does not carry, sorted.
	others []string
}

// modelVars is what one long-form entry of a service's models states: the model
// it names, and the variables it names the model's address and identifier under
// — its endpoint_var and its model_var — each empty where the entry names none.
// The names are carried as the source writes them and never read for meaning.
type modelVars struct {
	model, endpoint, identifier string
}

// port is one entry of a service's ports: its text as written, the key Compose
// merges entries by, and the port the container listens on.
type port struct {
	text, key, target string
}

// volume is one entry of a service's volumes: its text as written and the path
// in the container Compose merges entries by.
type volume struct {
	text, target string
}

// envVar is one variable of a service's environment: its name, and its value
// where the source writes one.
type envVar struct {
	name  string
	value *string
}

// model is one model a project's top-level models declares.
type model struct {
	name, ref, key string
	// identifier is the model's OCI identifier, its model member; nil where
	// the source writes none.
	identifier *string
	// contextSize is the context_size member as the tree writes it; nil where
	// the source writes none.
	contextSize json.RawMessage
	// runtimeFlags are the runtime_flags, in order.
	runtimeFlags []string
	// others names the members the draft does not carry, sorted.
	others []string
	// component is the Component the model's instance is of, and referenced is
	// set where it is ModelRuntime, which the catalog holds, rather than a
	// Component drafted for the model; both are set by its decision.
	component  string
	referenced bool
}

// projectsOf groups the sources an import reads into Compose projects, as
// Compose reads a directory: in each directory, by the order of the directories'
// names, the first present of BaseFileNames is the project's Compose file and
// the first present of OverrideFileNames is merged over it; every other of those
// files is not read. A source of any other name — one handed to the import by
// itself, or by an attachment under a name of its own — is a project of its own.
// A directory holding override files and no Compose file reads its first override
// file as its Compose file.
func projectsOf(sources []aggrega.ImportSource) []*project {
	byDir := map[string][]aggrega.ImportSource{}
	var dirs []string
	var out []*project
	for _, src := range sources {
		base := path.Base(src.Name)
		if src.Name == "" || (!slices.Contains(BaseFileNames, base) && !slices.Contains(OverrideFileNames, base)) {
			out = append(out, &project{files: []aggrega.ImportSource{src}})
			continue
		}
		dir := path.Dir(src.Name)
		if _, seen := byDir[dir]; !seen {
			dirs = append(dirs, dir)
		}
		byDir[dir] = append(byDir[dir], src)
	}
	slices.Sort(dirs)
	for _, dir := range dirs {
		files := byDir[dir]
		first := func(names []string) (aggrega.ImportSource, bool) {
			for _, n := range names {
				for _, f := range files {
					if path.Base(f.Name) == n {
						return f, true
					}
				}
			}
			return aggrega.ImportSource{}, false
		}
		p := &project{}
		base, hasBase := first(BaseFileNames)
		override, hasOverride := first(OverrideFileNames)
		if !hasBase {
			base, hasBase, hasOverride = override, true, false
		}
		p.files = append(p.files, base)
		if hasOverride {
			p.files = append(p.files, override)
		}
		for _, f := range files {
			if f.Name != base.Name && (!hasOverride || f.Name != override.Name) {
				p.notRead = append(p.notRead, f.Name)
			}
		}
		out = append(out, p)
	}
	return out
}

// read merges one file's parsed tree into the project, its services and models
// in the order text writes them: a service or a model the project already
// holds is merged with what the file says of it, as Compose merges an override
// file, and a new one follows those it holds. A top-level name written as a
// scalar names the project, replacing what an earlier file named it.
func (p *project) read(tree json.RawMessage, text []byte) {
	var top map[string]json.RawMessage
	if json.Unmarshal(tree, &top) != nil {
		return
	}
	projectName, named := scalarText(top["name"])
	if named && projectName != "" {
		p.name = projectName
	}
	for _, member := range sortedKeys(top) {
		if member == "services" || member == "models" || (member == "name" && named) {
			continue
		}
		if !slices.Contains(p.others, member) {
			p.others = append(p.others, member)
		}
	}
	slices.Sort(p.others)

	var services map[string]json.RawMessage
	_ = json.Unmarshal(top["services"], &services)
	for _, name := range yamlorder.KeyOrder(text, []string{"services"}, sortedKeys(services)) {
		var fields map[string]json.RawMessage
		if json.Unmarshal(services[name], &fields) != nil || fields == nil {
			continue
		}
		p.service(name).merge(fields, func(names []string) []string {
			return yamlorder.KeyOrder(text, []string{"services", name, "environment"}, names)
		})
	}

	var models map[string]json.RawMessage
	_ = json.Unmarshal(top["models"], &models)
	for _, name := range yamlorder.KeyOrder(text, []string{"models"}, sortedKeys(models)) {
		var fields map[string]json.RawMessage
		if json.Unmarshal(models[name], &fields) != nil || fields == nil {
			continue
		}
		p.model(name).merge(fields)
	}
}

// service returns the project's service named name, adding it where the project
// holds none.
func (p *project) service(name string) *service {
	for _, s := range p.services {
		if s.name == name {
			return s
		}
	}
	s := &service{name: name}
	p.services = append(p.services, s)
	return s
}

// model returns the project's model named name, adding it where the project
// holds none.
func (p *project) model(name string) *model {
	for _, m := range p.models {
		if m.name == name {
			return m
		}
	}
	m := &model{name: name}
	p.models = append(p.models, m)
	return m
}

// merge merges what one file says of the service into it, as Compose merges an
// override file over a Compose file: the image replaced; ports merged by their
// address, protocol and container port, volumes by their path in the container,
// the environment by variable name, a later entry replacing an earlier one in
// its place and a new one following; expose appended; depends_on and models
// joined, a long-form models entry's endpoint_var and model_var each replacing
// what an earlier file states for that model. A member written null is read as
// absent. envOrder returns the names of an environment written as a mapping in
// the order the file writes them.
func (s *service) merge(fields map[string]json.RawMessage, envOrder func([]string) []string) {
	for _, name := range sortedKeys(fields) {
		raw := fields[name]
		if string(raw) == "null" {
			continue
		}
		switch name {
		case "image":
			if text, ok := scalarText(raw); ok {
				s.image = text
			}
		case "build":
			s.build = true
			s.addOther(name)
		case "ports":
			for _, item := range itemsOf(raw) {
				p := portOf(item)
				if i := slices.IndexFunc(s.ports, func(q port) bool { return q.key == p.key }); i >= 0 {
					s.ports[i] = p
				} else {
					s.ports = append(s.ports, p)
				}
			}
		case "expose":
			for _, item := range itemsOf(raw) {
				if text, ok := scalarText(item); ok {
					s.expose = append(s.expose, text)
				}
			}
		case "volumes":
			for _, item := range itemsOf(raw) {
				v := volumeOf(item)
				if i := slices.IndexFunc(s.volumes, func(w volume) bool { return w.target == v.target }); i >= 0 {
					s.volumes[i] = v
				} else {
					s.volumes = append(s.volumes, v)
				}
			}
		case "environment":
			for _, e := range envOf(raw, envOrder) {
				if i := slices.IndexFunc(s.env, func(f envVar) bool { return f.name == e.name }); i >= 0 {
					s.env[i] = e
				} else {
					s.env = append(s.env, e)
				}
			}
		case "depends_on":
			s.dependsOn = joined(s.dependsOn, namesOf(raw))
		case "models":
			s.models = joined(s.models, namesOf(raw))
			for _, e := range importer.ObjectEntries(raw) {
				s.readModelVars(e.Key, e.Value)
			}
		default:
			s.addOther(name)
		}
	}
}

// readModelVars reads one long-form entry of the service's models, naming model:
// each of its endpoint_var and model_var that is a scalar replaces what the
// service holds for that model. An entry naming neither — a short-form item, or
// a mapping entry written empty — leaves nothing.
func (s *service) readModelVars(model string, entry json.RawMessage) {
	for _, f := range importer.ObjectEntries(entry) {
		text, ok := scalarText(f.Value)
		if !ok || text == "" || (f.Key != "endpoint_var" && f.Key != "model_var") {
			continue
		}
		i := slices.IndexFunc(s.modelVars, func(v modelVars) bool { return v.model == model })
		if i < 0 {
			s.modelVars = append(s.modelVars, modelVars{model: model})
			i = len(s.modelVars) - 1
		}
		if f.Key == "endpoint_var" {
			s.modelVars[i].endpoint = text
		} else {
			s.modelVars[i].identifier = text
		}
	}
}

// varsOf returns what the service's long-form models entry for model states, and
// whether it states either variable.
func (s *service) varsOf(model string) (modelVars, bool) {
	i := slices.IndexFunc(s.modelVars, func(v modelVars) bool { return v.model == model })
	if i < 0 {
		return modelVars{}, false
	}
	return s.modelVars[i], true
}

// addOther names a member of the service the draft does not carry.
func (s *service) addOther(name string) {
	if !slices.Contains(s.others, name) {
		s.others = append(s.others, name)
		slices.Sort(s.others)
	}
}

// merge merges what one file says of the model into it: its model and its
// context_size replaced, its runtime_flags appended.
func (m *model) merge(fields map[string]json.RawMessage) {
	for _, name := range sortedKeys(fields) {
		raw := fields[name]
		if string(raw) == "null" {
			continue
		}
		switch name {
		case "model":
			if text, ok := scalarText(raw); ok {
				m.identifier = &text
			}
		case "context_size":
			m.contextSize = raw
		case "runtime_flags":
			for _, item := range itemsOf(raw) {
				if text, ok := scalarText(item); ok {
					m.runtimeFlags = append(m.runtimeFlags, text)
				}
			}
		default:
			if !slices.Contains(m.others, name) {
				m.others = append(m.others, name)
				slices.Sort(m.others)
			}
		}
	}
}

// portOf reads one entry of ports: in the short syntax, [ip:][published:]target
// with an optional /protocol, or in the long syntax, an object of host_ip,
// published, target and protocol.
func portOf(raw json.RawMessage) port {
	var ip, published, target, protocol string
	text, ok := scalarText(raw)
	if ok {
		rest := text
		if i := strings.LastIndexByte(rest, '/'); i >= 0 {
			rest, protocol = rest[:i], rest[i+1:]
		}
		if strings.HasPrefix(rest, "[") {
			if end := strings.Index(rest, "]:"); end >= 0 {
				ip, rest = rest[:end+1], rest[end+2:]
			}
		}
		parts := strings.Split(rest, ":")
		target = parts[len(parts)-1]
		if len(parts) >= 2 {
			published = parts[len(parts)-2]
		}
		if len(parts) >= 3 {
			ip = strings.Join(parts[:len(parts)-2], ":")
		}
	} else {
		text = strings.TrimSpace(string(raw))
		for _, f := range importer.ObjectEntries(raw) {
			value, _ := scalarText(f.Value)
			switch f.Key {
			case "host_ip":
				ip = value
			case "published":
				published = value
			case "target":
				target = value
			case "protocol":
				protocol = value
			}
		}
	}
	if protocol == "" {
		protocol = "tcp"
	}
	return port{text: text, key: ip + "|" + published + "|" + target + "|" + protocol, target: target}
}

// volumeOf reads one entry of volumes: in the short syntax, [source:]target with
// an optional :mode, or in the long syntax, an object holding target.
func volumeOf(raw json.RawMessage) volume {
	if text, ok := scalarText(raw); ok {
		parts := strings.Split(text, ":")
		target := parts[0]
		if len(parts) >= 2 {
			target = parts[1]
		}
		return volume{text: text, target: target}
	}
	v := volume{text: strings.TrimSpace(string(raw))}
	for _, f := range importer.ObjectEntries(raw) {
		if f.Key == "target" {
			v.target, _ = scalarText(f.Value)
		}
	}
	return v
}

// envOf reads a service's environment: a mapping of variable to value, in the
// order order returns its names, or a list of NAME=value items, in the list's
// order; a name with no value takes its value from the environment Compose runs
// in, and so is written with none.
func envOf(raw json.RawMessage, order func([]string) []string) []envVar {
	var out []envVar
	if entries := importer.ObjectEntries(raw); entries != nil {
		values := map[string]json.RawMessage{}
		var names []string
		for _, e := range entries {
			values[e.Key] = e.Value
			names = append(names, e.Key)
		}
		for _, name := range order(names) {
			v := envVar{name: name}
			if text, ok := scalarText(values[name]); ok {
				v.value = &text
			}
			out = append(out, v)
		}
		return out
	}
	for _, item := range itemsOf(raw) {
		text, ok := scalarText(item)
		if !ok {
			continue
		}
		name, value, hasValue := strings.Cut(text, "=")
		v := envVar{name: name}
		if hasValue {
			v.value = &value
		}
		out = append(out, v)
	}
	return out
}

// namesOf reads the names a depends_on or a service's models member lists: the
// items of a list, or the keys of a mapping.
func namesOf(raw json.RawMessage) []string {
	var out []string
	if entries := importer.ObjectEntries(raw); entries != nil {
		for _, e := range entries {
			out = append(out, e.Key)
		}
		return out
	}
	for _, item := range itemsOf(raw) {
		if text, ok := scalarText(item); ok {
			out = append(out, text)
		}
	}
	return out
}

// joined returns the names of a and b, each once, sorted.
func joined(a, b []string) []string {
	out := append(slices.Clone(a), b...)
	slices.Sort(out)
	return slices.Compact(out)
}

// itemsOf returns the items of a JSON list, none for any other value.
func itemsOf(raw json.RawMessage) []json.RawMessage {
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}
	return items
}

// sortedKeys returns the keys of m, sorted.
func sortedKeys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// scalarText is the drafts' shared reading of a scalar of the parsed tree.
var scalarText = draftyaml.ScalarText
