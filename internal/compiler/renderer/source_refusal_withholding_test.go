// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for what a refusal raised while resolving a remote source may say when a
// coordinate it names was produced by a template reading a value an author declared
// sensitive. Each row resolves the same source twice — once with the origin the
// render hands the resolution, once with an origin declaring nothing — so a row
// whose fixture never reached a coordinate-bearing refusal fails on its control
// rather than passing on its own.
package renderer

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

const (
	// sourceSecret is what a template produced from the declared value; sourceNote
	// what one produced from a value nobody declared.
	sourceSecret = "s0urce-secret-plaintext"
	sourceNote   = "s0urce-note-plaintext"
	sourceURL    = "oci://example.test/charts/app"
	sourceDoc    = "/ws/catalog/recipe/helm/app/1.0.0/recipe.yaml"
)

// sourceWithheldKeys is the set the source's templates were evaluated against.
func sourceWithheldKeys() redact.Keys {
	return redact.Keys{Values: map[string]bool{"adminPassword": true}}
}

// emptyLockInput is a render input whose lockfile pins nothing, so every remote
// source halts at the lookup with the lookup's own text.
func emptyLockInput(t *testing.T) RendererInput {
	t.Helper()
	return RendererInput{
		Lockfile:      &lockfile.Lockfile{Version: 1, Release: "./r.yaml", GeneratedAt: "2026-05-22T10:00:00Z"},
		WorkspaceRoot: t.TempDir(),
		CacheDir:      ".aggrega/cache",
	}
}

// disagreeingPinInput is a render input whose lockfile records the source at one
// digest while the pin recorded beside the entity records another, so the
// resolution halts at the pin comparison.
func disagreeingPinInput(t *testing.T, chart, version string) RendererInput {
	t.Helper()
	return RendererInput{
		Lockfile: &lockfile.Lockfile{
			Version: 1, Release: "./r.yaml", GeneratedAt: "2026-05-22T10:00:00Z",
			SourceArtifacts: []lockfile.SourceArtifact{{
				Kind: "helm-oci-chart", URL: sourceURL, Chart: chart, PinnedVersion: version,
				Digest:    "sha256:1111111111111111111111111111111111111111111111111111111111111111",
				Mediatype: "application/vnd.cncf.helm.chart.content.v1.tar+gzip",
			}},
		},
		WorkspaceRoot: t.TempDir(),
		CacheDir:      ".aggrega/cache",
		RecordedPins: recipepin.Lookup{sourceDoc: recipepin.Pins{SourceDigests: []types.SourceDigest{{
			Kind: "helm-oci-chart", URL: sourceURL, Chart: chart, Version: version,
			Digest: "sha256:2222222222222222222222222222222222222222222222222222222222222222",
		}}}},
	}
}

// TestResolveEntitySource_WithholdsACoordinateATemplateProducedFromADeclaredValue
// is the table. A withheld row must name no value a template produced from the
// declared one, read as want, keep the refusal's condition, and still be routed and
// advised as that condition is; a byte-identical row must produce exactly the
// refusal the same resolution produces with nothing declared.
func TestResolveEntitySource_WithholdsACoordinateATemplateProducedFromADeclaredValue(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		input         func(t *testing.T) RendererInput
		authored      any // the source as its author wrote it
		produced      any // what its templates produced
		secret        string
		byteIdentical bool
		want          string
		condition     error
	}{
		{
			name:  "a version a template produced from the declared value, refused by the lookup",
			input: emptyLockInput,
			authored: map[string]any{"kind": "helm-oci-chart", "url": sourceURL,
				"version": "{{ .values.adminPassword }}"},
			produced:  map[string]any{"kind": "helm-oci-chart", "url": sourceURL, "version": sourceSecret},
			secret:    sourceSecret,
			want:      "source-artifact " + sourceURL + "@" + redact.Placeholder() + ": " + sourceRefusalWithheld,
			condition: oci.ErrFetchNotFound(),
		},
		{
			// The chart is not part of the name, but the lookup's own text quotes
			// it, so the reason is withheld while the name keeps what nothing
			// declared produced.
			name:  "a chart a template produced from the declared value, refused by the lookup",
			input: emptyLockInput,
			authored: map[string]any{"kind": "helm-oci-chart", "url": sourceURL, "version": "1.0.0",
				"chart": "{{ .values.adminPassword }}"},
			produced:  map[string]any{"kind": "helm-oci-chart", "url": sourceURL, "version": "1.0.0", "chart": sourceSecret},
			secret:    sourceSecret,
			want:      "source-artifact " + sourceURL + "@1.0.0: " + sourceRefusalWithheld,
			condition: oci.ErrFetchNotFound(),
		},
		{
			name:      "a source written as one string a template produced from the declared value",
			input:     emptyLockInput,
			authored:  "oci://example.test/{{ .values.adminPassword }}/app",
			produced:  "oci://example.test/" + sourceSecret + "/app",
			secret:    sourceSecret,
			want:      "source-artifact " + redact.Placeholder() + ": " + sourceRefusalWithheld,
			condition: oci.ErrFetchNotFound(),
		},
		{
			// Refused before any coordinate exists, so nothing names the source.
			name:      "the evaluation refusing what a template produced from the declared value",
			input:     emptyLockInput,
			authored:  "https://example.test/{{ .values.adminPassword }}",
			produced:  "https://example.test/" + sourceSecret,
			secret:    sourceSecret,
			want:      sourceRefusalWithheld,
			condition: closure.ErrSourceTemplateAtLockTime(),
		},
		{
			name:  "a version a template produced from the declared value, refused by the pin comparison",
			input: func(t *testing.T) RendererInput { return disagreeingPinInput(t, "", sourceSecret) },
			authored: map[string]any{"kind": "helm-oci-chart", "url": sourceURL,
				"version": "{{ .values.adminPassword }}"},
			produced:  map[string]any{"kind": "helm-oci-chart", "url": sourceURL, "version": sourceSecret},
			secret:    sourceSecret,
			want:      "source-artifact " + sourceURL + "@" + redact.Placeholder() + ": " + sourceRefusalWithheld,
			condition: oci.ErrFetchIntegrity(),
		},
		{
			// The pin comparison names the source by its url and version alone, so a
			// chart produced from the declared value is not in its text.
			name:  "a chart a template produced from the declared value, refused by the pin comparison",
			input: func(t *testing.T) RendererInput { return disagreeingPinInput(t, sourceSecret, "1.0.0") },
			authored: map[string]any{"kind": "helm-oci-chart", "url": sourceURL, "version": "1.0.0",
				"chart": "{{ .values.adminPassword }}"},
			produced:      map[string]any{"kind": "helm-oci-chart", "url": sourceURL, "version": "1.0.0", "chart": sourceSecret},
			byteIdentical: true,
		},
		{
			name:  "a version a template produced from a value nobody declared",
			input: emptyLockInput,
			authored: map[string]any{"kind": "helm-oci-chart", "url": sourceURL,
				"version": "{{ .values.adminNote }}"},
			produced:      map[string]any{"kind": "helm-oci-chart", "url": sourceURL, "version": sourceNote},
			byteIdentical: true,
		},
		{
			// A template handing on the whole of its data reads the declared value
			// whatever it then prints — here it prints only the undeclared note, so
			// the coordinate withheld is one nothing declared actually reached: the
			// direction that costs a reader a coordinate rather than printing one.
			name:  "a version a template produced while handing on the whole of its data",
			input: emptyLockInput,
			authored: map[string]any{"kind": "helm-oci-chart", "url": sourceURL,
				"version": "{{ .values.adminNote }}{{ (toJson .) | trunc 0 }}"},
			produced:  map[string]any{"kind": "helm-oci-chart", "url": sourceURL, "version": sourceNote},
			secret:    sourceNote,
			want:      "source-artifact " + sourceURL + "@" + redact.Placeholder() + ": " + sourceRefusalWithheld,
			condition: oci.ErrFetchNotFound(),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			resolve := func(origin sourceOrigin) error {
				_, err := resolveEntitySource(c.input(t), c.produced, types.OrderedMap[string]{}, "app:1.0.0",
					map[string]any{}, map[string]any{}, sourceDoc, origin)
				if err == nil {
					t.Fatalf("the source resolved; the fixture must refuse, or every assertion below is vacuous")
				}
				return wrapSourceResolutionError(err, "app", `node "app" source resolution`)
			}
			got := resolve(sourceOrigin{authored: c.authored, withheld: sourceWithheldKeys()})
			plain := resolve(sourceOrigin{})
			gotText, plainText := cerrors.CauseChain(got), cerrors.CauseChain(plain)
			if c.byteIdentical {
				if gotText != plainText {
					t.Errorf("a refusal naming no coordinate a declared value produced must be byte-identical:\nwithheld: %s\nplain:    %s", gotText, plainText)
				}
				return
			}
			if !strings.Contains(plainText, c.secret) {
				t.Fatalf("the control does not quote the produced coordinate, so the row proves nothing: %s", plainText)
			}
			if strings.Contains(gotText, c.secret) {
				t.Errorf("the refusal carries a coordinate the declared value produced: %s", gotText)
			}
			if !strings.Contains(gotText, c.want) {
				t.Errorf("the refusal must read %q:\n%s", c.want, gotText)
			}
			if !errors.Is(got, c.condition) {
				t.Errorf("the refusal lost its condition %v: %s", c.condition, gotText)
			}
			var withheldEnv, plainEnv *cerrors.CompilerError
			if !errors.As(got, &withheldEnv) || !errors.As(plain, &plainEnv) {
				t.Fatalf("both refusals must arrive wrapped by the source resolution's frame")
			}
			if withheldEnv.Category() != plainEnv.Category() {
				t.Errorf("the refusal is routed as %q where the same refusal unwithheld is %q", withheldEnv.Category(), plainEnv.Category())
			}
		})
	}
}
