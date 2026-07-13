// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

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
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	guest := buildInstallGuest(t, "cli-multi-guest")

	// Only the fetch is faked (no registry); the describe gate is the real one.
	withInstallSeams(t, fixedFetch("oci://local/greet", guest), nil)

	var out bytes.Buffer
	if err := pluginInstallRun(context.Background(), nil, "oci://local/greet", false, true, strings.NewReader(""), &out); err != nil {
		t.Fatalf("install multi-command guest: %v", err)
	}

	idx, err := readInstalledPluginIndex()
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	a, okA := idx.Plugins["greet-a"]
	b, okB := idx.Plugins["greet-b"]
	if !okA || !okB {
		t.Fatalf("both commands must register from one install; got index %+v", idx.Plugins)
	}
	if a.Usage != "Greet in style A" || b.Usage != "Greet in style B" {
		t.Fatalf("each command keeps its own short help: greet-a=%q greet-b=%q", a.Usage, b.Usage)
	}
	if a.URI != "oci://local/greet" || b.URI != "oci://local/greet" {
		t.Fatalf("both commands share the one resolved URI: greet-a=%q greet-b=%q", a.URI, b.URI)
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
