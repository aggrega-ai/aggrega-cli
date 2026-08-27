// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline_test

import (
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

// The reading over what a bound Recipe publishes is exit-code-neutral, and this
// file is where that is proved as a property of what a build EMITS rather than of
// a declaration.
//
// Publishing more than a Component declares is legal: the provides contract is
// subset-required and superset-permitted. So the reading names the key and lets
// the build finish. That consequence is invisible in the rule's own source — a
// resolver finding carries no severity, and which collection it is appended to is
// what decides whether the build stops — so the assertion below runs a real
// resolve over an input that trips the reading and requires BOTH halves: the
// advisory is reported, AND the resolve returns no error.
//
// The condition is authored by wrapping the Recipe binding rather than by editing
// a fixture on disk: an extra published key is legal input, so adding one changes
// nothing else about the build, while removing a DECLARED key would relax a
// contract other readings over the same fixture are asserted against.
const extraPublishedKey = "debug_marker"

// publishingAnExtraKey wraps a binding so the Recipe it answers for one component
// type publishes one further key, with a literal template so nothing about the
// probe depends on what a template may reference.
//
// The Recipe is copied before the key is added. The catalog hands back a value
// whose provides map is shared with the parsed document, so writing into it would
// leave the fixture altered for every later reader in the same process.
func publishingAnExtraKey(inner resolver.RecipeBinding, componentType string) resolver.RecipeBinding {
	return func(instanceName, ct string) (*types.Recipe, bool) {
		recipe, found := inner(instanceName, ct)
		if !found || recipe == nil || ct != componentType {
			return recipe, found
		}
		copied := *recipe
		provides := types.OrderedMap[string]{}
		for k, v := range recipe.Spec.Provides.All() {
			provides.Set(k, v)
		}
		provides.Set(extraPublishedKey, "literal")
		copied.Spec.Provides = provides
		return &copied, true
	}
}

// TestProvidesPublicationAdvisoryReportsWithoutHaltingTheBuild is the
// emitted-severity proof for the reading, and the proof that a real resolve
// reaches it at all.
//
// It is deliberately not parallel: it redirects the process diagnostic stream.
func TestProvidesPublicationAdvisoryReportsWithoutHaltingTheBuild(t *testing.T) {
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

	inputs := pipeline.Inputs{
		Release:           rel,
		Manifest:          manifest,
		AggregateRawBytes: aggBytes,
		AggregatePath:     fullstackAggregatePath,
		WorkspaceConfig:   workspaceconfig.Default(),
		CompilerVersion:   fixtureCompilerVersion,
	}
	baseBinding := cat.RecipeLookupWithManifest(manifest).Recipe

	resolveWith := func(binding resolver.RecipeBinding) ([]*resolver.ResolverError, string, error) {
		var advisories []*resolver.ResolverError
		var resolveErr error
		stderr := captureStderr(t, func() {
			resolveErr = pipeline.ResolveAndValidate(context.Background(), inputs,
				wiredLookup(cat, manifest), binding,
				func(rr *resolver.ResolveResult) error {
					advisories = rr.LintAdvisories
					return nil
				},
			)
		})
		return advisories, stderr, resolveErr
	}

	// The control comes first: the fixture as authored trips the reading nowhere,
	// so a probe that reported the same thing on both arms would be measuring
	// something else.
	clean, cleanStderr, cleanErr := resolveWith(baseBinding)
	if cleanErr != nil {
		t.Fatalf("the fixture as authored must resolve: %v\nstderr:\n%s", cleanErr, cleanStderr)
	}
	for _, a := range clean {
		if strings.Contains(a.Message, extraPublishedKey) {
			t.Fatalf("the fixture as authored already names %q; the probe would be inert", extraPublishedKey)
		}
	}

	tripped, stderr, resolveErr := resolveWith(
		publishingAnExtraKey(baseBinding, "core/compute/web-ui"))

	// The half a "a diagnostic still appears" assertion cannot make: the reading
	// is advisory, so the resolve it came from must succeed.
	if resolveErr != nil {
		t.Fatalf("the publication reading must not halt the build; resolve returned %v\nstderr:\n%s",
			resolveErr, stderr)
	}

	var named []*resolver.ResolverError
	for _, a := range tripped {
		if strings.Contains(a.Message, extraPublishedKey) {
			named = append(named, a)
		}
	}
	if len(named) == 0 {
		t.Fatalf("the reading reported nothing about %q.\nadvisories: %v\nstderr:\n%s",
			extraPublishedKey, tripped, stderr)
	}
	for _, a := range named {
		if a.Field != ".spec.provides."+extraPublishedKey {
			t.Errorf("the finding is positioned at %q; want the Recipe's own entry", a.Field)
		}
		if !strings.HasPrefix(a.Entity, "Recipe ") {
			t.Errorf("the finding is attributed to %q; want the Recipe the entry is written in", a.Entity)
		}
		if !strings.Contains(a.Message, "core/compute/web-ui") {
			t.Errorf("the finding does not name the component type whose contract lacks the key: %q", a.Message)
		}
	}

	// It reaches the drain both front ends consume, on the advisory stream and not
	// the halting one.
	if !strings.Contains(stderr, "lint:") {
		t.Errorf("the advisory did not reach the advisory drain.\nstderr:\n%s", stderr)
	}
	for _, line := range strings.Split(stderr, "\n") {
		if strings.Contains(line, extraPublishedKey) && strings.Contains(line, "guard:") {
			t.Errorf("the finding reached the halting stream: %q", line)
		}
	}
}
