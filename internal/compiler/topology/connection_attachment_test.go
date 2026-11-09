// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// connectionCatalog declares a consuming Component whose "models" socket declares
// one slot and whose "logs" socket declares none, and a component to connect to.
func connectionCatalog(t *testing.T) resolver.CatalogLookup {
	t.Helper()
	const gwYAML = `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: app/gateway
  version: "1.0.0"
spec:
  usesSchema:
    models:
      selector: "component.aggrega.ai/category=ai-model"
      featuresSchema:
        credential:
          selector: "feature.aggrega.ai/kind=secret"
          sensitive: true
    logs:
      selector: "component.aggrega.ai/category=sink"
`
	const modelYAML = `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: app/model
  version: "1.0.0"
spec: {}
`
	var gw, model types.Component
	if err := yaml.Unmarshal([]byte(gwYAML), &gw); err != nil {
		t.Fatalf("gateway fixture parse: %v", err)
	}
	if err := yaml.Unmarshal([]byte(modelYAML), &model); err != nil {
		t.Fatalf("model fixture parse: %v", err)
	}
	return resolver.CatalogLookup{
		Component: func(u string) (types.Component, bool) {
			switch u {
			case "app/gateway:1.0.0":
				return gw, true
			case "app/model:1.0.0":
				return model, true
			}
			return types.Component{}, false
		},
	}
}

// connectionComponents is a components block — a gateway consuming two model
// connections and a third without an attachment — written at the indentation a
// document's spec.components takes, with the gateway's connections supplied.
func connectionComponents(uses string) string {
	return `    gw:
      type: app/gateway:1.0.0
      uses:
` + indentBlock(uses, 8) + `    gpt:
      type: app/model:1.0.0
    nim:
      type: app/model:1.0.0
`
}

// indentBlock prefixes every non-empty line of s with n spaces.
func indentBlock(s string, n int) string {
	pad := strings.Repeat(" ", n)
	var b strings.Builder
	for _, line := range strings.SplitAfter(s, "\n") {
		if strings.TrimSpace(line) != "" {
			b.WriteString(pad)
		}
		b.WriteString(line)
	}
	return b.String()
}

const twoFilledConnections = `models:
  gpt:
    target: gpt
    features:
      credential:
        type: core/referenced-secret
        values:
          key: openai-team-a
  nim:
    target: nim
    features:
      credential:
        type: core/referenced-secret
        description: the NIM key
  plain:
    target: gpt
`

// moduleRoot is an Aggregate composing one sub-module under the prefix "mod".
const moduleRoot = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: demo
spec:
  aggregates:
    mod:
      type: ./mod.yaml
`

func moduleDocument(uses string) string {
	return `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: mod
spec:
  components:
` + connectionComponents(uses)
}

func rootDocument(uses string) string {
	return `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: demo
spec:
  components:
` + connectionComponents(uses)
}

// loweredAttachment is what one attachment of a component says about where it
// came from, read off the attachment the composition carries.
type loweredAttachment struct {
	name        string
	connection  *types.AttachmentConnection
	featureType string
	description string
	key         any
}

func loweredAttachments(attachments []FeatureAttachment) []loweredAttachment {
	var out []loweredAttachment
	for _, a := range attachments {
		key, _ := a.Feature.Values.Get("key")
		out = append(out, loweredAttachment{
			name:        a.InstanceName,
			connection:  a.Connection,
			featureType: a.Feature.Type,
			description: a.Feature.Description,
			key:         key,
		})
	}
	return out
}

// TestConnectionAttachmentsAreLoweredOntoTheConsumer proves an attachment written
// on a connection becomes an attachment of the CONSUMING component, under the name
// the alias and the slot compose, carrying the connection it was written on as
// data — for a component the Aggregate declares and for one a sub-module
// contributes alike, after the component's own attachment block and in author
// order. The target of the connection gains nothing, and a connection carrying no
// attachment contributes none.
func TestConnectionAttachmentsAreLoweredOntoTheConsumer(t *testing.T) {
	t.Parallel()

	gptCredential := loweredAttachment{
		name:        types.JoinConnectionAttachmentName("gpt", "credential"),
		connection:  &types.AttachmentConnection{Socket: "models", Alias: "gpt", Attachment: "credential"},
		featureType: "core/referenced-secret",
		key:         "openai-team-a",
	}
	nimCredential := loweredAttachment{
		name:        types.JoinConnectionAttachmentName("nim", "credential"),
		connection:  &types.AttachmentConnection{Socket: "models", Alias: "nim", Attachment: "credential"},
		featureType: "core/referenced-secret",
		description: "the NIM key",
	}

	cases := []struct {
		name     string
		agg      string
		mod      string
		consumer string
		target   string
		want     []loweredAttachment
	}{
		{
			name:     "a component the Aggregate declares",
			agg:      rootDocument(twoFilledConnections),
			consumer: "gw",
			target:   "gpt",
			want:     []loweredAttachment{gptCredential, nimCredential},
		},
		{
			name: "after the component's own attachment block",
			agg: strings.Replace(rootDocument(twoFilledConnections),
				"      type: app/gateway:1.0.0\n",
				"      type: app/gateway:1.0.0\n      features:\n        audit:\n          type: core/audit-log\n", 1),
			consumer: "gw",
			target:   "gpt",
			want: []loweredAttachment{
				{name: "audit", featureType: "core/audit-log"},
				gptCredential, nimCredential,
			},
		},
		{
			name:     "a component a sub-module contributes",
			agg:      moduleRoot,
			mod:      moduleDocument(twoFilledConnections),
			consumer: "mod.gw",
			target:   "mod.gpt",
			want:     []loweredAttachment{gptCredential, nimCredential},
		},
		{
			name:     "a connection carrying no attachment contributes none",
			agg:      rootDocument("models:\n  gpt:\n    target: gpt\n"),
			consumer: "gw",
			target:   "gpt",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, err := buildResultWithCatalog(t, connectionCatalog(t), tc.agg, "", tc.mod)
			if err != nil {
				t.Fatalf("the composition must build; got %v", err)
			}
			got := loweredAttachments(result.FeatureAttachments[tc.consumer])
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("attachments of %s = %+v; want %+v", tc.consumer, got, tc.want)
			}
			if target := result.FeatureAttachments[tc.target]; len(target) != 0 {
				t.Errorf("the connection's target gained attachments %+v; a connection's attachment belongs to its consumer", loweredAttachments(target))
			}
		})
	}
}

// TestModuleConnectionAttachmentSlotsAreRefusedOnce proves the declared-slots rule
// is asked once the composition is flattened of the names a sub-module's own
// document wrote on its component's connections, and of every name the Release
// wrote on a connection entry — for a component the Aggregate declares as for one
// a sub-module contributes — in both directions; and that the developer's names on
// a component the Aggregate declares are left to the earlier half, so one fault
// is reported once. A Release finding is positioned at the entry the operator
// wrote, inside the boundary it sits in, and names a declared slot within one edit
// of the refused name.
//
// Each row carries one fault or none. The Release places every component, so a
// row halts only for the fault it carries.
func TestModuleConnectionAttachmentSlotsAreRefusedOnce(t *testing.T) {
	t.Parallel()

	const declared = "models:\n  gpt:\n    target: gpt\n    features:\n      credential:\n        type: core/referenced-secret\n"
	const undeclared = "models:\n  gpt:\n    target: gpt\n    features:\n      token:\n        type: core/referenced-secret\n"
	const onSlotlessSocket = "logs:\n  sink:\n    target: gpt\n    features:\n      credential:\n        type: core/referenced-secret\n"
	// release places the three components and writes gwUses on the gateway's
	// connections: under the boundary's own components for a component the
	// Aggregate declares, and through the module's routing entry for one the
	// sub-module contributes.
	release := func(throughModule bool, gwUses string) string {
		components := "      components:\n"
		pad := 8
		if throughModule {
			components = "      aggregates:\n        mod:\n          components:\n"
			pad = 12
		}
		at := strings.Repeat(" ", pad)
		return `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
` + components + at + "gw:\n" + at + "  uses:\n" + indentBlock(gwUses, pad+4) +
			at + "gpt: {}\n" + at + "nim: {}\n"
	}

	type finding struct {
		entity string
		field  string
		// near is the declared slot the finding must name as the one the refused
		// name sits near, empty where it must name none.
		near string
	}
	cases := []struct {
		name    string
		agg     string
		mod     string
		release string
		want    []finding
	}{
		{
			name: "the module fills the slot the socket declares",
			agg:  moduleRoot,
			mod:  moduleDocument(declared),
		},
		{
			name: "the module writes a name the socket declares no slot for",
			agg:  moduleRoot,
			mod:  moduleDocument(undeclared),
			want: []finding{{entity: "Aggregate mod.yaml", field: ".spec.components.gw.uses.models.gpt"}},
		},
		{
			name: "the module writes an attachment on a socket declaring no slots",
			agg:  moduleRoot,
			mod:  moduleDocument(onSlotlessSocket),
			want: []finding{{entity: "Aggregate mod.yaml", field: ".spec.components.gw.uses.logs.sink"}},
		},
		{
			name: "the module writes an attachment on a socket its type does not declare",
			agg:  moduleRoot,
			mod: moduleDocument(
				"backups:\n  spare:\n    target: gpt\n    features:\n      credential:\n        type: core/referenced-secret\n"),
			want: []finding{{entity: "Aggregate mod.yaml", field: ".spec.components.gw.uses.backups.spare"}},
		},
		{
			name:    "the operator sizes the slot the module filled",
			agg:     moduleRoot,
			mod:     moduleDocument(declared),
			release: release(true, "models:\n  gpt:\n    features:\n      credential:\n        infra:\n          rotation: hourly\n"),
		},
		{
			name:    "the operator writes a name the socket declares no slot for",
			agg:     moduleRoot,
			mod:     moduleDocument(declared),
			release: release(true, "models:\n  gpt:\n    features:\n      token:\n        type: core/referenced-secret\n"),
			want: []finding{{
				entity: "Release demo-prod",
				field:  ".spec.boundaries.prod.aggregates.mod.components.gw.uses.models.gpt",
			}},
		},
		{
			name:    "the operator writes a name one edit from a declared slot",
			agg:     moduleRoot,
			mod:     moduleDocument(declared),
			release: release(true, "models:\n  gpt:\n    features:\n      credentail:\n        type: core/referenced-secret\n"),
			want: []finding{{
				entity: "Release demo-prod",
				field:  ".spec.boundaries.prod.aggregates.mod.components.gw.uses.models.gpt",
				near:   "credential",
			}},
		},
		{
			// The Aggregate's own components are judged while the authoring
			// documents are read; this stage leaves the developer's names on them
			// alone.
			name: "a component the Aggregate declares is the earlier half's",
			agg:  rootDocument(undeclared),
		},
		{
			name:    "the operator sizes the slot the developer filled on a component the Aggregate declares",
			agg:     rootDocument(declared),
			release: release(false, "models:\n  gpt:\n    features:\n      credential:\n        infra:\n          rotation: hourly\n"),
		},
		{
			// The operator's names are judged here for every component, so the
			// finding names the entry the operator wrote rather than a path shaped
			// like the developer's, which the Release does not contain.
			name:    "the operator writes a name no slot declares on a component the Aggregate declares",
			agg:     rootDocument(declared),
			release: release(false, "models:\n  gpt:\n    features:\n      token:\n        type: core/referenced-secret\n"),
			want: []finding{{
				entity: "Release demo-prod",
				field:  ".spec.boundaries.prod.components.gw.uses.models.gpt",
			}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var err error
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("the build panicked: %v", r)
					}
				}()
				_, err = buildResultWithCatalog(t, connectionCatalog(t), tc.agg, tc.release, tc.mod)
			}()
			if len(tc.want) == 0 {
				if err != nil {
					t.Fatalf("the composition must build; got %v", err)
				}
				return
			}
			var multi *resolver.MultiResolverError
			if !errors.As(err, &multi) {
				t.Fatalf("the build must halt with a group of findings; got %v", err)
			}
			var got []finding
			for i, v := range multi.Violations {
				if !errors.Is(v, resolver.ErrConnectionAttachmentSlotUndeclared()) {
					t.Errorf("finding %v does not carry the declared-slots condition", v)
					continue
				}
				near := ""
				if i < len(tc.want) && tc.want[i].near != "" &&
					strings.Contains(v.Message, `near the declared slot "`+tc.want[i].near+`"`) {
					near = tc.want[i].near
				}
				got = append(got, finding{entity: v.Entity, field: v.Field, near: near})
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("findings = %+v; want %+v", got, tc.want)
			}
		})
	}
}
