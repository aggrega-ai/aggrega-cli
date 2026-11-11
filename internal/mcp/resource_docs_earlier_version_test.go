// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

// The session's documentation bundle is chosen as every other surface chooses
// it: the bundle for the session's own version where one is published, otherwise
// the newest earlier one, which the session's reads then read — and where that
// earlier one is in a format this compiler no longer reads, an explanation says
// both facts in one sentence.

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/internal/docsbundle"
	"github.com/aggrega-ai/aggrega-cli/internal/docsbundle/docsbundletest"
	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// earlierVersionServer returns a server over an empty workspace and a cache root
// of its own, asking for the documentation of version 1.1.0, whose bundle target
// is reg.
func earlierVersionServer(t *testing.T, reg *docsbundletest.Registry) *server {
	t.Helper()
	return &server{
		out:           io.Discard,
		logw:          io.Discard,
		serverVersion: "1.1.0",
		launchRoot:    t.TempDir(),
		docsRoot:      t.TempDir(),
		docsBundleRef: docsTestRef,
		docsTarget: func(*toolWorkspace, string, string, func(string)) (oras.ReadOnlyTarget, docscache.VerifyAtPull, error) {
			return reg, nil, nil
		},
	}
}

// TestTheSessionReadsTheEarlierVersionsBundleWhereItsOwnIsNotPublished pins that
// the session acquires through the shared choice and then reads what it acquired:
// with no bundle published for its version, a page is served from the newest
// earlier one.
func TestTheSessionReadsTheEarlierVersionsBundleWhereItsOwnIsNotPublished(t *testing.T) {
	t.Parallel()
	reg := docsbundletest.NewRegistry()
	reg.Publish(t, "1.0.0", docsFixtureBundle(t), nil)
	s := earlierVersionServer(t, reg)

	runServer(t, s)

	if cached, err := docscache.Cached(s.docsRoot, docsTestRef, "1.0.0"); err != nil || !cached {
		t.Fatalf("the session did not acquire the earlier version's bundle (%v)", err)
	}
	rr, rerr := readResource(t, s, "langref://entities/component")
	if rerr != nil {
		t.Fatalf("a page of the bundle the session acquired was not served: %v", rerr)
	}
	if got := rr.Contents[0].Text; got != langrefBody {
		t.Errorf("served page text = %q; want %q", got, langrefBody)
	}
}

// TestTheSessionsExplanationSaysBothFactsWhereTheEarlierBundleIsRetired pins the
// refusal on this surface: the session's acquisition measured that no bundle is
// published for its version and that the earlier one is in a retired format, and
// an explanation says exactly that rather than that its own version is absent.
func TestTheSessionsExplanationSaysBothFactsWhereTheEarlierBundleIsRetired(t *testing.T) {
	t.Parallel()
	reg := docsbundletest.NewRegistry()
	reg.PublishUnder(t, "1.0.0", docsbundletest.RetiredMediaType(t), docsFixtureBundle(t), nil)
	s := earlierVersionServer(t, reg)

	runServer(t, s)

	_, err := s.explainPages(context.Background())
	var none *docsbundle.NoBundleForVersionError
	if !errors.As(err, &none) || !errors.Is(err, oci.ErrRetiredMediaType()) {
		t.Fatalf("the explanation's reason is %v, want both facts", err)
	}
	want := docsTestRef + " publishes no documentation bundle for 1.1.0, and the newest earlier one, 1.0.0, " +
		"is in a format this version of aggrega no longer reads, so the walk-through is not shown here"
	if !strings.HasPrefix(err.Error(), want) {
		t.Errorf("the reason reads\n%s\nwant it to open with\n%s", err, want)
	}
	if strings.Contains(err.Error(), "publish the artifact again") {
		t.Errorf("the reason tells the reader to publish an artifact they did not publish:\n%s", err)
	}
}
