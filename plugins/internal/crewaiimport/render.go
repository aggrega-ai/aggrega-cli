// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package crewaiimport

import (
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/draftyaml"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/importer"
)

// The drafts are written by hand rather than marshalled, so the key order is
// fixed and two runs over one project write the same bytes. Every text read from
// a source is written as a double-quoted YAML scalar through the drafts' shared
// writer, which reads back as exactly the source's text — escaped first, in the
// Aggregate, so it reads back as itself once the Aggregate is evaluated as a
// template — and every name written into a comment through its comment writer.
var (
	yamlString  = draftyaml.String
	commentText = draftyaml.Comment
	commentList = draftyaml.CommentList
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
	b.WriteString("apiVersion: aggrega.ai/v1alpha1\n")
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
			b.WriteString("    # The model " + commentText(p.ref) + ".\n")
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
		b.WriteString("# CrewAI declarations read:\n")
		for _, f := range files {
			b.WriteString("#   - " + commentText(f) + "\n")
		}
	}
	b.WriteString("# Not read: the crew's program, which runs its agents and its tasks and defines its tools; it runs " +
		"in the image the Release names for each agent.\n")
	for _, c := range read {
		if len(c.tasks) > 0 {
			b.WriteString("# Tasks the crew's program runs, drafted as no Component:\n")
			for _, t := range c.tasks {
				line := "#   - " + commentText(c.prefix+t.name)
				if t.agent != "" {
					line += ", assigned to " + commentText(t.agent)
				}
				b.WriteString(line + "\n")
			}
		}
		if len(c.others) > 0 {
			b.WriteString("# Not carried, since the draft reads a crew's agents and tasks alone: " + commentList(c.others) +
				".\n")
		}
		if len(c.shadowed) > 0 {
			b.WriteString("# Not read, since CrewAI reads the .jsonc file where a crew or an agent is written in both " +
				"spellings: " + commentList(c.shadowed) + ".\n")
		}
		for _, a := range c.agents {
			if a.missing {
				b.WriteString("# Not drafted: the agent " + commentText(a.ref) + ", which the crew lists and whose " +
					"declaration the import was not handed.\n")
			}
		}
		for _, n := range c.notes {
			b.WriteString("# " + n + ".\n")
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
		b.WriteString("    # The agent " + commentText(a.ref) + ".\n")
	}
	if len(a.tools) > 0 {
		b.WriteString("    # Tools the crew's program defines or loads for this agent, drafted as no Component: " +
			commentList(a.tools) + ".\n")
	}
	if len(a.others) > 0 {
		b.WriteString("    # Not carried, since " + Component + " takes no value for it: " + commentList(a.others) + ".\n")
	}
	for _, note := range []string{a.delegationNote, a.verboseNote} {
		if note != "" {
			b.WriteString("    # Not carried, so the Component's default stands: " + note + ".\n")
		}
	}
	for _, missing := range []struct {
		key  string
		text *string
	}{{"role", a.role}, {"goal", a.goal}, {"backstory", a.backstory}} {
		if missing.text == nil {
			b.WriteString("    # values." + missing.key + " is left for the author, since the declaration states none.\n")
		}
	}
	if a.part == nil {
		b.WriteString("    # The " + modelSocket + " connection is left for the author, since the agent's llm " + a.llm.why +
			"; the build halts naming the socket until one is wired.\n")
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

// renderRelease writes the drafted Release, named name and deploying the
// Aggregate of that name: for each agent, under its key, a comment naming what
// the Release supplies — the image running the crew's program, and the
// credential the agent presents to its model — and for each model part declared
// with an llm object, the members of that object, in a comment above its entry.
// The Release's environment and its Manifest are left unset, each named in a
// comment where it belongs.
func renderRelease(name string, read []*crew, parts []*modelPart) string {
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
	for _, c := range read {
		for _, a := range c.agents {
			if a.missing {
				continue
			}
			b.WriteString("    # Left for the Release, since no declaration states it:\n")
			b.WriteString("    #   - infra.image, the container image running the crew's program\n")
			if a.part != nil {
				b.WriteString("    #   - the credential " + a.key + " presents to the model it reaches, attached under " +
					"credential on its connection uses." + modelSocket + "." + a.part.key + "\n")
			}
			b.WriteString("    " + a.key + ":\n")
			b.WriteString("      infra: {}\n")
		}
	}
	for _, p := range parts {
		if len(p.stated) == 0 {
			continue
		}
		b.WriteString("    # The declaration also states, for the Release to carry where " + p.component + " takes it:\n")
		for _, m := range p.stated {
			switch {
			case m.emptied:
				b.WriteString("    #   " + commentText(m.name) + "\n")
			case m.located:
				b.WriteString("    #   " + commentText(m.name) + ": " + yamlString(m.text) + "\n")
			default:
				// A number or a boolean, written as the source writes it.
				b.WriteString("    #   " + commentText(m.name) + ": " + m.text + "\n")
			}
		}
		b.WriteString("    " + p.key + ":\n")
		b.WriteString("      infra: {}\n")
	}
	return b.String()
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
	b.WriteString("apiVersion: aggrega.ai/v1alpha1\n")
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
