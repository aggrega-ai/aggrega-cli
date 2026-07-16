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

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"oras.land/oras-go/v2/content/memory"
)

// stampCommentedEntity is a hand-authored Plugin entity carrying comments, blank
// lines, and a runtime block with a maxMemoryPages field and no hostFunctions —
// the on-disk shape an author commits before recording the machine-owned set.
const stampCommentedEntity = `# SPDX-License-Identifier: Apache-2.0
apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: frp/patch # the plugin's bare identity
  version: "1.0.0"
spec:
  plugin: "oci://hub.aggrega.ai/plugin/frp/patch"
  pluginType: frp_step
  abiVersion: v1alpha1

  # runtime carries the execution limits an operator tunes.
  runtime:
    maxMemoryPages: 16 # a generous cap for this guest
`

// TestSpliceHostFunctions_PreservesCommentsAndBlanks proves the splice is
// byte-faithful: recording the derived set adds only the hostFunctions block
// under spec.runtime, leaving every comment, blank line, and other field
// untouched.
func TestSpliceHostFunctions_PreservesCommentsAndBlanks(t *testing.T) {
	t.Parallel()
	out, changed, err := spliceHostFunctions([]byte(stampCommentedEntity), []string{"aggrega_emit_warning", "aggrega_log"})
	if err != nil {
		t.Fatalf("spliceHostFunctions: %v", err)
	}
	if !changed {
		t.Fatal("recording a new set should report a change")
	}
	got := string(out)

	// Every comment and blank-line-bearing line of the original survives verbatim.
	for _, line := range []string{
		"# SPDX-License-Identifier: Apache-2.0",
		"  name: frp/patch # the plugin's bare identity",
		"  # runtime carries the execution limits an operator tunes.",
		"    maxMemoryPages: 16 # a generous cap for this guest",
	} {
		if !strings.Contains(got, line) {
			t.Errorf("splice dropped or mangled %q; result:\n%s", line, got)
		}
	}
	// The blank line before the runtime comment survives.
	if !strings.Contains(got, "abiVersion: v1alpha1\n\n  # runtime") {
		t.Errorf("splice did not preserve the blank line before the runtime block; result:\n%s", got)
	}
	// The derived set is recorded as a nested block list under runtime.
	if !strings.Contains(got, "    hostFunctions:\n      - aggrega_emit_warning\n      - aggrega_log") {
		t.Errorf("hostFunctions block not recorded as expected; result:\n%s", got)
	}

	// The bytes re-parse into the typed machine-owned field.
	var wp types.Plugin
	if err := yaml.Unmarshal(out, &wp); err != nil {
		t.Fatalf("re-parse recorded entity: %v", err)
	}
	if wp.Spec.Runtime == nil || len(wp.Spec.Runtime.HostFunctions) != 2 {
		t.Fatalf("recorded hostFunctions = %+v; want the canonical two-element set", wp.Spec.Runtime)
	}
	if wp.Spec.Runtime.MaxMemoryPages != 16 {
		t.Errorf("maxMemoryPages lost on splice; got %d", wp.Spec.Runtime.MaxMemoryPages)
	}
}

// TestSpliceHostFunctions_CreatesRuntimeWhenAbsent proves the splice creates the
// spec.runtime block when the file has none, so the machine-owned record always
// lands, while preserving the surrounding bytes.
func TestSpliceHostFunctions_CreatesRuntimeWhenAbsent(t *testing.T) {
	t.Parallel()
	const noRuntime = `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: frp/patch
  version: "1.0.0"
spec:
  plugin: "oci://hub.aggrega.ai/plugin/frp/patch" # bare identity
  pluginType: frp_step
  abiVersion: v1alpha1
`
	out, changed, err := spliceHostFunctions([]byte(noRuntime), []string{"aggrega_log"})
	if err != nil {
		t.Fatalf("spliceHostFunctions: %v", err)
	}
	if !changed {
		t.Fatal("creating the runtime block should report a change")
	}
	got := string(out)
	if !strings.Contains(got, `  plugin: "oci://hub.aggrega.ai/plugin/frp/patch" # bare identity`) {
		t.Errorf("comment on an existing field was lost; result:\n%s", got)
	}
	if !strings.Contains(got, "  runtime:\n    hostFunctions:\n      - aggrega_log") {
		t.Errorf("runtime block not created as expected; result:\n%s", got)
	}
	var wp types.Plugin
	if err := yaml.Unmarshal(out, &wp); err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if wp.Spec.Runtime == nil || len(wp.Spec.Runtime.HostFunctions) != 1 || wp.Spec.Runtime.HostFunctions[0] != "aggrega_log" {
		t.Fatalf("recorded hostFunctions = %+v; want [aggrega_log]", wp.Spec.Runtime)
	}
}

// TestSpliceHostFunctions_EmptySetExplicit proves a plugin importing no custom
// host functions records an explicit empty list, so the machine-owned field is
// present rather than absent.
func TestSpliceHostFunctions_EmptySetExplicit(t *testing.T) {
	t.Parallel()
	out, changed, err := spliceHostFunctions([]byte(stampCommentedEntity), nil)
	if err != nil {
		t.Fatalf("spliceHostFunctions: %v", err)
	}
	if !changed {
		t.Fatal("recording an explicit empty set into a file without one should change it")
	}
	if !strings.Contains(string(out), "hostFunctions: []") {
		t.Errorf("an empty derived set should record an explicit empty list; result:\n%s", out)
	}
}

// TestSpliceHostFunctions_Idempotent proves the splice is a byte-identical no-op
// when the file already records the derived set, so re-recording never churns the
// file.
func TestSpliceHostFunctions_Idempotent(t *testing.T) {
	t.Parallel()
	derived := []string{"aggrega_emit_warning", "aggrega_log"}
	first, changed, err := spliceHostFunctions([]byte(stampCommentedEntity), derived)
	if err != nil {
		t.Fatalf("first splice: %v", err)
	}
	if !changed {
		t.Fatal("first splice should change the file")
	}
	second, changed, err := spliceHostFunctions(first, derived)
	if err != nil {
		t.Fatalf("second splice: %v", err)
	}
	if changed {
		t.Error("re-recording the same set should report no change")
	}
	if !bytes.Equal(first, second) {
		t.Errorf("re-recording the same set must be byte-identical;\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	// A set-equal record in a different order is also a no-op.
	_, changed, err = spliceHostFunctions(first, []string{"aggrega_log", "aggrega_emit_warning"})
	if err != nil {
		t.Fatalf("reordered splice: %v", err)
	}
	if changed {
		t.Error("a set-equal record in a different order should report no change")
	}
}

// cliStampEntity is a CLI Plugin entity carrying neither the host-function record
// nor the command mirror, so a stamp exercises writing both machine-owned
// records: the cli-multi-guest imports one custom host function and describes two
// commands.
const cliStampEntity = `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: cli/greet
  version: "1.0.0"
spec:
  pluginType: cli
  abiVersion: v1alpha1
`

// TestStampPluginFile_WritesHostFunctionsAndCommands proves the file-level command
// reads a real CLI .wasm, derives its host-function imports AND reads its
// describe() command mirror, records both into the entity file, then reports the
// no-op on a second run. The cli-multi-guest imports one custom host function and
// describes the commands greet-a and greet-b, so both records are non-empty.
func TestStampPluginFile_WritesHostFunctionsAndCommands(t *testing.T) {
	guest := buildInstallGuest(t, "cli-multi-guest")
	root := t.TempDir()
	pluginPath := filepath.Join(root, "greet.wasm")
	if err := os.WriteFile(pluginPath, guest, 0o644); err != nil {
		t.Fatalf("write guest: %v", err)
	}
	entityPath := filepath.Join(root, "greet.yaml")
	if err := os.WriteFile(entityPath, []byte(cliStampEntity), 0o644); err != nil {
		t.Fatalf("write entity: %v", err)
	}

	var out bytes.Buffer
	if err := stampPluginFile(&out, entityPath, pluginPath); err != nil {
		t.Fatalf("stampPluginFile: %v", err)
	}
	if !strings.Contains(out.String(), "Recorded host functions") || !strings.Contains(out.String(), "greet-a") {
		t.Errorf("first run should report recording host functions and commands; got %q", out.String())
	}
	after, err := os.ReadFile(entityPath)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var wp types.Plugin
	if err := yaml.Unmarshal(after, &wp); err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if wp.Spec.Runtime == nil || !canonicalHostFunctionsEqual(wp.Spec.Runtime.HostFunctions, []string{"aggrega_cli_write_stdout"}) {
		t.Fatalf("recorded host functions %+v; want [aggrega_cli_write_stdout]", wp.Spec.Runtime)
	}
	// The command mirror is recorded in describe()-declared order with each
	// command's short help.
	if len(wp.Spec.Commands) != 2 ||
		wp.Spec.Commands[0].Name != "greet-a" || wp.Spec.Commands[0].Description != "Greet in style A" ||
		wp.Spec.Commands[1].Name != "greet-b" || wp.Spec.Commands[1].Description != "Greet in style B" {
		t.Fatalf("recorded commands %+v; want greet-a/greet-b with their short help in order", wp.Spec.Commands)
	}

	// Second run is an idempotent no-op leaving the file byte-identical.
	var out2 bytes.Buffer
	if err := stampPluginFile(&out2, entityPath, pluginPath); err != nil {
		t.Fatalf("second stampPluginFile: %v", err)
	}
	if !strings.Contains(out2.String(), "Already current") {
		t.Errorf("second run should report a no-op; got %q", out2.String())
	}
	again, err := os.ReadFile(entityPath)
	if err != nil {
		t.Fatalf("read back again: %v", err)
	}
	if !bytes.Equal(after, again) {
		t.Error("a no-op stamp must leave the file byte-identical")
	}
}

// pushVerifyEntity is an agreeing frp-step-go Plugin entity (its authored exports
// match the fixture's describe()) with the given trailing runtime block, so a
// push exercises the host-function verify — the fixture imports no custom host
// functions, so an empty or absent record is in sync and a non-empty record is
// out of sync.
const pushVerifyEntity = `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: frp/patch
  version: "1.0.0"
spec:
  pluginType: frp_step
  abiVersion: v1alpha1
  capabilities:
    - helm
  exports:
    - name: step
      operates: patch
      scope: own
`

// TestPushPluginHostFunctionsOutOfSyncHalts proves publish is verify-read-only:
// an entity whose committed hostFunctions record does not match the .wasm HALTS
// before any bytes land, and the error names the entity file and the re-record
// command.
func TestPushPluginHostFunctionsOutOfSyncHalts(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")
	root := t.TempDir()
	t.Chdir(root)
	entityPath := filepath.Join(root, "frp-step-patch.yaml")
	// The fixture imports no custom host functions; a committed [aggrega_log] is
	// therefore out of sync with the binary.
	writeEntityFixture(t, entityPath, pushVerifyEntity+"  runtime:\n    hostFunctions:\n      - aggrega_log\n")

	store := memory.New()
	err := pushRun(pushConfig{
		EntityPath: entityPath,
		PluginPath: pluginPath,
		Registry:   "hub.aggrega.ai",
		PushTarget: store,
	})
	if err == nil {
		t.Fatal("an out-of-sync host-function record should halt the publish; got nil")
	}
	for _, want := range []string{"out of sync", entityPath, "aggrega plugin sync"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("halt error should mention %q; got %v", want, err)
		}
	}
	// No bytes landed: the version tag does not exist.
	if _, exists, _ := oci.Resolve(context.Background(), store, "1.0.0"); exists {
		t.Error("no version tag should exist after a halted publish")
	}
}

// TestPushPluginHostFunctionsInSyncProceeds proves publish proceeds when the
// committed record matches the .wasm — an explicit empty list for a binary that
// imports no custom host functions — and publishes the on-disk bytes verbatim.
func TestPushPluginHostFunctionsInSyncProceeds(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")
	root := t.TempDir()
	t.Chdir(root)
	entityPath := filepath.Join(root, "frp-step-patch.yaml")
	writeEntityFixture(t, entityPath, pushVerifyEntity+"  runtime:\n    hostFunctions: []\n")

	store := memory.New()
	if err := pushRun(pushConfig{
		EntityPath: entityPath,
		PluginPath: pluginPath,
		Registry:   "hub.aggrega.ai",
		PushTarget: store,
	}); err != nil {
		t.Fatalf("an in-sync explicit-empty record should publish cleanly; got %v", err)
	}
}
