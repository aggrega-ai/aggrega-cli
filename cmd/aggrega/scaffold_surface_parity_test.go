// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// Drafting parity at the real entrypoint. Every caller-facing surface that
// carries the capability is driven as a spawned process over its own wire
// protocol — the command line over its output stream, the daemon and the tool
// protocol over theirs — and the documents they hand back are compared byte for
// byte.
//
// This is the end-to-end leg the per-package parity tests structurally cannot
// be. Each of those compares one surface against the shared drafting layer
// in-process; none observes another's wire encoding, and none would notice a
// surface that reshaped the text on its way out. Here the comparison is between
// the answers a caller actually receives.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/mcp"
)

// scaffoldParityKinds are the kinds both surfaces are asked for. They span the
// shapes the drafting walk decides differently — a nested required mapping, a
// bare required object, an extra required field, required scalars, a fixed
// value — so a surface that diverged on one shape rather than on all of them is
// still caught.
var scaffoldParityKinds = []string{
	"component", "aggregate", "release", "recipe", "manifest", "feature", "featurerecipe", "plugin",
}

// scaffoldParityName is the name both surfaces seed with, so the comparison
// covers the seeding path and not only the placeholder one.
const scaffoldParityName = "parity-drafted"

// draftViaCommandLine runs the drafting command once per kind against an empty
// workspace and returns what it printed, keyed by kind.
//
// It reads standard output alone. That stream is the whole of what this surface
// hands a caller — a redirect is how the document reaches a file — so a surface
// that decorated its answer with a banner or a trailing status line would fail
// the comparison below rather than pass it with the decoration attached.
func draftViaCommandLine(t *testing.T, bin string, kinds []string) map[string]string {
	t.Helper()

	ws := t.TempDir()
	out := map[string]string{}
	for _, kind := range kinds {
		cmd := exec.Command(bin, "scaffold", kind, scaffoldParityName)
		cmd.Dir = ws
		cmd.Env = builtBinaryEnv(t)
		var stdout, stderr strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("the command refused %q: %v (%s)", kind, err, stderr.String())
		}
		out[kind] = stdout.String()
	}
	return out
}

// draftViaDaemon spawns the daemon against an empty workspace and returns the
// skeleton it answers with for each kind, keyed by kind.
//
// The workspace is deliberately bare. The capability is answered from the schema
// set the binary carries, so a workspace holding anything at all would leave a
// reader unable to tell whether the answer depended on it.
func draftViaDaemon(t *testing.T, bin string, kinds []string) map[string]string {
	t.Helper()

	ws := t.TempDir()
	cmd := exec.Command(bin, "serve")
	cmd.Dir = ws
	cmd.Env = builtBinaryEnv(t)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("daemon stdin pipe: %v", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("daemon stdout pipe: %v", err)
	}
	stdout := bufio.NewReader(stdoutPipe)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the daemon: %v", err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	})

	ceiling := responseLivenessCeiling(t)
	out := map[string]string{}
	for i, kind := range kinds {
		if _, err := stdin.Write(framed(i+1, "aggrega/scaffoldEntity", map[string]any{
			"kind": kind,
			"name": scaffoldParityName,
		})); err != nil {
			t.Fatalf("write the daemon request for %q: %v", kind, err)
		}
		resp := readOneResponse(t, stdout, ceiling)
		if env, isErr := resp["error"]; isErr {
			t.Fatalf("the daemon refused %q: %+v", kind, env)
		}
		result, ok := resp["result"].(map[string]any)
		if !ok {
			t.Fatalf("the daemon's answer for %q carries no result object: %+v", kind, resp)
		}
		skeleton, ok := result["skeleton"].(string)
		if !ok {
			t.Fatalf("the daemon's answer for %q carries no skeleton: %+v", kind, result)
		}
		if got, _ := result["kind"].(string); got != kind {
			t.Errorf("the daemon reports kind %q for a request about %q", got, kind)
		}
		out[kind] = skeleton
	}
	return out
}

// draftViaToolProtocol spawns the tool protocol against an empty workspace and
// returns the skeleton its text block carries for each kind, keyed by kind.
//
// One process answers every kind, and the whole conversation is written before
// anything is read: the protocol is newline-delimited JSON over the process's
// own streams, so requests queue on the pipe and the replies arrive in order.
func draftViaToolProtocol(t *testing.T, bin string, kinds []string) map[string]string {
	t.Helper()

	ws := t.TempDir()
	cmd := exec.Command(bin, "mcp")
	cmd.Dir = ws
	cmd.Env = builtBinaryEnv(t)
	cmd.Stderr = os.Stderr

	var conversation strings.Builder
	conversation.WriteString(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":` +
		`{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"parity","version":"0"}}}` + "\n")
	for i, kind := range kinds {
		args, err := json.Marshal(map[string]any{"kind": kind, "name": scaffoldParityName})
		if err != nil {
			t.Fatalf("marshal the tool arguments for %q: %v", kind, err)
		}
		conversation.WriteString(fmt.Sprintf(
			`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":%s}}`+"\n",
			i+1, scaffoldToolName(t), args))
	}
	cmd.Stdin = strings.NewReader(conversation.String())

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("tool-protocol stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the tool protocol: %v", err)
	}
	defer func() { _ = cmd.Wait() }()

	byID := map[float64]map[string]any{}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("the tool protocol emitted a line that is not JSON: %v (%s)", err, line)
		}
		id, ok := msg["id"].(float64)
		if !ok {
			continue
		}
		byID[id] = msg
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read the tool protocol's output: %v", err)
	}

	out := map[string]string{}
	for i, kind := range kinds {
		msg, ok := byID[float64(i+1)]
		if !ok {
			t.Fatalf("the tool protocol answered nothing for %q", kind)
		}
		if env, isErr := msg["error"]; isErr {
			t.Fatalf("the tool protocol refused %q: %+v", kind, env)
		}
		result, ok := msg["result"].(map[string]any)
		if !ok {
			t.Fatalf("the tool protocol's answer for %q carries no result object: %+v", kind, msg)
		}
		if failed, _ := result["isError"].(bool); failed {
			t.Fatalf("the tool protocol reported a failure for %q: %+v", kind, result)
		}
		content, ok := result["content"].([]any)
		if !ok || len(content) == 0 {
			t.Fatalf("the tool protocol's answer for %q carries no content block: %+v", kind, result)
		}
		block, ok := content[0].(map[string]any)
		if !ok {
			t.Fatalf("the tool protocol's content block for %q is not an object: %+v", kind, content[0])
		}
		text, ok := block["text"].(string)
		if !ok {
			t.Fatalf("the tool protocol's content block for %q carries no text: %+v", kind, block)
		}
		out[kind] = text
	}
	return out
}

// scaffoldToolName returns the name the tool protocol advertises the drafting
// capability under in-process, read from the surface's own records rather than
// written here — a name spelled twice is a name that can drift.
//
// The operation is reachable under more than one name on that surface: the
// curated tool that derives the document in-process, and the tool derived from
// the command that now carries the same capability, which reaches it by running
// that command in a subprocess. Taking whichever name the availability record
// happens to list first would follow the derivation's ordering rather than the
// caller's intent, and it would do so silently — the wrong tool answers, with
// its own arguments and its own refusals.
//
// What separates the two is that only the curated tool exists for a session that
// was handed no command surface, which is exactly what the static tool set
// reports. So the selection is derived from that set rather than from a position
// or from a spelling written here.
func scaffoldToolName(t *testing.T) string {
	t.Helper()
	availability, ok := mcp.OperationAvailability()["scaffold-entity"]
	if !ok || !availability.IsExposed() {
		t.Fatal("the tool protocol declares no exposed name for the drafting capability")
	}
	static := map[string]bool{}
	for _, name := range mcp.StaticToolNames() {
		static[name] = true
	}
	for _, name := range availability.Names {
		if static[name] {
			return name
		}
	}
	t.Fatalf("the tool protocol claims the drafting capability under %v, and serves none of them "+
		"without a command surface", availability.Names)
	return ""
}

// TestDraftingIsByteIdenticalAcrossTheSurfacesThatCarryIt is the proof that one
// implementation does the work. Every surface asked for the same kind and the
// same name returns the same bytes; if any two differ, a second walker has
// appeared, and it will have appeared agreeing with the first on everything but
// the case that matters later.
func TestDraftingIsByteIdenticalAcrossTheSurfacesThatCarryIt(t *testing.T) {
	t.Parallel()
	bin := buildServeBinary(t)

	fromCommandLine := draftViaCommandLine(t, bin, scaffoldParityKinds)
	fromDaemon := draftViaDaemon(t, bin, scaffoldParityKinds)
	fromToolProtocol := draftViaToolProtocol(t, bin, scaffoldParityKinds)

	for _, kind := range scaffoldParityKinds {
		command, daemon, tool := fromCommandLine[kind], fromDaemon[kind], fromToolProtocol[kind]
		switch {
		case strings.TrimSpace(daemon) == "":
			t.Errorf("the daemon drafted nothing for %q, so the comparison for it asserts nothing", kind)
		case daemon != tool:
			t.Errorf("the daemon and the tool protocol draft %q differently.\ndaemon:\n%s\ntool protocol:\n%s",
				kind, daemon, tool)
		case command != daemon:
			t.Errorf("the command line and the daemon draft %q differently.\ncommand line:\n%s\ndaemon:\n%s",
				kind, command, daemon)
		case !strings.Contains(daemon, scaffoldParityName):
			t.Errorf("no surface seeded the requested name into %q, so the seeding path is untested:\n%s",
				kind, daemon)
		}
	}
}

// TestTheSurfacesRefuseAnUnknownKindInTheSameWords is the half a comparison over
// successful drafts cannot see. Surfaces can agree on every document they
// produce and still phrase the same rejection differently, which is what leaves
// a person at a terminal and an assistant correcting one typo from two different
// pieces of advice.
//
// The command's own half is asserted here too, because a refusal that printed a
// document as well would satisfy a text comparison while handing the caller an
// empty entity to redirect into a file.
func TestTheSurfacesRefuseAnUnknownKindInTheSameWords(t *testing.T) {
	t.Parallel()
	bin := buildServeBinary(t)
	const typo = "compoent"

	cmd := exec.Command(bin, "scaffold", typo)
	cmd.Dir = t.TempDir()
	cmd.Env = builtBinaryEnv(t)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	if err == nil {
		t.Fatalf("a kind no schema answers to was answered rather than refused: %q", stdout.String())
	}
	if stdout.String() != "" {
		t.Errorf("the refusal printed a document as well, which a redirect would have written:\n%s", stdout.String())
	}
	fromCommandLine := strings.TrimSpace(stderr.String())
	fromToolProtocol := refuseViaToolProtocol(t, bin, typo)
	if fromCommandLine != fromToolProtocol {
		t.Errorf("the surfaces phrase the same rejection differently.\ncommand line: %s\ntool protocol: %s",
			fromCommandLine, fromToolProtocol)
	}
	if !strings.Contains(fromCommandLine, typo) {
		t.Errorf("the refusal does not name the token that was sent: %s", fromCommandLine)
	}
}

// refuseViaToolProtocol spawns the tool protocol, asks it to draft a kind no
// schema answers to, and returns the refusal text it hands back.
func refuseViaToolProtocol(t *testing.T, bin, kind string) string {
	t.Helper()

	args, err := json.Marshal(map[string]any{"kind": kind})
	if err != nil {
		t.Fatalf("marshal the tool arguments: %v", err)
	}
	conversation := `{"jsonrpc":"2.0","id":0,"method":"initialize","params":` +
		`{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"parity","version":"0"}}}` + "\n" +
		fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":%s}}`+"\n",
			scaffoldToolName(t), args)

	cmd := exec.Command(bin, "mcp")
	cmd.Dir = t.TempDir()
	cmd.Env = builtBinaryEnv(t)
	cmd.Stdin = strings.NewReader(conversation)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run the tool protocol: %v", err)
	}

	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("the tool protocol emitted a line that is not JSON: %v (%s)", err, line)
		}
		if id, ok := msg["id"].(float64); !ok || id != 1 {
			continue
		}
		result, ok := msg["result"].(map[string]any)
		if !ok {
			t.Fatalf("the tool protocol's answer carries no result object: %+v", msg)
		}
		if failed, _ := result["isError"].(bool); !failed {
			t.Fatalf("the tool protocol answered a kind no schema answers to: %+v", result)
		}
		content, ok := result["content"].([]any)
		if !ok || len(content) == 0 {
			t.Fatalf("the tool protocol's refusal carries no content block: %+v", result)
		}
		block, ok := content[0].(map[string]any)
		if !ok {
			t.Fatalf("the tool protocol's content block is not an object: %+v", content[0])
		}
		text, ok := block["text"].(string)
		if !ok {
			t.Fatalf("the tool protocol's content block carries no text: %+v", block)
		}
		return strings.TrimSpace(text)
	}
	t.Fatal("the tool protocol answered nothing")
	return ""
}

// TestTheDaemonDraftsWithoutTouchingTheWorkspace pins at the real entrypoint
// what the reference page promises a client: the answer is a document to write,
// and nothing was written to produce it.
//
// The in-process check asserts the same property against the handler; this one
// asserts it against a spawned process, which is the only place a write from
// somewhere other than the handler — a stray output directory, a cache file —
// would show up.
func TestTheDaemonDraftsWithoutTouchingTheWorkspace(t *testing.T) {
	// Not parallel: spawns a daemon subprocess.
	bin := buildServeBinary(t)

	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "already-here.yaml"), []byte("kind: NotAggrega\n"), 0o644); err != nil {
		t.Fatalf("seed the workspace: %v", err)
	}
	before := treeSnapshot(t, ws)
	if len(before) == 0 {
		t.Fatal("the snapshot read nothing, so comparing it against itself would assert nothing")
	}

	cmd := exec.Command(bin, "serve")
	cmd.Dir = ws
	cmd.Env = builtBinaryEnv(t)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	stdout := bufio.NewReader(stdoutPipe)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the daemon: %v", err)
	}

	ceiling := responseLivenessCeiling(t)
	for i, kind := range scaffoldParityKinds {
		if _, err := stdin.Write(framed(i+1, "aggrega/scaffoldEntity", map[string]any{
			"kind": kind,
			"name": scaffoldParityName,
		})); err != nil {
			t.Fatalf("write the request for %q: %v", kind, err)
		}
		if resp := readOneResponse(t, stdout, ceiling); resp["error"] != nil {
			t.Fatalf("the daemon refused %q: %+v", kind, resp["error"])
		}
	}
	_ = stdin.Close()
	_ = cmd.Wait()

	if diff := diffSnapshots(before, treeSnapshot(t, ws)); diff != "" {
		t.Fatalf("drafting changed the workspace:\n%s", diff)
	}
}
