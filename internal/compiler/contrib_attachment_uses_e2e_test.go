// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package compiler_test — socket-allowlist coverage for a Feature attachment
// declared at a scope rather than on a single component instance.
//
// The rule was asked only of a component instance's own `features` block, so an
// attachment declared at a subsystem, at a Release boundary, or on a Release
// boundary component could name any socket at all. Measured before the closure,
// all three fixtures BUILT, and two of them MATERIALISED the invented socket as
// real edges from every component in the scope:
//
//	consumer/bogus-subsystem-socket/link -> store originFeature=cov/secret-value
//	store/bogus-subsystem-socket/link    -> store originFeature=cov/secret-value
//
// The Feature they name declares no usesSchema at all, so no socket on it is
// valid — which is what makes the invented name unambiguous evidence rather than a
// judgement call.

package compiler_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

func TestContribAttachmentUses_UndeclaredSocketHaltsAtEveryScope(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		release     string
		wantLocator string
	}{
		{
			name:        "subsystem-wide attachment",
			release:     "release-contrib-subsystem.yaml",
			wantLocator: `.spec.subsystems.svc.features["subsysfeat"].uses["bogus-subsystem-socket"]`,
		},
		{
			name:        "release boundary-wide attachment",
			release:     "release-contrib-boundary.yaml",
			wantLocator: `.spec.boundaries["cluster"].features["bndfeat"].uses["bogus-boundary-socket"]`,
		},
		{
			name:        "release boundary-component attachment",
			release:     "release-contrib-component.yaml",
			wantLocator: `.spec.boundaries["cluster"].components["consumer"].features["compfeat"].uses["bogus-component-socket"]`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := pipelinetest.BuildE(t, pipelinetest.Input{
				ReleasePath: "testdata/subagg-coverage/" + tc.release,
				CatalogPaths: []string{
					"testdata/uses-edge-config/catalog/base",
					"testdata/subagg-coverage/catalog/base",
				},
			})
			if !errors.Is(err, resolver.ErrUnknownUsesSocket) {
				t.Fatalf("want ErrUnknownUsesSocket; got %v", err)
			}
			// The locator has to name the scope the attachment was authored at —
			// an attachment contributed from a subsystem and one contributed from a
			// boundary are different edits to make.
			var multi *resolver.MultiResolverError
			if !errors.As(err, &multi) {
				t.Fatalf("want a MultiResolverError carrying the per-violation slice; got %T", err)
			}
			var found bool
			for _, v := range multi.Violations {
				if strings.Contains(v.Field, tc.wantLocator) {
					found = true
				}
			}
			if !found {
				t.Errorf("no violation located at %s; got %v", tc.wantLocator, multi.Violations)
			}
		})
	}
}
