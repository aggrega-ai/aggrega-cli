// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The proofs in this file drive the compiled binary through the rule that
// decides which transport `aggrega mcp` speaks: --transport and mcp.transport
// choose it, standard input and output by default, and --listen and mcp.listen
// say only where a listener binds.
//
// Every row is read off what the process did rather than off a value it
// resolved. An unset flag and a flag set to its zero value carry the same bits,
// so the only thing separating "fell through to the config key" from "forced a
// value over it" is where the process listened, or whether it answered on its
// own streams. Addresses are Unix sockets in a short temporary directory, so
// whether one was bound is a file's existence rather than a port's state.
//
// A refusal is read two ways, and each covers what the other cannot. After the
// process has ended, no socket the invocation named exists — the reading the
// listener's own refusal tests take. And its error stream carries no startup
// line: the listener writes that line as the first thing it does, before any
// refusal of its own, so a stream without one is a launch that never reached
// the listener at all. That second reading is what separates a refusal made
// before anything binds from a listener that bound, refused and closed its
// socket again between two looks at the directory.

// mcpPing is one request a server speaking over its own streams answers with an
// empty result, which is all a row needs to show that it spoke the protocol
// there.
const mcpPing = `{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n"

// mcpRun is one launch of the built binary as `aggrega mcp`, with both output
// streams written to files so they are read whole once the process has ended.
//
// Files rather than pipes because a pipe is drained by a goroutine the process
// handle owns, which makes "what had been written when the socket appeared" a
// race; a file the child writes into directly holds every byte the moment it is
// written.
type mcpRun struct {
	cmd     *exec.Cmd
	stdout  string
	stderr  string
	done    chan struct{}
	waitErr error
}

// launchMCP starts the built binary in workspace with the given input on its
// standard input, which it reaches the end of once input is consumed.
//
// The cleanup asks the process to stop the way a supervisor does, and ends it
// outright only after the liveness ceiling, so a row that fails leaves nothing
// running behind it.
func launchMCP(t *testing.T, workspace, input string, args ...string) *mcpRun {
	t.Helper()
	bin := buildServeBinary(t)
	dir := t.TempDir()
	r := &mcpRun{
		stdout: filepath.Join(dir, "stdout"),
		stderr: filepath.Join(dir, "stderr"),
		done:   make(chan struct{}),
	}
	stdout, err := os.Create(r.stdout)
	if err != nil {
		t.Fatalf("create the standard output file: %v", err)
	}
	stderr, err := os.Create(r.stderr)
	if err != nil {
		t.Fatalf("create the standard error file: %v", err)
	}
	r.cmd = exec.Command(bin, append([]string{"mcp"}, args...)...)
	r.cmd.Dir = workspace
	r.cmd.Env = builtBinaryEnv(t)
	r.cmd.Stdin = strings.NewReader(input)
	r.cmd.Stdout = stdout
	r.cmd.Stderr = stderr
	startErr := r.cmd.Start()
	// The child holds descriptors of its own once it has started, so these are
	// the parent's copies and nothing reads through them.
	_ = stdout.Close()
	_ = stderr.Close()
	if startErr != nil {
		t.Fatalf("start the server: %v", startErr)
	}
	go func() {
		r.waitErr = r.cmd.Wait()
		close(r.done)
	}()
	t.Cleanup(func() { r.stop(t) })
	return r
}

// stop ends the process if it is still running.
func (r *mcpRun) stop(t *testing.T) {
	select {
	case <-r.done:
		return
	default:
	}
	_ = r.cmd.Process.Signal(os.Interrupt)
	select {
	case <-r.done:
	case <-time.After(responseLivenessCeiling(t)):
		_ = r.cmd.Process.Kill()
		<-r.done
	}
}

// awaitEnd waits for the launch to end on its own and returns "" when it does.
//
// While it waits it watches the given socket paths, and the first one it finds
// bound is returned with the process stopped there. A launch that was meant to
// refuse, or to speak over its own streams, and listens instead would otherwise
// be read as a hang at the liveness ceiling; this reports it as what it did, at
// once, naming the address.
func (r *mcpRun) awaitEnd(t *testing.T, watch ...string) (bound string) {
	t.Helper()
	deadline := time.Now().Add(responseLivenessCeiling(t))
	for {
		select {
		case <-r.done:
			return ""
		default:
		}
		for _, path := range watch {
			if _, err := os.Stat(path); err == nil {
				r.stop(t)
				return path
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the launch neither ended nor bound a watched address within the liveness ceiling; "+
				"its error stream reads:\n%s", r.stderrText(t))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// awaitListening waits until something answers a dial on the socket at path,
// and fails naming what the process wrote if it ended first instead.
func (r *mcpRun) awaitListening(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(responseLivenessCeiling(t))
	for {
		select {
		case <-r.done:
			t.Fatalf("the launch ended, exit %d, without listening at %s; its error stream reads:\n%s",
				r.cmd.ProcessState.ExitCode(), path, r.stderrText(t))
		default:
		}
		if conn, err := net.Dial("unix", path); err == nil {
			_ = conn.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("nothing was listening at %s within the liveness ceiling", path)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (r *mcpRun) stderrText(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(r.stderr)
	if err != nil {
		t.Fatalf("read the error stream: %v", err)
	}
	return string(raw)
}

func (r *mcpRun) stdoutText(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(r.stdout)
	if err != nil {
		t.Fatalf("read the output stream: %v", err)
	}
	return string(raw)
}

// startupLine returns the listener's startup line from an error stream, decoded,
// and whether there was one.
func startupLine(t *testing.T, stream string) (map[string]any, bool) {
	t.Helper()
	sc := bufio.NewScanner(strings.NewReader(stream))
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var fields map[string]any
		if err := json.Unmarshal([]byte(line), &fields); err != nil {
			continue
		}
		if fields["event"] == "startup" {
			return fields, true
		}
	}
	return nil, false
}

// linesContaining returns every line of stream that contains fragment.
func linesContaining(stream, fragment string) []string {
	var out []string
	for _, line := range strings.Split(stream, "\n") {
		if strings.Contains(line, fragment) {
			out = append(out, line)
		}
	}
	return out
}

// wantAnsweredOverTheStreams asserts the launch ended on its own, with success,
// having answered the ping on standard output, and having started no listener.
// watch names the sockets it must not have bound on the way.
func wantAnsweredOverTheStreams(t *testing.T, r *mcpRun, watch ...string) {
	t.Helper()
	if bound := r.awaitEnd(t, watch...); bound != "" {
		t.Fatalf("the invocation listened at %s instead of speaking over its own streams; its error stream "+
			"reads:\n%s", bound, r.stderrText(t))
	}
	if code := r.cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("the invocation ended with exit %d rather than answering; its error stream reads:\n%s",
			code, r.stderrText(t))
	}
	first, _, _ := strings.Cut(r.stdoutText(t), "\n")
	var frame map[string]any
	if err := json.Unmarshal([]byte(first), &frame); err != nil {
		t.Fatalf("standard output's first line is not a protocol message, so nothing spoke the protocol "+
			"there: %q", first)
	}
	if _, answered := frame["result"]; !answered {
		t.Fatalf("the answer on standard output carries no result: %v", frame)
	}
	if line, started := startupLine(t, r.stderrText(t)); started {
		t.Errorf("the error stream carries a listener's startup line, so a listener was started: %v", line)
	}
	for _, path := range watch {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("%s exists, so the invocation bound it", path)
		}
	}
}

// wantRefusedBeforeAnythingBinds asserts the launch ended on its own with a
// failure whose text carries every fragment, having reached no listener and
// bound none of the watched sockets.
func wantRefusedBeforeAnythingBinds(t *testing.T, r *mcpRun, fragments []string, watch ...string) {
	t.Helper()
	if bound := r.awaitEnd(t, watch...); bound != "" {
		t.Fatalf("the invocation listened at %s instead of being refused; its error stream reads:\n%s",
			bound, r.stderrText(t))
	}
	stderr := r.stderrText(t)
	if code := r.cmd.ProcessState.ExitCode(); code == 0 {
		t.Fatalf("the invocation ended with success rather than a refusal; its error stream reads:\n%s", stderr)
	}
	for _, fragment := range fragments {
		if !strings.Contains(stderr, fragment) {
			t.Errorf("the refusal does not name %q, so a reader cannot act on it; the error stream reads:\n%s",
				fragment, stderr)
		}
	}
	if line, started := startupLine(t, stderr); started {
		t.Errorf("the error stream carries a listener's startup line, so the refusal was made after the "+
			"listener was entered rather than before anything binds: %v", line)
	}
	for _, path := range watch {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("%s exists, so the address was bound before the refusal", path)
		}
	}
	if out := r.stdoutText(t); out != "" {
		t.Errorf("a refused invocation wrote to standard output: %q", out)
	}
}

// wantListeningAt asserts the launch is listening at want, has bound none of
// the other watched sockets, and reported the HTTP transport on its startup
// line.
//
// The absence is read once, after want is answering: the process has finished
// choosing by then, so an address it has not bound is one it never will.
func wantListeningAt(t *testing.T, r *mcpRun, want string, others ...string) {
	t.Helper()
	r.awaitListening(t, want)
	for _, other := range others {
		wantNothingListening(t, other, "the invocation was to listen at "+want+" and nowhere else")
	}
	line, started := startupLine(t, r.stderrText(t))
	if !started {
		t.Fatalf("the listener at %s wrote no startup line; its error stream reads:\n%s", want, r.stderrText(t))
	}
	// The value an operator configures and the word the process reports it is
	// speaking are compared here, over the running process, rather than by a
	// comment saying they agree.
	if line["transport"] != "http" {
		t.Errorf("the startup line reports the transport %v, want %q, the value the configuration and the "+
			"flag name it by", line["transport"], "http")
	}
}

// TestTheTransportIsTheFlagThenTheConfigKeyThenStdio drives the precedence of
// the transport itself: nothing typed or configured is standard input and
// output, mcp.transport chooses HTTP, and a typed --transport wins over it.
func TestTheTransportIsTheFlagThenTheConfigKeyThenStdio(t *testing.T) {
	t.Parallel()
	t.Run("nothing typed or configured speaks over standard input and output", func(t *testing.T) {
		r := launchMCP(t, makeMCPWorkspace(t, ""), mcpPing)
		wantAnsweredOverTheStreams(t, r)
		if lines := linesContaining(r.stderrText(t), "is not used"); len(lines) != 0 {
			t.Errorf("with no address configured the error stream still says one is unused: %q", lines)
		}
	})

	t.Run("mcp.transport http opens a listener at mcp.listen", func(t *testing.T) {
		socket := filepath.Join(mcpSocketDir(t), "s")
		ws := makeMCPWorkspace(t, "mcp:\n  transport: http\n  listen: unix:"+socket+"\n"+mcpDeclaredDeployment)
		wantListeningAt(t, launchMCP(t, ws, ""), socket)
	})

	t.Run("a typed --transport stdio wins over a configured mcp.transport http", func(t *testing.T) {
		socket := filepath.Join(mcpSocketDir(t), "s")
		ws := makeMCPWorkspace(t, "mcp:\n  transport: http\n  listen: unix:"+socket+"\n"+mcpDeclaredDeployment)
		wantAnsweredOverTheStreams(t, launchMCP(t, ws, mcpPing, "--transport", "stdio"), socket)
	})
}

// TestTheHTTPTransportTakesItsAddressFromTheFlagThenTheConfigKey drives where a
// listener binds once the HTTP transport is chosen: --listen, else mcp.listen,
// and with neither a refusal naming both.
func TestTheHTTPTransportTakesItsAddressFromTheFlagThenTheConfigKey(t *testing.T) {
	t.Parallel()
	t.Run("--listen wins over mcp.listen", func(t *testing.T) {
		dir := mcpSocketDir(t)
		configured := filepath.Join(dir, "config")
		requested := filepath.Join(dir, "flag")
		ws := makeMCPWorkspace(t, "mcp:\n  listen: unix:"+configured+"\n"+mcpDeclaredDeployment)
		r := launchMCP(t, ws, "", "--transport", "http", "--listen", "unix:"+requested)
		wantListeningAt(t, r, requested, configured)
	})

	t.Run("mcp.listen is read when --listen is not typed", func(t *testing.T) {
		socket := filepath.Join(mcpSocketDir(t), "s")
		ws := makeMCPWorkspace(t, "mcp:\n  listen: unix:"+socket+"\n"+mcpDeclaredDeployment)
		wantListeningAt(t, launchMCP(t, ws, "", "--transport", "http"), socket)
	})

	// The health address is configured so that there is a socket this launch
	// could bind: a listener opens it before it asks whether it has a workspace to
	// serve, so a refusal made inside the listener rather than before it would be
	// seen binding one.
	for _, tc := range []struct {
		name  string
		block string
		args  []string
		chose string
	}{
		{
			name:  "typed --transport http with no address anywhere",
			block: "",
			args:  []string{"--transport", "http"},
			chose: "--transport http",
		},
		{
			name:  "configured mcp.transport http with no address anywhere",
			block: "  transport: http\n",
			chose: "mcp.transport: http",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			health := filepath.Join(mcpSocketDir(t), "health")
			ws := makeMCPWorkspace(t, "mcp:\n"+tc.block+"  health:\n    listen: unix:"+health+"\n"+
				mcpDeclaredDeployment)
			r := launchMCP(t, ws, "", tc.args...)
			wantRefusedBeforeAnythingBinds(t, r, []string{"--listen", "mcp.listen", tc.chose}, health)
		})
	}
}

// TestAListenAddressTypedUnderStdioIsRefused drives a typed --listen under the
// standard-input-and-output transport, however that transport was chosen.
//
// The default-case row is its own row rather than a third variant of the other
// two, because its refusal says something they do not: nothing chose a
// transport, and --transport http is what would.
func TestAListenAddressTypedUnderStdioIsRefused(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		block     string
		args      []string
		fragments []string
	}{
		{
			name:      "stdio typed as --transport stdio",
			args:      []string{"--transport", "stdio"},
			fragments: []string{"--listen", "--transport stdio", "--transport http"},
		},
		{
			name:      "stdio configured as mcp.transport stdio",
			block:     "  transport: stdio\n",
			fragments: []string{"--listen", "mcp.transport: stdio", "--transport http"},
		},
		{
			name:      "stdio by default, with no transport typed or configured",
			fragments: []string{"--listen", "no transport was chosen", "--transport http"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			socket := filepath.Join(mcpSocketDir(t), "s")
			ws := makeMCPWorkspace(t, "mcp:\n"+tc.block+mcpDeclaredDeployment)
			r := launchMCP(t, ws, "", append(tc.args, "--listen", "unix:"+socket)...)
			wantRefusedBeforeAnythingBinds(t, r, tc.fragments, socket)
		})
	}
}

// TestAConfiguredAddressUnderStdioIsLeftUnusedAndSaidSo drives the one
// combination that is neither refused nor silent: an address only the
// configuration names, under the standard-input-and-output transport. The
// server answers over its own streams, binds nothing, and says once on its
// error stream that the address is not used and which setting would use it.
//
// The row with no configured address, in the precedence test above, is this
// one's control: it carries no such line.
func TestAConfiguredAddressUnderStdioIsLeftUnusedAndSaidSo(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		block string
		args  []string
	}{
		{name: "stdio by default"},
		{name: "stdio configured", block: "  transport: stdio\n"},
		{name: "stdio typed over a configured http", block: "  transport: http\n",
			args: []string{"--transport", "stdio"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			socket := filepath.Join(mcpSocketDir(t), "s")
			ws := makeMCPWorkspace(t, "mcp:\n"+tc.block+"  listen: unix:"+socket+"\n"+mcpDeclaredDeployment)
			r := launchMCP(t, ws, mcpPing, tc.args...)
			wantAnsweredOverTheStreams(t, r, socket)

			lines := linesContaining(r.stderrText(t), "is not used")
			if len(lines) != 1 {
				t.Fatalf("the error stream carries %d lines saying the address is not used, want exactly one: %q",
					len(lines), lines)
			}
			for _, fragment := range []string{"mcp.listen", "unix:" + socket, "mcp.transport: http"} {
				if !strings.Contains(lines[0], fragment) {
					t.Errorf("the line saying the address is not used does not name %q: %q", fragment, lines[0])
				}
			}
		})
	}
}

// TestAnEmptyListenAddressIsRefusedWhateverTheTransport drives a typed --listen
// carrying no address, in every combination it can meet. It is refused as naming
// no address rather than falling through to mcp.listen, which the rows that
// configure one would show by binding it, and rather than choosing a transport.
func TestAnEmptyListenAddressIsRefusedWhateverTheTransport(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		block string
		args  []string
	}{
		{name: "stdio by default"},
		{name: "stdio typed", args: []string{"--transport", "stdio"}},
		{name: "stdio configured", block: "  transport: stdio\n"},
		{name: "http typed over a configured address", args: []string{"--transport", "http"}},
		{name: "http configured over a configured address", block: "  transport: http\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			socket := filepath.Join(mcpSocketDir(t), "s")
			ws := makeMCPWorkspace(t, "mcp:\n"+tc.block+"  listen: unix:"+socket+"\n"+mcpDeclaredDeployment)
			r := launchMCP(t, ws, "", append(tc.args, "--listen=")...)
			wantRefusedBeforeAnythingBinds(t, r, []string{"--listen", "empty address"}, socket)
		})
	}
}

// TestATransportOtherThanTheTwoIsRefused drives a value that is neither stdio
// nor http, on the flag and in a configuration file, and reads that each refusal
// names the two that are accepted.
func TestATransportOtherThanTheTwoIsRefused(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		block     string
		args      []string
		fragments []string
	}{
		{
			name:      "on the flag",
			args:      []string{"--transport", "tcp"},
			fragments: []string{`--transport was given "tcp"`, "stdio and http"},
		},
		{
			name:      "on the flag, empty",
			args:      []string{"--transport="},
			fragments: []string{`--transport was given ""`, "stdio and http"},
		},
		{
			// Refused by the configuration schema when the file loads, which
			// lists the values it admits.
			name:      "in a configuration file",
			block:     "mcp:\n  transport: tcp\n",
			fragments: []string{"mcp.transport", "tcp", "[stdio http]"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := launchMCP(t, makeMCPWorkspace(t, tc.block), "", tc.args...)
			wantRefusedBeforeAnythingBinds(t, r, tc.fragments)
		})
	}
}
