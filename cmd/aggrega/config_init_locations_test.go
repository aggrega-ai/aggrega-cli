// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/configops"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/machinedirs"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// afterSeedingLocations returns a writing seeding's answer with the two lines
// that open it — where the configuration file is, and where the machine-wide
// caches are — taken off, and fails the test when the answer does not open with
// them. A test about what seeding says after its locations reads the answer
// through this, so the locations are asserted once here rather than skipped by
// every such test.
func afterSeedingLocations(t *testing.T, answer string) string {
	t.Helper()
	lines := strings.SplitAfterN(answer, "\n", 3)
	if len(lines) < 3 {
		t.Fatalf("a writing seeding opens its answer with two location lines; it carried:\n%s", answer)
	}
	opensWithFile := (strings.HasPrefix(lines[0], "Created the ") || strings.HasPrefix(lines[0], "Using the ")) &&
		strings.Contains(lines[0], " configuration file at ")
	if !opensWithFile || !strings.HasPrefix(lines[1], "Machine-wide caches are ") {
		t.Fatalf("a writing seeding opens its answer with where the configuration file and the "+
			"machine-wide caches are; it opened with:\n%q\n%q", lines[0], lines[1])
	}
	return lines[2]
}

// machineCase is one platform's machine, stated as a frozen environment: the
// variables that platform sets, and where its conventions place the global
// configuration file and the machine-wide caches. The two locations are spelled
// here from the platform's convention rather than asked of the product, so the
// assertions can disagree with it.
type machineCase struct {
	name       string
	platform   machinedirs.Platform
	env        map[string]string
	wantConfig string
	wantCaches string
	// elsewhere is a directory the machine has that nothing may be written to: a
	// home the platform's rule must not read.
	elsewhere string
}

// machineCases states a Windows machine (APPDATA, LOCALAPPDATA and USERPROFILE,
// and no HOME), a Linux machine and a macOS machine, each under scratch.
func machineCases(t *testing.T) []machineCase {
	t.Helper()
	win := t.TempDir()
	lin := t.TempDir()
	mac := t.TempDir()
	return []machineCase{
		{
			name:     "windows",
			platform: machinedirs.Windows,
			env: map[string]string{
				"APPDATA":      filepath.Join(win, "AppData", "Roaming"),
				"LOCALAPPDATA": filepath.Join(win, "AppData", "Local"),
				"USERPROFILE":  filepath.Join(win, "Users", "Utente"),
			},
			wantConfig: filepath.Join(win, "AppData", "Roaming", "aggrega", "config.yaml"),
			wantCaches: filepath.Join(win, "AppData", "Local", "aggrega", "cache"),
			elsewhere:  filepath.Join(win, "Users", "Utente"),
		},
		{
			name:       "linux",
			platform:   machinedirs.Platform("linux"),
			env:        map[string]string{"HOME": filepath.Join(lin, "home")},
			wantConfig: filepath.Join(lin, "home", ".config", "aggrega", "config.yaml"),
			wantCaches: filepath.Join(lin, "home", ".cache", "aggrega"),
		},
		{
			name:       "macos",
			platform:   machinedirs.Darwin,
			env:        map[string]string{"HOME": filepath.Join(mac, "home")},
			wantConfig: filepath.Join(mac, "home", ".config", "aggrega", "config.yaml"),
			wantCaches: filepath.Join(mac, "home", "Library", "Caches", "aggrega"),
		},
	}
}

// offlineWorkspace writes a workspace whose configuration turns the offline
// posture on, so seeding the official hub pins its built-in trust baseline and
// contacts nothing.
func offlineWorkspace(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, workspaceconfig.ConfigFilename),
		[]byte("workspace: {}\noffline: true\n"), 0o644); err != nil {
		t.Fatalf("write the workspace configuration: %v", err)
	}
	return ws
}

// runAsMachine executes the real command tree against a stated machine: the
// platform and the frozen environment ride the context the tree is executed
// with, and the tree's own construction registers the command extensions that
// machine's configuration declares, through the production registration bound
// to the same machine. It returns the two streams separately and the error the
// tree returned.
func runAsMachine(t *testing.T, platform machinedirs.Platform, env map[string]string, ws string, args ...string) (string, string, error) {
	t.Helper()
	lookup := frozenEnv(env)
	prev := cliPluginRegistrations
	cliPluginRegistrations = func() []cliCommandReg {
		return cliRegistrationsResolving(context.Background(), ws, platform, lookup)
	}
	defer func() { cliPluginRegistrations = prev }()

	var out, errOut bytes.Buffer
	root := newRootCmd()
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetIn(strings.NewReader(""))
	root.SetArgs(append([]string{"--workspace-root", ws}, args...))
	err := root.ExecuteContext(withMachine(t.Context(), platform, lookup))
	return out.String(), errOut.String(), err
}

// hubBaselineNotice is the line seeding the official hub prints when the
// offline posture keeps it from asking the hub for its advertised policy.
func hubBaselineNotice() string {
	host := workspaceconfig.HubBaselineHost
	return fmt.Sprintf("Pinned the built-in trust baseline for %s instead of an advertised policy (registrytrust: fetch %s: %s)\n",
		host, host, egress.ErrOfflineEgress().Error())
}

// TestConfigInitSaysWhereTheMachinesFilesAreOnEachPlatform drives the real
// `config init` command tree as a Windows, a Linux and a macOS machine, each a
// frozen environment over scratch directories, and holds what it does to each
// platform's convention.
//
// On every platform: the global configuration file is created where that
// platform keeps it and nowhere else; the answer opens with the two location
// lines, exactly — a statement that the file was created, then where the
// machine-wide caches are — with no character after either path, and goes on
// with the lines seeding already printed; nothing is written to the error
// stream, so there is no warning about the file being created; a second seeding
// without --force refuses and prints nothing; and a second seeding with --force
// says it is using the file that is there.
//
// Planted defects: removing the Windows branch from the location rule fails the
// windows row, which then finds no file under APPDATA and a different first
// line; restoring a full stop after a path fails every row on the exact lines.
func TestConfigInitSaysWhereTheMachinesFilesAreOnEachPlatform(t *testing.T) {
	for _, tc := range machineCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			ws := offlineWorkspace(t)

			out, errOut, err := runAsMachine(t, tc.platform, tc.env, ws, "config", "init")
			if err != nil {
				t.Fatalf("config init: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
			}
			want := "Created the global configuration file at " + tc.wantConfig + "\n" +
				"Machine-wide caches are kept in " + tc.wantCaches + "\n" +
				hubBaselineNotice() +
				"Registered registry " + workspaceconfig.HubBaselineHost + " in " + tc.wantConfig + "\n"
			if out != want {
				t.Errorf("config init printed:\n%s\nwant exactly:\n%s", out, want)
			}
			if errOut != "" {
				t.Errorf("config init wrote to the error stream while creating the file it is for:\n%s", errOut)
			}
			if _, statErr := os.Stat(tc.wantConfig); statErr != nil {
				t.Fatalf("the global configuration file is not where %s keeps it (%s): %v", tc.name, tc.wantConfig, statErr)
			}
			if tc.elsewhere != "" {
				if entries, _ := os.ReadDir(tc.elsewhere); len(entries) != 0 {
					t.Errorf("config init wrote into %s, which this platform's rule does not read", tc.elsewhere)
				}
			}

			// A second seeding without --force refuses the file that is there, and
			// the refusal is its whole answer.
			out, errOut, err = runAsMachine(t, tc.platform, tc.env, ws, "config", "init")
			var exists *configops.ConfigFileExistsError
			if !errors.As(err, &exists) || exists.Path() != tc.wantConfig {
				t.Fatalf("a second config init = %v; want it refused naming %s", err, tc.wantConfig)
			}
			if out != "" || errOut != "" {
				t.Errorf("a refused config init printed %q / %q; it created nothing to report", out, errOut)
			}

			// With --force it writes into the file that is there, and says it is
			// using that file rather than that it created one.
			out, errOut, err = runAsMachine(t, tc.platform, tc.env, ws, "config", "init", "--force")
			if err != nil {
				t.Fatalf("config init --force: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
			}
			wantUsing := "Using the global configuration file at " + tc.wantConfig + "\n" +
				"Machine-wide caches are kept in " + tc.wantCaches + "\n"
			if !strings.HasPrefix(out, wantUsing) {
				t.Errorf("config init --force printed:\n%s\nwant it to open with exactly:\n%s", out, wantUsing)
			}
			if errOut != "" {
				t.Errorf("config init --force wrote to the error stream:\n%s", errOut)
			}
		})
	}
}

// TestAMissingNamedGlobalFileIsCreatedQuietlyAndRefusedInOneSentence states a
// Windows machine whose AGGREGA_CONFIG names a file that does not exist — named
// like a project's configuration file, in the user's profile — and drives the
// real command tree, its construction included.
//
// Seeding creates that file and says so, with nothing on the error stream: no
// warning that the file is missing while the command whose job is creating it
// runs, and none from building the command tree. Any other command that reads
// its configuration halts on a missing named file, and the halt is one sentence
// ending with the path, carrying neither the system call that looked nor the
// operating system's own wording.
func TestAMissingNamedGlobalFileIsCreatedQuietlyAndRefusedInOneSentence(t *testing.T) {
	scratch := t.TempDir()
	named := filepath.Join(scratch, "Users", "Utente", workspaceconfig.ConfigFilename)
	env := map[string]string{
		workspaceconfig.EnvConfigOverride: named,
		"APPDATA":                         filepath.Join(scratch, "AppData", "Roaming"),
		"LOCALAPPDATA":                    filepath.Join(scratch, "AppData", "Local"),
		"USERPROFILE":                     filepath.Join(scratch, "Users", "Utente"),
	}
	ws := offlineWorkspace(t)
	warnings := withRegistrationWarnings(t)

	out, errOut, err := runAsMachine(t, machinedirs.Windows, env, ws, "config", "init")
	if err != nil {
		t.Fatalf("config init: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
	if want := "Created the global configuration file at " + named + "\n"; !strings.HasPrefix(out, want) {
		t.Errorf("config init printed:\n%s\nwant it to open with exactly:\n%s", out, want)
	}
	if errOut != "" || warnings.Len() != 0 {
		t.Errorf("config init warned while creating the file it was named: stderr %q, registration %q", errOut, warnings.String())
	}
	if _, statErr := os.Stat(named); statErr != nil {
		t.Fatalf("the named file was not created at %s: %v", named, statErr)
	}

	// Another command, a fresh missing name.
	missing := filepath.Join(scratch, "elsewhere", workspaceconfig.ConfigFilename)
	env[workspaceconfig.EnvConfigOverride] = missing
	out, errOut, err = runAsMachine(t, machinedirs.Windows, env, ws, "config", "show")
	want := "the global configuration file named by --config or " + workspaceconfig.EnvConfigOverride +
		" does not exist: " + missing
	if err == nil || err.Error() != want {
		t.Fatalf("config show over a missing named file = %v; want exactly %q", err, want)
	}
	var notFound *workspaceconfig.GlobalConfigNotFoundError
	if !errors.As(err, &notFound) || notFound.Path() != missing || !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the halt does not carry the missing-file condition for %s: %#v", missing, err)
	}
	if out != "" || errOut != "" || warnings.Len() != 0 {
		t.Errorf("a halting config show printed %q / %q and registration warned %q; the halt is the whole answer",
			out, errOut, warnings.String())
	}
}

// TestRegistrationStillWarnsWhenTheProjectsConfigurationCannotBeRead is the
// control for the quiet case above: what keeps the command tree's construction
// quiet is the missing named file alone, so a project configuration that does
// not load is still reported, once, and registers nothing.
func TestRegistrationStillWarnsWhenTheProjectsConfigurationCannotBeRead(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, workspaceconfig.ConfigFilename),
		[]byte("workspace: {}\nplugins: [unclosed\n"), 0o644); err != nil {
		t.Fatalf("write the workspace configuration: %v", err)
	}
	warnings := withRegistrationWarnings(t)

	regs := cliRegistrationsResolving(t.Context(), ws, machinedirs.Windows, frozenEnv(map[string]string{
		"APPDATA": filepath.Join(t.TempDir(), "AppData", "Roaming"),
	}))
	if len(regs) != 0 {
		t.Errorf("a configuration that does not load registered %d command(s)", len(regs))
	}
	if !strings.HasPrefix(warnings.String(), "warning: ignoring configured CLI plugins: ") {
		t.Errorf("a configuration that does not load was not reported; registration wrote %q", warnings.String())
	}
}
