// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
)

// slotDeclaringCatalog copies the validate fixture catalog and, unless bare,
// gives its Component one required and one optional attachment slot. recipeEdit,
// when it is not nil, rewrites the copied Recipe. It returns the catalog
// directory and the Recipe's path.
func slotDeclaringCatalog(t *testing.T, bare bool, recipeEdit func(string) string) (string, string) {
	t.Helper()
	src, err := filepath.Abs(validateCatalogDir)
	if err != nil {
		t.Fatalf("locate the fixture catalog: %v", err)
	}
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(src)); err != nil {
		t.Fatalf("copy the fixture catalog: %v", err)
	}
	rewrite := func(rel string, edit func(string) string) string {
		path := filepath.Join(dir, rel)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		edited := edit(string(data))
		if edited == string(data) {
			t.Fatalf("the edit to %s changed nothing; the fixture no longer has the shape this test splices into", rel)
		}
		if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
		return path
	}
	if !bare {
		rewrite(strings.TrimPrefix(validateCompPath, validateCatalogDir+"/"), func(s string) string {
			return strings.Replace(s, "spec:\n  schema:",
				"spec:\n  featuresSchema:\n    credential:\n      required: true\n      inject: [env]\n"+
					"    audit:\n      inject: [env]\n  schema:", 1)
		})
	}
	recipe := filepath.Join(dir, strings.TrimPrefix(validateRecipePath, validateCatalogDir+"/"))
	if recipeEdit != nil {
		recipe = rewrite(strings.TrimPrefix(validateRecipePath, validateCatalogDir+"/"), recipeEdit)
	}
	return dir, recipe
}

// TestValidateRecipeDeclinesARequiredAttachmentSlot drives a Recipe through the
// single-entity check when the Component it implements declares a required
// attachment slot. The check attaches no Feature, so nothing it builds can fill
// such a slot; it declines that check, names the slots it declined, and carries
// on through resolution and the isolated render.
//
// The bare row is the control for the report line: a Component declaring no
// required slot gets none. The last row carries one fault of the Recipe's own — a
// stateful Workload with no Headless service — beside the declined slot, and
// proves the decline narrows only the slot requirement: the render still fails on
// the Recipe's fault, and the failure is that fault rather than the slot.
//
// That a Release build still refuses an unfilled required slot is held where
// the rule is evaluated over a real compilation, not here: this test reads only
// the single-entity check.
//
// A fault is matched by the condition it names and by the sentence a reader is
// shown, and never by the error's own Error text. The render's failure reaches
// the command as several nested diagnostics, and Error renders only the
// outermost one — "rendering node" — so the provider's sentence naming the fault
// sits in a frame that text never includes. The condition is read with
// errors.Is, and the sentence through the formatter the command prints with.
// The condition alone would not do: a provider reports several different faults
// under it, and the sentence is what says which one this is.
func TestValidateRecipeDeclinesARequiredAttachmentSlot(t *testing.T) {
	const (
		declinedLine = "Attachments: required attachment slots not checked (credential)"
		unfilled     = "required attachment slot unfilled"
		noHeadless   = "headless governing Service"
	)
	statefulWithoutHeadless := func(s string) string {
		return strings.Replace(s, "    replicas: 1\n", "    form: stateful\n    replicas: 1\n", 1)
	}

	cases := []struct {
		name       string
		bare       bool
		recipeEdit func(string) string
		// wantCondition is the registered condition the row's one fault names, or
		// nil for a row that must pass; wantSentence is the specific sentence the
		// printed diagnostic carries for that fault.
		wantCondition error
		wantSentence  string
		carries       []string
		lacks         []string
	}{
		{name: "a Component declaring no required slot gets no slot line",
			bare:    true,
			carries: []string{"Resolution: valid", "Render: 2 artifact(s)"},
			lacks:   []string{"Attachments:"}},
		{name: "a required slot is declined and named, and the Recipe renders",
			carries: []string{declinedLine, "when a Release that deploys the Component is built", "Resolution: valid", "Render: 2 artifact(s)"},
			// The optional slot is not declined, so it is not named.
			lacks: []string{unfilled, "credential, audit"}},
		{name: "a fault of the Recipe's own is still caught beside a declined slot",
			recipeEdit:    statefulWithoutHeadless,
			wantCondition: plugincondition.ErrPluginRequiredInputUnresolved(),
			wantSentence:  noHeadless,
			carries:       []string{declinedLine, "Resolution: valid", "Render: not completed in isolation"},
			lacks:         []string{unfilled, "Render: 2 artifact(s)"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			catalogDir, recipe := slotDeclaringCatalog(t, tc.bare, tc.recipeEdit)
			var buf bytes.Buffer
			err := dispatchValidate(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), recipe, "", []string{catalogDir}, false, &buf)
			report := buf.String()
			printed := formatCompilerError(err)
			switch {
			case tc.wantCondition == nil && err != nil:
				t.Fatalf("dispatchValidate:\n%s\nreport:\n%s", printed, report)
			case tc.wantCondition != nil && err == nil:
				t.Fatalf("dispatchValidate returned nil; want the fault naming %q\nreport:\n%s", tc.wantCondition, report)
			case tc.wantCondition != nil && !errors.Is(err, tc.wantCondition):
				t.Fatalf("dispatchValidate's error does not name the condition %q:\n%s", tc.wantCondition, printed)
			case tc.wantCondition != nil && !strings.Contains(printed, tc.wantSentence):
				t.Fatalf("the printed diagnostic does not carry %q:\n%s", tc.wantSentence, printed)
			}
			if err != nil && errors.Is(err, analyzer.ErrRequiredSlotUnfilled()) {
				t.Errorf("the error is the declined slot rather than the row's fault:\n%s", printed)
			}
			for _, phrase := range tc.carries {
				if !strings.Contains(report, phrase) {
					t.Errorf("report does not carry %q:\n%s", phrase, report)
				}
			}
			for _, phrase := range tc.lacks {
				if strings.Contains(report, phrase) {
					t.Errorf("report carries %q:\n%s", phrase, report)
				}
			}
		})
	}
}
