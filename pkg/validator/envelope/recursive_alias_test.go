// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package envelope

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// recursiveAliasDoc is a Component that anchors spec and aliases it as its
// own descendant. Expanding the alias reproduces the anchor, which contains
// the alias again: the document denotes an infinite tree.
var recursiveAliasDoc = []byte(`apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/compute/api
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec: &loop
  schema:
    values:
      port: { type: integer }
  nested: *loop
`)

// TestEnvelope_RecursiveAliasReachesTheNodeWalk pins the precondition the
// refusal depends on: the generic decode rejects this document, and the
// node parse that runs in its place accepts it. So the node walk is
// genuinely entered, and the refusal has to sit in front of that walk
// rather than resting on the generic decode having already objected.
//
// The generic decode's own message is deliberately not asserted: the
// refusal must not depend on it, because the decode also fails on
// template-expression keys, and a document carrying both would then be
// refused for the wrong reason or not at all.
func TestEnvelope_RecursiveAliasReachesTheNodeWalk(t *testing.T) {
	t.Parallel()
	var instance any
	if err := yaml.Unmarshal(recursiveAliasDoc, &instance); err == nil {
		t.Fatal("the generic decode is expected to reject a self-containing anchor; it accepted one")
	}
	var node yaml.Node
	if err := yaml.Unmarshal(recursiveAliasDoc, &node); err != nil {
		t.Fatalf("the node parse is expected to accept it, leaving the walk reachable: %v", err)
	}
}

// TestEnvelope_RecursiveAliasRefused pins the halt for a document whose
// anchor is aliased inside its own content. Such a document denotes an
// infinite tree, so the node-to-value conversion cannot terminate on it;
// the halt has to happen before that conversion is entered, and it names
// the anchor so the author knows which one to break.
func TestEnvelope_RecursiveAliasRefused(t *testing.T) {
	t.Parallel()
	_, err := ValidateBytes(KindComponent, recursiveAliasDoc, "recursive.yaml")
	if err == nil {
		t.Fatal("an anchor aliased inside its own content must fail the envelope; got nil")
	}
	if !errors.Is(err, ErrEntityEnvelope()) {
		t.Fatalf("want errors.Is(err, ErrEntityEnvelope); got %v", err)
	}
	msg := err.Error()
	// Both spellings of the anchor's name, because both are things the
	// author typed and has to go and find.
	for _, want := range []string{"&loop", "*loop", "recursive.yaml"} {
		if !strings.Contains(msg, want) {
			t.Errorf("diagnostic must name %q so the author knows what to change; got %v", want, err)
		}
	}
	// A crash reports nothing, so the point of the refusal is that there IS
	// a message. Kept visible in verbose runs: this text is what a reader of
	// the tool actually sees, and it is worth reviewing when it changes.
	t.Logf("author sees: %s", msg)
}

// TestEnvelope_MutuallyRecursiveAliasRefused pins the halt for the
// indirect shape: the alias back to the outer anchor sits under a second
// anchor, and it is reached through an alias to that second anchor. The
// sibling reference to the inner anchor is legitimate on its own; it is
// only the enclosing anchor's reappearance beneath itself that halts.
func TestEnvelope_MutuallyRecursiveAliasRefused(t *testing.T) {
	t.Parallel()
	doc := []byte(`apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/compute/api
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec: &outer
  schema:
    values:
      port: { type: integer }
  inner: &inner
    back: *outer
  again: *inner
`)
	_, err := ValidateBytes(KindComponent, doc, "indirect.yaml")
	if err == nil {
		t.Fatal("an anchor reached beneath itself through a second anchor must fail the envelope; got nil")
	}
	if !errors.Is(err, ErrEntityEnvelope()) {
		t.Fatalf("want errors.Is(err, ErrEntityEnvelope); got %v", err)
	}
	if !strings.Contains(err.Error(), "outer") {
		t.Errorf("diagnostic must name the offending anchor: %v", err)
	}
}

// deepAliasingDoc builds an Aggregate nesting depth levels deep inside
// free-form values content, carrying a template-expression key so the
// document travels the yaml.Node path rather than the generic decode, and
// re-using an anchor as a SIBLING of its own definition. It is legitimate
// on every axis the halt must not touch: deep, alias-bearing, and routed
// through the same code the halt guards.
func deepAliasingDoc(depth int) []byte {
	var b strings.Builder
	b.WriteString(`apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: deep-control
  version: "1.0.0"
spec:
  parameters:
    cache_ttl_seconds:
      type: integer
  components:
    api:
      type: core/server
      values:
        cache_ttl_seconds: {{ .parameters.cache_ttl_seconds | default "3000" }}
        nest: &nest
`)
	indent := "          "
	for i := 0; i < depth; i++ {
		fmt.Fprintf(&b, "%slevel%d:\n", indent, i)
		indent += "  "
	}
	b.WriteString(indent + "leaf: bottom\n")
	// A sibling of the anchor, not a descendant of it: expanding this alias
	// terminates, so it must survive the halt.
	b.WriteString("        reused: *nest\n")
	return []byte(b.String())
}

// TestEnvelope_DeepAliasingDocumentStillLoads is the control for the halt
// above: the refusal is of recursion, never of depth. A document nesting
// far deeper than any hand-authored one, re-using an anchor in the one
// direction that terminates, still validates.
func TestEnvelope_DeepAliasingDocumentStillLoads(t *testing.T) {
	t.Parallel()
	for _, depth := range []int{12, 200} {
		depth := depth
		t.Run(fmt.Sprintf("depth-%d", depth), func(t *testing.T) {
			t.Parallel()
			doc := deepAliasingDoc(depth)
			// Without this, the control could pass by never reaching the
			// refusal at all, and would then say nothing about whether the
			// refusal admits depth. The generic decode has to reject the
			// document for the walk behind the refusal to be entered.
			var instance any
			if err := yaml.Unmarshal(doc, &instance); err == nil {
				t.Fatal("control no longer travels the guarded path: the generic decode accepted it, " +
					"so this document would pass whether or not the refusal admits depth")
			}
			if _, err := ValidateBytes(KindAggregate, doc, "deep.yaml"); err != nil {
				t.Fatalf("a deep, non-recursive alias document must pass the envelope: %v", err)
			}
		})
	}
}
