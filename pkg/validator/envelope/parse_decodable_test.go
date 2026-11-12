// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package envelope_test

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// templateBearingDoc carries an unquoted template expression in free-form value
// content and declared references beside it, so a test can tell what the repair
// touches from what it must leave alone.
const templateBearingDoc = `
apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: template-bearing
  version: "1.0.0"
spec:
  aggregates:
    billing:
      type: acme/billing:1.2.3
  components:
    api:
      type: core/server
      features:
        tls:
          type: core/tls
      values:
        cache_ttl_seconds: {{ .parameters.cache_ttl_seconds | default "3000" }}
        plain: kept
`

// TestParseDecodable_ReachesTypedDecodeWithoutAFailedDecode is the contract in
// one test: the repair is called directly — no prior decode, failed or
// otherwise — and its result feeds a TYPED decode rather than the generic one
// the envelope runs. A caller needing an entity therefore reaches the repair
// without first having to fail at anything.
func TestParseDecodable_ReachesTypedDecodeWithoutAFailedDecode(t *testing.T) {
	t.Parallel()

	node, err := envelope.ParseDecodable([]byte(templateBearingDoc), "aggregate.yaml")
	if err != nil {
		t.Fatalf("template-bearing document must parse: %v", err)
	}

	var agg types.Aggregate
	if err := node.Decode(&agg); err != nil {
		t.Fatalf("repaired tree must decode into the typed entity: %v", err)
	}

	// Declared references survive verbatim: they are what a caller reading the
	// tree for structure depends on.
	sub, ok := agg.Spec.Aggregates.Get("billing")
	if !ok || sub.Type != "acme/billing:1.2.3" {
		t.Errorf("sub-aggregate reference must survive; got %q (present=%v)", sub.Type, ok)
	}
	ci, ok := agg.Spec.Components.Get("api")
	if !ok || ci.Type != "core/server" {
		t.Errorf("component reference must survive; got %q (present=%v)", ci.Type, ok)
	}
	fi, ok := ci.Features.Get("tls")
	if !ok || fi.Type != "core/tls" {
		t.Errorf("feature reference must survive; got %q (present=%v)", fi.Type, ok)
	}
	if v, ok := ci.Values.Get("plain"); !ok || v != "kept" {
		t.Errorf("a scalar value beside a template must survive; got %v (present=%v)", v, ok)
	}
	// The template-valued entry is present under its authored key; only the
	// expression's own text is replaced.
	if _, ok := ci.Values.Get("cache_ttl_seconds"); !ok {
		t.Error("the authored key of a template-valued entry must survive")
	}
}

// TestParseDecodable_FeedsBothDecodersFromOneTree pins the split the design
// rests on: one repair, two decoders. The same tree is read generically (as the
// envelope reads it) and typed (as an entity loader reads it), and neither
// needs the other's shape.
func TestParseDecodable_FeedsBothDecodersFromOneTree(t *testing.T) {
	t.Parallel()

	node, err := envelope.ParseDecodable([]byte(templateBearingDoc), "aggregate.yaml")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	var generic any
	if err := node.Decode(&generic); err != nil {
		t.Fatalf("repaired tree must decode generically: %v", err)
	}
	var agg types.Aggregate
	if err := node.Decode(&agg); err != nil {
		t.Fatalf("repaired tree must decode typed: %v", err)
	}
	if agg.Metadata.Name != "template-bearing" {
		t.Errorf("typed decode read the wrong document: %q", agg.Metadata.Name)
	}
	if _, isMap := generic.(map[string]any); !isMap {
		t.Errorf("generic decode must yield a mapping; got %T", generic)
	}
}

// TestParseDecodable_RepairsAKeyReachedThroughAnAlias covers the one shape where
// a structural walk and an alias walk can disagree: an anchor on a non-scalar
// key. The key slot takes a placeholder while the anchored node itself stays
// reachable through the alias, so the anchored node must be repaired too — or
// one document acquires two readings.
func TestParseDecodable_RepairsAKeyReachedThroughAnAlias(t *testing.T) {
	t.Parallel()

	doc := `
apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: aliased-key
  version: "1.0.0"
spec:
  components:
    api:
      type: core/server
      values:
        m:
          ? &ak {{ .parameters.x }}
          : v
        ref: *ak
`
	node, err := envelope.ParseDecodable([]byte(doc), "aggregate.yaml")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var generic map[string]any
	if err := node.Decode(&generic); err != nil {
		t.Fatalf("decode: %v", err)
	}

	values := generic["spec"].(map[string]any)["components"].(map[string]any)["api"].(map[string]any)["values"].(map[string]any)

	ref, ok := values["ref"].(map[string]any)
	if !ok {
		t.Fatalf("aliased node must decode as a mapping; got %T", values["ref"])
	}
	for k := range ref {
		if k == "" {
			t.Error("a key reached through an alias was left unrepaired (decoded as the empty key)")
		}
		if !strings.HasPrefix(k, "{{template-key-") {
			t.Errorf("unexpected key through alias: %q", k)
		}
	}
}

// TestParseDecodable_RefusesAnAnchorAliasedInsideItsOwnContent pins the refusal
// that every consumer of a returned tree inherits: such a document describes an
// infinite tree, so nothing may walk it.
func TestParseDecodable_RefusesAnAnchorAliasedInsideItsOwnContent(t *testing.T) {
	t.Parallel()

	doc := `
apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: recursive
  version: "1.0.0"
spec:
  components:
    api:
      type: core/server
      values:
        a: &loop
          self: *loop
`
	if _, err := envelope.ParseDecodable([]byte(doc), "aggregate.yaml"); err == nil {
		t.Fatal("an anchor aliased inside its own content must be refused; got nil")
	} else if !strings.Contains(err.Error(), "loop") {
		t.Errorf("the refusal must name the anchor: %v", err)
	}
}

// TestParseDecodable_LeavesADocumentWithoutTemplatesAlone keeps the repair
// honest about its own reach: a document with no non-scalar key must come back
// carrying no placeholder at all.
func TestParseDecodable_LeavesADocumentWithoutTemplatesAlone(t *testing.T) {
	t.Parallel()

	doc := `
apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: plain
  version: "1.0.0"
spec:
  components:
    api:
      type: core/server
      values:
        a: one
        b: 2
`
	node, err := envelope.ParseDecodable([]byte(doc), "aggregate.yaml")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out, err := yaml.Marshal(node)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), "template-key") {
		t.Errorf("a document without templates must acquire no placeholder:\n%s", out)
	}
}
