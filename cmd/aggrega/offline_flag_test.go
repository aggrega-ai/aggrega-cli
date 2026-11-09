// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	stderrors "errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/registryops"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// postureProbeCmd is the command name the posture probe registers under. It
// exists only inside these tests: the posture is not a thing a command reports,
// so driving it through a real command's own output would measure that
// command's reporting as well as the layer under test.
const postureProbeCmd = "offline-posture-probe"

// resolvedPosture runs the REAL command tree over args and reports the offline
// posture the invocation resolved, together with the layer the provenance
// report attributes it to.
//
// It drives the production tree rather than calling the overlay directly, which
// is the whole point: the flag is registered on the root, read in the root's
// pre-run, and composed onto the one resolution every command body reads, so a
// test that composed the layer itself would prove the overlay works and say
// nothing about whether anything reaches it. A probe command is attached so the
// pre-run runs against a command body that does nothing else.
//
// The environment is pinned to a directory holding no global config, so the
// resolution is the workspace under test plus whatever the caller sets and
// never the configuration of the machine the test runs on.
func resolvedPosture(t *testing.T, wsRoot string, args ...string) (bool, string) {
	t.Helper()
	empty := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", empty)
	t.Setenv("HOME", empty)
	if os.Getenv("AGGREGA_OFFLINE") != "" {
		t.Setenv("AGGREGA_OFFLINE", "")
	}
	return resolvedPostureNoEnvPin(t, wsRoot, args...)
}

// resolvedPostureNoEnvPin is resolvedPosture without the environment pinning,
// for a caller that has already pinned it — a test of the environment layer
// itself, which has to set the variable after the pinning rather than before.
func resolvedPostureNoEnvPin(t *testing.T, wsRoot string, args ...string) (bool, string) {
	t.Helper()
	var offline bool
	var origin string
	root := newRootCmd()
	root.AddCommand(&cobra.Command{
		Use:           postureProbeCmd,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			offline = rw.Config().Offline
			origins, err := rw.ValueOrigins()
			if err != nil {
				return err
			}
			for _, o := range origins {
				if o.Field == workspaceconfig.OfflineSettingKey {
					origin = o.Source
				}
			}
			return nil
		},
	})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs(append([]string{postureProbeCmd, "--workspace-root", wsRoot}, args...))
	if err := root.Execute(); err != nil {
		t.Fatalf("posture probe with %v: %v", args, err)
	}
	return offline, origin
}

// writeOfflineWorkspace declares a workspace root whose config file carries
// body verbatim beneath the root marker, and returns the root.
func writeOfflineWorkspace(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace: {}\n"+body)
	return root
}

// TestOfflineFlagIsOnTheGlobalSurface proves the flag is reachable from every
// command rather than from one, by reading it off the root's own help.
//
// The positive control matters more here than usual: a help text is a string
// this test searches, so a lookup that matched nothing would report the same
// clean result as a flag that is genuinely absent.
func TestOfflineFlagIsOnTheGlobalSurface(t *testing.T) {
	t.Parallel()
	root := newRootCmd()
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(out)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("--help: %v", err)
	}
	if !strings.Contains(out.String(), "--offline") {
		t.Errorf("the root help does not offer --offline:\n%s", out.String())
	}
	// The control: a flag the global surface really does carry. Without it a
	// help text that stopped listing global flags at all would read as this
	// flag being missing rather than as the listing being broken.
	if !strings.Contains(out.String(), "--config") {
		t.Fatal("the root help lists neither --offline nor --config, so the search above measures the help rendering and not the flag set")
	}
	if f := root.PersistentFlags().Lookup(workspaceconfig.OfflineSettingKey); f == nil {
		t.Error("--offline is not a persistent flag, so it reaches only the root and not the commands whose refusals depend on it")
	}
}

// TestOfflineFlagTopsThePrecedenceLadder drives all three layers through the
// real command tree and pins the order the configuration model declares: flag
// over environment over the configuration file.
//
// The fourth row is the one that cannot be got right by reading the flag's
// value, and it is the reason the layer exists at all. `--offline=false`
// against a file that turns the posture on has to win: a reader who did not
// write that file needs one way to get past it for one run. A layer that read
// "is the flag false" rather than "did the reader set it" would leave every
// invocation looking like that row, and would silently overwrite the file's
// posture on every command that never mentioned offline.
func TestOfflineFlagTopsThePrecedenceLadder(t *testing.T) {
	for _, tc := range []struct {
		name    string
		config  string
		env     string
		args    []string
		want    bool
		wantSrc string
	}{
		{
			name:    "nothing set anywhere",
			want:    false,
			wantSrc: "(built-in defaults)",
		},
		{
			name:    "the configuration file alone",
			config:  "offline: true\n",
			want:    true,
			wantSrc: "", // a path, asserted separately below
		},
		{
			name:    "the environment beats the file",
			config:  "offline: false\n",
			env:     "true",
			want:    true,
			wantSrc: "env:AGGREGA_OFFLINE",
		},
		{
			name:    "the flag beats the environment",
			config:  "offline: false\n",
			env:     "false",
			args:    []string{"--offline"},
			want:    true,
			wantSrc: "--offline",
		},
		{
			name:    "the flag turns off what the environment turned on",
			env:     "true",
			args:    []string{"--offline=false"},
			want:    false,
			wantSrc: "--offline",
		},
		{
			name:    "the flag turns off what the file turned on",
			config:  "offline: true\n",
			args:    []string{"--offline=false"},
			want:    false,
			wantSrc: "--offline",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wsRoot := writeOfflineWorkspace(t, tc.config)
			empty := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", empty)
			t.Setenv("HOME", empty)
			t.Setenv("AGGREGA_OFFLINE", tc.env)

			got, src := resolvedPostureNoEnvPin(t, wsRoot, tc.args...)
			if got != tc.want {
				t.Errorf("resolved offline = %v; want %v (config %q, AGGREGA_OFFLINE %q, args %v)",
					got, tc.want, strings.TrimSpace(tc.config), tc.env, tc.args)
			}
			switch tc.wantSrc {
			case "":
				if !filepath.IsAbs(src) {
					t.Errorf("origin = %q; want the absolute path of the configuration file that set it", src)
				}
			default:
				if src != tc.wantSrc {
					t.Errorf("origin = %q; want %q", src, tc.wantSrc)
				}
			}
		})
	}
}

// TestEveryOfflineLayerIsActuallyRead is the needle, and it is the one test
// here whose whole value is that it REDDENS when the mechanism is broken.
//
// Setting a layer and observing the posture proves nothing on its own: a layer
// the product never reads and a layer that reads `false` produce the same
// observation, which is exactly how a fixture writing the setting into a file
// nothing loads survived as a passing proof. The discriminator is a value the
// layer must REJECT. `yes` is parsed by YAML 1.2 as a string rather than a
// boolean, and the environment surface accepts only the unambiguous spellings,
// and the flag surface parses a Go boolean — so every one of the three refuses
// it, and a layer that is not read cannot refuse anything.
func TestEveryOfflineLayerIsActuallyRead(t *testing.T) {
	t.Run("the configuration file", func(t *testing.T) {
		wsRoot := writeOfflineWorkspace(t, "offline: yes\n")
		empty := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", empty)
		t.Setenv("HOME", empty)
		t.Setenv("AGGREGA_OFFLINE", "")
		_, err := workspaceconfig.Resolve(wsRoot, "")
		if err == nil {
			t.Fatal("a configuration file carrying `offline: yes` resolved cleanly; the key is not being read, because YAML 1.2 hands it over as a string and a boolean field cannot take one")
		}
		if !strings.Contains(err.Error(), workspaceconfig.OfflineSettingKey) {
			t.Errorf("the refusal does not name the offending key: %v", err)
		}
	})

	t.Run("the environment", func(t *testing.T) {
		wsRoot := writeOfflineWorkspace(t, "")
		empty := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", empty)
		t.Setenv("HOME", empty)
		t.Setenv("AGGREGA_OFFLINE", "yes")
		_, err := workspaceconfig.Resolve(wsRoot, "", os.Getenv)
		if err == nil {
			t.Fatal("AGGREGA_OFFLINE=yes resolved cleanly; the variable is not being read")
		}
		if !strings.Contains(err.Error(), "AGGREGA_OFFLINE") {
			t.Errorf("the refusal does not name the offending variable: %v", err)
		}
	})

	t.Run("the command line", func(t *testing.T) {
		wsRoot := writeOfflineWorkspace(t, "")
		root := newRootCmd()
		root.AddCommand(&cobra.Command{Use: postureProbeCmd, RunE: func(*cobra.Command, []string) error { return nil }})
		root.SetOut(io.Discard)
		root.SetErr(io.Discard)
		root.SetArgs([]string{postureProbeCmd, "--workspace-root", wsRoot, "--offline=yes"})
		if err := root.Execute(); err == nil {
			t.Fatal("--offline=yes parsed cleanly; the flag is not being read as a boolean")
		}
	})
}

// TestUpgradeRefusesUnderEveryOfflineLayer pins the ruled split's restrictive
// half: a full refresh floats each dependency to the highest version a registry
// currently offers, which is a claim about registry state, so it refuses
// however the posture was reached.
//
// The refusal is asserted by its condition rather than by its wording, and the
// wording is then checked for the three things a reader needs: that the posture
// is what stopped them, which layer set it, and how to get past it for one run.
func TestUpgradeRefusesUnderEveryOfflineLayer(t *testing.T) {
	for _, tc := range []struct {
		name      string
		config    string
		env       string
		args      []string
		wantNamed string
	}{
		{name: "set by the configuration file", config: "offline: true\n", wantNamed: workspaceconfig.ConfigFilename},
		{name: "set by the environment", env: "true", wantNamed: "the AGGREGA_OFFLINE environment variable"},
		{name: "set by the flag", args: []string{"--offline"}, wantNamed: "--offline"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			releasePath := authoringFixture(t, root)
			if tc.config != "" {
				writeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace: {}\n"+tc.config)
			}
			empty := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", empty)
			t.Setenv("HOME", empty)
			t.Setenv("AGGREGA_OFFLINE", tc.env)

			cmd := newRootCmd()
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			// --yes answers the bare refresh's confirmation in advance. That gate
			// sits ahead of the posture check by design — it guards the operator's
			// intent, which no resolved state changes — so without it this case
			// would stop at the confirmation and assert nothing about the posture
			// it exists to pin.
			args := append([]string{"pull", "--workspace-root", root, "--release", releasePath,
				"--catalog", filepath.Join(root, "catalog"), "--upgrade", "--yes"}, tc.args...)
			cmd.SetArgs(args)
			err := cmd.Execute()
			if err == nil {
				t.Fatal("--upgrade succeeded under the offline posture; a full refresh cannot learn what a registry currently offers")
			}
			assertOfflineRefusal(t, err, tc.wantNamed)
			if !strings.Contains(err.Error(), "highest version") {
				t.Errorf("the refusal does not say what about --upgrade cannot be done offline: %v", err)
			}
		})
	}
}

// assertOfflineRefusal pins that err is the offline condition and that its text
// carries the three things a reader acts on.
//
// The condition is matched through the exported accessor rather than against
// the registered code as a string: a string comparison type-checks, reads like
// a match, and goes on compiling after the condition is re-registered under a
// different code, at which point it silently stops matching.
func assertOfflineRefusal(t *testing.T, err error, wantLayerNamed string) {
	t.Helper()
	if !stderrors.Is(err, egress.ErrOfflineEgress()) {
		t.Fatalf("the failure is not the offline condition: %v", err)
	}
	text := err.Error()
	if !strings.Contains(text, "offline") {
		t.Errorf("the refusal does not name the posture: %s", text)
	}
	if !strings.Contains(text, wantLayerNamed) {
		t.Errorf("the refusal does not name the layer that set the posture (want %q): %s", wantLayerNamed, text)
	}
	// A variable is named in words, never in one shell's syntax: the reader may
	// be at a Windows prompt, where "$AGGREGA_OFFLINE" is not what they set.
	if strings.Contains(text, "$AGGREGA_") {
		t.Errorf("the refusal names a variable in a shell's syntax: %s", text)
	}
	// What to do for one run. A refusal a reader cannot act on for this one
	// invocation is the whole reason the flag was added, so its absence here is
	// a failure of the feature rather than of the wording.
	if !strings.Contains(text, "--offline=false") {
		t.Errorf("the refusal does not say how to override the posture for one run: %s", text)
	}
}

// TestTheIncrementalFillDoesNotRefuseUnderTheOfflinePosture is the permissive
// half of the ruled split, and the half a refusal keyed on "does this command
// reach a registry" would have broken.
//
// An incremental fill honours the pins it already records and resolves only
// what has none, so a workspace whose references are all satisfiable locally
// completes it without reaching out at all. An airgapped workspace holding a
// local copy of its catalog is exactly that case, which is why the vocabulary
// distinguishes the two: refusing the fill when the posture is set would refuse
// it for a reason that is not true of it. That holds for the fill a build runs
// as much as for the pull run on its own, since the build's fill is that pull.
//
// The rows are the whole of the split rather than its margins, and the refusing
// row and the filling rows are the acceptance together rather than any one alone.
// One half passing proves only that something happened.
func TestTheIncrementalFillDoesNotRefuseUnderTheOfflinePosture(t *testing.T) {
	// Not parallel: it sets the process environment and drives the command tree, whose pre-run writes the process-global display decision.
	root := t.TempDir()
	t.Chdir(root)
	releasePath := authoringFixture(t, root)
	writeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace: {}\n")
	catalogDir := filepath.Join(root, "catalog")
	lockPath := filepath.Join(root, "release.lock")
	empty := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", empty)
	t.Setenv("HOME", empty)
	t.Setenv("AGGREGA_OFFLINE", "")

	run := func(args ...string) error {
		cmd := newRootCmd()
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		cmd.SetArgs(append([]string{args[0], "--workspace-root", root,
			"--release", releasePath, "--catalog", catalogDir}, args[1:]...))
		return cmd.Execute()
	}

	// A build asked for no fill, with no lockfile, refuses and names the pull that
	// would fill it. It is the negative control for the rows below: without it, a
	// success there would not distinguish a fill that worked from a build that
	// never needed anything.
	if err := run("build", "--offline"); err == nil {
		t.Error("a build with no lockfile and no fill asked for succeeded under the offline posture; there was nothing pinned to render against")
	} else if !strings.Contains(err.Error(), "aggrega pull --release") || !strings.Contains(err.Error(), "--auto-pull") {
		t.Errorf("the refusal does not name the pull that would fill the lockfile: %v", err)
	}
	if _, err := os.Stat(lockPath); !stderrors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused build left a lockfile behind (stat: %v)", err)
	}

	// The fill, asked of the build, with the posture set by the flag: the build
	// runs the pull, which resolves the whole closure from the local catalog and
	// writes the lockfile. This is the row a build that stood its fill down under
	// the posture would turn red.
	if err := run("build", "--auto-pull", "--offline"); err != nil {
		t.Fatalf("a build asked to fill refused under the offline posture over a locally satisfiable closure: %v", err)
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("the build's fill reported success and wrote no lockfile: %v", err)
	}

	// The fill itself, run on its own: this is the row that a parse-time refusal
	// on the fill would have turned red.
	if err := run("pull", "--offline"); err != nil {
		t.Fatalf("the incremental fill refused under the offline posture over a locally satisfiable closure: %v", err)
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("the fill reported success and wrote no lockfile: %v", err)
	}

	// And the same build now that the machine holds what it needs.
	if err := run("build", "--auto-pull", "--offline"); err != nil {
		t.Errorf("a build over a filled lockfile refused under the offline posture: %v", err)
	}
}

// TestPublicationRefusesUnderEveryOfflineLayerAndDryRunDoesNot pins both sides
// of the network-act rule on the command whose entire work is a round trip.
//
// The dry-run row is the one that would fail under a refusal keyed on the
// command name. Its help promises it skips the network call, so it is the
// offline-friendly form of publishing rather than a conflict with the posture,
// and it has to keep working.
func TestPublicationRefusesUnderEveryOfflineLayerAndDryRunDoesNot(t *testing.T) {
	entity := `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/api-server
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      type: object
`
	for _, tc := range []struct {
		name      string
		config    string
		env       string
		args      []string
		wantNamed string
	}{
		{name: "set by the configuration file", config: "offline: true\n", wantNamed: workspaceconfig.ConfigFilename},
		{name: "set by the environment", env: "true", wantNamed: "the AGGREGA_OFFLINE environment variable"},
		{name: "set by the flag", args: []string{"--offline"}, wantNamed: "--offline"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			entityPath := filepath.Join(root, "1.0.0", "entity.yaml")
			writeFixture(t, entityPath, entity)
			writeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename),
				"workspace: {}\ndefault_registry: example.com\n"+tc.config)
			empty := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", empty)
			t.Setenv("HOME", empty)
			t.Setenv("AGGREGA_OFFLINE", tc.env)

			cmd := newRootCmd()
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(append([]string{"push", "--workspace-root", root, entityPath}, tc.args...))
			err := cmd.Execute()
			if err == nil {
				t.Fatal("push succeeded under the offline posture")
			}
			assertOfflineRefusal(t, err, tc.wantNamed)

			// The same posture, the same layer, the one form that reaches no
			// network: it must complete.
			dry := newRootCmd()
			dry.SetOut(io.Discard)
			dry.SetErr(io.Discard)
			dry.SetArgs(append([]string{"push", "--workspace-root", root, entityPath, "--dry-run"}, tc.args...))
			if err := dry.Execute(); err != nil {
				t.Errorf("push --dry-run refused under the offline posture; its help promises it skips the network call: %v", err)
			}
		})
	}
}

// TestRegistryUpdateRefusesUnderTheOfflinePosture pins the third network-act
// command. It carries no dry run, and there is nothing to carve out: the index
// records what each registry currently serves, which cannot be learned without
// reaching one.
func TestRegistryUpdateRefusesUnderTheOfflinePosture(t *testing.T) {
	root := writeOfflineWorkspace(t, "offline: true\nregistries:\n  - host: example.com\n")
	empty := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", empty)
	t.Setenv("HOME", empty)
	t.Setenv("AGGREGA_OFFLINE", "")

	rw, err := workspaceconfig.Resolve(root, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if _, err := registryops.Update(t.Context(), rw, registryops.UpdateOptions{}, io.Discard); err == nil {
		t.Fatal("the registry index build succeeded under the offline posture")
	} else {
		assertOfflineRefusal(t, err, workspaceconfig.ConfigFilename)
	}

	// The control: the same call against the same workspace with the posture
	// off must get past the refusal, so the red above measures the posture and
	// not a workspace that could never have been indexed.
	online := rw.WithFlags(workspaceconfig.FlagOverlay{Offline: new(bool)})
	if _, err := registryops.Update(t.Context(), online, registryops.UpdateOptions{}, io.Discard); err != nil {
		if stderrors.Is(err, egress.ErrOfflineEgress()) {
			t.Error("--offline=false left the offline refusal in place, so the refusal above is not keyed on the posture")
		}
	}
}
