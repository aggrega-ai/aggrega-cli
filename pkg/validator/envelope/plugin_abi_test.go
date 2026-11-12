// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package envelope

import (
	"errors"
	"strings"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/pluginabi"
	aggerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// pluginDeclaring is a Plugin entity carrying the given spec.abiVersion on its
// sixth line, and nothing else beyond what a Plugin entity needs to be read.
func pluginDeclaring(abiVersion string) []byte {
	return []byte(`apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata:
  name: probe
spec:
  abiVersion: ` + abiVersion + "\n")
}

// TestRetiredPluginABIVersion pins the refusal of a Plugin entity declaring a
// plugin ABI version this compiler no longer speaks. Each row changes the
// abiVersion line alone.
//
// The retired version is refused under its own condition and short code, the one
// a guest declaring that version is refused under at load, with one finding at
// spec.abiVersion naming both versions and advice naming what to do; it matches
// the gate's one sentinel as well. A version the compiler never spoke keeps the
// schema's refusal, so the condition is not the schema's enum mismatch renamed.
// The spoken version is read off the schema the gate reads, and is asserted to be
// the one the generated codec names, so the message cannot name a version the
// schema would not accept.
func TestRetiredPluginABIVersion(t *testing.T) {
	t.Parallel()

	speaks, _ := schemaValue(KindPlugin, []string{"spec", "abiVersion"})
	if speaks == "" {
		t.Fatal("the Plugin schema pins no single abiVersion, so the refusal can name no version to rebuild for")
	}
	if speaks != abi.AbiVersion {
		t.Fatalf("the Plugin schema pins abiVersion %q, but the describe schema's generated codec names %q", speaks, abi.AbiVersion)
	}
	if speaks != pluginabi.Version {
		t.Fatalf("the Plugin schema pins abiVersion %q, but the host speaks %q", speaks, pluginabi.Version)
	}

	cases := []struct {
		name          string
		abiVersion    string
		wantCondition error
		wantFragments []string
	}{
		{
			name:          "a retired ABI version is refused under its own condition",
			abiVersion:    "v1alpha1",
			wantCondition: ErrRetiredABIVersion(),
			wantFragments: []string{
				"(Plugin, at subject.yaml)",
				"; 6:3: spec.abiVersion: the Plugin declares abiVersion v1alpha1, and this compiler speaks " + speaks,
				". " + pluginabi.Advice + "; its Plugin entity then declares `abiVersion: " + speaks + "`",
			},
		},
		{
			name:          "an ABI version the compiler never spoke keeps the schema refusal",
			abiVersion:    "v9",
			wantCondition: ErrEntityEnvelope(),
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
				_, err = ValidateBytes(KindPlugin, pluginDeclaring(tc.abiVersion), "subject.yaml")
			}()
			var violation *Error
			if !errors.As(err, &violation) || violation.Condition() != tc.wantCondition {
				t.Fatalf("the refusal must report %v; got %v", tc.wantCondition, err)
			}
			if !errors.Is(err, ErrEntityEnvelope()) {
				t.Errorf("every refusal of the gate must match ErrEntityEnvelope; got %v", err)
			}
			for _, want := range tc.wantFragments {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not say %q; got %v", want, err)
				}
			}
			if tc.wantCondition == ErrRetiredABIVersion() {
				if code, ok := aggerrors.ShortCodeOf(err); !ok || code != "E0374" {
					t.Errorf("the short code must be E0374; got %q (found %v)", code, ok)
				}
				if len(violation.Findings) != 1 {
					t.Errorf("the refusal must carry the one abiVersion finding and nothing the schema said; got %v",
						violation.Findings)
				}
			}
		})
	}
}
