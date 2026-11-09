// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The tests in this file hold that a Feature an attachment on a connection names
// is pinned exactly as one a component's own attachment names: the one lock-time
// walk records it under the connection entry its author wrote — in the Aggregate,
// in a module the Aggregate imports, and in the Release — beside the realization
// the Manifest binds to it, and a lockfile without its row is refused as drift
// naming that entry.
//
// With the walk reading a component's own attachments alone, the rows carrying a
// `.uses.` site go absent: the first two tests miss them, and the third finds no
// row to remove.

// connectionLookup resolves every reference the fixtures in this file name, each
// to a digest of its own, so a row recorded under the wrong reference is visible
// and a reference no fixture names fails the walk.
func connectionLookup() *fakeLookup {
	ents := map[string]closure.Resolution{}
	for _, uri := range []string{
		"app/gw", "app/model", "sec/token", "sec/token:1.0.0", "sec/badge", "obs/audit",
		"helm/token-external:1.0.0", "helm/token-plain:1.0.0", "helm/badge-generated:1.0.0",
		connectionModuleURI,
	} {
		ents[uri] = resolutionOK("1.0.0", "sha256:"+uri, "tier1-local")
	}
	return &fakeLookup{entities: ents, plugins: map[string]closure.Resolution{}}
}

const connectionModuleURI = "example/gatewaymod:1.0.0"

func connectionManifest(features string) string {
	return "apiVersion: aggrega.ai/v1beta1\nkind: Manifest\nmetadata:\n  name: m\n  version: \"1.0.0\"\nspec:\n  mappings:\n" + features
}

// connectionAggregate is a gateway drawing two connections on its `models`
// socket; uses is the gateway's whole `uses:` block, written at column zero.
func connectionAggregate(scope, uses string) string {
	head := "apiVersion: aggrega.ai/v1beta1\nkind: Aggregate\nmetadata:\n  name: a\n  version: \"1.0.0\"\nspec:\n"
	pad := "  "
	if scope != "" {
		head += "  subsystems:\n    " + scope + ":\n"
		pad = "      "
	}
	body := pad + "components:\n" +
		pad + "  router:\n" + pad + "    type: app/gw\n" + pad + "    uses:\n" + indentBy(uses, pad+"      ") +
		pad + "  gpt:\n" + pad + "    type: app/model\n" +
		pad + "  nim:\n" + pad + "    type: app/model\n"
	return head + body
}

func indentBy(block, pad string) string {
	var b strings.Builder
	for _, line := range strings.Split(strings.Trim(block, "\n"), "\n") {
		b.WriteString(pad + line + "\n")
	}
	return b.String()
}

const (
	gptCarriesToken = `models:
  gpt:
    target: gpt
    features:
      credential:
        type: sec/token
        values: { key: api-key }
  nim:
    target: nim
`
	bothCarryToken = `models:
  gpt:
    target: gpt
    features:
      credential: { type: sec/token }
  nim:
    target: nim
    features:
      credential: { type: sec/token }
`
	noAttachment = `models:
  gpt: { target: gpt }
  nim: { target: nim }
`
	compactConnections = `models: [gpt, nim]
`
)

// connectionRelease is a Release whose one boundary, prod, holds boundary — written
// at column zero. It names no Aggregate or Manifest reference of its own: the walk
// is handed both documents directly, so a reference here would only add two rows
// every case would have to list.
func connectionRelease(boundary string) string {
	return "apiVersion: aggrega.ai/v1beta1\nkind: Release\nmetadata:\n  name: r\n  environment: test\n  version: \"1.0.0\"\nspec:\n  boundaries:\n    prod:\n" + indentBy(boundary, "      ")
}

// TestTheWalkPinsTheFeatureEveryConnectionAttachmentNames holds the walk to every
// Feature type a connection attachment names, on either document, each recorded
// with the feature kind under the path of the connection entry the author wrote.
// The last rows are the controls: a connection carrying nothing and a compact
// socket record exactly what a component with no connection attachment always
// recorded.
func TestTheWalkPinsTheFeatureEveryConnectionAttachmentNames(t *testing.T) {
	t.Parallel()

	type pin struct {
		kind           string
		referencedFrom []string
	}
	const (
		top     = "aggregate.spec.components."
		routerC = top + "router.uses.models.gpt.features.credential.type"
	)
	components := func(prefix string) map[string]pin {
		return map[string]pin{
			"app/gw":    {"component", []string{prefix + "router.type"}},
			"app/model": {"component", []string{prefix + "gpt.type", prefix + "nim.type"}},
		}
	}
	with := func(base map[string]pin, extra map[string]pin) map[string]pin {
		out := map[string]pin{}
		for k, v := range base {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}

	cases := []struct {
		name      string
		aggregate string
		release   string
		manifest  string
		want      map[string]pin
	}{
		{
			name:      "an Aggregate connection attachment whose realization the name tier binds",
			aggregate: connectionAggregate("", gptCarriesToken),
			manifest: connectionManifest(`    features:
      name:
        gpt.credential: helm/token-external:1.0.0
`),
			want: with(components(top), map[string]pin{
				"sec/token":                 {"feature", []string{routerC}},
				"helm/token-external:1.0.0": {"feature_recipe", []string{"manifest.spec.mappings.features.name.gpt.credential"}},
			}),
		},
		{
			name:      "an Aggregate connection attachment whose realization the type tier binds",
			aggregate: connectionAggregate("", gptCarriesToken),
			manifest: connectionManifest(`    features:
      type:
        sec/token:1.0.0: helm/token-plain:1.0.0
`),
			want: with(components(top), map[string]pin{
				"sec/token":              {"feature", []string{routerC}},
				"sec/token:1.0.0":        {"feature", []string{"manifest.spec.mappings.features.type.sec/token:1.0.0"}},
				"helm/token-plain:1.0.0": {"feature_recipe", []string{"manifest.spec.mappings.features.type.sec/token:1.0.0"}},
			}),
		},
		{
			name:      "a connection attachment in a subsystem the Aggregate declares",
			aggregate: connectionAggregate("edge", gptCarriesToken),
			manifest:  connectionManifest(""),
			want: with(components("aggregate.spec.subsystems.edge.components."), map[string]pin{
				"sec/token": {"feature", []string{"aggregate.spec.subsystems.edge.components.router.uses.models.gpt.features.credential.type"}},
			}),
		},
		{
			name:      "an attachment only the Release makes on a connection",
			aggregate: connectionAggregate("", noAttachment),
			release: `components:
  router:
    uses:
      models:
        nim:
          features:
            credential:
              type: sec/badge
`,
			manifest: connectionManifest(`    features:
      name:
        nim.credential: helm/badge-generated:1.0.0
`),
			want: with(components(top), map[string]pin{
				"sec/badge":                  {"feature", []string{"release.spec.boundaries.prod.components.router.uses.models.nim.features.credential.type"}},
				"helm/badge-generated:1.0.0": {"feature_recipe", []string{"manifest.spec.mappings.features.name.nim.credential"}},
			}),
		},
		{
			name:      "a Release entry sizing the Aggregate's attachment adds no reference of its own",
			aggregate: connectionAggregate("", gptCarriesToken),
			release: `components:
  router:
    uses:
      models:
        gpt:
          features:
            credential:
              infra: { rotation: hourly }
`,
			manifest: connectionManifest(""),
			want: with(components(top), map[string]pin{
				"sec/token": {"feature", []string{routerC}},
			}),
		},
		{
			name:      "a Release attachment on a component routed in from a module, under the routing entry",
			aggregate: connectionAggregate("", noAttachment),
			release: `aggregates:
  mod:
    components:
      router:
        uses:
          models:
            gpt:
              features:
                credential: { type: sec/badge }
`,
			manifest: connectionManifest(""),
			want: with(components(top), map[string]pin{
				"sec/badge": {"feature", []string{"release.spec.boundaries.prod.aggregates.mod.components.router.uses.models.gpt.features.credential.type"}},
			}),
		},
		{
			name:      "two connections naming one Feature type are one row naming both",
			aggregate: connectionAggregate("", bothCarryToken),
			manifest:  connectionManifest(""),
			want: with(components(top), map[string]pin{
				"sec/token": {"feature", []string{routerC, top + "router.uses.models.nim.features.credential.type"}},
			}),
		},
		{
			name:      "connections carrying no attachment record what they always recorded",
			aggregate: connectionAggregate("", noAttachment),
			manifest:  connectionManifest(""),
			want:      components(top),
		},
		{
			name:      "compact connections record what they always recorded",
			aggregate: connectionAggregate("", compactConnections),
			manifest:  connectionManifest(""),
			want:      components(top),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rel := &types.Release{}
			if tc.release != "" {
				rel = loadRelease(t, connectionRelease(tc.release))
			}
			c, err := closure.ComputeClosure(context.Background(), rel, loadManifest(t, tc.manifest),
				types.Normalize(loadAggregate(t, tc.aggregate)), workspaceconfig.Default(), connectionLookup())
			if err != nil {
				t.Fatalf("ComputeClosure: %v", err)
			}
			got := map[string]pin{}
			for _, e := range c.Entities {
				got[e.URI] = pin{kind: e.KindHint, referencedFrom: e.ReferencedFrom}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("the walk recorded\n  %v\nwant\n  %v", got, tc.want)
			}
		})
	}
}

// TestTheSubAggregateDescentPinsTheFeatureAModulesConnectionCarries holds the
// transitive descent to the same rule: a connection attachment inside a module
// the Aggregate imports is pinned under the module's own authoring path.
func TestTheSubAggregateDescentPinsTheFeatureAModulesConnectionCarries(t *testing.T) {
	t.Parallel()
	top := loadAggregate(t, "apiVersion: aggrega.ai/v1beta1\nkind: Aggregate\nmetadata:\n  name: top\n  version: \"1.0.0\"\nspec:\n  aggregates:\n    mod:\n      type: "+connectionModuleURI+"\n")
	loader := &fakeAggLoader{bodies: map[string]*types.Aggregate{
		connectionModuleURI: loadAggregate(t, connectionAggregate("", gptCarriesToken)),
	}}
	lookup := connectionLookup()
	c, err := closure.ComputeClosure(context.Background(), &types.Release{}, nil, types.Normalize(top),
		workspaceconfig.Default(), lookup)
	if err != nil {
		t.Fatalf("ComputeClosure: %v", err)
	}
	if err := closure.DescendSubAggregates(context.Background(), &c, loader, lookup); err != nil {
		t.Fatalf("DescendSubAggregates: %v", err)
	}
	want := "aggregate(" + connectionModuleURI + ").spec.components.router.uses.models.gpt.features.credential.type"
	for _, e := range c.Entities {
		if e.URI != "sec/token" {
			continue
		}
		if !reflect.DeepEqual(e.ReferencedFrom, []string{want}) || e.KindHint != "feature" {
			t.Fatalf("the module's connection attachment is pinned as (%q, %v); want (feature, [%s])", e.KindHint, e.ReferencedFrom, want)
		}
		return
	}
	t.Fatalf("the descent recorded no row for the Feature the module's connection attachment names; entities: %v", entityURIs(c))
}

// TestALockfileWithoutAConnectionAttachmentPinIsRefusedAsDrift holds the build's
// verify to the rows a connection attachment brings: a lockfile written from the
// walk is accepted, and with the Feature's row or the realization's row removed it
// is refused under the lockfile-drift condition, naming the reference and the
// entry that asked for it — the connection entry for a Feature, the instance name
// for a realization the name tier binds.
func TestALockfileWithoutAConnectionAttachmentPinIsRefusedAsDrift(t *testing.T) {
	t.Parallel()

	rel := loadRelease(t, connectionRelease(`components:
  router:
    uses:
      models:
        nim:
          features:
            credential: { type: sec/badge }
`))
	man := loadManifest(t, connectionManifest(`    features:
      name:
        gpt.credential: helm/token-external:1.0.0
        nim.credential: helm/badge-generated:1.0.0
`))
	c, err := closure.ComputeClosure(context.Background(), rel, man,
		types.Normalize(loadAggregate(t, connectionAggregate("", gptCarriesToken))), workspaceconfig.Default(), connectionLookup())
	if err != nil {
		t.Fatalf("ComputeClosure: %v", err)
	}
	if err := lockfile.Verify(lockfileOfEntities(c), c); err != nil {
		t.Fatalf("positive control: the lockfile written from the walk must verify; got %v", err)
	}

	cases := []struct {
		name string
		drop string
		kind string
		site string
	}{
		{"the Feature an Aggregate connection attachment names", "sec/token", closure.KindFeature,
			"aggregate.spec.components.router.uses.models.gpt.features.credential.type"},
		{"the Feature an attachment only the Release makes names", "sec/badge", closure.KindFeature,
			"release.spec.boundaries.prod.components.router.uses.models.nim.features.credential.type"},
		{"the realization the name tier binds to the connection attachment", "helm/token-external:1.0.0", closure.KindFeatureRecipe,
			"manifest.spec.mappings.features.name.gpt.credential"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			lf := lockfileOfEntities(c)
			kept := lf.Entities[:0]
			for _, e := range lf.Entities {
				if e.URI != tc.drop {
					kept = append(kept, e)
				}
			}
			if len(kept) == len(lf.Entities) {
				t.Fatalf("precondition: the lockfile written from the walk carries no row for %q", tc.drop)
			}
			lf.Entities = kept

			err := lockfile.Verify(lf, c)
			if !errors.Is(err, lockfile.ErrLockfileDrift()) {
				t.Fatalf("a lockfile without the row for %q must be refused as drift; got %v", tc.drop, err)
			}
			want := `entity "` + tc.drop + `" (` + tc.kind + `) not in lockfile (referenced from ` + tc.site + `)`
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not carry %q:\n%v", want, err)
			}
		})
	}
}
