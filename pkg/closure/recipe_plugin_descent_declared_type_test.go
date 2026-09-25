// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// declaredTypePluginName is the bare metadata.name every fixture below publishes
// under. The artifact reference a Plugin entity derives is <pluginType>/<name>, so
// holding the name fixed makes the declared type the only variable across rows.
const declaredTypePluginName = "engine"

// declaredTypeCase is one declared-type row: the plugin type an entity declares,
// whether its .wasm artifact must be pinned, and why.
type declaredTypeCase struct {
	declaredType string
	wantPinned   bool
	why          string
}

// declaredTypeCases enumerates the pin outcome for every plugin type the registry
// carries, plus a type it does not. It is the full domain of the artifact pin
// decision, because a Plugin entity's metadata.pluginType is what the decision
// reads and an entity may declare any of these.
//
// The expected column is written from the product rule — a plugin whose bytes
// reach the rendered output is pinned, one that only inspects or judges is not —
// rather than read back out of the registry, so a registry value that stops
// matching the rule fails a row here instead of agreeing with itself.
func declaredTypeCases() map[string]declaredTypeCase {
	return map[string]declaredTypeCase{
		"provider": {
			declaredType: string(plugintype.Provider),
			wantPinned:   true,
			why:          "a provider renders the artifacts, so its bytes are the output",
		},
		"governance": {
			declaredType: string(plugintype.Governance),
			wantPinned:   true,
			why:          "a transform-role attachment rewrites the graph the render reads",
		},
		"pipeline-step": {
			declaredType: string(plugintype.PipelineStep),
			wantPinned:   true,
			why:          "a pipeline step patches the documents a recipe renders",
		},
		"linter": {
			declaredType: string(plugintype.Linter),
			wantPinned:   false,
			why:          "a linter raises diagnostics and changes no rendered byte",
		},
		"importer": {
			declaredType: string(plugintype.Importer),
			wantPinned:   false,
			why:          "an importer drafts authoring files and never runs in a render",
		},
		"parser": {
			declaredType: string(plugintype.Parser),
			wantPinned:   false,
			why:          "a parser turns source text into a tree and emits no artifact",
		},
		"cli": {
			declaredType: string(plugintype.CLI),
			wantPinned:   false,
			why:          "a runtime subcommand runs outside the build entirely",
		},
		"unregistered-type": {
			declaredType: "unregistered-plugin-type",
			wantPinned:   true,
			why:          "an unknown type pins, because over-pinning costs a row and under-pinning moves output bytes",
		},
	}
}

// declaredTypeFixture wires a recipe whose pipeline step references a Plugin
// entity declaring declaredType, with the entity and its .wasm artifact both
// resolved from the registry at the supplied digests. Resolving both from the
// registry keeps the resolution path constant across rows, so the declared type is
// the only thing the pin decision can be reading.
func declaredTypeFixture(t *testing.T, declaredType, artifactDigest string) (*fakeLookup, *fakeRecipeLoader, string, string) {
	t.Helper()
	artifactURI := declaredType + "/" + declaredTypePluginName
	entityURI := artifactURI + ":1.0.0"

	lookup := pluginDescentLookup()
	lookup.entities[entityURI] = resolutionOK("1.0.0", "sha256:declared-type-entity", "tier-3-pull")
	lookup.plugins[artifactURI] = resolutionOK("1.0.0", artifactDigest, "tier-3-pull")

	loader := &fakeRecipeLoader{
		recipes: map[string]*types.Recipe{
			recipeURI: recipeWithPipelinePlugin(t, "svc", registryProvider, entityURI),
		},
		plugins: map[string]*types.Plugin{
			entityURI: pluginEntity(t, declaredTypePluginName, declaredType),
		},
	}
	return lookup, loader, entityURI, artifactURI
}

// TestPluginDescent_ArtifactPinFollowsDeclaredType proves the closure's artifact
// pin decision reads the declared plugin type's classification and acts on it:
// an output-shaping type is pinned, a judging or authoring-input type is not, and
// a type the registry does not carry is pinned as the safe direction.
//
// Every row resolves entity and artifact from the registry, so no resolution-path
// rule is in play and the declared type is the only input that varies. Each row
// also asserts the ENTITY was pinned, which is the per-row positive control: the
// entity axis reads the resolution path and not the type, so its pin proves the
// fixture actually reached the descent. Without it a "not pinned" row would pass
// just as well when nothing was descended at all.
func TestPluginDescent_ArtifactPinFollowsDeclaredType(t *testing.T) {
	for name, tc := range declaredTypeCases() {
		name, tc := name, tc
		t.Run(name, func(t *testing.T) {
			lookup, loader, entityURI, artifactURI := declaredTypeFixture(t, tc.declaredType, "sha256:declared-type-artifact")

			cl, err := runPluginDescent(t, lookup, loader)
			if err != nil {
				t.Fatalf("plugin descent: %v", err)
			}

			if _, ok := entityByURI(cl, entityURI); !ok {
				t.Fatalf("entity %q must be pinned for every declared type — the entity axis reads the "+
					"resolution path, not the type; its absence means the fixture never reached the "+
					"descent and the artifact verdict below measures nothing; entities=%+v",
					entityURI, cl.Entities)
			}

			_, pinned := pluginByURI(cl, artifactURI)
			if pinned != tc.wantPinned {
				t.Errorf("declared plugin type %q: artifact %q pinned = %v; want %v (%s); plugins=%+v",
					tc.declaredType, artifactURI, pinned, tc.wantPinned, tc.why, cl.Plugins)
			}
		})
	}
}

// TestPluginDescent_UnpinnedArtifactAdoptsAChangeSilently drives what an absent pin
// costs: an artifact that is pinned records its content digest,
// so a change to the guest's bytes moves the closure and a build comparing against
// the recorded one reports it; an artifact that is not pinned records nothing, so
// the same change moves nothing and is adopted by the very next build.
//
// The two arms differ only in the declared plugin type, and both run the identical
// pair of acquisitions over the identical pair of digests. That is what makes the
// contrast attributable to the pin rather than to the fixture.
func TestPluginDescent_UnpinnedArtifactAdoptsAChangeSilently(t *testing.T) {
	acquire := func(t *testing.T, declaredType, artifactDigest string) closure.Closure {
		t.Helper()
		lookup, loader, _, _ := declaredTypeFixture(t, declaredType, artifactDigest)
		cl, err := runPluginDescent(t, lookup, loader)
		if err != nil {
			t.Fatalf("plugin descent: %v", err)
		}
		return cl
	}

	const (
		before = "sha256:guest-bytes-before"
		after  = "sha256:guest-bytes-after"
	)

	t.Run("output-shaping-type-records-the-change", func(t *testing.T) {
		first := acquire(t, string(plugintype.PipelineStep), before)
		second := acquire(t, string(plugintype.PipelineStep), after)
		if reflect.DeepEqual(first.Plugins, second.Plugins) {
			t.Fatalf("two acquisitions of an output-shaping plugin whose bytes changed produced an "+
				"identical plugin axis, so the change is invisible to a drift comparison; plugins=%+v",
				first.Plugins)
		}
	})

	t.Run("non-output-shaping-type-cannot-record-it", func(t *testing.T) {
		first := acquire(t, string(plugintype.Linter), before)
		second := acquire(t, string(plugintype.Linter), after)
		if !reflect.DeepEqual(first.Plugins, second.Plugins) {
			t.Fatalf("a plugin the closure does not pin must leave the plugin axis identical across a "+
				"byte change:\n first=%#v\nsecond=%#v", first.Plugins, second.Plugins)
		}
		// Stated positively so the row cannot pass on two axes that are equal because
		// the descent produced nothing at all for either acquisition.
		if _, ok := pluginByURI(first, string(plugintype.Linter)+"/"+declaredTypePluginName); ok {
			t.Fatalf("the unpinned arm must record no artifact; plugins=%+v", first.Plugins)
		}
	})
}

// TestPluginDescent_SocketReconstructionAgreesWithAcquisitionOnDeclaredType holds the
// workspace-socket reconstruction a build performs from the lockfile to the same
// declared-type rule the acquisition's artifact descent applies. For every plugin type
// an entity may declare, the two sides either both pin its .wasm artifact or both leave
// it out, and which of the two follows the declared-type table above: an
// output-shaping type is pinned on both sides, a type that only inspects or judges on
// neither. A disagreement is what a build reports as drift against a lockfile nobody
// changed — the reconstruction producing an artifact row the acquisition never wrote,
// or dropping one it did.
//
// Every row resolves the entity and its artifact from the registry, so the declared
// type is the only input that varies; TestTheSocketReconstructionPinsWhatThePullPinned
// varies the resolution path instead, over the provider and governance types. Each row
// asserts the entity is present on both sides — pinned by the acquisition, and re-added
// by the reconstruction from the URI the lockfile records — which is the per-row
// positive control: without it, a row pinned on neither side would pass just as well
// when neither side descended anything at all.
//
// What a build reconstructs in the shipped product is the provider and
// transform-attachment set, both output-shaping. The rows declaring any other type hold
// the rule itself, so a change to how a type is classified moves both sides together.
func TestPluginDescent_SocketReconstructionAgreesWithAcquisitionOnDeclaredType(t *testing.T) {
	for name, tc := range declaredTypeCases() {
		t.Run(name, func(t *testing.T) {
			lookup, loader, entityURI, artifactURI := declaredTypeFixture(t, tc.declaredType, "sha256:socket-artifact")

			acquired, err := runPluginDescent(t, lookup, loader)
			if err != nil {
				t.Fatalf("plugin descent: %v", err)
			}
			var rebuilt closure.Closure
			if err := closure.ReconstructWorkspaceSocketEntities(
				context.Background(), &rebuilt, []string{entityURI}, loader, lookup, nil); err != nil {
				t.Fatalf("workspace-socket reconstruction: %v", err)
			}

			if _, ok := entityByURI(acquired, entityURI); !ok {
				t.Fatalf("the acquisition must pin entity %q for every declared type; its absence means "+
					"the fixture never reached the descent and the artifact verdicts below measure "+
					"nothing; entities=%+v", entityURI, acquired.Entities)
			}
			if _, ok := entityByURI(rebuilt, entityURI); !ok {
				t.Fatalf("the reconstruction must re-add entity %q, a URI the lockfile records, for every "+
					"declared type; its absence means the artifact verdicts below measure nothing; "+
					"entities=%+v", entityURI, rebuilt.Entities)
			}

			acquiredArt, acquiredPin := pluginByURI(acquired, artifactURI)
			rebuiltArt, rebuiltPin := pluginByURI(rebuilt, artifactURI)
			if acquiredPin != tc.wantPinned {
				t.Errorf("declared plugin type %q: the acquisition pinned artifact %q = %v; want %v (%s); "+
					"plugins=%+v", tc.declaredType, artifactURI, acquiredPin, tc.wantPinned, tc.why, acquired.Plugins)
			}
			if rebuiltPin != acquiredPin {
				t.Errorf("declared plugin type %q: the reconstruction pinned artifact %q = %v and the "+
					"acquisition = %v, so a build holds the lockfile to an artifact row the pull did not "+
					"write, or drops one it did; plugins=%+v",
					tc.declaredType, artifactURI, rebuiltPin, acquiredPin, rebuilt.Plugins)
			}
			if acquiredPin && rebuiltPin && (rebuiltArt.ResolvedVersion != acquiredArt.ResolvedVersion || rebuiltArt.Digest != acquiredArt.Digest) {
				t.Errorf("declared plugin type %q: the reconstruction pinned (%s, %s); the acquisition pinned (%s, %s)",
					tc.declaredType, rebuiltArt.ResolvedVersion, rebuiltArt.Digest, acquiredArt.ResolvedVersion, acquiredArt.Digest)
			}
		})
	}
}
