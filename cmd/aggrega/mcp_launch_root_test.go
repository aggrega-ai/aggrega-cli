// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// writeLaunchRootFile writes body to path, creating the directories above it.
func writeLaunchRootFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestAWorkspaceRootALaunchNamesThatIsNotThereIsRefusedNamingWhereItCameFrom
// starts `aggrega mcp` as a listener — by --listen, and by --transport http —
// and `aggrega serve` from the real command tree with a workspace root that is
// not there, given by the variable or by the flag, and reads the refusal each
// ends with at startup: it names whichever supplied the value — the variable,
// with the desktop setting that sets it, where the variable did, the flag where
// the flag did, the flag where both were given because the flag wins — and
// quotes the value as it was given. A client that launches the server with a
// placeholder it never filled is the case the wording exists for, so one row
// hands the variable exactly that text: the refusal reports it as it arrived and
// recognises nothing in it. `aggrega mcp` over standard input and output serves
// the same refusal instead of ending with it, which
// TestAnUnusableWorkspaceRootIsServedAsTheRefusalOverStandardInputAndOutput
// drives.
//
// 🔴 PLANTED DEFECT: have validateWorkspaceRootFlag name "--workspace-root"
// whatever its source, as it once did for every value, and each row whose value
// came from the variable fails, naming the flag nobody passed.
func TestAWorkspaceRootALaunchNamesThatIsNotThereIsRefusedNamingWhereItCameFrom(t *testing.T) {
	// Not parallel: every row sets the process environment and the working
	// directory, and drives the command tree, whose pre-run writes the
	// process-global display decision.
	const variable = workspaceconfig.EnvWorkspaceRoot
	const flag = "--workspace-root"
	missing := func(t *testing.T) string { return filepath.Join(t.TempDir(), "no-such-folder") }
	unfilled := func(*testing.T) string { return "${user_config.workspace_root}" }
	listen := []string{"mcp", "--listen", "127.0.0.1:0"}
	listenHTTP := []string{"mcp", "--transport", "http", "--listen", "127.0.0.1:0"}

	for _, tc := range []struct {
		name   string
		launch []string
		// fromVariable and fromFlag give the value through the variable, the flag,
		// or both; the other carries nothing, or an existing folder where both.
		fromVariable, fromFlag func(t *testing.T) string
		wantSource, notSource  string
	}{
		{name: "mcp --listen: the variable names a folder that is not there", launch: listen,
			fromVariable: missing, wantSource: workspaceconfig.EnvWorkspaceRootSource, notSource: flag},
		{name: "mcp --transport http: the variable names a folder that is not there", launch: listenHTTP,
			fromVariable: missing, wantSource: workspaceconfig.EnvWorkspaceRootSource, notSource: flag},
		{name: "mcp --listen: the variable carries a placeholder its client never filled", launch: listen,
			fromVariable: unfilled, wantSource: workspaceconfig.EnvWorkspaceRootSource, notSource: flag},
		{name: "mcp --transport http: the flag names a folder that is not there", launch: listenHTTP,
			fromFlag: missing, wantSource: flag, notSource: variable},
		{name: "mcp --listen: the flag wins over the variable, and is the one named", launch: listen,
			fromVariable: func(t *testing.T) string { return t.TempDir() }, fromFlag: missing,
			wantSource: flag, notSource: variable},
		{name: "serve: the variable names a folder that is not there", launch: []string{"serve"},
			fromVariable: missing, wantSource: workspaceconfig.EnvWorkspaceRootSource, notSource: flag},
		{name: "serve: the flag names a folder that is not there", launch: []string{"serve"},
			fromFlag: missing, wantSource: flag, notSource: variable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			args := append([]string(nil), tc.launch...)
			given := ""
			t.Setenv(variable, "")
			if tc.fromVariable != nil {
				given = tc.fromVariable(t)
				t.Setenv(variable, given)
			}
			if tc.fromFlag != nil {
				given = tc.fromFlag(t)
				args = append(args, flag, given)
			}

			var stdin, stdout, stderr bytes.Buffer
			cmd := newRootCmd()
			cmd.SetIn(&stdin)
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			cmd.SetArgs(args)
			_, err := cmd.ExecuteContextC(context.Background())
			if err == nil {
				t.Fatalf("aggrega %s started over a workspace root that is not there; stdout:\n%s",
					strings.Join(tc.launch, " "), stdout.String())
			}
			want := tc.wantSource + ` names the workspace root "` + given + `"`
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal reads %q; want it to carry %q", err, want)
			}
			if strings.Contains(err.Error(), tc.notSource) {
				t.Errorf("the refusal %q names %s, which did not supply the value", err, tc.notSource)
			}
			if !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("the refusal %q does not carry the reason the folder could not be used", err)
			}
			if stdout.Len() != 0 {
				t.Errorf("a refused launch wrote to its output stream: %q", stdout.String())
			}
		})
	}
}

// TestAnEmptyWorkspaceRootVariableLeavesTheServerToFindItsWorkspace starts
// `aggrega mcp` from the real command tree with the workspace-root variable set
// to nothing, in a folder inside a workspace, and asks its configuration reading
// which workspace it answers for: the variable is read as unset, so the server
// starts and finds its workspace by discovery from the folder it was started
// in — the enclosing workspace — exactly as with no variable at all. The folder
// is inside the workspace rather than the workspace itself so that an empty
// value taken as a root, which would be that folder, answers differently.
//
// It holds on both launches an empty value keeps that meaning on: one carrying
// no desktop bundle version, and one carrying it with --workspace-root typed
// empty, which is the reader's own choice and wins over the variable as on every
// command. Only the bundle's launch naming no root at all is refused instead,
// which TestAnEmptyWorkspaceFolderUnderTheDesktopBundleIsServedAsItsRefusal
// drives.
//
// 🔴 PLANTED DEFECTS: drop validateWorkspaceRootFlag's reading of an empty value
// as unset, and the empty variable roots the server in the folder it was started
// in, which is not the workspace. Have emptyDesktopWorkspaceFolder ignore whether
// the flag was typed, and the row typing it empty fails, refused.
func TestAnEmptyWorkspaceRootVariableLeavesTheServerToFindItsWorkspace(t *testing.T) {
	// Not parallel: every row sets the process environment and the working
	// directory.
	for _, row := range []struct {
		name string
		// flags follow `mcp` on the command line; bundle is AGGREGA_BUNDLE_VERSION,
		// empty for none.
		flags  []string
		bundle string
	}{
		{name: "no desktop bundle version"},
		{name: "the desktop bundle version, and --workspace-root typed empty",
			flags: []string{"--" + workspaceRootFlagName + "="}, bundle: bundleVersionProbe},
	} {
		t.Run(row.name, func(t *testing.T) {
			ws := t.TempDir()
			writeLaunchRootFile(t, filepath.Join(ws, workspaceconfig.ConfigFilename), "workspace: {}\n")
			startedIn := filepath.Join(ws, "sub")
			if err := os.MkdirAll(startedIn, 0o755); err != nil {
				t.Fatalf("create %s: %v", startedIn, err)
			}
			t.Chdir(startedIn)
			t.Setenv(workspaceconfig.EnvWorkspaceRoot, "")
			// Set on every row, empty where the row carries none, so a value in this
			// process's own environment cannot decide which launch a row is.
			t.Setenv(workspaceconfig.EnvBundleVersion, row.bundle)
			// Offline through the environment rather than a file, so the documentation
			// acquisition at the start of the run stays off the network whichever root a
			// planted defect lands the server in.
			t.Setenv("AGGREGA_OFFLINE", "true")

			var stdin, stdout, stderr bytes.Buffer
			stdin.WriteString(rpcLine(1, "initialize", `{"protocolVersion":"2025-06-18"}`))
			stdin.WriteString(rpcLine(2, "tools/call", `{"name":"config-show","arguments":{}}`))
			cmd := newRootCmd()
			cmd.SetIn(&stdin)
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			cmd.SetArgs(append([]string{"mcp"}, row.flags...))
			if code := executeRoot(context.Background(), cmd); code != 0 {
				t.Fatalf("aggrega mcp exited %d with the variable empty\nstderr:\n%s", code, stderr.String())
			}

			root, _ := configShowAnswer(t, answersByID(t, stdout.String(), 1, 2)[2])
			if !sameDirectory(t, root, ws) {
				t.Errorf("the server answers for %q; want the workspace enclosing the folder it was started in, %q",
					root, ws)
			}
		})
	}
}

// TestALaunchRootInsideAWorkspaceIsTheRootOfTheServersOwnToolsAndItsCommandTools
// runs the built binary as a client launches it — `aggrega mcp` started in a
// folder of the client's own, with the workspace root in
// AGGREGA_WORKSPACE_ROOT — where that root is a folder inside a workspace, and
// asks the same question two ways: the server's own configuration reading
// (config-show), and the command tool that runs `aggrega config show` in a
// separate process (cli-config-show), once for the files that contributed and
// once for the root's own configuration file.
//
// Both take the launch root as the workspace root, so both report that one
// folder and the one configuration file in it, and neither reads the enclosing
// workspace's. The folder holds a configuration file of its own, so the files
// compared are never an empty list agreeing with another empty list.
//
// 🔴 PLANTED DEFECT: have resolutionInputs hand a call naming no root the launch
// root as where resolution begins and nothing as the root — the start-folder
// reading — and the server's own reading answers for the enclosing workspace,
// with its file beneath the folder's, while the command still answers for the
// folder.
func TestALaunchRootInsideAWorkspaceIsTheRootOfTheServersOwnToolsAndItsCommandTools(t *testing.T) {
	t.Parallel()
	bin := buildServeBinary(t)

	ws := t.TempDir()
	wsConfig := filepath.Join(ws, workspaceconfig.ConfigFilename)
	writeLaunchRootFile(t, wsConfig, "workspace: {}\n")
	sub := filepath.Join(ws, "sub")
	subConfig := filepath.Join(sub, workspaceconfig.ConfigFilename)
	// Offline here keeps the documentation acquisition at the start of the run off
	// the network, read through the one file both answers must agree on.
	writeLaunchRootFile(t, subConfig, "offline: true\n")

	var stdin, stdout, stderr bytes.Buffer
	stdin.WriteString(rpcLine(1, "initialize", `{"protocolVersion":"2025-06-18"}`))
	stdin.WriteString(rpcLine(2, "tools/call", `{"name":"config-show","arguments":{}}`))
	stdin.WriteString(rpcLine(3, "tools/call", `{"name":"cli-config-show","arguments":{}}`))
	stdin.WriteString(rpcLine(4, "tools/call", `{"name":"cli-config-show","arguments":{"workspace":true}}`))
	cmd := exec.Command(bin, "mcp")
	cmd.Dir = t.TempDir() // the client's own folder, which no workspace resolves from
	cmd.Env = builtBinaryEnv(t, workspaceconfig.EnvWorkspaceRoot+"="+sub)
	cmd.Stdin = &stdin
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("aggrega mcp: %v\nstderr:\n%s", err, stderr.String())
	}
	answers := answersByID(t, stdout.String(), 1, 2, 3, 4)

	ownRoot, ownFiles := configShowAnswer(t, answers[2])
	if ownRoot != sub {
		t.Errorf("the server's own reading answers for %q; want the root the launch named, %q", ownRoot, sub)
	}
	if !slices.Contains(ownFiles, subConfig) || slices.Contains(ownFiles, wsConfig) {
		t.Errorf("the server's own reading names the configuration files %q; want %s and not %s",
			ownFiles, subConfig, wsConfig)
	}

	command, output := commandToolAnswer(t, answers[3])
	if !strings.Contains(command, "--workspace-root="+sub) {
		t.Errorf("the command tool ran %q; want it handed the launch root as --workspace-root", command)
	}
	var cliFiles []string
	for _, m := range numberedLayerLine.FindAllStringSubmatch(output, -1) {
		if filepath.IsAbs(m[1]) {
			cliFiles = append(cliFiles, m[1])
		}
	}
	if !slices.Equal(ownFiles, cliFiles) {
		t.Errorf("the server's own reading names the configuration files %q and the command names %q; "+
			"one launch root must be read as one workspace", ownFiles, cliFiles)
	}

	// The command prints the configuration file at the root it took; it must be
	// the file at the root the server's own reading answered for.
	_, printed := commandToolAnswer(t, answers[4])
	atOwnRoot, err := os.ReadFile(filepath.Join(ownRoot, workspaceconfig.ConfigFilename))
	if err != nil {
		t.Fatalf("read the configuration file at the server's root %s: %v", ownRoot, err)
	}
	if printed != string(atOwnRoot) {
		t.Errorf("the command printed %q as its workspace's configuration file, and the file at the root the "+
			"server answers for holds %q", printed, atOwnRoot)
	}
}

// numberedLayerLine is one entry of the contributing-layer list `aggrega config
// show` prints, capturing the entry as printed.
var numberedLayerLine = regexp.MustCompile(`(?m)^\s+\d+\. (.+)$`)

// configShowAnswer reads the server's configuration reading out of its answer:
// the workspace root it answered for, and the contributing layers that are
// configuration files, in the order it gives them.
func configShowAnswer(t *testing.T, frame rpcFrame) (root string, files []string) {
	t.Helper()
	if frame.Error != nil {
		t.Fatalf("config-show was refused: %s", frame.Error.Message)
	}
	var call struct {
		StructuredContent struct {
			WorkspaceRoot *string `json:"workspace_root"`
			Layers        []struct {
				Name *string `json:"name"`
			} `json:"layers"`
		} `json:"structuredContent"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(frame.Result, &call); err != nil || call.IsError || call.StructuredContent.WorkspaceRoot == nil {
		t.Fatalf("config-show did not answer with its reading (isError=%v): %v\n%s", call.IsError, err, frame.Result)
	}
	for _, layer := range call.StructuredContent.Layers {
		if layer.Name != nil && filepath.IsAbs(*layer.Name) {
			files = append(files, *layer.Name)
		}
	}
	return *call.StructuredContent.WorkspaceRoot, files
}

// commandToolAnswer reads a generated command tool's answer: the command line
// it ran and what that command wrote to its output.
func commandToolAnswer(t *testing.T, frame rpcFrame) (command, output string) {
	t.Helper()
	if frame.Error != nil {
		t.Fatalf("the command tool was refused: %s", frame.Error.Message)
	}
	var call struct {
		StructuredContent struct {
			Command string `json:"command"`
			Output  string `json:"output"`
		} `json:"structuredContent"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(frame.Result, &call); err != nil || call.IsError {
		t.Fatalf("the command tool did not succeed (isError=%v): %v\n%s", call.IsError, err, frame.Result)
	}
	return call.StructuredContent.Command, call.StructuredContent.Output
}

// sameDirectory reports whether a and b name one directory, reading through any
// link either is spelled through: a working directory is reported by the
// operating system with its links resolved, where a temporary directory may be
// spelled through one.
func sameDirectory(t *testing.T, a, b string) bool {
	t.Helper()
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		t.Fatalf("resolve %s: %v", a, err)
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		t.Fatalf("resolve %s: %v", b, err)
	}
	return ra == rb
}
