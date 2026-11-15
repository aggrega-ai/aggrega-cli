// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

// Drafting single-source parity, tool-protocol leg. The scaffold tool runs the
// shared drafting layer and adds only its own argument decoding and result
// shaping. These prove the adapter surfaces that layer faithfully — the same
// bytes in the text block, the same bytes in the structured payload, the same
// words in the refusal — for the same arguments.
//
// The oracle is a direct call to the layer over the arguments the tool received.
// The daemon's leg is held to the same oracle, so the two surfaces answer
// identically for one request without either test reaching into the other's
// package.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	schemas "github.com/aggrega-ai/aggrega-schemas"

	"github.com/aggrega-ai/aggrega-cli/internal/scaffoldops"
)

// callScaffoldTool invokes the scaffold tool the way tools/call reaches it and
// returns the result envelope.
func callScaffoldTool(t *testing.T, args map[string]any) *callToolResult {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal arguments: %v", err)
	}
	res, rpcErr := draftToolSpec.invoke(nil, context.Background(), raw)
	if rpcErr != nil {
		t.Fatalf("the scaffold tool returned a protocol error envelope: %+v", rpcErr)
	}
	if res == nil {
		t.Fatal("the scaffold tool returned neither a result nor an error")
	}
	if len(res.Content) == 0 {
		t.Fatal("the scaffold tool returned a result with no content block")
	}
	return res
}

// TestScaffoldToolReturnsTheSharedDraft pins that the text block and the
// structured payload both carry exactly what the shared drafting layer produces
// for the same arguments — so a second walker appearing on this surface would
// show up here rather than as a quiet divergence between two surfaces.
func TestScaffoldToolReturnsTheSharedDraft(t *testing.T) {
	for _, kind := range []string{"component", "aggregate", "release", "recipe", "manifest", "feature", "plugin"} {
		t.Run(kind, func(t *testing.T) {
			want, err := scaffoldops.Skeleton(kind, "drafted-name")
			if err != nil {
				t.Fatalf("the oracle refused %q: %v", kind, err)
			}

			res := callScaffoldTool(t, map[string]any{"kind": kind, "name": "drafted-name"})
			if res.IsError {
				t.Fatalf("the tool reported a failure for %q: %s", kind, res.Content[0].Text)
			}
			if res.Content[0].Text != want {
				t.Errorf("the tool's text block is not the shared draft.\n got:\n%s\nwant:\n%s",
					res.Content[0].Text, want)
			}

			var structured scaffoldResult
			if err := json.Unmarshal(res.StructuredContent, &structured); err != nil {
				t.Fatalf("decode structured content: %v (body: %s)", err, res.StructuredContent)
			}
			if structured.Kind != kind {
				t.Errorf("the structured payload reports kind %q for a request about %q", structured.Kind, kind)
			}
			if structured.Skeleton != want {
				t.Errorf("the structured payload is not the shared draft.\n got:\n%s\nwant:\n%s",
					structured.Skeleton, want)
			}
		})
	}
}

// TestScaffoldToolRefusesWithTheSharedGuidance pins the refusal, which is the
// half a parity check over successful calls cannot see: two surfaces can agree
// on every document they produce and still phrase the same rejection
// differently, which is what leaves an assistant and an editor correcting the
// same typo from different advice.
func TestScaffoldToolRefusesWithTheSharedGuidance(t *testing.T) {
	res := callScaffoldTool(t, map[string]any{"kind": "compoent"})
	if !res.IsError {
		t.Fatalf("a kind no schema answers to was answered rather than refused: %s", res.Content[0].Text)
	}
	if got, want := res.Content[0].Text, scaffoldops.UnknownKindGuidance("compoent"); got != want {
		t.Fatalf("the tool's refusal is not the shared guidance.\n got: %s\nwant: %s", got, want)
	}
	for _, kind := range schemas.Kinds() {
		if !strings.Contains(res.Content[0].Text, kind) {
			t.Errorf("the refusal omits the valid kind %q: %s", kind, res.Content[0].Text)
		}
	}
}

// TestScaffoldToolStillRefusesAnAbsentKind is the positive control for the
// argument decoding that stays on this surface: the tool's own message for a
// call that names no kind, which is not the layer's and must not become it.
func TestScaffoldToolStillRefusesAnAbsentKind(t *testing.T) {
	res := callScaffoldTool(t, map[string]any{})
	if !res.IsError {
		t.Fatalf("a call naming no kind was answered: %s", res.Content[0].Text)
	}
	if !strings.Contains(res.Content[0].Text, "requires a 'kind' argument") {
		t.Fatalf("the refusal does not name what the call is missing: %s", res.Content[0].Text)
	}
}

// TestGetSchemaRefusesWithTheSharedGuidance pins the other capability that
// resolves a kind token against the same set. Both reach one rendering of the
// rejection, so an author who mistypes a kind is corrected in the same words
// whichever of the two they were calling.
func TestGetSchemaRefusesWithTheSharedGuidance(t *testing.T) {
	raw, err := json.Marshal(map[string]any{"kind": "compoent"})
	if err != nil {
		t.Fatalf("marshal arguments: %v", err)
	}
	res, rpcErr := getSchemaToolSpec.invoke(nil, context.Background(), raw)
	if rpcErr != nil {
		t.Fatalf("the get-schema tool returned a protocol error envelope: %+v", rpcErr)
	}
	if !res.IsError {
		t.Fatalf("a kind no schema answers to was answered rather than refused: %s", res.Content[0].Text)
	}
	if got, want := res.Content[0].Text, scaffoldops.UnknownKindGuidance("compoent"); got != want {
		t.Fatalf("the two capabilities phrase the same rejection differently.\n got: %s\nwant: %s", got, want)
	}
}

// TestDraftAndScaffoldToolsAnswerAlike pins that the deprecated spelling is the
// drafting tool under a second name and nothing else: for the same arguments
// both names answer with the same text, the same structured payload and the
// same failure flag, refusals included.
func TestDraftAndScaffoldToolsAnswerAlike(t *testing.T) {
	s := newTestServer(t)
	for _, tc := range []struct {
		scenario string
		args     map[string]any
	}{
		{"a kind", map[string]any{"kind": "component"}},
		{"a kind and a seed name", map[string]any{"kind": "release", "name": "drafted-name"}},
		{"a kind no schema answers to", map[string]any{"kind": "compoent"}},
		{"no kind", map[string]any{}},
		{"fields of two forms", map[string]any{"kind": "component", "example": "redis-cache"}},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			draft := callTool(t, s, "draft", tc.args)
			alias := callTool(t, s, "scaffold", tc.args)
			if draft.IsError != alias.IsError {
				t.Errorf("draft reports failure %v and scaffold %v", draft.IsError, alias.IsError)
			}
			if draft.Content[0].Text != alias.Content[0].Text {
				t.Errorf("the text blocks differ.\n draft:\n%s\nscaffold:\n%s", draft.Content[0].Text, alias.Content[0].Text)
			}
			if string(draft.StructuredContent) != string(alias.StructuredContent) {
				t.Errorf("the structured payloads differ.\n draft: %s\nscaffold: %s", draft.StructuredContent, alias.StructuredContent)
			}
		})
	}
}

// TestScaffoldToolDescribedDeprecated pins what a client reads about the two
// names: each listed once, the deprecated one opening with the replacement and
// the release that removes it, followed by the drafting tool's own description,
// and both taking the same arguments.
func TestScaffoldToolDescribedDeprecated(t *testing.T) {
	s := newTestServer(t)
	res, rpcErr := s.handleToolsList(context.Background())
	if rpcErr != nil {
		t.Fatalf("tools/list returned a protocol error: %+v", rpcErr)
	}
	listed := map[string][]tool{}
	for _, tl := range res.(listToolsResult).Tools {
		listed[tl.Name] = append(listed[tl.Name], tl)
	}
	for _, name := range []string{"draft", "scaffold"} {
		if got := len(listed[name]); got != 1 {
			t.Fatalf("tools/list carries %q %d times, want once", name, got)
		}
	}
	draft, alias := listed["draft"][0], listed["scaffold"][0]

	const prefix = "Deprecated: use `draft`; this name is removed in 0.10.0. "
	if !strings.HasPrefix(alias.Description, prefix) {
		t.Errorf("the scaffold tool's description does not open with the deprecation:\n%s", alias.Description)
	}
	if strings.HasPrefix(draft.Description, "Deprecated") {
		t.Errorf("the draft tool is described as deprecated:\n%s", draft.Description)
	}
	if got := strings.TrimPrefix(alias.Description, prefix); got != draft.Description {
		t.Errorf("after the deprecation, the scaffold tool's description is not the draft tool's.\n got:\n%s\nwant:\n%s",
			got, draft.Description)
	}
	if string(alias.InputSchema) != string(draft.InputSchema) {
		t.Errorf("the two names take different arguments.\n draft: %s\nscaffold: %s", draft.InputSchema, alias.InputSchema)
	}
}
