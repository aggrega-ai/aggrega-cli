// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package entitypin

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
)

// entityDoc authors a minimal document of kind named name.
func entityDoc(kind, name string) string {
	return "apiVersion: aggrega.ai/v1alpha1\nkind: " + kind + "\nmetadata:\n  name: " + name + "\n  version: \"1.0.0\"\n"
}

// TestRefuseSharedDirectoryReadsOnlyWhichEntitiesSitBeside drives the one
// predicate over the layouts it has to tell apart. Every refusing row names the
// neighbour it found; every admitting row is a layout the rule must leave alone,
// which is the half that proves the rule is the ruled one — a predicate refusing
// every multi-document directory would pass every refusing row.
func TestRefuseSharedDirectoryReadsOnlyWhichEntitiesSitBeside(t *testing.T) {
	cases := []struct {
		name string
		// subject is the file judged, and its document.
		subject, body string
		// beside is every other file written, by path relative to the directory.
		beside map[string]string
		// refusedNaming is the neighbour the halt must name; empty means admitted.
		refusedNaming []string
	}{
		{
			name: "a Recipe alone in its directory is admitted", subject: "recipe.yaml", body: entityDoc("Recipe", "a"),
		},
		{
			name: "a Recipe beside another Recipe is refused", subject: "a.yaml", body: entityDoc("Recipe", "a"),
			beside:        map[string]string{"b.yaml": entityDoc("Recipe", "b")},
			refusedNaming: []string{`Recipe "b" in b.yaml`},
		},
		{
			name: "a Recipe beside a Release is refused, since only the sharing kinds share", subject: "recipe.yaml", body: entityDoc("Recipe", "a"),
			beside:        map[string]string{"release.yaml": entityDoc("Release", "r")},
			refusedNaming: []string{`Release "r" in release.yaml`},
		},
		{
			name: "every neighbour is named, in file-name order", subject: "a.yaml", body: entityDoc("Component", "a"),
			beside:        map[string]string{"c.yml": entityDoc("Feature", "c"), "b.yaml": entityDoc("SchemaLibrary", "B")},
			refusedNaming: []string{`SchemaLibrary "B" in b.yaml, beside it: Feature "c" in c.yml`},
		},
		{
			name: "a hidden entity document is an entity the catalog reads, and is in the way", subject: "a.yaml", body: entityDoc("Recipe", "a"),
			beside:        map[string]string{".b.yaml": entityDoc("Recipe", "b")},
			refusedNaming: []string{`Recipe "b" in .b.yaml`},
		},
		{
			name: "an entity kind with malformed metadata is still in the way", subject: "a.yaml", body: entityDoc("Recipe", "a"),
			beside:        map[string]string{"b.yaml": "kind: Recipe\nmetadata: not-a-map\n"},
			refusedNaming: []string{`Recipe "" in b.yaml`},
		},
		{
			name: "companions that are not entities are not in the way", subject: "recipe.yaml", body: entityDoc("Recipe", "a"),
			beside: map[string]string{
				"deployment.yaml": "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n",
				"compose.yaml":    "services:\n  web:\n    image: nginx\n",
				"broken.yaml":     "kind: [unclosed\n",
				"README.md":       "kind: Recipe\n",
				"entity.lock":     "version: 1\n",
			},
		},
		{
			name: "an entity in a subdirectory is not beside the entity", subject: "recipe.yaml", body: entityDoc("Recipe", "a"),
			beside: map[string]string{"source/chart.yaml": entityDoc("Recipe", "nested"), "nested.yaml/x": "x\n"},
		},
		{
			name: "Aggregates, Releases and Manifests share a directory", subject: "app.yaml", body: entityDoc("Aggregate", "app"),
			beside: map[string]string{
				"other.yaml":    entityDoc("Aggregate", "other"),
				"release.yaml":  entityDoc("Release", "r"),
				"manifest.yaml": entityDoc("Manifest", "m"),
			},
		},
		{
			name: "an Aggregate is admitted even beside a Recipe, whose own verdict is the refusal", subject: "app.yaml", body: entityDoc("Aggregate", "app"),
			beside: map[string]string{"recipe.yaml": entityDoc("Recipe", "a")},
		},
		{
			name: "a document of no entity kind is not this rule's to judge", subject: "values.yaml", body: "replicas: 1\n",
			beside: map[string]string{"recipe.yaml": entityDoc("Recipe", "a")},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			subject := filepath.Join(dir, tc.subject)
			writeFile(t, subject, tc.body)
			for rel, body := range tc.beside {
				writeFile(t, filepath.Join(dir, rel), body)
			}

			err := RefuseSharedDirectory(subject)
			if len(tc.refusedNaming) == 0 {
				if err != nil {
					t.Fatalf("the layout must be admitted; got %v", err)
				}
				return
			}
			if !errors.Is(err, ErrEntitySharesItsDirectory()) {
				t.Fatalf("the layout must be refused under the shared-directory condition; got %v", err)
			}
			if code, _ := cerrors.ShortCodeOf(err); code != "E0303" {
				t.Errorf("the refusal carries short code %q; want E0303", code)
			}
			for _, want := range append(tc.refusedNaming, "give each entity a directory of its own",
				"only the Aggregate, Manifest and Release kinds may share a directory") {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal must carry %q:\n%v", want, err)
				}
			}
			var ce *cerrors.CompilerError
			if !errors.As(err, &ce) || ce.Source().File != subject {
				t.Errorf("the refusal must be positioned at the judged document %s; got %+v", subject, ce)
			}
		})
	}
}

// TestEveryKindButTheSharingOnesHasADirectoryOfItsOwn walks the whole
// vocabulary, so a kind added to it is covered the moment it exists: each kind is
// put beside a Recipe and must be refused unless the vocabulary lets it share,
// and the kinds that may share must be exactly the three the ruling names — a
// fourth opting in would pass every other assertion here.
func TestEveryKindButTheSharingOnesHasADirectoryOfItsOwn(t *testing.T) {
	kinds := entitykind.Kinds()
	if len(kinds) == 0 {
		t.Fatal("the vocabulary enumerates no kinds; the loop below would assert nothing")
	}
	for _, k := range kinds {
		t.Run(string(k), func(t *testing.T) {
			dir := t.TempDir()
			subject := filepath.Join(dir, "subject.yaml")
			writeFile(t, subject, entityDoc(string(k), "subject"))
			writeFile(t, filepath.Join(dir, "neighbour.yaml"), entityDoc("Recipe", "neighbour"))
			err := RefuseSharedDirectory(subject)
			if entitykind.SharesDirectory(k) {
				if err != nil {
					t.Errorf("%s may share a directory and must be admitted; got %v", k, err)
				}
				return
			}
			if !errors.Is(err, ErrEntitySharesItsDirectory()) {
				t.Errorf("%s has a directory of its own and must be refused beside a Recipe; got %v", k, err)
			}
		})
	}
	var sharing []string
	for _, k := range entitykind.DirectorySharing() {
		sharing = append(sharing, string(k))
	}
	if got := strings.Join(sharing, ","); got != "Aggregate,Manifest,Release" {
		t.Errorf("the kinds that may share a directory are %q; want Aggregate, Manifest and Release", got)
	}
}

// TestAPlanForWritingRefusesASharedDirectoryBeforeResolvingAnything is the pin
// step's half of the rule, driven through Compute: two Recipes in one directory,
// each naming a remote source whose identity is written out, are refused before
// the resolver is asked about either — and the same two, in a plan computed only
// to be checked, are not refused at all, which is what keeps a pull that does not
// pin from refusing a directory it writes nothing into.
func TestAPlanForWritingRefusesASharedDirectoryBeforeResolvingAnything(t *testing.T) {
	ws := t.TempDir()
	cat := filepath.Join(ws, "catalog")
	for _, name := range []string{"one", "two"} {
		writeFile(t, filepath.Join(cat, "shared", name+".yaml"), recipeYAML(name,
			"  source:\n    kind: helm-https-chart\n    url: https://example.invalid/"+name+".tgz\n    version: \"1.0.0\"\n", false))
	}
	compute := func(forWriting bool, res *recordingResolver) (Plan, error) {
		return Compute(PlanInputs{
			View:          catalog.NewLazyLookup(ws, "", []string{cat}, nil),
			URIs:          []string{"helm/one:1.0.0", "helm/two:1.0.0"},
			Resolver:      res,
			WorkspaceRoot: ws,
			ForWriting:    forWriting,
		})
	}

	res := &recordingResolver{}
	_, err := compute(true, res)
	if !errors.Is(err, ErrEntitySharesItsDirectory()) {
		t.Fatalf("a plan for writing over two Recipes in one directory must be refused; got %v", err)
	}
	for _, want := range []string{`Recipe "one"`, `Recipe "two"`, "one.yaml", "two.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("both refusals are reported in one run, each naming the other; %q is missing from:\n%v", want, err)
		}
	}
	if len(res.asked) != 0 {
		t.Errorf("the refusal must come before any source is resolved; the resolver was asked about %+v", res.asked)
	}

	checked := &recordingResolver{}
	plan, err := compute(false, checked)
	if err != nil {
		t.Fatalf("a plan computed only to be checked claims no lock and must not be refused; got %v", err)
	}
	if len(checked.asked) != 2 {
		t.Errorf("the checked plan resolves both sources as a pull that does not pin does; asked %+v", checked.asked)
	}
	if uerr := plan.Unpinned(testRemedy); !errors.Is(uerr, recipepin.ErrEntityNotPinned()) {
		t.Errorf("the checked plan still holds the locks to the pins; got %v", uerr)
	}
}

// TestAPlanComputedToBeCheckedCannotBeWritten holds the structural half: a plan
// is written or reported only if it was computed for writing, so no caller can
// reach the lock's writer without the directory rule having run first. The same
// plan computed for writing writes, which is the control that the refusal is the
// plan's origin rather than the fixture.
func TestAPlanComputedToBeCheckedCannotBeWritten(t *testing.T) {
	ws := t.TempDir()
	cat := filepath.Join(ws, "catalog")
	recipe := filepath.Join(cat, "server", "recipe.yaml")
	writeFile(t, recipe, recipeYAML("server", "", true))
	writeFile(t, filepath.Join(cat, "server", "extra.yaml"), "kind: ConfigMap\n")
	compute := func(forWriting bool) Plan {
		t.Helper()
		plan, err := Compute(PlanInputs{View: catalog.NewLazyLookup(ws, "", []string{cat}, nil),
			URIs: []string{"helm/server:1.0.0"}, Resolver: &recordingResolver{}, WorkspaceRoot: ws, ForWriting: forWriting})
		if err != nil {
			t.Fatalf("Compute: %v", err)
		}
		return plan
	}

	checked := compute(false)
	if _, err := checked.Write(); !errors.Is(err, errPlanNotForWriting) {
		t.Errorf("Write over a plan computed to be checked must refuse; got %v", err)
	}
	if _, _, err := checked.Report(); !errors.Is(err, errPlanNotForWriting) {
		t.Errorf("Report over a plan computed to be checked must refuse; got %v", err)
	}
	if _, err := os.Stat(recipepin.LockPath(recipe)); !os.IsNotExist(err) {
		t.Fatalf("a refused write left a lock; stat err = %v", err)
	}
	if _, err := compute(true).Write(); err != nil {
		t.Fatalf("the same plan computed for writing must write; got %v", err)
	}
}
