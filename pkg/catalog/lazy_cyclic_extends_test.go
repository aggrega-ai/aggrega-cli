// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

const cyclicExtendsCompAYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/loop/cyc-a
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  extends: core/loop/cyc-b
  schema: inherited
`

const cyclicExtendsCompBYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/loop/cyc-b
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  extends: core/loop/cyc-a
  schema: inherited
`

const cyclicExtendsFeatAYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: core/loop/feat-a
  version: "1.0.0"
  labels:
    feature.aggrega.ai/type: policy
spec:
  extends: core/loop/feat-b
`

const cyclicExtendsFeatBYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: core/loop/feat-b
  version: "1.0.0"
  labels:
    feature.aggrega.ai/type: policy
spec:
  extends: core/loop/feat-a
`

// mergedComponentChainFor reads uri through the Component cascade and merges it
// along its extends chain through the same lock-free core the merged accessor
// runs, reporting whether the cascade found it and what the merge refused.
func mergedComponentChainFor(l *LazyLookup, uri string) (bool, []*validator.ValidationError) {
	l.mu.Lock()
	defer l.mu.Unlock()
	ctx := context.Background()
	comp, ok := l.resolveComponentContext(ctx, uri, l.registryTier(true))
	if !ok {
		return false, nil
	}
	_, errs := l.mergeComponentChain(ctx, uri, comp)
	return true, errs
}

// mergedFeatureChainFor is mergedComponentChainFor for the Feature cascade.
func mergedFeatureChainFor(l *LazyLookup, uri string) (bool, []*validator.ValidationError) {
	l.mu.Lock()
	defer l.mu.Unlock()
	ctx := context.Background()
	feat, ok := l.resolveFeatureContext(ctx, uri, l.registryTier(true))
	if !ok {
		return false, nil
	}
	_, errs := l.mergeFeatureChain(ctx, uri, feat)
	return true, errs
}

// TestLazyLookupCircularExtendsChainReportsTheCycle drives the merged lazy
// accessor over a two-entity extends loop, for a Component and for a Feature,
// and asserts the loop is reported as the cyclic extends chain rather than
// recursing without end. The merge reads a chain's parents through the accessor
// itself, and the accessor remembers an entity only once its merge returns, so
// without the in-progress answer each read starts a fresh merge and the test
// binary dies of a stack overflow instead of failing an assertion — a crashed
// test binary is this test's red.
//
// An acyclic chain of each kind is the positive control: the same calls merge it
// with no failure, so a cycle row cannot pass on a lookup that refuses
// everything.
func TestLazyLookupCircularExtendsChainReportsTheCycle(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		files     [][2]string
		uri       string
		read      func(l *LazyLookup, uri string) bool
		merge     func(l *LazyLookup, uri string) (bool, []*validator.ValidationError)
		wantCycle bool
	}{
		{
			name:  "component loop",
			files: [][2]string{{"cyc-a.yaml", cyclicExtendsCompAYAML}, {"cyc-b.yaml", cyclicExtendsCompBYAML}},
			uri:   "core/loop/cyc-a",
			read: func(l *LazyLookup, uri string) bool {
				_, ok := l.Component(uri)
				return ok
			},
			merge:     mergedComponentChainFor,
			wantCycle: true,
		},
		{
			name:  "feature loop",
			files: [][2]string{{"feat-a.yaml", cyclicExtendsFeatAYAML}, {"feat-b.yaml", cyclicExtendsFeatBYAML}},
			uri:   "core/loop/feat-a",
			read: func(l *LazyLookup, uri string) bool {
				_, ok := l.Feature(uri)
				return ok
			},
			merge:     mergedFeatureChainFor,
			wantCycle: true,
		},
		{
			name:  "acyclic component chain",
			files: [][2]string{{"parent.yaml", extendsParentCompYAML}, {"child.yaml", extendsChildCompYAML}},
			uri:   "core/test/child-comp",
			read: func(l *LazyLookup, uri string) bool {
				_, ok := l.Component(uri)
				return ok
			},
			merge: mergedComponentChainFor,
		},
		{
			name:  "acyclic feature chain",
			files: [][2]string{{"parent.yaml", extendsParentFeatYAML}, {"child.yaml", extendsChildFeatYAML}},
			uri:   "core/test/child-feat",
			read: func(l *LazyLookup, uri string) bool {
				_, ok := l.Feature(uri)
				return ok
			},
			merge: mergedFeatureChainFor,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ws := t.TempDir()
			cd := filepath.Join(ws, "catalog")
			for _, f := range tt.files {
				writeCatalogFile(t, cd, f[0], f[1])
			}

			// The exported accessor returns at all: before the in-progress answer
			// it never did on a loop.
			if !tt.read(NewLazyLookup(ws, "", []string{cd}, nil), tt.uri) {
				t.Fatalf("the merged accessor must find %q", tt.uri)
			}

			found, errs := tt.merge(NewLazyLookup(ws, "", []string{cd}, nil), tt.uri)
			if !found {
				t.Fatalf("the cascade must find %q", tt.uri)
			}
			if !tt.wantCycle {
				if len(errs) != 0 {
					t.Fatalf("an acyclic chain must merge without failure; got %v", errs)
				}
				return
			}
			if len(errs) == 0 {
				t.Fatal("a looping chain must be refused, not merged")
			}
			if !errors.Is(errs[0], validator.ErrCyclicExtension()) {
				t.Errorf("a looping chain must be reported as the cyclic extends chain; got %v", errs[0])
			}
		})
	}
}
