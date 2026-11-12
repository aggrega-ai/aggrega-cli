// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
	schemas "github.com/aggrega-ai/aggrega-schemas"
)

// TestServeSchemaInline proves schema:// serves the canonical JSON Schema for a
// kind, byte-identical to the embedded schema set — the single-sourced schema,
// not a restatement — and reports a resource-not-found for an unknown kind.
func TestServeSchemaInline(t *testing.T) {
	t.Parallel()
	s := &server{out: io.Discard, logw: io.Discard}

	rr, rerr := readResource(t, s, "schema://component")
	if rerr != nil {
		t.Fatalf("resources/read schema://component: %+v", rerr)
	}
	if len(rr.Contents) != 1 {
		t.Fatalf("got %d contents, want 1", len(rr.Contents))
	}
	c := rr.Contents[0]
	if c.MimeType != schemaMimeType {
		t.Errorf("mimeType = %q, want %q", c.MimeType, schemaMimeType)
	}
	want, _ := schemas.SchemaByKind("component")
	if c.Text != want {
		t.Errorf("schema://component body is not the single-sourced schema")
	}

	if _, rerr := readResource(t, s, "schema://does-not-exist"); rerr == nil || rerr.Code != codeResourceNotFound {
		t.Errorf("unknown schema kind error = %+v, want code %d", rerr, codeResourceNotFound)
	}
}

// TestServeCatalogEntity proves catalog:// serves a catalog entity's authored
// YAML verbatim from the resolved workspace, and reports resource-not-found for
// an unrecognised kind segment and an absent entity.
func TestServeCatalogEntity(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	body := "apiVersion: aggrega.ai/v1beta1\n" +
		"kind: Component\n" +
		"metadata:\n" +
		"  name: core/compute/api-server\n" +
		"  version: \"1.0.0\"\n" +
		"  labels:\n" +
		"    component.aggrega.ai/type: \"server\"\n" +
		"spec:\n" +
		"  inject: [\"env\"]\n" +
		"  schema:\n" +
		"    infra:\n" +
		"      port: { type: integer, default: 8080 }\n" +
		"    provides:\n" +
		"      host: { type: string, required: true }\n"
	// The entity sits in the conventional catalog directory rather than loose at
	// the workspace root, which is the shape a reader is told to author: the
	// search stack reads that directory without being told to, and a root holding
	// entities directly is read only when the reader lists it themselves.
	catalogDir := filepath.Join(ws, workspaceconfig.ConventionalCatalogDir)
	if err := os.MkdirAll(catalogDir, 0o755); err != nil {
		t.Fatalf("mkdir the conventional catalog directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(catalogDir, "component.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}
	s := &server{out: io.Discard, logw: io.Discard, launchRoot: ws}

	rr, rerr := readResource(t, s, "catalog://component/core/compute/api-server")
	if rerr != nil {
		t.Fatalf("resources/read catalog entity: %+v", rerr)
	}
	if len(rr.Contents) != 1 || rr.Contents[0].Text != body {
		t.Fatalf("catalog content = %+v, want verbatim authored YAML", rr.Contents)
	}
	if rr.Contents[0].MimeType != catalogMimeType {
		t.Errorf("mimeType = %q, want %q", rr.Contents[0].MimeType, catalogMimeType)
	}

	for _, uri := range []string{
		"catalog://component/core/compute/absent",
		"catalog://notakind/core/compute/probe",
		"catalog://component",
	} {
		if _, rerr := readResource(t, s, uri); rerr == nil || rerr.Code != codeResourceNotFound {
			t.Errorf("resources/read %s error = %+v, want code %d", uri, rerr, codeResourceNotFound)
		}
	}
}

// TestServeCliHelp proves cli:// resolves a command path to that command's help
// through the renderer seam, that the empty path serves the top-level help, and
// that a flag-shaped or traversal segment is rejected without the renderer ever
// being reached.
//
// The call count is what separates "asked for the top-level command" from "not
// asked at all": both carry an empty path, so reading the path alone could not
// tell the root read from a URI the validation was supposed to stop.
func TestServeCliHelp(t *testing.T) {
	t.Parallel()
	var gotPath []string
	var calls int
	s := &server{
		out:  io.Discard,
		logw: io.Discard,
		renderHelp: func(path []string) (string, bool) {
			calls++
			gotPath = path
			return "USAGE: aggrega " + strings.Join(path, " "), true
		},
	}

	rr, rerr := readResource(t, s, "cli://plugin/gen-catalog")
	if rerr != nil {
		t.Fatalf("resources/read cli://plugin/gen-catalog: %+v", rerr)
	}
	if want := []string{"plugin", "gen-catalog"}; !equalStrings(gotPath, want) {
		t.Errorf("cli command path = %v, want %v", gotPath, want)
	}
	if rr.Contents[0].MimeType != cliMimeType {
		t.Errorf("mimeType = %q, want %q", rr.Contents[0].MimeType, cliMimeType)
	}
	if !strings.Contains(rr.Contents[0].Text, "USAGE") {
		t.Errorf("cli help text not served: %q", rr.Contents[0].Text)
	}

	calls, gotPath = 0, []string{"unset"}
	if _, rerr := readResource(t, s, "cli://"); rerr != nil {
		t.Fatalf("resources/read cli:// (root): %+v", rerr)
	}
	if calls != 1 {
		t.Errorf("the root read reached the renderer %d time(s), want 1", calls)
	}
	if len(gotPath) != 0 {
		t.Errorf("root cli command path = %v, want the empty path", gotPath)
	}

	// A flag-shaped or traversal segment must never reach the seam.
	for _, uri := range []string{"cli://-rf", "cli://../etc", "cli://plugin/--output"} {
		calls = 0
		if _, rerr := readResource(t, s, uri); rerr == nil || rerr.Code != codeResourceNotFound {
			t.Errorf("resources/read %s error = %+v, want code %d", uri, rerr, codeResourceNotFound)
		}
		if calls != 0 {
			t.Errorf("resources/read %s reached the renderer; it must be rejected before that", uri)
		}
	}

	// A path the renderer does not resolve is the same answer an unrunnable
	// command was: the resource is not there.
	s.renderHelp = func([]string) (string, bool) { return "", false }
	if _, rerr := readResource(t, s, "cli://not-a-command"); rerr == nil || rerr.Code != codeResourceNotFound {
		t.Errorf("resources/read cli://not-a-command error = %+v, want code %d", rerr, codeResourceNotFound)
	}
}

// TestPromptsRenderReferenceResources proves the prompts render with kind
// arguments, that their messages point at resources by URI rather than inlining
// content, and that the per-kind template's required kind is enforced.
func TestPromptsRenderReferenceResources(t *testing.T) {
	t.Parallel()

	dev := getPrompt(t, "developer-authoring", map[string]string{"kind": "component"})
	body := dev.Messages[0].Content.Text
	for _, want := range []string{"schema://component", "langref://entities/component", "authoring://getting-started", "scaffold", "validate"} {
		if !strings.Contains(body, want) {
			t.Errorf("developer-authoring body missing pointer %q", want)
		}
	}
	// The body references resources and tools; it must not carry raw schema text.
	if strings.Contains(body, "\"$schema\"") || strings.Contains(body, "\"properties\"") {
		t.Errorf("developer-authoring body inlines schema content")
	}

	// Default kind applies when the argument is absent.
	arch := getPrompt(t, "architect-authoring", nil)
	if !strings.Contains(arch.Messages[0].Content.Text, "schema://component") {
		t.Errorf("architect-authoring default kind did not resolve to component")
	}

	// The per-kind template requires a kind.
	if _, rerr := renderPrompt("author-entity", nil); rerr == nil || rerr.Code != codeInvalidParams {
		t.Errorf("author-entity without kind error = %+v, want code %d", rerr, codeInvalidParams)
	}
	// An unknown kind is rejected against the addressable schema set.
	if _, rerr := renderPrompt("author-entity", map[string]string{"kind": "nonsense"}); rerr == nil || rerr.Code != codeInvalidParams {
		t.Errorf("author-entity unknown kind error = %+v, want code %d", rerr, codeInvalidParams)
	}
	// A valid kind renders and points at that kind's resources.
	rel := getPrompt(t, "author-entity", map[string]string{"kind": "release"})
	if !strings.Contains(rel.Messages[0].Content.Text, "schema://release") {
		t.Errorf("author-entity release body missing schema://release pointer")
	}
}

// getPrompt renders a prompt and fails the test on any error.
func getPrompt(t *testing.T, name string, args map[string]string) getPromptResult {
	t.Helper()
	res, rerr := renderPrompt(name, args)
	if rerr != nil {
		t.Fatalf("prompts/get %s: %+v", name, rerr)
	}
	if len(res.Messages) == 0 {
		t.Fatalf("prompts/get %s returned no messages", name)
	}
	return res
}

// renderPrompt invokes prompts/get for name with args and returns the parsed
// result.
func renderPrompt(name string, args map[string]string) (getPromptResult, *rpcError) {
	params, err := json.Marshal(getPromptParams{Name: name, Arguments: args})
	if err != nil {
		return getPromptResult{}, &rpcError{Code: codeInternalError, Message: err.Error()}
	}
	res, rerr := handlePromptsGet(params)
	if rerr != nil {
		return getPromptResult{}, rerr
	}
	gp, ok := res.(getPromptResult)
	if !ok {
		return getPromptResult{}, &rpcError{Code: codeInternalError, Message: "prompts/get returned wrong type"}
	}
	return gp, nil
}
