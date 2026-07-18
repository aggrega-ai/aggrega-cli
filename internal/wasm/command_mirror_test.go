// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCommandMirrorsEqual covers the mirror-equality rule the publish verify and
// the load gate share: full equality by name, short help, declared flag list, and
// order, with a nil slice and an empty slice equal. The flag cases prove a
// divergence in a flag alone — the command name and short help unchanged — is a
// mirror mismatch, so the machine record must reflect the binary's flags exactly.
func TestCommandMirrorsEqual(t *testing.T) {
	t.Parallel()
	base := []PluginCLICommand{{Name: "a", Description: "A"}, {Name: "b", Description: "B"}}
	withFlag := []PluginCLICommand{
		{Name: "a", Description: "A", Flags: []PluginCommandFlag{{Name: "verbose", Shorthand: "v", Type: "bool"}}},
		{Name: "b", Description: "B"},
	}
	cases := []struct {
		name string
		x, y []PluginCLICommand
		want bool
	}{
		{"identical", base, []PluginCLICommand{{Name: "a", Description: "A"}, {Name: "b", Description: "B"}}, true},
		{"nil vs empty", nil, []PluginCLICommand{}, true},
		{"description drift", base, []PluginCLICommand{{Name: "a", Description: "A"}, {Name: "b", Description: "different"}}, false},
		{"name drift", base, []PluginCLICommand{{Name: "a", Description: "A"}, {Name: "c", Description: "B"}}, false},
		{"order drift", base, []PluginCLICommand{{Name: "b", Description: "B"}, {Name: "a", Description: "A"}}, false},
		{"length drift", base, []PluginCLICommand{{Name: "a", Description: "A"}}, false},
		{"flags identical", withFlag, []PluginCLICommand{
			{Name: "a", Description: "A", Flags: []PluginCommandFlag{{Name: "verbose", Shorthand: "v", Type: "bool"}}},
			{Name: "b", Description: "B"},
		}, true},
		// A command whose name and short help match but whose flag list differs
		// only in one flag is a mismatch — the flag is part of the machine record.
		{"flag presence drift", withFlag, base, false},
		{"flag shorthand drift", withFlag, []PluginCLICommand{
			{Name: "a", Description: "A", Flags: []PluginCommandFlag{{Name: "verbose", Shorthand: "w", Type: "bool"}}},
			{Name: "b", Description: "B"},
		}, false},
		{"flag default drift", withFlag, []PluginCLICommand{
			{Name: "a", Description: "A", Flags: []PluginCommandFlag{{Name: "verbose", Shorthand: "v", Type: "bool", Default: "true"}}},
			{Name: "b", Description: "B"},
		}, false},
		{"flag order drift", []PluginCLICommand{
			{Name: "a", Description: "A", Flags: []PluginCommandFlag{{Name: "one"}, {Name: "two"}}},
		}, []PluginCLICommand{
			{Name: "a", Description: "A", Flags: []PluginCommandFlag{{Name: "two"}, {Name: "one"}}},
		}, false},
		{"flag nil vs empty", []PluginCLICommand{{Name: "a", Description: "A", Flags: nil}},
			[]PluginCLICommand{{Name: "a", Description: "A", Flags: []PluginCommandFlag{}}}, true},
	}
	for _, tc := range cases {
		if got := CommandMirrorsEqual(tc.x, tc.y); got != tc.want {
			t.Errorf("%s: CommandMirrorsEqual = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// buildCLIMultiGuest builds the cli-multi-guest fixture — a CLI plugin whose
// describe() declares the commands greet-a and greet-b and imports the streaming
// host function — and returns its bytes. The fixture lives with the cmd tests; it
// is built here from its source so the load gate's command check is exercised
// against a real guest. The Go toolchain is the test toolchain and the wasip1
// target ships with it, so a build failure is a genuine failure, never a skip.
func buildCLIMultiGuest(t *testing.T) []byte {
	t.Helper()
	srcDir := filepath.Join("..", "..", "cmd", "aggrega", "testdata", "cli-multi-guest")
	if _, err := os.Stat(filepath.Join(srcDir, "main.go")); err != nil {
		t.Fatalf("cli-multi-guest fixture source missing at %s: %v", srcDir, err)
	}
	out := filepath.Join(t.TempDir(), "cli-multi-guest.wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", out, "./")
	cmd.Dir = srcDir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOOS=wasip1", "GOARCH=wasm")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build cli-multi-guest guest: %v\noutput:\n%s", err, output)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read built cli-multi-guest guest: %v", err)
	}
	return b
}

// describedCommands is the cli-multi-guest's describe()-declared command mirror,
// in the order and with the short help the guest self-reports.
var describedCommands = []PluginCLICommand{
	{Name: "greet-a", Description: "Greet in style A"},
	{Name: "greet-b", Description: "Greet in style B"},
}

// TestDescribeAndValidate_CommandMirrorMatchPasses proves the load gate admits an
// entity-fronted CLI load whose supplied command mirror equals the plugin's
// describe() commands exactly.
func TestDescribeAndValidate_CommandMirrorMatchPasses(t *testing.T) {
	guest := buildCLIMultiGuest(t)
	ctx := context.Background()
	loader := NewCachingLoader(nil)
	spec := PluginSpec{
		Name:             "cli/greet:1.0.0",
		PluginType:       PluginTypeCLI,
		Bytes:            guest,
		Commands:         describedCommands,
		CommandsMirrored: true,
	}
	plugin, _, err := loader.LoadForDescribe(ctx, spec)
	if err != nil {
		t.Fatalf("a matching command mirror should pass the load gate; got %v", err)
	}
	_ = plugin.Close(ctx)
}

// TestDescribeAndValidate_CommandMirrorDriftHalts proves the load gate halts with
// ErrWasmCommandsMirrorMismatch when the entity-supplied mirror diverges from the
// plugin's describe() commands, and the halt names the plugin and the re-record
// command.
func TestDescribeAndValidate_CommandMirrorDriftHalts(t *testing.T) {
	guest := buildCLIMultiGuest(t)
	ctx := context.Background()
	loader := NewCachingLoader(nil)
	spec := PluginSpec{
		Name:             "cli/greet:1.0.0",
		PluginType:       PluginTypeCLI,
		Bytes:            guest,
		Commands:         []PluginCLICommand{{Name: "greet-a", Description: "Greet in style A"}},
		CommandsMirrored: true,
	}
	_, _, err := loader.LoadForDescribe(ctx, spec)
	if !errors.Is(err, ErrWasmCommandsMirrorMismatch) {
		t.Fatalf("a drifted command mirror should halt with ErrWasmCommandsMirrorMismatch; got %v", err)
	}
	for _, want := range []string{"cli/greet:1.0.0", "aggrega plugin sync"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("halt should mention %q; got %v", want, err)
		}
	}
}

// TestDescribeAndValidate_CommandMirrorFlagDriftHalts proves the load gate reads
// a command's flags into its full-equality check: a supplied mirror whose command
// names and short help match the plugin's describe() exactly but that records a
// flag the binary does not describe is a divergence, so the load halts with
// ErrWasmCommandsMirrorMismatch and the diagnostic surfaces the drifted flag.
func TestDescribeAndValidate_CommandMirrorFlagDriftHalts(t *testing.T) {
	guest := buildCLIMultiGuest(t)
	ctx := context.Background()
	loader := NewCachingLoader(nil)
	// The mirror matches the guest's described commands in every field except a
	// single extra flag on the first command — a flag-only divergence.
	mirror := []PluginCLICommand{
		{Name: "greet-a", Description: "Greet in style A", Flags: []PluginCommandFlag{{Name: "loud", Shorthand: "l", Type: "bool"}}},
		{Name: "greet-b", Description: "Greet in style B"},
	}
	spec := PluginSpec{
		Name:             "cli/greet:1.0.0",
		PluginType:       PluginTypeCLI,
		Bytes:            guest,
		Commands:         mirror,
		CommandsMirrored: true,
	}
	_, _, err := loader.LoadForDescribe(ctx, spec)
	if !errors.Is(err, ErrWasmCommandsMirrorMismatch) {
		t.Fatalf("a flag-only mirror divergence should halt with ErrWasmCommandsMirrorMismatch; got %v", err)
	}
	for _, want := range []string{"cli/greet:1.0.0", "loud", "aggrega plugin sync"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("halt should mention %q; got %v", want, err)
		}
	}
}

// TestDescribeAndValidate_BareLoadSkipsCommandCheck proves a bare .wasm load — one
// that supplies no command mirror (CommandsMirrored false) — skips the command
// check entirely, even though the guest's describe() declares commands and the
// spec's Commands field is left unset. This is the load-bearing correctness
// constraint: a bare CLI load must not fail the mirror check it never opted into.
func TestDescribeAndValidate_BareLoadSkipsCommandCheck(t *testing.T) {
	guest := buildCLIMultiGuest(t)
	ctx := context.Background()
	loader := NewCachingLoader(nil)
	// No mirror supplied; also set a deliberately-wrong Commands to prove it is
	// ignored when CommandsMirrored is false.
	spec := PluginSpec{
		Name:             "cli/greet:1.0.0",
		PluginType:       PluginTypeCLI,
		Bytes:            guest,
		Commands:         []PluginCLICommand{{Name: "does-not-match", Description: "x"}},
		CommandsMirrored: false,
	}
	plugin, desc, err := loader.LoadForDescribe(ctx, spec)
	if err != nil {
		t.Fatalf("a bare CLI load must skip the command check; got %v", err)
	}
	defer func() { _ = plugin.Close(ctx) }()
	// The guest genuinely describes commands — the check was skipped, not vacuous.
	if !CommandMirrorsEqual(desc.Commands, describedCommands) {
		t.Fatalf("fixture describe() commands = %v; want the guest's two commands", desc.Commands)
	}
}
