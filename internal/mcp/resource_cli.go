// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"bytes"
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

// commandRunner runs an aggrega command by argument vector, writing its output
// to stdout and stderr. It is the seam both command-driven surfaces share: the
// cli:// resource, which asks for a command's help, and the generated command
// tools, which ask the command to do its work. The production value re-invokes
// the running binary, so a command's behavior and its help text both keep their
// single home in the command layer, and a test injects a fake that records the
// arguments.
type commandRunner func(ctx context.Context, args []string, stdout, stderr io.Writer) error

// cliResourceTemplate returns the CLI-help URI-template family. The template is
// parameterised by a command path; a cli:// read resolves it to that command's
// help output.
func cliResourceTemplate() resourceTemplate {
	return resourceTemplate{
		URITemplate: "cli://{command}",
		Name:        "cli-help",
		Description: "The command-line help for an aggrega command, served from the running binary. The path names a command or subcommand path (for example cli://validate or cli://plugin/gen-catalog); an empty path (cli://) serves the top-level help.",
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

// readCliResource serves cli://{command} as the command's help output, produced
// by the running binary so the help has a single home in the command layer
// rather than being restated here. An empty path serves the top-level help. A
// path with a segment that is not a plain command token — a leading dash, a dot
// segment, or any non-command character — is rejected as a resource-not-found
// so a URI can never inject a flag or escape into an arbitrary argument, and a
// command whose help the binary cannot produce is likewise a resource-not-found.
func (s *server) readCliResource(uri string) (any, *rpcError) {
	_, body, ok := splitScheme(uri)
	if !ok {
		return nil, resourceNotFound(uri)
	}
	argv, ok := cliHelpArgs(body)
	if !ok {
		return nil, resourceNotFound(uri)
	}
	run := s.runCommand
	if run == nil {
		run = selfExecCommand
	}
	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), argv, &stdout, &stderr); err != nil {
		return nil, resourceNotFound(uri)
	}
	text := stdout.String()
	if strings.TrimSpace(text) == "" {
		text = stderr.String()
	}
	return readResourceResult{
		Contents: []resourceContents{{URI: uri, MimeType: cliMimeType, Text: text}},
	}, nil
}

// cliHelpArgs turns a cli:// URI body into the argument vector that requests the
// command's help. An empty body requests the top-level help. Each path segment
// must be a plain command token — lowercase letters, digits, and internal
// dashes — so a URI cannot inject a flag (a leading dash) or a traversal (a dot
// segment). It reports ok=false for any segment that is not a command token.
func cliHelpArgs(body string) ([]string, bool) {
	body = strings.Trim(body, "/")
	if body == "" {
		return []string{"--help"}, true
	}
	segs := strings.Split(body, "/")
	for _, seg := range segs {
		if !isCommandToken(seg) {
			return nil, false
		}
	}
	return append(segs, "--help"), true
}

// isCommandToken reports whether s is a plain command token: a lowercase letter
// first, then lowercase letters, digits, or internal dashes. It excludes a
// leading dash (which would read as a flag), an empty segment, and any other
// character, so only genuine command names reach the argument vector.
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
func selfExecCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate running binary to run a command: %w", err)
	}
	cmd := exec.CommandContext(ctx, self, args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}
