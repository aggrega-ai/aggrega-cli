// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package crewaiimport

import (
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/apiversion"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/draftyaml"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// The drafts are written by hand rather than marshalled, so the key order is
// fixed and two runs over one project write the same bytes. Every text read from
// a source is written as a double-quoted YAML scalar through the drafts' shared
// writer, which reads back as exactly the source's text — escaped first, in the
// Aggregate, so it reads back as itself once the Aggregate is evaluated as a
// template — and every name written into a comment through its comment writer,
// each comment line of the Aggregate through the drafts' comment-line writer,
// which escapes it so it too reads back as written once evaluated.
var (
	yamlString  = draftyaml.String
	commentText = draftyaml.Comment
	commentList = draftyaml.CommentList
	commentLine = draftyaml.CommentLine
)

// The values of log_level an agent is drafted with: DEBUG for an agent declared
// verbose, which narrates each step of its reasoning, and INFO for one that is
// not, which reports progress and problems only.
const (
	logVerbose = "DEBUG"
	logQuiet   = "INFO"
)

// renderAggregate writes the drafted Aggregate, named name: one instance of
// Component per agent, crew by crew in the order the declarations write them,
// carrying its values and wired to its model part; then one instance per model
// part, serving its model. Comments above it name the declarations read, what the
// draft does not read, and each crew's tasks; comments above an agent name what
// of its declaration the draft does not carry.
func renderAggregate(name string, read []*crew, parts []*modelPart) string {
	var b strings.Builder
	writeHeader(&b, read)
	b.WriteString("apiVersion: " + apiversion.APIVersion + "\n")
	b.WriteString("kind: Aggregate\n")
	b.WriteString("metadata:\n")
	b.WriteString("  name: " + name + "\n")
	b.WriteString("  version: 0.1.0\n")
	b.WriteString("  description: " + importer.DraftDescription(Format) + "\n")
	b.WriteString("spec:\n")
	b.WriteString("  components:\n")
	for _, c := range read {
		for _, a := range c.agents {
			if !a.missing {
				writeAgent(&b, a)
			}
		}
	}
	for _, p := range parts {
		if p.ref != p.key {
			b.WriteString(commentLine("    ", "The model "+commentText(p.ref)+"."))
		}
		b.WriteString("    " + p.key + ":\n")
		b.WriteString("      type: " + p.component + "\n")
		b.WriteString("      values:\n")
		b.WriteString("        model: " + aggregateText(p.model) + "\n")
	}
	return b.String()
}

// writeHeader writes the comments opening the Aggregate: the declaration files
// read, crew by crew; that the crew's program is not read; each crew's tasks and
// the agents they are assigned to; the members of crew.jsonc the draft does not
// read; and what the reading of a crew found that the draft cannot write.
func writeHeader(b *strings.Builder, read []*crew) {
	var files []string
	for _, c := range read {
		files = append(files, c.files...)
	}
	if len(files) > 0 {
		b.WriteString(commentLine("", "CrewAI declarations read:"))
		for _, f := range files {
			b.WriteString(commentLine("", "  - "+commentText(f)))
		}
	}
	b.WriteString(commentLine("", "Not read: the crew's program, which runs its agents and its tasks and defines its tools; it runs "+
		"in the image the Release names for each agent."))
	for _, c := range read {
		if len(c.tasks) > 0 {
			b.WriteString(commentLine("", "Tasks the crew's program runs, drafted as no Component:"))
			for _, t := range c.tasks {
				line := "  - " + commentText(c.prefix+t.name)
				if t.agent != "" {
					line += ", assigned to " + commentText(t.agent)
				}
				b.WriteString(commentLine("", line))
			}
		}
		if len(c.others) > 0 {
			b.WriteString(commentLine("", "Not carried, since the draft reads a crew's agents and tasks alone: "+commentList(c.others)+
				"."))
		}
		if len(c.shadowed) > 0 {
			b.WriteString(commentLine("", "Not read, since CrewAI reads the .jsonc file where a crew or an agent is written in both "+
				"spellings: "+commentList(c.shadowed)+"."))
		}
		for _, a := range c.agents {
			if a.missing {
				b.WriteString(commentLine("", "Not drafted: the agent "+commentText(a.ref)+", which the crew lists and whose "+
					"declaration the import was not handed."))
			}
		}
		for _, n := range c.notes {
			b.WriteString(commentLine("", n+"."))
		}
	}
}

// writeAgent writes one agent's instance of Component: its values, and its model
// connection where it has a model part, with comments above it naming the agent
// where its key does not, the tools its program defines, what of its
// declaration the draft does not carry, and why its model is not wired where it
// is not.
func writeAgent(b *strings.Builder, a *agent) {
	if a.ref != a.key {
		b.WriteString(commentLine("    ", "The agent "+commentText(a.ref)+"."))
	}
	if len(a.tools) > 0 {
		b.WriteString(commentLine("    ", "Tools the crew's program defines or loads for this agent, drafted as no Component: "+
			commentList(a.tools)+"."))
	}
	if len(a.others) > 0 {
		b.WriteString(commentLine("    ", "Not carried, since "+Component+" takes no value for it: "+commentList(a.others)+"."))
	}
	for _, note := range []string{a.delegationNote, a.verboseNote} {
		if note != "" {
			b.WriteString(commentLine("    ", "Not carried, so the Component's default stands: "+note+"."))
		}
	}
	for _, missing := range []struct {
		key  string
		text *string
	}{{"role", a.role}, {"goal", a.goal}, {"backstory", a.backstory}} {
		if missing.text == nil {
			b.WriteString(commentLine("    ", "values."+missing.key+" is left for the author, since the declaration states none."))
		}
	}
	if a.part == nil {
		b.WriteString(commentLine("    ", "The "+modelSocket+" connection is left for the author, since the agent's llm "+a.llm.why+
			"; the build halts naming the socket until one is wired."))
	}
	b.WriteString("    " + a.key + ":\n")
	b.WriteString("      type: " + Component + "\n")
	b.WriteString("      values:\n")
	for _, v := range []struct {
		key  string
		text *string
	}{{"role", a.role}, {"goal", a.goal}, {"backstory", a.backstory}} {
		if v.text != nil {
			b.WriteString("        " + v.key + ": " + aggregateText(*v.text) + "\n")
		}
	}
	switch {
	case a.delegationNote != "":
	case a.delegation:
		b.WriteString("        allow_delegation: true\n")
	default:
		b.WriteString("        allow_delegation: false\n")
	}
	if a.verboseNote == "" {
		level := logQuiet
		if a.verbose {
			level = logVerbose
		}
		b.WriteString("        log_level: " + level + "\n")
	}
	if a.part != nil {
		b.WriteString("      uses:\n")
		b.WriteString("        " + modelSocket + ":\n")
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

// crewaiRelease is the drafted Release, as the plugin kit's Release writer
// writes it: for each agent, under its key, leaving for the Release the image
// running the crew's program and the credential the agent presents to its model;
// and for each model part an entry under its key, since a build places only the
// instances a Release names, with the members of an llm object it is declared
// with in a comment above it.
func crewaiRelease(read []*crew, parts []*modelPart) importer.ReleaseDraft {
	draft := importer.ReleaseDraft{}
	for _, c := range read {
		for _, a := range c.agents {
			if a.missing {
				continue
			}
			entry := importer.ReleaseEntry{Key: a.key, Left: []importer.LeftItem{{Path: "infra.image",
				Text: "the container image running the crew's program"}}}
			if a.part != nil {
				entry.Left = append(entry.Left, importer.ConnectionCredential(a.key, modelSocket, a.part.key, a.part.key))
			}
			draft.Entries = append(draft.Entries, entry)
		}
	}
	for _, p := range parts {
		entry := importer.ReleaseEntry{Key: p.key}
		if len(p.stated) > 0 {
			entry.Comments = append(entry.Comments, "The declaration also states, for the Release to carry where "+
				p.component+" takes it:")
		}
		for _, m := range p.stated {
			switch {
			case m.emptied:
				entry.Comments = append(entry.Comments, "  "+commentText(m.name))
			case m.located:
				entry.Comments = append(entry.Comments, "  "+commentText(m.name)+": "+yamlString(m.text))
			default:
				// A number or a boolean, written as the source writes it.
				entry.Comments = append(entry.Comments, "  "+commentText(m.name)+": "+m.text)
			}
		}
		draft.Entries = append(draft.Entries, entry)
	}
	return draft
}

// renderModel writes the drafted Component of a model provider the catalog
// holds no Component for, named name: a Component of the ai-model category whose
// values declare the name of the model it serves. The label the agent's model
// socket selects a model by, naming the API its endpoint speaks, and a Recipe
// publishing its endpoint and its model, are the author's to supply, as the
// comment opening it says.
func renderModel(name string) string {
	var b strings.Builder
	b.WriteString("# The model.aggrega.ai/api label, naming the API this provider's endpoint speaks, is left for the " +
		"author, as is a Recipe publishing its endpoint and its model: the CrewAI agent's model socket selects a " +
		"Component carrying the label.\n")
	b.WriteString("apiVersion: " + apiversion.APIVersion + "\n")
	b.WriteString("kind: Component\n")
	b.WriteString("metadata:\n")
	b.WriteString("  name: " + name + "\n")
	b.WriteString("  version: 0.1.0\n")
	b.WriteString("  labels:\n")
	b.WriteString("    component.aggrega.ai/type: resource\n")
	b.WriteString("    component.aggrega.ai/category: ai-model\n")
	b.WriteString("spec:\n")
	b.WriteString("  schema:\n")
	b.WriteString("    values:\n")
	b.WriteString("      type: object\n")
	b.WriteString("      properties:\n")
	b.WriteString("        model: { type: string }\n")
	b.WriteString("      additionalProperties: false\n")
	return b.String()
}
