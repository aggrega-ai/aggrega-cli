// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcpimport

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/apiversion"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/draftyaml"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// The two drafts are written by hand rather than marshalled, so the key order is
// fixed and two runs over one source write the same bytes. Every value read
// from a source is written as a double-quoted YAML scalar through yamlString,
// which reads back as exactly the source's text.

// renderAggregate writes the drafted Aggregate, named name: one instance of
// Component per server, under its key, in the order the sources list them, with
// a comment naming the server where its key does not.
func renderAggregate(name string, servers []server) string {
	var b strings.Builder
	b.WriteString("apiVersion: " + apiversion.APIVersion + "\n")
	b.WriteString("kind: Aggregate\n")
	b.WriteString("metadata:\n")
	b.WriteString("  name: " + name + "\n")
	b.WriteString("  version: 0.1.0\n")
	b.WriteString("  description: " + importer.DraftDescription(Format) + "\n")
	b.WriteString("spec:\n")
	b.WriteString("  components:\n")
	for _, s := range servers {
		if s.ref != s.key {
			b.WriteString(commentLine("    ", "The MCP server "+commentText(s.ref)+"."))
		}
		b.WriteString("    " + s.key + ":\n")
		b.WriteString("      type: " + Component + "\n")
	}
	return b.String()
}

// mcpRelease is the drafted Release, as the plugin kit's Release writer writes
// it: for each server, under its key, the infrastructure the source states and
// the server_env attachment carrying its environment, leaving for the Release
// what the draft does not write, with a comment above it naming what it does not
// carry, the headers the server is sent and the parts of its address
// infra.endpoint leaves out.
func mcpRelease(servers []server) importer.ReleaseDraft {
	draft := importer.ReleaseDraft{}
	for _, s := range servers {
		draft.Entries = append(draft.Entries, serverEntry(s))
	}
	return draft
}

// serverEntry is one server's entry of the Release.
func serverEntry(s server) importer.ReleaseEntry {
	entry := importer.ReleaseEntry{Key: s.key}
	runtime := s.runtime
	if s.runtimeFrom && len(s.command) > 0 {
		runtime = runtimeOf(s.command[0].text)
	}
	entry.Left = append(entry.Left, s.left...)
	for j, c := range s.command {
		if c.unset {
			entry.Left = append(entry.Left, importer.LeftItem{Path: fmt.Sprintf("infra.stdio_command.%d", j),
				Text: "written empty, since " + c.hint})
		}
	}
	switch {
	case !s.stdio || len(s.command) == 0 || runtime != "":
	case s.command[0].text == "":
		entry.Left = append(entry.Left, importer.LeftItem{Path: "infra.stdio_runtime",
			Text: "since the command is written empty"})
	default:
		entry.Left = append(entry.Left, importer.LeftItem{Path: "infra.stdio_runtime",
			Text: "since the command names no launcher a bridge image carries"})
	}
	var entries []namedValue
	for _, e := range s.env {
		value := "the value of " + envSlot + "." + commentText(e.name)
		switch {
		case e.value != nil && e.value.reference:
			entry.Left = append(entry.Left, importer.LeftItem{Text: value + ", which the source reads from " +
				commentText(e.value.text)})
		case e.value == nil && e.hint != "":
			entry.Left = append(entry.Left, importer.LeftItem{Text: value + ", since " + e.hint})
		case e.value == nil:
			entry.Left = append(entry.Left, importer.LeftItem{Text: value})
		}
		entries = append(entries, e)
	}

	if len(s.notCarried) > 0 {
		entry.Comments = append(entry.Comments, "Not carried, since no infrastructure key of "+Component+" takes it: "+
			commentList(s.notCarried)+".")
	}
	if len(s.headers) > 0 {
		entry.Comments = append(entry.Comments, "The headers the source sends this server, which no infrastructure key of "+
			Component+" takes, since a remote server's credential is held by each consumer, on its connection to the tool:")
		for _, h := range s.headers {
			entry.Comments = append(entry.Comments, "  - "+headerLine(h))
		}
	}
	entry.Comments = append(entry.Comments, AddressComment(s.addressParts)...)
	entry.Comments = append(entry.Comments, s.notes...)

	var infra []string
	if len(s.command) > 0 {
		infra = append(infra, "  stdio_command:")
		for _, c := range s.command {
			infra = append(infra, "    - "+yamlString(c.text))
		}
	}
	if s.stdio && runtime != "" {
		infra = append(infra, "  stdio_runtime: "+runtime)
	}
	if s.endpoint != nil {
		infra = append(infra, "  endpoint: "+yamlString(s.endpoint.text))
	}
	if s.image != "" {
		infra = append(infra, "  image: "+yamlString(s.image))
	}
	if len(infra) > 0 {
		entry.Lines = append(append(entry.Lines, "infra:"), infra...)
	}
	if !s.stdio || len(entries) == 0 {
		return entry
	}
	entry.Lines = append(entry.Lines,
		"features:",
		"  "+envSlot+":",
		"    type: "+secretSet,
		"    infra:",
		"      entries:")
	for _, e := range entries {
		entry.Lines = append(entry.Lines, "        - key: "+yamlString(e.name))
		if e.value != nil && !e.value.reference {
			entry.Lines = append(entry.Lines, "          value: "+yamlString(e.value.text))
		}
	}
	return entry
}

// headerLine writes one header a server is sent, for the comment above its
// entry: its name and its value as written, as a double-quoted scalar reading
// back as exactly the source's text; for a value that is one variable reference,
// the reference it reads from; for a value the empty_credentials setting leaves
// out, or one the source does not write, its name alone, saying why.
func headerLine(h namedValue) string {
	name := commentText(h.name)
	switch {
	case h.emptied:
		return name + ", its value left out, as the empty_credentials setting asks"
	case h.value == nil:
		return name + ", with no value, since " + h.hint
	case h.value.reference:
		return name + ", its value read from " + commentText(h.value.text)
	}
	return name + ": " + yamlString(h.value.text)
}

// Every value read from a source is written through the drafts' shared writers:
// a scalar reading back as exactly the source's text, text inside a comment that
// nothing a source holds can end, and each comment line of the Aggregate escaped
// so it reads back as written once the Aggregate is evaluated as a template.
var (
	yamlString  = draftyaml.String
	commentText = draftyaml.Comment
	commentList = draftyaml.CommentList
	commentLine = draftyaml.CommentLine
)

// jsonString writes s as a JSON string.
func jsonString(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}
