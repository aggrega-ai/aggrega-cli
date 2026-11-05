// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package agentframeworkimport

import (
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/draftyaml"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/mcpimport"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/importer"
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
// carrying its displayName and its description and wired to its model part and
// to its MCP servers; then one instance per model part, serving its model; then
// one instance of the MCP tool per MCP server, carrying its description. Comments
// above it name the declarative agents read and what the draft does not read;
// comments above an instance name what the application reads from the
// declarative agent, the tools drafted as no Component, why its model is not
// wired where it is not, a model name left for the author, and an MCP server's
// approval mode and the tools it allows.
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
	b.WriteString("apiVersion: aggrega.ai/v1alpha1\n")
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
	for _, a := range agents {
		for _, t := range a.tools {
			if t.mcp() {
				writeTool(&b, a, t)
			}
		}
	}
	return b.String()
}

// writeAgent writes one declarative agent's instance of Component: its
// displayName and its description, its model connection where it has a model
// part, and a connection per MCP server, with comments above it naming the
// agent, what the application reads from the declarative agent, its tools
// drafted as no Component, and why its model is not wired where it is not.
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
	var tools []*tool
	for _, t := range a.tools {
		if t.mcp() {
			tools = append(tools, t)
		}
	}
	if a.part == nil && len(tools) == 0 {
		return
	}
	b.WriteString("      uses:\n")
	if p := a.part; p != nil {
		b.WriteString("        " + modelSocket + ":\n")
		b.WriteString("          " + p.key + ":\n")
		b.WriteString("            target: " + p.key + "\n")
		if a.model.connection.kind == "anonymous" {
			b.WriteString("            values:\n")
			b.WriteString("              unauthenticated: true\n")
		}
	}
	if len(tools) > 0 {
		b.WriteString("        " + toolsSocket + ":\n")
		for _, t := range tools {
			b.WriteString("          " + t.key + ":\n")
			b.WriteString("            target: " + t.key + "\n")
		}
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
		b.WriteString(commentLine("    ", "values.model is left for the author, since the declarative agent reads its model's id "+
			"from the environment variable "+commentText(p.id.variable)+", which the import does not read; the build "+
			"halts naming it until it is set."))
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

// writeTool writes one MCP server's instance of the MCP tool, carrying its
// description, with a comment above it naming the server, the agent reaching it,
// and its approval mode and the tools it allows, which the application applies.
func writeTool(b *strings.Builder, a *agent, t *tool) {
	b.WriteString(commentLine("    ", "The MCP server "+commentText(firstOf(t.name, mcpKind))+", which "+commentText(a.ref)+
		" reaches."))
	var applied []string
	if t.approval != "" {
		applied = append(applied, "its approval mode, "+commentText(t.approval))
	}
	if len(t.allowed) > 0 {
		applied = append(applied, "the tools of the server it allows, "+commentList(t.allowed))
	}
	if len(applied) > 0 {
		b.WriteString(commentLine("    ", "Applied by the application from the declarative agent, and taken by no key of "+
			mcpimport.Component+": "+strings.Join(applied, "; ")+"."))
	}
	b.WriteString("    " + t.key + ":\n")
	b.WriteString("      type: " + mcpimport.Component + "\n")
	if t.description != nil {
		b.WriteString("      values:\n")
		b.WriteString("        description: " + aggregateText(*t.description) + "\n")
	}
}

// aggregateText writes text read from a source as a value of the drafted
// Aggregate: escaped so it reads back as itself once the Aggregate is evaluated
// as a template, then as a double-quoted scalar.
func aggregateText(text string) string {
	return yamlString(importer.EscapeTemplateText(text))
}

// renderRelease writes the drafted Release, named name and deploying the
// Aggregate of that name: for each agent, under its instance's key, a comment
// naming what the Release supplies — the image running the application, the
// credential it presents to its model, and the one it presents to each MCP
// server whose connection presents a key — and the values its declarative agent
// states for its connections, written as stated or, where the credential
// decision wrote one empty, with no value; for each model part, an entry, with
// the endpoint its connection states where it states one; and for each MCP
// server, its address. The Release's environment and its Manifest are left
// unset, each named in a comment where it belongs. Each importer renders its
// own Release; the plugin kit renders none.
func renderRelease(name string, agents []*agent, parts []*modelPart) string {
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
		"the agents are built for.\n")
	b.WriteString("  components:\n")
	for _, a := range agents {
		writeAgentEntry(&b, a)
	}
	for _, p := range parts {
		writePartEntry(&b, p)
	}
	for _, a := range agents {
		for _, t := range a.tools {
			if t.mcp() {
				writeToolEntry(&b, t)
			}
		}
	}
	return b.String()
}

// writeAgentEntry writes one agent's entry of the Release.
func writeAgentEntry(b *strings.Builder, a *agent) {
	b.WriteString("    # Left for the Release, since the declarative agent does not state it:\n")
	b.WriteString("    #   - infra.image, the container image running the application built on Agent Framework, " +
		"holding its declarative agent\n")
	if p := a.part; p != nil && a.model.connection.kind != "anonymous" {
		b.WriteString("    #   - the credential " + a.key + " presents to the model it reaches, attached under credential " +
			"on its connection uses." + modelSocket + "." + p.key + "\n")
	}
	for _, t := range a.tools {
		if t.mcp() && t.connection.keyField != "" {
			b.WriteString("    #   - the credential " + a.key + " presents to " + t.key + ", attached under credential on " +
				"its connection uses." + toolsSocket + "." + t.key + "\n")
		}
	}
	if len(a.stated) > 0 {
		b.WriteString("    # The declarative agent also states these values for its connections, which the application " +
			"reads from it, written here for review:\n")
		for _, v := range a.stated {
			b.WriteString("    #   - " + statedLine(v) + "\n")
		}
	}
	b.WriteString("    " + a.key + ":\n")
	b.WriteString("      infra: {}\n")
}

// statedLine writes one stated value for the comment above an agent's entry:
// its field and its value as written, as a double-quoted scalar reading back as
// exactly the source's text; for a value written as a reference, the variable it
// reads; for one written as another expression, that it is one; and for a value
// the empty_credentials setting leaves out, its field alone, saying why.
func statedLine(v statedValue) string {
	field := commentText(v.field)
	switch {
	case v.emptied:
		return field + ", " + emptiedNote
	case v.text.variable != "":
		return field + ", read from the environment variable " + commentText(v.text.variable)
	case v.text.expression:
		return field + ", computed by an expression the import does not evaluate"
	case !v.text.literal():
		return field + ", with no value"
	}
	return field + ": " + yamlString(v.text.value)
}

// writePartEntry writes one model part's entry of the Release, since a build
// places only the instances a Release names: the endpoint its connection states
// as infra.endpoint, or a comment naming the variable it is read from, saying it
// is computed, or leaving it for the Release where it was written empty. A part
// whose connection states no endpoint has an entry holding no infrastructure:
// the Component's own default is its address.
func writePartEntry(b *strings.Builder, p *modelPart) {
	e := p.endpointText
	switch {
	case p.endpoint != nil:
	case e.variable != "":
		b.WriteString("    # infra.endpoint is left at the Component's default, while the declarative agent reads its " +
			"model's address from the environment variable " + commentText(e.variable) + ".\n")
	case e.expression:
		b.WriteString("    # infra.endpoint is left at the Component's default, while the declarative agent computes its " +
			"model's address with an expression the import does not evaluate.\n")
	case e.literal():
		b.WriteString("    # Left for the Release, since the draft does not write it:\n")
		b.WriteString("    #   - infra.endpoint, " + emptiedNote + "; until it is set, the Component's default " +
			"address is used\n")
	}
	b.WriteString("    " + p.key + ":\n")
	if p.endpoint == nil {
		b.WriteString("      infra: {}\n")
		return
	}
	b.WriteString("      infra:\n")
	b.WriteString("        endpoint: " + yamlString(*p.endpoint) + "\n")
}

// writeToolEntry writes one MCP server's entry of the Release: its address as
// infra.endpoint, as the MCP importer writes a remote server's, or why none is
// written, with the comment the MCP importer writes above it naming each part
// of its url the address leaves out, beside its text or, where the
// empty_credentials setting leaves it out, alone.
func writeToolEntry(b *strings.Builder, t *tool) {
	if t.left != "" {
		b.WriteString("    # Left for the Release, since the draft does not write it:\n")
		b.WriteString("    #   - " + t.left + "\n")
	}
	for _, line := range mcpimport.AddressComment(t.parts) {
		b.WriteString("    # " + line + "\n")
	}
	b.WriteString("    " + t.key + ":\n")
	if t.endpoint == nil {
		b.WriteString("      infra: {}\n")
		return
	}
	b.WriteString("      infra:\n")
	b.WriteString("        endpoint: " + yamlString(*t.endpoint) + "\n")
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
	b.WriteString("apiVersion: aggrega.ai/v1alpha1\n")
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
