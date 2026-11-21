// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"context"
	"errors"
	"io"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestListEntityVersionsReadsTheRegistry holds the production puller's listing of
// a Component's versions to what an OCI registry answers over HTTP: the tags of the
// repository a pull binds the Component to, less each version the withdrawal
// screen withholds, and the registry's not-found for a repository it does not
// hold, which the view a pull settles versions over reads as no versions at all.
func TestListEntityVersionsReadsTheRegistry(t *testing.T) {
	t.Parallel()
	reg := newReadAheadRegistry(t)
	for _, version := range []string{"1.0.0", "1.1.0", "2.0.0"} {
		reg.publish(t, "Component", "demo/listed", version, oci.MediaTypeComponent, componentDoc("demo/listed", version))
	}
	cfg := workspaceconfig.Config{DefaultRegistry: reg.host}
	lister := func(t *testing.T, withdrawn map[string]string) EntityVersionLister {
		t.Helper()
		screen := &withdrawalScreen{out: io.Discard, withdrawn: map[string]map[string]string{
			RegistryReference(reg.host, "Component", "", "demo/listed"): withdrawn,
		}}
		puller, ok := NewOCIPullerWithRetractionScreen(cfg, screen).(EntityVersionLister)
		if !ok {
			t.Fatal("the production puller lists no versions")
		}
		return puller
	}

	t.Run("the tags the registry holds are listed", func(t *testing.T) {
		t.Parallel()
		got, err := lister(t, nil).ListEntityVersions(context.Background(), "demo/listed", "Component")
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if want := []string{"1.0.0", "1.1.0", "2.0.0"}; !slices.Equal(inVersionOrder(got), want) {
			t.Errorf("listed %q, want %q", got, want)
		}
	})

	t.Run("a version the screen withholds is not listed", func(t *testing.T) {
		t.Parallel()
		got, err := lister(t, map[string]string{"1.1.0": "recalled"}).ListEntityVersions(context.Background(), "demo/listed", "Component")
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if want := []string{"1.0.0", "2.0.0"}; !slices.Equal(inVersionOrder(got), want) {
			t.Errorf("listed %q, want %q", got, want)
		}
	})

	t.Run("a repository the registry does not hold lists none", func(t *testing.T) {
		t.Parallel()
		_, err := lister(t, nil).ListEntityVersions(context.Background(), "demo/absent", "Component")
		if !errors.Is(err, oci.ErrFetchNotFound()) {
			t.Fatalf("err = %v, want the registry's not-found", err)
		}
		puller := NewOCIPullerWithRetractionScreen(cfg, nil)
		view := NewLazyLookup(t.TempDir(), readAheadCacheDir, nil, puller).ResolutionVersions(context.Background(), nil, "", "")
		listing, ok := view.(listedVersions)
		if !ok {
			t.Fatal("the view a pull settles versions over lists none")
		}
		listed, err := listing.ListedVersions(closure.KindComponent, "demo/absent")
		if err != nil || len(listed) != 0 {
			t.Errorf("the view lists (%q, %v), want no versions and no failure", listed, err)
		}
	})
}
