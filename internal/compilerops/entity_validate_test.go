// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compilerops

import (
	"context"
	stderrors "errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/validateops"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

// entityProbeComponent is a Component named name. With refused set its inject
// list is written as a scalar, which its kind's schema does not accept, so the
// catalog load refuses the file while its header still declares the identity.
func entityProbeComponent(name string, refused bool) string {
	inject := ""
	if refused {
		inject = "  inject: env\n"
	}
	return "apiVersion: aggrega.ai/v1beta1\n" +
		"kind: Component\n" +
		"metadata:\n" +
		"  name: " + name + "\n" +
		"  version: \"1.0.0\"\n" +
		"  labels:\n" +
		"    component.aggrega.ai/type: \"server\"\n" +
		"    component.aggrega.ai/category: compute\n" +
		"spec:\n" +
		inject +
		"  schema:\n" +
		"    infra:\n" +
		"      image:\n" +
		"        type: string\n" +
		"        required: true\n"
}

// entityProbeRecipe is a helm Recipe implementing example/compute/probe.
const entityProbeRecipe = `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: probe
  version: "1.0.0"
spec:
  capability: helm
  implements: example/compute/probe
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    replicas: 1
    containers:
      main:
        image: "{{ .infra.image }}"
        ports:
          http: 8080
    services:
      default:
        type: ClusterIP
        ports:
          http: 8080
`

// TestValidateSingleEntity_ARefusedImplementsTargetLeadsTheReport drives the
// single-entity check of a Recipe whose implements target is absent from the
// catalog, through the entry every surface that checks one entity calls, over a
// catalog holding one file the load refuses.
//
// Where the refused file declares the identity the Recipe implements, that
// refusal leads the report and the not-found failure moves into the note beneath
// it. Where the refused file declares something else, the not-found failure
// leads and the refusal is listed beneath it. Where nothing was refused, the
// failure is exactly the one the check returns on its own, so a catalog that
// refuses nothing reports what it always did.
func TestValidateSingleEntity_ARefusedImplementsTargetLeadsTheReport(t *testing.T) {
	t.Parallel()
	const notFound = `render: implements target "example/compute/probe" not found in catalog`
	for _, tc := range []struct {
		name string
		// component is the Component file's content, or empty for none.
		component string
		promoted  bool
		listed    bool
	}{
		{"the refused file declares the implements target", entityProbeComponent("example/compute/probe", true), true, false},
		{"the refused file declares something else", entityProbeComponent("example/compute/other", true), false, true},
		{"nothing is refused", "", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			dir := filepath.Join(root, "catalog")
			compPath := filepath.Join(dir, "component/example/compute/probe/1.0.0/component.yaml")
			// The Recipe sits outside the catalog directory, so the load's record
			// holds only what the Component file contributes.
			recipePath := filepath.Join(root, "recipe.yaml")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("create %s: %v", dir, err)
			}
			files := map[string]string{recipePath: entityProbeRecipe}
			if tc.component != "" {
				files[compPath] = tc.component
			}
			for path, content := range files {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatalf("create %s: %v", filepath.Dir(path), err)
				}
				if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
					t.Fatalf("write %s: %v", path, err)
				}
			}

			_, err := func() (res validateops.EntityResult, err error) {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("the check panicked: %v", r)
					}
				}()
				return ValidateSingleEntity(context.Background(), root, filepath.Join(root, "cache"), []string{dir}, nil, recipePath, false)
			}()
			if err == nil {
				t.Fatal("the check passed a Recipe whose implements target the catalog does not hold")
			}
			var target *closure.UnresolvedEntityError
			if !stderrors.As(err, &target) || target.URI != "example/compute/probe" {
				t.Fatalf("the failure does not carry the absent implements target: %v", err)
			}
			// Carrying the reference adds no condition: the failure names none, so
			// the account of where local entities are read from stays silent.
			if stderrors.Is(err, closure.ErrUnresolvableURI()) {
				t.Errorf("the failure gained the unresolvable-reference condition: %v", err)
			}

			var sce *SkipContextError
			annotated := stderrors.As(err, &sce) && sce != nil
			if !tc.promoted && !tc.listed {
				if annotated {
					t.Errorf("a catalog that refused nothing gained a refusal account:\n%s", err)
				}
				if err.Error() != notFound {
					t.Errorf("err = %q, want exactly %q", err.Error(), notFound)
				}
				return
			}
			if !annotated {
				t.Fatalf("the failure carries no account of the refused file:\n%s", err)
			}
			refusal, promoted := sce.PromotedRefusal()
			if promoted != tc.promoted {
				t.Fatalf("promoted = %v, want %v:\n%s", promoted, tc.promoted, err)
			}
			text := err.Error()
			if !strings.Contains(text, compPath) {
				t.Errorf("the report does not name the refused file %s:\n%s", compPath, text)
			}
			if !strings.Contains(text, notFound) {
				t.Errorf("the report drops the failure it explains:\n%s", text)
			}
			if tc.promoted {
				if refusal.Path != compPath {
					t.Errorf("promoted refusal = %s, want %s", refusal.Path, compPath)
				}
				if sce.Leading() != refusal.Err {
					t.Errorf("the report does not lead with the refusal:\n%s", text)
				}
				if !strings.Contains(sce.Note(), RefusedDocumentNote) {
					t.Errorf("the note does not say why the refusal explains the failure:\n%s", text)
				}
				if strings.Contains(sce.Note(), "Release") {
					t.Errorf("a check of one entity speaks of a Release:\n%s", text)
				}
			}
			if tc.listed {
				if !strings.HasPrefix(text, notFound) {
					t.Errorf("the failure does not lead where no refusal declares the target:\n%s", text)
				}
				if !strings.Contains(sce.Note(), "catalog files skipped at load") {
					t.Errorf("the note does not list the refused files:\n%s", text)
				}
			}
		})
	}
}
