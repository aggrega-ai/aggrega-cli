// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

// The stdio leg. There is no stream here to open: the transport already frames
// one message per line, so a notification is a line like any other and what has
// to be true is the ORDER — every report ahead of the response it belongs to —
// and that each line parses on its own.
//
// The proof drives the message loop rather than a tool's invoke, because the
// order is a property of the writer and the writer is only reached through the
// loop.

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/requestcancel"
)

// driveStdioPull runs one resolution call through the stdio loop against a
// workspace the tool can reach, with the resolution replaced by a seam that
// reports the given number of stages, and returns the raw stdout.
//
// The stages are reported and the run returns; nothing here waits, because
// nothing here is about time.
func driveStdioPull(t *testing.T, steps int, token any) string {
	t.Helper()
	s, root, dirs, release := pullWorkspace(t)
	seam := newStepwiseResolution(steps, 0)
	s.resolveDependencies = seam.run

	params := map[string]any{
		"name": "pull",
		"arguments": map[string]any{
			"release": release, "workspace_root": root, "catalog_dirs": dirs,
		},
	}
	if token != nil {
		params["_meta"] = map[string]any{metaKeyProgressToken: token}
	}
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 7, "method": "tools/call", "params": params,
	})
	if err != nil {
		t.Fatalf("compose tools/call: %v", err)
	}

	out := &lockedBuffer{}
	s.out = out
	// The loop is driven directly rather than through the entry point, because
	// what is under test is the ORDER the writer framed things in and the entry
	// point would compose a session of its own around it. The registry the
	// dispatch path registers each request in is built here for the same reason:
	// the entry point is what ordinarily builds it.
	s.cancels = requestcancel.New()
	s.writeFailed = make(chan struct{})
	if err := s.messageLoop(context.Background(), bytes.NewReader(append(body, '\n')), nil); err != nil {
		t.Fatalf("message loop: %v", err)
	}
	return out.String()
}

// stdioLines splits what the run framed, insisting every line is a JSON object —
// which is what a diagnostic reaching this stream would not be.
func stdioLines(t *testing.T, written string) []map[string]json.RawMessage {
	t.Helper()
	trimmed := strings.TrimSuffix(written, "\n")
	if trimmed == "" {
		return nil
	}
	out := make([]map[string]json.RawMessage, 0)
	for _, line := range strings.Split(trimmed, "\n") {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &fields); err != nil {
			t.Fatalf("a line on the protocol stream does not parse as a JSON object: %q: %v", line, err)
		}
		out = append(out, fields)
	}
	return out
}

// TestStdioReportsProgressAheadOfTheResponse is clause 5.
//
// Three things are asserted and they fail independently: that the notifications
// are there at all, that each is well formed and names the token the request
// minted, and that every one of them precedes the response. The order is the
// load-bearing one — a client correlating a report to a call it has already been
// answered for has nothing to do with it.
func TestStdioReportsProgressAheadOfTheResponse(t *testing.T) {
	written := driveStdioPull(t, 3, "token-stdio")
	lines := stdioLines(t, written)
	if len(lines) < 2 {
		t.Fatalf("the run framed %d lines; want the notifications and the response:\n%s", len(lines), written)
	}

	seenResponse := false
	notifications := 0
	for _, line := range lines {
		if _, answered := line["id"]; answered {
			seenResponse = true
			continue
		}
		if seenResponse {
			t.Errorf("a notification was framed AFTER the response it belongs to:\n%s", written)
		}
		method := auditString(t, line, "method")
		if method != progressMethod {
			t.Errorf("the run framed a notification naming method %q; want %q", method, progressMethod)
			continue
		}
		var params struct {
			ProgressToken json.RawMessage `json:"progressToken"`
			Progress      uint64          `json:"progress"`
		}
		if err := json.Unmarshal(line["params"], &params); err != nil {
			t.Fatalf("a progress notification's params do not parse: %s: %v", line["params"], err)
		}
		if got := string(params.ProgressToken); got != `"token-stdio"` {
			t.Errorf("a progress notification names token %s; the request minted %q", got, "token-stdio")
		}
		notifications++
	}
	if notifications == 0 {
		t.Errorf("the run framed no progress notification at all:\n%s", written)
	}
	if !seenResponse {
		t.Errorf("the run framed no response:\n%s", written)
	}
}

// TestStdioWritesNoNotificationWithoutAToken is clause 5's negative row, and it
// is what makes the row above a measurement: a transport that reported to
// everybody would satisfy the assertions above unchanged.
func TestStdioWritesNoNotificationWithoutAToken(t *testing.T) {
	written := driveStdioPull(t, 3, nil)
	for _, line := range stdioLines(t, written) {
		if _, answered := line["id"]; !answered {
			t.Errorf("a call that minted no token was sent a notification anyway:\n%s", written)
		}
	}
}

// concurrentWriteCheck guards the one property a shared writer owes: two frames
// emitted at once must not interleave their bytes.
//
// It is here rather than beside the writer because the notification is the first
// thing on this transport that a goroutine other than the answering one can
// emit, so the lock covering both is new coverage rather than restated.
func TestFramesFromManyGoroutinesDoNotInterleave(t *testing.T) {
	out := &lockedBuffer{}
	s := &server{out: out, logw: &bytes.Buffer{}}

	var wg sync.WaitGroup
	for i := range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if i%2 == 0 {
				_ = s.writeNotification(progressMethod, progressNotification{
					ProgressToken: json.RawMessage(`"t"`), Progress: uint64(i),
				})
				return
			}
			_ = s.write(&rpcResponse{JSONRPC: "2.0", ID: json.RawMessage(`1`)})
		}()
	}
	wg.Wait()

	if lines := stdioLines(t, out.String()); len(lines) != 32 {
		t.Fatalf("32 frames produced %d parseable lines; a line that will not parse is two frames "+
			"whose bytes were interleaved", len(lines))
	}
}
