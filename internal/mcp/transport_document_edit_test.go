// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
)

// What a caller reaching a listener may do with the tool that writes, driven
// over a real listener with tokens minted by an authorization server running in
// this process. Nothing here is inferred from the registry: every row issues a
// request and reads the answer.

// editOverTheListener seeds the edit fixture into the workspace a listener
// fixture serves and hands back the document's workspace-relative name.
//
// The document is written after the fixture is built, into the root that
// fixture resolved, so the file the calls below name is genuinely under the
// served root rather than under a second directory that merely looks like it.
func editOverTheListener(t *testing.T, f *scopeGridFixture) (rel, abs string) {
	t.Helper()
	rel = "storefront.aggregate.yaml"
	abs = filepath.Join(f.root, rel)
	if err := os.WriteFile(abs, []byte(authoredAggregateForToolEdit), 0o644); err != nil {
		t.Fatalf("seed the served workspace: %v", err)
	}
	return rel, abs
}

// editCallArguments is the one request the cases below send, spelled once so a
// refused call and an admitted one ask for the same thing.
func editCallArguments(rel string) map[string]any {
	return map[string]any{
		"edit": "add-component",
		"file": rel,
		"node": []string{"spec", "components"},
		"name": "checkout", "value": "core/api-server:1.0.0",
	}
}

// toolResultOf decodes a tool result off an answer the listener returned,
// failing where the answer is not one.
func toolResultOf(t *testing.T, tool string, got answer) callToolResult {
	t.Helper()
	if got.status != http.StatusOK {
		t.Fatalf("%s: status = %d, want 200 (body %q)", tool, got.status, got.raw)
	}
	if got.frame.Error != nil {
		t.Fatalf("%s: the answer carries an error envelope rather than a tool result: %+v", tool, got.frame.Error)
	}
	var res callToolResult
	if err := json.Unmarshal(got.frame.Result, &res); err != nil {
		t.Fatalf("%s: the result is not a tool result: %v (body %q)", tool, err, got.raw)
	}
	if len(res.Content) == 0 {
		t.Fatalf("%s: the tool result carries no content block: %q", tool, got.raw)
	}
	return res
}

// TestAWriteScopedCredentialEditsADocumentUnderTheServedRoot is the admitted
// half of the edit tool over a listener, asserted at the filesystem.
//
// The assertion is the file on disk rather than the answer, because a tool that
// composed the document, answered with it, and wrote nothing would satisfy an
// assertion on the answer alone — and what a caller inspects afterwards is the
// document.
func TestAWriteScopedCredentialEditsADocumentUnderTheServedRoot(t *testing.T) {
	f := newScopeGridFixture(t, false)
	rel, abs := editOverTheListener(t, f)

	got := f.callTool(t, f.grantOf(t, scopeWorkspaceWrite), editDocumentToolName, editCallArguments(rel))
	res := toolResultOf(t, editDocumentToolName, got)
	if res.IsError {
		t.Fatalf("the edit was refused for a credential that carries the write scope: %s", res.Content[0].Text)
	}

	var payload documentEditPayload
	if err := json.Unmarshal(res.StructuredContent, &payload); err != nil {
		t.Fatalf("the result carries no structured answer: %v (body %q)", err, got.raw)
	}
	if len(payload.Written) != 1 || payload.Written[0] != rel {
		t.Errorf("the answer names %v as what reached disk, want exactly [%q]", payload.Written, rel)
	}
	if on := readEditedDocument(t, abs); !strings.Contains(on, "    checkout:\n      type: core/api-server:1.0.0\n") {
		t.Errorf("the entry did not reach the document under the served root\n--- document ---\n%s", on)
	}
}

// TestAReadScopedCredentialIsOfferedTheEditAndRefusedIt is the refused half,
// and it is asserted on both channels a caller meets.
//
// The tool is listed to a credential carrying only the read scope, on each
// listing surface, because the list is what this server carries: a client
// acting for a user offers a model only what it listed, so a tool withheld for
// want of a scope would be one no call names, and the refusal that sends the
// client after the write scope would never arrive. The call is refused naming
// that scope and nothing reaches the document, because a listing that offered
// the tool and a call that then ran it would be a credential bounding nothing.
//
// 🔴 PLANTED DEFECT, DEMONSTRATED: change the declared class of the document-edit
// operation in the operation set to machine and this case reddens — the tool
// leaves the write-scoped listing and the admitted call above is refused — with
// no edit to the tool, because the tool declares no class of its own and the one
// it is admitted under is read from that set where it is registered.
func TestAReadScopedCredentialIsOfferedTheEditAndRefusedIt(t *testing.T) {
	f := newScopeGridFixture(t, false)
	rel, abs := editOverTheListener(t, f)
	before := readEditedDocument(t, abs)

	readOnly := f.grantOf(t, scopeRead)
	wantListedOnBothSurfaces(t, f, readOnly, "a credential carrying only the read scope", editDocumentToolName)

	got := f.callTool(t, readOnly, editDocumentToolName, editCallArguments(rel))
	wantRefusedForWantOfScope(t, editDocumentToolName, got,
		scopeAdmittingInThisTest(opsurface.ReachWorkspace))
	if after := readEditedDocument(t, abs); after != before {
		t.Errorf("a refused call reached the document anyway\n--- before ---\n%s\n--- after ---\n%s",
			before, after)
	}

	// The same two listings under the credential that does admit the call name
	// it too, because the list does not move with the credential.
	wantListedOnBothSurfaces(t, f, f.grantOf(t, scopeWorkspaceWrite), "the write-scoped credential",
		editDocumentToolName)
}

// wantListedOnBothSurfaces asserts that each of the two listings a listener
// serves — tools/list and the capability tool's unscoped index — names the tool
// to this credential, on its own rather than as a union, so a surface that
// dropped the tool fails even where the other one carried it.
func wantListedOnBothSurfaces(t *testing.T, f *scopeGridFixture, credential, who, tool string) {
	t.Helper()
	for _, surface := range []struct {
		name  string
		names []string
	}{
		{"tools/list", f.listed(t, credential)},
		{describeCapabilitiesName, f.surfaced(t, credential)},
	} {
		if !slices.Contains(surface.names, tool) {
			t.Errorf("%s did not list %s to %s; the list is what this server carries whatever the "+
				"credential holds, and it listed %v", surface.name, tool, who, sortedNames(surface.names))
		}
	}
}

// TestTheEditToolResolvesItsDocumentInsideTheServedWorkspace is the containment
// half over the transport it bounds.
//
// Each row names a location the served workspace does not carry, and each is
// required to be refused naming the bound rather than answered — and the
// refusal is required to name the argument, because a caller told only that
// something was out of bounds does not know which of its arguments to correct.
func TestTheEditToolResolvesItsDocumentInsideTheServedWorkspace(t *testing.T) {
	f := newScopeGridFixture(t, false)
	rel, _ := editOverTheListener(t, f)
	credential := f.grantOf(t, scopeWorkspaceWrite)

	// The control runs first: the same call against a document inside the root
	// is answered, so a refusal below is attributable to the path rather than to
	// anything else about the request.
	inside := toolResultOf(t, editDocumentToolName,
		f.callTool(t, credential, editDocumentToolName, editCallArguments(rel)))
	if inside.IsError {
		t.Fatalf("the control call was refused, so nothing below is attributable: %s", inside.Content[0].Text)
	}

	for _, tc := range []struct {
		scenario string
		file     string
	}{
		{"a path climbing out of the served root", "../escaped.aggregate.yaml"},
		{"a path climbing out and back in again", "sub/../../escaped.aggregate.yaml"},
		{"an absolute location on the host", filepath.Join(t.TempDir(), "elsewhere.aggregate.yaml")},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			args := editCallArguments(rel)
			args["file"] = tc.file
			res := toolResultOf(t, editDocumentToolName,
				f.callTool(t, credential, editDocumentToolName, args))
			if !res.IsError {
				t.Fatalf("a document outside the served workspace was edited rather than refused: %s",
					res.Content[0].Text)
			}
			text := res.Content[0].Text
			if !strings.Contains(text, "'file'") {
				t.Errorf("the refusal does not name the argument to correct: %q", text)
			}
			// The bound is named in words, and the served root is not named at all:
			// it is a place on the host a caller reaching a listener is not told by
			// any answer, a refusal included.
			if !strings.Contains(text, "inside the workspace this server serves") {
				t.Errorf("the refusal does not name the bound, the workspace this server serves: %q", text)
			}
			if strings.Contains(text, f.root) {
				t.Errorf("the refusal names the served root %q, a location on the host: %q", f.root, text)
			}
		})
	}
}

// plantedMachineReachTool is a curated tool registered for an operation the
// operation set declares as reaching the machine.
//
// It is built as a spec literal rather than through the registry entry point,
// and that is deliberate: the entry point refuses an operation the curated table
// does not name, and adding such a name would ship the very thing this measures
// is filtered out. The class still comes from the operation set — it is read by
// the caller below and never written here — so what is planted is a tool of that
// class rather than a tool asserting one.
func plantedMachineReachTool(t *testing.T, operation string) toolSpec {
	t.Helper()
	op, known := opsurface.Lookup(operation)
	if !known {
		t.Fatalf("%q is not an operation, so no tool can be planted for it", operation)
	}
	if op.Reach != opsurface.ReachMachine {
		t.Fatalf("%q declares the class %q; this case needs an operation that reaches the machine, or it "+
			"measures a different admission entirely", operation, op.Reach)
	}
	return toolSpec{
		name:        "planted-machine-reach",
		operation:   operation,
		reach:       op.Reach,
		summary:     "Stands in for a curated tool answering an operation that reaches the machine.",
		description: "Stands in for a curated tool answering an operation that reaches the machine.",
		inputSchema: `{"type": "object", "properties": {}}`,
		invoke: func(*server, context.Context, json.RawMessage) (*callToolResult, *rpcError) {
			return errorResult("this tool exists to be measured and does nothing else")
		},
	}
}

// withPlantedMachineReachTool installs the planted tool in the deferred registry
// for the life of one case.
//
// 🔴 It swaps a package-level registry, so it must NOT be made parallel and no
// test that reads that registry may be made parallel either. The swap is safe as
// written because the runner resumes a parallel test only once every sequential
// one has finished, and this one restores the registry before it returns.
func withPlantedMachineReachTool(t *testing.T, spec toolSpec) {
	t.Helper()
	restore := deferredToolRegistry
	deferredToolRegistry = append(append([]toolSpec(nil), deferredToolRegistry...), spec)
	t.Cleanup(func() { deferredToolRegistry = restore })
}

// TestNoListenerOffersAToolThatReachesTheMachine is the class property, asserted
// with a planted tool rather than with whichever tool happens to carry that
// class today.
//
// A tool of that class is absent from every listing a listener serves, whatever
// scopes the token carries — so the assertion runs over every grant an
// authorization server can issue here, including the one carrying all three and
// the one carrying none. The listing is what the server carries rather than what
// a credential holds, and a class no scope admits is one no credential changes,
// so a listener leaves it out as it leaves out a withdrawn kind.
//
// The stdio session listing the same planted tool is the CONTROL, and it is what
// makes this measure the admission rather than the registry. Without it, a
// planted tool that failed to reach any registry at all would satisfy every row
// above while nothing was being filtered.
func TestNoListenerOffersAToolThatReachesTheMachine(t *testing.T) {
	planted := plantedMachineReachTool(t, "logout")
	withPlantedMachineReachTool(t, planted)

	control := toolSpecNames(newCommandToolServer(t, nil).servedTools(context.Background()))
	if !slices.Contains(control, planted.name) {
		t.Fatalf("the planted tool reaches no listing even over the process's own streams, so every row below "+
			"would pass over a tool that is simply not there; that session was served %v", sortedNames(control))
	}

	f := newScopeGridFixture(t, false)
	for _, row := range gridGrants() {
		t.Run(row.name, func(t *testing.T) {
			credential := f.grantOf(t, row.grant...)
			surfaces := []struct {
				name  string
				names []string
			}{{"tools/list", f.listed(t, credential)}}
			// A credential admitting no read is refused the capability tool's own
			// call, which indexedFor asserts; it then has one listing to read.
			if index, answered := f.indexedFor(t, credential, row.grant, map[string]any{}); answered {
				surfaces = append(surfaces, struct {
					name  string
					names []string
				}{describeCapabilitiesName, index})
			}
			for _, s := range surfaces {
				if slices.Contains(s.names, planted.name) {
					t.Errorf("%s listed a tool reaching the machine to %s; it listed %v",
						s.name, row.name, sortedNames(s.names))
				}
			}

			// Naming it directly is the other half: a tool absent from a listing
			// and still callable is exactly what a client that read about it
			// elsewhere produces.
			got := f.callTool(t, credential, planted.name, map[string]any{})
			if got.status != http.StatusForbidden {
				t.Fatalf("calling it by name was answered %d, want 403 (body %q)", got.status, got.raw)
			}
			if got.frame.Error == nil {
				t.Fatalf("a refused call was answered with a result rather than an error envelope: %q", got.raw)
			}
			if challenge := got.header.Get(headerAuthenticate); challenge != "" {
				t.Errorf("the refusal carries the challenge %q, inviting the caller to obtain a scope that does "+
					"not exist", challenge)
			}
			if !strings.Contains(got.frame.Error.Message, "no credential admits") {
				t.Errorf("the refusal does not say the class is unreachable from here: %q", got.frame.Error.Message)
			}
		})
	}
}

// TestTheEditToolDisclosesItsCommandTwinOnlyWhereThatTwinExists reads both
// listings and holds the pairing disclosure to the session that derived the
// generated tool.
//
// The disclosure is derived rather than authored, so what this measures is the
// derivation reaching this tool at all — and the listener row is what keeps a
// sentence about a tool that session does not serve from being advertised to a
// caller who can never call it.
func TestTheEditToolDisclosesItsCommandTwinOnlyWhereThatTwinExists(t *testing.T) {
	twin := commandToolName("aggrega edit-document")

	t.Run("a session handed a command surface lists both and each names the other", func(t *testing.T) {
		s := twinnedServer()
		served := map[string]string{}
		for _, spec := range s.servedTools(context.Background()) {
			served[spec.name] = spec.description
		}

		curatedDesc, listed := served[editDocumentToolName]
		if !listed {
			t.Fatalf("%s is not served at all, so the disclosure measures nothing", editDocumentToolName)
		}
		generatedDesc, listed := served[twin]
		if !listed {
			t.Fatalf("%s is not served, so this session derived no twin to disclose", twin)
		}
		if !strings.Contains(curatedDesc, "`"+twin+"`") {
			t.Errorf("%s does not name %s, which reaches the same capability by running a command:\n%s",
				editDocumentToolName, twin, curatedDesc)
		}
		if !strings.Contains(generatedDesc, "`"+editDocumentToolName+"`") {
			t.Errorf("%s does not name %s, which answers the same capability in-process:\n%s",
				twin, editDocumentToolName, generatedDesc)
		}
		if !strings.Contains(generatedDesc, firstSentence(curatedDesc)) {
			t.Errorf("%s does not carry %s's own opening sentence, so its caller is told nothing about what the "+
				"in-process form writes:\n%s", twin, editDocumentToolName, generatedDesc)
		}
	})

	t.Run("a session reached over a listener lists only the in-process tool and claims no pair", func(t *testing.T) {
		advertised := advertisedOverHTTP(t, newHTTPFixture(t, defaultTransportConfig(), nil))

		seen := 0
		for _, a := range advertised {
			if a.tool == twin {
				t.Errorf("%s offered %s over a listener; the tools that run a command in a separate process "+
					"are not composed there at all", a.surface, twin)
			}
			if a.tool != editDocumentToolName {
				continue
			}
			seen++
			if strings.Contains(a.description, twin) {
				t.Errorf("%s advertises %s over a listener with a sentence naming %s, which that session does "+
					"not serve:\n%s", a.surface, editDocumentToolName, twin, a.description)
			}
		}
		if seen == 0 {
			t.Fatalf("no listener surface advertised %s at all, so the silence above measures nothing",
				editDocumentToolName)
		}
	})
}
