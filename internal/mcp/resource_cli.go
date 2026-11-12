// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

const (
	// schemeCli addresses a command's command-line help.
	schemeCli = "cli"

	// cliMimeType is the media type served for a cli:// resource. Command help
	// is plain text.
	cliMimeType = "text/plain"
)

// commandRunner runs an aggrega command by argument vector in the working folder
// dir, writing its output to stdout and stderr. It is the seam the generated
// command tools reach when they ask a command to do its work: the production
// value re-invokes the running binary, so a command's behavior keeps its single
// home in the command layer, and a test injects a fake that records the
// arguments and the folder.
//
// dir is the folder the command starts in, chosen per call by the tool
// (commandTool.commandFolder): the workspace root the call or this server's
// launch names, so a relative path the command reads is read there. Empty starts
// the command in this server's own working folder.
type commandRunner func(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) error

// HelpRenderer renders one command's help text from the live command tree.
//
// The path names a command by its own name segments, with no binary name and no
// flags; cli://plugin/gen-catalog is the path {"plugin", "gen-catalog"}, and an
// empty path names the top-level command. It reports ok=false for a path naming
// no command, which a cli:// read answers as a resource that is not there.
//
// The command layer supplies it for the same reason it supplies the command
// surface: that layer is the only one that can see both the command tree and
// this server. A session handed none serves no cli:// resource at all, rather
// than reaching for some other way to produce the text.
type HelpRenderer func(path []string) (text string, ok bool)

// cliResourceTemplate returns the CLI-help URI-template family. The template is
// parameterised by a command path; a cli:// read resolves it to that command's
// help output.
func cliResourceTemplate() resourceTemplate {
	return resourceTemplate{
		URITemplate: "cli://{command}",
		Name:        "cli-help",
		Description: "The command-line help for an aggrega command, answered from the commands this server itself carries rather than by running anything. The path names a command or subcommand path (for example cli://validate or cli://plugin/gen-catalog); an empty path (cli://) serves the top-level help.",
		MimeType:    cliMimeType,
	}
}

// cliRootResource is the single concrete entry point for the CLI-help family:
// the top-level aggrega help. The rest of the family is templated, resolved
// per command path at read time.
func cliRootResource() resource {
	return resource{
		URI:         "cli://",
		Name:        "aggrega command help",
		Description: "Top-level aggrega command-line help. Read cli://<command> for a specific command's help.",
		MimeType:    cliMimeType,
	}
}

// readCliResource serves cli://{command} as that command's help text, rendered
// from the command tree this process already holds, so the help keeps its single
// home in the command layer rather than being restated here. An empty path
// serves the top-level help.
//
// Three cases are one answer, a resource that is not there. A path with a
// segment that is not a plain command token — a leading dash, a dot segment, or
// any non-command character — is refused before any renderer is consulted, so a
// URI can never turn into a flag or climb out into something else. A path the
// renderer resolves to no command is refused because there is no such command to
// serve help for. And a session that was handed no renderer refuses
// every path in the family: there is no second way to produce the text, so the
// honest answer is that the resource is not there.
func (s *server) readCliResource(uri string) (readResourceResult, *rpcError) {
	_, body, ok := splitScheme(uri)
	if !ok {
		return readResourceResult{}, resourceNotFound(uri)
	}
	path, ok := cliCommandPath(body)
	if !ok {
		return readResourceResult{}, resourceNotFound(uri)
	}
	if s.renderHelp == nil {
		return readResourceResult{}, resourceNotFound(uri)
	}
	text, ok := s.renderHelp(path)
	if !ok {
		return readResourceResult{}, resourceNotFound(uri)
	}
	return readResourceResult{
		Contents: []resourceContents{{URI: uri, MimeType: cliMimeType, Text: text}},
	}, nil
}

// cliCommandPath turns a cli:// URI body into the command path that names the
// command whose help is wanted. An empty body names the top-level command and
// yields an empty path. Each path segment must be a plain command token —
// lowercase letters, digits, and internal dashes — so a URI cannot turn into a
// flag (a leading dash) or a traversal (a dot segment). It reports ok=false for
// any segment that is not a command token.
func cliCommandPath(body string) ([]string, bool) {
	body = strings.Trim(body, "/")
	if body == "" {
		return nil, true
	}
	segs := strings.Split(body, "/")
	for _, seg := range segs {
		if !isCommandToken(seg) {
			return nil, false
		}
	}
	return segs, true
}

// isCommandToken reports whether s is a plain command token: a lowercase letter
// first, then lowercase letters, digits, or internal dashes. It excludes a
// leading dash (which would read as a flag), an empty segment, and any other
// character, so only genuine command names reach the command path.
func isCommandToken(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case i > 0 && r >= '0' && r <= '9':
		case i > 0 && r == '-':
		default:
			return false
		}
	}
	return true
}

// selfExecCommand runs a command by re-invoking the running binary with the
// given argument vector as a subprocess, so the command's behavior keeps its
// single home in the command layer rather than being reimplemented here.
//
// The subprocess is also what keeps this server's stream discipline
// structural rather than merely intended. The child writes to the buffers the
// caller supplies, on its own file descriptors, so a command that writes to the
// process stdout directly — several do, rather than to the writer their command
// object carries — cannot reach the parent's stdout, where the protocol stream
// lives.
//
// The child starts in dir, which is how the folder a call's command runs in
// reaches it; an empty dir leaves the child in this server's own working folder.
//
// A command run this way carries no live progress of any kind to the caller, and
// that is a bound of the subprocess rather than a gap a command can close: the
// child's error stream is a buffer rather than a terminal, so its own progress
// surface draws nothing, and nothing the child's operation reports — a step it
// waits on, a registry that asked it to slow down — crosses the process boundary
// to this server's progress notifications. The caller sees the command's output
// once it ends.
func selfExecCommand(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate running binary to run a command: %w", err)
	}
	cmd := exec.CommandContext(ctx, self, args...)
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}
