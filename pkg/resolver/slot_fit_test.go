// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestFeatureFitsSlot pins the two questions a slot's label constraint answers
// and where they part: whether a Feature may fill the slot, and whether a Feature
// is evidently meant for it. They agree wherever the slot declares a constraint
// the matcher can evaluate, and part on the two shapes that carry no evidence — a
// slot declaring no constraint, which admits anything and is fitted by nothing,
// and a constraint the matcher cannot parse, which is an error to the first
// question and no fit to the second.
func TestFeatureFitsSlot(t *testing.T) {
	t.Parallel()

	secret := types.OrderedMap[string]{}
	secret.Set("feature.aggrega.ai/kind", "secret")
	unlabelled := types.OrderedMap[string]{}

	rows := []struct {
		name       string
		constraint string
		labels     types.OrderedMap[string]
		wantAdmits bool
		wantErr    bool
		wantFits   bool
	}{
		{
			name:       "a constraint the Feature satisfies is admitted and fitted",
			constraint: "feature.aggrega.ai/kind=secret",
			labels:     secret,
			wantAdmits: true,
			wantFits:   true,
		},
		{
			name:       "a constraint the Feature fails is neither",
			constraint: "feature.aggrega.ai/kind=secret",
			labels:     unlabelled,
		},
		{
			name:       "no constraint admits any Feature and is fitted by none",
			constraint: "",
			labels:     secret,
			wantAdmits: true,
		},
		{
			name:       "a constraint the matcher cannot parse is an error and fitted by none",
			constraint: "!",
			labels:     secret,
			wantErr:    true,
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			admits, err := SlotSelectorAdmits(row.constraint, row.labels)
			if (err != nil) != row.wantErr {
				t.Fatalf("SlotSelectorAdmits error = %v, want error %v", err, row.wantErr)
			}
			if err == nil && admits != row.wantAdmits {
				t.Errorf("SlotSelectorAdmits = %v, want %v", admits, row.wantAdmits)
			}
			if fits := FeatureFitsSlot(row.constraint, row.labels); fits != row.wantFits {
				t.Errorf("FeatureFitsSlot = %v, want %v", fits, row.wantFits)
			}
		})
	}
}

// TestRunAdvisories_AttachmentNearMissStepsAsideForAFittingFeature asserts the
// near-miss reading pairs a name with a declared slot only where the attached
// Feature does not fit that slot. A Feature that fits is a fill written under
// another name, judged by the halting rule once every scope has folded, so the
// reading staying silent there is what keeps one attachment from drawing an
// advisory and a halt at once. Each silent row is paired with a reporting row
// that differs from it in one fact.
func TestRunAdvisories_AttachmentNearMissStepsAsideForAFittingFeature(t *testing.T) {
	t.Parallel()

	catalogWith := func(selector string, featureLabels types.OrderedMap[string]) CatalogLookup {
		return CatalogLookup{
			Component: func(typeURI string) (types.Component, bool) {
				if typeURI != "core/datastore/db" {
					return types.Component{}, false
				}
				slots := types.OrderedMap[types.AttachmentSlot]{}
				slots.Set("db_password", types.AttachmentSlot{Selector: selector})
				return types.Component{
					Metadata: types.ComponentMetadata{Name: "db", Version: "1.0.0"},
					Spec:     types.ComponentSpec{FeaturesSchema: slots},
				}, true
			},
			Feature: func(typeURI string) (types.Feature, bool) {
				if typeURI != "core/some-feature" {
					return types.Feature{}, false
				}
				return types.Feature{
					Metadata: types.FeatureMetadata{Name: "core/some-feature", Version: "1.0.0", Labels: featureLabels},
				}, true
			},
		}
	}
	secret := types.OrderedMap[string]{}
	secret.Set("feature.aggrega.ai/kind", "secret")
	unlabelled := types.OrderedMap[string]{}

	rows := []struct {
		name       string
		catalog    CatalogLookup
		wantFields []string
	}{
		{
			name:       "a Feature fitting the near slot draws no advisory",
			catalog:    catalogWith("feature.aggrega.ai/kind=secret", secret),
			wantFields: nil,
		},
		{
			name:       "a Feature failing the near slot's constraint draws the advisory",
			catalog:    catalogWith("feature.aggrega.ai/kind=secret", unlabelled),
			wantFields: []string{".spec.components.db.features.db_passwrod"},
		},
		{
			name:       "a near slot declaring no constraint is fitted by nothing and draws the advisory",
			catalog:    catalogWith("", secret),
			wantFields: []string{".spec.components.db.features.db_passwrod"},
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			got := advisoriesOf(t, aggWithAttachments(t, []string{"db_passwrod"}), row.catalog)
			if gotFields := fieldsOf(got); !equalStrings(gotFields, row.wantFields) {
				t.Fatalf("locators = %v; want %v (advisories: %v)", gotFields, row.wantFields, got)
			}
		})
	}
}
