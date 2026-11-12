// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// pullAdvice is the advice a missing extends parent carries where a pull changes
// what the resolution reads.
const pullAdvice = "a base published to a registry is fetched by `aggrega pull --release <release>`"

// TestTheMissingParentAdviceNamesThePullOnlyWhereThePullHelps drives one parent no
// source holds through the two whole-catalog resolutions. The one that reads
// catalog directories alone reports it without naming the pull, because a pull that
// succeeds changes nothing it reads; the one that reads what a pull pins reports it
// naming the pull. Both report the same condition and the same missing link, so the
// advice is the only difference between them.
func TestTheMissingParentAdviceNamesThePullOnlyWhereThePullHelps(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"component", "feature"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			build := func() *Catalog {
				if kind == "component" {
					return ancestryCatalog([]types.Component{ancestryComponent("core/child", "1.0.0", "core/absent:1.0.0")})
				}
				c := &Catalog{Components: map[string]types.Component{}, Features: map[string]types.Feature{}}
				feat := types.Feature{}
				feat.APIVersion = "aggrega.ai/v1beta1"
				feat.Kind = "Feature"
				feat.Metadata.Name = "core/child"
				feat.Metadata.Version = "1.0.0"
				feat.Spec.Extends = "core/absent:1.0.0"
				c.Features["core/child:1.0.0"] = feat
				return c
			}
			for _, tc := range []struct {
				name       string
				resolve    func(*Catalog) []*validator.ValidationError
				wantAdvice bool
			}{
				{"over the catalog directories alone", func(c *Catalog) []*validator.ValidationError {
					return c.ResolveAllExtends()
				}, false},
				{"reading what a pull pins", func(c *Catalog) []*validator.ValidationError {
					return c.ResolveAllExtendsWithPins(t.Context(), t.TempDir(), ".aggrega/cache", nil, nil, nil)
				}, true},
			} {
				errs := tc.resolve(build())
				if len(errs) != 1 {
					t.Fatalf("%s: want one extends failure, got %d: %v", tc.name, len(errs), errs)
				}
				if !errors.Is(errs[0], validator.ErrInheritanceMerge()) {
					t.Errorf("%s: failure = %v; want the inheritance-merge condition", tc.name, errs[0])
				}
				if !strings.Contains(errs[0].Error(), "core/child extends core/absent:1.0.0, which no catalog entity provides") {
					t.Errorf("%s: failure %q does not name the missing link", tc.name, errs[0].Error())
				}
				if got := strings.Contains(errs[0].Error(), pullAdvice); got != tc.wantAdvice {
					t.Errorf("%s: failure %q carries the pull advice = %v; want %v", tc.name, errs[0].Error(), got, tc.wantAdvice)
				}
			}
		})
	}
}
