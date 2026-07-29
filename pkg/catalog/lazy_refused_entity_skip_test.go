// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// refusedComponentDoc is a Component that declares extends and then authors
// pillar content, which the Component envelope refuses by a named rule: an
// extending Component inherits its base's pillars verbatim and declares the
// inheritance marker instead of authoring its own.
const refusedComponentDoc = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: acc/refused
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  extends: acc/base:1.0.0
  inject: ["env"]
  schema:
    provides:
      host: { type: string, required: true }
`

const refusedFeatureDoc = `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: acc/refused-feature
  version: "1.0.0"
spec:
  schema: not-a-mapping
`

func writeRefusedFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestLazyLookup_RefusedComponentRecordedNotDiscarded pins the reason a refused
// Component file must be RECORDED rather than dropped: a refusal makes the
// cascade report a miss, and a miss reaches the operator as an unresolvable
// entity — a symptom naming nothing they can act on. The recorded reason is what
// lets the surfaces that report the miss also report the file and the constraint.
func TestLazyLookup_RefusedComponentRecordedNotDiscarded(t *testing.T) {
	ws := t.TempDir()
	dir := filepath.Join(ws, "catalog")
	path := filepath.Join(dir, "components", "refused.yaml")
	writeRefusedFixture(t, path, refusedComponentDoc)

	ll := catalog.NewLazyLookup(ws, filepath.Join(ws, "cache"), []string{dir}, nil)

	// Precondition: the refusal is what makes the lookup miss. A hit would mean
	// the fixture no longer exercises the class.
	if _, ok := ll.Component("acc/refused:1.0.0"); ok {
		t.Fatal("fixture no longer exercises the class: the refused Component resolved")
	}

	skips := ll.Skips()
	reason, recorded := skips[path]
	if !recorded {
		t.Fatalf("the refusal was discarded; Skips() = %v", skips)
	}
	const rule = "extending-component-must-declare-schema-inherited"
	if !strings.Contains(reason.Error(), rule) && !strings.Contains(cerrors.CauseChain(reason), rule) {
		t.Errorf("the recorded reason must name the rule %q:\n%s", rule, cerrors.CauseChain(reason))
	}

	// The same record, in the shape one shared annotator renders.
	entities := ll.SkippedEntities()
	if len(entities) != 1 || entities[0].Path != path {
		t.Fatalf("SkippedEntities() = %#v; want one entry for %s", entities, path)
	}
	if entities[0].Err == nil {
		t.Error("SkippedEntities() dropped the reason")
	}
}

// TestLazyLookup_RefusedFeatureRecordedNotDiscarded is the Feature half; the two
// cascade readers must not diverge on whether a refusal is kept.
func TestLazyLookup_RefusedFeatureRecordedNotDiscarded(t *testing.T) {
	ws := t.TempDir()
	dir := filepath.Join(ws, "catalog")
	path := filepath.Join(dir, "features", "refused.yaml")
	writeRefusedFixture(t, path, refusedFeatureDoc)

	ll := catalog.NewLazyLookup(ws, filepath.Join(ws, "cache"), []string{dir}, nil)
	if _, ok := ll.Feature("acc/refused-feature:1.0.0"); ok {
		t.Fatal("fixture no longer exercises the class: the refused Feature resolved")
	}
	if _, recorded := ll.Skips()[path]; !recorded {
		t.Fatalf("the Feature refusal was discarded; Skips() = %v", ll.Skips())
	}
}

// TestLazyLookup_ResolvableEntityRecordsNoSkip is the negative leg: recording a
// refusal must not turn an ordinary cascade miss (no such file) or a clean read
// into a skip, which would make every note noise.
func TestLazyLookup_ResolvableEntityRecordsNoSkip(t *testing.T) {
	ws := t.TempDir()
	dir := filepath.Join(ws, "catalog")
	writeRefusedFixture(t, filepath.Join(dir, "components", "clean.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: acc/clean
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  inject: ["env"]
  schema:
    provides:
      host: { type: string, required: true }
`)
	ll := catalog.NewLazyLookup(ws, filepath.Join(ws, "cache"), []string{dir}, nil)
	if _, ok := ll.Component("acc/clean:1.0.0"); !ok {
		t.Fatal("the clean Component must resolve")
	}
	if _, ok := ll.Component("acc/absent:1.0.0"); ok {
		t.Fatal("an absent Component must miss")
	}
	if n := len(ll.Skips()); n != 0 {
		t.Errorf("a clean read and an absent entity must record no skip; got %d: %v", n, ll.Skips())
	}
	if e := ll.SkippedEntities(); e != nil {
		t.Errorf("SkippedEntities() = %#v; want nil", e)
	}
}
