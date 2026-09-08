// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// One code, two surfaces, one answer.
//
// The command line and the daemon both expand a code a build printed, and the
// contract between them is not that they agree today but that there is only one
// answer to agree with. This drives BOTH surfaces over their real entry points —
// the command tree, and the daemon's dispatch loop over its own framing — against
// one seeded workspace, and compares what each produced.
//
// The expectation is not read from the composition either surface uses. It is the
// text one surface actually wrote, compared against the payload the other actually
// returned, re-rendered here through a format written in this file. So a change
// that made the two diverge cannot satisfy this by moving both sides at once
// through the code they share.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/haltref"
	"github.com/aggrega-ai/aggrega-cli/internal/serve"
)

// explainErrorReply is the daemon's answer, decoded by field name rather than by
// importing the daemon's own result type.
//
// Decoding into a local shape is what makes this a reading of the WIRE. Sharing
// the producer's struct would make a renamed key invisible here, and the key names
// are part of what a client binds to.
type explainErrorReply struct {
	ShortCode string `json:"shortCode"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	Reference string `json:"reference"`
	Note      string `json:"note"`
	Entries   []struct {
		Heading string `json:"heading"`
		Body    string `json:"body"`
	} `json:"entries"`
}

// explainOverTheDaemonWire starts the daemon over the workspace, sends one
// aggrega/explainError request through its own framing, and returns the decoded
// reply.
//
// It goes through serve.Run rather than through a handler so the request is
// ROUTED: the method exists only because the availability table names it, so a row
// that was never added fails here as an unknown method rather than passing on a
// handler nothing can reach.
func explainOverTheDaemonWire(t *testing.T, workspaceRoot, code string) explainErrorReply {
	t.Helper()

	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "aggrega/explainError",
		"params":  map[string]string{"code": code},
	})
	if err != nil {
		t.Fatalf("marshal the request: %v", err)
	}

	var stdin bytes.Buffer
	fmt.Fprintf(&stdin, "Content-Length: %d\r\n\r\n", len(body))
	stdin.Write(body)

	var stdout, stderr bytes.Buffer
	// Stdin is fully written before the daemon starts and is never appended to, so
	// the loop reads one request and then reaches EOF, which is the clean exit.
	if rerr := serve.Run(serve.RunConfig{
		Stdin:           &stdin,
		Stdout:          &stdout,
		Stderr:          &stderr,
		WorkspaceRoot:   workspaceRoot,
		CompilerVersion: provenanceCompilerVersion(),
	}); rerr != nil {
		t.Fatalf("the daemon exited with an error: %v\nstderr: %s", rerr, stderr.String())
	}

	envelope := decodeOneFrame(t, stdout.Bytes())
	if raw, refused := envelope["error"]; refused {
		t.Fatalf("aggrega/explainError refused %q: %s\nstderr: %s", code, raw, stderr.String())
	}
	raw, answered := envelope["result"]
	if !answered {
		t.Fatalf("the reply carries neither a result nor an error: %s", stdout.String())
	}
	var reply explainErrorReply
	if uerr := json.Unmarshal(raw, &reply); uerr != nil {
		t.Fatalf("decode the result: %v\nresult: %s", uerr, raw)
	}
	return reply
}

// decodeOneFrame reads the first Content-Length-framed message out of the
// daemon's output stream.
//
// The parse is deliberately strict about the framing rather than scanning for the
// first opening brace: the framing is part of the wire, and a reply that arrived
// unframed would be a defect this should report rather than skip past.
func decodeOneFrame(t *testing.T, out []byte) map[string]json.RawMessage {
	t.Helper()
	const header = "Content-Length: "
	start := bytes.Index(out, []byte(header))
	if start < 0 {
		t.Fatalf("the daemon wrote no framed message: %q", out)
	}
	rest := out[start+len(header):]
	end := bytes.Index(rest, []byte("\r\n\r\n"))
	if end < 0 {
		t.Fatalf("the framing header is unterminated: %q", out)
	}
	var length int
	if _, err := fmt.Sscanf(string(rest[:end]), "%d", &length); err != nil {
		t.Fatalf("the framing header carries no length: %q", rest[:end])
	}
	payload := rest[end+4:]
	if len(payload) < length {
		t.Fatalf("the frame declares %d bytes and carries %d: %q", length, len(payload), out)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(payload[:length], &envelope); err != nil {
		t.Fatalf("decode the frame: %v\nframe: %s", err, payload[:length])
	}
	return envelope
}

// renderReply composes the reply the way a terminal reads it, through a format
// written HERE.
//
// This is the whole point of the comparison below, and it is why the format is
// duplicated rather than shared. A test that rendered through the same code the
// command line renders through would compare one implementation against itself:
// both sides would move together and the comparison could never fail. Written
// independently, it fails when either surface stops carrying what the other does.
//
// It is a deliberate near-duplicate for exactly that reason. Do not unify it with
// the shared renderer; unifying it removes the only thing it measures.
func renderReply(reply explainErrorReply) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s  %s\n\nA diagnostic naming this condition opens with:\n  %s\n",
		reply.ShortCode, reply.Code, reply.Message)
	for _, entry := range reply.Entries {
		fmt.Fprintf(&b, "\n%s\n", entry.Body)
	}
	return b.String()
}

// TestExplainIsOneAnswerOnBothSurfaces is clause-level: the daemon's answer for a
// code is the command line's answer for the same code, byte for byte.
//
// It runs against the COMMITTED published pages rather than a fixture, so what is
// compared is two surfaces reading one real corpus. A fixture page is written by
// whoever writes the test and would agree with both readers by construction.
func TestExplainIsOneAnswerOnBothSurfaces(t *testing.T) {
	// The pages are read BEFORE the environment is isolated, and the order is
	// load-bearing: isolation moves the working directory, and the pages are
	// addressed relative to this package.
	published := publishedErrorPages(t)
	isolateCLIEnvironment(t)

	pages := map[string]string{}
	for _, p := range []string{haltref.ReferencePagePath, haltref.PagePath} {
		body, ok := published[filepath.Base(p)]
		if !ok {
			t.Fatalf("the published error pages carry no %s", filepath.Base(p))
		}
		pages[p] = body
	}
	ws := seedExplainWorkspace(t, pages)
	short, code, message := explainProbe(t)

	fromCommand, notes, err := runExplainCommand(t, "explain", short, "--workspace-root", ws)
	if err != nil {
		t.Fatalf("aggrega explain %s: %v\nnotes: %s", short, err, notes)
	}
	fromDaemon := explainOverTheDaemonWire(t, ws, short)

	// The condition itself, which neither surface reads from the documentation.
	if fromDaemon.ShortCode != short || fromDaemon.Code != code || fromDaemon.Message != message {
		t.Errorf("the daemon reports a different condition than the registry declares:\n"+
			"got  %s / %s / %s\nwant %s / %s / %s",
			fromDaemon.ShortCode, fromDaemon.Code, fromDaemon.Message, short, code, message)
	}

	// The precondition, without which the comparison below would hold over an
	// answer carrying no published prose at all.
	if len(fromDaemon.Entries) == 0 {
		t.Fatalf("the daemon returned no published entries for %s, so comparing the two answers would "+
			"compare the condition line alone; note: %q", short, fromDaemon.Note)
	}
	if fromDaemon.Note != "" {
		t.Errorf("the daemon reports the walk-through missing while returning %d entries: %q",
			len(fromDaemon.Entries), fromDaemon.Note)
	}

	if got := renderReply(fromDaemon); got != fromCommand {
		t.Errorf("the two surfaces answer %s differently.\n--- the daemon, rendered ---\n%s\n"+
			"--- the command line ---\n%s", short, got, fromCommand)
	}
}

// TestExplainOnTheDaemonCarriesTheReferenceAndNoProseItDidNotRead pins the two
// properties of the reply that are not about agreement.
//
// The reference is the address a diagnostic's own pointer resolves to, so a client
// holding either gets the same place; and the entries are the page's, so an answer
// must not carry prose the page does not.
func TestExplainOnTheDaemonCarriesTheReferenceAndNoProseItDidNotRead(t *testing.T) {
	published := publishedErrorPages(t)
	isolateCLIEnvironment(t)

	pages := map[string]string{}
	for _, p := range []string{haltref.ReferencePagePath, haltref.PagePath} {
		pages[p] = published[filepath.Base(p)]
	}
	ws := seedExplainWorkspace(t, pages)
	short, _, _ := explainProbe(t)

	reply := explainOverTheDaemonWire(t, ws, short)
	if reply.Reference != haltref.ExplanationURL() {
		t.Errorf("reference = %q; want the same address a diagnostic's pointer carries (%q)",
			reply.Reference, haltref.ExplanationURL())
	}

	// Each entry names a heading the page actually carries, and its body opens with
	// that heading. Without the second half an answer could return the whole page
	// as one entry and satisfy the first.
	//
	// The body is deliberately NOT asserted to be a substring of the page. What an
	// entry carries is what a reader of the rendered page sees, which is the page's
	// markdown with the declarations addressed to the generator removed — so a
	// substring comparison would be asserting that the removal did not happen. The
	// removal is asserted directly below instead.
	page := pages[haltref.PagePath]
	for _, entry := range reply.Entries {
		if !strings.Contains(page, "# "+entry.Heading+"\n") {
			t.Errorf("an entry names the heading %q, which the page does not carry", entry.Heading)
		}
		if !strings.HasPrefix(strings.TrimLeft(entry.Body, "#"), " "+entry.Heading) {
			t.Errorf("the entry body does not open with the heading it names (%q):\n%s",
				entry.Heading, firstLineOf(entry.Body))
		}
		// A generated-region marker renders as nothing on the published page, so a
		// reader never sees one. An entry carrying one would put vocabulary
		// belonging to the generator in front of somebody reading a terminal.
		if strings.Contains(entry.Body, "<!--") {
			t.Errorf("the entry for %q carries markup a reader of the page never sees:\n%s",
				entry.Heading, entry.Body)
		}
	}
}

// TestTheDiagnosticPointerResolvesOnThePublishedPages is the guard on the one
// part of the pointer nothing else can check: that the section it names still
// exists on the page it names.
//
// The address is composed from a page path and an anchor, and the anchor is the
// published site's own derivation of a HEADING's words. So a reworded heading
// moves the anchor silently — the page still renders, the link still resolves to
// the page, and the reader simply lands nowhere in particular. Nothing about the
// link's shape can detect that; only reading the page can.
//
// It asserts the heading rather than re-deriving the anchor. Re-deriving would be
// a second implementation of the site's own slug rule, which would agree with it
// until the site changed the rule — and the disagreement would be exactly the
// silent mislanding this exists to prevent.
func TestTheDiagnosticPointerResolvesOnThePublishedPages(t *testing.T) {
	t.Parallel()
	published := publishedErrorPages(t)
	page, ok := published[filepath.Base(haltref.ReferencePagePath)]
	if !ok {
		t.Fatalf("the published error pages carry no %s", filepath.Base(haltref.ReferencePagePath))
	}
	if !strings.Contains(page, "\n## "+haltref.ExplanationHeading+"\n") {
		t.Fatalf("the pointer every coded diagnostic carries resolves to the section %q on %s, and that "+
			"page carries no such heading. Either the heading was reworded — in which case the anchor "+
			"the pointer is composed from no longer names it, and both the heading constant and the "+
			"anchor constant need correcting together — or the section moved to another page.",
			haltref.ExplanationHeading, haltref.ReferencePagePath)
	}
	// The address the pointer actually carries, so a reader of a passing run can
	// see what was verified rather than only that something was.
	t.Logf("the pointer resolves to %s", haltref.ExplanationURL())
}

// firstLineOf is for a failure message: the whole body of a published entry is
// several paragraphs, and what a reader of a failing run needs is the line the
// assertion was about.
func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// TestExplainOnTheDaemonRefusesACodeNothingNames pins that the daemon reports the
// same taxonomy the command line does rather than answering everything.
//
// A code nothing resolves is a correction to the REQUEST, so it is refused as an
// invalid-params envelope rather than answered with an empty condition — which is
// what a client would otherwise render as a condition whose every field is blank.
func TestExplainOnTheDaemonRefusesACodeNothingNames(t *testing.T) {
	published := publishedErrorPages(t)
	isolateCLIEnvironment(t)

	pages := map[string]string{}
	for _, p := range []string{haltref.ReferencePagePath, haltref.PagePath} {
		pages[p] = published[filepath.Base(p)]
	}
	ws := seedExplainWorkspace(t, pages)

	for _, tc := range []struct {
		name string
		code string
		says string
	}{
		{"a code no condition carries", "E0000", "registered under"},
		{"a number that named a condition once", "E0157", "retired"},
		{"no code at all", "", "requires a `code`"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			envelope := explainRefusalOverTheDaemonWire(t, ws, tc.code)
			if !strings.Contains(envelope, tc.says) {
				t.Errorf("the refusal does not say %q, so a reader cannot tell which of the "+
					"refusals they hit: %s", tc.says, envelope)
			}
		})
	}
}

// explainRefusalOverTheDaemonWire drives one aggrega/explainError request and
// returns the message of the error envelope it was refused with, failing the test
// on a successful reply.
func explainRefusalOverTheDaemonWire(t *testing.T, workspaceRoot, code string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "aggrega/explainError",
		"params":  map[string]string{"code": code},
	})
	if err != nil {
		t.Fatalf("marshal the request: %v", err)
	}
	var stdin bytes.Buffer
	fmt.Fprintf(&stdin, "Content-Length: %d\r\n\r\n", len(body))
	stdin.Write(body)

	var stdout, stderr bytes.Buffer
	if rerr := serve.Run(serve.RunConfig{
		Stdin:           &stdin,
		Stdout:          &stdout,
		Stderr:          &stderr,
		WorkspaceRoot:   workspaceRoot,
		CompilerVersion: provenanceCompilerVersion(),
	}); rerr != nil {
		t.Fatalf("the daemon exited with an error: %v\nstderr: %s", rerr, stderr.String())
	}

	envelope := decodeOneFrame(t, stdout.Bytes())
	raw, refused := envelope["error"]
	if !refused {
		t.Fatalf("%q was answered rather than refused: %s", code, envelope["result"])
	}
	return string(raw)
}
