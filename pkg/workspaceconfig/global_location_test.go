// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig_test

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/machinedirs"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestTheGlobalFileOnWindowsIsReadAndWrittenUnderAPPDATA resolves a Windows
// machine stated as a frozen environment and holds both sides of the global
// file to APPDATA: the layer loaded beneath the cascade, and the file a global
// write goes to. The environment also carries the variables the other platforms
// read, each pointing at a file of its own, so a rule that consulted them on
// Windows would load the wrong value.
func TestTheGlobalFileOnWindowsIsReadAndWrittenUnderAPPDATA(t *testing.T) {
	t.Parallel()
	scratch := t.TempDir()
	appData := filepath.Join(scratch, "AppData", "Roaming")
	xdg := filepath.Join(scratch, "xdg")
	home := filepath.Join(scratch, "home")
	wantFile := filepath.Join(appData, "aggrega", "config.yaml")
	write(t, wantFile, "default_registry: windows.example\n")
	write(t, filepath.Join(xdg, "aggrega", "config.yaml"), "default_registry: xdg.example\n")
	write(t, filepath.Join(home, ".config", "aggrega", "config.yaml"), "default_registry: home.example\n")
	lookup := frozenLookup(map[string]string{
		"APPDATA":         appData,
		"LOCALAPPDATA":    filepath.Join(scratch, "AppData", "Local"),
		"USERPROFILE":     filepath.Join(scratch, "profile"),
		"XDG_CONFIG_HOME": xdg,
		"HOME":            home,
	})
	root := t.TempDir()

	rw, err := workspaceconfig.ResolveOn(machinedirs.Windows, root, "", lookup)
	if err != nil {
		t.Fatalf("ResolveOn(windows): %v", err)
	}
	if got := rw.Config().DefaultRegistry; got != "windows.example" {
		t.Errorf("DefaultRegistry = %q; want windows.example, the file under APPDATA", got)
	}
	if got, err := rw.WriteDestination(workspaceconfig.OpRegistry, false, false); err != nil || got != wantFile {
		t.Errorf("WriteDestination = %q, %v; want %q", got, err, wantFile)
	}
	if got, err := rw.GlobalConfigFile(); err != nil || got != wantFile {
		t.Errorf("GlobalConfigFile = %q, %v; want %q", got, err, wantFile)
	}
	if got, err := rw.MachineCacheRoot(); err != nil || got != filepath.Join(scratch, "AppData", "Local", "aggrega", "cache") {
		t.Errorf("MachineCacheRoot = %q, %v; want the cache directory under LOCALAPPDATA", got, err)
	}

	// The same machine read as Linux reads XDG_CONFIG_HOME, so the platform is
	// what decided the file above.
	linux, err := workspaceconfig.ResolveOn(machinedirs.Platform("linux"), root, "", lookup)
	if err != nil {
		t.Fatalf("ResolveOn(linux): %v", err)
	}
	if got := linux.Config().DefaultRegistry; got != "xdg.example" {
		t.Errorf("DefaultRegistry on linux = %q; want xdg.example", got)
	}
}

// TestNoGlobalLocationIsWordedForEveryPlatform holds the answer given when no
// location resolves for the global file to words a reader can act on at any
// prompt: no shell's variable syntax, and no Go package name.
func TestNoGlobalLocationIsWordedForEveryPlatform(t *testing.T) {
	t.Parallel()
	const want = "no location resolves for the global configuration file: pass --config, or set the " +
		"AGGREGA_CONFIG environment variable, or APPDATA on Windows / XDG_CONFIG_HOME or HOME elsewhere"
	for _, p := range []machinedirs.Platform{machinedirs.Windows, machinedirs.Platform("linux"), machinedirs.Darwin} {
		rw, err := workspaceconfig.ResolveWithoutGlobalOn(p, t.TempDir(), "", frozenLookup(map[string]string{}))
		if err != nil {
			t.Fatalf("ResolveWithoutGlobalOn(%s): %v", p, err)
		}
		_, err = rw.WriteDestination(workspaceconfig.OpRegistry, false, false)
		if err == nil || err.Error() != want {
			t.Errorf("%s: WriteDestination with nothing set = %v; want %q", p, err, want)
		}
		if _, err := rw.GlobalConfigFile(); err == nil || err.Error() != want {
			t.Errorf("%s: GlobalConfigFile with nothing set = %v; want %q", p, err, want)
		}
	}
	if strings.Contains(want, "$") || strings.Contains(want, "workspaceconfig") {
		t.Fatalf("the expected wording itself carries shell syntax or a package name: %q", want)
	}
}

// TestAMissingNamedGlobalFileHaltsInOneSentence holds the halt on a named
// global file that does not exist to one sentence ending with the path — no
// system call, no operating-system wording — while it still carries the
// condition for a caller matching it.
func TestAMissingNamedGlobalFileHaltsInOneSentence(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "Users", "Utente", workspaceconfig.ConfigFilename)
	_, err := workspaceconfig.Resolve(t.TempDir(), "", frozenLookup(map[string]string{
		workspaceconfig.EnvConfigOverride: missing,
	}))
	want := "the global configuration file named by --config or AGGREGA_CONFIG does not exist: " + missing
	if err == nil || err.Error() != want {
		t.Fatalf("Resolve over a missing named file = %v; want exactly %q", err, want)
	}
	var notFound *workspaceconfig.GlobalConfigNotFoundError
	if !errors.As(err, &notFound) || notFound.Path() != missing {
		t.Errorf("the halt is not a GlobalConfigNotFoundError naming %s: %#v", missing, err)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the halt does not report the file as not existing: %v", err)
	}
}
