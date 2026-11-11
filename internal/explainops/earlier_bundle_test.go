// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package explainops

// Which version's documentation an explanation reads, driven through the
// production acquisition against an in-process registry: the bundle for the
// version asked for where it is published, otherwise the newest earlier one —
// the choice every documentation surface shares — and, where that earlier one is
// in a format this compiler no longer reads, one sentence saying both.

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/internal/docsbundle"
	"github.com/aggrega-ai/aggrega-cli/internal/docsbundle/docsbundletest"
	"github.com/aggrega-ai/aggrega-cli/internal/haltref"
	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// earlierHost is the default registry these cases' workspaces name.
const earlierHost = "explain.example.invalid"

// earlierFixture is a registry the cases publish to, a workspace trusting its
// signer, and a context stating the case's own cache root and that registry as
// the acquisition's target.
type earlierFixture struct {
	reg    *docsbundletest.Registry
	signer *docsbundletest.Signer
	cfg    workspaceconfig.Config
	ctx    context.Context
}

func newEarlierFixture(t *testing.T) *earlierFixture {
	t.Helper()
	f := &earlierFixture{reg: docsbundletest.NewRegistry(), signer: docsbundletest.NewSigner(t)}
	f.cfg = workspaceconfig.Config{
		DefaultRegistry: earlierHost,
		Registries: []workspaceconfig.RegistryConfig{{
			Host:      earlierHost,
			Signing:   workspaceconfig.SigningConfig{Required: true},
			PublicKey: string(f.signer.PublicKeyPEM()),
		}},
	}
	ctx := docsbundle.WithCacheRoot(context.Background(), t.TempDir())
	f.ctx = docsbundle.WithTarget(ctx, func(cfg workspaceconfig.Config, ref, version string, warn func(string)) (oras.ReadOnlyTarget, docscache.VerifyAtPull, error) {
		return f.reg, docsbundle.Verifier(cfg, ref, version, f.reg, warn), nil
	})
	return f
}

// pagesBundle is a bundle carrying the two published pages an explanation reads.
func pagesBundle(t *testing.T) []byte {
	t.Helper()
	return docsbundletest.Bundle(t, []docsbundletest.Member{
		{Name: haltref.ReferencePagePath, Body: string(fixtureReferencePage(t))},
		{Name: haltref.PagePath, Body: string(fixtureHaltsPage(t, true))},
	})
}

// TestAnExplanationReadsTheEarlierVersionsPagesWhereItsOwnIsNotPublished pins
// that an explanation chooses its bundle as an example does: with no bundle
// published for the version asked for, the newest earlier one is read, and the
// pages say which they are.
func TestAnExplanationReadsTheEarlierVersionsPagesWhereItsOwnIsNotPublished(t *testing.T) {
	t.Parallel()
	f := newEarlierFixture(t)
	f.reg.Publish(t, "1.0.0", pagesBundle(t), f.signer)

	pages, err := LoadPages(f.ctx, f.cfg, "1.1.0", nil)
	if err != nil {
		t.Fatalf("an earlier version's published bundle was not read: %v", err)
	}
	if pages.Version != "1.0.0" || pages.Requested != "1.1.0" {
		t.Errorf("the pages name (version %q, asked for %q), want (1.0.0, 1.1.0)", pages.Version, pages.Requested)
	}
	if !bytes.Equal(pages.Reference, fixtureReferencePage(t)) || !bytes.Equal(pages.Halts, fixtureHaltsPage(t, true)) {
		t.Error("the pages read are not the earlier bundle's")
	}

	explanation, err := Compose(probeShortCode(t), func() (Pages, error) { return pages, nil })
	if err != nil || len(explanation.Entries) != 1 {
		t.Errorf("the earlier version's walk-through was not given (%d entries, %v)", len(explanation.Entries), err)
	}
}

// TestAMissingBundleWhoseEarlierOneIsInARetiredFormatIsOneSentence pins the
// refusal a release candidate met: no bundle published for its version, and the
// newest earlier one published by an earlier release in a format this compiler
// no longer reads. The explanation still answers the condition, and the reason
// its walk-through is missing is one sentence naming both versions — never the
// earlier bundle's own refusal alone, whose remedy the reader cannot act on.
func TestAMissingBundleWhoseEarlierOneIsInARetiredFormatIsOneSentence(t *testing.T) {
	t.Parallel()
	f := newEarlierFixture(t)
	f.reg.PublishUnder(t, "1.0.0", docsbundletest.RetiredMediaType(t), pagesBundle(t), f.signer)

	_, err := LoadPages(f.ctx, f.cfg, "1.1.0-rc.1", nil)
	var none *docsbundle.NoBundleForVersionError
	if !errors.As(err, &none) || !errors.Is(err, oci.ErrRetiredMediaType()) || !errors.Is(err, docscache.ErrFetchNotFound()) {
		t.Fatalf("answered %v, want both facts, each matchable", err)
	}
	want := docsbundle.Ref(earlierHost) + " publishes no documentation bundle for 1.1.0-rc.1, and the newest " +
		"earlier one, 1.0.0, is in a format this version of aggrega no longer reads, so the walk-through is not " +
		"shown here; every condition, including the ones a provider declares, is published at " +
		haltref.PublishedPath(haltref.ReferencePagePath) + ", and the walk-through of each authoring fix at " +
		haltref.PublishedPath(haltref.PagePath)
	if err.Error() != want {
		t.Errorf("the reason reads\n%s\nwant exactly\n%s", err, want)
	}
	if strings.Contains(err.Error(), "publish the artifact again") {
		t.Errorf("the reason tells the reader to publish an artifact they did not publish:\n%s", err)
	}

	explanation, cerr := Compose(probeShortCode(t), func() (Pages, error) { return Pages{}, err })
	if cerr != nil || explanation.Condition.ShortCode != probeShortCode(t) {
		t.Fatalf("the condition was not answered beside the missing walk-through (%+v, %v)", explanation.Condition, cerr)
	}
	var out, notes bytes.Buffer
	if werr := Write(&out, &notes, explanation); werr != nil {
		t.Fatalf("Write: %v", werr)
	}
	if strings.TrimSpace(notes.String()) != want {
		t.Errorf("the note reads\n%s\nwant\n%s", notes.String(), want)
	}
}

// TestACodeAnEarlierVersionsReferenceDoesNotCarryIsUndetermined pins what an
// earlier version's reference can settle: a code absent from it may name a
// condition declared since, so the refusal is the undetermined one, saying which
// version's reference was read — while the same absence from the reference of
// the version asked for is the definitive negative.
func TestACodeAnEarlierVersionsReferenceDoesNotCarryIsUndetermined(t *testing.T) {
	t.Parallel()
	const code = "no-condition-is-registered-under-this-name"
	cases := []struct {
		name         string
		requested    string
		undetermined bool
	}{
		{name: "the earlier version's reference", requested: "1.1.0", undetermined: true},
		{name: "the reference of the version asked for", requested: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pages := fixturePages(t)
			pages.Version, pages.Requested = "1.0.0", tc.requested
			_, err := Compose(code, func() (Pages, error) { return pages, nil })
			var unresolved *haltref.UnresolvedCodeError
			var unknown *haltref.UnknownCodeError
			switch {
			case err == nil:
				t.Fatal("a code nothing declares was answered")
			case tc.undetermined:
				if !errors.As(err, &unresolved) || errors.As(err, &unknown) {
					t.Errorf("answered %v, want the undetermined refusal", err)
				}
				if !strings.Contains(err.Error(), "the one for 1.0.0") || !strings.Contains(err.Error(), "none is published for 1.1.0") {
					t.Errorf("the refusal does not say which version's reference was read:\n%v", err)
				}
			default:
				if !errors.As(err, &unknown) {
					t.Errorf("answered %v, want the definitive negative", err)
				}
			}
		})
	}
}
