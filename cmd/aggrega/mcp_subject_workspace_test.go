// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// subjectToolCeiling separates "the server answered" from "the server never
// will". It is a LIVENESS CEILING and not a performance budget.
const subjectToolCeiling = 5 * time.Minute

// TestTheServersOwnPullAndItsGeneratedTwinAnswerFromTheReleasesWorkspace drives
// the built binary as `aggrega mcp`, launched with no root in a folder that
// anchors no workspace, and calls the server's own pull tool and the tool
// generated from `aggrega pull` — which runs that command in a separate process
// — over a Release named by its absolute path inside a workspace whose
// configuration gives that Release a catalog directory of its own.
//
// Each call completes only where it ran in the Release's own workspace: that
// directory is the only place the Release's Component is declared. So each
// writes the lockfile beside the Release and nothing in the folder the server
// was started in, and the two lockfiles pin the same entities at the same
// digests — the two tools answered one call from one workspace. The lockfiles
// are compared by what they pin rather than byte for byte, because each records
// the moment it was written.
//
// The binary is launched as a client launches it, with no workspace root on its
// command line or in its environment: that absence is the condition under test,
// so it is not modelled by pointing anything at a fixture.
func TestTheServersOwnPullAndItsGeneratedTwinAnswerFromTheReleasesWorkspace(t *testing.T) {
	bin := buildServeBinary(t)
	pinned := map[string][]lockfile.Entity{}
	for _, tool := range []string{"pull", "cli-pull"} {
		_, release := subjectFixture(t)
		startedIn := folderNoWorkspaceResolvesFrom(t)

		text, isError := callToolOverStdio(t, bin, startedIn, tool, map[string]any{"release": release})
		if isError {
			t.Fatalf("%s over %s was refused:\n%s", tool, release, text)
		}
		lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(release))
		if err != nil {
			t.Fatalf("%s wrote no readable lockfile beside the Release: %v\nanswer:\n%s", tool, err, text)
		}
		if len(lf.Entities) == 0 {
			t.Fatalf("%s wrote a lockfile pinning nothing, so a comparison with it would prove nothing", tool)
		}
		pinned[tool] = lf.Entities

		entries, err := os.ReadDir(startedIn)
		if err != nil {
			t.Fatalf("read %s: %v", startedIn, err)
		}
		for _, e := range entries {
			t.Errorf("%s created %s in the folder the server was started in", tool, e.Name())
		}
	}
	if !reflect.DeepEqual(pinned["pull"], pinned["cli-pull"]) {
		t.Errorf("the two tools pinned different entities for the same Release\npull:     %+v\ncli-pull: %+v",
			pinned["pull"], pinned["cli-pull"])
	}
}

// callToolOverStdio launches the built binary as the server in dir, with no
// workspace root and the offline posture — the server acquires its documentation
// bundle at startup, and nothing here concerns a registry — and calls one tool.
// It returns the answer's text and whether the answer is a refusal.
func callToolOverStdio(t *testing.T, bin, dir, tool string, args map[string]any) (string, bool) {
	t.Helper()
	params, err := json.Marshal(map[string]any{"name": tool, "arguments": args})
	if err != nil {
		t.Fatalf("encode the call: %v", err)
	}
	cmd := exec.Command(bin, "mcp")
	cmd.Dir = dir
	cmd.Env = builtBinaryEnv(t, "AGGREGA_OFFLINE=1")
	// The binary inherits this process's environment, so a root exported there
	// would be a launch root, and the launch would no longer be one naming none.
	for _, entry := range cmd.Env {
		if name, value, _ := strings.Cut(entry, "="); name == workspaceconfig.EnvWorkspaceRoot && value != "" {
			t.Fatalf("the server would be launched with %s, which names a workspace root", entry)
		}
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("open the server's input: %v", err)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the server: %v", err)
	}
	requests := rpcLine(1, "initialize", `{"protocolVersion":"2025-06-18"}`) + rpcLine(2, "tools/call", string(params))
	if _, err := io.WriteString(stdin, requests); err != nil {
		t.Fatalf("write to the server: %v", err)
	}
	if err := stdin.Close(); err != nil {
		t.Fatalf("close the server's input: %v", err)
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	select {
	case err := <-waited:
		if err != nil {
			t.Fatalf("the server exited %v (stderr: %s)", err, stderr.String())
		}
	case <-time.After(subjectToolCeiling):
		_ = cmd.Process.Kill()
		t.Fatalf("the server did not finish within the liveness ceiling (stderr: %s)", stderr.String())
	}

	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		var frame struct {
			ID     int `json:"id"`
			Result *struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
				IsError bool `json:"isError"`
			} `json:"result"`
			Error *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			t.Fatalf("the server wrote a line that is not a protocol frame: %q: %v", line, err)
		}
		if frame.ID != 2 {
			continue
		}
		if frame.Error != nil {
			t.Fatalf("tools/call %s: error %d: %s", tool, frame.Error.Code, frame.Error.Message)
		}
		if frame.Result == nil || len(frame.Result.Content) == 0 {
			t.Fatalf("tools/call %s answered with no content: %s", tool, line)
		}
		return frame.Result.Content[0].Text, frame.Result.IsError
	}
	t.Fatalf("the server answered nothing for tools/call %s (stderr: %s)", tool, stderr.String())
	return "", false
}
