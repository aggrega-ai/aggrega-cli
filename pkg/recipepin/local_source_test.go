// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package recipepin_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestLocalSourcePinsDigestsTheFilesItIsGiven holds the digesting half to what it
// is handed: each named file, read beside the entity, digested with the content
// digest a build takes of the same bytes, in path order — and a file it cannot
// read is a failure rather than a pin quietly left out.
func TestLocalSourcePinsDigestsTheFilesItIsGiven(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"chart/templates/cm.yaml": "kind: ConfigMap\n",
		"chart/Chart.yaml":        "name: app\n",
	}
	for rel, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := recipepin.LocalSourcePins(dir, []string{"chart/templates/cm.yaml", "chart/Chart.yaml"})
	if err != nil {
		t.Fatalf("LocalSourcePins: %v", err)
	}
	want := []types.LocalSourceDigest{
		{Path: "chart/Chart.yaml", Digest: digest.ContentDigest([]byte("name: app\n"))},
		{Path: "chart/templates/cm.yaml", Digest: digest.ContentDigest([]byte("kind: ConfigMap\n"))},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LocalSourcePins = %+v; want %+v", got, want)
	}

	if _, err := recipepin.LocalSourcePins(dir, []string{"chart/missing.yaml"}); err == nil {
		t.Error("a file that cannot be read must fail the pin rather than be left out of it")
	}
}

// TestALocalSourceIsComparedAsOneSet drives the build's comparison over each way
// a pinned source can move, and over the two answers that compare nothing, and
// holds every refusal to the one registered condition naming the file and the
// digests on each side.
func TestALocalSourceIsComparedAsOneSet(t *testing.T) {
	const doc = "/ws/catalog/recipe/helm/app/1.0.0/recipe.yaml"
	chart := types.LocalSourceDigest{Path: "chart/Chart.yaml", Digest: "sha256:aa"}
	tmpl := types.LocalSourceDigest{Path: "chart/templates/cm.yaml", Digest: "sha256:bb"}
	editedTmpl := types.LocalSourceDigest{Path: "chart/templates/cm.yaml", Digest: "sha256:cc"}
	added := types.LocalSourceDigest{Path: "chart/templates/new.yaml", Digest: "sha256:dd"}

	cases := []struct {
		name     string
		recorded []types.LocalSourceDigest
		current  []types.LocalSourceDigest
		// says is what the refusal must carry; empty means the comparison admits.
		says []string
	}{
		{name: "a lock recording no local source pin compares nothing", current: []types.LocalSourceDigest{chart, tmpl}},
		{name: "the files as pinned are admitted", recorded: []types.LocalSourceDigest{chart, tmpl}, current: []types.LocalSourceDigest{chart, tmpl}},
		{
			name:     "a file whose content moved names both digests",
			recorded: []types.LocalSourceDigest{chart, tmpl},
			current:  []types.LocalSourceDigest{chart, editedTmpl},
			says:     []string{"file chart/templates/cm.yaml digests to sha256:cc and its recorded pin is sha256:bb"},
		},
		{
			name:     "a file added to a pinned source is refused",
			recorded: []types.LocalSourceDigest{chart, tmpl},
			current:  []types.LocalSourceDigest{chart, tmpl, added},
			says:     []string{"file chart/templates/new.yaml digests to sha256:dd and no pin is recorded for it"},
		},
		{
			name:     "a file removed from a pinned source is refused",
			recorded: []types.LocalSourceDigest{chart, tmpl, added},
			current:  []types.LocalSourceDigest{chart, tmpl},
			says:     []string{"file chart/templates/new.yaml is no longer in the source and its recorded pin is sha256:dd"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := recipepin.CheckLocalSourcePins(doc, tc.recorded, tc.current)
			if len(tc.says) == 0 {
				if err != nil {
					t.Fatalf("the comparison refused what it must admit: %v", err)
				}
				return
			}
			if !errors.Is(err, recipepin.ErrLocalSourceContentMismatch()) {
				t.Fatalf("the refusal must carry the local-source mismatch condition; got %v", err)
			}
			if code, ok := cerrors.ShortCodeOf(err); !ok || code != "E0305" {
				t.Errorf("the refusal's short code = %q (found %v); want E0305", code, ok)
			}
			msg := err.Error()
			for _, want := range append(tc.says, "aggrega pin") {
				if !strings.Contains(msg, want) {
					t.Errorf("the refusal does not carry %q:\n%s", want, msg)
				}
			}
		})
	}
}
