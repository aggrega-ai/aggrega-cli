// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package envelope

import (
	"errors"
	"strings"
	"testing"
)

// TestEnvelope_ConnectionAttachments is the negative corpus for the attachments a
// connection carries and the slots a socket declares, one fault per document,
// each asserted against the gate's own identifier and against the key it names.
//
// The positive rows are the controls: each refusal row differs from the positive
// row of its document kind by exactly the fault it names, so a refusal cannot be
// an earlier gate objecting to something else in the document.
func TestEnvelope_ConnectionAttachments(t *testing.T) {
	t.Parallel()

	aggregate := func(attachment, featureUses string) []byte {
		return []byte(`apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: connection-attachments
  version: "1.0.0"
spec:
  components:
    gpt:
      type: core/ai-model/openai
    router:
      type: core/gateway/litellm
      uses:
        models:
          gpt:
            target: gpt
            features:
              credential:
                type: core/referenced-secret
                values:
                  origin: team-a
` + attachment + `      features:
        bridge:
          type: core/compose-bridge
          uses:
            peers:
              p:
                target: gpt
` + featureUses)
	}
	subsystem := func(featureUses string) []byte {
		return []byte(`apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: connection-attachments
  version: "1.0.0"
spec:
  subsystems:
    core:
      features:
        bridge:
          type: core/compose-bridge
          uses:
            peers:
              p:
                target: gpt
` + featureUses + `      components:
        gpt:
          type: core/ai-model/openai
`)
	}
	release := func(attachment string) []byte {
		return []byte(`apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: connection-attachments-prod
  environment: production
  version: "1.0.0"
spec:
  aggregate: connection-attachments
  manifest: connection-attachments
  components:
    router:
      uses:
        models:
          gpt:
            features:
              credential:
                infra:
                  rotation: hourly
` + attachment)
	}
	component := func(socketField string) []byte {
		return []byte(`apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/gateway/probe
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
  usesSchema:
    models:
      selector: "component.aggrega.ai/category=ai-model"
      featuresSchema:
        credential:
          selector: "feature.aggrega.ai/kind=secret"
          sensitive: true
          inject: [env]
          required: true
          description: the key this connection presents
` + socketField + `  schema:
    values:
      name: { type: string }
`)
	}
	feature := func(socketField string) []byte {
		return []byte(`apiVersion: aggrega.ai/v1beta1
kind: Feature
metadata:
  name: core/bridge-probe
  version: 1.0.0
spec:
  usesSchema:
    peers:
      selector: "component.aggrega.ai/category=ai-model"
` + socketField)
	}

	cases := []struct {
		name string
		kind Kind
		doc  []byte
		// refusedKey is the fragment of the refusal naming the key and what is wrong
		// with it; empty for a document the envelope accepts.
		refusedKey string
	}{
		{name: "a component's connection carrying an attachment", kind: KindAggregate, doc: aggregate("", "")},
		{
			name:       "a connection attachment declaring a uses block",
			kind:       KindAggregate,
			doc:        aggregate("                uses:\n                  x: [gpt]\n", ""),
			refusedKey: `unexpected property: "uses"`,
		},
		{
			name:       "a connection attachment declaring an inject block",
			kind:       KindAggregate,
			doc:        aggregate("                inject:\n                  env:\n                    - in: K\n                      from: name\n", ""),
			refusedKey: `unexpected property: "inject"`,
		},
		{
			name:       "a connection attachment naming no type",
			kind:       KindAggregate,
			doc:        aggregate("              second:\n                values: {}\n", ""),
			refusedKey: "missing required property: type",
		},
		{
			name: "a connection a component's attachment draws carrying an attachment",
			kind: KindAggregate,
			doc: aggregate("", "                features:\n                  credential:\n"+
				"                    type: core/referenced-secret\n"),
			refusedKey: `unexpected property: "features"`,
		},
		{name: "a connection a subsystem-wide attachment draws", kind: KindAggregate, doc: subsystem("")},
		{
			name: "a connection a subsystem-wide attachment draws carrying an attachment",
			kind: KindAggregate,
			doc: subsystem("                features:\n                  credential:\n" +
				"                    type: core/referenced-secret\n"),
			refusedKey: `unexpected property: "features"`,
		},
		{name: "a Release connection entry carrying an attachment", kind: KindRelease, doc: release("")},
		{
			name:       "a Release connection attachment declaring values",
			kind:       KindRelease,
			doc:        release("                values:\n                  origin: elsewhere\n"),
			refusedKey: `unexpected property: "values"`,
		},
		{
			name:       "a Release connection attachment declaring a uses block",
			kind:       KindRelease,
			doc:        release("                uses:\n                  x: [gpt]\n"),
			refusedKey: `unexpected property: "uses"`,
		},
		{name: "a Component socket declaring slots", kind: KindComponent, doc: component("")},
		{
			name:       "a Component socket slot declaring a key no slot takes",
			kind:       KindComponent,
			doc:        component("          values: {}\n"),
			refusedKey: `unexpected property: "values"`,
		},
		{name: "a Feature socket", kind: KindFeature, doc: feature("")},
		{
			name: "a Feature socket declaring slots",
			kind: KindFeature,
			doc: feature("      featuresSchema:\n        credential:\n" +
				"          selector: \"feature.aggrega.ai/kind=secret\"\n"),
			refusedKey: `unexpected property: "featuresSchema"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var err error
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("validation panicked: %v", r)
					}
				}()
				_, err = ValidateBytes(tc.kind, tc.doc, "document.yaml")
			}()
			if tc.refusedKey == "" {
				if err != nil {
					t.Fatalf("the document must pass its envelope; got %v\n%s", err, tc.doc)
				}
				return
			}
			if err == nil {
				t.Fatalf("the document must be refused for %q; got nil\n%s", tc.refusedKey, tc.doc)
			}
			if !errors.Is(err, ErrEntityEnvelope()) {
				t.Fatalf("want errors.Is(err, ErrEntityEnvelope); got %v", err)
			}
			if !strings.Contains(err.Error(), tc.refusedKey) {
				t.Errorf("the refusal must read %q: %v", tc.refusedKey, err)
			}
		})
	}
}
