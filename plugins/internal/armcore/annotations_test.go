// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armcore

import "testing"

func TestPlacementAnnotations(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		annotations map[string]string
		want        placement
	}{
		{"absent", nil, placement{
			subscription:  "subscription().subscriptionId",
			resourceGroup: "resourceGroup().name",
		}},
		{"empty", map[string]string{AnnotationSubscription: "", AnnotationResourceGroup: ""}, placement{
			subscription:  "subscription().subscriptionId",
			resourceGroup: "resourceGroup().name",
		}},
		{"both given", map[string]string{
			AnnotationSubscription:  "00000000-0000-0000-0000-000000000001",
			AnnotationResourceGroup: "rg-payments",
		}, placement{
			subscription:  "'00000000-0000-0000-0000-000000000001'",
			resourceGroup: "'rg-payments'",
		}},
		{"subscription only", map[string]string{AnnotationSubscription: "sub-a"}, placement{
			subscription:  "'sub-a'",
			resourceGroup: "resourceGroup().name",
		}},
		{"resource group only, with a quote", map[string]string{AnnotationResourceGroup: "rg-o'brien"}, placement{
			subscription:  "subscription().subscriptionId",
			resourceGroup: StringLiteral("rg-o'brien"),
		}},
		{"other annotations are not read", map[string]string{AnnotationManagementGroup: "mg", "aggrega.ai/aws-region": "eu-west-1"}, placement{
			subscription:  "subscription().subscriptionId",
			resourceGroup: "resourceGroup().name",
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := placementOf(c.annotations); got != c.want {
				t.Errorf("placementOf(%v) = %+v, want %+v", c.annotations, got, c.want)
			}
		})
	}
	if got := placementOf(map[string]string{AnnotationResourceGroup: "rg-o'brien"}).resourceGroup; got != "'rg-o''brien'" {
		t.Errorf("a quote inside a given resource group is written %s, want it doubled inside the literal", got)
	}
}
