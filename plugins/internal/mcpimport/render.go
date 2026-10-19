// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcpimport

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/aggrega-ai/aggrega-pdk-go/pkg/importer"
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
	b.WriteString("apiVersion: aggrega.ai/v1alpha1\n")
	b.WriteString("kind: Aggregate\n")
	b.WriteString("metadata:\n")
	b.WriteString("  name: " + name + "\n")
	b.WriteString("  version: 0.1.0\n")
	b.WriteString("  description: " + importer.DraftDescription(Format) + "\n")
	b.WriteString("spec:\n")
	b.WriteString("  components:\n")
	for _, s := range servers {
		if s.ref != s.key {
			b.WriteString("    # The MCP server " + commentText(s.ref) + ".\n")
		}
		b.WriteString("    " + s.key + ":\n")
		b.WriteString("      type: " + Component + "\n")
	}
	return b.String()
}

// renderRelease writes the drafted Release, named name and deploying the
// Aggregate of that name: for each server, under its key, the infrastructure the
// source states and the server_env attachment carrying its environment, with a
// comment above it naming what the draft leaves for the Release and what it does
// not carry. The Release's environment and its Manifest are left unset, each
// named in a comment where it belongs.
func renderRelease(name string, servers []server) string {
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
		"the servers are built for.\n")
	b.WriteString("  components:\n")
	for _, s := range servers {
		writeServer(&b, s)
	}
	return b.String()
}

// writeServer writes one server's entry of the Release.
func writeServer(b *strings.Builder, s server) {
	runtime := s.runtime
	if s.runtimeFrom && len(s.command) > 0 {
		runtime = runtimeOf(s.command[0].text)
	}
	left := append([]string{}, s.left...)
	for j, c := range s.command {
		if c.unset {
			left = append(left, fmt.Sprintf("infra.stdio_command.%d, written empty, since %s", j, c.hint))
		}
	}
	switch {
	case !s.stdio || len(s.command) == 0 || runtime != "":
	case s.command[0].text == "":
		left = append(left, "infra.stdio_runtime, since the command is written empty")
	default:
		left = append(left, "infra.stdio_runtime, since the command names no launcher a bridge image carries")
	}
	var entries []envEntry
	for _, e := range s.env {
		switch {
		case e.value != nil && e.value.reference:
			left = append(left, "the value of "+envSlot+"."+commentText(e.name)+", which the source reads from "+
				commentText(e.value.text))
		case e.value == nil && e.hint != "":
			left = append(left, "the value of "+envSlot+"."+commentText(e.name)+", since "+e.hint)
		case e.value == nil:
			left = append(left, "the value of "+envSlot+"."+commentText(e.name))
		}
		entries = append(entries, e)
	}

	if len(left) > 0 {
		b.WriteString("    # Left for the Release, since the draft does not write it:\n")
		for _, l := range left {
			b.WriteString("    #   - " + l + "\n")
		}
	}
	if len(s.notCarried) > 0 {
		b.WriteString("    # Not carried, since no infrastructure key of " + Component + " takes it: " +
			commentList(s.notCarried) + ".\n")
	}
	switch len(s.headers) {
	case 0:
	case 1:
		b.WriteString("    # The source sends this server the header " + commentText(s.headers[0]) + ". Its value is not " +
			"written: a remote server's credential is held by each consumer, on its connection to the tool.\n")
	default:
		b.WriteString("    # The source sends this server the headers " + commentList(s.headers) + ". Their values are " +
			"not written: a remote server's credential is held by each consumer, on its connection to the tool.\n")
	}
	for _, n := range s.notes {
		b.WriteString("    # " + n + "\n")
	}

	b.WriteString("    " + s.key + ":\n")
	var infra []string
	if len(s.command) > 0 {
		infra = append(infra, "        stdio_command:\n")
		for _, c := range s.command {
			infra = append(infra, "          - "+yamlString(c.text)+"\n")
		}
	}
	if s.stdio && runtime != "" {
		infra = append(infra, "        stdio_runtime: "+runtime+"\n")
	}
	if s.endpoint != nil {
		infra = append(infra, "        endpoint: "+yamlString(s.endpoint.text)+"\n")
	}
	if s.image != "" {
		infra = append(infra, "        image: "+yamlString(s.image)+"\n")
	}
	if len(infra) == 0 {
		b.WriteString("      infra: {}\n")
	} else {
		b.WriteString("      infra:\n")
		for _, line := range infra {
			b.WriteString(line)
		}
	}
	if !s.stdio || len(entries) == 0 {
		return
	}
	b.WriteString("      features:\n")
	b.WriteString("        " + envSlot + ":\n")
	b.WriteString("          type: " + secretSet + "\n")
	b.WriteString("          infra:\n")
	b.WriteString("            entries:\n")
	for _, e := range entries {
		b.WriteString("              - key: " + yamlString(e.name) + "\n")
		if e.value != nil && !e.value.reference {
			b.WriteString("                value: " + yamlString(e.value.text) + "\n")
		}
	}
}

// yamlString writes s as a double-quoted YAML scalar reading back as exactly s:
// a double quote and a backslash escaped, every printable ASCII character as it
// is, and every other character as its \u or \U escape, so no character a YAML
// reader could take for a line break, and no control character, is written
// raw. A byte that is not part of a UTF-8 sequence is written as U+FFFD.
func yamlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r >= 0x20 && r < 0x7f:
			b.WriteRune(r)
		case r <= 0xffff:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			fmt.Fprintf(&b, `\U%08X`, r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// commentText writes text read from a source into a comment: as it is where it
// holds only letters, digits and the punctuation a name or a variable reference
// is written with, and otherwise quoted as yamlString writes it, so nothing a
// source holds can end the comment's line.
func commentText(text string) string {
	if text == "" {
		return `""`
	}
	for _, r := range text {
		if r >= utf8.RuneSelf || !(r == '-' || r == '_' || r == '.' || r == '/' || r == ':' || r == '@' || r == '$' ||
			r == '{' || r == '}' || r == '#' || r == '+' || r == '=' || r == ' ' ||
			(r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			return yamlString(text)
		}
	}
	return text
}

// commentList writes names into a comment, each through commentText, joined by
// commas.
func commentList(names []string) string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, commentText(n))
	}
	return strings.Join(out, ", ")
}

// jsonString writes s as a JSON string.
func jsonString(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}
