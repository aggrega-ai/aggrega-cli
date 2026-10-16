// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// emptyWorkspaceFolderRefusal is the sentence a desktop launch with an empty
// Workspace folder setting is refused in, written out rather than composed, so
// the sentence a desktop user reads is what the tests below hold.
const emptyWorkspaceFolderRefusal = "the desktop application launched this server with its Workspace folder " +
	"setting empty, so AGGREGA_WORKSPACE_ROOT names no workspace root: set Workspace folder to the folder that " +
	"holds your workspace's .aggrega-config.yaml"

// noWorkspaceFoundNote opens the line a command writes when it uses the folder it
// was started in as the workspace root (noWorkspaceNotice).
const noWorkspaceFoundNote = "no Aggrega workspace was found from "

// TestAnEmptyWorkspaceFolderUnderTheDesktopBundleIsServedAsItsRefusal runs the
// built binary as the desktop bundle launches it — `aggrega mcp` over standard
// input and output, started in a folder of the client's own that holds no
// workspace, with the bundle's version in its environment — and with the
// Workspace folder setting left empty: the variable set to nothing, or absent.
//
// The server answers initialize, so the client connects. A validation, a catalog
// read and a generated command tool, none naming a workspace of its own, each
// answer with the refusal naming the empty setting, the variable and the folder
// to choose. A schema read and the prompt listing, which act in no workspace,
// answer as on any server, and a configuration reading naming a workspace of its
// own is answered there. The server's stderr carries the refusal and never the
// line naming the started-in folder as the workspace root, nothing is written in
// that folder — no .aggrega directory, nothing at all — and the process exits
// cleanly once its input closes.
//
// 🔴 PLANTED DEFECT: remove the bundle-version branch from runMCP (the case
// asking emptyDesktopWorkspaceFolder), and every row fails: the validation and
// the reads are answered from the started-in folder rather than refused, and
// stderr carries the line naming that folder as the workspace root.
func TestAnEmptyWorkspaceFolderUnderTheDesktopBundleIsServedAsItsRefusal(t *testing.T) {
	t.Parallel()
	bin := buildServeBinary(t)

	for _, row := range []struct {
		name string
		// flags follow `mcp` on the command line.
		flags []string
		// absent leaves AGGREGA_WORKSPACE_ROOT out of the environment; otherwise it
		// is set to nothing.
		absent bool
	}{
		{name: "the variable is set to nothing"},
		{name: "the variable is absent", absent: true},
		{name: "--transport stdio, and the variable is set to nothing", flags: []string{"--transport", "stdio"}},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			startedIn := t.TempDir() // the client's own folder, which no workspace resolves from
			own := t.TempDir()
			if err := os.WriteFile(filepath.Join(own, workspaceconfig.ConfigFilename), []byte("workspace: {}\n"), 0o644); err != nil {
				t.Fatalf("write the call's own workspace: %v", err)
			}

			env := builtBinaryEnv(t, "AGGREGA_OFFLINE=1", workspaceconfig.EnvWorkspaceRoot+"=",
				workspaceconfig.EnvBundleVersion+"="+bundleVersionProbe)
			if row.absent {
				env = slices.DeleteFunc(env, func(entry string) bool {
					return strings.HasPrefix(entry, workspaceconfig.EnvWorkspaceRoot+"=")
				})
			}
			ownArgs, err := json.Marshal(map[string]any{"workspace_root": own})
			if err != nil {
				t.Fatalf("encode the call's own workspace: %v", err)
			}

			// A liveness ceiling, not a budget: it separates a server that answers and
			// exits once its input closes from one that never will.
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, bin, append([]string{"mcp"}, row.flags...)...)
			cmd.Dir = startedIn
			cmd.Env = env
			cmd.Stdin = strings.NewReader(rpcLine(1, "initialize", `{"protocolVersion":"2025-06-18"}`) +
				rpcLine(2, "tools/call", `{"name":"validate","arguments":{"release":"release.yaml"}}`) +
				rpcLine(3, "resources/read", `{"uri":"catalog://component/core/compute/api-server"}`) +
				rpcLine(4, "tools/call", `{"name":"`+mcpCommandToolName("aggrega validate")+`","arguments":{}}`) +
				rpcLine(5, "tools/call", `{"name":"get-schema","arguments":{"kind":"component"}}`) +
				rpcLine(6, "tools/call", `{"name":"config-show","arguments":`+string(ownArgs)+`}`) +
				rpcLine(7, "prompts/list", ""))
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("aggrega mcp did not exit cleanly once its input closed: %v\nstdout:\n%s\nstderr:\n%s",
					err, stdout.String(), stderr.String())
			}
			answers := answersByID(t, stdout.String(), 1, 2, 3, 4, 5, 6, 7)

			if a := answers[1]; a.Error != nil || !strings.Contains(string(a.Result), `"serverInfo"`) {
				t.Errorf("initialize was answered %s %+v; want the handshake's result", a.Result, a.Error)
			}
			for _, id := range []int{2, 4} {
				var call struct {
					IsError bool `json:"isError"`
					Content []struct {
						Text string `json:"text"`
					} `json:"content"`
				}
				a := answers[id]
				if a.Error != nil || json.Unmarshal(a.Result, &call) != nil || !call.IsError || len(call.Content) == 0 ||
					!strings.Contains(call.Content[0].Text, emptyWorkspaceFolderRefusal) {
					t.Errorf("tool call %d was answered %s %+v; want a tool error carrying %q",
						id, a.Result, a.Error, emptyWorkspaceFolderRefusal)
				}
			}
			if a := answers[3]; a.Error == nil || !strings.Contains(a.Error.Message, emptyWorkspaceFolderRefusal) {
				t.Errorf("the catalog read was answered %s %+v; want an error carrying %q",
					a.Result, a.Error, emptyWorkspaceFolderRefusal)
			}
			var schema struct {
				IsError bool `json:"isError"`
			}
			if a := answers[5]; a.Error != nil || json.Unmarshal(a.Result, &schema) != nil || schema.IsError {
				t.Errorf("get-schema, which acts in no workspace, was answered %s %+v; want the schema", a.Result, a.Error)
			}
			if root, _ := configShowAnswer(t, answers[6]); !sameDirectory(t, root, own) {
				t.Errorf("a configuration reading naming its own workspace answers for %q; want %q", root, own)
			}
			var prompts struct {
				Prompts []json.RawMessage `json:"prompts"`
			}
			if a := answers[7]; a.Error != nil || json.Unmarshal(a.Result, &prompts) != nil || len(prompts.Prompts) == 0 {
				t.Errorf("prompts/list was answered %s %+v; want the prompt list", a.Result, a.Error)
			}

			if !strings.Contains(stderr.String(), emptyWorkspaceFolderRefusal) {
				t.Errorf("the server's stderr does not carry the refusal %q:\n%s", emptyWorkspaceFolderRefusal, stderr.String())
			}
			if strings.Contains(stderr.String(), noWorkspaceFoundNote) {
				t.Errorf("the server's stderr names the folder it was started in as the workspace root:\n%s",
					stderr.String())
			}
			entries, err := os.ReadDir(startedIn)
			if err != nil {
				t.Fatalf("read the started-in folder %s: %v", startedIn, err)
			}
			if len(entries) != 0 {
				names := make([]string, 0, len(entries))
				for _, e := range entries {
					names = append(names, e.Name())
				}
				t.Errorf("the server wrote %q in the folder it was started in, which nothing names as a workspace", names)
			}
		})
	}
}

// TestAnEmptyWorkspaceFolderUnderTheDesktopBundleRefusesTheInvocationsOwnWorkspace
// starts `aggrega mcp` from the real command tree as the desktop bundle launches
// it with an empty Workspace folder setting, in a folder holding no workspace,
// sends initialize alone, and then asks the invocation's own workspace
// resolution — the one the compiled-module cache's size cap is read through the
// first time a guest loads — for its workspace. It answers with the refusal
// rather than resolving the folder the server was started in.
//
// 🔴 PLANTED DEFECT: drop runMCP's refuseWorkspace call, and the resolution
// answers with that folder as the workspace root.
func TestAnEmptyWorkspaceFolderUnderTheDesktopBundleRefusesTheInvocationsOwnWorkspace(t *testing.T) {
	// Not parallel: it sets the process environment and the working directory.
	t.Chdir(t.TempDir())
	t.Setenv(workspaceconfig.EnvWorkspaceRoot, "")
	t.Setenv(workspaceconfig.EnvBundleVersion, bundleVersionProbe)
	t.Setenv("AGGREGA_OFFLINE", "true")

	var stdin, stdout, stderr bytes.Buffer
	stdin.WriteString(rpcLine(1, "initialize", `{"protocolVersion":"2025-06-18"}`))
	root := newRootCmd()
	root.SetIn(&stdin)
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"mcp"})
	ran, err := root.ExecuteContextC(context.Background())
	if err != nil {
		t.Fatalf("aggrega mcp ended with %v; want it served\nstderr:\n%s", err, stderr.String())
	}
	answersByID(t, stdout.String(), 1)
	if !strings.Contains(stderr.String(), emptyWorkspaceFolderRefusal) {
		t.Errorf("the launch's stderr does not carry the refusal %q:\n%s", emptyWorkspaceFolderRefusal, stderr.String())
	}

	ws, werr := workspaceFromContext(ran.Context())
	var empty *workspaceconfig.EmptyWorkspaceFolderError
	if !errors.As(werr, &empty) {
		root := ""
		if ws != nil {
			root = ws.Root()
		}
		t.Errorf("the invocation's own workspace resolution answered %q, %v; want the refusal of the empty setting",
			root, werr)
	}
}

// TestAnEmptyWorkspaceFolderOnAListenerIsNotTheDesktopRefusal starts `aggrega mcp`
// as a listener — by --listen, and by --transport http — from the real command
// tree, with the variable set to nothing and the bundle's version in the
// environment, in a folder holding no workspace. A listener is no desktop
// launch: each is refused at startup for its own reasons, as it would be with no
// bundle version at all, and none with the refusal of an empty Workspace folder.
//
// 🔴 PLANTED DEFECT: drop servesUnusableWorkspaceOverStdio from
// emptyDesktopWorkspaceFolder, and both rows fail, each refused with the empty
// setting's refusal.
func TestAnEmptyWorkspaceFolderOnAListenerIsNotTheDesktopRefusal(t *testing.T) {
	// Not parallel: every row sets the process environment and the working
	// directory, and drives the command tree, whose pre-run writes the
	// process-global display decision.
	for _, launch := range [][]string{
		{"mcp", "--listen", "127.0.0.1:0"},
		{"mcp", "--transport", "http", "--listen", "127.0.0.1:0"},
	} {
		t.Run(strings.Join(launch, " "), func(t *testing.T) {
			t.Chdir(t.TempDir())
			t.Setenv(workspaceconfig.EnvWorkspaceRoot, "")
			t.Setenv(workspaceconfig.EnvBundleVersion, bundleVersionProbe)
			t.Setenv("AGGREGA_OFFLINE", "true")

			var stdin, stdout, stderr bytes.Buffer
			cmd := newRootCmd()
			cmd.SetIn(&stdin)
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			cmd.SetArgs(launch)
			_, err := cmd.ExecuteContextC(context.Background())
			if err == nil {
				t.Fatalf("aggrega %s started with no workspace named or found; stdout:\n%s",
					strings.Join(launch, " "), stdout.String())
			}
			var empty *workspaceconfig.EmptyWorkspaceFolderError
			if errors.As(err, &empty) || strings.Contains(err.Error(), emptyWorkspaceFolderRefusal) {
				t.Errorf("a listener was refused with the desktop refusal of an empty Workspace folder: %v", err)
			}
		})
	}
}
