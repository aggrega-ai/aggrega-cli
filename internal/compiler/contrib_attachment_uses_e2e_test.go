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
		name    string
		release string
		// wantLocator is the authoring site the halt must name. It is a PREFIX
		// match, because the locator names the attachment and the offending socket
		// is carried in the message beside it.
		wantLocator string
		// wantSocket is the socket the halt must name. It is asserted separately
		// because a halt about the wrong socket at the right attachment would
		// otherwise pass — and it is looked for across the whole diagnostic,
		// because the two paths answering these rows spell it in different places:
		// the pre-flatten rules carry it in the message beside a locator naming the
		// attachment, and the post-flatten one spells it in the locator itself.
		wantSocket string
	}{
		{
			name:        "subsystem-wide attachment",
			release:     "release-contrib-subsystem.yaml",
			wantLocator: `.spec.subsystems.svc.features.subsysfeat`,
			wantSocket:  "bogus-subsystem-socket",
		},
		{
			name:        "release boundary-wide attachment",
			release:     "release-contrib-boundary.yaml",
			wantLocator: `.spec.boundaries.cluster.features.bndfeat`,
			wantSocket:  "bogus-boundary-socket",
		},
		{
			// 🔴 The locator names the component's site on the AGGREGATE document
			// while the attachment itself is written on the Release. That follows
			// from the coordinate this rule is invoked per — a component's
			// attachment names, taken from both documents and presented under the
			// component — and the entity beside it says which document to open.
			name:        "release boundary-component attachment",
			release:     "release-contrib-component.yaml",
			wantLocator: `.spec.components.consumer.features.compfeat`,
			wantSocket:  "bogus-component-socket",
		},
		{
			// The positive control for the row below: the identical `components:`
			// block at the root of an Aggregate. It halts, and it always did — so a
			// module row that halts is the rule firing rather than the fixture
			// tripping some unrelated gate.
			name:        "component attachment at an Aggregate root",
			release:     "release-contrib-component-root.yaml",
			wantLocator: `.spec.components.consumer.features.modfeat`,
			wantSocket:  "bogus-module-socket",
		},
		{
			// The same block one document deeper. The locator names the flattened
			// node rather than an authoring path inside the module, matching every
			// other post-flatten diagnostic: the module's own document is out of
			// view by the time the node exists.
			//
			// This row is answered after the composition is flattened rather than
			// by the pre-flatten rules above, which is why its locator still spells
			// the socket and still carries the bracketed keys.
			name:        "component attachment inside a sub-aggregate module",
			release:     "release-contrib-module.yaml",
			wantLocator: `.data.consumer.features["modfeat"].uses["bogus-module-socket"]`,
			wantSocket:  "bogus-module-socket",
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
				named := strings.Contains(v.Field, tc.wantSocket) || strings.Contains(v.Message, tc.wantSocket)
				if strings.Contains(v.Field, tc.wantLocator) && named {
					found = true
				}
			}
			if !found {
				t.Errorf("no violation located at %s naming socket %q; got %v",
					tc.wantLocator, tc.wantSocket, multi.Violations)
			}
		})
	}
}
