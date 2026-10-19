// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/importops"
	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// What a caller reaching a listener may do with the tool that turns a foreign
// description into draft authoring files, driven over a real listener with
// tokens minted by an authorization server running in this process. Nothing here
// is inferred from the registry: every row issues a request and reads the
// answer, and every claim about where a file went is read off the tree rather
// than off the answer.

// importOverTheListener installs what a session needs in order to run an
// importer at all — the plugin-bytes seam the command layer supplies in
// production — and seeds a source into the workspace the listener serves.
//
// The source is written after the fixture is built, into the root that fixture
// resolved, so the file the calls below name is genuinely under the served root
// rather than under a second directory that merely looks like it.
func importOverTheListener(t *testing.T, f *scopeGridFixture, rel string, content []byte) string {
	t.Helper()
	f.srv.resolveImportPlugin = importToolResolver
	abs := filepath.Join(f.root, rel)
	if err := os.WriteFile(abs, content, 0o644); err != nil {
		t.Fatalf("seed the served workspace: %v", err)
	}
	return abs
}

// importCallArguments is the one request the cases below send, spelled once so a
// refused call and an admitted one ask for the same thing.
func importCallArguments(rel string) map[string]any {
	return map[string]any{"source": rel}
}

// importPayloadOf decodes the structured answer off a tool result the listener
// returned.
func importPayloadOf(t *testing.T, res callToolResult) importPayload {
	t.Helper()
	if res.IsError {
		t.Fatalf("the import was refused: %s", res.Content[0].Text)
	}
	var p importPayload
	if err := json.Unmarshal(res.StructuredContent, &p); err != nil {
		t.Fatalf("the result carries no structured answer: %v (body %s)", err, res.StructuredContent)
	}
	return p
}

// TestAWriteScopedCredentialDraftsIntoTheServedWorkspace is the admitted half of
// the import tool over a listener, asserted at the filesystem.
//
// The assertion is the tree rather than the answer, because a tool that drafted
// the right files, named them, and put them somewhere else would satisfy an
// assertion on the answer alone — and what a caller inspects afterwards is the
// workspace. The format is left unnamed, so the file the tree carries is also
// the detection's own answer reaching this surface.
func TestAWriteScopedCredentialDraftsIntoTheServedWorkspace(t *testing.T) {
	pointImportSeamAtTheDemoGuest(t)
	f := newScopeGridFixture(t, false)
	importOverTheListener(t, f, "flow.json", readImportFixture(t, langflowFixture))
	before := treeUnder(t, f.root)

	got := f.callTool(t, f.grantOf(t, scopeWorkspaceWrite), importToolName, importCallArguments("flow.json"))
	p := importPayloadOf(t, toolResultOf(t, importToolName, got))

	if p.Format != "langflow" {
		t.Errorf("the source was read as the %q format, want langflow", p.Format)
	}
	if !slices.Equal(p.Written, []string{"langflow.yaml", importops.RecordFileName}) {
		t.Errorf("the answer names %v as what reached disk, want exactly [langflow.yaml %s]", p.Written,
			importops.RecordFileName)
	}
	// The root the drafts are relative to is the served workspace's, which is a
	// directory on the host a caller reaching a listener is not told about: both
	// the root and the directory the drafts are relative to are kept back, and
	// each says so.
	if p.WorkspaceRoot != nil || !p.WorkspaceRootWithheld {
		t.Errorf("the answer names the workspace root as %q (withheld=%v); over a listener it is kept back "+
			"and marked", told(p.WorkspaceRoot), p.WorkspaceRootWithheld)
	}
	if p.OutputDir != nil || !p.OutputDirWithheld {
		t.Errorf("the answer names the output directory as %q (withheld=%v); over a listener it is the "+
			"served root, kept back and marked", told(p.OutputDir), p.OutputDirWithheld)
	}

	body, err := os.ReadFile(filepath.Join(f.root, "langflow.yaml"))
	if err != nil {
		t.Fatalf("the drafted file is not under the served workspace root: %v", err)
	}
	if string(body) != importDemoEchoBody("langflow") {
		t.Errorf("the drafted file carries %q, want %q", string(body), importDemoEchoBody("langflow"))
	}
	want := []string{importops.RecordFileName, "langflow.yaml"}
	if added := treeAdditions(before, treeUnder(t, f.root)); !slices.Equal(added, want) {
		t.Errorf("the import put %v under the served root, want exactly %v, the draft and its record", added, want)
	}
}

// TestAReadScopedCredentialIsOfferedTheImportAndRefusedIt is the refused half,
// and it is asserted on both channels a caller meets.
//
// The tool is listed to a credential carrying only the read scope, on each
// listing surface, because the list is what this server carries: a tool
// withheld for want of a scope would be one no client acting for a user ever
// calls, so the refusal that sends it after the write scope would never arrive.
// The call is refused naming that scope and nothing is drafted, because a
// listing that offered the tool and a call that then ran it would be a
// credential bounding nothing.
//
// 🔴 PLANTED DEFECT: change the declared class of the import operation in the
// operation set and this case reddens — the refusal names the scope admitting
// the new class rather than the write scope, or, where the new class is the
// machine, the tool leaves both listings — and the admitted call above is
// refused, with no edit to the tool, because the tool declares no class of its
// own and the one it is admitted under is read from that set where it is
// registered.
func TestAReadScopedCredentialIsOfferedTheImportAndRefusedIt(t *testing.T) {
	pointImportSeamAtTheDemoGuest(t)
	f := newScopeGridFixture(t, false)
	importOverTheListener(t, f, "flow.json", readImportFixture(t, langflowFixture))
	before := treeUnder(t, f.root)

	readOnly := f.grantOf(t, scopeRead)
	wantListedOnBothSurfaces(t, f, readOnly, "a credential carrying only the read scope", importToolName)

	got := f.callTool(t, readOnly, importToolName, importCallArguments("flow.json"))
	wantRefusedForWantOfScope(t, importToolName, got, scopeAdmittingInThisTest(opsurface.ReachWorkspace))
	if after := treeUnder(t, f.root); !slices.Equal(after, before) {
		t.Errorf("a refused call drafted into the served workspace anyway\n--- before ---\n%v\n--- after ---\n%v",
			before, after)
	}

	// The same two listings under the credential that does admit the call name
	// it too, because the list does not move with the credential.
	wantListedOnBothSurfaces(t, f, f.grantOf(t, scopeWorkspaceWrite), "the write-scoped credential",
		importToolName)
}

// TestTheImportToolResolvesItsSourceInsideTheServedWorkspace is the containment
// half over the transport it bounds.
//
// Each row names a location the served workspace does not carry, and each is
// required to be refused naming the bound rather than read — and the refusal is
// required to name the argument, because a caller told only that something was
// out of bounds does not know which of its arguments to correct.
func TestTheImportToolResolvesItsSourceInsideTheServedWorkspace(t *testing.T) {
	pointImportSeamAtTheDemoGuest(t)
	f := newScopeGridFixture(t, false)
	importOverTheListener(t, f, "flow.json", readImportFixture(t, langflowFixture))
	credential := f.grantOf(t, scopeWorkspaceWrite)

	// The control runs first: the same call against a source inside the root is
	// answered, so a refusal below is attributable to the path rather than to
	// anything else about the request.
	inside := toolResultOf(t, importToolName,
		f.callTool(t, credential, importToolName, importCallArguments("flow.json")))
	if inside.IsError {
		t.Fatalf("the control call was refused, so nothing below is attributable: %s", inside.Content[0].Text)
	}

	for _, tc := range []struct {
		scenario string
		source   string
	}{
		{"a path climbing out of the served root", "../elsewhere.json"},
		{"a path climbing out and back in again", "sub/../../elsewhere.json"},
		{"an absolute location on the host", filepath.Join(t.TempDir(), "elsewhere.json")},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			res := toolResultOf(t, importToolName,
				f.callTool(t, credential, importToolName, importCallArguments(tc.source)))
			if !res.IsError {
				t.Fatalf("a source outside the served workspace was read rather than refused: %s",
					res.Content[0].Text)
			}
			text := res.Content[0].Text
			if !strings.Contains(text, "'source'") {
				t.Errorf("the refusal does not name the argument to correct: %q", text)
			}
			// The bound is named in words: the served workspace's location is a
			// place on the host, and a refusal tells a listener caller no more
			// about it than any other answer does.
			if !strings.Contains(text, "inside the workspace this server serves") {
				t.Errorf("the refusal does not name the bound, the workspace this server serves: %q", text)
			}
			if strings.Contains(text, f.root) {
				t.Errorf("the refusal names the served root %q, a location on the host: %q", f.root, text)
			}
		})
	}
}

// importAttachmentEscapingTheRoot is a workspace whose attached importer is
// configured to name its drafted file one level ABOVE the workspace root.
//
// The demo guest composes its scaffold path from a setting the attachment
// threads, so a setting carrying a climbing segment makes the PLUGIN return the
// escaping path — which is the only honest way to drive this. A fabricated file
// list would be this test writing the offending path itself and then watching
// the host refuse its own input.
//
// The entry names the demo importer by its identity and version, which is what
// attaches an importer: only the entry attaching the importer that runs hands it
// its settings, so an entry naming anything else would leave the guest drafting
// inside the root and this case measuring nothing. It is the first entry, which
// is the position the case tells the stand-in resolution it attaches by.
const importAttachmentEscapingTheRoot = `workspace: {}
offline: true
plugins:
  importer:
    - uri: importer/demo-importer:1.0.0
      with:
        settings:
          target_namespace: "../escaped"
`

// TestADraftedPathClimbingOutOfTheServedWorkspaceIsRefused is the half of
// containment a path argument cannot reach: the path the IMPORTER returns.
//
// A caller's own argument is judged where it is resolved. What the importer
// returns is judged nowhere near that, because it arrives after the guest has
// run — and it is untrusted for exactly the same reason a caller's value is.
// This drives a planted importer whose output names a path climbing out of the
// served root, and asserts the tree in both directions: nothing above the root,
// and nothing inside it either, because a refused import that left a contained
// prefix behind is a partial result nobody asked for.
//
// The seam answers the workspace's first plugins.importer entry as attaching the
// demo importer. The control's workspace has no such entry, so the control's
// guest is handed no settings; the planted one has, and its settings reach the
// guest.
func TestADraftedPathClimbingOutOfTheServedWorkspaceIsRefused(t *testing.T) {
	pointImportSeamAtTheDemoGuestAttachedBy(t, 0)
	f := newScopeGridFixture(t, false)
	importOverTheListener(t, f, "flow.json", readImportFixture(t, langflowFixture))

	// The control runs first, under the workspace as the fixture wrote it: the
	// same call drafts a file, so the refusal below is attributable to the
	// planted attachment rather than to anything else about the request.
	credential := f.grantOf(t, scopeWorkspaceWrite)
	control := importPayloadOf(t, toolResultOf(t, importToolName,
		f.callTool(t, credential, importToolName, importCallArguments("flow.json"))))
	if !slices.Equal(control.Written, []string{"langflow.yaml", importops.RecordFileName}) {
		t.Fatalf("the control import drafted %v; nothing below is attributable", control.Written)
	}
	if err := os.Remove(filepath.Join(f.root, "langflow.yaml")); err != nil {
		t.Fatalf("clear the control's draft: %v", err)
	}

	// Now the planted importer.
	if err := os.WriteFile(filepath.Join(f.root, workspaceconfig.ConfigFilename),
		[]byte(importAttachmentEscapingTheRoot), 0o644); err != nil {
		t.Fatalf("plant the escaping importer attachment: %v", err)
	}
	above := filepath.Dir(f.root)
	aboveBefore := treeUnder(t, above)
	insideBefore := treeUnder(t, f.root)

	res := toolResultOf(t, importToolName,
		f.callTool(t, credential, importToolName, importCallArguments("flow.json")))
	if !res.IsError {
		t.Fatalf("a drafted path climbing out of the served workspace was written rather than refused: %s",
			res.Content[0].Text)
	}
	text := res.Content[0].Text
	if !strings.Contains(text, "../escaped-langflow.yaml") {
		t.Errorf("the refusal does not name the path the importer returned, so a reader cannot tell which of "+
			"the two to correct: %q", text)
	}
	// The root the path was required to stay inside is the served root, so a
	// caller reaching a listener reads it as kept back rather than by its location.
	if strings.Contains(text, f.root) {
		t.Errorf("the refusal names the served root %q, a location on the host: %q", f.root, text)
	}
	if !strings.Contains(text, keptBackLocationWords) {
		t.Errorf("the refusal does not say where the root it names was kept back: %q", text)
	}
	// Logged so the refusal this case turns on is readable from a run rather
	// than only reconstructable from the assertions above it.
	t.Logf("the planted importer's escaping draft was refused with: %s", text)

	// The tree, in both directions. This is the claim a reported path cannot
	// make: what was written, not what was said.
	if after := treeUnder(t, above); !slices.Equal(after, aboveBefore) {
		t.Errorf("bytes reached a directory above the served workspace\n--- before ---\n%v\n--- after ---\n%v",
			aboveBefore, after)
	}
	if after := treeUnder(t, f.root); !slices.Equal(after, insideBefore) {
		t.Errorf("a refused import left files inside the served workspace\n--- before ---\n%v\n--- after ---\n%v",
			insideBefore, after)
	}
}

// TestShapeDetectionReachesTheImportToolOverAListener drives the recognised
// shapes and the unrecognised one over the transport a remote caller uses.
//
// The recognised rows are driven against the committed source fixtures rather
// than against shapes written here, because what they measure is recognition of
// a real exported document. The file each drafts is named after the format the
// detection settled on, so the answer is read off the tree rather than off a
// field this surface could have filled from its own argument.
func TestShapeDetectionReachesTheImportToolOverAListener(t *testing.T) {
	pointImportSeamAtTheDemoGuest(t)

	for _, tc := range []struct {
		scenario string
		source   string
		content  []byte
		format   string
	}{
		{"a Langflow export, whose graph is nested", "langflow.json", readImportFixture(t, langflowFixture), "langflow"},
		{"a Flowise export, whose graph is flat", "flowise.json", readImportFixture(t, flowiseFixture), "flowise"},
		{"an n8n workflow, whose connections are keyed by node", "n8n.json", readImportFixture(t, n8nFixture), "n8n"},
		{"an MCP client's configuration, told by its name", "claude_desktop_config.json",
			readImportFixture(t, mcpDesktopFixture), "mcp"},
		{"an MCP registry server description, told by its schema", "server.json",
			readImportFixture(t, mcpServerFixture), "mcp"},
		{"a Compose application, told by its name and its services", "compose.yaml",
			readImportFixture(t, composeFixture), "compose"},
		{"a Dify application, told by its kind, its mode and its version", "support-answers.yml",
			readImportFixture(t, difyFixture), "dify"},
		{"a CrewAI crew, told by its name and its agents, read as JSON with comments", "crew.jsonc",
			readImportFixture(t, crewaiCrewFixture), "crewai"},
		{"a LangGraph application's configuration, told by its name and its graphs", "langgraph.json",
			readImportFixture(t, langgraphFixture), "langgraph"},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			f := newScopeGridFixture(t, false)
			importOverTheListener(t, f, tc.source, tc.content)

			p := importPayloadOf(t, toolResultOf(t, importToolName,
				f.callTool(t, f.grantOf(t, scopeWorkspaceWrite), importToolName, importCallArguments(tc.source))))
			if p.Format != tc.format {
				t.Errorf("the shape was read as the %q format, want %q", p.Format, tc.format)
			}
			if _, err := os.Stat(filepath.Join(f.root, tc.format+".yaml")); err != nil {
				t.Errorf("the drafted file the detection's answer names is not under the served root: %v", err)
			}
		})
	}

	t.Run("a shape matching none of them is refused asking for the format", func(t *testing.T) {
		f := newScopeGridFixture(t, false)
		importOverTheListener(t, f, "mystery.json", []byte(`{"unrecognised":"shape"}`))
		before := treeUnder(t, f.root)

		res := toolResultOf(t, importToolName,
			f.callTool(t, f.grantOf(t, scopeWorkspaceWrite), importToolName, importCallArguments("mystery.json")))
		if !res.IsError {
			t.Fatalf("an unrecognised shape was handed to a guessed importer: %s", res.Content[0].Text)
		}
		if !strings.Contains(res.Content[0].Text, "'format'") {
			t.Errorf("the refusal does not name the argument that would settle it: %q", res.Content[0].Text)
		}
		if after := treeUnder(t, f.root); !slices.Equal(after, before) {
			t.Errorf("a refused import drafted into the served workspace\n--- before ---\n%v\n--- after ---\n%v",
				before, after)
		}
	})

	t.Run("naming the format outright settles it without any detection", func(t *testing.T) {
		f := newScopeGridFixture(t, false)
		importOverTheListener(t, f, "mystery.json", []byte(`{"unrecognised":"shape"}`))

		p := importPayloadOf(t, toolResultOf(t, importToolName,
			f.callTool(t, f.grantOf(t, scopeWorkspaceWrite), importToolName,
				map[string]any{"source": "mystery.json", "format": "langflow"})))
		if p.Format != "langflow" {
			t.Errorf("a format named outright was read as %q", p.Format)
		}
	})
}

// TestAnImportOverAListenerCannotNameAWorkspaceOfItsOwn holds the import tool to
// the rule every tool here is held to, at the argument this one would otherwise
// resolve its destination from.
//
// It matters more here than elsewhere: this tool's destination IS the resolved
// workspace, so an argument naming another workspace would be an argument naming
// where the drafts go. The rule is the transport's rather than this tool's, and
// what this asserts is that the tool is inside it.
func TestAnImportOverAListenerCannotNameAWorkspaceOfItsOwn(t *testing.T) {
	pointImportSeamAtTheDemoGuest(t)
	f := newScopeGridFixture(t, false)
	importOverTheListener(t, f, "flow.json", readImportFixture(t, langflowFixture))
	elsewhere := t.TempDir()

	res := toolResultOf(t, importToolName, f.callTool(t, f.grantOf(t, scopeWorkspaceWrite), importToolName,
		map[string]any{"source": "flow.json", "workspace_root": elsewhere}))
	if !res.IsError {
		t.Fatalf("a call naming a workspace of its own was answered rather than refused: %s", res.Content[0].Text)
	}
	if !strings.Contains(res.Content[0].Text, argumentWorkspaceRoot) {
		t.Errorf("the refusal does not name the argument to remove: %q", res.Content[0].Text)
	}
	if left := treeUnder(t, elsewhere); len(left) != 0 {
		t.Errorf("the refused call drafted %v into the directory it named", left)
	}

	// The argument is withheld from what this session advertises as well as
	// refused at the call, so a caller never composes the request in the first
	// place.
	var advertised struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	spec, found := lookupTool(importToolName)
	if !found {
		t.Fatalf("no tool is registered as %q", importToolName)
	}
	if err := json.Unmarshal(f.srv.advertisedSchema(*spec), &advertised); err != nil {
		t.Fatalf("the advertised schema does not parse: %v", err)
	}
	if _, offered := advertised.Properties[argumentWorkspaceRoot]; offered {
		t.Errorf("a listener advertises %q on %s, sending a caller to compose a call it will be refused for",
			argumentWorkspaceRoot, importToolName)
	}
}
