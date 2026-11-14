// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package adkimport

import (
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/apiversion"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/draftyaml"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// The drafts are written by hand rather than marshalled, so the key order is
// fixed and two runs over one application write the same bytes. Every text read
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
// Component per agent tree, in the order the import was handed their root
// configurations, carrying its root agent's description and wired to its model
// part; then one instance per model part, serving its model. Comments above it
// name the configurations read, what the draft does not read, how ADK states its
// Agent Config, and the handed files no tree reads; comments above an instance
// name the tree's other agents, the sub-agents it does not read, its tools, the
// classes ADK deprecates and the models its other agents ask for.
func renderAggregate(name string, apps []*application, parts []*modelPart, unread []string) string {
	var b strings.Builder
	writeHeader(&b, apps, unread)
	b.WriteString("apiVersion: " + apiversion.APIVersion + "\n")
	b.WriteString("kind: Aggregate\n")
	b.WriteString("metadata:\n")
	b.WriteString("  name: " + name + "\n")
	b.WriteString("  version: 0.1.0\n")
	b.WriteString("  description: " + importer.DraftDescription(Format) + "\n")
	b.WriteString("spec:\n")
	b.WriteString("  components:\n")
	for _, app := range apps {
		writeApplication(&b, app)
	}
	for _, p := range parts {
		b.WriteString(commentLine("    ", "The model "+commentText(p.ref)+"."))
		b.WriteString("    " + p.key + ":\n")
		b.WriteString("      type: " + p.component + "\n")
		b.WriteString("      values:\n")
		b.WriteString("        model: " + aggregateText(p.model) + "\n")
	}
	return b.String()
}

// writeHeader writes the comments opening the Aggregate: the configurations read,
// tree by tree; that the application's code and the environment file are not
// read; how ADK states its Agent Config; and the files the import was handed that
// no tree reads.
func writeHeader(b *strings.Builder, apps []*application, unread []string) {
	var files []string
	for _, app := range apps {
		for _, f := range app.files {
			if f != "" {
				files = append(files, f)
			}
		}
	}
	if len(files) > 0 {
		b.WriteString(commentLine("", "Google ADK agent configurations read:"))
		for _, f := range files {
			b.WriteString(commentLine("", "  - "+commentText(f)))
		}
	}
	b.WriteString(commentLine("", "Not read: the application's code, which defines the tools, callbacks and agents a configuration "+
		"names by their code, nor the environment file ADK's scaffold writes beside "+RootFile+"; the "+
		"application runs in the image the Release names for each instance, and loads its configurations itself."))
	b.WriteString(commentLine("", "ADK states that its Agent Config is experimental. An agent's model is a Gemini model by "+
		"default; ADK serves some other providers' models by name through classes of its own, such as "+
		"claude-sonnet-4-5 and gpt-4o, and others through a LiteLLM model string."))
	if len(unread) > 0 {
		b.WriteString(commentLine("", "Not read, since no agent configuration names it: "+commentList(unread)+"."))
	}
}

// writeApplication writes one tree's instance of Component: its root agent's
// description, and its model connection where it has a model part, with comments
// above it naming the tree, its other agents, the sub-agents it does not read,
// its tools, the classes ADK deprecates, the models its other agents ask for, and
// why its model is not wired where it is not.
func writeApplication(b *strings.Builder, app *application) {
	root := app.root
	tree := "the configuration handed"
	if app.file != "" {
		tree = commentText(app.file)
	}
	b.WriteString(commentLine("    ", "The agent tree of "+tree+", served from its root agent "+commentText(root.ref)+"."))
	var subs, missing, deprecated, models []string
	for _, a := range app.agents {
		if a != root {
			subs = append(subs, a.ref)
		}
		for _, s := range a.subs {
			if s.agent == nil {
				line := "a sub-agent of " + commentText(a.ref) + ", which " + s.why
				if s.notHanded {
					line += ". " + handHint
				}
				missing = append(missing, line)
			}
		}
		if class := a.deprecated(); class != "" {
			deprecated = append(deprecated, "the "+class+" "+commentText(a.ref))
		}
		if asksOtherModel(app, a) {
			models = append(models, commentText(a.model)+" ("+commentText(a.ref)+", served through "+
				familyOf(a.model)+")")
		}
	}
	if len(subs) > 0 {
		b.WriteString(commentLine("    ", "Sub-agents ADK builds inside this application, drafted as no instance of their own: "+
			commentList(subs)+"."))
	}
	for _, m := range missing {
		b.WriteString(commentLine("    ", "Not read: "+m+"."))
	}
	var shipped, coded []string
	for _, a := range app.agents {
		for _, t := range a.tools {
			list := &coded
			if t.own() {
				list = &shipped
			}
			if !containsText(*list, t.name) {
				*list = append(*list, t.name)
			}
		}
	}
	if len(shipped) > 0 {
		b.WriteString(commentLine("    ", "Tools ADK ships, which run inside the application, drafted as no Component: "+
			commentList(shipped)+"."))
	}
	if len(coded) > 0 {
		b.WriteString(commentLine("    ", "Tools the application's code defines, drafted as no Component: "+commentList(coded)+"."))
	}
	if len(deprecated) > 0 {
		b.WriteString(commentLine("    ", "Deprecated by ADK in favour of Workflow: "+strings.Join(deprecated, ", ")+"."))
	}
	// The line names the key the instance's model connection delivers, so it is
	// written only where the instance has that connection.
	if len(models) > 0 && app.part != nil {
		b.WriteString(commentLine("    ", "Models other agents of the tree ask for, each with the family ADK serves it "+
			"through; one whose requests ADK sends to the Gemini API is sent with the key this instance's "+
			modelSocket+" connection delivers: "+strings.Join(models, ", ")+"."))
	}
	if app.part == nil {
		b.WriteString(commentLine("    ", "The "+modelSocket+" connection is left for the author, since "+unwiredWhy(app)+
			"; the build halts naming the socket until one is wired."))
	}
	b.WriteString("    " + app.key + ":\n")
	b.WriteString("      type: " + Component + "\n")
	if root.description != nil {
		b.WriteString("      values:\n")
		b.WriteString("        description: " + aggregateText(*root.description) + "\n")
	}
	if app.part != nil {
		b.WriteString("      uses:\n")
		b.WriteString("        " + modelSocket + ":\n")
		b.WriteString("          " + app.part.key + ":\n")
		b.WriteString("            target: " + app.part.key + "\n")
	}
}

// containsText reports whether texts holds text.
func containsText(texts []string, text string) bool {
	for _, t := range texts {
		if t == text {
			return true
		}
	}
	return false
}

// aggregateText writes text read from a source as a value of the drafted
// Aggregate: escaped so it reads back as itself once the Aggregate is evaluated
// as a template, then as a double-quoted scalar.
func aggregateText(text string) string {
	return yamlString(importer.EscapeTemplateText(text))
}

// adkRelease is the drafted Release, as the plugin kit's Release writer writes
// it: for each tree, under its instance's key, leaving for the Release the image
// running the application and the credential the instance presents to its model,
// with a comment naming the values the tree's configurations hand their tools and
// their model requests, written as stated or, where the credential decision wrote
// one empty, with no value; then an entry for each model part, since a build
// places only the instances a Release names.
func adkRelease(apps []*application, parts []*modelPart) importer.ReleaseDraft {
	draft := importer.ReleaseDraft{}
	for _, app := range apps {
		entry := importer.ReleaseEntry{Key: app.key, Left: []importer.LeftItem{{Path: "infra.image",
			Text: "the container image running the application built on ADK, holding its configurations"}}}
		if app.part != nil {
			entry.Left = append(entry.Left, importer.ConnectionCredential(app.key, modelSocket, app.part.key, app.part.key))
		}
		if len(app.stated) > 0 {
			entry.Comments = append(entry.Comments, "The configurations also hand these values to their tools and their "+
				"model requests, which the application reads from its own configurations, written here for review:")
			for _, v := range app.stated {
				line := "  " + commentText(v.agent.ref) + " " + commentText(v.field)
				switch {
				case v.emptied:
				case v.located:
					line += ": " + yamlString(v.text)
				default:
					// A number, a boolean or null, written as the configuration writes it.
					line += ": " + v.text
				}
				entry.Comments = append(entry.Comments, line)
			}
		}
		draft.Entries = append(draft.Entries, entry)
	}
	for _, p := range parts {
		draft.Entries = append(draft.Entries, importer.ReleaseEntry{Key: p.key})
	}
	return draft
}

// renderModel writes the drafted Gemini Component, named name, for a catalog
// holding none: a Component of the ai-model category whose model.aggrega.ai/api
// label names the Gemini API, the label the ADK agent's model socket selects a
// model by, and whose values declare the name of the model it serves. A Recipe
// publishing its endpoint and its model is the author's to supply, as the
// comment opening it says.
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
	b.WriteString("    model.aggrega.ai/api: " + provider + "\n")
	b.WriteString("spec:\n")
	b.WriteString("  schema:\n")
	b.WriteString("    values:\n")
	b.WriteString("      type: object\n")
	b.WriteString("      properties:\n")
	b.WriteString("        model: { type: string }\n")
	b.WriteString("      additionalProperties: false\n")
	return b.String()
}
