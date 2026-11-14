// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package langgraphimport is the LangGraph importer: it reads the graphs a
// LangGraph application declares in its configuration, langgraph.json, and
// drafts an Aggregate holding one instance of the standard library's LangGraph
// agent, core/ai-agent/langgraph, per graph, each wired to the datastore its
// checkpointer keeps state in, and a Release beside it carrying the environment
// the configuration states.
//
// The importer guest (plugins/langgraph-importer) holds the descriptor, the
// signatures a configuration is recognised by and the export wiring; everything
// it does with a source is here, so it compiles and is tested on the host
// toolchain as well.
//
// # What a configuration declares
//
// A LangGraph application keeps its graphs in code and declares them in
// langgraph.json: under graphs, each graph's id mapped to the import path of the
// graph in the application's code — module:attr, or ./file.py:attr — or to an
// object naming that path under path and describing the graph under
// description. The configuration also names the checkpointer that persists a
// graph's state between steps (checkpointer, its backend default, PostgreSQL;
// mongo, a store speaking MongoDB's protocol; or custom, a checkpointer the code
// defines at path), the
// memory store (store, its semantic-search index and the time its items live),
// and the environment the application runs with (env): an object of names to
// values, or the path of a file of KEY=VALUE lines. Its other members configure
// the server image langgraph build builds — dependencies, python_version,
// base_image and the like — or the server inside it.
//
// The importer reads the configuration and nothing else. The application's code
// is never among the sources: a directory is handed to the importer as the file
// its signatures name, langgraph.json, which a program's files are not, and no
// environment file is handed with it. What the code does is the author's to
// bring, in the image the Release runs.
//
// # What it drafts
//
// Each graph is an instance of core/ai-agent/langgraph keyed by its id, in the
// order the configuration writes them: graph_entrypoint the graph's import path,
// written as the configuration writes it, and description the graph's
// description where the configuration gives one. recursion_limit is not written,
// so the Component's default stands, since the configuration states none.
//
// The checkpointer's backend is a datastore: default, or a configuration naming
// no backend, which LangGraph reads as default, is PostgreSQL, an instance of
// core/datastore/postgres, which, where the request's catalog view holds no
// Component of that name, is drafted under the target namespace,
// <target_namespace>/datastore/postgres; mongo is a store speaking MongoDB's
// protocol, an instance of core/datastore/ferretdb wired on its backend socket to
// an instance of core/datastore/documentdb, the PostgreSQL it keeps its data in,
// as the catalog's FerretDB stack wires the two, where the catalog view holds
// both. Each graph of an application is wired to its application's datastore on
// its checkpointer_db socket. A custom checkpointer, a backend the importer does
// not map, and mongo where the catalog view lacks either Component, name no
// datastore: the socket is left for the author and the draft says why. The store
// names no backend — LangGraph keeps it in the PostgreSQL database its server
// uses — and its decision says so.
//
// The datastore keeps one password, which the configuration does not name: it is
// attached by reference, through the plugin kit's importer.Secret, read from a
// variable named after the datastore's key followed by _PASSWORD, as the
// datastore's own, as FerretDB's on its connection to DocumentDB, and as each
// graph's on its checkpointer_db connection, each declared, for compose, with the
// key its Recipes read it under.
//
// The configuration names no model. Where the catalog view holds
// core/ai-model/openai, whose client library's variables the catalog's LangGraph
// Recipes hand a graph, one instance of it is drafted and every graph is wired to
// it on its model socket, its values.model written empty for the author to name
// the model, and a key attached on each graph's model connection, read from the
// variable OpenAI's client libraries read; otherwise each graph's model socket is
// left for the author, and the draft says so, the build halting naming the
// socket until one is wired.
//
// The Aggregate and the Release are named after the graph, as
// importer.DraftName names a draft: a configuration carries no name of its own,
// so where every configuration read declares one graph, and all of them the
// same id, the draft is named after that id, and otherwise after the directory
// or the file the import was handed.
//
// The Release, release.yaml beside aggregate.yaml, names the Aggregate and leaves
// for the Release each graph's image: the application's own, its code with
// LangGraph installed, rather than the server image langgraph build builds, and
// a comment above the first graph names the variables the image is handed. Each
// datastore, and the drafted model, has an entry of its own, so the Release
// places it with the graphs, each attaching the secrets drafted for it. The Release is written by the plugin kit's Release writer,
// importer.DraftRelease, whose documentation states what every drafted Release
// declares beside its entries.
//
// # The environment
//
// Every variable the configuration's env names is an input the Release
// supplies, and none is drafted as a credential slot: LangGraph marks no
// variable as a secret, and no variable's name is read for what it means. The
// environment is written in a comment above the application's first graph in
// the Release, since core/ai-agent/langgraph takes no value for it.
//
// An env written as an object is part of the configuration, so each of its
// values is carried as written, and each value holding something is handed to
// the plugin kit's credential decision, importer.CarryCredentials, as an input
// its format marks as no credential that can carry one: by default it is carried
// as written and named in a credential-carried warning by its configuration and
// its variable, never by its value; where the empty_credentials setting is all,
// it is written with no value and reported under credential-emptied.
//
// An env naming a file names a file the configuration only points at, which
// commonly holds local secrets. Its variables' names are read, and its values
// never: the file is read only where the importer's with.sources hands it, under
// the last segment of the path the configuration names — a directory import
// hands no environment file, and one the import is not handed is named as not
// handed, with how to hand it — and each line naming a variable gives its name
// alone; a line naming no variable is named by its number. No value of the file
// reaches a draft, a decision or a diagnostic, whatever the setting says, so
// each variable is left for the Release to supply.
package langgraphimport

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/draftyaml"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// Format is the one format the importer serves: a LangGraph application's
// configuration.
const Format = "langgraph"

// Syntax is the syntactic format a configuration is parsed as.
const Syntax = "json"

// ConfigFile is the name a LangGraph application's configuration is read under
// by default, and the one its signature names.
const ConfigFile = "langgraph.json"

// SchemaURL is the $schema a configuration names its schema by, which
// recognises a configuration under another name.
const SchemaURL = "https://langgra.ph/schema.json"

// EnvFiles is the pattern an environment file's name matches, which tells a
// source the attachment's with.sources hands for one, so it is read for its
// variables' names and never parsed. It matches .env itself. No signature names
// it: a directory is never handed an environment file.
const EnvFiles = "*.env"

// Component is the catalog Component each graph is drafted as an instance of.
const Component = "core/ai-agent/langgraph"

// The sockets of Component the draft names: the datastore a graph's checkpointer
// keeps its state in, and the model, which the configuration never names.
const (
	checkpointerSocket = "checkpointer_db"
	modelSocket        = "model"
)

// where completes each credential report, naming where the value is written.
const where = "in the Release the draft writes"

// backend is a datastore a checkpointer backend keeps a graph's state in: the
// product, the catalog Component serving it, and the last segment of that
// Component's name, which a drafted one is named by and its instance keyed by;
// the slot its own password is attached on and the key its Recipes read it
// under, empty where it keeps none of its own; and the datastore it keeps its
// data in, where that is another one.
type backend struct {
	product, component, name string
	slot, key                string
	behind                   *backing
}

// backing is the datastore a checkpointer's store keeps its data in, wired to
// the store on its socket: the product and the catalog Component serving it, and
// the last segment of that Component's name, which its instance is keyed by; the
// variables the connection delivers to the store, each from what the backing
// publishes; the slot the backing's own password is attached on and the key its
// Recipes read it under; and the key the store reads the same password under,
// attached on its connection to the backing.
type backing struct {
	product, component, name, socket string
	inject                           []draftyaml.Injected
	slot, key, edgeKey               string
}

// backends are the checkpointer backends the configuration names a datastore
// by, keyed by the value of checkpointer.backend: default is PostgreSQL; mongo
// is a store speaking MongoDB's protocol, served by FerretDB, which keeps its
// data in PostgreSQL with the DocumentDB extension and authenticates every
// client as that database's user, so the two are drafted together, the FerretDB
// proxy wired to its DocumentDB backend as the catalog's FerretDB stack wires
// them — the backend's host, port, user and database delivered under the names
// FerretDB reads — and the one password the two share attached to each.
//
// DELIBERATE NEAR-DUPLICATE of facts the catalog holds: the slots and the keys
// the Compose Recipes of PostgreSQL, DocumentDB and FerretDB read a password
// under, and the names the catalog's core/ferretdb Aggregate delivers the
// backend's address under. An importer is handed no Recipe and no Aggregate, and
// the image table generated from the Recipes does not reach this package, so the
// facts are written here; TestTheBackendsTableIsWhatTheHubsEntitiesSay reads the
// hub's entities and fails while any of them differs.
var backends = map[string]backend{
	"default": {product: "PostgreSQL", component: "core/datastore/postgres", name: "postgres",
		slot: "password", key: "POSTGRES_PASSWORD"},
	"mongo": {product: "FerretDB, which speaks MongoDB's protocol", component: "core/datastore/ferretdb",
		name: "ferretdb", behind: &backing{product: "PostgreSQL with the DocumentDB extension",
			component: "core/datastore/documentdb", name: "documentdb", socket: "backend",
			inject: []draftyaml.Injected{{In: "FERRETDB_PG_HOST", From: "host"}, {In: "FERRETDB_PG_PORT", From: "port"},
				{In: "FERRETDB_PG_USER", From: "user"}, {In: "FERRETDB_PG_DB", From: "database"}},
			slot: "password", key: "POSTGRES_PASSWORD", edgeKey: "PGPASSWORD"}},
}

// modelComponent is the catalog Component the model a graph calls is drafted as
// an instance of: the configuration names no model, and the catalog's LangGraph
// Recipes hand a graph the OpenAI client library's variables.
const modelComponent = "core/ai-model/openai"

// modelPart is the model instance the import drafts for every graph to call,
// since the configuration names none: its key and the Component it is an
// instance of, its model left for the author.
type modelPart struct {
	key, component string
}

// defaultBackend is the backend LangGraph reads where the configuration names
// none.
const defaultBackend = "default"

// datastorePart is the datastore an application's graphs are wired to: an
// instance of the Component its checkpointer's backend maps to.
type datastorePart struct {
	// key is the instance's key; ref names it in a report.
	key, ref string
	// component is the Component it is an instance of; drafted is set where that
	// Component is drafted rather than referenced.
	component string
	drafted   bool
	// backend is the backend it serves.
	backend backend
	// stated is the backend as the configuration writes it, "" where it writes
	// none.
	stated string
	// graphs are the graphs wired to it, by ref.
	graphs []string
	// behindKey is the key of the instance of its backend's backing, where the
	// backend keeps its data in another datastore.
	behindKey string
}

// variable is the variable the password a part's store keeps is read from, the
// one every secret attaching that password reads: named after the store's key,
// since the configuration names none.
func (p *datastorePart) variable() string {
	return importer.VariableName(p.key) + "_PASSWORD"
}

// secrets are the secrets the drafts attach for the password p's store keeps,
// none for a drafted store, which declares no slot: by the key of the instance
// attaching each, the store's own, its backing's own and the store's on its
// connection to its backing.
func (p *datastorePart) secrets() map[string][]importer.Secret {
	out := map[string][]importer.Secret{}
	if p.drafted {
		return out
	}
	note := " (the configuration names no variable for it)"
	if b := p.backend; b.slot != "" {
		out[p.key] = append(out[p.key], importer.Secret{Path: "features." + b.slot, Variable: p.variable(),
			Key: b.key, Text: "the password of " + p.key + note})
	}
	if b := p.backend.behind; b != nil {
		edge := importer.ConnectionCredential(p.key, b.socket, p.behindKey, p.behindKey, p.variable())
		edge.Key, edge.Text = b.edgeKey, edge.Text+", the password of "+p.behindKey+note
		out[p.key] = append(out[p.key], edge)
		out[p.behindKey] = append(out[p.behindKey], importer.Secret{Path: "features." + b.slot, Variable: p.variable(),
			Key: b.key, Text: "the password of " + p.behindKey + note})
	}
	return out
}

// checkpointerCredential is the credential graph g presents to its
// application's store p on its checkpointer_db connection: the store's password,
// read from the variable the store's own reads.
func checkpointerCredential(g *graph, p *datastorePart) importer.Secret {
	s := importer.ConnectionCredential(g.key, checkpointerSocket, p.key, p.key, p.variable())
	s.Text += ", the password of " + p.key + " (the configuration names no variable for it)"
	return s
}

// Import turns one import request into the drafts: the request's sources — its
// primary bytes, where it carries any, and its named sources — are each read as
// a configuration, parsed once through parse, the host's parser, or, where named
// like an environment file, kept to be read for its variables' names where a
// configuration names it. A configuration the parser refuses is reported under
// parse-failed, which halts the import, and one declaring no graph under
// no-nodes; the import carries on with the others. A request naming a format
// other than Format reads no graph.
//
// Where any configuration declares a graph, it returns the drafted Aggregate and
// Release and any drafted datastore Component, as files and entities, and a
// decision per graph, datastore, checkpointer naming none and store; where the
// empty_credentials setting holds a value it does not take, it returns that halt
// and drafts nothing. Given one request and one parser, it returns
// byte-identical output.
func Import(in aggrega.ImportInput, parse func(syntax string, source []byte) (json.RawMessage, error)) aggrega.ImportOutput {
	var out aggrega.ImportOutput
	apps, unread, diags := readApplications(in, parse)
	out.Diagnostics = append(out.Diagnostics, diags...)

	var read []*application
	for _, a := range apps {
		if !slices.ContainsFunc(a.graphs, func(g *graph) bool { return g.entrypoint != "" }) {
			out.Diagnostics = append(out.Diagnostics, importer.NoNodes(importer.Named(in, aggrega.ImportSource{Name: a.file}),
				in.Format))
			continue
		}
		read = append(read, a)
	}
	if len(read) == 0 {
		return out
	}

	used := map[string]bool{}
	for _, a := range read {
		if len(read) > 1 && a.dir != "" {
			a.prefix = a.dir + "#"
		}
		for _, g := range a.graphs {
			g.ref = a.prefix + g.id
			if g.entrypoint != "" {
				g.key = importer.UniqueKey(used, g.id)
			}
		}
	}
	parts := wire(in, read, used)
	model := draftModel(in, used)

	written, credentialDiags, err := importer.CarryCredentials(in.Settings, draftedValues(read), where, located(read))
	if err != nil {
		out.Error = "langgraph importer: write the drafted values: " + err.Error()
		return out
	}
	out.Diagnostics = append(out.Diagnostics, credentialDiags...)
	if written == nil {
		return out
	}
	if err := readBack(read, written); err != nil {
		out.Error = "langgraph importer: read the drafted values back: " + err.Error()
		return out
	}

	out.Decisions = decisions(read, model)
	var entities []aggrega.EmittedEntity
	for _, a := range read {
		for _, g := range a.graphs {
			if g.entrypoint == "" {
				continue
			}
			entities = append(entities, aggrega.EmittedEntity{Kind: "Component", Name: Component, Mode: "reference",
				SourceRef: g.ref, DecisionRef: "decision:" + g.ref})
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
			mode, body = "draft", renderDatastore(p.component, p.backend)
		}
		entities = append(entities, aggrega.EmittedEntity{Kind: "Component", Name: p.component, Mode: mode, Body: body,
			SourceRef: p.ref, DecisionRef: "decision:" + p.ref})
		if b := p.backend.behind; b != nil {
			entities = append(entities, aggrega.EmittedEntity{Kind: "Component", Name: b.component, Mode: "reference",
				SourceRef: p.ref, DecisionRef: "decision:" + p.ref})
		}
	}
	if model != nil {
		entities = append(entities, aggrega.EmittedEntity{Kind: "Component", Name: model.component, Mode: "reference",
			SourceRef: modelRef, DecisionRef: "decision:" + modelRef})
	}

	var titles []string
	for _, a := range read {
		for _, g := range a.graphs {
			if g.entrypoint != "" {
				titles = append(titles, g.id)
			}
		}
	}
	name := importer.DraftName(in, titles...)
	aggregate := aggrega.EmittedEntity{Kind: "Aggregate", Name: name, Mode: "draft",
		Body: renderAggregate(name, in.Target, read, parts, model, unread)}
	out.Entities = append(append(entities, aggregate),
		importer.DraftRelease(in, aggregate, langgraphRelease(read, parts, model))...)
	out.Files = importer.Render(in.Settings, out.Entities, out.Decisions)
	return out
}

// wire makes the datastore part each application's graphs are wired to, keying
// each after every graph: one part per application whose checkpointer's backend
// maps to a datastore, an instance of the catalog's Component for that backend
// where the request's catalog view holds it, and of a Component drafted under
// the target namespace otherwise. An application whose checkpointer names no
// datastore gets none.
func wire(in aggrega.ImportInput, read []*application, used map[string]bool) []*datastorePart {
	held := map[string]bool{}
	if in.Catalog != nil {
		for _, c := range in.Catalog.Components {
			held[c.Name] = true
		}
	}
	namespace := importer.TargetNamespace(in.Settings)
	var parts []*datastorePart
	for _, a := range read {
		b, ok := backends[a.checkpointer.backend]
		if !ok || a.checkpointer.why != "" {
			continue
		}
		if b.behind != nil && (!held[b.component] || !held[b.behind.component]) {
			a.checkpointer.why = "names the backend " + draftyaml.Comment(a.checkpointer.stated) + ", which the " +
				"import drafts as " + b.component + " over " + b.behind.component + ", and the workspace's catalog " +
				"holds " + missingOf(held, b.component, b.behind.component)
			continue
		}
		p := &datastorePart{ref: a.prefix + "checkpointer", component: b.component, backend: b,
			stated: a.checkpointer.stated, key: importer.UniqueKey(used, b.name)}
		if b.behind != nil {
			p.behindKey = importer.UniqueKey(used, b.behind.name)
		}
		if !held[b.component] {
			p.component = namespace + "/datastore/" + b.name
			p.drafted = true
		}
		for _, g := range a.graphs {
			if g.entrypoint != "" {
				p.graphs = append(p.graphs, g.ref)
			}
		}
		a.part = p
		parts = append(parts, p)
	}
	return parts
}

// missingOf names, of the Components given, those held does not hold.
func missingOf(held map[string]bool, components ...string) string {
	var missing []string
	for _, c := range components {
		if !held[c] {
			missing = append(missing, c)
		}
	}
	return "no " + strings.Join(missing, " and no ")
}

// draftModel returns the model instance every graph is wired to, keyed after
// every other instance, where the request's catalog view holds modelComponent;
// nil otherwise, each graph's model connection then left for the author.
func draftModel(in aggrega.ImportInput, used map[string]bool) *modelPart {
	if in.Catalog == nil || !slices.ContainsFunc(in.Catalog.Components, func(c aggrega.CatalogComponent) bool {
		return c.Name == modelComponent
	}) {
		return nil
	}
	return &modelPart{key: importer.UniqueKey(used, "model"), component: modelComponent}
}

// draftedValues writes, as one JSON document, the values the credential decision
// judges: under each application's position in read, the text of each value its
// inline env holds, in the order the draft writes them, a value holding nothing
// written as null.
func draftedValues(read []*application) []byte {
	doc := map[string]map[string][]*string{}
	for i, a := range read {
		values := make([]*string, 0, len(a.env.entries))
		for j := range a.env.entries {
			e := &a.env.entries[j]
			if e.located {
				values = append(values, &e.text)
			} else {
				values = append(values, nil)
			}
		}
		doc[strconv.Itoa(i)] = map[string][]*string{"env": values}
	}
	raw, _ := json.Marshal(doc)
	return raw
}

// located returns every value of draftedValues the credential decision judges,
// in the order the draft writes them: each value of an inline env that holds
// something, named by its configuration and its variable, as an input its
// format marks as no credential. A variable's name is not read for what it
// means, so a name reading as a credential's and one that does not are located
// alike.
func located(read []*application) []importer.Credential {
	var out []importer.Credential
	for i, a := range read {
		for j, e := range a.env.entries {
			if !e.located {
				continue
			}
			out = append(out, importer.Credential{Node: a.name(), Field: "env." + e.name,
				Pointer: importer.Pointer(strconv.Itoa(i), "env", strconv.Itoa(j)), Class: importer.CredentialUnmarked})
		}
	}
	return out
}

// readBack reads the values the credential decision wrote empty back onto the
// applications: a value that held something and is written empty is written
// with no value. A value the decision carried is left exactly as the
// configuration writes it.
func readBack(read []*application, written []byte) error {
	var values map[string]struct {
		Env []*string `json:"env"`
	}
	if err := json.Unmarshal(written, &values); err != nil {
		return err
	}
	for i, a := range read {
		got := values[strconv.Itoa(i)].Env
		for j := range a.env.entries {
			e := &a.env.entries[j]
			if e.located && j < len(got) && got[j] != nil && *got[j] == "" {
				e.text, e.emptied = "", true
			}
		}
	}
	return nil
}

// modelRef is the reference the drafted model instance is decided and named by
// in a report: it stands for no node of a configuration, which names no model.
const modelRef = "model"

// decisions returns, application by application in the order read, a decision
// per graph in the order the configuration writes them, then one for the
// datastore its graphs are wired to or for the checkpointer naming none, then
// one for the store where the configuration configures it; and, where the
// import drafts one, a decision for the model every graph is wired to.
func decisions(read []*application, model *modelPart) []aggrega.ImportDecision {
	var out []aggrega.ImportDecision
	for _, a := range read {
		for _, g := range a.graphs {
			out = append(out, graphDecision(a, g, model))
		}
		switch {
		case a.part != nil:
			out = append(out, partDecision(a.part))
		case a.checkpointer.why != "":
			out = append(out, aggrega.ImportDecision{SourceNode: aggrega.DecisionSourceNode{ID: a.prefix + "checkpointer",
				Type: "checkpointer", ParamsSummary: a.checkpointer.summary()},
				Rationale: "the checkpointer " + a.checkpointer.why + ", so no datastore is drafted for it and each " +
					"graph's " + checkpointerSocket + " connection is left for the author"})
		}
		if a.store != nil {
			out = append(out, storeDecision(a))
		}
	}
	if model != nil {
		out = append(out, aggrega.ImportDecision{SourceNode: aggrega.DecisionSourceNode{ID: modelRef, Type: "model",
			ParamsSummary: "no model"}, Chosen: &aggrega.DecisionChoice{Target: model.component, Mode: "reference",
			Tier: "NONE"}, Rationale: "the configuration names no model, while each graph's " + modelSocket +
			" socket requires one: " + model.key + " is an instance of " + model.component + ", whose client " +
			"library's variables the catalog's LangGraph Recipes hand a graph, its values.model left empty for the " +
			"author to name the model"})
	}
	return out
}

// graphDecision is the decision for one graph: an instance of Component running
// the graph at its import path, wired to its application's datastore where it
// has one, and to model, the model the import drafts, where it drafts one, the
// model's connection otherwise left for the author. A graph naming no import
// path records no choice.
func graphDecision(a *application, g *graph, model *modelPart) aggrega.ImportDecision {
	d := aggrega.ImportDecision{SourceNode: aggrega.DecisionSourceNode{ID: g.ref, Type: "graph",
		ParamsSummary: g.summary()}}
	if g.entrypoint == "" {
		d.Rationale = "the graph " + g.why + ", so it is not drafted"
		return d
	}
	d.Chosen = &aggrega.DecisionChoice{Target: Component, Mode: "reference", Tier: "NONE"}
	d.Rationale = "a LangGraph graph is an instance of " + Component + " running the graph at its import path, " +
		"graph_entrypoint"
	if a.part != nil {
		d.Rationale += "; its " + checkpointerSocket + " socket is wired to " + a.part.key + ", the " +
			a.part.backend.product + " its checkpointer keeps state in"
	} else {
		d.Rationale += "; its " + checkpointerSocket + " socket is left for the author, since its checkpointer " +
			a.checkpointer.why
	}
	if model != nil {
		d.Rationale += "; its " + modelSocket + " socket is wired to " + model.key + ", whose model is left for the " +
			"author, since the configuration names none"
	} else {
		d.Rationale += "; its " + modelSocket + " socket is left for the author, since the configuration names no " +
			"model and the catalog holds no " + modelComponent
	}
	return d
}

// partDecision is the decision for one datastore part: a reference to the
// catalog's Component for its backend, or that Component drafted where the
// catalog holds none.
func partDecision(p *datastorePart) aggrega.ImportDecision {
	summary := "backend " + p.stated
	because := "names the backend " + p.stated + ", which is " + p.backend.product
	if p.stated == "" {
		summary = "no backend"
		because = "names no backend, and LangGraph reads one naming none as " + defaultBackend + ", which is " +
			p.backend.product
	}
	d := aggrega.ImportDecision{SourceNode: aggrega.DecisionSourceNode{ID: p.ref, Type: "checkpointer",
		ParamsSummary: summary}}
	if p.drafted {
		d.Chosen = &aggrega.DecisionChoice{Target: p.component, Mode: "draft", Tier: "NONE"}
		d.Rationale = "the checkpointer " + because + "; the catalog holds no " + p.backend.component + ", so a " +
			"Component for it is drafted as " + p.component + " and wired on the " + checkpointerSocket + " socket of " +
			strings.Join(p.graphs, ", ")
		return d
	}
	d.Chosen = &aggrega.DecisionChoice{Target: p.component, Mode: "reference", Tier: "NONE"}
	d.Rationale = "the checkpointer " + because + ", so " + p.key + " is an instance of " + p.component +
		" wired on the " + checkpointerSocket + " socket of " + strings.Join(p.graphs, ", ")
	if b := p.backend.behind; b != nil {
		d.Rationale += ", keeping its data in " + p.behindKey + ", an instance of " + b.component + ", " + b.product +
			", wired on its " + b.socket + " socket; the two share one password, attached to each"
	}
	return d
}

// storeDecision is the decision for an application's store: no choice, since the
// store names no backend and LangGraph keeps it in the PostgreSQL database its
// server uses.
func storeDecision(a *application) aggrega.ImportDecision {
	d := aggrega.ImportDecision{SourceNode: aggrega.DecisionSourceNode{ID: a.prefix + "store", Type: "store",
		ParamsSummary: a.store.summary()},
		Rationale: "the store configures the memory store's search index and the time its items live, and names no " +
			"backend: LangGraph keeps the store in the PostgreSQL database its server uses"}
	if a.part != nil && a.part.backend.name == backends[defaultBackend].name {
		d.Rationale += ", which " + a.part.key + " serves"
	}
	d.Rationale += "; core/ai-agent/langgraph takes no value for its index or its ttl, so neither is carried"
	return d
}
