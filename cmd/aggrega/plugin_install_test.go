// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// withInstallSeams points the install fetch and describe seams at fakes for the
// duration of the test, so the install flow runs without a registry or a guest.
func withInstallSeams(t *testing.T, fetch func(context.Context, *workspaceconfig.ResolvedWorkspace, string) (resolvedInstallable, error), describe func(context.Context, resolvedInstallable) (wasm.PluginDescriptor, error)) {
	t.Helper()
	prevFetch := installPluginFetch
	prevDescribe := describeInstallable
	if fetch != nil {
		installPluginFetch = fetch
	}
	if describe != nil {
		describeInstallable = describe
	}
	t.Cleanup(func() {
		installPluginFetch = prevFetch
		describeInstallable = prevDescribe
	})
}

// withTerminal overrides the interactive-terminal seam for the duration of the
// test, so the confirmation path is exercised deterministically.
func withTerminal(t *testing.T, v bool) {
	t.Helper()
	prev := isTerminal
	isTerminal = func() bool { return v }
	t.Cleanup(func() { isTerminal = prev })
}

// fixedFetch returns an install fetch seam that hands the flow the given bytes
// and URI for any reference.
func fixedFetch(uri string, b []byte) func(context.Context, *workspaceconfig.ResolvedWorkspace, string) (resolvedInstallable, error) {
	return func(context.Context, *workspaceconfig.ResolvedWorkspace, string) (resolvedInstallable, error) {
		return resolvedInstallable{URI: uri, Bytes: b}, nil
	}
}

// fixedDescribe returns a describe seam that hands the flow a fixed descriptor.
func fixedDescribe(desc wasm.PluginDescriptor) func(context.Context, resolvedInstallable) (wasm.PluginDescriptor, error) {
	return func(context.Context, resolvedInstallable) (wasm.PluginDescriptor, error) {
		return desc, nil
	}
}

// cliDesc builds a CLI descriptor contributing one command with a short help
// line. An empty command name builds a descriptor with no commands, so the
// no-commands install guard can be exercised.
func cliDesc(command, description string) wasm.PluginDescriptor {
	d := wasm.PluginDescriptor{
		AbiVersion: wasm.ABIVersion,
		PluginType: string(wasm.PluginTypeCLI),
		Exports:    []wasm.PluginExport{{Name: wasm.CLIRunExport.Name()}},
	}
	if command != "" {
		d.Commands = []wasm.PluginCLICommand{{Name: command, Description: description}}
	}
	return d
}

// TestConfirmInstallCapabilities covers the capability-confirmation gate: the
// no-additional-capabilities message, the skip flags, the non-interactive
// refusal, and the interactive yes/no branches.
func TestConfirmInstallCapabilities(t *testing.T) {
	// Not parallel: mutates the process-global isTerminal seam other tests read.
	t.Run("empty caps message and skip via --yes", func(t *testing.T) {
		var out bytes.Buffer
		if err := confirmInstallCapabilities(&out, strings.NewReader(""), "moo", nil, false, true); err != nil {
			t.Fatalf("--yes should consent: %v", err)
		}
		if !strings.Contains(out.String(), "no additional capabilities beyond the baseline") {
			t.Fatalf("missing no-additional-capabilities message:\n%s", out.String())
		}
	})

	t.Run("skip via --accept-capabilities", func(t *testing.T) {
		var out bytes.Buffer
		if err := confirmInstallCapabilities(&out, strings.NewReader(""), "moo", nil, true, false); err != nil {
			t.Fatalf("--accept-capabilities should consent: %v", err)
		}
	})

	t.Run("non-interactive without a flag refuses", func(t *testing.T) {
		withTerminal(t, false)
		var out bytes.Buffer
		err := confirmInstallCapabilities(&out, strings.NewReader(""), "moo", nil, false, false)
		if err == nil || !strings.Contains(err.Error(), "non-interactive") {
			t.Fatalf("non-tty without a flag should refuse; got %v", err)
		}
	})

	t.Run("interactive yes consents", func(t *testing.T) {
		withTerminal(t, true)
		var out bytes.Buffer
		if err := confirmInstallCapabilities(&out, strings.NewReader("y\n"), "moo", nil, false, false); err != nil {
			t.Fatalf("interactive yes should consent: %v", err)
		}
	})

	t.Run("interactive no aborts", func(t *testing.T) {
		withTerminal(t, true)
		var out bytes.Buffer
		err := confirmInstallCapabilities(&out, strings.NewReader("n\n"), "moo", nil, false, false)
		if err == nil || !strings.Contains(err.Error(), "aborted") {
			t.Fatalf("interactive no should abort; got %v", err)
		}
	})

	t.Run("beyond-baseline capabilities are listed", func(t *testing.T) {
		var out bytes.Buffer
		_ = confirmInstallCapabilities(&out, strings.NewReader(""), "moo", []string{"network"}, true, false)
		if !strings.Contains(out.String(), "- network") {
			t.Fatalf("declared capability not listed:\n%s", out.String())
		}
	})
}

// reResolveCLI re-resolves the workspace rooted at rw and returns its composed
// plugins.cli list, so a test reads back exactly what an install wrote.
func reResolveCLI(t *testing.T, rw *workspaceconfig.ResolvedWorkspace) []workspaceconfig.CLIPluginRef {
	t.Helper()
	rw2, err := workspaceconfig.Resolve(rw.Root(), "")
	if err != nil {
		t.Fatalf("re-resolve workspace: %v", err)
	}
	return rw2.Config().Plugins.CLI
}

// TestPluginInstall_Success proves a CLI plugin naming its command installs: the
// plugins.cli list gains the {uri, digest} pin, and the describe metadata is
// cached under that digest.
func TestPluginInstall_Success(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	withCLIRegistrations(t, nil)
	withInstallSeams(t, fixedFetch("oci://local/moo", []byte("guest-bytes")), fixedDescribe(cliDesc("moo", "Print a greeting")))
	rw := resolvedTestWorkspace(t)

	var out bytes.Buffer
	if err := pluginInstallRun(context.Background(), rw, "oci://local/moo", false, true, false, true, strings.NewReader(""), &out); err != nil {
		t.Fatalf("install: %v", err)
	}
	if !strings.Contains(out.String(), "no additional capabilities") {
		t.Fatalf("capability prompt not shown:\n%s", out.String())
	}

	wantDigest := digest.ContentDigest([]byte("guest-bytes"))
	refs := reResolveCLI(t, rw)
	if len(refs) != 1 {
		t.Fatalf("plugins.cli should carry exactly one pin; got %+v", refs)
	}
	if refs[0].URI != "oci://local/moo" || refs[0].Digest != wantDigest {
		t.Fatalf("unexpected cli pin: %+v (want digest %s)", refs[0], wantDigest)
	}

	cached, ok := readDescribeCache(wantDigest)
	if !ok || len(cached.Commands) != 1 || cached.Commands[0].Name != "moo" || cached.Commands[0].Description != "Print a greeting" {
		t.Fatalf("describe metadata not cached: ok=%v cached=%+v", ok, cached)
	}
}

// TestPluginInstall_CollisionAndTypeGuards proves install refuses — without
// writing the index — a plugin that is not a CLI plugin, names no command,
// shadows a built-in command, or shadows a reserved command name.
func TestPluginInstall_CollisionAndTypeGuards(t *testing.T) {
	cases := []struct {
		name    string
		desc    wasm.PluginDescriptor
		wantErr string
	}{
		{"not a CLI plugin", wasm.PluginDescriptor{AbiVersion: wasm.ABIVersion, PluginType: string(wasm.PluginTypeProvider)}, "not a CLI plugin"},
		{"no commands", cliDesc("", "help"), "declares no commands"},
		{"built-in collision", cliDesc("build", "help"), "built-in command"},
		{"reserved collision", cliDesc("deploy", "help"), "reserved command name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			withCLIRegistrations(t, nil)
			withInstallSeams(t, fixedFetch("oci://local/x", []byte("b")), fixedDescribe(tc.desc))
			rw := resolvedTestWorkspace(t)

			var out bytes.Buffer
			err := pluginInstallRun(context.Background(), rw, "oci://local/x", false, true, true, true, strings.NewReader(""), &out)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q; got %v", tc.wantErr, err)
			}
			if refs := reResolveCLI(t, rw); len(refs) != 0 {
				t.Fatalf("plugins.cli must not be written on a refused install: %+v", refs)
			}
		})
	}
}

// TestPluginInstall_NonInteractiveRefuse proves an install on a non-interactive
// stdin without a skip flag refuses rather than hanging, and writes nothing.
func TestPluginInstall_NonInteractiveRefuse(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	withCLIRegistrations(t, nil)
	withTerminal(t, false)
	withInstallSeams(t, fixedFetch("oci://local/moo", []byte("b")), fixedDescribe(cliDesc("moo", "help")))
	rw := resolvedTestWorkspace(t)

	var out bytes.Buffer
	err := pluginInstallRun(context.Background(), rw, "oci://local/moo", false, true, false, false, strings.NewReader(""), &out)
	if err == nil || !strings.Contains(err.Error(), "non-interactive") {
		t.Fatalf("non-tty install without a flag should refuse; got %v", err)
	}
	if refs := reResolveCLI(t, rw); len(refs) != 0 {
		t.Fatalf("plugins.cli must not be written on a refused install: %+v", refs)
	}
}

// TestPluginInstall_RealGuestDescribe proves the command name flows from a real
// guest's describe() output through install into the index: it builds a CLI
// guest that declares command "moo", installs it through the real describe gate,
// and asserts the index and describe cache carry the guest's self-reported
// command and help.
func TestPluginInstall_RealGuestDescribe(t *testing.T) {
	// Not parallel: sets the process environment and swaps the process-global registration and install seams.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	withCLIRegistrations(t, nil)
	guest := buildInstallGuest(t, "cli-install-guest")
	// Only the fetch is faked (no registry); the describe gate is the real one.
	withInstallSeams(t, fixedFetch("oci://local/moo", guest), nil)
	rw := resolvedTestWorkspace(t)

	var out bytes.Buffer
	if err := pluginInstallRun(context.Background(), rw, "oci://local/moo", false, true, false, true, strings.NewReader(""), &out); err != nil {
		t.Fatalf("install real guest: %v", err)
	}
	wantDigest := digest.ContentDigest(guest)
	refs := reResolveCLI(t, rw)
	if len(refs) != 1 || refs[0].URI != "oci://local/moo" || refs[0].Digest != wantDigest {
		t.Fatalf("plugins.cli missing the real-guest pin: %+v", refs)
	}
	cached, ok := readDescribeCache(wantDigest)
	if !ok || len(cached.Commands) != 1 || cached.Commands[0].Name != "moo" {
		t.Fatalf("real-guest describe metadata not cached: ok=%v cached=%+v", ok, cached)
	}
	if cached.Commands[0].Description != "Print a friendly greeting" {
		t.Fatalf("help should come from the guest describe() command description; got %q", cached.Commands[0].Description)
	}
}

// buildInstallGuest builds a fixture guest under testdata/<name> to a .wasm
// artifact and returns its bytes. Each guest is a standalone module (its own
// go.mod), so the build runs with GOWORK=off. The Go toolchain is the test
// toolchain itself and the wasip1 target ships with it, so a build failure is a
// genuine failure, never a toolchain skip.
func buildInstallGuest(t *testing.T, name string) []byte {
	t.Helper()
	srcDir := filepath.Join("testdata", name)
	if _, err := os.Stat(filepath.Join(srcDir, "main.go")); err != nil {
		t.Fatalf("fixture source missing at %s: %v", srcDir, err)
	}
	outPath := filepath.Join(t.TempDir(), name+".wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", outPath, "./")
	cmd.Dir = srcDir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOOS=wasip1", "GOARCH=wasm")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s guest: %v\noutput:\n%s", name, err, output)
	}
	b, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read built %s guest: %v", name, err)
	}
	return b
}
