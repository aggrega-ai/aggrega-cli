// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package exampleops_test

// The examples and the explanations choose their documentation bundle the same
// way, driven through the production acquisition against an in-process registry:
// both read the newest earlier version's bundle where none is published for the
// version asked for, and where that earlier one is in a format this compiler no
// longer reads, the examples are refused in one sentence saying both.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/internal/docsbundle"
	"github.com/aggrega-ai/aggrega-cli/internal/docsbundle/docsbundletest"
	"github.com/aggrega-ai/aggrega-cli/internal/exampleops"
	"github.com/aggrega-ai/aggrega-cli/internal/explainops"
	"github.com/aggrega-ai/aggrega-cli/internal/haltref"
	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// earlierHost is the default registry these cases' workspaces name.
const earlierHost = "examples-earlier.example.invalid"

// earlierRegistry returns a registry, a workspace configuration trusting the
// signer it publishes with, and a context stating a cache root of the case's own
// and that registry as the acquisition's target.
func earlierRegistry(t *testing.T) (*docsbundletest.Registry, *docsbundletest.Signer, workspaceconfig.Config, context.Context) {
	t.Helper()
	reg, signer := docsbundletest.NewRegistry(), docsbundletest.NewSigner(t)
	cfg := workspaceconfig.Config{
		DefaultRegistry: earlierHost,
		Registries: []workspaceconfig.RegistryConfig{{
			Host:      earlierHost,
			Signing:   workspaceconfig.SigningConfig{Required: true},
			PublicKey: string(signer.PublicKeyPEM()),
		}},
	}
	ctx := docsbundle.WithCacheRoot(context.Background(), t.TempDir())
	ctx = docsbundle.WithTarget(ctx, func(cfg workspaceconfig.Config, ref, version string, warn func(string)) (oras.ReadOnlyTarget, docscache.VerifyAtPull, error) {
		return reg, docsbundle.Verifier(cfg, ref, version, reg, warn), nil
	})
	return reg, signer, cfg, ctx
}

// TestExamplesAndExplanationsReadTheSameEarlierBundle pins that the two surfaces
// make one choice: asked for a version with no bundle published, each reads the
// newest earlier one — the same one — and never a newer one or a pre-release.
func TestExamplesAndExplanationsReadTheSameEarlierBundle(t *testing.T) {
	t.Parallel()
	reg, signer, cfg, ctx := earlierRegistry(t)
	for _, v := range []string{"0.9.0", "1.0.0", "1.1.0-rc.1", "1.2.0"} {
		reg.Publish(t, v, docsbundletest.Bundle(t, docsbundletest.ExampleMembers(
			docsbundletest.Member{Name: haltref.ReferencePagePath, Body: "# Error sentinels " + v + "\n"},
			docsbundletest.Member{Name: haltref.PagePath, Body: "# Fatal Halts " + v + "\n"},
		)), signer)
	}

	examples, err := exampleops.Open(ctx, cfg, "1.1.0", nil)
	if err != nil {
		t.Fatalf("the examples were not read: %v", err)
	}
	pages, err := explainops.LoadPages(ctx, cfg, "1.1.0", nil)
	if err != nil {
		t.Fatalf("the explanation's pages were not read: %v", err)
	}
	if examples.Version != "1.0.0" || pages.Version != "1.0.0" {
		t.Errorf("the examples read %q and the explanation %q, want both the newest earlier one, 1.0.0",
			examples.Version, pages.Version)
	}
}

// TestExamplesFromAMissingBundleWhoseEarlierOneIsInARetiredFormatAreOneSentence
// pins the refusal a release candidate met: no bundle published for its version,
// and the newest earlier one in a format this compiler no longer reads. The
// refusal is one sentence naming both versions and what it means for the
// examples, its short code is the not-found of the version asked for, and it
// carries no remedy the reader cannot act on.
func TestExamplesFromAMissingBundleWhoseEarlierOneIsInARetiredFormatAreOneSentence(t *testing.T) {
	t.Parallel()
	reg, signer, cfg, ctx := earlierRegistry(t)
	reg.PublishUnder(t, "1.0.0", docsbundletest.RetiredMediaType(t),
		docsbundletest.Bundle(t, docsbundletest.ExampleMembers()), signer)

	_, err := exampleops.Open(ctx, cfg, "1.1.0-rc.1", nil)
	var none *docsbundle.NoBundleForVersionError
	if !errors.As(err, &none) || !errors.Is(err, oci.ErrRetiredMediaType()) || !errors.Is(err, docscache.ErrFetchNotFound()) {
		t.Fatalf("answered %v, want both facts, each matchable", err)
	}
	want := docsbundle.Ref(earlierHost) + " publishes no documentation bundle for 1.1.0-rc.1, and the newest " +
		"earlier one, 1.0.0, is in a format this version of aggrega no longer reads, so the examples cannot be read"
	if err.Error() != want {
		t.Errorf("the refusal reads\n%s\nwant exactly\n%s", err, want)
	}
	if strings.Contains(err.Error(), "publish the artifact again") {
		t.Errorf("the refusal tells the reader to publish an artifact they did not publish:\n%s", err)
	}
	notFound, _ := cerrors.ShortCodeOf(docscache.ErrFetchNotFound())
	if code, ok := cerrors.ShortCodeOf(err); !ok || code != notFound {
		t.Errorf("the refusal's short code is %q (%v), want the not-found of the version asked for, %q", code, ok, notFound)
	}
}
