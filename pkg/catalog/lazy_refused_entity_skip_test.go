// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

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
const refusedComponentDoc = `apiVersion: aggrega.ai/v1beta1
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

const refusedFeatureDoc = `apiVersion: aggrega.ai/v1beta1
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

	// The identity the refused document declares for itself, recovered from its
	// header. Without it the record carries a path and nothing else, and a
	// consumer that matches a failing reference against a declared identity
	// matches nothing on any input.
	if got := entities[0].Kind; got != "Component" {
		t.Errorf("SkippedEntities()[0].Kind = %q; want the kind the refused document declares", got)
	}
	if got := entities[0].Name; got != "acc/refused" {
		t.Errorf("SkippedEntities()[0].Name = %q; want the name the refused document declares", got)
	}
	if got := entities[0].Version; got != "1.0.0" {
		t.Errorf("SkippedEntities()[0].Version = %q; want the version the refused document declares", got)
	}
}

// TestLazyLookup_RefusedRecordIsMatchableByAReference is the consumer half, and
// it is the assertion that makes the identity above worth recovering.
//
// The matcher this drives is what lets a surface report a refusal AHEAD of the
// failure it explains rather than beneath it, and it matches on the identity a
// refused document declares. A record carrying only a path is not a near miss
// for it — it matches nothing, on every input — so a producer-side omission
// leaves the promotion structurally incapable of firing while every gate stays
// green. The negative leg is what makes the positive one discriminating: a
// reference to something else must still single nothing out.
func TestLazyLookup_RefusedRecordIsMatchableByAReference(t *testing.T) {
	ws := t.TempDir()
	dir := filepath.Join(ws, "catalog")
	path := filepath.Join(dir, "components", "refused.yaml")
	writeRefusedFixture(t, path, refusedComponentDoc)

	ll := catalog.NewLazyLookup(ws, filepath.Join(ws, "cache"), []string{dir}, nil)
	if _, ok := ll.Component("acc/refused:1.0.0"); ok {
		t.Fatal("fixture no longer exercises the class: the refused Component resolved")
	}

	// The kind hint is the collection-site vocabulary a closure walk records —
	// lowercase, and a different vocabulary from the capitalised kind a document
	// declares at its head. Passing the declared form here would leave the
	// reference's kind unmapped, and both legs below would then pass without ever
	// reaching the record: the positive one would fail, and the negative one would
	// succeed for a reason that has nothing to do with what it asserts.
	skipped := ll.SkippedEntities()
	match, ok := catalog.SoleRefusalFor(skipped, "acc/refused:1.0.0", "component")
	if !ok {
		t.Fatalf("the reference the refusal made unresolvable must single that refusal out; record = %#v", skipped)
	}
	if match.Path != path {
		t.Errorf("SoleRefusalFor matched %s; want the refused file %s", match.Path, path)
	}

	if _, ok := catalog.SoleRefusalFor(skipped, "acc/other:1.0.0", "component"); ok {
		t.Error("a reference no refusal declares must single nothing out; naming the wrong file as the cause " +
			"is worse than naming none, because a reader acts on it")
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
	writeRefusedFixture(t, filepath.Join(dir, "components", "clean.yaml"), `apiVersion: aggrega.ai/v1beta1
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
