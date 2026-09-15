// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// cliHelpParityCeiling separates "the server answered" from "the server never
// will". It is a LIVENESS CEILING and not a performance budget: nothing here
// measures how long a read takes, only that the exchange finishes at all.
const cliHelpParityCeiling = 5 * time.Minute

// TestCliHelpResourceMatchesTheCommandsOwnHelp is the parity proof for the
// cli:// resource family: for every command in the live tree, the text the
// resource serves is byte-identical to the text that command prints for --help.
//
// Both arms run the BUILT BINARY, which is what makes this a proof rather than a
// comparison of two things this test assembled. The authority arm spawns the
// binary once per command with --help and keeps its standard output. The arm
// under test spawns the binary once as the server and reads cli://<command> for
// each of the same commands over the protocol, so what is compared is what a
// connected client receives, through the production wiring, rather than a
// renderer called directly.
//
// The command list is derived from the live tree rather than written down: a
// list beside the thing it lists goes stale, and a parity test over a stale list
// reports success for commands it never looked at. The tree is initialised the
// way an invocation initialises it, so the commands cobra contributes at run
// time — help, completion and the shells beneath it — are in the list too.
//
// One command is deliberately outside it. The hidden completion-protocol command
// is added by an unexported initialiser this test cannot call, and its name
// begins with an underscore, which the resource URI's own token rule rejects — so
// it is unreachable as a cli:// resource and there is nothing to compare.
//
// The spawns are sequential. Concurrent subprocesses in this package multiply
// peak memory past its budget, and nothing here needs them to overlap.
func TestCliHelpResourceMatchesTheCommandsOwnHelp(t *testing.T) {
	started := time.Now()
	withCLIRegistrations(t, nil)
	bin := buildServeBinary(t)
	// One working directory for both arms, holding no workspace, so each arm
	// builds the same command tree: the binary registers a command per
	// configured CLI plugin at tree construction, read from the workspace that
	// resolves where it was started.
	dir := t.TempDir()

	paths := cliHelpParityPaths(t)
	t.Logf("commands derived from the live tree: %d", len(paths))
	assertTheBinarysOwnListingIsCovered(t, bin, dir, paths)

	served := readCliHelpOverStdio(t, bin, dir, paths)

	for _, path := range paths {
		uri := cliHelpURI(path)
		got, answered := served[uri]
		if !answered {
			t.Errorf("%s: the server answered nothing for %q", commandLabel(path), uri)
			continue
		}
		want := commandOwnHelp(t, bin, dir, path)
		if got == want {
			continue
		}
		at := firstDifference(want, got)
		t.Errorf("%s: %q is not the help the command prints.\nfirst difference at byte %d\n"+
			"  command prints: %q\n  resource serves: %q",
			commandLabel(path), uri, at, window(want, at), window(got, at))
	}
	t.Logf("elapsed: %s", time.Since(started).Round(time.Millisecond))
}

// TestTheHelpRendererSurvivesConcurrentReads drives the renderer the server is
// handed from several goroutines at once over one command tree.
//
// Rendering mutates that tree: it installs a writer on the command being
// rendered, because the writer lookup walks up from there, and the tree merges a
// command's inherited flags the first time it is asked for them. Requests are
// answered on their own goroutines, so two reads arriving together meet exactly
// that. The assertion is that every concurrent render equals the one taken
// serially first — a renderer that did not serialise would hand one goroutine
// the bytes the other had not yet collected — and running it under the race
// detector is what catches the unsynchronised writes themselves.
func TestTheHelpRendererSurvivesConcurrentReads(t *testing.T) {
	t.Parallel()
	withCLIRegistrations(t, nil)
	render := commandHelp(executingRootCmd())
	if render == nil {
		t.Fatal("the command layer supplied no renderer")
	}

	paths := [][]string{{}, {"build"}, {"validate"}, {"plugin", "gen-catalog"}, {"cache", "status"}}
	serial := make([]string, len(paths))
	for i, path := range paths {
		text, ok := render(path)
		if !ok || text == "" {
			t.Fatalf("%s: the renderer produced nothing, so the comparison below is empty",
				commandLabel(path))
		}
		serial[i] = text
	}

	var wg sync.WaitGroup
	for round := 0; round < 8; round++ {
		for i, path := range paths {
			wg.Add(1)
			go func(i int, path []string) {
				defer wg.Done()
				text, ok := render(path)
				if !ok {
					t.Errorf("%s: a concurrent render reported no such command", commandLabel(path))
					return
				}
				if text != serial[i] {
					at := firstDifference(serial[i], text)
					t.Errorf("%s: a concurrent render differs from the serial one at byte %d:\n"+
						"  serial:     %q\n  concurrent: %q",
						commandLabel(path), at, window(serial[i], at), window(text, at))
				}
			}(i, path)
		}
	}
	wg.Wait()
}

// cliHelpParityPaths derives one command path per command in the live tree,
// each as the command's own name segments with the binary's name dropped.
//
// The guards below are what stop this reporting success over a list it never
// filled. A comparison across an empty or truncated list passes and proves
// nothing, and it fails silently in the direction that reads as health.
func cliHelpParityPaths(t *testing.T) [][]string {
	t.Helper()
	root := executingRootCmd()
	var paths [][]string
	for _, cmd := range reachableCommands(root) {
		segments := strings.Fields(cmd.CommandPath())
		if len(segments) == 0 {
			t.Fatalf("a command in the tree reports an empty path")
		}
		paths = append(paths, segments[1:])
	}
	if len(paths) == 0 {
		t.Fatal("the tree walk yielded no commands, so the parity comparison is empty")
	}

	have := map[string]bool{}
	for _, path := range paths {
		have[strings.Join(path, " ")] = true
	}
	// The root is the empty path, and it is the one the cli:// entry point reads.
	if !have[""] {
		t.Error("the walk did not yield the top-level command")
	}
	// help and completion exist only in a tree that has been initialised the way
	// an invocation initialises one, so their presence is what proves those two
	// calls took effect rather than being assumed.
	for _, want := range []string{"help", "completion", "completion bash"} {
		if !have[want] {
			t.Errorf("the walk did not yield %q; the tree was not initialised the way the "+
				"binary initialises the one it runs, so the list is short by every command "+
				"cobra contributes at run time", want)
		}
	}
	// Anchors at each shape: a leaf, a grouping command, and a command two levels
	// deep, so a walk that reached only the first level is visible here.
	for _, want := range []string{"build", "plugin", "plugin gen-catalog", "cache status", "auth token"} {
		if !have[want] {
			t.Errorf("the walk did not yield %q", want)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
	return paths
}

// assertTheBinarysOwnListingIsCovered checks the derived list against the
// commands the binary itself lists, and fails loudly on a divergence.
//
// The two can only diverge in one direction that matters. Every command the walk
// has but the binary does not fails by itself, because --help on a command the
// binary lacks exits non-zero. A command the BINARY has and the walk does not is
// the silent one: it is simply never compared, and the parity run stays green
// over a command nobody looked at. The class in play is a command registered per
// configured CLI plugin, which is registered at the top level, which is the level
// the binary's own listing names.
//
// Only listed-implies-walked is asserted. The other direction would need this
// test to reproduce the rule deciding which commands a help block lists, and a
// second copy of that rule is a copy that drifts.
func assertTheBinarysOwnListingIsCovered(t *testing.T, bin, dir string, paths [][]string) {
	t.Helper()
	topLevel := map[string]bool{}
	for _, path := range paths {
		if len(path) == 1 {
			topLevel[path[0]] = true
		}
	}

	listed := listedCommandNames(commandOwnHelp(t, bin, dir, nil))
	if len(listed) == 0 {
		t.Fatal("the binary's top-level help lists no commands, so this check reads nothing")
	}
	for _, name := range listed {
		if !topLevel[name] {
			t.Errorf("the binary lists a top-level command %q that the derived list does not "+
				"carry: the two arms are looking at different command trees, and every command "+
				"only one of them has is compared by nothing", name)
		}
	}
	t.Logf("top-level commands the binary lists: %d", len(listed))
}

// listedCommandNames reads the command names out of a help block's available-
// commands section: the indented entries following that heading, up to the first
// blank line, each naming its command first.
func listedCommandNames(help string) []string {
	var names []string
	inSection := false
	for _, line := range strings.Split(help, "\n") {
		if strings.HasSuffix(strings.TrimSpace(line), "Available Commands:") {
			inSection = true
			continue
		}
		if !inSection {
			continue
		}
		if strings.TrimSpace(line) == "" {
			break
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			break
		}
		names = append(names, fields[0])
	}
	return names
}

// commandOwnHelp is the authority arm: the binary run once for one command with
// --help, and what it printed to standard output.
func commandOwnHelp(t *testing.T, bin, dir string, path []string) string {
	t.Helper()
	args := append(append([]string{}, path...), "--help")
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = cliHelpParityEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s: %v --help exited %v (stderr: %s)", commandLabel(path), args, err, stderr.String())
	}
	if stdout.Len() == 0 {
		t.Fatalf("%s: --help printed nothing to standard output", commandLabel(path))
	}
	return stdout.String()
}

// readCliHelpOverStdio is the arm under test: one server process, spoken to over
// its own transport, asked for every command's help resource.
//
// Every request is written and the input then closed, which is what ends the
// run. Requests are answered on their own goroutines, so the answers can arrive
// in any order and are matched back by request id rather than by position.
func readCliHelpOverStdio(t *testing.T, bin, dir string, paths [][]string) map[string]string {
	t.Helper()
	cmd := exec.Command(bin, "mcp")
	cmd.Dir = dir
	cmd.Env = cliHelpParityEnv()
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

	var requests strings.Builder
	requests.WriteString(rpcLine(1, "initialize", `{"protocolVersion":"2025-06-18"}`))
	uriByID := make(map[int]string, len(paths))
	for i, path := range paths {
		id := i + 2
		uri := cliHelpURI(path)
		uriByID[id] = uri
		requests.WriteString(rpcLine(id, "resources/read", `{"uri":"`+uri+`"}`))
	}
	if _, err := io.WriteString(stdin, requests.String()); err != nil {
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
	case <-time.After(cliHelpParityCeiling):
		_ = cmd.Process.Kill()
		t.Fatalf("the server did not finish within the liveness ceiling; it answered %d line(s) "+
			"(stderr: %s)", len(strings.Split(strings.TrimSpace(stdout.String()), "\n")), stderr.String())
	}

	out := make(map[string]string, len(paths))
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var frame struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			t.Fatalf("the server wrote a line that is not a protocol frame: %q: %v", line, err)
		}
		uri, addressed := uriByID[frame.ID]
		if !addressed {
			continue
		}
		if frame.Error != nil {
			t.Errorf("resources/read %s: error %d: %s", uri, frame.Error.Code, frame.Error.Message)
			continue
		}
		var result struct {
			Contents []struct {
				URI  string `json:"uri"`
				Text string `json:"text"`
			} `json:"contents"`
		}
		if err := json.Unmarshal(frame.Result, &result); err != nil {
			t.Fatalf("resources/read %s returned an unreadable result: %v", uri, err)
		}
		if len(result.Contents) != 1 {
			t.Errorf("resources/read %s returned %d contents, want 1", uri, len(result.Contents))
			continue
		}
		if result.Contents[0].URI != uri {
			t.Errorf("resources/read %s answered for %q", uri, result.Contents[0].URI)
		}
		out[uri] = result.Contents[0].Text
	}
	return out
}

// cliHelpParityEnv is the environment both arms run under, so each builds the
// same command tree. It is the test process's own, which a hermetic entry point
// has already pointed at throwaway per-user homes, with the offline posture
// added: the server acquires its documentation bundle at startup, and this proof
// has nothing to do with a registry.
func cliHelpParityEnv() []string {
	return append(os.Environ(), "AGGREGA_OFFLINE=1")
}

// cliHelpURI is the resource URI addressing one command's help.
func cliHelpURI(path []string) string { return "cli://" + strings.Join(path, "/") }

// commandLabel names a command the way a reader invokes it, so a failure says
// which command it is about.
func commandLabel(path []string) string {
	if len(path) == 0 {
		return "aggrega"
	}
	return "aggrega " + strings.Join(path, " ")
}

// firstDifference is the byte index at which two strings first differ, or the
// length of the shorter when one is a prefix of the other.
func firstDifference(a, b string) int {
	limit := min(len(a), len(b))
	for i := 0; i < limit; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return limit
}

// window is the region of s around index at, so a failure shows the difference
// rather than two whole help blocks.
func window(s string, at int) string {
	const span = 80
	start := max(at-span/2, 0)
	end := min(at+span, len(s))
	out := s[start:end]
	if start > 0 {
		out = "…" + out
	}
	if end < len(s) {
		out += "…"
	}
	return out
}
