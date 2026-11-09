// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types_test

import (
	"errors"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

const validPlugin = `apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata:
  name: publishing-linter
  pluginType: linter
  version: 1.0.0
spec:
  abiVersion: v1alpha1
  signature:
    type: sigstore-cosign
    publicKeyRef: cosign.pub
  runtime:
    maxMemoryPages: 16
    timeoutMs: 5000
    hostFunctions: [log]
    configKV:
      ZEBRA: last
      ALPHA: first
      MIKE: middle
`

// TestPlugin_EnvelopeAcceptsValidInstance is the positive control: a
// fully-populated Plugin validates through the registered kind with zero
// findings, proving the kind is wired into the envelope validator.
func TestPlugin_EnvelopeAcceptsValidInstance(t *testing.T) {
	t.Parallel()
	if _, err := envelope.ValidateBytes(envelope.KindPlugin, []byte(validPlugin), "plugin.yaml"); err != nil {
		t.Fatalf("valid Plugin rejected: %v", err)
	}
}

// TestPlugin_RoundTripPreservesConfigKVOrder proves the entity unmarshals
// into the typed struct and re-marshals with configKV in author order — the
// determinism contract for the ordered string→string runtime map.
func TestPlugin_RoundTripPreservesConfigKVOrder(t *testing.T) {
	t.Parallel()
	var wp types.Plugin
	if err := yaml.Unmarshal([]byte(validPlugin), &wp); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if wp.Spec.Runtime == nil {
		t.Fatal("runtime block lost on unmarshal")
	}
	gotKeys := wp.Spec.Runtime.ConfigKV.Keys()
	wantKeys := []string{"ZEBRA", "ALPHA", "MIKE"}
	if len(gotKeys) != len(wantKeys) {
		t.Fatalf("configKV keys = %v, want %v", gotKeys, wantKeys)
	}
	for i := range wantKeys {
		if gotKeys[i] != wantKeys[i] {
			t.Fatalf("configKV author order not preserved: got %v, want %v", gotKeys, wantKeys)
		}
	}

	// Re-marshal and re-parse: the key order survives the round-trip.
	out, err := yaml.Marshal(&wp)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	var again types.Plugin
	if err := yaml.Unmarshal(out, &again); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if got := again.Spec.Runtime.ConfigKV.Keys(); len(got) != 3 || got[0] != "ZEBRA" || got[2] != "MIKE" {
		t.Fatalf("configKV order drifted across round-trip: %v", got)
	}
}

// TestPlugin_OmittedOptionalBlocksRoundTripClean proves a minimal entity
// (no signature, no runtime) round-trips without re-emitting an empty block —
// the reason the nested blocks are pointer-typed.
func TestPlugin_OmittedOptionalBlocksRoundTripClean(t *testing.T) {
	t.Parallel()
	const minimal = `apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata:
  name: p
  pluginType: provider
  version: 1.0.0
spec:
  abiVersion: v1alpha1
`
	var wp types.Plugin
	if err := yaml.Unmarshal([]byte(minimal), &wp); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if wp.Spec.Signature != nil || wp.Spec.Runtime != nil {
		t.Fatalf("optional blocks should be nil when omitted; got signature=%v runtime=%v", wp.Spec.Signature, wp.Spec.Runtime)
	}
	out, err := yaml.Marshal(&wp)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	// The re-marshalled document must validate (no `signature: {}` /
	// `runtime: {}` blocks that would violate required-field constraints).
	if _, err := envelope.ValidateBytes(envelope.KindPlugin, out, "min.yaml"); err != nil {
		t.Fatalf("re-marshalled minimal plugin failed envelope: %v\n%s", err, out)
	}
}

// TestPlugin_EnvelopeRejectsMalformed proves the envelope schema halts the
// authoring mistakes the kind must catch: an unknown plugin type, an unknown
// ABI version, and an unknown spec sibling (additionalProperties is closed).
func TestPlugin_EnvelopeRejectsMalformed(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"unknown pluginType": `apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata: {name: p, version: 1.0.0, pluginType: nonexistent}
spec:
  abiVersion: v1alpha1
`,
		"unknown abiVersion": `apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata: {name: p, version: 1.0.0, pluginType: linter}
spec:
  abiVersion: v2
`,
		"unknown spec sibling": `apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata: {name: p, version: 1.0.0, pluginType: linter}
spec:
  abiVersion: v1alpha1
  allowedHosts: [example.com]
`,
	}
	for name, src := range cases {
		src := src
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := envelope.ValidateBytes(envelope.KindPlugin, []byte(src), "bad.yaml")
			if err == nil {
				t.Fatalf("expected envelope rejection for %q; got nil", name)
			}
			if !errors.Is(err, envelope.ErrEntityEnvelope()) {
				t.Fatalf("expected ErrEntityEnvelope for %q; got %v", name, err)
			}
		})
	}
}

const pluginWithExports = `apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata:
  name: publishing-linter
  pluginType: linter
  version: 1.0.0
spec:
  abiVersion: v1alpha1
  exports:
    - name: lint_naming
      operates: patch
      scope: own
      selector: workloads carrying a pod template
    - name: step
      operates: emit
      scope: host
`

// TestPlugin_EnvelopeAcceptsExports is the positive control for the
// per-export capability block: a plugin declaring an exports array with the
// operates/scope enums and a free-form selector validates through the kind.
func TestPlugin_EnvelopeAcceptsExports(t *testing.T) {
	t.Parallel()
	if _, err := envelope.ValidateBytes(envelope.KindPlugin, []byte(pluginWithExports), "plugin.yaml"); err != nil {
		t.Fatalf("Plugin with exports rejected: %v", err)
	}
}

// TestPlugin_ExportsAreAdditive proves the exports block is additive: a
// document that omits exports validates exactly as one that declares them — so
// existing Plugin documents authored before the block stay valid.
func TestPlugin_ExportsAreAdditive(t *testing.T) {
	t.Parallel()
	const withoutExports = `apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata:
  name: publishing-linter
  pluginType: linter
  version: 1.0.0
spec:
  abiVersion: v1alpha1
`
	if _, err := envelope.ValidateBytes(envelope.KindPlugin, []byte(withoutExports), "no-exports.yaml"); err != nil {
		t.Fatalf("Plugin omitting exports rejected (the block must be additive): %v", err)
	}
}

// TestPlugin_ExportsRoundTripPreservesOrder proves the exports array
// unmarshals into the typed slice, preserves author order and each capability
// field, and survives a marshal→unmarshal round-trip.
func TestPlugin_ExportsRoundTripPreservesOrder(t *testing.T) {
	t.Parallel()
	var wp types.Plugin
	if err := yaml.Unmarshal([]byte(pluginWithExports), &wp); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if got := len(wp.Spec.Exports); got != 2 {
		t.Fatalf("exports length = %d, want 2", got)
	}
	first := wp.Spec.Exports[0]
	if first.Name != "lint_naming" || first.Operates != "patch" || first.Scope != "own" || first.Selector != "workloads carrying a pod template" {
		t.Fatalf("first export not parsed field-for-field: %+v", first)
	}
	if second := wp.Spec.Exports[1]; second.Name != "step" || second.Operates != "emit" || second.Scope != "host" {
		t.Fatalf("second export not parsed field-for-field: %+v", second)
	}

	out, err := yaml.Marshal(&wp)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	var again types.Plugin
	if err := yaml.Unmarshal(out, &again); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if len(again.Spec.Exports) != 2 || again.Spec.Exports[0].Name != "lint_naming" || again.Spec.Exports[1].Name != "step" {
		t.Fatalf("exports order drifted across round-trip: %+v", again.Spec.Exports)
	}
}

// TestPlugin_EnvelopeRejectsBadExportEnum proves the operates/scope enums
// are live at the envelope: an out-of-vocabulary value, or an unknown sibling
// inside an export entry, is rejected.
func TestPlugin_EnvelopeRejectsBadExportEnum(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"bad operates": `apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata: {name: p, version: 1.0.0, pluginType: linter}
spec:
  abiVersion: v1alpha1
  exports:
    - name: lint_naming
      operates: rewrite
`,
		"bad scope": `apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata: {name: p, version: 1.0.0, pluginType: linter}
spec:
  abiVersion: v1alpha1
  exports:
    - name: lint_naming
      scope: cluster
`,
		"export missing name": `apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata: {name: p, version: 1.0.0, pluginType: linter}
spec:
  abiVersion: v1alpha1
  exports:
    - operates: patch
`,
		"unknown export sibling": `apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata: {name: p, version: 1.0.0, pluginType: linter}
spec:
  abiVersion: v1alpha1
  exports:
    - name: lint_naming
      stability: stable
`,
	}
	for name, src := range cases {
		src := src
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := envelope.ValidateBytes(envelope.KindPlugin, []byte(src), "bad.yaml")
			if err == nil {
				t.Fatalf("expected envelope rejection for %q; got nil", name)
			}
			if !errors.Is(err, envelope.ErrEntityEnvelope()) {
				t.Fatalf("expected ErrEntityEnvelope for %q; got %v", name, err)
			}
		})
	}
}

// pluginWithCommandFlag renders a CLI Plugin whose one mirrored command carries
// the given flag entry, written as a YAML flow mapping.
func pluginWithCommandFlag(flag string) string {
	return `apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata: {name: greet, version: 1.0.0, pluginType: cli}
spec:
  abiVersion: v1alpha1
  commands:
    - name: greet
      flags:
        - ` + flag + "\n"
}

// TestPlugin_EnvelopeJudgesCommandFlagShape proves the command mirror's flag
// bounds are live at the envelope, one fault per row beside positive controls:
// a shorthand is a single ASCII letter or digit, and a type is one of string,
// bool or int. A refused row must be refused for the field it breaks.
func TestPlugin_EnvelopeJudgesCommandFlagShape(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		flag  string
		field string // empty: the document is valid
	}{
		{"well-formed flag", `{name: times, shorthand: t, type: int, default: "3"}`, ""},
		{"digit shorthand, no type", `{name: seven, shorthand: "7"}`, ""},
		{"two-character shorthand", `{name: verbose, shorthand: vv}`, "shorthand"},
		{"non-ASCII shorthand", `{name: accent, shorthand: é}`, "shorthand"},
		{"hyphen shorthand", `{name: dash, shorthand: "-"}`, "shorthand"},
		{"type outside the set", `{name: loud, type: boolean}`, "type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := envelope.ValidateBytes(envelope.KindPlugin, []byte(pluginWithCommandFlag(tc.flag)), "plugin.yaml")
			if tc.field == "" {
				if err != nil {
					t.Fatalf("valid flag declaration rejected: %v", err)
				}
				return
			}
			var envErr *envelope.Error
			if !errors.As(err, &envErr) {
				t.Fatalf("expected a schema violation for %s; got %v", tc.name, err)
			}
			for _, f := range envErr.Findings {
				if strings.Contains(f.FieldPath, tc.field) {
					return
				}
			}
			t.Fatalf("no finding names the %q field: %v", tc.field, err)
		})
	}
}
