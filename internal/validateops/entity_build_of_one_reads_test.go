// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validateops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// renderOutcome is what the single-entity check made of a Recipe's isolated
// render.
type renderOutcome int

const (
	// rendered is a render that completed: the check passes and carries the
	// rendered artifacts.
	rendered renderOutcome = iota
	// notAttempted is a render the check set aside: it passes, carries no
	// artifact, and names the reads a build of one cannot supply.
	notAttempted
	// failed is a render stop the check reports: it fails with a render stop.
	failed
	// warned is a render that stopped on a failed assertion: it passes, carries
	// no artifact, names no reads, and reports the assertion as its one warning.
	warned
)

// buildOfOneWorkload is the probe Recipe's workload with the given image
// template, in the shape the probe Component's Recipe renders.
func buildOfOneWorkload(image string) string {
	return `apiVersion: aggrega.ai/v1alpha1
kind: Workload
replicas: 1
containers:
  main:
    image: "` + image + `"
    ports:
      http: 8080
services:
  default:
    type: ClusterIP
    ports:
      http: 8080
`
}

// buildOfOneRecipe is a helm Recipe implementing the probe Component whose
// workload image is the given template — written inline, or imported from a
// workload file beside the Recipe — and, when assertion is set, an assertion
// over the Component's optional infra.note, which no build of one sets. It
// returns the Recipe and any file it imports, keyed by name.
func buildOfOneRecipe(image string, imported, assertion bool) (string, map[string]string) {
	recipe := `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: probe
  version: "1.0.0"
spec:
  capability: helm
  implements: example/compute/probe
`
	extra := map[string]string{}
	workload := buildOfOneWorkload(image)
	if imported {
		extra["workload.yaml"] = workload
		recipe += "  workload:\n    $import: ./workload.yaml\n"
	} else {
		recipe += "  workload: |\n"
		for _, line := range strings.Split(strings.TrimRight(workload, "\n"), "\n") {
			recipe += "    " + line + "\n"
		}
	}
	if assertion {
		recipe += "  assertions:\n" +
			`    note_supplied: '{{ if not (optional .infra "note" "") }}{{ fail "set infra.note in the Release" }}{{ end }}'` + "\n"
	}
	return recipe, extra
}

// TestValidateRecipeDeclinesWhatABuildOfOneCannotSupply drives Recipes through the
// one single-entity check and reads which of the three outcomes it gives.
//
// A Recipe whose render stops while its templates read anything under
// .context.inject, .context.uses or .features is set aside as not attempted: a
// build of one wires no connection and attaches no Feature, so those reads find
// nothing. The decision is the Recipe's reads, so it holds for a template the
// Recipe imports as for one it writes inline, and for a misspelled key beside such
// a read — the stated blind spot, which the row names. A read that finds nothing
// but does not stop the render is not declined: the render ran and completed.
//
// What still fails beside such a read is what no supplied value changes: a
// template that does not parse, and a reference the render refuses. A Recipe
// reading none of those values fails on its own fault as before, inline or
// imported. A Recipe assertion beside such a read — here one over optional infra
// a Release sets — is neither set aside nor failed: it is reported as the
// warning a failed assertion always is in this check.
//
// The outcome is read from structure, never from a sentence: an error that is a
// render stop, a condition matched through its exported accessor, artifacts or
// none, and the reads the census reports. The report's Reads line is checked as
// the presentation of those same reads.
func TestValidateRecipeDeclinesWhatABuildOfOneCannotSupply(t *testing.T) {
	const (
		inject     = "{{ .infra.image }}:{{ .context.inject.env.TAG }}"
		misspelled = "{{ .infra.imge.repo }}:{{ .infra.tag }}"
	)
	cases := []struct {
		name      string
		image     string
		imported  bool
		assertion bool
		want      renderOutcome
		// wantReads are the reads the census names as what a build of one cannot
		// supply; wantUnreadable is a census that could not be taken at all.
		wantReads      []string
		wantUnreadable bool
		// wantCondition is a registered condition a failure must name, or nil.
		wantCondition error
	}{
		{
			name:  "a Recipe reading only what the build fills renders",
			image: "{{ .infra.image }}:{{ .infra.tag }}",
			want:  rendered,
		},
		{
			name:      "a read under .context.inject stops the render, which is not attempted",
			image:     inject,
			want:      notAttempted,
			wantReads: []string{".context.inject.env.TAG"},
		},
		{
			name:      "a read under .features stops the render, which is not attempted",
			image:     "{{ .infra.image }}:{{ .features.cred.provides.key }}",
			want:      notAttempted,
			wantReads: []string{".features.cred.provides.key"},
		},
		{
			name:      "a read under .context.uses that finds nothing and stops nothing renders",
			image:     "{{ .infra.image }}{{ range $a, $e := .context.uses }}-{{ $a }}{{ end }}",
			want:      rendered,
			wantReads: []string{".context.uses"},
		},
		{
			name:      "a read in an imported workload file is read as the render reads it",
			image:     inject,
			imported:  true,
			want:      notAttempted,
			wantReads: []string{".context.inject.env.TAG"},
		},
		{
			name:      "the blind spot: a misspelled key beside such a read is not attempted",
			image:     "{{ .infra.imge.repo }}:{{ .context.inject.env.TAG }}",
			want:      notAttempted,
			wantReads: []string{".context.inject.env.TAG"},
		},
		{
			name:  "a misspelled key in a Recipe reading none of them fails",
			image: misspelled,
			want:  failed,
		},
		{
			name:     "a misspelled key in an imported workload reading none of them fails",
			image:    misspelled,
			imported: true,
			want:     failed,
		},
		{
			name:           "a template that does not parse fails beside such a read",
			image:          "{{ nosuchfunc .context.inject.env.TAG }}",
			want:           failed,
			wantUnreadable: true,
		},
		{
			name:          "a reference the render refuses fails beside such a read",
			image:         "{{ .context.instance_idd }}:{{ .context.inject.env.TAG }}",
			want:          failed,
			wantReads:     []string{".context.inject.env.TAG"},
			wantCondition: renderer.ErrTemplateUnknownRoot(),
		},
		{
			name:          "a Recipe assertion over optional infra beside such a read is a warning",
			image:         inject,
			assertion:     true,
			want:          warned,
			wantReads:     []string{".context.inject.env.TAG"},
			wantCondition: renderer.ErrAssertionFailed(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recipeText, extra := buildOfOneRecipe(tc.image, tc.imported, tc.assertion)
			dir, recipe := writeProbeCatalog(t, recipeText)
			for name, content := range extra {
				if err := os.WriteFile(filepath.Join(filepath.Dir(recipe), name), []byte(content), 0o644); err != nil {
					t.Fatalf("write %s: %v", name, err)
				}
			}
			root := filepath.Dir(dir)
			cache := filepath.Join(root, "cache")
			res, err := ValidateEntity(context.Background(), root, cache, []string{dir}, nil, recipe, false)
			report := strings.Join(res.Diagnostics, "\n")

			reads := probeBuildOfOneReads(t, root, cache, dir, recipe)
			if reads.readable == tc.wantUnreadable {
				t.Errorf("census readable = %v, want %v", reads.readable, !tc.wantUnreadable)
			}
			if !slices.Equal(reads.paths, tc.wantReads) {
				t.Errorf("census reads = %q, want %q", reads.paths, tc.wantReads)
			}

			switch tc.want {
			case rendered, notAttempted:
				if err != nil {
					t.Fatalf("ValidateEntity: %v\nreport:\n%s", err, report)
				}
				if !res.Valid {
					t.Errorf("Valid = false with no error\nreport:\n%s", report)
				}
				if len(res.Warnings) != 0 {
					t.Errorf("warnings = %q, want none\nreport:\n%s", res.Warnings, report)
				}
				if got := len(res.Artifacts) > 0; got != (tc.want == rendered) {
					t.Errorf("rendered artifacts = %d, want rendered = %v\nreport:\n%s",
						len(res.Artifacts), tc.want == rendered, report)
				}
				if tc.want == notAttempted {
					line := "  Reads: " + strings.Join(tc.wantReads, ", ")
					if !slices.Contains(res.Diagnostics, line) {
						t.Errorf("the report does not name the reads as %q:\n%s", line, report)
					}
				}
			case warned:
				if err != nil {
					t.Fatalf("ValidateEntity: %v\nreport:\n%s", err, report)
				}
				if !res.Valid {
					t.Errorf("Valid = false with no error\nreport:\n%s", report)
				}
				if len(res.Artifacts) != 0 {
					t.Errorf("rendered artifacts = %d, want none\nreport:\n%s", len(res.Artifacts), report)
				}
				for _, line := range res.Diagnostics {
					if strings.HasPrefix(line, "  Reads: ") {
						t.Errorf("the report sets the render aside as %q rather than reporting the assertion:\n%s", line, report)
					}
				}
				if len(res.Warnings) != 1 {
					t.Fatalf("warnings = %q, want the one failed assertion\nreport:\n%s", res.Warnings, report)
				}
				if !errors.Is(res.Warnings[0], tc.wantCondition) {
					t.Errorf("the warning does not name the condition %q:\n%v", tc.wantCondition, res.Warnings[0])
				}
			case failed:
				if err == nil {
					t.Fatalf("ValidateEntity returned nil; want a render stop\nreport:\n%s", report)
				}
				var stopped *renderStopError
				if !errors.As(err, &stopped) {
					t.Errorf("the failure is not a render stop:\n%v", err)
				}
				if res.Valid {
					t.Errorf("Valid = true with error %v", err)
				}
				if tc.wantCondition != nil && !errors.Is(err, tc.wantCondition) {
					t.Errorf("the failure does not name the condition %q:\n%v", tc.wantCondition, err)
				}
			}
		})
	}
}

// probeBuildOfOneReads takes the census the check takes over the Recipe at path,
// through the same catalog and lookup the check builds for it.
func probeBuildOfOneReads(t *testing.T, root, cache, dir, path string) buildOfOneReads {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	rec, err := catalog.ParseRecipeEntity(data, path)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	cat, _, err := loadResolvedCatalog(root, cache, []string{dir}, nil)
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	cat.UseEmbeddedProviderResolution()
	return buildOfOneReadsOf(&rec, cat, cat.CatalogLookup())
}

// TestDeclinesRenderStopKeepsTheRecipesOwnFaults asks the decision itself about a
// render stop, over reads that would decline it and over each condition that keeps
// a stop a failure whatever the Recipe reads. Each condition is its own row, named
// through its exported accessor and wrapped the way the render wraps a stop, so a
// condition dropped from the set the check consults turns its row red.
//
// A failed assertion has no row: the render tier reports it as a warning before
// this decision is asked, which TestValidateRecipeDeclinesWhatABuildOfOneCannotSupply
// holds through the check itself.
func TestDeclinesRenderStopKeepsTheRecipesOwnFaults(t *testing.T) {
	declining := buildOfOneReads{paths: []string{".context.inject.env.TAG"}, readable: true}
	stop := func(cause error) error {
		inner := cerrors.Wrap(fmt.Errorf("template %q execute: %w", "workload[probe]", cause),
			cerrors.CatalogError, cerrors.SourceLocation{File: "recipe.yaml"}, "recipe \"probe\" workload template")
		return cerrors.Wrap(inner, cerrors.CatalogError, cerrors.SourceLocation{File: "instance"}, "rendering node \"instance\"")
	}
	plain := stop(errors.New("nil pointer evaluating interface {}.TAG"))
	cases := []struct {
		name  string
		reads buildOfOneReads
		err   error
		want  bool
	}{
		{name: "a stop beside a read a build of one lacks is declined", reads: declining, err: plain, want: true},
		{name: "a stop in a Recipe reading none of them is not", reads: buildOfOneReads{readable: true}, err: plain},
		{name: "a stop whose reads could not be taken is not", reads: buildOfOneReads{paths: declining.paths}, err: plain},
		{name: "a run its caller withdrew is not", reads: declining, err: stop(context.Canceled)},
		{name: "a run whose deadline passed is not", reads: declining, err: stop(context.DeadlineExceeded)},
		{name: "a refused reference is not", reads: declining, err: stop(renderer.ErrTemplateUnknownRoot())},
		{name: "a provides reference outside its set is not", reads: declining, err: stop(renderer.ErrProvidesSelfContainment())},
		{name: "a locals forward reference is not", reads: declining, err: stop(renderer.ErrLocalsForwardReference())},
		{name: "a locals unknown reference is not", reads: declining, err: stop(renderer.ErrLocalsUnknownReference())},
		{name: "an artifacts read of an undeclared pillar is not", reads: declining, err: stop(renderer.ErrArtifactPillarNotDeclared())},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.reads.declinesRenderStop(tc.err); got != tc.want {
				t.Errorf("declinesRenderStop = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestBuildOfOneReadsAreThoseUnderWhatTheBuildLacks reads which census paths count
// as reading what a build of one cannot supply: a path at or beneath one of the
// three roots, or one enclosing a root, and nothing that merely shares a prefix.
func TestBuildOfOneReadsAreThoseUnderWhatTheBuildLacks(t *testing.T) {
	cases := []struct {
		read string
		want bool
	}{
		{".context.inject.env.DB_HOST", true},
		{".context.inject", true},
		{".context.uses", true},
		{".context.uses.db.provides.host", true},
		{".features", true},
		{".features.cred.provides.key", true},
		{".context", true},
		{".context.instance_id", false},
		{".context.injection", false},
		{".featuresx", false},
		{".values.features", false},
		{".infra.image", false},
	}
	for _, tc := range cases {
		t.Run(tc.read, func(t *testing.T) {
			if got := readsWhatABuildOfOneLacks(tc.read); got != tc.want {
				t.Errorf("readsWhatABuildOfOneLacks(%q) = %v, want %v", tc.read, got, tc.want)
			}
		})
	}
}

// TestBuildOfOneReadsUseTheProvidersArtifactDelimiters reads an artifacts body the
// way the render does: with the delimiter pair the Recipe's provider declares for
// artifacts. The body carries a read in that pair and a target template in the
// usual one, which the shared function set does not define — so read with the
// declared pair the census holds the read, and read with the usual pair it cannot
// be taken at all. The last row asks the embedded chart provider for its own pair.
func TestBuildOfOneReadsUseTheProvidersArtifactDelimiters(t *testing.T) {
	var artifacts types.OrderedMap[types.ArtifactEntry]
	artifacts.Set("netpol.yaml", types.ArtifactEntry{
		Inline: "name: [[ .context.inject.env.HOST ]]\nlabels: {{ include \"chart.labels\" . }}\n",
	})
	rec := types.Recipe{}
	rec.Metadata.Name = "probe"
	rec.Spec.Capability = "stub"
	rec.Spec.Artifacts = artifacts

	declaring := func(d *types.PluginArtifactDelimiters) resolver.CatalogLookup {
		return resolver.CatalogLookup{ProviderPlugin: func(string) (types.Plugin, bool, error) {
			var p types.Plugin
			p.Spec.Provider.Contract.ArtifactDelimiters = d
			return p, true, nil
		}}
	}
	cases := []struct {
		name         string
		lookup       resolver.CatalogLookup
		wantReadable bool
		wantReads    []string
	}{
		{
			name:         "the provider declares an alternate pair",
			lookup:       declaring(&types.PluginArtifactDelimiters{Left: "[[", Right: "]]"}),
			wantReadable: true,
			wantReads:    []string{".context.inject.env.HOST"},
		},
		{
			name:   "the provider declares none, so the usual pair reads the target template",
			lookup: declaring(nil),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildOfOneReadsOf(&rec, &catalog.Catalog{}, tc.lookup)
			if got.readable != tc.wantReadable || !slices.Equal(got.paths, tc.wantReads) {
				t.Errorf("reads = %+v, want readable %v and %q", got, tc.wantReadable, tc.wantReads)
			}
		})
	}

	t.Run("the embedded chart provider declares its own pair", func(t *testing.T) {
		root := t.TempDir()
		dir := filepath.Join(root, "catalog")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
		cat, _, err := loadResolvedCatalog(root, filepath.Join(root, "cache"), []string{dir}, nil)
		if err != nil {
			t.Fatalf("load catalog: %v", err)
		}
		cat.UseEmbeddedProviderResolution()
		left, right := providerArtifactDelimiters(cat.CatalogLookup(), "helm-chart")
		if left != "[[" || right != "]]" {
			t.Errorf("providerArtifactDelimiters(helm-chart) = %q %q, want the provider's declared [[ ]]", left, right)
		}
	})
}
