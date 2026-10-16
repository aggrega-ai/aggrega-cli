// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestAnUnusableWorkspaceRootIsServedAsTheRefusalOverStandardInputAndOutput runs
// the built binary as the desktop bundle launches it — `aggrega mcp` over
// standard input and output, started in a folder of the client's own — with a
// workspace root that is not an existing folder, given by the variable or by the
// flag, and holds the session open over a pipe.
//
// The server answers initialize, so the client connects, and lists its tools.
// A validation, a catalog read, and — sent only once those are answered — a
// generated command tool each answer with the refusal the launch met: the
// variable named with the desktop setting that sets it, or the flag, and the
// value quoted exactly as given, a placeholder its client never filled included,
// with nothing recognised in it. The same refusal opens the server's stderr, in
// the words a launch that ends with it writes. The process answers the request sent
// after the others were answered, which a process that had ended could not, and
// exits cleanly once its input closes.
//
// Two rows launch over a folder that is there, by the variable and by the flag,
// and remove it once initialize is answered: every call after that answers with
// the same refusal, naming the variable with the setting, or the flag, rather
// than the server's launch, while the server's stderr does not open with it,
// which is what tells these rows from a launch over a folder that was never there.
//
// 🔴 PLANTED DEFECTS: have runMCP return the refusal of a root that is not an
// existing folder, as it does for a listener, and every row but the two removing
// their folder fails: the process ends before it answers initialize. Have runMCP
// hand the server the launch root's source only where that root was refused at
// startup, and the two rows removing their folder fail, each refusal naming this
// server's launch.
func TestAnUnusableWorkspaceRootIsServedAsTheRefusalOverStandardInputAndOutput(t *testing.T) {
	t.Parallel()
	bin := buildServeBinary(t)
	missing := filepath.Join(t.TempDir(), "no-such-folder")
	const unfilled = "${user_config.workspace_root}"
	// The approved wording, written out rather than composed, so the sentence a
	// desktop user reads is what this row holds.
	const byVariable = "AGGREGA_WORKSPACE_ROOT — the variable the desktop application's Workspace folder " +
		"setting sets —"
	// existing stands, in a row, for a folder the row creates before the launch
	// and removes once initialize is answered.
	const existing = "<a folder removed once the server has started>"

	for _, row := range []struct {
		name string
		// flags follow `mcp` on the command line; variable is AGGREGA_WORKSPACE_ROOT,
		// empty for none.
		flags          []string
		variable       string
		given, opening string
	}{
		{name: "the variable names a folder that is not there",
			variable: missing, given: missing, opening: byVariable},
		{name: "the variable carries a placeholder its client never filled",
			variable: unfilled, given: unfilled, opening: byVariable},
		{name: "--transport stdio, and the variable names a folder that is not there",
			flags: []string{"--transport", "stdio"}, variable: missing, given: missing, opening: byVariable},
		{name: "--workspace-root names a folder that is not there",
			flags: []string{"--workspace-root", missing}, given: missing, opening: "--workspace-root"},
		{name: "the variable names a folder removed once the server has started",
			variable: existing, given: existing, opening: byVariable},
		{name: "--workspace-root names a folder removed once the server has started",
			flags: []string{"--workspace-root", existing}, given: existing, opening: "--workspace-root"},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			variable, flags, given := row.variable, slices.Clone(row.flags), row.given
			removedAfterStart := given == existing
			if removedAfterStart {
				given = filepath.Join(t.TempDir(), "workspace")
				if err := os.Mkdir(given, 0o755); err != nil {
					t.Fatalf("create %s: %v", given, err)
				}
				if variable == existing {
					variable = given
				}
				for i := range flags {
					if flags[i] == existing {
						flags[i] = given
					}
				}
			}
			want := row.opening + ` names the workspace root "` + given + `", which cannot be used: `

			cmd := exec.Command(bin, append([]string{"mcp"}, flags...)...)
			cmd.Dir = t.TempDir() // the client's own folder, which no workspace resolves from
			cmd.Env = builtBinaryEnv(t, "AGGREGA_OFFLINE=1", workspaceconfig.EnvWorkspaceRoot+"="+variable)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatalf("stdin pipe: %v", err)
			}
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatalf("stdout pipe: %v", err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatalf("start aggrega mcp: %v", err)
			}
			lines := make(chan string, 64)
			go func() {
				scanner := bufio.NewScanner(stdout)
				scanner.Buffer(make([]byte, 0, 1<<20), 64<<20)
				for scanner.Scan() {
					lines <- scanner.Text()
				}
				close(lines)
			}()

			var transcript strings.Builder
			fail := func(format string, args ...any) {
				t.Helper()
				_ = stdin.Close()
				_ = cmd.Process.Kill()
				for line := range lines {
					transcript.WriteString(line + "\n")
				}
				_ = cmd.Wait()
				t.Fatalf(format+"\nstdout:\n%s\nstderr:\n%s", append(args, transcript.String(), stderr.String())...)
			}
			send := func(line string) {
				t.Helper()
				if _, err := stdin.Write([]byte(line)); err != nil {
					fail("write %q to the server: %v", line, err)
				}
			}
			answered := map[int]bool{}
			await := func(ids ...int) {
				t.Helper()
				// A liveness ceiling, not a budget: it separates an answer from none.
				deadline := time.After(2 * time.Minute)
				for {
					pending := false
					for _, id := range ids {
						pending = pending || !answered[id]
					}
					if !pending {
						return
					}
					select {
					case line, open := <-lines:
						if !open {
							fail("the server ended its output with ids %v not all answered", ids)
						}
						transcript.WriteString(line + "\n")
						var frame struct {
							ID int `json:"id"`
						}
						if json.Unmarshal([]byte(line), &frame) == nil {
							answered[frame.ID] = true
						}
					case <-deadline:
						fail("ids %v were not all answered", ids)
					}
				}
			}

			send(rpcLine(1, "initialize", `{"protocolVersion":"2025-06-18"}`))
			await(1)
			if removedAfterStart {
				if err := os.Remove(given); err != nil {
					fail("remove %s once the server has started: %v", given, err)
				}
			}
			send(rpcLine(2, "tools/call", `{"name":"validate","arguments":{"release":"release.yaml"}}`))
			send(rpcLine(3, "resources/read", `{"uri":"catalog://component/core/compute/api-server"}`))
			send(rpcLine(4, "tools/list", ""))
			await(2, 3, 4)
			send(rpcLine(5, "tools/call", `{"name":"`+mcpCommandToolName("aggrega validate")+`","arguments":{}}`))
			await(5)

			if err := stdin.Close(); err != nil {
				fail("close the server's input: %v", err)
			}
			// The same liveness ceiling: a server that outlives its input fails here
			// rather than holding the test open.
			deadline := time.After(2 * time.Minute)
			for open := true; open; {
				select {
				case line, more := <-lines:
					if open = more; more {
						transcript.WriteString(line + "\n")
					}
				case <-deadline:
					fail("the server did not end its output once its input closed")
				}
			}
			if err := cmd.Wait(); err != nil {
				t.Fatalf("aggrega mcp did not exit cleanly once its input closed: %v\nstderr:\n%s", err, stderr.String())
			}
			answers := answersByID(t, transcript.String(), 1, 2, 3, 4, 5)

			if a := answers[1]; a.Error != nil || !strings.Contains(string(a.Result), `"serverInfo"`) {
				t.Errorf("initialize was answered %s %+v; want the handshake's result", a.Result, a.Error)
			}
			var listed struct {
				Tools []json.RawMessage `json:"tools"`
			}
			if a := answers[4]; a.Error != nil || json.Unmarshal(a.Result, &listed) != nil || len(listed.Tools) == 0 {
				t.Errorf("tools/list was answered %s %+v; want the tool list", a.Result, a.Error)
			}
			for _, id := range []int{2, 5} {
				var call struct {
					IsError bool `json:"isError"`
					Content []struct {
						Text string `json:"text"`
					} `json:"content"`
				}
				a := answers[id]
				if a.Error != nil || json.Unmarshal(a.Result, &call) != nil || !call.IsError || len(call.Content) == 0 ||
					!strings.Contains(call.Content[0].Text, want) {
					t.Errorf("tool call %d was answered %s %+v; want a tool error carrying %q", id, a.Result, a.Error, want)
				}
			}
			if a := answers[3]; a.Error == nil || !strings.Contains(a.Error.Message, want) {
				t.Errorf("the catalog read was answered %s %+v; want an error carrying %q", a.Result, a.Error, want)
			}
			first, _, _ := strings.Cut(stderr.String(), "\n")
			if opens := strings.HasPrefix(first, want); opens == removedAfterStart {
				t.Errorf("the server's stderr opens with %q; that it opens with the refusal %q is %v, want %v",
					first, want, opens, !removedAfterStart)
			}
		})
	}
}
