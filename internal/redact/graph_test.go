// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package redact_test

import (
	"encoding/json"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// om builds an ordered map from alternating key/value arguments, so a fixture
// reads as the document it stands for rather than as a run of Set calls.
func om(kv ...any) types.OrderedMap[any] {
	m := types.OrderedMap[any]{}
	for i := 0; i+1 < len(kv); i += 2 {
		m.Set(kv[i].(string), kv[i+1])
	}
	return m
}

// markerFor is the shape every assertion below joins on: the single-key object
// the module emits where a value was. It is built through the exported
// constructor rather than spelled here, so a test cannot pass against a marker
// the module no longer produces.
func markerFor(reason redact.WithheldReason) map[string]string {
	return redact.WithheldMarker(reason)
}

// leafAt walks a decoded JSON payload to a dotted path and reports what sits
// there, so an assertion names the coordinate it is about rather than indexing
// through anonymous maps.
func leafAt(t *testing.T, payload json.RawMessage, path string) any {
	t.Helper()
	var decoded any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	cur := decoded
	start := 0
	for i := 0; i <= len(path); i++ {
		if i != len(path) && path[i] != '.' {
			continue
		}
		seg := path[start:i]
		start = i + 1
		m, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("path %q: segment %q is not reachable; %v is not an object", path, seg, cur)
		}
		cur, ok = m[seg]
		if !ok {
			t.Fatalf("path %q: segment %q is absent", path, seg)
		}
	}
	return cur
}

// assertWithheld requires the value at a coordinate to be the withheld marker,
// read as the marker rather than as a string comparison: the assertion is about
// the SHAPE a reader recognises, and a string compare would pass on a document
// that merely mentions the key.
func assertWithheld(t *testing.T, subject string, got any, reason redact.WithheldReason) {
	t.Helper()
	m, ok := got.(map[string]any)
	if !ok {
		t.Errorf("%s: withheld value is %#v, which is not an object; a reader cannot tell it from an authored value", subject, got)
		return
	}
	if len(m) != 1 {
		t.Errorf("%s: withheld marker carries %d keys, want exactly 1; a reader recognises the marker by its single key", subject, len(m))
		return
	}
	v, ok := m[redact.WithheldMarkerKey()]
	if !ok {
		t.Errorf("%s: withheld marker has no %q key; the object is %#v", subject, redact.WithheldMarkerKey(), m)
		return
	}
	if v != string(reason) {
		t.Errorf("%s: withheld marker reason is %v, want %q", subject, v, reason)
	}
}

// markedGraph is the one fixture the node-pillar tests read. It carries, in one
// node, a directly declared leaf, two leaves beneath a declared object, a leaf
// two levels down, an infra leaf, and — deliberately — public siblings at each
// of those depths, including one whose name carries a declared leaf's name as a
// textual prefix.
func markedGraph(t *testing.T) *types.IRGraph {
	t.Helper()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("app", types.IRNode{
		Component: "mark/app",
		SensitivePayloadLeaves: []string{
			"values.dbPassword",
			"values.credentials.username",
			"values.credentials.password",
			"values.outer.inner.secret",
			"infra.backup.passphrase",
		},
	})

	values := types.OrderedMap[json.RawMessage]{}
	values.Set("app", json.RawMessage(`{"appName":"demo","dbPassword":"hunter2","credentials":{"username":"admin","password":"nested"},"tuning":{"poolSize":25},"dbPasswordExtra":"not-a-secret","outer":{"public":"visible","inner":{"secret":"deep"}}}`))

	infra := types.OrderedMap[json.RawMessage]{}
	infra.Set("app", json.RawMessage(`{"replicas":2,"backup":{"passphrase":"backup-secret"}}`))

	g := &types.IRGraph{}
	g.Spec.Nodes = nodes
	g.Spec.ValuesMapping = values
	g.Spec.InfraMapping = infra
	return g
}

// TestGraph_NodePillarsWithholdDeclaredLeaves is the surface the defect lived
// on: a node's own authored payloads, emitted whole by every structured
// serialization of the graph. Each row names one coordinate and why it is the
// case it is, so a failure says which reading of the marking broke.
func TestGraph_NodePillarsWithholdDeclaredLeaves(t *testing.T) {
	t.Parallel()
	out := redact.Graph(markedGraph(t))

	valuesPayload, ok := out.Spec.ValuesMapping.Get("app")
	if !ok {
		t.Fatalf("values payload for node app is absent from the result")
	}
	infraPayload, ok := out.Spec.InfraMapping.Get("app")
	if !ok {
		t.Fatalf("infra payload for node app is absent from the result")
	}

	for _, row := range []struct {
		payload json.RawMessage
		path    string
		why     string
	}{
		{valuesPayload, "dbPassword", "declared at the leaf"},
		{valuesPayload, "credentials.username", "declared on the containing object, which names both children"},
		{valuesPayload, "credentials.password", "the second child of that one declaration"},
		{valuesPayload, "outer.inner.secret", "two levels beneath the declaration, so the descent climbs more than one segment"},
		{infraPayload, "backup.passphrase", "the infra pillar, which a reading of values alone would leave in the clear"},
	} {
		assertWithheld(t, row.path+" ("+row.why+")", leafAt(t, row.payload, row.path), redact.WithheldDeclared)
	}
}

// TestGraph_NodePillarsLeavePublicLeavesAlone is the other half, and it is the
// half with no cheap undo: over-withholding declares public data secret, and a
// reader then cannot see a value its author deliberately left readable.
//
// The prefix row is the sharpest. dbPasswordExtra carries a declared leaf's name
// as a TEXTUAL prefix, so a covering rule trimming characters rather than whole
// path segments would withhold it.
func TestGraph_NodePillarsLeavePublicLeavesAlone(t *testing.T) {
	t.Parallel()
	out := redact.Graph(markedGraph(t))
	valuesPayload, _ := out.Spec.ValuesMapping.Get("app")
	infraPayload, _ := out.Spec.InfraMapping.Get("app")

	for _, row := range []struct {
		payload json.RawMessage
		path    string
		want    any
		why     string
	}{
		{valuesPayload, "appName", "demo", "a public top-level leaf nothing declares"},
		{valuesPayload, "tuning.poolSize", float64(25), "a leaf beneath an object nothing declares"},
		{valuesPayload, "dbPasswordExtra", "not-a-secret", "a sibling whose name has a declared leaf's name as a textual prefix"},
		{valuesPayload, "outer.public", "visible", "a public sibling of the declared object, at the same depth"},
		{infraPayload, "replicas", float64(2), "a public infra leaf beside a declared infra object"},
	} {
		if got := leafAt(t, row.payload, row.path); got != row.want {
			t.Errorf("%s is %#v, want %#v — %s; withholding it declares public data secret",
				row.path, got, row.want, row.why)
		}
	}
}

// TestGraph_AttachmentAndBoundaryPillarsWithhold covers the two surfaces that
// carry authored data outside a node's own payloads. Each reads its OWN marking
// rather than the node's, because an attachment authors against its Feature's
// schema and a node against its Component's, and neither answer is recoverable
// from the other.
func TestGraph_AttachmentAndBoundaryPillarsWithhold(t *testing.T) {
	t.Parallel()

	entry := types.IRFeatureEntry{
		Type:                   "feature.mark/db-credential",
		Values:                 om("secretName", "db-creds", "vault", om("token", "t-secret", "path", "p-secret")),
		Infra:                  om("region", "eu-west-1", "key", "k-secret"),
		SensitivePayloadLeaves: []string{"values.vault.token", "values.vault.path", "infra.key"},
	}
	attachments := types.OrderedMap[types.IRFeatureEntry]{}
	attachments.Set("credentials", entry)
	byNode := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	byNode.Set("app", attachments)

	boundaryAttachment := types.IRBoundaryFeature{
		Type:                   "feature.mark/boundary-guard",
		Infra:                  om("note", "boundary-wide", "apiKey", "a-secret"),
		SensitivePayloadLeaves: []string{"infra.apiKey"},
	}
	boundaryAttachments := types.OrderedMap[types.IRBoundaryFeature]{}
	boundaryAttachments.Set("guard", boundaryAttachment)
	byBoundary := types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]]{}
	byBoundary.Set("cluster", boundaryAttachments)

	g := &types.IRGraph{}
	g.Spec.FeatureValuesMapping = byNode
	g.Spec.BoundaryFeatures = byBoundary

	out := redact.Graph(g)

	gotAttachments, _ := out.Spec.FeatureValuesMapping.Get("app")
	gotEntry, _ := gotAttachments.Get("credentials")
	vault, _ := gotEntry.Values.Get("vault")
	vaultMap, ok := vault.(types.OrderedMap[any])
	if !ok {
		t.Fatalf("attachment values.vault is %T, want an ordered map", vault)
	}
	token, _ := vaultMap.Get("token")
	assertWithheldGo(t, "attachment values.vault.token", token, redact.WithheldDeclared)
	key, _ := gotEntry.Infra.Get("key")
	assertWithheldGo(t, "attachment infra.key", key, redact.WithheldDeclared)

	if name, _ := gotEntry.Values.Get("secretName"); name != "db-creds" {
		t.Errorf("attachment values.secretName is %#v, want the authored value; it is a public locator its author left undeclared", name)
	}
	if region, _ := gotEntry.Infra.Get("region"); region != "eu-west-1" {
		t.Errorf("attachment infra.region is %#v, want the authored value", region)
	}

	gotBoundaryAttachments, _ := out.Spec.BoundaryFeatures.Get("cluster")
	gotBoundary, _ := gotBoundaryAttachments.Get("guard")
	apiKey, _ := gotBoundary.Infra.Get("apiKey")
	assertWithheldGo(t, "boundary attachment infra.apiKey", apiKey, redact.WithheldDeclared)
	if note, _ := gotBoundary.Infra.Get("note"); note != "boundary-wide" {
		t.Errorf("boundary attachment infra.note is %#v, want the authored value", note)
	}
}

// assertWithheldGo is assertWithheld for a value still in its Go shape, before
// any encoding. The two exist because the marker has to be recognisable on both
// sides of the encoding boundary, and a test asserting only the encoded side
// would pass on a value that encodes correctly by accident.
func assertWithheldGo(t *testing.T, subject string, got any, reason redact.WithheldReason) {
	t.Helper()
	m, ok := got.(map[string]string)
	if !ok {
		t.Errorf("%s: withheld value is %#v (%T), want the marker object", subject, got, got)
		return
	}
	want := markerFor(reason)
	if len(m) != len(want) || m[redact.WithheldMarkerKey()] != want[redact.WithheldMarkerKey()] {
		t.Errorf("%s: withheld marker is %#v, want %#v", subject, m, want)
	}
}

// TestGraph_UnmarkedPayloadIsUntouched is the non-regression half. A graph whose
// nodes declare nothing must emit exactly the bytes it emitted before this
// existed — asserted as byte identity on the payload, because a re-encode that
// merely produced equivalent JSON would still move the output of a contract that
// promises byte-identical results for identical input.
func TestGraph_UnmarkedPayloadIsUntouched(t *testing.T) {
	t.Parallel()
	const authored = `{"appName":"demo","tuning":{"poolSize":25}}`

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("app", types.IRNode{Component: "mark/app"})
	values := types.OrderedMap[json.RawMessage]{}
	values.Set("app", json.RawMessage(authored))

	g := &types.IRGraph{}
	g.Spec.Nodes = nodes
	g.Spec.ValuesMapping = values

	out := redact.Graph(g)
	got, _ := out.Spec.ValuesMapping.Get("app")
	if string(got) != authored {
		t.Errorf("an unmarked payload was rewritten.\n  got:  %s\n  want: %s", got, authored)
	}
}

// TestGraph_UnreadableInputWithholdsWholly states the fail-safe, in the two
// shapes that reach it. Failing to read a payload is indistinguishable from
// reading one whose every leaf is public, so the only safe reading of either is
// that no leaf under it could be judged.
func TestGraph_UnreadableInputWithholdsWholly(t *testing.T) {
	t.Parallel()

	for _, row := range []struct {
		name    string
		nodes   func() types.OrderedMap[types.IRNode]
		payload string
		why     string
	}{
		{
			name: "a payload that does not decode",
			nodes: func() types.OrderedMap[types.IRNode] {
				n := types.OrderedMap[types.IRNode]{}
				n.Set("app", types.IRNode{SensitivePayloadLeaves: []string{"values.dbPassword"}})
				return n
			},
			payload: `{"dbPassword": NOT-JSON`,
			why:     "no leaf under an undecodable payload can be judged",
		},
		{
			name:    "a payload keyed by a node the graph does not carry",
			nodes:   func() types.OrderedMap[types.IRNode] { return types.OrderedMap[types.IRNode]{} },
			payload: `{"dbPassword":"hunter2"}`,
			why:     "the marking lives on the node, so an absent node leaves nothing to read the declarations from",
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			values := types.OrderedMap[json.RawMessage]{}
			values.Set("app", json.RawMessage(row.payload))
			g := &types.IRGraph{}
			g.Spec.Nodes = row.nodes()
			g.Spec.ValuesMapping = values

			got, _ := redact.Graph(g).Spec.ValuesMapping.Get("app")
			var decoded any
			if err := json.Unmarshal(got, &decoded); err != nil {
				t.Fatalf("the withheld payload does not decode: %v (%s)", err, got)
			}
			assertWithheld(t, row.name+" — "+row.why, decoded, redact.WithheldUnreadable)
		})
	}
}

// TestGraph_InputIsNotMutated is what keeps the withholding on the display side
// of the boundary. The graph handed in is the one the pipeline hands onward, so
// a substitution reaching it would reach the deployable — which is the one place
// a placeholder is a broken deployment rather than a protected one.
func TestGraph_InputIsNotMutated(t *testing.T) {
	t.Parallel()
	in := markedGraph(t)
	before, _ := in.Spec.ValuesMapping.Get("app")
	authored := string(before)

	_ = redact.Graph(in)

	after, _ := in.Spec.ValuesMapping.Get("app")
	if string(after) != authored {
		t.Errorf("the input graph was mutated.\n  got:  %s\n  want: %s", after, authored)
	}
}

// TestWithheldMarker_UnrecognisedReasonFallsBack states the producing side of the
// reader's fallback. A reason no reader defines would leave a reader deciding
// what it meant, so the fallback is applied here — at the one place a marker is
// built — and it falls back to the reading that claims least.
func TestWithheldMarker_UnrecognisedReasonFallsBack(t *testing.T) {
	t.Parallel()
	got := redact.WithheldMarker(redact.WithheldReason("invented"))
	if got[redact.WithheldMarkerKey()] != string(redact.WithheldAttachment) {
		t.Errorf("an unrecognised reason produced %#v, want the attachment-wide reason, which claims least", got)
	}
	if len(got) != 1 {
		t.Errorf("the marker carries %d keys, want exactly 1", len(got))
	}
}

// TestWithheldMarker_IsFreshPerCall keeps two withheld positions from being one
// shared value. A document encoder emits a repeated reference as an alias of its
// first occurrence, which a reader joining on the marker key would have to
// resolve before it could recognise the marker at all.
func TestWithheldMarker_IsFreshPerCall(t *testing.T) {
	t.Parallel()
	first := redact.WithheldMarker(redact.WithheldDeclared)
	second := redact.WithheldMarker(redact.WithheldDeclared)
	first["probe"] = "mutated"
	if _, leaked := second["probe"]; leaked {
		t.Error("two markers share one map; a document encoder would emit the second as an alias of the first")
	}
}
