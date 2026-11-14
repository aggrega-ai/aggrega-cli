// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package composeimport

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/apiversion"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/draftyaml"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// The drafts are written by hand rather than marshalled, so the key order is
// fixed and two runs over one source write the same bytes. Every value read
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

// aggregateText writes text read from a source as a value of the drafted
// Aggregate: escaped so it reads back as itself once the Aggregate is evaluated
// as a template, then as a double-quoted scalar.
func aggregateText(text string) string {
	return yamlString(importer.EscapeTemplateText(text))
}

// renderAggregate writes the drafted Aggregate, named name, for target: one
// instance per service, of the Component its decision chose, in the order the
// projects write them, carrying what translate read as its values and declaring
// its secrets, connected to each service it depends on or reads a variable from
// and each model it uses — each connection under a socket and an alias both
// named for its target's key, delivering the variables translate read from that
// service — a comment above a connection to a model the service names in the
// long form stating the variables it names; then one instance per model, of the Component
// its decision chose, writing the model's identifier, context size and runtime
// flags as values — the context size under the key ModelRuntime declares for it
// where the instance is of ModelRuntime. Comments above it state the files read
// and what of them the draft does not read.
func renderAggregate(name, target string, read []*project, types map[*service]string) string {
	var b strings.Builder
	writeFilesRead(&b, read)
	b.WriteString("apiVersion: " + apiversion.APIVersion + "\n")
	b.WriteString("kind: Aggregate\n")
	b.WriteString("metadata:\n")
	b.WriteString("  name: " + name + "\n")
	b.WriteString("  version: 0.1.0\n")
	b.WriteString("  description: " + importer.DraftDescription(Format) + "\n")
	b.WriteString("spec:\n")
	b.WriteString("  components:\n")
	for _, p := range read {
		for _, s := range p.services {
			if s.ref != s.key {
				b.WriteString(commentLine("    ", "The service "+commentText(s.ref)+"."))
			}
			b.WriteString("    " + s.key + ":\n")
			b.WriteString("      type: " + types[s] + "\n")
			writeTranslated(&b, target, s, types[s])
			var producers []string
			for _, d := range s.dependsOn {
				if i := slices.IndexFunc(p.services, func(o *service) bool { return o.name == d }); i >= 0 && p.services[i] != s {
					producers = append(producers, p.services[i].key)
				}
			}
			for key := range s.inject {
				producers = append(producers, key)
			}
			// The report above a connection to a model, keyed by the model's
			// instance key; read by key alone, never iterated.
			reports := map[string]string{}
			for _, m := range s.models {
				if i := slices.IndexFunc(p.models, func(o *model) bool { return o.name == m }); i >= 0 {
					producers = append(producers, p.models[i].key)
					if v, ok := s.varsOf(m); ok {
						reports[p.models[i].key] = modelVarsReport(s.ref, v, commentText)
					}
				}
			}
			slices.Sort(producers)
			producers = slices.Compact(producers)
			if len(producers) > 0 {
				// Each connection is written under the socket and the alias of its
				// target's key. A component reads its connections by alias whatever
				// socket each is wired into, so one alias shared by two connections
				// halts the build; the keys are distinct, so the aliases are.
				b.WriteString("      uses:\n")
				for _, producer := range producers {
					if report, ok := reports[producer]; ok {
						b.WriteString(commentLine("        ", "T"+report[1:]+"."))
					}
					b.WriteString("        " + producer + ":\n")
					b.WriteString("          " + producer + ":\n")
					b.WriteString("            target: " + producer + "\n")
					if delivered := s.inject[producer]; len(delivered) > 0 {
						b.WriteString(commentLine("            ", "The variables the source sets to what "+producer+
							" publishes, delivered from it under the names the source gives them."))
						b.WriteString(draftyaml.InjectEnv("            ", delivered))
					}
				}
			}
		}
	}
	for _, p := range read {
		for _, m := range p.models {
			if m.ref != m.key {
				b.WriteString(commentLine("    ", "The model "+commentText(m.ref)+"."))
			}
			if len(m.others) > 0 {
				declarer := "the drafted model"
				if m.referenced {
					declarer = ModelRuntime
				}
				b.WriteString(commentLine("    ", "Not carried, since "+declarer+" declares no value for it: "+
					commentList(m.others)+"."))
			}
			if m.identifier == nil {
				b.WriteString(commentLine("    ", "values.model is left for the author, since the source names no model."))
			}
			b.WriteString("    " + m.key + ":\n")
			b.WriteString("      type: " + m.component + "\n")
			var values []string
			if m.identifier != nil {
				values = append(values, "        model: "+aggregateText(*m.identifier)+"\n")
			}
			if m.contextSize != nil {
				key := "context_size"
				if m.referenced {
					key = runtimeContextKey
				}
				if text, number, ok := contextSizeText(m.contextSize); ok {
					if !number {
						text = aggregateText(text)
					}
					values = append(values, "        "+key+": "+text+"\n")
				}
			}
			if len(m.runtimeFlags) > 0 {
				values = append(values, "        runtime_flags:\n")
				for _, f := range m.runtimeFlags {
					values = append(values, "          - "+aggregateText(f)+"\n")
				}
			}
			if len(values) > 0 {
				b.WriteString("      values:\n")
				for _, v := range values {
					b.WriteString(v)
				}
			}
		}
	}
	return b.String()
}

// writeTranslated writes, inside the instance of s, an instance of typ, what
// translate read of its environment: the values of typ its source sets, beneath
// a comment naming the variables they are set as; then, for the secrets it
// attaches, what the drafted Aggregate declares of them for target, as
// importer.DeclarationsText writes it.
func writeTranslated(b *strings.Builder, target string, s *service, typ string) {
	if len(s.values) > 0 {
		variables := make([]string, 0, len(s.values))
		for _, a := range s.values {
			variables = append(variables, a.variable)
		}
		b.WriteString(commentLine("      ", "The values of "+typ+" the source sets as "+commentList(variables)+
			", the variables its Compose Recipes set from them."))
		b.WriteString("      values:\n")
		for _, a := range s.values {
			b.WriteString("        " + a.key + ": " + aggregateText(a.text) + "\n")
		}
	}
	b.WriteString(importer.DeclarationsText(target, "      ", s.key, s.secrets, false))
}

// writeFilesRead writes the comments opening the Aggregate: where the import
// read named files, the files of each project in the order Compose merges them
// and the files it does not read; and the top-level members of the files the
// draft does not read.
func writeFilesRead(b *strings.Builder, read []*project) {
	named := false
	var others []string
	for _, p := range read {
		if p.files[0].Name != "" {
			named = true
		}
		for _, o := range p.others {
			if !slices.Contains(others, o) {
				others = append(others, o)
			}
		}
	}
	slices.Sort(others)
	if named {
		b.WriteString(commentLine("", "Compose files read, each override file merged over the Compose file before it as Compose merges "+
			"them:"))
		for _, p := range read {
			b.WriteString(commentLine("", "  - "+commentText(p.files[0].Name)))
			for _, f := range p.files[1:] {
				b.WriteString(commentLine("", "  - "+commentText(f.Name)+", merged over "+commentText(p.files[0].Name)))
			}
		}
		for _, p := range read {
			if len(p.notRead) > 0 {
				b.WriteString(commentLine("", "Not read, since Compose reads the first Compose file and the first override file of a "+
					"directory in its order of preference: "+commentList(p.notRead)+"."))
			}
		}
	}
	if len(others) > 0 {
		b.WriteString(commentLine("", "Not carried, since the draft reads the services and the models alone: "+commentList(others)+
			"."))
	}
}

// composeRelease is the drafted Release, as the plugin kit's Release writer
// writes it: for each service, under its key, the infrastructure the draft
// writes — the image and the port of an instance of Server, and, for the
// compose target, the host port the same ports entry publishes, and its image's tag
// as the infra key the image table names for it — and the secrets it attaches,
// leaving for the Release what the draft does not write, with a comment above it
// naming what else the source states for the service's operation, which the
// drafts do not translate, and the members it does not carry; then, for each
// model, of ModelRuntime or drafted, an empty entry under its key.
func composeRelease(read []*project, types map[*service]string) importer.ReleaseDraft {
	draft := importer.ReleaseDraft{}
	for _, p := range read {
		for _, s := range p.services {
			draft.Entries = append(draft.Entries, serviceEntry(s, types[s]))
		}
	}
	// A build places only the instances a Release names, so each model's
	// instance has an empty entry, an instance of ModelRuntime and a drafted
	// model's alike: no source states infrastructure for either, and a drafted
	// model's instance is placed already, so it builds once its author supplies
	// the Recipe, with no entry to add by hand.
	for _, p := range read {
		for _, m := range p.models {
			draft.Entries = append(draft.Entries, importer.ReleaseEntry{Key: m.key})
		}
	}
	return draft
}

// serviceEntry is one service's entry of the Release.
func serviceEntry(s *service, typ string) importer.ReleaseEntry {
	entry := importer.ReleaseEntry{Key: s.key}
	server := typ == Server
	var infra []string
	if server {
		switch {
		case s.image != "":
			infra = append(infra, "  image: "+yamlString(s.image))
		case s.build:
			entry.Left = append(entry.Left, importer.LeftImage("since the service builds its image rather than naming one"))
		default:
			entry.Left = append(entry.Left, importer.LeftImage("since the service names no image"))
		}
		if first, ok := firstPort(s); ok {
			if n, err := strconv.Atoi(first); err == nil {
				infra = append(infra, "  port: "+strconv.Itoa(n))
				// The host side of that same entry, where the source publishes one,
				// is the port the stack is reached at on Docker Compose: the source's
				// own, so never moved.
				if len(s.ports) > 0 {
					if host, err := strconv.Atoi(s.ports[0].published); err == nil {
						entry.Published = append(entry.Published, importer.PublishedPort{Port: host, Stated: true})
					}
				}
			} else {
				entry.Left = append(entry.Left, importer.LeftItem{Path: "infra.port",
					Text: "since the container port the source states first, " + commentText(first) + ", is not one number"})
			}
		}
	}

	if s.tag != "" {
		entry.Values = append(entry.Values, importer.ReleaseValue{Path: "infra." + s.row.Tag, Value: yamlString(s.tag),
			Comment: "The tag of the image the source runs, " + commentText(s.image) + ", the " + s.row.Tag +
				" of " + typ + " its Compose Recipes run."})
	}
	entry.Secrets = append(entry.Secrets, s.secrets...)

	var stated []string
	if !server && s.image != "" && s.tag == "" {
		stated = append(stated, "  image: "+yamlString(s.image))
	}
	stated = append(stated, statedList("ports", portTexts(s.ports))...)
	stated = append(stated, statedList("expose", s.expose)...)
	stated = append(stated, statedList("volumes", volumeTexts(s.volumes))...)
	var env []envVar
	for _, e := range s.env {
		if !s.translated[e.name] {
			env = append(env, e)
		}
	}
	if len(env) > 0 {
		stated = append(stated, "  environment:")
		for _, e := range env {
			if e.value == nil {
				stated = append(stated, "    "+commentText(e.name))
				continue
			}
			stated = append(stated, "    "+commentText(e.name)+": "+yamlString(*e.value))
		}
	}
	if len(stated) > 0 {
		if server {
			entry.Comments = append(entry.Comments, "The source also states, for the Release to carry where "+typ+" takes it:")
		} else {
			entry.Comments = append(entry.Comments, "The source states, for the Release to carry where "+typ+" takes it:")
		}
		entry.Comments = append(entry.Comments, stated...)
	}
	if len(s.others) > 0 {
		entry.Comments = append(entry.Comments, "Not carried, since the draft reads no infrastructure from it: "+
			commentList(s.others)+".")
	}

	if len(infra) > 0 {
		entry.Lines = append([]string{"infra:"}, infra...)
	}
	return entry
}

// firstPort returns the port the container listens on that the source states
// first: the target of the first entry of ports, or the first entry of expose
// where ports holds none.
func firstPort(s *service) (string, bool) {
	if len(s.ports) > 0 {
		return s.ports[0].target, true
	}
	if len(s.expose) > 0 {
		return s.expose[0], true
	}
	return "", false
}

// statedList writes a list the source states for a service into the comment
// above its entry, one comment line each, each item as yamlString writes it;
// nothing for an empty list.
func statedList(key string, items []string) []string {
	if len(items) == 0 {
		return nil
	}
	out := []string{"  " + key + ":"}
	for _, item := range items {
		out = append(out, "    - "+yamlString(item))
	}
	return out
}

// portTexts returns the text of each entry of ports, as written.
func portTexts(ports []port) []string {
	out := make([]string, 0, len(ports))
	for _, p := range ports {
		out = append(out, p.text)
	}
	return out
}

// volumeTexts returns the text of each entry of volumes, as written.
func volumeTexts(volumes []volume) []string {
	out := make([]string, 0, len(volumes))
	for _, v := range volumes {
		out = append(out, v.text)
	}
	return out
}

// contextSizeText reads a model's context_size as the tree writes it: a number,
// as its text, with number set, or another scalar, as the source writes it, which
// the draft writes as a string; ok is false for any other value, which no draft
// writes.
func contextSizeText(raw json.RawMessage) (text string, number, ok bool) {
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String(), true, true
	}
	text, ok = scalarText(raw)
	return text, false, ok
}

// renderModel writes the drafted Component of one model, named name: a
// Component of the ai-model category whose values declare the identifier of the
// model it serves, its context size and its runtime flags — the members a
// Compose model is written with.
func renderModel(name string) string {
	var b strings.Builder
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
	b.WriteString("        context_size: { type: integer }\n")
	b.WriteString("        runtime_flags: { type: array, items: { type: string } }\n")
	b.WriteString("      additionalProperties: false\n")
	return b.String()
}
