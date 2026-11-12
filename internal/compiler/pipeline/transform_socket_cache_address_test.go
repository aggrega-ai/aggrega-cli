// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pipeline

import (
	"context"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// A transform-role guest's bytes live in a cache slot addressed by a version, and
// the lockfile pins are what normally supply it. A pass that has no lockfile
// therefore addresses the unversioned slot and misses a guest that is on disk, so
// such a pass derives the address from the cached manifest instead.
//
// These guards hold the two halves of that, because neither fails loudly on its
// own. A fall-through that leaked into a pass which DID supply a lockfile would
// resolve an output-shaping guest the lockfile does not pin — silently, since the
// resolution succeeds and only the pin is missing. And a fall-through wired at one
// transform role but not the other would leave the unwired role failing exactly as
// it does today, which reads as "that role is simply unused" rather than as a gap.
//
// Both roles are driven through their own runner rather than through the shared
// construction they call, so the guard holds what each role actually does. Plugin
// resolution is the first thing either runner reaches, so neither needs the graph
// state a real run would carry.

const (
	transformGuardCoordinate = "governance/guard-probe"
	transformGuardVersion    = "9.9.9"
	transformGuardCachedRef  = transformGuardCoordinate + ":" + transformGuardVersion
)

// transformGuardCatalog returns a catalog lookup serving one Plugin entity for a
// transform-role guest, and a pointer to the slice recording every reference the
// cache-backed read is asked for.
//
// The entity resolves, so the entity-fronted cascade gets PAST its front and fails
// where a lockfile-free resolution really fails: with no address for the versioned
// cache slot. The cache read reports a clean miss, so what each case asserts is
// whether that read was consulted and with which reference — never that the
// fixture holds loadable bytes, which it deliberately does not.
func transformGuardCatalog() (resolver.CatalogLookup, *[]string) {
	var entity types.Plugin
	entity.Metadata.Name = "guard-probe"
	entity.Metadata.PluginType = "governance"
	entity.Metadata.Version = transformGuardVersion

	asked := &[]string{}
	return resolver.CatalogLookup{
		Plugin: func(string) (types.Plugin, bool) {
			return entity, true
		},
		PluginFromCache: func(uri string) (types.Plugin, string, bool, error) {
			*asked = append(*asked, uri)
			return types.Plugin{}, "", false, nil
		},
	}, asked
}

// transformGuardRunners drives each transform role through its own runner. The
// attachment names its plugin by bare coordinate, which is the form a
// reconstructed attachment carries and the form that has no version to address a
// cache slot with.
var transformGuardRunners = []struct {
	role plugintype.Role
	run  func(context.Context, Inputs, resolver.CatalogLookup) error
}{
	{
		role: plugintype.RoleLabelTransform,
		run: func(ctx context.Context, in Inputs, lookup resolver.CatalogLookup) error {
			return runMutatorSocket(ctx, in, lookup, nil)
		},
	},
	{
		role: plugintype.RoleAnnotationTransform,
		run: func(ctx context.Context, in Inputs, lookup resolver.CatalogLookup) error {
			return runAnnotationSocket(ctx, in, lookup, &resolver.ResolveResult{Release: &types.Release{}})
		},
	},
}

// transformGuardInputs builds pipeline inputs carrying exactly one transform
// attachment in the given role, with lf as the pass's lockfile — nil for a pass
// that has none to read.
//
// The pin source is derived from lf so each case states what its own pass means,
// exactly as the two production surfaces these cases model do. A pass with no
// record names a pin-free source: it is the pass that WRITES the record, so no
// recorded identity governs what it resolves. A pass handed a record names a
// recorded source over that record's derived pins, so the record governs
// whatever it happens to pin — an empty one included.
//
// Naming it is not optional here. These runners reach the resolver construction
// directly rather than through the graph entry, so the entry's refusal never runs
// for them and an omitted source is read by nothing that could report it.
func transformGuardInputs(t *testing.T, role plugintype.Role, lf *lockfile.Lockfile) Inputs {
	t.Helper()
	cfg := &workspaceconfig.Config{}
	cfg.Plugins.Governance = []workspaceconfig.PluginRef{{URI: transformGuardCoordinate, Role: role}}
	pins := pluginpath.NoPins()
	if lf != nil {
		pins = pluginpath.RecordedPins(lockfile.PluginPins(lf, ""))
	}
	return Inputs{WorkspaceConfig: cfg, WorkspaceRoot: t.TempDir(), Lockfile: lf, PluginPins: pins}
}

// TestTransformSocketResolvesFromTheCacheWhenNoLockfileSuppliesTheAddress holds
// that BOTH transform roles reach the cached-manifest read on a pass with no
// lockfile, and that each asks for the versioned reference the entity's cache slot
// is addressed by rather than the bare coordinate the attachment names.
func TestTransformSocketResolvesFromTheCacheWhenNoLockfileSuppliesTheAddress(t *testing.T) {
	for _, tc := range transformGuardRunners {
		t.Run(string(tc.role), func(t *testing.T) {
			lookup, asked := transformGuardCatalog()

			err := tc.run(context.Background(), transformGuardInputs(t, tc.role, nil), lookup)
			if err == nil {
				t.Fatal("the fixture caches no guest bytes, so the run cannot succeed; a clean run would make this guard vacuous")
			}
			if len(*asked) == 0 {
				t.Fatalf("the %s role never reached the cache-backed read; a pass with no lockfile has no address for the versioned slot and must derive one from the cached manifest (it halted with: %v)", tc.role, err)
			}
			if (*asked)[0] != transformGuardCachedRef {
				t.Errorf("the cache was asked for %q; the entity's slot is addressed by the versioned reference %q, and a bare coordinate misses it", (*asked)[0], transformGuardCachedRef)
			}
		})
	}
}

// TestTransformSocketKeepsThePinnedCascadeWhenALockfileIsSupplied is the other
// half: a pass that supplied a lockfile resolves a transform guest strictly
// through the pinned cascade, whatever that lockfile happens to pin. An empty one
// is supplied deliberately — it pins no plugin at all, so a fall-through keyed on
// "this plugin has no pin" would fire here while one keyed on "no lockfile was
// supplied" does not, and only the second keeps an unpinned output-shaping guest
// from resolving.
func TestTransformSocketKeepsThePinnedCascadeWhenALockfileIsSupplied(t *testing.T) {
	for _, tc := range transformGuardRunners {
		t.Run(string(tc.role), func(t *testing.T) {
			lookup, asked := transformGuardCatalog()

			err := tc.run(context.Background(), transformGuardInputs(t, tc.role, &lockfile.Lockfile{}), lookup)
			if err == nil {
				t.Fatal("the fixture resolves no guest bytes, so the run cannot succeed; a clean run would make this guard vacuous")
			}
			if len(*asked) != 0 {
				t.Errorf("the %s role consulted the cache-backed read while a lockfile was supplied (it asked for %v); a supplied lockfile governs every guest that shapes output bytes, so an attachment missing from it must halt rather than resolve unpinned", tc.role, *asked)
			}
		})
	}
}
