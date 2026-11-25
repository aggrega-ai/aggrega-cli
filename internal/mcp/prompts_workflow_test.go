// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// workflowPromptExpectation is one workflow prompt's observable contract: the
// name it is advertised under, the tools its rendered body tells a client to
// call, and how many ordered items that body enumerates.
//
// The tool list is checked in both directions. Registry-side: a name in the body
// that no registry resolves would leave the prompt teaching a call a client
// cannot make, which is worse than omitting it. Body-side: a name dropped from
// the prose while it stays in this table would leave the table asserting nothing,
// so the table cannot rot into a list of tools the prompt no longer names.
type workflowPromptExpectation struct {
	name  string
	tools []string
	steps int
}

var workflowPromptExpectations = []workflowPromptExpectation{
	{
		name:  constructAggregatePromptName,
		tools: []string{"describe", "get-schema", "draft", "validate", "build", "explain-error"},
		steps: 9,
	},
	{
		name:  migrateDeploymentPromptName,
		tools: []string{"import", "describe", "validate", "build", "explain-error"},
		steps: 7,
	},
	{
		name:  diagnoseBuildPromptName,
		tools: []string{"validate", "explain-error", "describe", "build", "lint", describeCapabilitiesName},
		steps: 7,
	},
}

// wirePromptNames drives a real prompts/list request through the server's message
// loop and returns the advertised prompt names in wire order, keyed by name to
// the description that came back with each.
//
// It decodes into generic objects keyed by the literal wire names rather than
// into listPromptsResult. Unmarshalling into listPromptsResult would read each
// field back through the same struct tag that wrote it, so a mistagged field
// would round-trip cleanly while no client ever received the value. The literal
// keys are an independent statement of the wire contract.
func wirePromptNames(t *testing.T) (order []string, descriptions map[string]string) {
	t.Helper()
	resps, _, _, err := drive(t, `{"jsonrpc":"2.0","id":1,"method":"prompts/list"}`)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(resps) != 1 {
		t.Fatalf("want 1 response, got %d", len(resps))
	}
	if resps[0].Error != nil {
		t.Fatalf("prompts/list returned error envelope: %+v", resps[0].Error)
	}
	var envelope struct {
		Prompts []map[string]json.RawMessage `json:"prompts"`
	}
	if err := json.Unmarshal(resps[0].Result, &envelope); err != nil {
		t.Fatalf("unmarshal prompts/list: %v", err)
	}
	descriptions = map[string]string{}
	for i, p := range envelope.Prompts {
		raw, ok := p["name"]
		if !ok {
			t.Fatalf("prompt[%d] carries no name key on the wire: %v", i, p)
		}
		var name string
		if err := json.Unmarshal(raw, &name); err != nil {
			t.Fatalf("prompt[%d] name is not a JSON string: %s: %v", i, raw, err)
		}
		order = append(order, name)
		if desc, ok := p["description"]; ok {
			var s string
			if err := json.Unmarshal(desc, &s); err == nil {
				descriptions[name] = s
			}
		}
	}
	return order, descriptions
}

// wirePromptText drives a real prompts/get request for name and returns the text
// of its first message as it appears on the wire, read through the literal wire
// keys for the same reason wirePromptNames uses them.
func wirePromptText(t *testing.T, name string) string {
	t.Helper()
	req, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "prompts/get",
		Params:  json.RawMessage(`{"name":` + strconv.Quote(name) + `}`),
	})
	if err != nil {
		t.Fatalf("marshal prompts/get request: %v", err)
	}
	resps, _, _, err := drive(t, string(req))
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(resps) != 1 {
		t.Fatalf("want 1 response, got %d", len(resps))
	}
	if resps[0].Error != nil {
		t.Fatalf("prompts/get %s returned error envelope: %+v", name, resps[0].Error)
	}
	var envelope struct {
		Messages []struct {
			Role    string `json:"role"`
			Content struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(resps[0].Result, &envelope); err != nil {
		t.Fatalf("unmarshal prompts/get %s: %v", name, err)
	}
	if len(envelope.Messages) == 0 {
		t.Fatalf("prompts/get %s returned no messages on the wire", name)
	}
	if envelope.Messages[0].Content.Type != "text" {
		t.Errorf("prompts/get %s first content type = %q, want text", name, envelope.Messages[0].Content.Type)
	}
	return envelope.Messages[0].Content.Text
}

// namesWholeWord reports whether body names token as a whole word, treating
// letters, digits and the hyphen as word characters.
//
// The hyphen has to count, or "describe" would match inside
// "describe-capabilities" and a body naming only the capability tool would
// wrongly satisfy an assertion about the describe tool. Excluding it also keeps
// "build" from matching inside an ordinary English word.
func namesWholeWord(body, token string) bool {
	isWordByte := func(b byte) bool {
		switch {
		case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9', b == '-':
			return true
		}
		return false
	}
	for from := 0; ; {
		i := strings.Index(body[from:], token)
		if i < 0 {
			return false
		}
		i += from
		beforeOK := i == 0 || !isWordByte(body[i-1])
		after := i + len(token)
		afterOK := after == len(body) || !isWordByte(body[after])
		if beforeOK && afterOK {
			return true
		}
		from = i + 1
	}
}

// TestPromptsListAdvertisesTheWorkflowPromptsOnTheWire proves the workflow
// prompts reach a client through the real prompts/list path, and that adding them
// did not displace the authoring set.
func TestPromptsListAdvertisesTheWorkflowPromptsOnTheWire(t *testing.T) {
	order, descriptions := wirePromptNames(t)
	advertised := map[string]bool{}
	for _, name := range order {
		advertised[name] = true
	}

	for _, want := range workflowPromptExpectations {
		if !advertised[want.name] {
			t.Errorf("prompts/list does not advertise the %q workflow prompt; advertised: %v", want.name, order)
			continue
		}
		if strings.TrimSpace(descriptions[want.name]) == "" {
			t.Errorf("workflow prompt %q is advertised with an empty description", want.name)
		}
	}

	// The authoring set is not rebuilt or replaced by the workflow set.
	for _, want := range []string{"developer-authoring", "architect-authoring", "ops-authoring", "author-entity"} {
		if !advertised[want] {
			t.Errorf("prompts/list no longer advertises the %q authoring prompt; advertised: %v", want, order)
		}
	}
}

// promptsListRequest is the raw prompts/list frame both byte-stability
// assertions drive.
const promptsListRequest = `{"jsonrpc":"2.0","id":1,"method":"prompts/list"}`

// TestPromptsListIsByteStableAcrossRepeatedRequests proves two server runs in one
// process answer prompts/list with identical bytes. It compares the raw response
// rather than a decoded structure, because decoding is exactly what would hide a
// reordering.
//
// It covers nondeterminism introduced per request — an unordered iteration inside
// the list handler, say. It structurally cannot see nondeterminism frozen at
// package initialization, because a value computed once at start-up is the same
// value for every run in that process; the cross-process assertion below is what
// covers that class.
func TestPromptsListIsByteStableAcrossRepeatedRequests(t *testing.T) {
	_, first, _, err := drive(t, promptsListRequest)
	if err != nil {
		t.Fatalf("first run returned error: %v", err)
	}
	_, second, _, err := drive(t, promptsListRequest)
	if err != nil {
		t.Fatalf("second run returned error: %v", err)
	}
	if strings.TrimSpace(first) == "" {
		t.Fatal("prompts/list produced no output; this comparison would be vacuous")
	}
	if first != second {
		t.Errorf("prompts/list is not byte-stable across repeated requests:\nfirst:  %s\nsecond: %s", first, second)
	}
}

// promptsListProbeEnv switches the cross-process assertion's binary into its
// child role: emit the raw prompts/list response between the markers below and
// return, so the parent can compare the bytes two independent processes produced.
const promptsListProbeEnv = "AGGREGA_MCP_PROMPTS_LIST_BYTES"

// The child delimits its payload so the parent can lift it out of the test
// binary's own output.
const (
	promptsListProbeBegin = "<<<PROMPTS-LIST-BEGIN>>>"
	promptsListProbeEnd   = "<<<PROMPTS-LIST-END>>>"
)

// TestPromptsListIsByteStableAcrossProcesses proves the advertised prompt list is
// byte-identical across two independent processes, which is the property the
// registry's fixed order exists to provide and the one a client relies on.
//
// It re-executes this test binary twice rather than driving the server twice in
// one process, because the ordering a run inherits from package initialization is
// fixed for the life of that process: an unordered iteration feeding an advertised
// field would produce one arbitrary order at start-up and then repeat it faithfully
// for every in-process run. Two processes each make that choice independently.
func TestPromptsListIsByteStableAcrossProcesses(t *testing.T) {
	if os.Getenv(promptsListProbeEnv) == "1" {
		var out bytes.Buffer
		if err := Run(context.Background(), Config{
			Stdin:         strings.NewReader(promptsListRequest + "\n"),
			Stdout:        &out,
			Stderr:        io.Discard,
			ServerVersion: "test-version",
		}); err != nil {
			t.Fatalf("child run returned error: %v", err)
		}
		fmt.Print(promptsListProbeBegin, out.String(), promptsListProbeEnd)
		return
	}

	first := promptsListFromChildProcess(t)
	second := promptsListFromChildProcess(t)
	if strings.TrimSpace(first) == "" {
		t.Fatal("the child process produced no prompts/list bytes; this comparison would be vacuous")
	}
	if first != second {
		t.Errorf("prompts/list is not byte-stable across processes:\nfirst:  %s\nsecond: %s", first, second)
	}
}

// promptsListFromChildProcess re-executes this test binary in its child role and
// returns the prompts/list bytes it emitted.
func promptsListFromChildProcess(t *testing.T) string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$")
	cmd.Env = append(os.Environ(), promptsListProbeEnv+"=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child process failed: %v\noutput:\n%s", err, out)
	}
	text := string(out)
	begin := strings.Index(text, promptsListProbeBegin)
	end := strings.Index(text, promptsListProbeEnd)
	if begin < 0 || end < begin {
		t.Fatalf("child output carries no delimited prompts/list payload:\n%s", text)
	}
	return text[begin+len(promptsListProbeBegin) : end]
}

// TestWorkflowPromptsRenderAnOrderedSequenceOverRealTools proves each workflow
// prompt renders through the real prompts/get path, enumerates its calls in
// order, and names only tools a client can actually call.
func TestWorkflowPromptsRenderAnOrderedSequenceOverRealTools(t *testing.T) {
	for _, want := range workflowPromptExpectations {
		t.Run(want.name, func(t *testing.T) {
			body := wirePromptText(t, want.name)
			if strings.TrimSpace(body) == "" {
				t.Fatal("rendered body is empty")
			}

			// The enumerated items appear, and they appear in ascending order:
			// presence alone would be satisfied by a shuffled sequence.
			prev := -1
			for n := 1; n <= want.steps; n++ {
				marker := "\n" + strconv.Itoa(n) + ". "
				at := strings.Index(body, marker)
				if at < 0 {
					t.Errorf("body does not enumerate item %d, so the sequence is shorter than the %d calls it is expected to teach", n, want.steps)
					continue
				}
				if at <= prev {
					t.Errorf("item %d appears at offset %d, before item %d at offset %d: the sequence is not in order", n, at, n-1, prev)
				}
				prev = at
			}

			for _, tool := range want.tools {
				if !namesWholeWord(body, tool) {
					t.Errorf("body does not name the %q tool, so this expectation asserts nothing about it", tool)
				}
				if _, ok := lookupTool(tool); !ok {
					t.Errorf("body points at tool %q, which no registry resolves: the prompt teaches a call a client cannot make", tool)
				}
			}

			// A workflow prompt teaches by pointing at the read surfaces; it does
			// not inline schema text.
			for _, pointer := range []string{"schema://", "langref://", "authoring://"} {
				if !strings.Contains(body, pointer) {
					t.Errorf("body does not point at the %s resource family", pointer)
				}
			}
			if strings.Contains(body, `"$schema"`) || strings.Contains(body, `"properties"`) {
				t.Error("body inlines schema content rather than pointing at the schema resource")
			}
		})
	}
}

// TestWorkflowGoalsAreNotTools holds the settled shape of this surface: a
// high-level goal is a prompt, never a tool.
//
// A tool answering to a goal name would carry orchestration no single operation
// can verify and that no other surface could reproduce, so the assertion is that
// each goal name resolves as a prompt and resolves as nothing in either tool
// registry. It stays true as tools are legitimately added, because it constrains
// the goal names rather than pinning the tool set.
func TestWorkflowGoalsAreNotTools(t *testing.T) {
	if len(workflowPromptExpectations) == 0 {
		t.Fatal("no workflow prompts were declared; this assertion would be vacuous")
	}
	for _, want := range workflowPromptExpectations {
		t.Run(want.name, func(t *testing.T) {
			if _, ok := lookupPrompt(want.name); !ok {
				t.Errorf("goal %q resolves as no prompt", want.name)
			}
			if spec, ok := lookupTool(want.name); ok {
				t.Errorf("goal %q also resolves as the tool %q: a high-level goal is taught as a prompt over single-purpose tools, not exposed as one tool", want.name, spec.name)
			}
		})
	}
}

// TestInstructionsPointAtTheWorkflowPromptSurface holds the connect-time guidance
// to the prompt surface it describes, in both directions: the guidance names the
// workflow prompts, and workflow prompts are actually registered.
//
// Either direction failing alone is the drift worth catching. Guidance that
// describes only the authoring prompts understates a surface a client has;
// guidance describing workflow prompts that are not registered points a client at
// nothing.
func TestInstructionsPointAtTheWorkflowPromptSurface(t *testing.T) {
	if !strings.Contains(instructionsBody, "workflow prompts") {
		t.Error("the connect-time guidance does not mention the workflow prompts, so a client is told a narrower prompt surface than it has")
	}
	if len(workflowPromptSpecs) == 0 {
		t.Fatal("no workflow prompts are registered, so the connect-time guidance points a client at nothing")
	}
	registered := map[string]bool{}
	for i := range promptRegistry {
		registered[promptRegistry[i].name] = true
	}
	for i := range workflowPromptSpecs {
		if !registered[workflowPromptSpecs[i].name] {
			t.Errorf("workflow prompt %q is declared but absent from the advertised registry", workflowPromptSpecs[i].name)
		}
	}
}

// TestMigratePromptNamesTheImportToolBeforeTheHandPath holds the migration
// prompt to the order it teaches in: the import tool first, for the formats an
// importer covers, and the hand path after it, for the rest. It also holds the
// prompt to saying the import tool is among the tools: no sentence tells a
// client that none translates a source or that the import path lies elsewhere.
func TestMigratePromptNamesTheImportToolBeforeTheHandPath(t *testing.T) {
	body := wirePromptText(t, migrateDeploymentPromptName)
	importAt := strings.Index(body, "Start with the import tool")
	handAt := strings.Index(body, "no importer covers")
	if importAt < 0 || handAt < 0 || importAt > handAt {
		t.Fatalf("the body names the import tool at %d and the hand path at %d; want the import tool first", importAt, handAt)
	}
	if _, ok := lookupTool("import"); !ok {
		t.Fatal("the prompt names the import tool, which no registry resolves")
	}
	if !strings.Contains(body, "the part you do by hand for a format no importer covers") {
		t.Error("the body's first step does not say the reading is the hand path's own part")
	}
	for _, denied := range []string{"the part no tool does", "no tool on this surface", "rather than among these tools", "not a score", "no score"} {
		if strings.Contains(body, denied) {
			t.Errorf("the body still says %q", denied)
		}
	}
}
