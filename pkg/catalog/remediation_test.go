// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// TestTier3EntityRefusal_NamesTheLocalCatalogRemedy pins the WHOLE diagnostic the
// entity registry pull prints when a recipe reference carries no provider scope,
// remediation line included. The refusal explains that an empty provider cannot
// bind to an OCI location, which states the obstacle without naming any way out;
// the remedy is to supply the local catalog directory holding the recipe, and the
// reference only reached the registry because no local directory held it.
//
// The assertion is on the exact string rather than on a substring because the
// remediation line's presence is the whole point: a substring check on the
// obstacle alone passed just as happily when no remedy was offered at all.
//
// The production puller drives it. PullEntity binds the reference before it opens
// any repository, so the refusal comes from the real binding step with a nil HTTP
// client and no network access — the error is raised, not forged.
func TestTier3EntityRefusal_NamesTheLocalCatalogRemedy(t *testing.T) {
	p := DefaultOCIPuller{registry: "127.0.0.1:5000"}

	_, _, err := tier3PullCatalogEntity(context.Background(), t.TempDir(), ".aggrega/cache", p, "adminer-recipe", "Recipe")
	if err == nil {
		t.Fatal("tier3PullCatalogEntity: want a refusal for an unscoped recipe reference; got nil error")
	}
	if !errors.Is(err, ErrUnscopedRecipeReference()) {
		t.Fatalf("error = %v; want errors.Is(err, ErrUnscopedRecipeReference)", err)
	}

	const want = `catalog: tier-3 pull adminer-recipe: recipe reference must be provider-scoped (<provider>/<name>); an empty provider cannot bind to an OCI location: "adminer-recipe"
  remediation: no local catalog directory held this reference, so it was looked for in the registry, where a provider segment is required; re-run as aggrega pull --release <release> --catalog <dir> so the directory that holds it is searched first (--catalog is repeatable)`

	if got := err.Error(); got != want {
		t.Errorf("diagnostic mismatch\n got: %q\nwant: %q", got, want)
	}
}

// TestTier3EntityRefusal_NamesThePluginCoordinateRemedy pins the WHOLE diagnostic
// the entity registry pull prints for each of the two plugin-reference refusals,
// remediation line included. The two remedies are opposites and the assertion is
// what keeps them so: an unscoped reference is missing its pluginType segment and
// the remedy is to add it; a registry-qualified reference carries a location the
// binder supplies itself and the remedy is to remove it. A shared "check your
// plugin reference" line would satisfy neither reader.
//
// The assertion is on the exact string rather than on a substring, for the same
// reason its recipe-side sibling is: a substring check on the obstacle alone passes
// just as happily when no remedy is offered at all.
//
// The production puller drives it. PullEntity binds the reference before it opens
// any repository, so both refusals come from the real binding step with a nil HTTP
// client and no network access — the errors are raised, not forged.
func TestTier3EntityRefusal_NamesThePluginCoordinateRemedy(t *testing.T) {
	p := DefaultOCIPuller{registry: "127.0.0.1:5000"}

	t.Run("unscoped reference is told to add the pluginType segment", func(t *testing.T) {
		_, _, err := tier3PullCatalogEntity(context.Background(), t.TempDir(), ".aggrega/cache", p, "patch-credential", "Plugin")
		if err == nil {
			t.Fatal("tier3PullCatalogEntity: want a refusal for an unscoped plugin reference; got nil error")
		}
		if !errors.Is(err, ErrUnscopedPluginReference()) {
			t.Fatalf("error = %v; want errors.Is(err, ErrUnscopedPluginReference)", err)
		}

		const want = `catalog: tier-3 pull patch-credential: wasm plugin reference must carry a pluginType scope (<pluginType>/<name>); an empty scope cannot bind to an OCI location: "patch-credential"
  remediation: add the missing pluginType segment — write the reference as <pluginType>/<name>, for example provider/helm or governance/redact, using the pluginType the plugin entity declares under metadata.pluginType`

		if got := err.Error(); got != want {
			t.Errorf("diagnostic mismatch\n got: %q\nwant: %q", got, want)
		}
	})

	t.Run("qualified reference is told to remove the registry host", func(t *testing.T) {
		_, _, err := tier3PullCatalogEntity(context.Background(), t.TempDir(), ".aggrega/cache", p, "host.tld/plugin/pipeline/patch-credential", "Plugin")
		if err == nil {
			t.Fatal("tier3PullCatalogEntity: want a refusal for a registry-qualified plugin reference; got nil error")
		}
		if !errors.Is(err, ErrQualifiedPluginReference()) {
			t.Fatalf("error = %v; want errors.Is(err, ErrQualifiedPluginReference)", err)
		}

		const want = `catalog: tier-3 pull host.tld/plugin/pipeline/patch-credential: wasm plugin reference must be the plugin coordinate identity (<pluginType>/<name>:<version>), not a registry-qualified or artifact path: "host.tld/plugin/pipeline/patch-credential"
  remediation: remove the registry host and the artifact path, leaving the coordinate alone — <pluginType>/<name>:<version>, for example provider/helm:1.0.0; the pull prefixes the configured registry itself, so a reference that already carries one is prefixed twice`

		if got := err.Error(); got != want {
			t.Errorf("diagnostic mismatch\n got: %q\nwant: %q", got, want)
		}
	})

	t.Run("the two remedies are not interchangeable", func(t *testing.T) {
		unscoped := remediationFor(errUnscopedPluginWrapped(), "127.0.0.1:5000")
		qualified := remediationFor(errQualifiedPluginWrapped(), "127.0.0.1:5000")
		if unscoped == qualified {
			t.Fatalf("both plugin refusals yielded the same remediation %q; one must say add the missing segment and the other remove the extra one", unscoped)
		}
		if !strings.Contains(unscoped, "add the missing pluginType segment") {
			t.Errorf("unscoped remediation = %q; want it to say the segment is added", unscoped)
		}
		if !strings.Contains(qualified, "remove the registry host") {
			t.Errorf("qualified remediation = %q; want it to say the host is removed", qualified)
		}
	})
}

// errUnscopedPluginWrapped and errQualifiedPluginWrapped return each plugin
// sentinel wrapped the way the binding step wraps it, so the chain-traversal
// assertions exercise the real wrapping rather than a bare sentinel.
func errUnscopedPluginWrapped() error {
	_, err := entityReference("127.0.0.1:5000", "Plugin", "patch-credential")
	return err
}

func errQualifiedPluginWrapped() error {
	_, err := entityReference("127.0.0.1:5000", "Plugin", "host.tld/plugin/pipeline/patch-credential")
	return err
}

// erroringEntityPuller is an EntityPuller whose PullEntity fails with a chosen
// error, so a test can drive the registry-pull diagnostic for a sentinel that a
// real binding step would never raise on its own.
type erroringEntityPuller struct{ err error }

func (p erroringEntityPuller) Pull(_ context.Context, _, _ string) (*oci.PullResult, error) {
	return nil, p.err
}

func (p erroringEntityPuller) PullEntity(_ context.Context, _, _, _ string) (*oci.PullResult, string, error) {
	return nil, "", p.err
}

func (p erroringEntityPuller) RegistryPrefix() string { return "127.0.0.1:5000" }

// TestRemediationFor_DelegationPrecedence asserts the catalog-side mapping adds to
// the fetch-sentinel mapping rather than displacing it: a fetch sentinel still
// yields its own remediation verbatim, this package's own sentinel yields the
// local-catalog remedy, and an unrecognised error yields no remediation line at
// all so the caller can omit it.
//
// The first case is the one that matters most. The catalog-side mapping sits in
// front of the fetch-sentinel one on this path, so a regression that stopped
// delegating would silently drop every fetch remediation — an authorization
// failure would lose its login pointer — while the new sentinel's own test kept
// passing.
func TestRemediationFor_DelegationPrecedence(t *testing.T) {
	const host = "127.0.0.1:5000"

	t.Run("fetch sentinel keeps its own remediation", func(t *testing.T) {
		got := remediationFor(oci.ErrFetchUnauthorized(), host)
		if want := oci.RemediationFor(oci.ErrFetchUnauthorized(), host); got != want {
			t.Errorf("remediationFor(ErrFetchUnauthorized) = %q; want the fetch mapping's own %q", got, want)
		}
		if !strings.Contains(got, "docker login "+host) {
			t.Errorf("remediationFor(ErrFetchUnauthorized) = %q; want it to keep the login pointer", got)
		}
	})

	t.Run("wrapped catalog sentinel is recognised through the chain", func(t *testing.T) {
		wrapped := errors.New("outer: " + ErrUnscopedRecipeReference().Error())
		if remediationFor(wrapped, host) != "" {
			t.Error("a look-alike error that does not wrap the sentinel must not match")
		}
		if got := remediationFor(errUnscopedRecipeWrapped(), host); !strings.Contains(got, "--catalog") {
			t.Errorf("remediationFor(wrapped ErrUnscopedRecipeReference) = %q; want the --catalog remedy", got)
		}
	})

	t.Run("unrecognised error yields no remediation", func(t *testing.T) {
		if got := remediationFor(errors.New("some transport blip"), host); got != "" {
			t.Errorf("remediationFor(non-sentinel) = %q; want the empty string", got)
		}
		if got := remediationFor(nil, host); got != "" {
			t.Errorf("remediationFor(nil) = %q; want the empty string", got)
		}
	})
}

// errUnscopedRecipeWrapped returns the sentinel wrapped the way the binding step
// wraps it, so the chain-traversal assertion exercises the real wrapping rather
// than a bare sentinel.
func errUnscopedRecipeWrapped() error {
	_, err := entityReference("127.0.0.1:5000", "Recipe", "adminer-recipe")
	return err
}

// TestTier3EntityRefusal_FetchSentinelKeepsItsRemediation asserts the whole
// diagnostic for a fetch sentinel on the same path still carries that sentinel's
// own remediation line, so the catalog-side mapping is proven additive at the call
// site and not only in isolation.
func TestTier3EntityRefusal_FetchSentinelKeepsItsRemediation(t *testing.T) {
	p := erroringEntityPuller{err: oci.ErrFetchUnauthorized()}

	_, _, err := tier3PullCatalogEntity(context.Background(), t.TempDir(), ".aggrega/cache", p, "compose/mariadb:1.0.0", "Recipe")
	if err == nil {
		t.Fatal("tier3PullCatalogEntity: want the authorization failure; got nil error")
	}
	got := err.Error()
	if !strings.Contains(got, "docker login 127.0.0.1:5000") {
		t.Errorf("diagnostic = %q; want it to carry the login pointer", got)
	}
	if strings.Contains(got, "--catalog") {
		t.Errorf("diagnostic = %q; want no local-catalog remedy for an authorization failure", got)
	}
}
