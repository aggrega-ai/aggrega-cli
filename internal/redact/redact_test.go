// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for the redaction primitive — the placeholder, the appliers, and the set
// assembly. Every positive assertion is paired with a discrimination assertion on
// a non-sensitive sibling, because a blanket replacement passes every positive
// test on its own.
package redact

import (
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func om(pairs ...any) types.OrderedMap[any] {
	m := types.OrderedMap[any]{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i].(string), pairs[i+1])
	}
	return m
}

func sm(pairs ...string) types.OrderedMap[string] {
	m := types.OrderedMap[string]{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i], pairs[i+1])
	}
	return m
}

func TestPillar_ReplacesDeclaredLeafAndKeepsSibling(t *testing.T) {
	t.Parallel()
	in := om("dbPassword", "hunter2", "appName", "demo")
	out := Pillar(in, map[string]bool{"dbPassword": true})

	if got, _ := out.Get("dbPassword"); got != Placeholder() {
		t.Errorf("declared-sensitive leaf must be replaced; got %v", got)
	}
	if got, _ := out.Get("appName"); got != "demo" {
		t.Errorf("undeclared sibling must survive verbatim; got %v", got)
	}
	if got := out.Keys(); len(got) != 2 || got[0] != "dbPassword" || got[1] != "appName" {
		t.Errorf("key set and author order must survive; got %v", got)
	}
	// The input is the same map the render context carries into the artifacts.
	if got, _ := in.Get("dbPassword"); got != "hunter2" {
		t.Errorf("the input must not be mutated; got %v", got)
	}
}

func TestPillar_ReplacesNestedLeafByDottedPath(t *testing.T) {
	t.Parallel()
	nested := om("password", "s3cr3t", "user", "app")
	in := om("db", &nested, "port", 5432)
	out := Pillar(in, map[string]bool{"db.password": true})

	got, _ := out.Get("db")
	sub, ok := got.(*types.OrderedMap[any])
	if !ok {
		t.Fatalf("nested pointer shape must survive; got %T", got)
	}
	if v, _ := sub.Get("password"); v != Placeholder() {
		t.Errorf("nested declared leaf must be replaced; got %v", v)
	}
	if v, _ := sub.Get("user"); v != "app" {
		t.Errorf("nested sibling must survive; got %v", v)
	}
	if v, _ := out.Get("port"); v != 5432 {
		t.Errorf("unrelated top-level leaf must survive; got %v", v)
	}
	if v, _ := nested.Get("password"); v != "s3cr3t" {
		t.Errorf("the nested input must not be mutated; got %v", v)
	}
}

// A declaration on an intermediate object withholds the whole subtree rather
// than descending into it. The godoc says so; without this the claim rests on
// statement order alone.
func TestPillar_DeclarationOnIntermediateObjectHidesTheSubtree(t *testing.T) {
	t.Parallel()
	inner := om("password", "s3cr3t", "user", "app")
	in := om("db", &inner, "port", 5432)
	out := Pillar(in, map[string]bool{"db": true})

	if got, _ := out.Get("db"); got != Placeholder() {
		t.Errorf("a declaration on the object must replace the whole subtree; got %#v", got)
	}
	if v, _ := out.Get("port"); v != 5432 {
		t.Errorf("a sibling of the withheld object must survive; got %v", v)
	}
	if v, _ := inner.Get("password"); v != "s3cr3t" {
		t.Errorf("the input subtree must not be mutated; got %v", v)
	}
}

func TestPillar_EmptySetIsIdentity(t *testing.T) {
	t.Parallel()
	in := om("a", "1")
	out := Pillar(in, nil)
	if v, _ := out.Get("a"); v != "1" {
		t.Errorf("an empty set must leave the pillar untouched; got %v", v)
	}
}

func TestInjectPipe_ReplacesMarkedDeliveryKeyAndKeepsSibling(t *testing.T) {
	t.Parallel()
	in := types.InjectPipe{
		Env: sm("DATABASE_PASSWORD", "s3cr3t", "DATABASE_NAME", "db-creds"),
		All: sm("DATABASE_PASSWORD", "s3cr3t", "DATABASE_NAME", "db-creds"),
	}
	out := InjectPipe(in, map[string]map[string]bool{"env": {"DATABASE_PASSWORD": true}})

	if v, _ := out.Env.Get("DATABASE_PASSWORD"); v != Placeholder() {
		t.Errorf("marked delivery key must be replaced; got %q", v)
	}
	if v, _ := out.Env.Get("DATABASE_NAME"); v != "db-creds" {
		t.Errorf("an unmarked sibling delivery key must survive; got %q", v)
	}
	// .all is the host-built union of the typed mechanisms; the same datum
	// reappears there and must not be re-printed in the clear.
	if v, _ := out.All.Get("DATABASE_PASSWORD"); v != Placeholder() {
		t.Errorf("the union pillar must hide what the typed pillar hid; got %q", v)
	}
	if v, _ := out.All.Get("DATABASE_NAME"); v != "db-creds" {
		t.Errorf("the union pillar must keep unmarked keys; got %q", v)
	}
	if v, _ := in.Env.Get("DATABASE_PASSWORD"); v != "s3cr3t" {
		t.Errorf("the input pipe must not be mutated; got %q", v)
	}
}

func TestInjectPipe_CustomMechanism(t *testing.T) {
	t.Parallel()
	additional := types.OrderedMap[types.OrderedMap[string]]{}
	additional.Set("vault", sm("TOKEN", "t0ken", "PATH", "secret/data/app"))
	in := types.InjectPipe{Additional: additional}
	out := InjectPipe(in, map[string]map[string]bool{"vault": {"TOKEN": true}})

	got, ok := out.Additional.Get("vault")
	if !ok {
		t.Fatalf("custom mechanism must survive; got %v", out.Additional.Keys())
	}
	if v, _ := got.Get("TOKEN"); v != Placeholder() {
		t.Errorf("marked custom delivery key must be replaced; got %q", v)
	}
	if v, _ := got.Get("PATH"); v != "secret/data/app" {
		t.Errorf("unmarked custom delivery key must survive; got %q", v)
	}
}

func TestInjectPipe_EmptyMarkingIsIdentity(t *testing.T) {
	t.Parallel()
	in := types.InjectPipe{Env: sm("A", "1")}
	out := InjectPipe(in, nil)
	if v, _ := out.Env.Get("A"); v != "1" {
		t.Errorf("an empty marking must leave the pipe untouched; got %q", v)
	}
}

func TestNodeKeys_FoldsAttachmentMarkingAndIgnoresTheRollUp(t *testing.T) {
	t.Parallel()
	marked := types.OrderedMap[types.OrderedMap[string]]{}
	marked.Set("env", sm("DATABASE_PASSWORD", "password"))

	attachments := types.OrderedMap[types.IRFeatureEntry]{}
	// An attachment that handles secret material but marks one delivery key.
	// Its own values leaves must NOT be swept in by the roll-up alone.
	attachments.Set("credentials", types.IRFeatureEntry{
		Type:            "feature.test/db-credential",
		Sensitive:       true,
		SensitiveInject: marked,
		Values:          om("name", "db-creds", "key", "password"),
	})

	keys := NodeKeys(map[string]bool{"dbPassword": true}, nil, attachments, Derived{})

	if !keys.Delivery("env", "DATABASE_PASSWORD") {
		t.Errorf("the attachment's marked delivery key must reach the set; got %v", keys.Inject)
	}
	if keys.Delivery("env", "DATABASE_NAME") {
		t.Errorf("an unmarked delivery key must not reach the set; got %v", keys.Inject)
	}
	if !keys.Leaf("values", "dbPassword") {
		t.Errorf("the component's declared leaf must reach the set; got %v", keys.Values)
	}
	if keys.Leaf("values", "name") || keys.Leaf("values", "key") {
		t.Errorf("the per-attachment roll-up must not mark leaves; got %v", keys.Values)
	}
	if keys.Leaf("provides", "dbPassword") {
		t.Errorf("an unrecognised pillar must report false rather than guess")
	}
}

func TestNodeKeys_NothingDeclaredIsEmpty(t *testing.T) {
	t.Parallel()
	attachments := types.OrderedMap[types.IRFeatureEntry]{}
	attachments.Set("plain", types.IRFeatureEntry{Type: "feature.test/plain"})
	if keys := NodeKeys(nil, nil, attachments, Derived{}); !keys.Empty() {
		t.Errorf("a node declaring nothing must produce an empty set; got %+v", keys)
	}
}

func TestNodeKeys_CarriesDerivedSetsWithoutMergingThem(t *testing.T) {
	t.Parallel()
	keys := NodeKeys(map[string]bool{"dbPassword": true}, nil,
		types.OrderedMap[types.IRFeatureEntry]{},
		Derived{
			Locals: map[string]bool{"dsn": true},
			Source: map[string]bool{WholeSourceKey: true},
		})

	if !keys.Locals["dsn"] {
		t.Errorf("a derived local must reach the set; got %v", keys.Locals)
	}
	if keys.Locals["dbPassword"] {
		t.Errorf("a declared values leaf must not leak into the locals set; got %v", keys.Locals)
	}
	if !keys.Source[WholeSourceKey] {
		t.Errorf("a derived whole-string source must reach the set; got %v", keys.Source)
	}
	// The pillar sets stay separate: a local named like a leaf is a different datum.
	if keys.Leaf("values", "dsn") {
		t.Errorf("a derived local must not be readable as a declared values leaf")
	}
}

func TestEmpty_CountsTheDerivedSets(t *testing.T) {
	t.Parallel()
	if (Keys{Locals: map[string]bool{"dsn": true}}).Empty() {
		t.Error("a set naming only a derived local is not empty; every applier would be skipped")
	}
	if (Keys{Source: map[string]bool{WholeSourceKey: true}}).Empty() {
		t.Error("a set naming only a derived source field is not empty")
	}
}

func TestLocals_ReplacesWithheldEntryAndKeepsAuthorOrder(t *testing.T) {
	t.Parallel()
	// Authored order is deliberately not alphabetical: a sort creeping in would
	// reorder the output and this assertion is what catches it.
	in := types.OrderedMap[string]{}
	in.Set("zdsn", "postgres://app:hunter2@db/appdb")
	in.Set("appLabel", "demo-local")

	out := Locals(in, map[string]bool{"zdsn": true})

	if v, _ := out.Get("zdsn"); v != Placeholder() {
		t.Errorf("the withheld local must show the placeholder; got %q", v)
	}
	if v, _ := out.Get("appLabel"); v != "demo-local" {
		t.Errorf("an untouched local must survive verbatim; got %q", v)
	}
	if got := out.Keys(); !reflect.DeepEqual(got, []string{"zdsn", "appLabel"}) {
		t.Errorf("author order must survive the substitution; got %v", got)
	}
	// The input is not mutated: the same map also feeds the render context.
	if v, _ := in.Get("zdsn"); v != "postgres://app:hunter2@db/appdb" {
		t.Errorf("the input must not be mutated; got %q", v)
	}
}

func TestLocals_EmptySetIsIdentity(t *testing.T) {
	t.Parallel()
	in := types.OrderedMap[string]{}
	in.Set("dsn", "postgres://app:hunter2@db/appdb")
	out := Locals(in, nil)
	if v, _ := out.Get("dsn"); v != "postgres://app:hunter2@db/appdb" {
		t.Errorf("an empty set must leave the block untouched; got %q", v)
	}
}

func TestSource_StringShapeIsKeyedByTheWholeSourceKey(t *testing.T) {
	t.Parallel()
	if got := Source("oci://ghcr.io/x@sha256:abc", map[string]bool{WholeSourceKey: true}); got != Placeholder() {
		t.Errorf("a withheld string source must show the placeholder; got %v", got)
	}
	// The control: the same shape with nothing withheld survives verbatim.
	if got := Source("oci://ghcr.io/x@sha256:abc", map[string]bool{"ref": true}); got != "oci://ghcr.io/x@sha256:abc" {
		t.Errorf("a string source not named by the set must survive verbatim; got %v", got)
	}
}

func TestSource_StructuredShapeReplacesOnlyTheNamedField(t *testing.T) {
	t.Parallel()
	in := map[string]any{
		"ref":      "ghcr.io/app:hunter2",
		"registry": "ghcr.io",
		"port":     443,
	}
	out, ok := Source(in, map[string]bool{"ref": true}).(map[string]any)
	if !ok {
		t.Fatalf("a structured source must stay structured; got %T", Source(in, map[string]bool{"ref": true}))
	}
	if out["ref"] != Placeholder() {
		t.Errorf("the withheld field must show the placeholder; got %v", out["ref"])
	}
	if out["registry"] != "ghcr.io" {
		t.Errorf("an untouched sibling field must survive verbatim; got %v", out["registry"])
	}
	if out["port"] != 443 {
		t.Errorf("a non-string field must survive verbatim; got %v", out["port"])
	}
	if in["ref"] != "ghcr.io/app:hunter2" {
		t.Errorf("the input must not be mutated; got %v", in["ref"])
	}
}

func TestSource_UnknownShapeAndEmptySetPassThrough(t *testing.T) {
	t.Parallel()
	type descriptor struct{ Ref string }
	d := descriptor{Ref: "ghcr.io/app:hunter2"}
	if got := Source(d, map[string]bool{WholeSourceKey: true}); got != any(d) {
		t.Errorf("a shape no template was evaluated against must pass through; got %v", got)
	}
	if got := Source("oci://x", nil); got != "oci://x" {
		t.Errorf("an empty set must leave the source untouched; got %v", got)
	}
	if got := Source(nil, map[string]bool{WholeSourceKey: true}); got != nil {
		t.Errorf("a nil source must stay nil; got %v", got)
	}
}
