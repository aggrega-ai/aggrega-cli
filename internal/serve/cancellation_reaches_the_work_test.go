// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// probeRegistryHost is the registry the seeding requests below name.
//
// It is a loopback authority nothing listens on, and deliberately so. The
// observable these tests turn on is whether the REGISTRATION happened — the entry
// in the configuration file — and not what the trust endpoint said: the fetch
// meets a connection refused at once, which the registration tolerates as a
// registry advertising nothing, so a seeding nobody withdrew writes its entry
// without waiting on any network. What a trust endpoint was asked is proved where
// an endpoint is counted: beside the registration in internal/registryops, and
// over this protocol in registry_trust_fetch_test.go.
const probeRegistryHost = "127.0.0.1:9"

// seedingProbeWorkspace stands a daemon over a real two-layer configuration
// cascade wired for a dispatch-loop run, and returns it with the workspace
// configuration file the seeding will write.
//
// The global layer is a file of this test's own, reached through a frozen lookup
// rather than through the machine's own configuration, so nothing here can read or
// write the developer's real registries. That is not tidiness: the operation
// defaults to the global file, and the workspace entry is pre-registered on the
// session so the dispatch loop answers from this resolution rather than resolving
// one off disk.
func seedingProbeWorkspace(t *testing.T, in io.Reader, out io.Writer) (*Server, string) {
	t.Helper()

	root := t.TempDir()
	globalPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(globalPath, []byte("registries: []\n"), 0o644); err != nil {
		t.Fatalf("write the global layer: %v", err)
	}
	wsPath := filepath.Join(root, workspaceconfig.ConfigFilename)
	if err := os.WriteFile(wsPath, []byte("workspace: {}\n"), 0o644); err != nil {
		t.Fatalf("write the workspace layer: %v", err)
	}
	lookup := func(k string) string {
		if k == workspaceconfig.EnvConfigOverride {
			return globalPath
		}
		return ""
	}
	rw, err := workspaceconfig.Resolve(root, root, lookup)
	if err != nil {
		t.Fatalf("resolve the probe workspace: %v", err)
	}

	srv := newPoolTestServerAt(t, root, in, out, 4, nil)
	srv.resolvedWorkspace = rw
	return srv, wsPath
}

// parkUntilCancelled holds the target request inside the dispatcher until its own
// context is cancelled, then lets it run on.
//
// This is what makes the proof deterministic rather than a race. The handler
// enters its operation with an ALREADY-cancelled context, so whether the
// operation reaches the network is a property of the threading and of nothing
// else — there is no window in which a fast machine completes the fetch before the
// cancel lands, and therefore no timing bound anywhere in the assertion.
func parkUntilCancelled(t *testing.T, srv *Server, targetID int) (entered chan struct{}) {
	t.Helper()
	entered = make(chan struct{})
	srv.testCtxHook = func(ctx context.Context, id json.RawMessage) {
		var n int
		if json.Unmarshal(id, &n) != nil || n != targetID {
			return
		}
		close(entered)
		<-ctx.Done()
	}
	return entered
}

// TestConfigInitOverTheWireWritesNothingWhenTheClientCancels is the at-the-wire
// proof that a client's cancellation reaches the work rather than only the answer.
//
// A real aggrega/configInit request goes in on the daemon's own stdin; a real
// $/cancelRequest notification carrying that id follows it through the dispatch
// loop. The observable is the FILE: a withdrawn seeding leaves the workspace
// configuration without the entry it was asked to write. That is work not done,
// readable from outside the process, and it is not a statement about the reply —
// which is dropped either way and therefore proves nothing.
//
// It is also the reason the threading is a correctness fix rather than a
// responsiveness one. An interrupted trust fetch is indistinguishable, from its own
// error, from a registry that advertises nothing; a seeding that could not observe
// the withdrawal read the interruption as "advertises nothing", proceeded, and
// wrote a configuration whose trust policy was empty because a request was cut
// short rather than because the registry declined to publish one.
func TestConfigInitOverTheWireWritesNothingWhenTheClientCancels(t *testing.T) {
	t.Parallel()

	const host = probeRegistryHost
	stdinR, stdinW := io.Pipe()
	stdout := &syncBuffer{}
	srv, wsPath := seedingProbeWorkspace(t, stdinR, stdout)

	const targetID = 11
	entered := parkUntilCancelled(t, srv, targetID)

	loopDone := make(chan error, 1)
	go func() { loopDone <- srv.dispatchLoop(context.Background(), 500*time.Millisecond) }()

	if _, err := stdinW.Write(framedRequestIn(t, srv, targetID, "aggrega/configInit", map[string]any{
		"registry":  host,
		"workspace": true,
		"force":     true,
	})); err != nil {
		t.Fatalf("write the seeding request: %v", err)
	}

	// A liveness ceiling, not a performance budget: it separates "the handler was
	// reached" from "it never was", which no amount of machine contention blurs.
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the seeding request never reached the dispatcher")
	}

	if _, err := stdinW.Write(framedNotification("$/cancelRequest", map[string]any{"id": targetID})); err != nil {
		t.Fatalf("write the cancel: %v", err)
	}
	if _, err := stdinW.Write(framedNotification("aggrega/shutdown", nil)); err != nil {
		t.Fatalf("write shutdown: %v", err)
	}
	stdinW.Close()

	select {
	case err := <-loopDone:
		if err != nil {
			t.Fatalf("dispatchLoop: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the dispatch loop did not exit")
	}

	ws, err := os.ReadFile(wsPath)
	if err != nil {
		t.Fatalf("read the workspace layer: %v", err)
	}
	if strings.Contains(string(ws), host) {
		t.Errorf("the withdrawn seeding wrote the registry entry anyway:\n%s", ws)
	}
}

// TestConfigInitOverTheWireWritesWhenNobodyCancels is the positive control, and it
// is what keeps the test above from being satisfied by a seeding that never writes
// at all.
//
// Same request, same registry, same workspace — the only difference is that nothing
// cancels. The entry is written and the reply reports the write. It asserts nothing
// about the trust outcome, because that outcome is a property of whether a transport
// was supplied rather than of the withdrawal this pair is about, and an assertion on
// it here would pin today's transport wiring into a test about cancellation.
func TestConfigInitOverTheWireWritesWhenNobodyCancels(t *testing.T) {
	t.Parallel()

	const host = probeRegistryHost
	srv, wsPath := seedingProbeWorkspace(t, nil, nil)

	resp := callDaemonMethod(t, srv, "aggrega/configInit", map[string]any{
		"registry":  host,
		"workspace": true,
		"force":     true,
	})
	var res configInitResult
	decodeDaemonResult(t, resp, &res)

	if !res.Written {
		t.Error("the reply reports no write, so the file assertion below would be measuring the wrong operation")
	}
	ws, err := os.ReadFile(wsPath)
	if err != nil {
		t.Fatalf("read the workspace layer: %v", err)
	}
	if !strings.Contains(string(ws), host) {
		t.Errorf("the completed seeding wrote no entry for the registry it seeded:\n%s", ws)
	}
}

// TestValidationRunsNoConventionRuleWhenTheClientCancels is the lint entry point's
// half of the same proof, at the daemon's own request boundary.
//
// The publishing-conventions layer runs the linter guests on every validation, so
// it is the lint path most likely to be in flight when a keystroke supersedes it.
// The validation is handed an already-cancelled context; the observable is that no
// conventions entry reaches the reply, where the identical validation under a live
// context carries one. The rules did not run.
//
// It is asserted through the composed reply rather than through the return value
// for the same reason the seeding test asserts the file: a withheld answer proves
// nothing, and what this reads is the layer the rules would have published.
func TestValidationRunsNoConventionRuleWhenTheClientCancels(t *testing.T) {
	t.Parallel()
	root, rel := conventionsWorkspace(t, conventionsBadComponent)

	t.Run("a live request: the rules run and the layer carries what they found", func(t *testing.T) {
		t.Parallel()
		srv := conventionsServer(t, root, true)
		entries := conventionEntries(validateSavedFile(t, srv, root, rel, ""))
		if len(entries) == 0 {
			t.Fatal("the conventions layer published nothing over a convention-breaking entity, so the withdrawn case below would prove nothing")
		}
	})

	t.Run("a withdrawn request: no rule runs, so the layer carries nothing", func(t *testing.T) {
		t.Parallel()
		srv := conventionsServer(t, root, true)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		res, envelope := srv.handleValidate(ctx, validateParams{
			File:    filepath.Join(root, rel),
			Content: conventionsBadComponent,
		})
		if envelope != nil {
			t.Fatalf("the validation was refused: [%d] %s", envelope.Code, envelope.Message)
		}
		if entries := conventionEntries(res.Diagnostics); len(entries) != 0 {
			t.Errorf("the conventions rules ran over a withdrawn validation and published %d entry(ies):\n  %s",
				len(entries), diagSummary(entries))
		}
	})
}
