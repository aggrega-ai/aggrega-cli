// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for the redaction primitive — the placeholder, the appliers, and the set
// assembly. Every positive assertion is paired with a discrimination assertion on
// a non-sensitive sibling, because a blanket replacement passes every positive
// test on its own.
package redact

import (
	"reflect"
	"strings"
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
	out := deliveryOnly(DeliveryKeys{"env": {"DATABASE_PASSWORD": true}}).InjectPipe(in)

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
	out := deliveryOnly(DeliveryKeys{"vault": {"TOKEN": true}}).InjectPipe(in)

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
	out := Keys{}.InjectPipe(in)
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

	keys := NodeKeys(map[string]bool{"dbPassword": true}, nil, attachments, CrossNode{}, nil, nil, nil, Derived{})

	if !keys.Delivery("env", "DATABASE_PASSWORD") {
		t.Errorf("the attachment's marked delivery key must reach the set; got %v", keys)
	}
	if keys.Delivery("env", "DATABASE_NAME") {
		t.Errorf("an unmarked delivery key must not reach the set; got %v", keys)
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
	if keys := NodeKeys(nil, nil, attachments, CrossNode{}, nil, nil, nil, Derived{}); !keys.Empty() {
		t.Errorf("a node declaring nothing must produce an empty set; got %+v", keys)
	}
}

func TestNodeKeys_CarriesDerivedSetsWithoutMergingThem(t *testing.T) {
	t.Parallel()
	keys := NodeKeys(map[string]bool{"dbPassword": true}, nil,
		types.OrderedMap[types.IRFeatureEntry]{}, CrossNode{}, nil, nil, nil,
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
	if (Keys{UsesProvides: map[string]map[string]bool{"main": {"dsn": true}}}).Empty() {
		t.Error("a set naming only a consumed target's provides key is not empty")
	}
	for _, decision := range []WorkloadDisplay{WorkloadSubstituted, WorkloadUnavailable} {
		if (Keys{Workload: decision}).Empty() {
			t.Errorf("a set that only changes the workload display is not empty; decision %d", decision)
		}
	}
	if !(Keys{Workload: WorkloadVerbatim}).Empty() {
		t.Error("showing the emitted document is the zero value and changes nothing")
	}
}

// TestAnyWithheld_ExcludesTheWorkloadDecision pins the one asymmetry between the
// two predicates. AnyWithheld is the INPUT side of the whole-projection reading,
// so including the reading's own result would make it self-referential.
func TestAnyWithheld_ExcludesTheWorkloadDecision(t *testing.T) {
	t.Parallel()
	for _, decision := range []WorkloadDisplay{WorkloadSubstituted, WorkloadUnavailable} {
		if (Keys{Workload: decision}).AnyWithheld() {
			t.Errorf("the workload display decision is the reading's output, not an input it may consult; decision %d", decision)
		}
	}
	if !(Keys{UsesProvides: map[string]map[string]bool{"main": {"dsn": true}}}).AnyWithheld() {
		t.Error("a consumed target's provides key is readable data and must count")
	}
	if !deliveryOnly(DeliveryKeys{"env": {"DSN": true}}).AnyWithheld() {
		t.Error("a marked delivery key is readable data and must count")
	}
	if (Keys{}).AnyWithheld() {
		t.Error("the empty set names nothing readable")
	}
}

// TestNodeKeys_CarriesConsumedTargetDeclarationsPerAlias is the assembly half of
// the cross-node withholding: a node that declares NOTHING itself still carries a
// set, because a target it consumes declared something.
func TestNodeKeys_CarriesConsumedTargetDeclarationsPerAlias(t *testing.T) {
	t.Parallel()
	keys := NodeKeys(nil, nil, types.OrderedMap[types.IRFeatureEntry]{},
		CrossNode{Provides: map[string]map[string]bool{"storeEdge": {"zzTargetSecret": true}}},
		nil, nil, nil, Derived{})

	if keys.Empty() {
		t.Error("a consumer whose target declares a provides key must carry a set")
	}
	if !keys.UsesProvides["storeEdge"]["zzTargetSecret"] {
		t.Errorf("the target's declared provides key must reach the set; got %v", keys.UsesProvides)
	}
	if keys.UsesProvides["plainEdge"]["zzTargetSecret"] {
		t.Errorf("an alias nothing was declared for must stay absent; got %v", keys.UsesProvides)
	}
	// The consumer declares nothing, and a cross-node set must not be readable as
	// one: the two answer different questions about different entities.
	if keys.Leaf("values", "zzTargetSecret") {
		t.Error("a consumed target's provides key must not read as this node's own leaf")
	}
}

// TestNodeKeys_FoldsEveryDeliveryRouteIntoOneMarking is the assembly assertion for
// the delivery pipe, which is fed from five independent routes — an attachment's
// own inject mapping, a uses edge's, a FeatureRecipe's injectMapping, the node's own
// component-level self-inject, and the fail-safe for a key whose provenance cannot
// be traced — into ONE displayed map.
//
// Every route is asserted in one test because the failure mode is a display
// covering some and not the others: the pipe is flat, so a reader of it cannot tell
// which route filled a key, and a key left unmarked prints in the clear beside one
// that did not. Every gap in this family was exactly that. An unmarked sibling from
// each route is the control, so a marking that fired on everything fails the
// control half.
func TestNodeKeys_FoldsEveryDeliveryRouteIntoOneMarking(t *testing.T) {
	t.Parallel()
	attachmentMarked := types.OrderedMap[types.OrderedMap[string]]{}
	attachmentMarked.Set("env", sm("DATABASE_PASSWORD", "password"))
	attachments := types.OrderedMap[types.IRFeatureEntry]{}
	attachments.Set("credentials", types.IRFeatureEntry{
		Type:            "feature.test/db-credential",
		SensitiveInject: attachmentMarked,
	})

	keys := NodeKeys(nil, nil, attachments,
		CrossNode{Inject: DeliveryKeys{"env": {"STORE_SECRET": true}}},
		DeliveryKeys{"env": {"MAPPED_SECRET": true}},
		DeliveryKeys{"env": {"SELF_INJECTED_SECRET": true}},
		DeliveryKeys{"env": {"UNTRACED_KEY": true}},
		Derived{})

	for route, key := range map[string]string{
		"attachment inject mapping":     "DATABASE_PASSWORD",
		"uses edge":                     "STORE_SECRET",
		"FeatureRecipe injectMapping":   "MAPPED_SECRET",
		"the node's own self-inject":    "SELF_INJECTED_SECRET",
		"a key whose route is untraced": "UNTRACED_KEY",
	} {
		if !keys.Delivery("env", key) {
			t.Errorf("the delivery key routed by %s must reach the set; got %v", route, keys)
		}
	}
	for _, key := range []string{"DATABASE_NAME", "STORE_HOST", "MAPPED_LOCATOR", "SELF_INJECTED_PLAIN", "TRACED_PLAIN"} {
		if keys.Delivery("env", key) {
			t.Errorf("an unmarked delivery key %q must stay absent; got %v", key, keys)
		}
	}
	if keys.Delivery("file", "STORE_SECRET") {
		t.Errorf("a marking must not cross into another mechanism; got %v", keys)
	}
}

// TestNodeKeys_UntracedDeliveryKeyIsWithheld is the fail-safe on its own, isolated
// from every other route so nothing else can be what withheld the key.
//
// It matters because no compiler mechanism can force a route that exists in the
// pipeline to be plumbed into the assembly — three gaps in this family were exactly
// an unplumbed route — so the assembly needs an answer for a delivery key it cannot
// attribute. The answer is the same one the reading of a template gives an
// unresolvable reference: withhold.
func TestNodeKeys_UntracedDeliveryKeyIsWithheld(t *testing.T) {
	t.Parallel()
	keys := NodeKeys(nil, nil, types.OrderedMap[types.IRFeatureEntry]{}, CrossNode{},
		nil,
		nil,
		DeliveryKeys{"env": {"NO_TRACED_ROUTE": true}, "vault": {"TOKEN": true}},
		Derived{})

	if keys.Empty() {
		t.Fatal("a key no route accounted for must produce a non-empty set")
	}
	if !keys.Delivery("env", "NO_TRACED_ROUTE") {
		t.Errorf("an untraced delivery key must be withheld; got %v", keys)
	}
	if !keys.Delivery("vault", "TOKEN") {
		t.Errorf("the fail-safe must cover a custom mechanism too; got %v", keys)
	}
	if keys.Delivery("env", "TRACED_PLAIN") {
		t.Errorf("the fail-safe must not reach a key it was not given; got %v", keys)
	}
}

// deliveryOnly builds a set whose ONLY content is the given delivery marking, for
// the appliers whose subject is the pipe. It goes through the one assembler rather
// than a struct literal, because the marking is unreachable from outside this
// package and a test that reached around that would not be testing what ships.
func deliveryOnly(marked DeliveryKeys) Keys {
	return NodeKeys(nil, nil, types.OrderedMap[types.IRFeatureEntry]{}, CrossNode{}, nil, nil, marked, Derived{})
}

// TestUses_ReplacesTheMarkedProvidesKeyAndKeepsEveryControl is the applier half.
// Three controls sit beside the one positive assertion, because a blanket
// replacement over the uses map would satisfy the positive one on its own: the
// marked edge's own unmarked sibling key, a DIFFERENT edge publishing the SAME key
// name, and each edge's non-provides fields.
func TestUses_ReplacesTheMarkedProvidesKeyAndKeepsEveryControl(t *testing.T) {
	t.Parallel()
	in := types.OrderedMap[types.EnrichedEdge]{}
	// Author order is deliberately not alphabetical inside each provides bag.
	in.Set("storeEdge", types.EnrichedEdge{
		Target:    "store",
		Socket:    "marked",
		Component: "core/uses-target",
		Provides:  sm("zzTargetSecret", "wobble", "aaTargetHost", "store"),
		Values:    om("protocol", "grpc"),
	})
	in.Set("plainEdge", types.EnrichedEdge{
		Target:    "plain",
		Socket:    "unmarked",
		Component: "core/uses-plain",
		Provides:  sm("zzTargetSecret", "visible", "aaTargetHost", "plain"),
	})

	out := Uses(in, map[string]map[string]bool{"storeEdge": {"zzTargetSecret": true}})

	marked, _ := out.Get("storeEdge")
	if v, _ := marked.Provides.Get("zzTargetSecret"); v != placeholder {
		t.Errorf("the marked provides key must show the placeholder; got %q", v)
	}
	if v, _ := marked.Provides.Get("aaTargetHost"); v != "store" {
		t.Errorf("an unmarked sibling on the same edge must survive; got %q", v)
	}
	if got := marked.Provides.Keys(); len(got) != 2 || got[0] != "zzTargetSecret" {
		t.Errorf("author order must survive the substitution; got %v", got)
	}
	if marked.Target != "store" || marked.Socket != "marked" {
		t.Errorf("the edge's own fields must survive; got %+v", marked)
	}
	if v, _ := marked.Values.Get("protocol"); v != "grpc" {
		t.Errorf("per-connection values are not what a target's provides declaration governs; got %v", v)
	}

	plain, _ := out.Get("plainEdge")
	if v, _ := plain.Provides.Get("zzTargetSecret"); v != "visible" {
		t.Errorf("the SAME key name on an edge nothing was declared for must survive verbatim; got %q", v)
	}

	// The input is never mutated: the display copies, so an in-place substitution
	// would reach the render context these edges are read from.
	src, _ := in.Get("storeEdge")
	if v, _ := src.Provides.Get("zzTargetSecret"); v != "wobble" {
		t.Errorf("Uses must not mutate its input; the source edge now reads %q", v)
	}
	if got := out.Keys(); len(got) != 2 || got[0] != "storeEdge" {
		t.Errorf("alias order must survive; got %v", got)
	}
}

func TestUses_EmptySetIsIdentity(t *testing.T) {
	t.Parallel()
	in := types.OrderedMap[types.EnrichedEdge]{}
	in.Set("main", types.EnrichedEdge{Target: "db", Provides: sm("dsn", "postgres://x")})
	out := Uses(in, nil)
	edge, _ := out.Get("main")
	if v, _ := edge.Provides.Get("dsn"); v != "postgres://x" {
		t.Errorf("an empty set must leave the uses projection untouched; got %q", v)
	}
}

// TestWithheldProjection_IsDistinctFromThePlaceholderAndNamesTheEscapeHatch pins
// the two properties the statement exists for: a reader can tell a withheld whole
// projection from a withheld single value, and can act on it.
func TestWithheldProjection_IsDistinctFromThePlaceholderAndNamesTheEscapeHatch(t *testing.T) {
	t.Parallel()
	got := WithheldProjection()
	if got == Placeholder() {
		t.Error("a withheld whole projection and a withheld single value must be distinguishable")
	}
	assertNamesARunnableUnredactedInvocation(t, got, "the withholding statement")
	if !strings.Contains(got, "withheld") {
		t.Errorf("the statement must say the data was withheld rather than merely absent; got %q", got)
	}
}

// TestSubstitutedProjection_NamesARunnableInvocation holds the re-render
// announcement to the same standard as the withholding statement. Both are read
// out of a file rather than off a terminal, so a partial command in either one
// sends the reader to a halt with nothing on screen to complete it from.
func TestSubstitutedProjection_NamesARunnableInvocation(t *testing.T) {
	t.Parallel()
	assertNamesARunnableUnredactedInvocation(t, SubstitutedProjection(), "the re-render announcement")
}

// assertNamesARunnableUnredactedInvocation requires a text to name the whole
// invocation that captures a trace in the clear, not just the flag selecting it.
//
// Each fragment is asserted separately so a failure says which one is missing.
// Naming only the behaviour-selecting flag is the shape this guards against: that
// form halts twice over — once on the required release, and again on the trace the
// unredacted capture has nothing to write without.
func assertNamesARunnableUnredactedInvocation(t *testing.T, got, subject string) {
	t.Helper()
	for _, fragment := range []string{"aggrega build", "--release", "--trace ", "--trace-unredacted"} {
		if !strings.Contains(got, fragment) {
			t.Errorf("%s must name a runnable invocation and is missing %q; got %q", subject, fragment, got)
		}
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
