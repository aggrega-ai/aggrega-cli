// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// handshakeRead is one resources/read in the handshake revision: no per-request
// metadata, so the answer to an absent resource carries the code that revision
// uses rather than the one the per-request revision re-codes it to.
func handshakeRead(uri string) string {
	return `{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"` + uri + `"}}`
}

// TestReadingCliHelpRunsNoCommand is the proof that a cli:// read starts nothing.
//
// The control is the half that carries it. A recording runner that no read
// touched proves nothing on its own — a runner nothing could ever reach would
// satisfy the same assertion — so the same server then invokes a generated
// command tool, which must reach that runner. One server, one runner, two routes:
// the read records nothing and the tool call records its argument vector.
func TestReadingCliHelpRunsNoCommand(t *testing.T) {
	t.Parallel()
	var ran [][]string
	s := newCommandToolServer(t, func(_ context.Context, args []string, stdout, _ io.Writer) error {
		ran = append(ran, args)
		_, _ = io.WriteString(stdout, "the command ran\n")
		return nil
	})
	s.renderHelp = func(path []string) (string, bool) {
		return "USAGE: aggrega " + strings.Join(path, " "), true
	}

	for _, uri := range []string{"cli://", "cli://work", "cli://group/nested"} {
		rr, rerr := readResource(t, s, uri)
		if rerr != nil {
			t.Fatalf("resources/read %s: %+v", uri, rerr)
		}
		if len(rr.Contents) != 1 || !strings.HasPrefix(rr.Contents[0].Text, "USAGE: aggrega") {
			t.Fatalf("resources/read %s served %+v, want the rendered help", uri, rr.Contents)
		}
	}
	if len(ran) != 0 {
		t.Errorf("reading the cli:// family ran %d command(s): %v", len(ran), ran)
	}

	// The control: the same runner, reached the way a generated command tool
	// reaches it.
	res := callTool(t, s, "cli-work", map[string]any{})
	if res.IsError {
		t.Fatalf("cli-work reported an error: %s", res.Content[0].Text)
	}
	if len(ran) != 1 {
		t.Fatalf("the command tool reached the runner %d time(s), want 1; the zero above says "+
			"nothing while the runner is unreachable", len(ran))
	}
	if strings.Join(ran[0], " ") != "work" {
		t.Errorf("the command tool ran %v, want [work]", ran[0])
	}
}

// TestASessionHandedNoRendererServesNoCliResource is the proof that an absent
// renderer is an absent resource rather than a reason to reach for the command
// runner.
//
// The same control applies: the runner is recording and reachable, and the tool
// call at the end is what makes "the runner was never called" a fact about the
// read.
func TestASessionHandedNoRendererServesNoCliResource(t *testing.T) {
	t.Parallel()
	var ran [][]string
	s := newCommandToolServer(t, func(_ context.Context, args []string, stdout, _ io.Writer) error {
		ran = append(ran, args)
		_, _ = io.WriteString(stdout, "the command ran\n")
		return nil
	})
	if s.renderHelp != nil {
		t.Fatal("the fixture server was handed a renderer, so this proves nothing")
	}

	for _, uri := range []string{"cli://", "cli://work", "cli://group/nested"} {
		if _, rerr := readResource(t, s, uri); rerr == nil || rerr.Code != codeResourceNotFound {
			t.Errorf("resources/read %s error = %+v, want code %d", uri, rerr, codeResourceNotFound)
		}
	}
	if len(ran) != 0 {
		t.Errorf("reading the cli:// family with no renderer ran %d command(s): %v", len(ran), ran)
	}

	res := callTool(t, s, "cli-work", map[string]any{})
	if res.IsError {
		t.Fatalf("cli-work reported an error: %s", res.Content[0].Text)
	}
	if len(ran) != 1 {
		t.Fatalf("the command tool reached the runner %d time(s), want 1; the zero above says "+
			"nothing while the runner is unreachable", len(ran))
	}
}

// TestTheHelpRendererReachesTheHTTPTransport names the transport on purpose.
//
// The server is built from a Config at two places — the stdio entry point and
// the HTTP handler's constructor — and a renderer carried into one of them and
// not the other leaves cli:// answering "not there" over HTTP. That answer is a
// legal one, so every assertion that does not name the transport passes while the
// family is unreachable to every caller that reaches this server over the
// network. Both arms below therefore go through the exported constructor, which
// is the site that would be missing the field.
//
// The second arm is the control: the same constructor, the same request, and a
// configuration carrying no renderer, which must answer that the resource is not
// there. Without it the first arm would pass on a handler that served any text at
// all.
func TestTheHelpRendererReachesTheHTTPTransport(t *testing.T) {
	// Not parallel: the offline posture is set on the process environment, so
	// the start-of-run documentation acquisition refuses at once instead of
	// waiting on a registry this proof has nothing to do with.
	t.Setenv("AGGREGA_OFFLINE", "1")

	served := serveOverHTTP(t, Config{
		Stderr: io.Discard,
		RenderHelp: func(path []string) (string, bool) {
			return "USAGE: aggrega " + strings.Join(path, " "), true
		},
	}, handshakeRead("cli://work"))
	if served.frame.Error != nil {
		t.Fatalf("resources/read cli://work over HTTP: %+v", served.frame.Error)
	}
	if !strings.Contains(served.raw, `USAGE: aggrega work`) {
		t.Errorf("the HTTP answer does not carry the rendered help: %s", served.raw)
	}

	unserved := serveOverHTTP(t, Config{Stderr: io.Discard}, handshakeRead("cli://work"))
	if unserved.frame.Error == nil {
		t.Fatalf("a session carrying no renderer served %s", unserved.raw)
	}
	if unserved.frame.Error.Code != codeResourceNotFound {
		t.Errorf("error code = %d (%q), want %d",
			unserved.frame.Error.Code, unserved.frame.Error.Message, codeResourceNotFound)
	}
}

// serveOverHTTP drives one request against a handler built by the exported
// constructor, with the test-only authenticator installed so the credential
// check does not stand in front of the clause under test.
func serveOverHTTP(t *testing.T, cfg Config, body string) answer {
	t.Helper()
	h, err := NewHTTPHandler(context.Background(), cfg, defaultTransportConfig())
	if err != nil {
		t.Fatalf("NewHTTPHandler: %v", err)
	}
	hh, ok := h.(*httpHandler)
	if !ok {
		t.Fatalf("NewHTTPHandler returned %T, want the package's own handler", h)
	}
	hh.auth = acceptAllAuthenticator{}
	ts := httptest.NewServer(hh)
	t.Cleanup(ts.Close)

	f := &httpFixture{ts: ts, h: hh}
	return f.send(t, http.MethodPost, endpointPath, nil, body)
}
