// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// cliCommentedEntity is a hand-authored CLI Plugin entity carrying a comment and
// a blank line, so the command splice can be shown to be byte-faithful.
const cliCommentedEntity = `# SPDX-License-Identifier: Apache-2.0
apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: greet # the plugin's bare identity
  pluginType: cli
  version: "1.0.0"
spec:
  abiVersion: v1alpha1
`

// TestSpliceCommands_PreservesCommentsAndBlanks proves the command splice is
// byte-faithful: recording the mirror adds only the spec.commands block, in
// describe()-declared order with each command's short help, leaving every comment
// and other field untouched.
func TestSpliceCommands_PreservesCommentsAndBlanks(t *testing.T) {
	t.Parallel()
	out, changed, err := spliceCommands([]byte(cliCommentedEntity), []wasm.PluginCLICommand{
		{Name: "greet-a", Description: "Greet in style A"},
		{Name: "greet-b", Description: "Greet in style B"},
	})
	if err != nil {
		t.Fatalf("spliceCommands: %v", err)
	}
	if !changed {
		t.Fatal("recording a new mirror should report a change")
	}
	got := string(out)
	for _, line := range []string{
		"# SPDX-License-Identifier: Apache-2.0",
		"  name: greet # the plugin's bare identity",
	} {
		if !strings.Contains(got, line) {
			t.Errorf("splice dropped or mangled %q; result:\n%s", line, got)
		}
	}
	if !strings.Contains(got, "  commands:\n    - name: greet-a\n      description: Greet in style A\n    - name: greet-b\n      description: Greet in style B") {
		t.Errorf("commands block not recorded in declared order; result:\n%s", got)
	}

	var wp types.Plugin
	if err := yaml.Unmarshal(out, &wp); err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if len(wp.Spec.Commands) != 2 || wp.Spec.Commands[0].Name != "greet-a" || wp.Spec.Commands[1].Name != "greet-b" {
		t.Fatalf("recorded commands = %+v; want greet-a/greet-b in order", wp.Spec.Commands)
	}
}

// TestSpliceCommands_CarriesFlags proves the command splice records each command's
// flags in describe()-declared order, that the recorded entity round-trips back to
// the same mirror the load gate compares (stamp write and entity read agree on the
// flag record), and that re-recording the same flag mirror is a byte-identical
// no-op.
func TestSpliceCommands_CarriesFlags(t *testing.T) {
	t.Parallel()
	described := []wasm.PluginCLICommand{
		{
			Name:        "greet-a",
			Description: "Greet in style A",
			Flags: []wasm.PluginCommandFlag{
				{Name: "loud", Shorthand: "l", Description: "shout the greeting", Type: "bool", Default: "false"},
				{Name: "name", Description: "who to greet", Type: "string"},
			},
		},
		{Name: "greet-b", Description: "Greet in style B"},
	}
	out, changed, err := spliceCommands([]byte(cliCommentedEntity), described)
	if err != nil {
		t.Fatalf("spliceCommands: %v", err)
	}
	if !changed {
		t.Fatal("recording a new mirror should report a change")
	}
	got := string(out)
	if !strings.Contains(got, "      flags:\n        - name: loud\n          shorthand: l\n          description: shout the greeting\n          type: bool\n          default: \"false\"") {
		t.Errorf("flags block not recorded in field-declaration order; result:\n%s", got)
	}

	// The recorded entity must read back to the exact mirror the load gate compares,
	// so stamp write and entity read agree on the flag record.
	var wp types.Plugin
	if err := yaml.Unmarshal(out, &wp); err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if !wasm.CommandMirrorsEqual(entityCommandMirror(wp), described) {
		t.Errorf("round-tripped mirror diverges from the described commands;\n got %s\nwant %s",
			wasm.FormatCommandMirror(entityCommandMirror(wp)), wasm.FormatCommandMirror(described))
	}

	second, changed, err := spliceCommands(out, described)
	if err != nil {
		t.Fatalf("second spliceCommands: %v", err)
	}
	if changed {
		t.Error("re-recording the same flag mirror should report no change")
	}
	if !bytes.Equal(out, second) {
		t.Error("a no-op command splice must leave the file byte-identical")
	}
}

// TestSpliceCommands_EmptyExplicitAndIdempotent proves a plugin describing no
// commands records an explicit empty list, and re-recording the same mirror is a
// byte-identical no-op.
func TestSpliceCommands_EmptyExplicitAndIdempotent(t *testing.T) {
	t.Parallel()
	out, changed, err := spliceCommands([]byte(cliCommentedEntity), nil)
	if err != nil {
		t.Fatalf("spliceCommands: %v", err)
	}
	if !changed {
		t.Fatal("recording an explicit empty list into a file without one should change it")
	}
	if !strings.Contains(string(out), "commands: []") {
		t.Errorf("an empty mirror should record an explicit empty list; result:\n%s", out)
	}
	second, changed, err := spliceCommands(out, nil)
	if err != nil {
		t.Fatalf("second spliceCommands: %v", err)
	}
	if changed {
		t.Error("re-recording the same empty mirror should report no change")
	}
	if !bytes.Equal(out, second) {
		t.Error("a no-op command splice must leave the file byte-identical")
	}
}

// TestPushPluginCommandsOutOfSyncHalts proves publish is verify-read-only over the
// command mirror: an entity whose recorded spec.commands does not match the .wasm
// describe() HALTS before any bytes land, and the error names the entity file and
// the re-record command.
func TestPushPluginCommandsOutOfSyncHalts(t *testing.T) {
	guest := buildInstallGuest(t, "cli-multi-guest")
	root := t.TempDir()
	t.Chdir(root)
	pluginPath := filepath.Join(root, "greet.wasm")
	if err := os.WriteFile(pluginPath, guest, 0o644); err != nil {
		t.Fatalf("write guest: %v", err)
	}
	entityPath := filepath.Join(root, "greet.yaml")
	// The guest describes greet-a AND greet-b; a mirror recording only greet-a is
	// out of sync. The host-function record is in sync so the halt is on commands.
	writeEntityFixture(t, entityPath, `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: greet
  pluginType: cli
  version: "1.0.0"
spec:
  abiVersion: v1alpha1
  exports:
    - name: cli_run
  commands:
    - name: greet-a
      description: Greet in style A
  runtime:
    hostFunctions:
      - aggrega_cli_write_stdout
`)

	store := memory.New()
	err := pushRun(pushConfig{
		EntityPath: entityPath,
		PluginPath: pluginPath,
		Registry:   "hub.aggrega.ai",
		PushTarget: store,
	})
	if err == nil {
		t.Fatal("an out-of-sync command mirror should halt the publish; got nil")
	}
	for _, want := range []string{"out of sync", entityPath, "aggrega plugin sync"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("halt error should mention %q; got %v", want, err)
		}
	}
	if _, exists, _ := oci.Resolve(context.Background(), store, "1.0.0"); exists {
		t.Error("no version tag should exist after a halted publish")
	}
}

// TestPushPluginUnderDeclarationAdvisoryExitUnchanged proves the under-declaration
// advisory is exactly that: an entity that omits an export the binary describes
// (its cli_run) publishes cleanly, printing a notice, with no error — under-
// declaration never changes the outcome. The command mirror and host-function
// record are stamped first so the publish reaches the advisory.
func TestPushPluginUnderDeclarationAdvisoryExitUnchanged(t *testing.T) {
	guest := buildInstallGuest(t, "cli-multi-guest")
	root := t.TempDir()
	t.Chdir(root)
	pluginPath := filepath.Join(root, "greet.wasm")
	if err := os.WriteFile(pluginPath, guest, 0o644); err != nil {
		t.Fatalf("write guest: %v", err)
	}
	entityPath := filepath.Join(root, "greet.yaml")
	// No exports declared, so the binary's cli_run export is under-declared.
	writeEntityFixture(t, entityPath, `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: greet
  pluginType: cli
  version: "1.0.0"
spec:
  abiVersion: v1alpha1
`)
	// Stamp so the machine-owned records are in sync, leaving only the (legitimate)
	// under-declaration of the export surface for the advisory to report.
	if err := stampPluginFile(&bytes.Buffer{}, entityPath, pluginPath); err != nil {
		t.Fatalf("stamp: %v", err)
	}

	var out bytes.Buffer
	err := pushRun(pushConfig{
		EntityPath: entityPath,
		PluginPath: pluginPath,
		Registry:   "hub.aggrega.ai",
		DryRun:     true,
		Out:        &out,
	})
	if err != nil {
		t.Fatalf("under-declaration must not change the exit; got error %v", err)
	}
	if !strings.Contains(out.String(), "notice:") || !strings.Contains(out.String(), "cli_run") {
		t.Errorf("expected an under-declaration notice about cli_run; got:\n%s", out.String())
	}
}
