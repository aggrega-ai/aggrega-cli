// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/helmcore"
)

// longestLowercaseName is the longest all-lowercase identity this target
// accepts: every character is carried through the release-name derivation as
// itself, so its release name is exactly as long as the bound.
var longestLowercaseName = strings.Repeat("abcdefghij", 5) + "abc"

// longestUnderscoreName carries one underscore, which the derivation writes out
// at a cost of three characters, so the identity is two characters shorter than
// the bound and its release name reaches the bound exactly.
var longestUnderscoreName = "ab_" + strings.Repeat("cdefghijab", 4) + "cdefghij"

// TestHelmNameVerdict_RefusesOnlyTheNamesItsServicesCannotBeginWith drives every
// legal shape a component name takes through this provider's answer and through
// the compiler's settle path that acts on it, and asserts which of them halt.
//
// The rows that build are as load-bearing as the rows that halt. A first-character
// rule written against the identity rather than against the derived name would
// refuse an underscore or a capital wherever it sits; one written against the
// release name's own grammar would accept a leading digit, which Helm admits and
// the cluster then refuses in every Service named after it. Only a rule reading
// the derived name's first character against a Service name's grammar passes
// every row.
//
// Each refusal is asserted through the real settle path rather than on the
// verdict alone, because what a reader meets is the compiler's message carrying
// the verdict: the condition, the field the author wrote, the kind of name that
// refused and the target's reason. A refusal no shortening repairs states no
// length, so the remedy is asserted to carry none.
func TestHelmNameVerdict_RefusesOnlyTheNamesItsServicesCannotBeginWith(t *testing.T) {
	t.Parallel()

	cases := []struct {
		scenario string
		identity string
		// refusedAs is the kind of name the halt reports; empty for an identity
		// every kind accepts.
		refusedAs string
		reason    []string
	}{
		{scenario: "control: an ordinary lowercase name", identity: "kv"},
		{scenario: "an underscore inside the name", identity: "my_kv"},
		{scenario: "a capital after the first character", identity: "myKv"},
		{scenario: "a name inside a composed Aggregate", identity: "kvmod.inner_kv"},
		{scenario: "the longest all-lowercase name the target accepts", identity: longestLowercaseName},
		{scenario: "the longest name carrying an underscore", identity: longestUnderscoreName},
		{
			scenario:  "a digit first",
			identity:  "1kv",
			refusedAs: helmcore.ServiceNameKind,
			reason:    []string{"a Kubernetes Service name begins with a lowercase letter", `would begin with "1"`},
		},
		{
			scenario:  "a digit first on the composed Aggregate's own name",
			identity:  "1mod.cache",
			refusedAs: helmcore.ServiceNameKind,
			reason:    []string{"a Kubernetes Service name begins with a lowercase letter", `would begin with "1"`},
		},
		{
			scenario:  "a capital first, still reported as the release name it cannot be",
			identity:  "Kv",
			refusedAs: helmcore.ReleaseNameKind,
			reason:    []string{"a release name begins with a lowercase letter or a digit"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.scenario, func(t *testing.T) {
			t.Parallel()

			field := "spec.components." + tc.identity
			err := renderer.SettleAuthoredComponentIdentity(context.Background(), &helmOracle{}, tc.identity, field, nil)

			if tc.refusedAs == "" {
				if err != nil {
					t.Fatalf("%q is a name this target accepts and must build: %v", tc.identity, err)
				}
				for _, v := range helmcore.NameBoundOutput(rendercore.NameBoundInput{Proposed: tc.identity, Authored: true}).Names {
					if !v.Usable {
						t.Errorf("%q is refused as a %s: %s", tc.identity, v.Kind, v.Reason)
					}
				}
				return
			}

			if err == nil {
				t.Fatalf("%q must halt the build", tc.identity)
			}
			if !errors.Is(err, renderer.ErrComponentIdentityTargetRefuses()) {
				t.Fatalf("the halt must carry the target-refuses condition; got %v", err)
			}
			var compilerErr *cerrors.CompilerError
			if !errors.As(err, &compilerErr) {
				t.Fatalf("the halt must carry a category; got %T", err)
			}
			if compilerErr.Category() != cerrors.CatalogError {
				t.Errorf("category = %v, want %v: a name a person wrote is a fault in authored content",
					compilerErr.Category(), cerrors.CatalogError)
			}
			want := append([]string{
				`component "` + tc.identity + `"`,
				"written at " + field,
				"which the target cannot use as a " + tc.refusedAs,
				"choose a name the target accepts",
			}, tc.reason...)
			for _, w := range want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("the halt does not carry %q; got %v", w, err)
				}
			}
			if strings.Contains(err.Error(), "choose a name of at most") {
				t.Errorf("no shortening changes a first character, so the halt must state no length; got %v", err)
			}
		})
	}
}

// TestServiceNameVerdict_IsStricterThanTheReleaseNameOnlyAtTheFirstCharacter pins
// what separates the two kinds, so neither is mistaken for a copy of the other.
//
// A leading digit is a release name Helm accepts and a Service name the cluster
// refuses: the release-name verdict passes it and the Service-name verdict
// refuses it with no bound. A name that is only too long is the release name's
// to refuse, with a bound, and is no concern of the Service-name verdict at all.
func TestServiceNameVerdict_IsStricterThanTheReleaseNameOnlyAtTheFirstCharacter(t *testing.T) {
	t.Parallel()

	if v := helmcore.ReleaseNameVerdict("1kv"); !v.Usable {
		t.Errorf("Helm accepts a release name opening with a digit, and the release-name verdict refused one: %s", v.Reason)
	}
	v := helmcore.ServiceNameVerdict(rendercore.NameBoundInput{Proposed: "1kv"})
	if v.Usable {
		t.Fatal("a Service named after an identity opening with a digit is refused by the cluster; the verdict accepted it")
	}
	if v.Bound != nil {
		t.Errorf("the Service-name refusal came back bounded to %d; no shortening changes a first character", *v.Bound)
	}

	long := strings.Repeat("a", 60)
	if v := helmcore.ServiceNameVerdict(rendercore.NameBoundInput{Proposed: long}); !v.Usable {
		t.Errorf("a long lowercase identity is refused as a Service name, which judges only the first character: %s", v.Reason)
	}
	if v := helmcore.ReleaseNameVerdict(long); v.Usable || v.Bound == nil {
		t.Errorf("a %d-character identity must be refused as a release name with a bound; got usable=%v bound=%v",
			len(long), v.Usable, v.Bound)
	}

	if v := helmcore.ServiceNameVerdict(rendercore.NameBoundInput{}); !v.Usable {
		t.Errorf("an empty proposal names nothing and must not be refused: %s", v.Reason)
	}
}
