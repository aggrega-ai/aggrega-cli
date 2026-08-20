// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline_test

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The near-miss advisories are exit-code-neutral, and this file is where that is
// proved as a property of what a build EMITS rather than as a property of a
// declaration.
//
// A near miss is a heuristic: an author may deliberately name a dependency socket
// or an attachment one edit from a declared name, and refusing such a name would
// refuse legal authoring. So the reading names both spellings and lets the build
// finish. That consequence is invisible in the check's own source — a resolver
// finding carries no severity field, and which channel collects it is what
// decides whether the build stops — so the assertions below run a real resolve
// over an input that trips each reading and require BOTH halves: the advisory is
// reported, AND the resolve returns no error.
//
// A test that asserted only "a diagnostic appears" would pass just as happily
// against a build that halted on it, which is the exact regression this file
// exists to catch.

// nearMissProbe is one input that trips one of the two near-miss readings,
// spelled as the edit that produces it.
type nearMissProbe struct {
	name string
	// splice rewrites the application document's bytes in memory. The fixture on
	// disk is shared with other proofs and must keep producing what they expect.
	splice func(*testing.T, []byte) []byte
	// decorate adjusts what the catalog declares, for a reading whose near miss
	// needs a declaration to be near. The fixture catalog declares no attachment
	// slot, so the slot reading has nothing to pair against without this.
	decorate func(resolver.CatalogLookup) resolver.CatalogLookup
	// authored are the spellings the probe writes and declared is the one the
	// catalog declares. Every advisory must name the declared spelling and one
	// authored spelling, or the author cannot see the pair.
	authored []string
	declared string
	// wantAdvisories is how many advisories the probe's own reading produces.
	// Written out rather than compared against a second run, so the row states
	// something an implementation agreeing with itself cannot satisfy.
	wantAdvisories int
}

// spliceAt replaces the first occurrence of anchor with replacement, refusing a
// splice that changed nothing: an inert splice would leave every assertion below
// measuring the untouched fixture and passing.
func spliceAt(t *testing.T, in []byte, anchor, replacement string) []byte {
	t.Helper()
	if !bytes.Contains(in, []byte(anchor)) {
		t.Fatalf("fixture no longer carries the %q anchor; the splice would be silently inert", anchor)
	}
	out := bytes.Replace(in, []byte(anchor), []byte(replacement), 1)
	if bytes.Equal(out, in) {
		t.Fatal("splice changed nothing; the probe would not be authored")
	}
	return out
}

// declareSlot returns a lookup whose one component type declares one attachment
// slot, leaving every other answer the real catalog's.
//
// It decorates rather than replaces because everything else in the resolve has to
// stay real: a hand-built catalog would have to answer for every type the fixture
// names, and a wrong answer there would halt the build for a reason that has
// nothing to do with the reading under test.
func declareSlot(base resolver.CatalogLookup, componentType, slot string) resolver.CatalogLookup {
	inner := base.Component
	base.Component = func(typeURI string) (types.Component, bool) {
		comp, found := inner(typeURI)
		if !found || typeURI != componentType {
			return comp, found
		}
		slots := types.OrderedMap[types.AttachmentSlot]{}
		slots.Set(slot, types.AttachmentSlot{})
		comp.Spec.FeaturesSchema = slots
		return comp, true
	}
	return base
}

func nearMissProbes() []nearMissProbe {
	return []nearMissProbe{
		{
			// "apsi" is one transposition from the "apis" socket the web-ui type
			// declares.
			name:     "authored socket name near a declared one",
			authored: []string{"apsi"},
			declared: "apis",
			splice: func(t *testing.T, in []byte) []byte {
				return spliceAt(t, in, "        apis:\n",
					"        apsi:\n"+
						"          alt-api:\n"+
						"            target: backend\n"+
						"        apis:\n")
			},
			wantAdvisories: 1,
		},
		{
			// "audit_logs" is one insertion from the declared "audit_log" slot.
			name:     "attachment name near a declared slot",
			authored: []string{"audit_logs"},
			declared: "audit_log",
			decorate: func(l resolver.CatalogLookup) resolver.CatalogLookup {
				return declareSlot(l, "core/compute/web-ui", "audit_log")
			},
			splice: func(t *testing.T, in []byte) []byte {
				return spliceAt(t, in, "      type: core/compute/web-ui\n",
					"      type: core/compute/web-ui\n"+
						"      features:\n"+
						"        audit_logs:\n"+
						"          type: core/proof-injector\n")
			},
			wantAdvisories: 1,
		},
		{
			// Two near misses on one component, so the count assertion is made
			// over more than one occurrence. A single advisory cannot tell a
			// reading that reports every offence from one that reports the first.
			name:     "two authored sockets near the same declared one",
			authored: []string{"apsi", "aips"},
			declared: "apis",
			splice: func(t *testing.T, in []byte) []byte {
				return spliceAt(t, in, "        apis:\n",
					"        apsi:\n"+
						"          alt-api:\n"+
						"            target: backend\n"+
						"        aips:\n"+
						"          other-api:\n"+
						"            target: backend\n"+
						"        apis:\n")
			},
			wantAdvisories: 2,
		},
	}
}

// TestNearMissAdvisoryReportsWithoutHaltingTheBuild is the emitted-severity proof
// for both near-miss readings: each is reported on the advisory drain, and the
// resolve the report came from returns no error.
//
// It is deliberately not parallel: it redirects the process diagnostic stream.
func TestNearMissAdvisoryReportsWithoutHaltingTheBuild(t *testing.T) {
	for _, probe := range nearMissProbes() {
		t.Run(probe.name, func(t *testing.T) {
			rel, err := resolver.LoadRelease(fullstackReleasePath)
			if err != nil {
				t.Fatalf("load release: %v", err)
			}
			manifest, err := resolver.LoadManifest(fullstackManifestPath)
			if err != nil {
				t.Fatalf("load manifest: %v", err)
			}
			aggBytes, err := os.ReadFile(fullstackAggregatePath)
			if err != nil {
				t.Fatalf("read aggregate: %v", err)
			}
			cat, err := catalog.LoadAll("", fullstackCatalogPath)
			if err != nil {
				t.Fatalf("catalog load: %v", err)
			}
			if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
				t.Fatalf("catalog extends: %d error(s); first: %v", len(extErrs), extErrs[0])
			}

			lookup := wiredLookup(cat, manifest)
			if probe.decorate != nil {
				lookup = probe.decorate(lookup)
			}
			inputs := pipeline.Inputs{
				Release:           rel,
				Manifest:          manifest,
				AggregateRawBytes: probe.splice(t, aggBytes),
				AggregatePath:     fullstackAggregatePath,
				WorkspaceConfig:   workspaceconfig.Default(),
				CompilerVersion:   fixtureCompilerVersion,
			}

			var advisories []*resolver.ResolverError
			var resolveErr error
			stderr := captureStderr(t, func() {
				resolveErr = pipeline.ResolveAndValidate(context.Background(), inputs,
					lookup,
					cat.RecipeLookupWithManifest(manifest).Recipe,
					func(rr *resolver.ResolveResult) error {
						advisories = rr.LintAdvisories
						return nil
					},
				)
			})

			// The half that a "a diagnostic still appears" assertion cannot make:
			// the reading is advisory, so the resolve it came from must succeed.
			if resolveErr != nil {
				t.Fatalf("the near-miss reading must not halt the build; resolve returned %v\nstderr:\n%s",
					resolveErr, stderr)
			}

			named := 0
			for _, a := range advisories {
				if !strings.Contains(a.Message, probe.declared) {
					continue
				}
				for _, authored := range probe.authored {
					if strings.Contains(a.Message, authored) {
						named++
						break
					}
				}
			}
			if named != probe.wantAdvisories {
				t.Errorf("advisories pairing %q with one of %q = %d; want %d\nall advisories: %v",
					probe.declared, probe.authored, named, probe.wantAdvisories, advisories)
			}
			if !strings.Contains(stderr, "lint:") {
				t.Errorf("the advisory did not reach the advisory drain.\nstderr:\n%s", stderr)
			}
			for _, authored := range probe.authored {
				if !strings.Contains(stderr, authored) {
					t.Errorf("the diagnostic stream does not name the authored spelling %q.\nstderr:\n%s",
						authored, stderr)
				}
				// The offending name must not reach the halting stream, whose lines
				// the build prefixes differently. A reading that halted would print
				// there.
				for _, line := range strings.Split(stderr, "\n") {
					if strings.Contains(line, "guard:") && strings.Contains(line, authored) {
						t.Errorf("the near miss reached the halting stream: %q", line)
					}
				}
			}

			// Control: the unspliced fixture must produce none of this, or every
			// assertion above could be passing on unrelated output.
			inputs.AggregateRawBytes = aggBytes
			clean := captureStderr(t, func() {
				if rerr := pipeline.ResolveAndValidate(context.Background(), inputs,
					lookup,
					cat.RecipeLookupWithManifest(manifest).Recipe,
					func(*resolver.ResolveResult) error { return nil },
				); rerr != nil {
					t.Errorf("ResolveAndValidate (control): %v", rerr)
				}
			})
			for _, authored := range probe.authored {
				if strings.Contains(clean, authored) {
					t.Errorf("the control run named %q; the fixture is not clean.\nstderr:\n%s",
						authored, clean)
				}
			}
		})
	}
}
