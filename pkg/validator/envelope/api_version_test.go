// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package envelope

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/apiversion"
	aggerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// componentDeclaring is a Component its schema accepts in every respect but,
// possibly, the API version it declares on its first line.
func componentDeclaring(apiVersion string) []byte {
	return []byte("apiVersion: " + apiVersion + `
kind: Component
metadata:
  name: my-service
  version: "0.1.0"
  labels:
    component.aggrega.ai/type: server
spec: {}
`)
}

// TestRetiredAPIVersion pins the refusal of a document declaring an API version
// this compiler no longer reads. Each row changes the `apiVersion` line alone.
//
// The retired version is refused under its own condition and short code, which
// match the gate's one sentinel as well, with one finding positioned at the
// `apiVersion` line naming both versions and advice naming the line to write. A
// version the compiler never read keeps the generic schema refusal, so the
// condition is not the schema's constant mismatch renamed. The version the
// compiler reads validates, which is the row keeping the others honest: a
// document the schema refused for some other reason would fail it.
func TestRetiredAPIVersion(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		apiVersion string
		// wantCondition is nil for a document the gate accepts.
		wantCondition error
		wantFragments []string
	}{
		{
			name:          "the retired version is refused under its own condition",
			apiVersion:    "aggrega.ai/v1alpha1",
			wantCondition: ErrRetiredAPIVersion(),
			wantFragments: []string{
				"(Component, at subject.yaml)",
				"; 1:1: apiVersion: the document declares aggrega.ai/v1alpha1, and this compiler reads aggrega.ai/v1beta1",
				". change the apiVersion line to `apiVersion: aggrega.ai/v1beta1`",
			},
		},
		{
			name:          "a version the compiler never read keeps the schema refusal",
			apiVersion:    "aggrega.ai/v9",
			wantCondition: ErrEntityEnvelope(),
			wantFragments: []string{adviceCorrectDocument},
		},
		{
			name:       "the version the compiler reads validates",
			apiVersion: "aggrega.ai/v1beta1",
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
				_, err = ValidateBytes(KindComponent, componentDeclaring(tc.apiVersion), "subject.yaml")
			}()
			if tc.wantCondition == nil {
				if err != nil {
					t.Fatalf("the document must validate; got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("the document must be refused")
			}
			var violation *Error
			if !errors.As(err, &violation) || violation.Condition() != tc.wantCondition {
				t.Fatalf("the refusal must report %v; got %v", tc.wantCondition, err)
			}
			if !errors.Is(err, ErrEntityEnvelope()) {
				t.Errorf("every refusal of the gate must match ErrEntityEnvelope; got %v", err)
			}
			if !strings.HasPrefix(err.Error(), tc.wantCondition.Error()) {
				t.Errorf("the refusal must open with %q; got %v", tc.wantCondition.Error(), err)
			}
			for _, want := range tc.wantFragments {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not say %q; got %v", want, err)
				}
			}
			if tc.wantCondition == ErrRetiredAPIVersion() {
				if code, ok := aggerrors.ShortCodeOf(err); !ok || code != "E0373" {
					t.Errorf("the short code must be E0373; got %q (found %v)", code, ok)
				}
				if len(violation.Findings) != 1 {
					t.Errorf("the refusal must carry the one apiVersion finding and nothing the schema said; got %v",
						violation.Findings)
				}
			}
		})
	}
}

// TestRetiredAPIVersionReachesEveryVersionedKind holds the refusal to every kind
// whose schema states the API version its documents declare, by naming any such
// kind the refusal cannot reach. The set of kinds is read from the gate rather
// than restated, and each kind's version is read from its own schema, so a kind
// added later is held to it without this test changing. Each kind's version is
// also held to the generated constant every writer in this module drafts, which
// is generated from one schema: a schema left at another version fails here.
//
// The kinds whose schema declares no `apiVersion` are listed, so a kind silently
// dropping its version from the set the refusal reads fails here too.
func TestRetiredAPIVersionReachesEveryVersionedKind(t *testing.T) {
	t.Parallel()

	unversioned := map[Kind]bool{
		KindAggregaConfig: true,
		KindLockfile:      true,
		KindEntityLock:    true,
		KindPluginsTrust:  true,
	}
	for _, kind := range Kinds() {
		version, ok := schemaValue(kind, []string{"apiVersion"})
		switch {
		case unversioned[kind] && ok:
			t.Errorf("%s: its schema now states apiVersion %q; take it off the unversioned list", kind, version)
		case !unversioned[kind] && !ok:
			t.Errorf("%s: its schema states no apiVersion the refusal can name", kind)
		case ok && version != apiversion.APIVersion:
			t.Errorf("%s: its schema states apiVersion %q, not %q, the version the compiler's writers draft",
				kind, version, apiversion.APIVersion)
		}
		if !ok {
			continue
		}
		doc := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: " + string(kind) + "\n")
		if _, err := ValidateBytes(kind, doc, "subject.yaml"); !errors.Is(err, ErrRetiredAPIVersion()) {
			t.Errorf("%s: a document declaring aggrega.ai/v1alpha1 is not refused as a retired version; got %v", kind, err)
		}
	}
}
