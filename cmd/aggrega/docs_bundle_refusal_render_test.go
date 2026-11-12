// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// How a terminal shows the refusal of an example whose documentation bundle is
// not published for the running version while the newest earlier one is in a
// format this compiler no longer reads: one diagnostic, one sentence — never the
// two underlying refusals rendered one after the other.

import (
	"context"
	"strings"
	"testing"

	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/internal/docsbundle"
	"github.com/aggrega-ai/aggrega-cli/internal/docsbundle/docsbundletest"
	"github.com/aggrega-ai/aggrega-cli/internal/exampleops"
	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestAnExampleFromARetiredEarlierBundleIsRefusedAsOneDiagnostic drives the
// examples' acquisition against an in-process registry publishing only an
// earlier version's bundle in a retired format, and renders the refusal as the
// command line does.
func TestAnExampleFromARetiredEarlierBundleIsRefusedAsOneDiagnostic(t *testing.T) {
	t.Parallel()
	const host = "render.example.invalid"
	reg, signer := docsbundletest.NewRegistry(), docsbundletest.NewSigner(t)
	reg.PublishUnder(t, "1.0.0", docsbundletest.RetiredMediaType(t),
		docsbundletest.Bundle(t, docsbundletest.ExampleMembers()), signer)
	cfg := workspaceconfig.Config{
		DefaultRegistry: host,
		Registries: []workspaceconfig.RegistryConfig{{
			Host: host, Signing: workspaceconfig.SigningConfig{Required: true}, PublicKey: string(signer.PublicKeyPEM()),
		}},
	}
	ctx := docsbundle.WithCacheRoot(context.Background(), t.TempDir())
	ctx = docsbundle.WithTarget(ctx, func(cfg workspaceconfig.Config, ref, version string, warn func(string)) (oras.ReadOnlyTarget, docscache.VerifyAtPull, error) {
		return reg, docsbundle.Verifier(cfg, ref, version, reg, warn), nil
	})

	_, err := exampleops.Open(ctx, cfg, "1.1.0-rc.1", nil)
	if err == nil {
		t.Fatal("an example was read from a bundle in a retired format")
	}
	rendered := strings.TrimSpace(formatCompilerError(err))

	notFound, _ := cerrors.ShortCodeOf(docscache.ErrFetchNotFound())
	sentence := docsbundle.Ref(host) + " publishes no documentation bundle for 1.1.0-rc.1, and the newest earlier " +
		"one, 1.0.0, is in a format this version of aggrega no longer reads, so the examples cannot be read"
	if want := "[" + notFound + "]\n  " + sentence; rendered != want {
		t.Errorf("the terminal shows\n%s\nwant exactly\n%s", rendered, want)
	}
	for _, unwanted := range []string{"\n---\n", "publish the artifact again"} {
		if strings.Contains(rendered, unwanted) {
			t.Errorf("the terminal shows %q:\n%s", unwanted, rendered)
		}
	}
}
