// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package composeimport is the Compose importer: it reads the services and the
// models a Compose application declares and drafts an Aggregate holding one
// instance per service — of the catalog Component its image matches, or of the
// standard library's generic server, core/compute/server, running its image —
// connected as its depends_on says, one drafted model Component per model, and a
// Release beside it carrying what each service states for its operation.
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
// recorded as a hint in its decision and draws nothing. Each model is drafted as
// a model Component of its own, its identifier, context size and runtime flags
// written as values, and a service naming it is connected to it.
//
// The Release, release.yaml beside aggregate.yaml, names the Aggregate and
// carries per service what the source states for its operation: for an instance
// of core/compute/server, its image and the port its container listens on as
// infra.image and infra.port; and for every service, in a comment above its
// entry, the image, ports, volumes and environment the source writes and no key
// of the draft carries, for the author to place where the Component takes them.
// The Release's environment and its Manifest are left unset, each named in a
// comment, since no source names either and the import drafts no Manifest.
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

	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/importer"
)

// Format is the one format the importer serves: a Compose application.
const Format = "compose"

// Syntax is the syntactic format a source is parsed as.
const Syntax = "yaml"

// Server is the catalog Component a service no image row maps is drafted as an
// instance of.
const Server = "core/compute/server"

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
// per service; where the empty_credentials setting holds a value it does not
// take, it returns that halt and drafts nothing. Given one request and one
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
				out.Diagnostics = append(out.Diagnostics, importer.ParseFailed(src, in.Format, err))
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
			out.Diagnostics = append(out.Diagnostics, importer.NoNodes(p.files[0], in.Format))
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

	types := map[*service]string{}
	drafted := map[string]bool{}
	var components []aggrega.EmittedEntity
	for _, p := range read {
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
		for _, m := range p.models {
			name := modelName(in.Settings, m)
			components = append(components, aggrega.EmittedEntity{Kind: "Component", Name: name, Mode: "draft",
				Body: renderModel(name), SourceRef: m.ref})
		}
	}

	name := importer.TargetNamespace(in.Settings) + "/flow"
	aggregate := renderAggregate(name, in.Settings, read, types)
	release := renderRelease(name, read, types)
	out.Entities = append(components,
		aggrega.EmittedEntity{Kind: "Aggregate", Name: name, Mode: "draft", Body: aggregate},
		aggrega.EmittedEntity{Kind: "Release", Name: name, Mode: "draft", Body: release})
	out.Files = importer.Render(in.Settings, out.Entities, out.Decisions)
	if slices.ContainsFunc(out.Files, func(f aggrega.ScaffoldFile) bool { return f.Path == "aggregate.yaml" }) {
		out.Files = append(out.Files, aggrega.ScaffoldFile{Path: "release.yaml", Content: release})
	}
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
// way, hints are recorded in the decision's rationale.
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
	if len(hints) > 0 {
		decision.Rationale += "; " + strings.Join(hints, "; ")
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

// modelName is the name a model's drafted Component is given: the
// target_namespace setting's namespace, the ai-model category and its instance
// key.
func modelName(settings json.RawMessage, m *model) string {
	return importer.TargetNamespace(settings) + "/ai-model/" + m.key
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
