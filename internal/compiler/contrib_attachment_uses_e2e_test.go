// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package compiler_test — socket-allowlist coverage for a Feature attachment
// whose socket names the pre-flatten walk over the Aggregate and Release documents
// does not reach: one declared at a scope rather than on a single component
// instance, and one declared inside a sub-aggregate module.
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
// A component-level attachment inside a sub-aggregate module is the same class one
// document deeper, and it is the sharper case: the identical `components:` block
// halts when the root Aggregate declares it and BUILT when a module did, so the
// coverage of the rule depended on how deep the author nested the component. That
// build materialised the invented socket into the consuming node's render context
// too, which is what makes the green a wrong artifact rather than a missing
// warning.
//
// The Feature they name declares no usesSchema at all, so no socket on it is
// valid — which is what makes the invented name unambiguous evidence rather than a
// judgement call. Each row's positive control is the identical block at the root of
// an Aggregate, which halts on the same sentinel; a row asserting only "some error"
// could not tell the rule from any other gate rejecting the document first.

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
		{
			// The positive control for the row below: the identical `components:`
			// block at the root of an Aggregate. It halts, and it always did — so a
			// module row that halts is the rule firing rather than the fixture
			// tripping some unrelated gate.
			name:        "component attachment at an Aggregate root",
			release:     "release-contrib-component-root.yaml",
			wantLocator: `.spec.components["consumer"].features["modfeat"].uses["bogus-module-socket"]`,
		},
		{
			// The same block one document deeper. The locator names the flattened
			// node rather than an authoring path inside the module, matching every
			// other post-flatten diagnostic: the module's own document is out of
			// view by the time the node exists.
			name:        "component attachment inside a sub-aggregate module",
			release:     "release-contrib-module.yaml",
			wantLocator: `.data.consumer.features["modfeat"].uses["bogus-module-socket"]`,
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
			if !errors.Is(err, resolver.ErrUnknownUsesSocket()) {
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
