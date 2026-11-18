// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package validateops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"

	"github.com/aggrega-ai/aggrega-cli/internal/embeddedentities"
)

// writeAssertionCatalog writes component and recipe into a fresh catalog
// directory, at the paths writeProbeCatalog uses, and returns the directory and
// the Recipe's path.
func writeAssertionCatalog(t *testing.T, component, recipe string) (string, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "catalog")
	files := map[string]string{
		"component/example/compute/probe/1.0.0/component.yaml": component,
		"recipe/helm/probe/1.0.0/recipe.yaml":                  recipe,
	}
	for rel, content := range files {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return dir, filepath.Join(dir, "recipe/helm/probe/1.0.0/recipe.yaml")
}

// withAssertion appends one named assertion to an entity whose last top-level
// block sits under spec, as the probe Component and the probe Recipe both do.
func withAssertion(entity, name, body string) string {
	return entity + "  assertions:\n    " + name + ": '" + body + "'\n"
}

// TestValidateReportsAFailedAssertionAsAWarning drives the single-entity check
// over a Recipe and the Component it implements, each carrying an assertion over
// infra a Release sets, and reads how the check reports it.
//
// The check writes its own Release, so an assertion over a value a Release sets
// fails there whether or not the entity is sound. That failure is a warning: the
// check passes, the warning carries the assertion's own sentence and the one
// saying a Release must satisfy it, and its chain still names the condition — and,
// for the Recipe's assertion, the key it is written under in the Recipe. The
// Recipe's assertion stops the render; the Component's stops resolution, so the
// render is reported as not attempted.
//
// The last two rows hold the other side, one fault each: a template fault beside
// an assertion that holds is still the check's failure and no warning, and a
// Recipe whose assertion holds renders with no warning — the positive control
// showing that the assertion, and nothing else in the fixture, is what the first
// rows report.
func TestValidateReportsAFailedAssertionAsAWarning(t *testing.T) {
	embeddedentities.Require(t)
	const (
		rendersImage = "{{ .infra.image }}:{{ .infra.tag }}"
		noteSupplied = `{{ if not (optional .infra "note" "") }}{{ fail "set infra.note in the Release" }}{{ end }}`
		tagPinned    = `{{ if eq .infra.tag "1.0" }}{{ fail "pin infra.tag in the Release" }}{{ end }}`
		alwaysHolds  = `{{ if false }}{{ fail "never" }}{{ end }}`
	)
	cases := []struct {
		name      string
		component string
		recipe    string
		// wantWarning is the assertion's own sentence the one warning must carry, or
		// "" for a row that must report none; wantLocator is the key the warning's
		// chain must state in the Recipe, or "" where it states none.
		wantWarning string
		wantLocator string
		// wantErr is whether the check fails.
		wantErr       bool
		reportCarries []string
		reportLacks   []string
		wantArtifacts bool
	}{
		{
			name:        "a Recipe assertion over infra a Release sets is a warning on its own key",
			component:   probeComponent,
			recipe:      withAssertion(probeRecipe(rendersImage, ""), "note_supplied", noteSupplied),
			wantWarning: "set infra.note in the Release",
			wantLocator: ".spec.assertions.note_supplied",
			reportCarries: []string{
				"Resolution: valid",
				"Render: stopped on a failed assertion, reported as a warning",
			},
			reportLacks: []string{"Render: not completed in isolation"},
		},
		{
			name:        "a Component assertion over infra a Release sets is a warning, and the render is not attempted",
			component:   withAssertion(probeComponent, "tag_pinned", tagPinned),
			recipe:      probeRecipe(rendersImage, ""),
			wantWarning: "pin infra.tag in the Release",
			reportCarries: []string{
				"Resolution: stopped on a failed assertion, reported as a warning",
				"Render: not attempted (the run stopped on that assertion)",
			},
			reportLacks: []string{"Resolution: valid"},
		},
		{
			name:          "a template fault beside an assertion that holds is still the check's failure",
			component:     probeComponent,
			recipe:        withAssertion(probeRecipe("{{ .infra.imge.repo }}:{{ .infra.tag }}", ""), "always_holds", alwaysHolds),
			wantErr:       true,
			reportCarries: []string{"Resolution: valid", "Render: not completed in isolation"},
			reportLacks:   []string{"reported as a warning"},
		},
		{
			name:          "a Recipe whose assertion holds renders with no warning",
			component:     probeComponent,
			recipe:        withAssertion(probeRecipe(rendersImage, ""), "always_holds", alwaysHolds),
			reportCarries: []string{"Resolution: valid", "Render: 2 artifact(s)"},
			reportLacks:   []string{"reported as a warning"},
			wantArtifacts: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, recipe := writeAssertionCatalog(t, tc.component, tc.recipe)
			root := filepath.Dir(dir)
			res, err := ValidateEntity(context.Background(), root, filepath.Join(root, "cache"),
				[]string{dir}, nil, recipe, false)
			report := strings.Join(res.Diagnostics, "\n")

			switch {
			case tc.wantErr && err == nil:
				t.Fatalf("ValidateEntity returned nil; want the check's failure\nreport:\n%s", report)
			case !tc.wantErr && err != nil:
				t.Fatalf("ValidateEntity: %v\nreport:\n%s", err, report)
			}
			if tc.wantErr {
				var stopped *renderStopError
				if !errors.As(err, &stopped) {
					t.Errorf("the failure is not a render stop, so the row did not reach the render:\n%v", err)
				}
				if errors.Is(err, renderer.ErrAssertionFailed()) {
					t.Errorf("the failure names a failed assertion, so the row does not carry the fault it is about:\n%v", err)
				}
			}
			if res.Valid == tc.wantErr {
				t.Errorf("Valid = %v with error %v", res.Valid, err)
			}
			if got := len(res.Artifacts) > 0; got != tc.wantArtifacts {
				t.Errorf("rendered artifacts = %d, want artifacts = %v\nreport:\n%s", len(res.Artifacts), tc.wantArtifacts, report)
			}
			for _, line := range tc.reportCarries {
				if !strings.Contains(report, line) {
					t.Errorf("the report does not carry %q:\n%s", line, report)
				}
			}
			for _, line := range tc.reportLacks {
				if strings.Contains(report, line) {
					t.Errorf("the report carries %q:\n%s", line, report)
				}
			}

			if tc.wantWarning == "" {
				if len(res.Warnings) != 0 {
					t.Errorf("warnings = %q, want none", res.Warnings)
				}
				return
			}
			if len(res.Warnings) != 1 {
				t.Fatalf("warnings = %q, want the one failed assertion\nreport:\n%s", res.Warnings, report)
			}
			warning := res.Warnings[0]
			if !errors.Is(warning, renderer.ErrAssertionFailed()) {
				t.Errorf("the warning does not name the failed-assertion condition:\n%v", warning)
			}
			text := warning.Error()
			for _, want := range []string{tc.wantWarning, assertionAwaitsRelease} {
				if !strings.Contains(text, want) {
					t.Errorf("the warning's text does not carry %q:\n%s", want, text)
				}
			}
			if !strings.HasSuffix(text, assertionAwaitsRelease) {
				t.Errorf("the warning's text does not close with the sentence saying a Release must satisfy it:\n%s", text)
			}
			at, located := cerrors.LocatedAt(warning)
			if tc.wantLocator == "" {
				return
			}
			if !located || at.File != recipe || at.Locator != tc.wantLocator {
				t.Errorf("the warning is located at %+v (located %v); want %s in %s", at, located, tc.wantLocator, recipe)
			}
		})
	}
}

// TestValidateHaltsOnAnAssertionReadingAMissingKey holds the line between the two
// stops an assertion has, both of which carry the assertion condition: one that
// calls fail is a warning, because the check cannot supply what a Release sets,
// and one that stops on a key it reads but does not find is the check's failure,
// because that is a fault in the assertion's own text. One fault per row; the
// fail row is the control showing the same fixture otherwise reports a warning.
//
// INVARIANT: a missing-key stop fails the check under the assertion condition,
// and a fail call is a warning.
// CONSEQUENCE: were the missing-key stop reported as a warning, a misspelt key in
// an assertion would pass the check it exists to catch.
func TestValidateHaltsOnAnAssertionReadingAMissingKey(t *testing.T) {
	const rendersImage = "{{ .infra.image }}:{{ .infra.tag }}"
	for _, tc := range []struct {
		name    string
		body    string
		wantErr bool
	}{
		{"an assertion calling fail is a warning (control)", `{{ fail "set infra.note in the Release" }}`, false},
		{"an assertion reading a key nothing supplies fails the check", `{{ if .infra.nosuchkey }}{{ end }}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, recipe := writeAssertionCatalog(t, probeComponent,
				withAssertion(probeRecipe(rendersImage, ""), "probe", tc.body))
			root := filepath.Dir(dir)
			res, err := ValidateEntity(context.Background(), root, filepath.Join(root, "cache"),
				[]string{dir}, nil, recipe, false)
			if !tc.wantErr {
				if err != nil || len(res.Warnings) != 1 {
					t.Fatalf("want one warning and no failure; got err %v, warnings %q", err, res.Warnings)
				}
				return
			}
			if err == nil {
				t.Fatalf("the check passed an assertion reading a key nothing supplies; warnings %q", res.Warnings)
			}
			if !errors.Is(err, renderer.ErrAssertionFailed()) {
				t.Errorf("the failure does not carry the assertion condition: %v", err)
			}
			if renderer.AssertionCalledFail(err) {
				t.Errorf("a missing-key stop was read as a fail call: %v", err)
			}
			if len(res.Warnings) != 0 || res.Valid {
				t.Errorf("a missing-key stop was reported as a warning: valid %v, warnings %q", res.Valid, res.Warnings)
			}
			if !strings.Contains(err.Error(), `map has no entry for key "nosuchkey"`) &&
				!strings.Contains(cerrors.CauseChain(err), `map has no entry for key "nosuchkey"`) {
				t.Errorf("the failure does not name the key: %v", cerrors.CauseChain(err))
			}
		})
	}
}

// TestAssertionWarningClosesTheStopsSentence reads the one piece of composition
// the warning's text carries: the stop's own text is closed as a sentence, then
// the sentence saying a Release must satisfy the assertion follows, whether or not
// the assertion's author ended their sentence with a full stop.
func TestAssertionWarningClosesTheStopsSentence(t *testing.T) {
	cases := []struct {
		name string
		stop error
		want string
	}{
		{
			name: "an author's sentence with no full stop is closed",
			stop: errors.New("assertion failed: set infra.note in the Release"),
			want: "assertion failed: set infra.note in the Release. " + assertionAwaitsRelease,
		},
		{
			name: "an author's sentence ending in a full stop is not closed twice",
			stop: errors.New("assertion failed: set infra.note in the Release."),
			want: "assertion failed: set infra.note in the Release. " + assertionAwaitsRelease,
		},
		{
			name: "every cause beneath the stop's own frame is written out",
			stop: cerrors.Wrap(errors.New("assertion failed: pin infra.tag"), cerrors.DeveloperError,
				cerrors.SourceLocation{}, "Component/Feature assertions"),
			want: "[DEVELOPER_ERROR] Component/Feature assertions: assertion failed: pin infra.tag. " + assertionAwaitsRelease,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := (&assertionWarning{stop: tc.stop}).Error()
			if got != tc.want {
				t.Errorf("warning text =\n  %q\nwant\n  %q", got, tc.want)
			}
		})
	}
}
