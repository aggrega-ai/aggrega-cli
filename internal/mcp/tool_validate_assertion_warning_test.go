// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/validateops"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// imageChosenAssertion is an assertion over the infra field the validate
// fixture's Component defaults, which only a Release sets to anything else: the
// single-entity check writes its own Release, so the assertion fails there.
const imageChosenAssertion = "  assertions:\n" +
	`    image_chosen: '{{ if eq .infra.image "nginx:alpine" }}{{ fail "choose an image by setting infra.image in the Release" }}{{ end }}'` + "\n"

// TestTheValidateToolReportsAFailedAssertionAsAWarning asks the entity scope about
// a Recipe whose assertion fails only because no Release set what it reads, and
// holds the answer to the core's own: the check reports the assertion as a
// warning rather than as its verdict, so the tool answers valid with no
// diagnostic, and the warning travels in notices — in the core's words, closed by
// the sentence saying a Release must satisfy the assertion — and in the text half.
//
// The fixture is the command line's validate catalog copied, with the assertion
// written into its Recipe and nothing else changed, so the assertion is the one
// thing the answer can be reporting.
func TestTheValidateToolReportsAFailedAssertionAsAWarning(t *testing.T) {
	root := t.TempDir()
	catalogDir := filepath.Join(root, "catalog")
	if err := os.CopyFS(catalogDir, os.DirFS(filepath.Join(moduleRoot(t), "cmd", "aggrega", "testdata", "validate", "catalog"))); err != nil {
		t.Fatalf("copy the validate fixture catalog: %v", err)
	}
	// The workspace block anchors the root here rather than letting discovery walk
	// up into the checkout the suite runs inside.
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte("workspace: {}\n"), 0o644); err != nil {
		t.Fatalf("write workspace config: %v", err)
	}
	recipe := filepath.Join(catalogDir, "recipe", "helm", "service", "1.0.0", "recipe.yaml")
	authored, err := os.ReadFile(recipe)
	if err != nil {
		t.Fatalf("read the fixture Recipe: %v", err)
	}
	const anchor = "  implements: example/compute/service\n"
	if !strings.Contains(string(authored), anchor) {
		t.Fatalf("the fixture Recipe no longer carries %q, so the assertion would be written into the wrong place", anchor)
	}
	asserted := strings.Replace(string(authored), anchor, anchor+imageChosenAssertion, 1)
	if err := os.WriteFile(recipe, []byte(asserted), 0o644); err != nil {
		t.Fatalf("write the asserted Recipe: %v", err)
	}

	s := newTestServer(t)
	dirs := []string{catalogDir}
	tw := resolvedToolWorkspace(t, s, root, dirs)
	core, coreErr := validateops.ValidateEntity(context.Background(), tw.root, tw.cacheDir, tw.dirs, tw.named,
		resolvedEntityPath(t, tw, recipe), false)
	if coreErr != nil {
		t.Fatalf("the core failed the Recipe rather than warning about its assertion: %v", coreErr)
	}
	if len(core.Warnings) != 1 || !errors.Is(core.Warnings[0], renderer.ErrAssertionFailed()) {
		t.Fatalf("the core reports %q; want the one failed assertion as a warning", core.Warnings)
	}
	warning := core.Warnings[0].Error()

	res := callTool(t, s, "validate", validateArgs{Entity: recipe, WorkspaceRoot: root, CatalogDirs: dirs})
	if res.IsError {
		t.Fatalf("validate entity scope reported error: %s", res.Content[0].Text)
	}
	payload := unmarshalValidatePayload(t, res)
	if !payload.Valid || len(payload.Diagnostics) != 0 {
		t.Fatalf("the answer is %+v; want valid with no diagnostic", payload)
	}
	if !slices.Contains(payload.Notices, warning) {
		t.Errorf("notices do not carry the core's warning verbatim.\n--- want ---\n%s\n--- notices ---\n%q", warning, payload.Notices)
	}
	text := res.Content[0].Text
	for _, want := range []string{
		"choose an image by setting infra.image in the Release",
		"A Release must satisfy this assertion before a build of it can pass.",
		"Render: stopped on a failed assertion, reported as a warning",
	} {
		if !strings.Contains(warning+"\n"+strings.Join(core.Diagnostics, "\n"), want) {
			t.Errorf("the core's answer does not carry %q, so this row would hold the tool to nothing", want)
		}
		if !strings.Contains(text, want) {
			t.Errorf("the text half does not carry %q:\n%s", want, text)
		}
	}
}
