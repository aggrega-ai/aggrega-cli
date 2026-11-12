// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package validateops

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// TestValidateRecipe_AStoppedRenderCarriesItsConditionsShortCode drives Recipes
// whose isolated render stops on an assertion through the single-entity check, and
// reads the one-line text every surface of the check reports the stop in — the
// warning a failed assertion becomes, and the report line naming why a render was
// set aside. Each carries the assertion condition's short code in its head, the
// token a build's own halt opens with, so a reader can look the condition up from
// the check as they can from a build.
//
// The code is read off the condition's own accessor rather than written out, so a
// renumbered condition cannot leave this test asserting a stale code.
func TestValidateRecipe_AStoppedRenderCarriesItsConditionsShortCode(t *testing.T) {
	t.Parallel()
	code, ok := cerrors.ShortCodeOf(renderer.ErrAssertionFailed())
	if !ok || code == "" {
		t.Fatal("the assertion condition reports no short code")
	}
	head := regexp.MustCompile(`\[[A-Z_]+ ` + regexp.QuoteMeta(code) + `\] `)

	t.Run("a failed assertion's warning", func(t *testing.T) {
		t.Parallel()
		recipeText, _ := buildOfOneRecipe("{{ .infra.image }}", false, true)
		res, err := validateProbe(t, recipeText)
		if err != nil {
			t.Fatalf("ValidateEntity: %v", err)
		}
		if len(res.Warnings) != 1 {
			t.Fatalf("warnings = %q, want the one failed assertion\nreport:\n%s", res.Warnings, strings.Join(res.Diagnostics, "\n"))
		}
		if text := res.Warnings[0].Error(); !head.MatchString(text) {
			t.Errorf("the warning carries no head naming the assertion condition %s:\n%s", code, text)
		}
	})

	t.Run("a render set aside after an assertion stopped it", func(t *testing.T) {
		t.Parallel()
		recipeText, _ := buildOfOneRecipe("{{ .infra.image }}:{{ .context.inject.env.TAG }}", false, false)
		recipeText += "  assertions:\n    reads_absent: '{{ .infra.nosuchkey }}'\n"
		res, err := validateProbe(t, recipeText)
		if err != nil {
			t.Fatalf("ValidateEntity: %v", err)
		}
		var stopped string
		for _, line := range res.Diagnostics {
			if strings.HasPrefix(line, "  The render stopped: ") {
				stopped = line
			}
		}
		if stopped == "" {
			t.Fatalf("the report names no stopped render:\n%s", strings.Join(res.Diagnostics, "\n"))
		}
		if !head.MatchString(stopped) {
			t.Errorf("the stopped-render line carries no head naming the assertion condition %s:\n%s", code, stopped)
		}
	})
}

// validateProbe writes recipeText into the probe catalog and runs the
// single-entity check over it.
func validateProbe(t *testing.T, recipeText string) (EntityResult, error) {
	t.Helper()
	dir, recipe := writeProbeCatalog(t, recipeText)
	root := filepath.Dir(dir)
	return ValidateEntity(context.Background(), root, filepath.Join(root, "cache"), []string{dir}, nil, recipe, false)
}
