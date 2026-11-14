// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package composeimport is the Compose importer: it reads the services and the
// models a Compose application declares and drafts an Aggregate holding one
// instance per service — of the catalog Component its image matches, or of the
// standard library's generic server, core/compute/server, running its image —
// connected as its depends_on says, one instance per model — of the standard
// library's Docker Model Runner runtime, core/ai-model/docker-model-runner, where
// the workspace's catalog holds it, and of a model Component drafted for it
// otherwise — and a Release beside it carrying what each service states for its
// operation.
//
// The importer guest (plugins/compose-importer) holds the descriptor, the
// signature a Compose file is recognised by and the export wiring; everything it
// does with a source is here, so it compiles and is tested on the host toolchain
// as well.
//
// # What a source holds
//
// A Compose file lists its services under services, each running an image — or
// building one — with the ports, the volumes and the environment it runs with,
// and the services it depends on under depends_on; and the models its services
// use under models, each the OCI identifier of a model, its context_size and its
// runtime_flags, a service naming the models it uses under its own models. A
// directory is read as Compose reads one: in each directory the first present of
// compose.yaml, compose.yml, docker-compose.yml and docker-compose.yaml, with the
// first present of compose.override.yml, compose.override.yaml,
// docker-compose.override.yml and docker-compose.override.yaml merged over it as
// Compose merges an override file. The drafted Aggregate states the files read,
// in that order, and any it does not read.
//
// The host's parse decides whether a source parses and gives every value; the
// order a file lists its services and its models in is read from its text, since
// the parser's tree writes the members of a mapping in sorted order.
//
// # What it drafts
//
// Each service's image is matched through the image table: a table from image
// repository to the Component a Compose Recipe of the hub runs an image of that
// repository for, generated from the hub's Compose Recipes when the importer is
// built and embedded in it, with the mapping_overrides setting layered over it
// for the compose format, keyed by repository. A service whose image a row maps
// is decided as every importer built on the plugin kit decides a node — scored
// against the workspace catalog's Components, referenced where the reuse mode
// auto-applies a HIGH match and drafted as a new Component otherwise, with the
// match recorded as a hint. A service no row maps, or that builds its image, is
// an instance of core/compute/server, its image written into the Release.
// depends_on draws a connection from each service to each service it depends on;
// an environment value naming another service the service does not depend on is
// recorded as a hint in its decision and draws nothing. Each model is an instance
// of ModelRuntime where the catalog the import is handed holds a Component of that
// name, whatever the reuse mode says, and no Component is drafted for it: its
// identifier is written as values.model, its context size as
// values.context_window, the key the runtime declares for a model's context
// length, and its runtime flags as values.runtime_flags. Where the catalog holds
// no ModelRuntime, the model is drafted as a model Component of its own, its
// identifier, context size and runtime flags written as values of the names
// Compose gives them. Every model's decision says which, and why. A service
// naming a model is connected to it, either way. A service naming
// a model in the long form, with an endpoint_var or a model_var, names the
// variables the model's address and identifier reach it under; no key of the
// draft holds a variable's name, so each is named, as the entry writes it, in a
// comment above the connection and in the service's decision.
//
// The Aggregate and the Release are named after the project, as
// importer.DraftName names a draft: after the name its top-level name member
// gives it, where every project read gives the same one, and otherwise after the
// directory the import was handed, as Compose names a project after the
// directory holding it, or after the file the import was handed on its own.
//
// The Release, release.yaml beside aggregate.yaml, names the Aggregate and
// carries per service what the source states for its operation: for an instance
// of core/compute/server, its image and the port its container listens on as
// infra.image and infra.port; and for every service, in a comment above its
// entry, the image, ports, volumes and environment the source writes and no key
// of the draft carries, for the author to place where the Component takes them;
// and an empty entry for each model's instance, of ModelRuntime or drafted,
// since a build places only the instances a Release names and no source states
// infrastructure for a model. It is written by the plugin kit's Release writer,
// importer.DraftRelease, whose documentation states what every drafted Release
// declares beside its entries.
//
// # Credential values
//
// Compose marks no environment value as a credential, and a variable's name is
// not read for what it means, so every environment value that holds something is
// an input its format marks as no credential that can carry one. Each is handed
// to the plugin kit's credential decision, importer.CarryCredentials, which reads
// the empty_credentials setting: by default each is carried as written into the
// Release and named in a credential-carried warning by its service and its
// variable, never by its value; where the setting is all, each is written empty
// and reported under credential-emptied, its variable named in the Release with
// no value. A value that is one variable reference and nothing else — ${NAME} or
// $NAME — holds no value of its own, and is written as the text it is.
package composeimport

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// Format is the one format the importer serves: a Compose application.
const Format = "compose"

// Syntax is the syntactic format a source is parsed as.
const Syntax = "yaml"

// Server is the catalog Component a service no image row maps is drafted as an
// instance of.
const Server = "core/compute/server"

// ModelRuntime is the catalog Component a model of the top-level models is an
// instance of where the workspace's catalog holds it: the standard library's
// runtime serving a model, named by the OCI reference a Compose model writes,
// through Docker Model Runner.
const ModelRuntime = "core/ai-model/docker-model-runner"

// runtimeContextKey is the value ModelRuntime declares for a model's context
// length, which Compose writes as context_size: the context_window it takes from
// the standard library's shared model parameter.
const runtimeContextKey = "context_window"

// BaseFileNames are the names Compose reads a directory's Compose file under,
// in the order Compose prefers them; OverrideFileNames are the names of the
// override file it merges over it, in the same order of preference.
var (
	BaseFileNames     = []string{"compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml"}
	OverrideFileNames = []string{"compose.override.yml", "compose.override.yaml", "docker-compose.override.yml",
		"docker-compose.override.yaml"}
)

// where completes each credential report, naming where the value is written.
const where = "in the Release the draft writes"

// arm is the format as the kit decides a service by: its name, its syntax and
// the image table read as its mapping arm.
var arm = importer.Format{Name: Format, Syntax: Syntax, Mappings: mappings()}

// Import turns one import request into the drafts: the request's sources — its
// named sources, or its primary bytes — are grouped into Compose projects
// (projectsOf), and each file of each project is parsed as YAML through parse,
// the host's parser, and merged in Compose's order. A file the parser refuses is
// reported under parse-failed, which halts the import, and a project holding no
// service and no model under no-nodes; the import carries on with the other
// projects. A request naming a format other than Format reads nothing.
//
// Where any project holds a service or a model, it returns the drafted Aggregate
// and Release and the drafted Components, as files and entities, and a decision
// per model and per service, a project's models before its services: the kit's
// renderer reads a drafted Component's hint from the last decision naming its
// node, and a service and a model may share a name, so a service's decision is
// the one read for it. Where the empty_credentials setting holds a value it does
// not take, it returns that halt and drafts nothing. Given one request and one
// parser, it returns byte-identical output.
func Import(in aggrega.ImportInput, parse func(syntax string, source []byte) (json.RawMessage, error)) aggrega.ImportOutput {
	var out aggrega.ImportOutput
	projects := projectsOf(importer.Sources(in))
	var read []*project
	for _, p := range projects {
		failed := false
		for _, src := range p.files {
			tree, err := parse(Syntax, src.Bytes)
			if err != nil {
				out.Diagnostics = append(out.Diagnostics, importer.ParseFailed(importer.Named(in, src), in.Format, err))
				failed = true
				continue
			}
			if in.Format == Format {
				p.read(tree, src.Bytes)
			}
		}
		if failed {
			continue
		}
		if len(p.services) == 0 && len(p.models) == 0 {
			out.Diagnostics = append(out.Diagnostics, importer.NoNodes(importer.Named(in, p.files[0]), in.Format))
			continue
		}
		read = append(read, p)
	}
	if len(read) == 0 {
		return out
	}

	used := map[string]bool{}
	for _, p := range read {
		for _, s := range p.services {
			s.ref = refOf(read, p, s.name)
			s.key = importer.UniqueKey(used, s.name)
		}
		for _, m := range p.models {
			m.ref = refOf(read, p, m.name)
			m.key = importer.UniqueKey(used, m.name)
		}
	}

	// The hints are read off the environment as the source states it, before the
	// credential decision writes any value empty: a hint names a variable and a
	// service, never a value.
	hints := map[*service][]string{}
	for _, p := range read {
		for _, s := range p.services {
			hints[s] = environmentHints(p, s)
		}
	}

	written, diags, err := importer.CarryCredentials(in.Settings, draftedValues(read), where, located(read))
	if err != nil {
		out.Error = "compose importer: write the drafted values: " + err.Error()
		return out
	}
	out.Diagnostics = append(out.Diagnostics, diags...)
	if written == nil {
		return out
	}
	if err := readBack(read, written); err != nil {
		out.Error = "compose importer: read the drafted values back: " + err.Error()
		return out
	}

	held := holdsModelRuntime(in.Catalog)
	types := map[*service]string{}
	drafted := map[string]bool{}
	var components []aggrega.EmittedEntity
	for _, p := range read {
		var models []aggrega.EmittedEntity
		for _, m := range p.models {
			decision, entity := decideModel(in.Settings, p, m, held)
			out.Decisions = append(out.Decisions, decision)
			models = append(models, entity)
		}
		for _, s := range p.services {
			decision, entity := decide(in, s, hints[s])
			out.Decisions = append(out.Decisions, decision)
			types[s] = entity.Name
			if entity.Mode == "draft" {
				if drafted[entity.Name] {
					continue
				}
				drafted[entity.Name] = true
			}
			components = append(components, entity)
		}
		components = append(components, models...)
	}

	titles := make([]string, 0, len(read))
	for _, p := range read {
		titles = append(titles, p.name)
	}
	name := importer.DraftName(in, titles...)
	aggregate := aggrega.EmittedEntity{Kind: "Aggregate", Name: name, Mode: "draft",
		Body: renderAggregate(name, read, types)}
	out.Entities = append(append(components, aggregate),
		importer.DraftRelease(in, aggregate, composeRelease(read, types))...)
	out.Files = importer.Render(in.Settings, out.Entities, out.Decisions)
	return out
}

// refOf names a service or a model of p in a report: by its name, prefixed by the
// name of p's Compose file where the import reads more than one project.
func refOf(read []*project, p *project, name string) string {
	if len(read) == 1 || p.files[0].Name == "" {
		return name
	}
	return p.files[0].Name + "#" + name
}

// decide makes the decision for one service: a service whose image a row of the
// image table or of the mapping_overrides setting maps is decided by the kit; a
// service no row maps, or that names no image, is an instance of Server. Either
// way, hints, and the variables a long-form models entry names a model's address
// and identifier under, are recorded in the decision's rationale.
func decide(in aggrega.ImportInput, s *service, hints []string) (aggrega.ImportDecision, aggrega.EmittedEntity) {
	repository, ok := Repository(s.image)
	var decision aggrega.ImportDecision
	var entity aggrega.EmittedEntity
	mapped := false
	if ok {
		decision, entity, mapped = importer.Decide(in, arm, importer.Node{ID: s.ref, Type: repository}, s.ref)
	}
	if !mapped {
		tier := "NONE"
		if len(decision.Candidates) > 0 {
			tier = decision.Candidates[0].Tier
		}
		decision.SourceNode = aggrega.DecisionSourceNode{ID: s.ref, Type: repository, ParamsSummary: "no parameters"}
		decision.Chosen = &aggrega.DecisionChoice{Target: Server, Mode: "reference", Tier: tier}
		switch {
		case s.image == "":
			decision.Rationale = "the service names no image, so it is drafted as an instance of " + Server +
				" and its image is left for the Release"
		case !ok:
			decision.Rationale = "the image " + strconv.Quote(s.image) + " names no repository the import reads, so " +
				"the service is drafted as an instance of " + Server + " running it"
		default:
			decision.Rationale = "no row of the image table or of the mapping_overrides setting maps the repository " +
				repository + ", so the service is drafted as an instance of " + Server + " running its image"
		}
		entity = aggrega.EmittedEntity{Kind: "Component", Name: Server, Mode: "reference", SourceRef: s.ref,
			DecisionRef: "decision:" + s.ref}
	}
	if notes := append(slices.Clone(hints), modelVarsReports(s)...); len(notes) > 0 {
		decision.Rationale += "; " + strings.Join(notes, "; ")
	}
	return decision, entity
}

// environmentHints returns a hint for each environment variable of s whose value
// names another service of p that s does not depend on: a token of the value,
// split at every character a host name is not written with, equal to that
// service's name. The hint names the variable and the service, never the value,
// and draws no connection.
func environmentHints(p *project, s *service) []string {
	var hints []string
	for _, e := range s.env {
		if e.value == nil {
			continue
		}
		tokens := strings.FieldsFunc(*e.value, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.')
		})
		for _, other := range p.services {
			if other == s || slices.Contains(s.dependsOn, other.name) || !slices.Contains(tokens, other.name) {
				continue
			}
			hints = append(hints, "hint: the value of its environment variable "+e.name+" names the service "+
				other.name+", which its depends_on does not name, so the draft draws no connection to it")
		}
	}
	return hints
}

// modelVarsReports returns, for each model s names in the long form with an
// endpoint_var or a model_var, in the order s names its models, the report
// modelVarsReport writes of it with each name as it is.
func modelVarsReports(s *service) []string {
	var out []string
	for _, m := range s.models {
		if v, ok := s.varsOf(m); ok {
			out = append(out, modelVarsReport(s.ref, v, func(name string) string { return name }))
		}
	}
	return out
}

// modelVarsReport is the one sentence reporting the variables a service's
// long-form models entry names a model's address and identifier under — its
// endpoint_var and its model_var — as the entry names them, each name written
// through name: the sentence the service's decision records and the comment
// above its connection to the model states. No key of the draft holds a
// variable's name: the connection is written on a socket named after the model,
// which declares no value, and an importer is handed no Component's
// infrastructure keys. It starts in lower case, as a clause of the decision's
// rationale.
func modelVarsReport(serviceRef string, v modelVars, name func(string) string) string {
	type stated struct{ what, variable string }
	var parts []stated
	if v.endpoint != "" {
		parts = append(parts, stated{"address", v.endpoint})
	}
	if v.identifier != "" {
		parts = append(parts, stated{"identifier", v.identifier})
	}
	text := "the service " + name(serviceRef) + " reads the " + parts[0].what + " of the model " + name(v.model) +
		" as " + name(parts[0].variable)
	if len(parts) == 2 {
		return text + " and its " + parts[1].what + " as " + name(parts[1].variable) +
			", the names its models entry gives them; no key of the draft holds either name"
	}
	return text + ", the name its models entry gives it; no key of the draft holds that name"
}

// modelName is the name a model's drafted Component is given: the
// target_namespace setting's namespace, the ai-model category and its instance
// key.
func modelName(settings json.RawMessage, m *model) string {
	return importer.TargetNamespace(settings) + "/ai-model/" + m.key
}

// holdsModelRuntime reports whether the catalog view an import is handed holds
// a Component named ModelRuntime, at any version.
func holdsModelRuntime(catalog *aggrega.CatalogProjection) bool {
	if catalog == nil {
		return false
	}
	return slices.ContainsFunc(catalog.Components, func(c aggrega.CatalogComponent) bool { return c.Name == ModelRuntime })
}

// decideModel makes the decision for one model of p and the entity it emits, and
// records on the model the Component its instance is of: ModelRuntime, referenced,
// where held says the workspace's catalog holds it, and otherwise a model
// Component drafted for it, named by modelName. The rationale says which and why,
// the model it serves and the services of p naming it.
func decideModel(settings json.RawMessage, p *project, m *model, held bool) (aggrega.ImportDecision, aggrega.EmittedEntity) {
	d := aggrega.ImportDecision{SourceNode: aggrega.DecisionSourceNode{ID: m.ref, Type: "model",
		ParamsSummary: modelSummary(m)}}
	var serving string
	if m.identifier != nil {
		serving = ", serving " + *m.identifier
	}
	var users []string
	for _, s := range p.services {
		if slices.Contains(s.models, m.name) {
			users = append(users, s.ref)
		}
	}
	if len(users) > 0 {
		serving += ", used by " + strings.Join(users, ", ")
	}
	entity := aggrega.EmittedEntity{Kind: "Component", SourceRef: m.ref, DecisionRef: "decision:" + m.ref}
	if held {
		m.component, m.referenced = ModelRuntime, true
		d.Chosen = &aggrega.DecisionChoice{Target: ModelRuntime, Mode: "reference", Tier: "NONE"}
		d.Rationale = "the workspace's catalog holds " + ModelRuntime + ", which serves a model named by its OCI " +
			"reference through Docker Model Runner, so " + m.key + " is an instance of it" + serving
		entity.Name, entity.Mode = ModelRuntime, "reference"
		return d, entity
	}
	m.component = modelName(settings, m)
	d.Chosen = &aggrega.DecisionChoice{Target: m.component, Mode: "draft", Tier: "NONE"}
	d.Rationale = "the workspace's catalog holds no " + ModelRuntime + ", so the model is drafted as " + m.component +
		", a Component of its own" + serving
	entity.Name, entity.Mode, entity.Body = m.component, "draft", renderModel(m.component)
	return d, entity
}

// modelSummary summarises a model for its decision: the model it names, its
// context size where it states one, and how many runtime flags it passes.
func modelSummary(m *model) string {
	parts := []string{"no model"}
	if m.identifier != nil {
		parts[0] = "model " + *m.identifier
	}
	if m.contextSize != nil {
		if text, _, ok := contextSizeText(m.contextSize); ok {
			parts = append(parts, "context_size "+text)
		}
	}
	switch n := len(m.runtimeFlags); {
	case n == 1:
		parts = append(parts, "1 runtime flag")
	case n > 1:
		parts = append(parts, strconv.Itoa(n)+" runtime flags")
	}
	return strings.Join(parts, ", ")
}

// draftedValues writes, as one JSON document, the values of every service the
// credential decision judges: under each service's key, its environment's
// values, in the order the draft writes them, a variable the source writes no
// value for written with none.
func draftedValues(read []*project) []byte {
	doc := map[string]map[string][]*string{}
	for _, p := range read {
		for _, s := range p.services {
			values := make([]*string, 0, len(s.env))
			for _, e := range s.env {
				values = append(values, e.value)
			}
			doc[s.key] = map[string][]*string{"environment": values}
		}
	}
	raw, _ := json.Marshal(doc)
	return raw
}

// located returns every environment value of draftedValues the credential
// decision judges, in the order the draft writes them: each that holds
// something and is not one variable reference, named by its service and its
// variable, as an input its format marks as no credential.
func located(read []*project) []importer.Credential {
	var out []importer.Credential
	for _, p := range read {
		for _, s := range p.services {
			for j, e := range s.env {
				if e.value == nil || isReference(*e.value) {
					continue
				}
				out = append(out, importer.Credential{Node: s.ref, Field: "environment." + e.name,
					Pointer: importer.Pointer(s.key, "environment", strconv.Itoa(j)), Class: importer.CredentialUnmarked})
			}
		}
	}
	return out
}

// readBack reads the values the credential decision wrote empty back onto the
// services: an environment value that held something and is written empty is
// written with no value. A value the decision carried is left exactly as the
// source writes it.
func readBack(read []*project, written []byte) error {
	var values map[string]struct {
		Environment []*string `json:"environment"`
	}
	if err := json.Unmarshal(written, &values); err != nil {
		return err
	}
	for _, p := range read {
		for _, s := range p.services {
			got := values[s.key].Environment
			for j := range s.env {
				e := &s.env[j]
				if e.value == nil || *e.value == "" || j >= len(got) || got[j] == nil || *got[j] != "" {
					continue
				}
				e.value = nil
			}
		}
	}
	return nil
}

// isReference reports whether text is one variable reference Compose
// interpolates and nothing else — ${NAME} or $NAME — and so holds no value of
// its own. A reference carrying a default or an error message, ${NAME:-default}
// or ${NAME?message}, holds that text, and text holding anything beside the
// reference holds that; neither is a reference here.
func isReference(text string) bool {
	name := ""
	switch {
	case strings.HasPrefix(text, "${") && strings.HasSuffix(text, "}"):
		name = text[2 : len(text)-1]
	case strings.HasPrefix(text, "$"):
		name = text[1:]
	default:
		return false
	}
	if name == "" {
		return false
	}
	for i, r := range name {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}
