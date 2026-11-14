// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package agentframeworkimport

import (
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/apiversion"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/draftyaml"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// The drafts are written by hand rather than marshalled, so the key order is
// fixed and two runs over one declarative agent write the same bytes. Every text
// read from a source is written as a double-quoted YAML scalar through the
// drafts' shared writer, which reads back as exactly the source's text — escaped
// first, in the Aggregate, so it reads back as itself once the Aggregate is
// evaluated as a template — and every name written into a comment through its
// comment writer, each comment line of the Aggregate escaped as its values are.
var (
	yamlString  = draftyaml.String
	commentText = draftyaml.Comment
	commentList = draftyaml.CommentList
	commentLine = draftyaml.CommentLine
)

// renderAggregate writes the drafted Aggregate, named name: one instance of
// Component per declarative agent, in the order the import was handed them,
// carrying its displayName and its description and wired to its model part;
// then one instance per model part, serving its model. Comments above it name
// the declarative agents read and what the draft does not read; comments above
// an instance name what the application reads from the declarative agent, the
// tools drafted as no Component, each MCP server the model's provider reaches,
// why its model is not wired where it is not, and a model name left for the
// author.
func renderAggregate(name string, agents []*agent, parts []*modelPart) string {
	var b strings.Builder
	var files []string
	for _, a := range agents {
		if a.file != "" {
			files = append(files, a.file)
		}
	}
	if len(files) > 0 {
		b.WriteString(commentLine("", "Agent Framework declarative agents read:"))
		for _, f := range files {
			b.WriteString(commentLine("", "  - "+commentText(f)))
		}
	}
	b.WriteString(commentLine("", "Not read: the application's code, which loads each declarative agent and binds the functions of "+
		"its function tools; the application runs in the image the Release names for each instance, and loads its "+
		"declarative agents itself."))
	b.WriteString("apiVersion: " + apiversion.APIVersion + "\n")
	b.WriteString("kind: Aggregate\n")
	b.WriteString("metadata:\n")
	b.WriteString("  name: " + name + "\n")
	b.WriteString("  version: 0.1.0\n")
	b.WriteString("  description: " + importer.DraftDescription(Format) + "\n")
	b.WriteString("spec:\n")
	b.WriteString("  components:\n")
	for _, a := range agents {
		writeAgent(&b, a)
	}
	for _, p := range parts {
		writePart(&b, p)
	}
	return b.String()
}

// writeAgent writes one declarative agent's instance of Component: its
// displayName and its description, and its model connection where it has a
// model part, with comments above it naming the agent, what the application
// reads from the declarative agent, its tools drafted as no Component, each MCP
// server it names, and why its model is not wired where it is not.
func writeAgent(b *strings.Builder, a *agent) {
	of := ""
	if a.file != "" {
		of = ", of " + commentText(a.file)
	}
	b.WriteString(commentLine("    ", "The declarative agent "+commentText(nameOf(a))+of+"."))
	read := append([]string{}, a.applications...)
	if len(a.model.options) > 0 {
		read = append(read, "the options of its model requests ("+commentList(a.model.options)+")")
	}
	if len(read) > 0 {
		b.WriteString(commentLine("    ", "Read by the application from the declarative agent, and taken by no key of "+Component+
			": "+strings.Join(read, ", ")+"."))
	}
	var bound, hosted, others []string
	for _, t := range a.tools {
		label := commentText(firstOf(t.name, firstOf(t.kind, "tool")))
		switch t.kind {
		case mcpKind:
		case "function":
			bound = append(bound, label)
		case "web_search", "file_search", "code_interpreter":
			hosted = append(hosted, label)
		default:
			others = append(others, label+" ("+commentText(firstOf(t.kind, "no kind"))+")")
		}
	}
	if len(bound) > 0 {
		b.WriteString(commentLine("    ", "Function tools, whose code the application binds, drafted as no Component: "+
			strings.Join(bound, ", ")+"."))
	}
	if len(hosted) > 0 {
		b.WriteString(commentLine("    ", "Tools the model's provider runs, drafted as no Component: "+strings.Join(hosted, ", ")+"."))
	}
	if len(others) > 0 {
		b.WriteString(commentLine("    ", "Tools of other kinds, which the application configures, drafted as no Component: "+
			strings.Join(others, ", ")+"."))
	}
	for _, t := range a.tools {
		if t.mcp() {
			writeServerComment(b, a, t)
		}
	}
	if a.part == nil {
		b.WriteString(commentLine("    ", "The "+modelSocket+" connection is left for the author, since "+unwiredWhy(a)+
			"; the build halts naming the socket until one is wired."))
	}
	b.WriteString("    " + a.key + ":\n")
	b.WriteString("      type: " + Component + "\n")
	if a.displayName != nil || a.description != nil {
		b.WriteString("      values:\n")
		if a.displayName != nil {
			b.WriteString("        display_name: " + aggregateText(*a.displayName) + "\n")
		}
		if a.description != nil {
			b.WriteString("        description: " + aggregateText(*a.description) + "\n")
		}
	}
	p := a.part
	if p == nil {
		return
	}
	b.WriteString("      uses:\n")
	b.WriteString("        " + modelSocket + ":\n")
	b.WriteString("          " + p.key + ":\n")
	b.WriteString("            target: " + p.key + "\n")
	if a.model.connection.kind == "anonymous" {
		b.WriteString("            values:\n")
		b.WriteString("              unauthenticated: true\n")
	}
}

// writePart writes one model part's instance, serving its model: its id as
// written, or, for an id written as a reference or as an expression, no name,
// with a comment saying why.
func writePart(b *strings.Builder, p *modelPart) {
	b.WriteString(commentLine("    ", "The model "+commentText(p.ref)+", which "+commentList(p.agents)+" names with the "+
		openAIProvider+" provider."))
	switch {
	case p.id.variable != "":
		b.WriteString(commentLine("    ", "values.model is left for the author, since the declarative agent writes its model's id "+
			"as "+envReference(commentText(p.id.variable))+"."))
		b.WriteString(commentLine("    ", "The import reads no environment variable; the build halts naming values.model until "+
			"it is set."))
	case p.id.expression:
		b.WriteString(commentLine("    ", "values.model is left for the author, since the declarative agent computes its model's "+
			"id with an expression the import does not evaluate; the build halts naming it until it is set."))
	}
	b.WriteString("    " + p.key + ":\n")
	b.WriteString("      type: " + p.component + "\n")
	if p.id.literal() {
		b.WriteString("      values:\n")
		b.WriteString("        model: " + aggregateText(p.id.value) + "\n")
	}
}

// writeServerComment writes the comment above an agent's instance naming one MCP
// server the declarative agent names: that it is drafted as no Component and no
// connection, since the model's provider, not the application, reaches it; and
// its description, its approval mode and the tools it allows, as the declarative
// agent states them. Its url is written above the agent's entry of the Release.
func writeServerComment(b *strings.Builder, a *agent, t *tool) {
	b.WriteString(commentLine("    ", "The MCP server "+commentText(firstOf(t.name, mcpKind))+", drafted as no Component and "+
		"no connection, since "+hostedTool("it")+"; its url is written above "+a.key+"'s entry in the Release."))
	var stated []string
	if t.description != nil {
		stated = append(stated, "its description, "+yamlString(*t.description))
	}
	if t.approval != "" {
		stated = append(stated, "its approval mode, "+commentText(t.approval))
	}
	if len(t.allowed) > 0 {
		stated = append(stated, "the tools of the server it allows, "+commentList(t.allowed))
	}
	if len(stated) > 0 {
		b.WriteString(commentLine("    ", "Stated by the declarative agent for "+commentText(firstOf(t.name, mcpKind))+": "+
			strings.Join(stated, "; ")+"."))
	}
}

// hostedTool says who reaches an MCP server a declarative agent names, the
// server written as server: the model's provider, to which Agent Framework sends
// it as a hosted tool. The comment naming the server above the agent's instance
// and the server's decision both say it through here, so the two state one fact.
func hostedTool(server string) string {
	return "Agent Framework sends " + server + " to the model's provider as a hosted tool, and the provider, not the " +
		"application, reaches it at its url, which must therefore be reachable from the provider"
}

// aggregateText writes text read from a source as a value of the drafted
// Aggregate: escaped so it reads back as itself once the Aggregate is evaluated
// as a template, then as a double-quoted scalar.
func aggregateText(text string) string {
	return yamlString(importer.EscapeTemplateText(text))
}

// agentFrameworkRelease is the drafted Release, as the plugin kit's Release
// writer writes it: for each agent, under its instance's key, leaving for the
// Release the image running the application and the credential it presents to
// its model, with a comment naming the values its declarative agent states for
// its model and its MCP servers, each server's url among them, written as stated
// or, where the credential decision wrote one empty, with no value; then for each
// model part, an entry, with the endpoint its connection states where it states
// one.
func agentFrameworkRelease(agents []*agent, parts []*modelPart) importer.ReleaseDraft {
	draft := importer.ReleaseDraft{}
	for _, a := range agents {
		draft.Entries = append(draft.Entries, agentEntry(a))
	}
	for _, p := range parts {
		draft.Entries = append(draft.Entries, partEntry(p))
	}
	return draft
}

// agentEntry is one agent's entry of the Release.
func agentEntry(a *agent) importer.ReleaseEntry {
	entry := importer.ReleaseEntry{Key: a.key, Left: []importer.LeftItem{{Path: "infra.image",
		Text: "the container image running the application built on Agent Framework, holding its declarative agent"}}}
	if p := a.part; p != nil && a.model.connection.kind != "anonymous" {
		entry.Left = append(entry.Left, importer.ConnectionCredential(a.key, modelSocket, p.key, p.key))
	}
	if len(a.stated) > 0 {
		entry.Comments = append(entry.Comments, "The declarative agent also states these values for its model and its MCP "+
			"servers, written here for review:")
		for _, v := range a.stated {
			entry.Comments = append(entry.Comments, "  - "+statedLine(v))
		}
	}
	return entry
}

// statedLine writes one stated value for the comment above an agent's entry:
// its field and its value as written, as a double-quoted scalar reading back as
// exactly the source's text; for a value written as a reference, the reference
// and when Agent Framework reads its variable; for one written as another
// expression, that it is one; and for a value
// the empty_credentials setting leaves out, its field alone, saying why.
func statedLine(v statedValue) string {
	field := commentText(v.field)
	switch {
	case v.emptied:
		return field + ", " + emptiedNote
	case v.text.variable != "":
		return field + ", written as " + envReference(commentText(v.text.variable))
	case v.text.expression:
		return field + ", computed by an expression the import does not evaluate"
	case !v.text.literal():
		return field + ", with no value"
	}
	return field + ": " + yamlString(v.text.value)
}

// partEntry is one model part's entry of the Release, since a build places only
// the instances a Release names: the endpoint its connection states as
// infra.endpoint, or a comment naming the reference it is written as, saying it
// is computed, or leaving it for the Release where it was written empty. A part
// whose connection states no endpoint has an entry holding no infrastructure:
// the Component's own default is its address.
func partEntry(p *modelPart) importer.ReleaseEntry {
	entry := importer.ReleaseEntry{Key: p.key}
	e := p.endpointText
	switch {
	case p.endpoint != nil:
	case e.variable != "":
		entry.Comments = append(entry.Comments, "infra.endpoint is left at the Component's default, while the declarative "+
			"agent writes its model's address as "+envReference(commentText(e.variable))+".")
	case e.expression:
		entry.Comments = append(entry.Comments, "infra.endpoint is left at the Component's default, while the declarative "+
			"agent computes its model's address with an expression the import does not evaluate.")
	case e.literal():
		entry.Left = append(entry.Left, importer.LeftItem{Path: "infra.endpoint",
			Text: emptiedNote + "; until it is set, the Component's default address is used"})
	}
	if p.endpoint == nil {
		return entry
	}
	entry.Lines = []string{"infra:", "  endpoint: " + yamlString(*p.endpoint)}
	return entry
}

// renderModel writes the drafted OpenAI Component, named name, for a catalog
// holding none: a Component of the ai-model category whose model.aggrega.ai/api
// label names the OpenAI API, the label the Agent Framework agent's model socket
// selects a model by, and whose values declare the name of the model it serves.
// A Recipe publishing its endpoint and its model is the author's to supply, as
// the comment opening it says.
func renderModel(name string) string {
	var b strings.Builder
	b.WriteString("# A Recipe publishing this Component's endpoint and its model is left for the author.\n")
	b.WriteString("apiVersion: " + apiversion.APIVersion + "\n")
	b.WriteString("kind: Component\n")
	b.WriteString("metadata:\n")
	b.WriteString("  name: " + name + "\n")
	b.WriteString("  version: 0.1.0\n")
	b.WriteString("  labels:\n")
	b.WriteString("    component.aggrega.ai/type: resource\n")
	b.WriteString("    component.aggrega.ai/category: ai-model\n")
	b.WriteString("    model.aggrega.ai/api: " + modelAPI + "\n")
	b.WriteString("spec:\n")
	b.WriteString("  schema:\n")
	b.WriteString("    values:\n")
	b.WriteString("      type: object\n")
	b.WriteString("      properties:\n")
	b.WriteString("        model: { type: string }\n")
	b.WriteString("      additionalProperties: false\n")
	b.WriteString("    infra:\n")
	b.WriteString("      endpoint: { type: string }\n")
	return b.String()
}
