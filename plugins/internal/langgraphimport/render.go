// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package langgraphimport

import (
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/draftyaml"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/importer"
)

// The drafts are written by hand rather than marshalled, so the key order is
// fixed and two runs over one configuration write the same bytes. Every text read
// from a source is written as a double-quoted YAML scalar through the drafts'
// shared writer, which reads back as exactly the source's text — escaped first,
// in the Aggregate, so it reads back as itself once the Aggregate is evaluated as
// a template — and every name written into a comment through its comment writer,
// each comment line of the Aggregate through the drafts' comment-line writer,
// which escapes it so it too reads back as written once evaluated.
var (
	yamlString  = draftyaml.String
	commentText = draftyaml.Comment
	commentList = draftyaml.CommentList
	commentLine = draftyaml.CommentLine
)

// renderAggregate writes the drafted Aggregate, named name: one instance of
// Component per graph, application by application in the order the
// configurations write them, carrying its import path and its description and
// wired to its application's datastore; then one instance per datastore part.
// Comments above it name the configurations read, what the draft does not read
// or carry, the environment files read for their names alone and those, unread,
// that no configuration names; comments above a graph name what the draft leaves
// for the author.
func renderAggregate(name string, read []*application, parts []*datastorePart, unread []string) string {
	var b strings.Builder
	writeHeader(&b, read, unread)
	b.WriteString("apiVersion: aggrega.ai/v1alpha1\n")
	b.WriteString("kind: Aggregate\n")
	b.WriteString("metadata:\n")
	b.WriteString("  name: " + name + "\n")
	b.WriteString("  version: 0.1.0\n")
	b.WriteString("  description: " + importer.DraftDescription(Format) + "\n")
	b.WriteString("spec:\n")
	b.WriteString("  components:\n")
	for _, a := range read {
		for _, g := range a.graphs {
			if g.entrypoint != "" {
				writeGraph(&b, a, g)
			}
		}
	}
	for _, p := range parts {
		of := ""
		if p.ref != "checkpointer" {
			of = ", " + commentText(p.ref)
		}
		b.WriteString(commentLine("    ", "The "+p.backend.product+" database the configuration's checkpointer"+of+
			" keeps each graph's state in."))
		if p.drafted {
			b.WriteString(commentLine("    ", p.component+" is drafted beside this Aggregate, since the catalog holds no "+
				p.backend.component+"; a Recipe publishing its host and port is the author's to supply."))
		}
		b.WriteString("    " + p.key + ":\n")
		b.WriteString("      type: " + p.component + "\n")
	}
	return b.String()
}

// writeHeader writes the comments opening the Aggregate: the configurations read
// and the environment files read for their names alone; the environment files
// handed that no configuration names, which are not read; that the application's
// code is not read; and, application by application, the members the draft does
// not carry, the store, what of the checkpointer it does not carry, the graphs it
// does not draft and the environment files it was not handed, with how to hand
// one.
func writeHeader(b *strings.Builder, read []*application, unread []string) {
	var files, envFiles []string
	for _, a := range read {
		if a.file != "" {
			files = append(files, a.file)
		}
		if a.env.read != "" {
			envFiles = append(envFiles, a.env.read)
		}
	}
	if len(files) > 0 {
		b.WriteString(commentLine("", "LangGraph configurations read:"))
		for _, f := range files {
			b.WriteString(commentLine("", "  - "+commentText(f)))
		}
	}
	if len(envFiles) > 0 {
		b.WriteString(commentLine("", "Environment files read for their variables' names alone, never their values:"))
		for _, f := range envFiles {
			b.WriteString(commentLine("", "  - "+commentText(f)))
		}
	}
	if len(unread) > 0 {
		b.WriteString(commentLine("", "Not read, since no configuration names them: the environment files "+commentList(unread)+
			"."))
	}
	b.WriteString(commentLine("", "Not read: the application's code, which defines each graph; it runs in the image the Release "+
		"names for each graph."))
	for _, a := range read {
		of := ""
		if len(read) > 1 {
			of = " of " + commentText(a.name())
		}
		if len(a.built) > 0 {
			b.WriteString(commentLine("", "Not carried, since they configure the server image langgraph build builds"+of+
				", and the image the Release names is the application's own: "+commentList(a.built)+"."))
		}
		if len(a.others) > 0 {
			b.WriteString(commentLine("", "Not carried, since "+Component+" takes no value for them"+of+": "+
				commentList(a.others)+"."))
		}
		if a.store != nil {
			line := "The store" + of + " configures the memory store, which LangGraph keeps in the PostgreSQL " +
				"database its server uses; not carried, since " + Component + " takes no value for it"
			if len(a.store.members) > 0 {
				line += ": " + commentList(a.store.members)
			}
			b.WriteString(commentLine("", line+"."))
		}
		if len(a.checkpointer.members) > 0 {
			b.WriteString(commentLine("", "Not carried, since "+Component+" takes no value for them"+of+": "+
				commentList(prefixed("checkpointer.", a.checkpointer.members))+"."))
		}
		for _, g := range a.graphs {
			if g.entrypoint == "" {
				b.WriteString(commentLine("", "Not drafted: the graph "+commentText(a.prefix+g.id)+", which "+g.why+"."))
			}
		}
		if a.env.note != "" {
			b.WriteString(commentLine("", "The "+a.env.note+of+"."))
		}
		if a.env.file != "" && a.env.read == "" {
			b.WriteString(commentLine("", "Not read: the environment file "+commentText(a.env.file)+of+", which the import "+
				"was not handed; its variables are left for the Release. Name the file under the LangGraph importer's "+
				"with.sources to have its variables' names read."))
		}
	}
}

// prefixed returns each name opened by prefix.
func prefixed(prefix string, names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, prefix+n)
	}
	return out
}

// writeGraph writes one graph's instance of Component: its import path and its
// description, and its datastore connection where its application has a
// datastore part, with comments above it naming the graph where its key does
// not, and why a connection is left for the author.
func writeGraph(b *strings.Builder, a *application, g *graph) {
	if g.ref != g.key {
		b.WriteString(commentLine("    ", "The graph "+commentText(g.ref)+"."))
	}
	if a.part == nil {
		b.WriteString(commentLine("    ", "The "+checkpointerSocket+" connection is left for the author, since the checkpointer "+
			a.checkpointer.why+"; the build halts naming the socket until one is wired."))
	}
	b.WriteString(commentLine("    ", "The "+modelSocket+" connection is left for the author, since the configuration names no "+
		"model; the build halts naming the socket until one is wired."))
	b.WriteString("    " + g.key + ":\n")
	b.WriteString("      type: " + Component + "\n")
	b.WriteString("      values:\n")
	b.WriteString("        graph_entrypoint: " + aggregateText(g.entrypoint) + "\n")
	if g.description != nil {
		b.WriteString("        description: " + aggregateText(*g.description) + "\n")
	}
	if a.part != nil {
		b.WriteString("      uses:\n")
		b.WriteString("        " + checkpointerSocket + ":\n")
		b.WriteString("          " + a.part.key + ":\n")
		b.WriteString("            target: " + a.part.key + "\n")
	}
}

// aggregateText writes text read from a source as a value of the drafted
// Aggregate: escaped so it reads back as itself once the Aggregate is evaluated
// as a template, then as a double-quoted scalar.
func aggregateText(text string) string {
	return yamlString(importer.EscapeTemplateText(text))
}

// renderRelease writes the drafted Release, named name and deploying the
// Aggregate of that name: above the first graph, what each graph's image holds
// and the variables it is handed; for each graph, under its key, a comment
// naming what the Release supplies — the image running the application — and,
// above each application's first graph, the environment its configuration
// states; then an entry for each datastore part, since a build places only the
// instances a Release names. The Release's environment and its Manifest are left
// unset, each named in a comment where it belongs. Each importer renders its own
// Release; the plugin kit renders none.
func renderRelease(name string, read []*application, parts []*datastorePart) string {
	var b strings.Builder
	b.WriteString("apiVersion: aggrega.ai/v1alpha1\n")
	b.WriteString("kind: Release\n")
	b.WriteString("metadata:\n")
	b.WriteString("  name: " + name + "\n")
	b.WriteString("  # environment: left for the operator, since no source names the environment this Release deploys to.\n")
	b.WriteString("  version: 0.1.0\n")
	b.WriteString("  description: Draft Release imported from a source in the " + Format + " format.\n")
	b.WriteString("spec:\n")
	b.WriteString("  aggregate: " + name + "\n")
	b.WriteString("  # manifest: left for the operator, since the import drafts no Manifest, which names the target " +
		"the graphs are built for.\n")
	b.WriteString("  components:\n")
	contract := true
	for _, a := range read {
		first := true
		for _, g := range a.graphs {
			if g.entrypoint == "" {
				continue
			}
			if contract {
				b.WriteString(imageContract)
				contract = false
			}
			if first {
				writeEnvironment(&b, a)
				first = false
			}
			b.WriteString("    # Left for the Release, since no configuration states it:\n")
			b.WriteString("    #   - infra.image, the container image running the application\n")
			b.WriteString("    " + g.key + ":\n")
			b.WriteString("      infra: {}\n")
		}
	}
	for _, p := range parts {
		b.WriteString("    " + p.key + ":\n")
		b.WriteString("      infra: {}\n")
	}
	return b.String()
}

// imageContract is the comment above the Release's first graph: what each
// graph's image holds, the variables the Recipes of Component hand it, who reads
// each, and which inject block replaces which default name. Every variable it
// names is checked against what a build through each of the catalog's Recipes
// renders, so a name changed on either side fails that check.
const imageContract = "    # Each graph's image is the application's own, not the server image langgraph build builds: its " +
	"code, with LangGraph installed.\n" +
	"    # It is handed, for its code to read, the graph to run as LANGGRAPH_GRAPH_ENTRYPOINT, the model's name as " +
	"OPENAI_MODEL, the checkpointer store's host and port as LANGGRAPH_CHECKPOINTER_HOST and " +
	"LANGGRAPH_CHECKPOINTER_PORT, its user and database, where the store publishes them, as " +
	"LANGGRAPH_CHECKPOINTER_USER and LANGGRAPH_CHECKPOINTER_DATABASE, and a password attached on the " +
	checkpointerSocket + " connection as LANGGRAPH_CHECKPOINTER_PASSWORD.\n" +
	"    # It is also handed the model's address as OPENAI_BASE_URL and a key attached on the " + modelSocket +
	" connection as OPENAI_API_KEY, which the OpenAI client library reads, and recursion_limit as " +
	"LANGGRAPH_DEFAULT_RECURSION_LIMIT, which LangGraph reads.\n" +
	"    # An inject: block on the graph's own instance, through an entry reading graph_entrypoint alone, replaces " +
	"LANGGRAPH_GRAPH_ENTRYPOINT with the name it writes; one on the " + modelSocket + " connection replaces " +
	"OPENAI_MODEL, and one on the " + checkpointerSocket + " connection the store's host, port, user and database, " +
	"with the names it writes.\n"

// writeEnvironment writes, in a comment, the environment an application's
// configuration states, for the Release to carry for each of its graphs: each
// variable an inline env names with its value as the credential decision left
// it — written with no value where it holds none or was written empty — or each
// variable its environment file names, by its name alone.
func writeEnvironment(b *strings.Builder, a *application) {
	of := commentText(a.name())
	switch {
	case len(a.env.entries) > 0:
		b.WriteString("    # The environment " + of + " states for its application, for the Release to carry where " +
			Component + " takes it for each of its graphs:\n")
		for _, e := range a.env.entries {
			if !e.located || e.emptied {
				b.WriteString("    #   " + commentText(e.name) + "\n")
				continue
			}
			b.WriteString("    #   " + commentText(e.name) + ": " + yamlString(e.text) + "\n")
		}
	case a.env.read != "":
		b.WriteString("    # The environment file " + commentText(a.env.read) + ", which " + of + " names, names " +
			"these variables for its application, each left for the Release to supply for each of its graphs, " +
			"since the import reads the file's names and never its values:\n")
		for _, n := range a.env.names {
			b.WriteString("    #   - " + commentText(n) + "\n")
		}
		if len(a.env.skipped) > 0 {
			b.WriteString("    # Not read, naming no variable: " + lineNumbers(a.env.skipped) + " of " +
				commentText(a.env.read) + ".\n")
		}
	}
}

// renderDatastore writes the drafted Component of a datastore the catalog holds
// no Component for, named name: a Component of the datastore category serving b,
// whose provides declare the host and the port a LangGraph agent's
// checkpointer_db connection reads. A Recipe publishing them is the author's to
// supply, as the comment opening it says.
func renderDatastore(name string, b backend) string {
	var s strings.Builder
	s.WriteString("# A Recipe running " + b.product + " and publishing its host and port is left for the author: the " +
		"LangGraph agent's checkpointer_db connection reads both.\n")
	s.WriteString("apiVersion: aggrega.ai/v1alpha1\n")
	s.WriteString("kind: Component\n")
	s.WriteString("metadata:\n")
	s.WriteString("  name: " + name + "\n")
	s.WriteString("  version: 0.1.0\n")
	s.WriteString("  labels:\n")
	s.WriteString("    component.aggrega.ai/type: stateful-server\n")
	s.WriteString("    component.aggrega.ai/category: datastore\n")
	s.WriteString("spec:\n")
	s.WriteString("  schema:\n")
	s.WriteString("    provides:\n")
	s.WriteString("      host: { type: string, required: true }\n")
	s.WriteString("      port: { type: integer, required: true }\n")
	return s.String()
}
