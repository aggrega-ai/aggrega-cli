// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/cliplugin"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
)

// TestPluginMultiCommand_InstallAndRoute is the end-to-end proof that one CLI
// guest backing several commands installs as several commands and routes on the
// invoked command name. It builds a real wasip1 guest declaring "greet-a" and
// "greet-b", installs it through the real describe gate (asserting both commands
// register in the index with their own short help, sharing the one resolved
// URI), then runs each command against the real guest through the runner and
// asserts the guest reached the correct branch's streamed output — proof the
// invoked command name flows into CLIRunInput.Command and the guest routes on it.
func TestPluginMultiCommand_InstallAndRoute(t *testing.T) {
	// Not parallel: sets the process environment and swaps the process-global registration and install seams.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	withCLIRegistrations(t, nil)
	guest := buildInstallGuest(t, "cli-multi-guest")

	// Only the fetch is faked (no registry); the describe gate is the real one.
	withInstallSeams(t, fixedFetch("oci://local/greet", guest), nil)
	rw := resolvedTestWorkspace(t)

	var out bytes.Buffer
	if err := pluginInstallRun(context.Background(), rw, "oci://local/greet", false, true, false, true, strings.NewReader(""), &out); err != nil {
		t.Fatalf("install multi-command guest: %v", err)
	}

	// One CLI plugin backing two commands is pinned as a single {uri, digest}
	// entry; both command names come from the one cached descriptor.
	refs := reResolveCLI(t, rw)
	if len(refs) != 1 || refs[0].URI != "oci://local/greet" {
		t.Fatalf("one plugin should be pinned as one entry sharing the URI; got %+v", refs)
	}
	cached, ok := readDescribeCache(refs[0].Digest)
	if !ok || len(cached.Commands) != 2 {
		t.Fatalf("both commands must come from one cached descriptor; ok=%v cached=%+v", ok, cached)
	}
	byName := map[string]string{}
	for _, c := range cached.Commands {
		byName[c.Name] = c.Description
	}
	if byName["greet-a"] != "Greet in style A" || byName["greet-b"] != "Greet in style B" {
		t.Fatalf("each command keeps its own short help: %+v", byName)
	}

	// Run each command against the real guest and assert it routed on the invoked
	// command name to the correct branch's output.
	resolve := func(string) (pluginpath.ResolvedPlugin, error) {
		return pluginpath.ResolvedPlugin{Bytes: guest}, nil
	}
	run := func(command string) string {
		var stdout bytes.Buffer
		r := cliplugin.Runner{Resolve: resolve, Stdout: &stdout, Stderr: io.Discard}
		code, _, runErr := r.Run(context.Background(), "oci://local/greet", command, nil)
		if runErr != nil {
			t.Fatalf("run %q: %v", command, runErr)
		}
		if code != 0 {
			t.Fatalf("run %q exit = %d; want 0", command, code)
		}
		return stdout.String()
	}

	if got, want := run("greet-a"), "hello from A\n"; got != want {
		t.Fatalf("greet-a routed to the wrong branch: got %q, want %q", got, want)
	}
	if got, want := run("greet-b"), "hello from B\n"; got != want {
		t.Fatalf("greet-b routed to the wrong branch: got %q, want %q", got, want)
	}
}
