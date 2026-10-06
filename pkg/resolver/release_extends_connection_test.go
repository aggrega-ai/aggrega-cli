// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The chain the connection-attachment fold tests compose. The base writes two
// connections of the gateway, one attachment each; the child redeclares one of
// those connections and writes a connection of its own, so every arm — a slot
// both wrote, a slot only the base wrote, a slot and a connection only the child
// wrote, and a key the child removes — is exercised in one fold.
//
// The base also writes the same attachment on the component's own block, and the
// child redeclares it there exactly as it redeclares the connection's, so the
// two surfaces can be compared on identical input.
const connectionFoldBase = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: base-release
  environment: prod
  version: "1.0.0"
spec:
  aggregate: fold-app
  manifest: prod-manifest
  components:
    gw:
      features:
        audit:
          type: core/referenced-secret
          description: the base's key
          infra:
            rotation: hourly
            window: 1h
      uses:
        models:
          gpt:
            features:
              credential:
                type: core/referenced-secret
                description: the base's key
                infra:
                  rotation: hourly
                  window: 1h
              token:
                type: core/generated-secret
          nim:
            features:
              credential:
                infra:
                  rotation: daily
`

const connectionFoldChild = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: child-release
  environment: local
  version: "1.0.0"
spec:
  extends: ./base.yaml
  components:
    gw:
      features:
        audit:
          type: core/generated-secret
          infra:
            window: null
            region: eu
      uses:
        models:
          gpt:
            features:
              credential:
                type: core/generated-secret
                infra:
                  window: null
                  region: eu
          local:
            features:
              credential:
                type: core/referenced-secret
`

// TestResolveReleaseExtends_ComposesConnectionAttachmentsPerSlot proves a child
// Release's connection attachments compose with its parent's per connection and
// per slot, and that nothing either link wrote is dropped: a slot both wrote takes
// the child's type and keeps the parent's description where the child wrote none,
// with its sizing composed key by key and a key the child writes null removed; a
// slot only the parent wrote stands; a connection only the child wrote is
// introduced whole.
//
// It also holds the rule to the one a component's own attachments compose under,
// on identical input written on the two surfaces: the composed connection
// attachment must equal the composed component attachment field for field, which
// a parallel rule could satisfy only by agreeing today.
func TestResolveReleaseExtends_ComposesConnectionAttachmentsPerSlot(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	head := writeFoldChain(t, dir,
		foldFixture{"child.yaml", connectionFoldChild},
		foldFixture{"base.yaml", connectionFoldBase},
	)
	composed, _, err := ResolveReleaseExtends("", dir, head)
	if err != nil {
		t.Fatalf("the chain must fold; got: %v", err)
	}

	// Both links write a top-level components block, which a Release is normalized
	// out of as it loads: the block becomes the synthesized default boundary and is
	// cleared, so the composed gateway is read where the fold leaves it.
	def, ok := composed.Spec.Boundaries.Get("default")
	if !ok {
		t.Fatalf("the composed Release carries no default boundary; it carries %v", composed.Spec.Boundaries.Keys())
	}
	comp, ok := def.Components.Get("gw")
	if !ok {
		t.Fatalf("the composed Release lost the gateway; the default boundary carries %v", def.Components.Keys())
	}
	models, ok := comp.Uses.Get("models")
	if !ok {
		t.Fatalf("the composed gateway lost its models socket; sockets %v", comp.Uses.Keys())
	}
	if got, want := models.Keys(), []string{"gpt", "nim", "local"}; !reflect.DeepEqual(got, want) {
		t.Errorf("connections = %v; want %v (the parent's in its order, then the child's)", got, want)
	}

	// attachment reads one composed connection attachment, failing the test that
	// asks for it when the fold dropped it.
	attachment := func(t *testing.T, alias, slot string) types.ReleaseConnectionAttachment {
		t.Helper()
		entry, ok := models.Get(alias)
		if !ok {
			t.Fatalf("connection %q is absent from the composition", alias)
		}
		att, ok := entry.Features.Get(slot)
		if !ok {
			t.Fatalf("connection %q lost its attachment %q; it carries %v", alias, slot, entry.Features.Keys())
		}
		return att
	}

	t.Run("a slot both wrote composes per pillar", func(t *testing.T) {
		got := attachment(t, "gpt", "credential")
		if got.Type != "core/generated-secret" {
			t.Errorf("type = %q; want the child's core/generated-secret", got.Type)
		}
		if got.Description != "the base's key" {
			t.Errorf("description = %q; want the parent's, which the child never wrote", got.Description)
		}
		wantValueAt(t, got.Infra, "hourly", "rotation")
		wantValueAt(t, got.Infra, "eu", "region")
		wantAbsentAt(t, got.Infra, "window")
	})
	t.Run("a slot only the parent wrote stands", func(t *testing.T) {
		if got := attachment(t, "gpt", "token"); got.Type != "core/generated-secret" {
			t.Errorf("type = %q; want the parent's core/generated-secret", got.Type)
		}
		entry, _ := models.Get("gpt")
		if got, want := entry.Features.Keys(), []string{"credential", "token"}; !reflect.DeepEqual(got, want) {
			t.Errorf("gpt's slots = %v; want %v", got, want)
		}
	})
	t.Run("a connection the child redeclared nothing on stands", func(t *testing.T) {
		wantValueAt(t, attachment(t, "nim", "credential").Infra, "daily", "rotation")
	})
	t.Run("a connection only the child wrote is introduced", func(t *testing.T) {
		if got := attachment(t, "local", "credential"); got.Type != "core/referenced-secret" {
			t.Errorf("type = %q; want the child's core/referenced-secret", got.Type)
		}
	})
	t.Run("the connection's rule is the component's rule", func(t *testing.T) {
		own, ok := comp.Features.Get("audit")
		if !ok {
			t.Fatalf("the composed gateway lost its own attachment; it carries %v", comp.Features.Keys())
		}
		conn := attachment(t, "gpt", "credential")
		if conn.Type != own.Type || conn.Description != own.Description || !reflect.DeepEqual(conn.Infra, own.Infra) {
			t.Errorf("the same input composed differently on the two surfaces:\n connection %+v\n component  %+v",
				conn, own)
		}
	})
}
