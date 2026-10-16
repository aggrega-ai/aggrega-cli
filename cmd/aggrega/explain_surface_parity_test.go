// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// One code, three surfaces, one answer.
//
// The command line, the daemon and the assistant-facing tool protocol each expand
// a code a build printed, and the contract between them is not that they agree
// today but that there is only one answer to agree with. This drives ALL THREE
// over their real entry points — the command tree, the daemon's dispatch loop over
// its own framing, and the tool protocol's message loop over its own — against one
// seeded workspace, and compares what each produced.
//
// The expectation is not read from the composition any surface uses. It is the
// text one surface actually wrote, compared against the payloads the others
// actually returned, re-rendered here through formats written in this file. So a
// change that made two of them diverge cannot satisfy this by moving both sides at
// once through the code they share.
//
// The third surface is handed a DIAGNOSTIC rather than a code, which is what a
// caller there holds: it resolves the code out of the text it was given. So the
// comparison is over the part of the answer that is about the condition, and the
// case composes the surrounding prose from a format written here.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/haltref"
	"github.com/aggrega-ai/aggrega-cli/internal/mcp"
	"github.com/aggrega-ai/aggrega-cli/internal/serve"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
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
	return explainOverTheDaemonWireWith(t, explainContext(t), workspaceRoot, code)
}

// explainOverTheDaemonWireWith is explainOverTheDaemonWire with the daemon run
// under a context the caller states.
func explainOverTheDaemonWireWith(t *testing.T, ctx context.Context, workspaceRoot, code string) explainErrorReply {
	t.Helper()

	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "aggrega/explainError",
		// Every request that acts on a workspace names it, and expanding a code
		// reads the workspace to name its documentation bundle and the check a
		// copy of it is admitted by.
		"params": map[string]string{"code": code, "workspaceRoot": workspaceRoot},
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
	// It runs under the context the case states, which carries the cache root the
	// case seeded the documentation every surface here reads in.
	if rerr := serve.Run(ctx, serve.RunConfig{
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

// assistantExplainReply is the assistant surface's answer, decoded by field name
// rather than by importing that surface's own result type.
//
// Decoding into a local shape is what makes this a reading of the WIRE, for the
// reason the daemon's reply is decoded that way: sharing the producer's struct
// would make a renamed key invisible here, and the key names are part of what a
// client binds to. The two surfaces spell their keys differently — one in the
// casing its protocol uses, one in the casing its own does — so this shape is not
// the daemon's with a different name, and a comparison across the two crosses that
// difference deliberately.
type assistantExplainReply struct {
	Message    string `json:"message"`
	Guidance   string `json:"guidance"`
	DocPointer string `json:"doc_pointer"`
	Reference  string `json:"reference"`
	Note       string `json:"note"`
	Condition  *struct {
		ShortCode string `json:"short_code"`
		Code      string `json:"code"`
		Message   string `json:"message"`
	} `json:"condition"`
	Entries []struct {
		Heading string `json:"heading"`
		Body    string `json:"body"`
	} `json:"entries"`
}

// explainOverTheAssistantWire starts the tool protocol over the workspace, calls
// the explain-error tool with one diagnostic through its own message loop, and
// returns the decoded structured payload beside the text block.
//
// It goes through mcp.Run rather than through a handler so the call is ROUTED: the
// tool exists only because a registry entry names it, so an entry that was never
// added fails here as an unknown tool rather than passing on a handler nothing can
// reach.
//
// The session is given no command surface, deliberately. A tool derived from the
// command tree re-invokes the running binary — which under test is this test
// binary — so a session carrying one could re-enter the suite.
//
// The working directory is moved to the workspace because this surface resolves
// the workspace it serves from where the process stands: the documentation a
// session serves is a property of the session rather than of one call, so there is
// no per-call root to pass. The other two surfaces are handed their root
// explicitly and are unaffected.
func explainOverTheAssistantWire(t *testing.T, workspaceRoot, diagnostic, kind string) (assistantExplainReply, string) {
	t.Helper()
	return explainOverTheAssistantWireWith(t, explainContext(t), workspaceRoot, diagnostic, kind)
}

// explainOverTheAssistantWireWith is explainOverTheAssistantWire with the session
// run under a context the caller states.
func explainOverTheAssistantWireWith(t *testing.T, ctx context.Context, workspaceRoot, diagnostic, kind string) (assistantExplainReply, string) {
	t.Helper()
	t.Chdir(workspaceRoot)

	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "explain-error",
			"arguments": map[string]string{"message": diagnostic, "kind": kind},
		},
	})
	if err != nil {
		t.Fatalf("marshal the request: %v", err)
	}

	var stdin bytes.Buffer
	stdin.Write(body)
	stdin.WriteByte('\n')

	var stdout, stderr bytes.Buffer
	// Stdin is fully written before the session starts and is never appended to, so
	// the loop reads one message and then reaches EOF, which is the clean exit.
	if rerr := mcp.Run(ctx, mcp.Config{
		Stdin:         &stdin,
		Stdout:        &stdout,
		Stderr:        &stderr,
		ServerVersion: provenanceCompilerVersion(),
	}); rerr != nil {
		t.Fatalf("the session exited with an error: %v\nstderr: %s", rerr, stderr.String())
	}

	var envelope struct {
		Result *struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			StructuredContent json.RawMessage `json:"structuredContent"`
			IsError           bool            `json:"isError"`
		} `json:"result"`
		Error json.RawMessage `json:"error"`
	}
	line := bytes.TrimSpace(stdout.Bytes())
	if len(line) == 0 {
		t.Fatalf("the session wrote no message\nstderr: %s", stderr.String())
	}
	if err := json.Unmarshal(line, &envelope); err != nil {
		t.Fatalf("decode the reply: %v\nreply: %s", err, line)
	}
	if envelope.Error != nil {
		t.Fatalf("explain-error was refused at the protocol level: %s\nstderr: %s", envelope.Error, stderr.String())
	}
	if envelope.Result == nil {
		t.Fatalf("the reply carries neither a result nor an error: %s", line)
	}
	if envelope.Result.IsError {
		t.Fatalf("explain-error reported a failure: %s", envelope.Result.Content[0].Text)
	}
	if len(envelope.Result.Content) == 0 || envelope.Result.Content[0].Type != "text" {
		t.Fatalf("the result carries no text block: %s", line)
	}
	var reply assistantExplainReply
	if err := json.Unmarshal(envelope.Result.StructuredContent, &reply); err != nil {
		t.Fatalf("decode the structured payload: %v\npayload: %s", err, envelope.Result.StructuredContent)
	}
	return reply, envelope.Result.Content[0].Text
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

// assistantDiagnosticKind is the entity kind the diagnostic the assistant surface
// is handed concerns. It selects that surface's pointer to the authoring
// contract, which is a different question from the condition and is asserted as
// its own fact below.
const assistantDiagnosticKind = "recipe"

// renderAssistantGuidance composes the prose the assistant surface is expected to
// have written, through a format written HERE and out of the text the COMMAND LINE
// actually produced.
//
// This is the whole point of the comparison below, and it is why the format is
// duplicated rather than shared. A case that composed the expectation through the
// same code the surface composes through would compare one implementation against
// itself: both sides would move together and the comparison could never fail.
// Written independently, and fed the other surface's real output, it fails when
// either surface stops carrying what the other does.
//
// The command line's answer ends in the newline a terminal wants and the prose
// here does not, so the trailing whitespace is normalised — the case's own
// decision about a line ending, not a reading of what the surface did with it.
func renderAssistantGuidance(diagnostic, fromCommand, docPointer string) string {
	return diagnostic + "\n\n" + strings.TrimSpace(fromCommand) + "\n\nSee: " + docPointer
}

// TestExplainIsOneAnswerOnEverySurface is clause-level: the daemon's answer for a
// code, the assistant surface's answer for a diagnostic carrying that code, and
// the command line's answer for the same code are one answer.
//
// It runs against the COMMITTED published pages rather than a fixture, so what is
// compared is three surfaces reading one real corpus. A fixture page is written by
// whoever writes the test and would agree with every reader by construction.
func TestExplainIsOneAnswerOnEverySurface(t *testing.T) {
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

	// The condition itself, which no surface reads from the documentation.
	if fromDaemon.ShortCode != short || fromDaemon.Code != code || fromDaemon.Message != message {
		t.Errorf("the daemon reports a different condition than the registry declares:\n"+
			"got  %s / %s / %s\nwant %s / %s / %s",
			fromDaemon.ShortCode, fromDaemon.Code, fromDaemon.Message, short, code, message)
	}

	// The precondition, without which every comparison below would hold over an
	// answer carrying no published prose at all.
	if len(fromDaemon.Entries) == 0 {
		t.Fatalf("the daemon returned no published entries for %s, so comparing the answers would "+
			"compare the condition line alone; note: %q", short, fromDaemon.Note)
	}
	if fromDaemon.Note != "" {
		t.Errorf("the daemon reports the walk-through missing while returning %d entries: %q",
			len(fromDaemon.Entries), fromDaemon.Note)
	}

	if got := renderReply(fromDaemon); got != fromCommand {
		t.Errorf("the daemon and the command line answer %s differently.\n--- the daemon, rendered ---\n%s\n"+
			"--- the command line ---\n%s", short, got, fromCommand)
	}

	// The assistant surface is handed the diagnostic a build would have printed,
	// because that is what a caller there holds. This runs last: it moves the
	// working directory to the workspace, which the two runs above do not want.
	diagnostic := "[CATALOG_ERROR " + short + "] recipe.yaml: " + message
	fromAssistant, assistantText := explainOverTheAssistantWire(t, ws, diagnostic, assistantDiagnosticKind)

	if fromAssistant.Condition == nil {
		t.Fatalf("the assistant surface resolved no condition out of %q, so it answered with a pointer "+
			"where the other two answered with the condition; note: %q", diagnostic, fromAssistant.Note)
	}
	// The same three facts the daemon reports, compared against the daemon's own
	// values rather than against the registry a second time — which is what makes
	// this a comparison of two surfaces and not two readings of one accessor.
	if fromAssistant.Condition.ShortCode != fromDaemon.ShortCode ||
		fromAssistant.Condition.Code != fromDaemon.Code ||
		fromAssistant.Condition.Message != fromDaemon.Message {
		t.Errorf("the assistant surface reports a different condition than the daemon:\n"+
			"got  %s / %s / %s\nwant %s / %s / %s",
			fromAssistant.Condition.ShortCode, fromAssistant.Condition.Code, fromAssistant.Condition.Message,
			fromDaemon.ShortCode, fromDaemon.Code, fromDaemon.Message)
	}
	if fromAssistant.Reference != fromDaemon.Reference {
		t.Errorf("the two structured surfaces publish the condition at different addresses: %q and %q",
			fromAssistant.Reference, fromDaemon.Reference)
	}
	if fromAssistant.Note != "" {
		t.Errorf("the assistant surface reports the walk-through missing while the daemon returned %d "+
			"entries from the same workspace: %q", len(fromDaemon.Entries), fromAssistant.Note)
	}

	// The published entries, per entry rather than by count: two sides short by one
	// still agree on a count, so the comparison names what is missing.
	daemonEntries := map[string]string{}
	for _, e := range fromDaemon.Entries {
		daemonEntries[e.Heading] = e.Body
	}
	assistantEntries := map[string]string{}
	for _, e := range fromAssistant.Entries {
		assistantEntries[e.Heading] = e.Body
	}
	for heading, body := range daemonEntries {
		got, carried := assistantEntries[heading]
		if !carried {
			t.Errorf("the assistant surface omits the entry %q that the daemon carries", heading)
			continue
		}
		if got != body {
			t.Errorf("the two surfaces carry different prose under %q.\n--- the assistant ---\n%s\n"+
				"--- the daemon ---\n%s", heading, got, body)
		}
	}
	for heading := range assistantEntries {
		if _, carried := daemonEntries[heading]; !carried {
			t.Errorf("the assistant surface carries the entry %q, which the daemon does not", heading)
		}
	}

	// The prose an assistant reads carries the command line's whole answer inside
	// it, unaltered. This is the assertion that would fail on a surface which
	// resolved the condition and then rendered it its own way.
	if want := renderAssistantGuidance(diagnostic, fromCommand, fromAssistant.DocPointer); fromAssistant.Guidance != want {
		t.Errorf("the assistant surface's prose is not the diagnostic, the command line's answer and the "+
			"pointer.\n--- got ---\n%s\n--- want ---\n%s", fromAssistant.Guidance, want)
	}
	if assistantText != fromAssistant.Guidance {
		t.Errorf("the text block and the payload's prose differ.\n--- text ---\n%s\n--- payload ---\n%s",
			assistantText, fromAssistant.Guidance)
	}
	// The pointer to the authoring contract answers a different question from the
	// condition, so it is asserted as its own fact against a value written here.
	if want := "https://aggrega.ai/docs/language-reference/entities/" + assistantDiagnosticKind + "/"; fromAssistant.DocPointer != want {
		t.Errorf("doc pointer = %q, want the %s language-reference page's published address (%q)",
			fromAssistant.DocPointer, assistantDiagnosticKind, want)
	}

	// What an assistant ends up reading, so a reader of a PASSING run sees the
	// answer rather than only that something was compared.
	t.Logf("the assistant surface answers %s with:\n%s", short, fromAssistant.Guidance)
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

// explainRetiredProbe is a short code the retirement record holds.
//
// It is quoted rather than read off an accessor because there is nothing to read it
// off: the record answers a lookup — is THIS number retired — and enumerates
// nothing, deliberately, since a retired number is a positive declaration rather
// than a gap in a sequence. The quote is checked against that record before it is
// used, so a case pointed at a number that stopped being retired fails saying so
// rather than quietly measuring nothing.
const explainRetiredProbe = "E0157"

// retiredRefusal returns the refusal a spent short code earns, built from the
// retirement record and the refusal's own type.
//
// Neither input is anything the three surfaces read to compose an answer: the
// record is the declaration all three resolve through, and the type is where the
// sentence lives. So an expectation built here is independent of every composition
// under comparison, and a change that reworded one surface's copy of the sentence
// fails rather than moving both sides of the comparison at once.
func retiredRefusal(t *testing.T) string {
	t.Helper()
	note, retired := cerrors.RetiredShortCode(explainRetiredProbe)
	if !retired {
		t.Fatalf("%s is not in the retirement record, so this case no longer exercises a spent "+
			"number; point it at one that is retired", explainRetiredProbe)
	}
	return (&haltref.RetiredCodeError{ShortCode: explainRetiredProbe, Note: note}).Error()
}

// TestExplainAnswersARetiredNumberOnEverySurface is the parity case for the one
// refusal that is a fact about the number rather than the absence of one.
//
// A number that named a condition once is answered as retired, with what it named,
// wherever it is asked: the command line reports it as the command's error, the
// daemon as a correction to the request, and the assistant-facing tool protocol as
// the note on its answer. The asymmetry this exists to prevent is silent — a
// surface that dropped the refusal would answer an old diagnostic with a pointer to
// a document, and its reader would go looking for a condition that no longer
// exists.
//
// No documentation is seeded and the workspace is pinned offline, and that is a
// property under test rather than a convenience: a spent number is answered out of
// a declaration, so no surface may make the answer wait on a bundle.
func TestExplainAnswersARetiredNumberOnEverySurface(t *testing.T) {
	// Not parallel: isolates the process environment and the working directory.
	isolateCLIEnvironment(t)
	t.Setenv("AGGREGA_OFFLINE", "1")
	want := retiredRefusal(t)
	ws := t.TempDir()

	out, notes, cmdErr := runExplainCommand(t, "explain", explainRetiredProbe, "--workspace-root", ws)
	if cmdErr == nil {
		t.Fatalf("the command line answered %s as a live condition:\n%s\nnotes: %s",
			explainRetiredProbe, out, notes)
	}
	if cmdErr.Error() != want {
		t.Errorf("the command line's refusal is not the one the retirement record earns.\n"+
			"--- got ---\n%s\n--- want ---\n%s", cmdErr.Error(), want)
	}
	if out != "" {
		t.Errorf("a spent number still wrote an answer:\n%s", out)
	}

	if got := daemonRefusalMessage(t, ws, explainRetiredProbe); got != want {
		t.Errorf("the daemon and the command line refuse %s differently.\n--- the daemon ---\n%s\n"+
			"--- the command line ---\n%s", explainRetiredProbe, got, want)
	}

	// The assistant surface is handed the diagnostic a build would have printed,
	// because that is what a caller there holds — pasting an old report is exactly
	// how a spent number reaches it. This runs last: it moves the working directory
	// to the workspace, which the two runs above do not want.
	diagnostic := "[CATALOG_ERROR " + explainRetiredProbe + "] recipe.yaml: what the condition used to name"
	fromAssistant, assistantText := explainOverTheAssistantWire(t, ws, diagnostic, assistantDiagnosticKind)

	if fromAssistant.Note != want {
		t.Errorf("the assistant surface does not carry the refusal the other two report.\n"+
			"--- got ---\n%s\n--- want ---\n%s", fromAssistant.Note, want)
	}
	// Nothing resolved, so nothing about a condition may be reported. The failure
	// this pins is a condition whose every value is blank, which a client would
	// render as an answer.
	if fromAssistant.Condition != nil {
		t.Errorf("a spent number was reported as a condition: %+v", *fromAssistant.Condition)
	}
	if len(fromAssistant.Entries) != 0 {
		t.Errorf("a spent number carried %d published entries: %+v",
			len(fromAssistant.Entries), fromAssistant.Entries)
	}
	if fromAssistant.Reference != "" {
		t.Errorf("a spent number carried the address a condition is published at: %q", fromAssistant.Reference)
	}
	// The prose is the diagnostic, the refusal and the pointer — re-composed here
	// out of the text the COMMAND LINE actually reported, through a format written
	// in this file.
	if expect := renderAssistantGuidance(diagnostic, cmdErr.Error(), fromAssistant.DocPointer); fromAssistant.Guidance != expect {
		t.Errorf("the assistant surface's prose is not the diagnostic, the refusal and the pointer.\n"+
			"--- got ---\n%s\n--- want ---\n%s", fromAssistant.Guidance, expect)
	}
	if assistantText != fromAssistant.Guidance {
		t.Errorf("the text block and the payload's prose differ.\n--- text ---\n%s\n--- payload ---\n%s",
			assistantText, fromAssistant.Guidance)
	}

	// What an assistant ends up reading, so a reader of a PASSING run sees the
	// answer rather than only that something was compared.
	t.Logf("the assistant surface answers a diagnostic carrying %s with:\n%s",
		explainRetiredProbe, fromAssistant.Guidance)
}

// daemonRefusalMessage drives one aggrega/explainError request and returns the
// message of the error envelope it was refused with.
//
// The message is read out of the envelope by field name rather than the envelope
// compared whole, because what the surfaces are compared on is the sentence a
// reader is shown; the numeric code beside it belongs to the protocol rather than
// to the answer.
func daemonRefusalMessage(t *testing.T, workspaceRoot, code string) string {
	t.Helper()
	raw := explainRefusalOverTheDaemonWire(t, workspaceRoot, code)
	var envelope struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		t.Fatalf("decode the error envelope: %v\nenvelope: %s", err, raw)
	}
	return envelope.Message
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
		// Every request that acts on a workspace names it, and expanding a code
		// reads the workspace to name its documentation bundle and the check a
		// copy of it is admitted by.
		"params": map[string]string{"code": code, "workspaceRoot": workspaceRoot},
	})
	if err != nil {
		t.Fatalf("marshal the request: %v", err)
	}
	var stdin bytes.Buffer
	fmt.Fprintf(&stdin, "Content-Length: %d\r\n\r\n", len(body))
	stdin.Write(body)

	var stdout, stderr bytes.Buffer
	if rerr := serve.Run(explainContext(t), serve.RunConfig{
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
