// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"strings"
	"testing"
)

// TestPointerOfAMappedInstanceIsLeftForTheOperator leaves a mapped instance's
// key reading a substrate or existing resource for the answers file, on its own
// target and on any other alike: its catalog Recipe takes the resource's
// literal identifier, so the comment names the resource, its type and the text
// the format's pointer gives for it, which is never written as the value;
// deploy-time text is written only where its target is the import's.
func TestPointerOfAMappedInstanceIsLeftForTheOperator(t *testing.T) {
	s := mappedServer(leaf(Ref{Node: "subnet"}, pathSubnetRef...),
		leaf(Opaque{Text: "[resourceGroup().location]", Target: "arm"}, pathSkuName...),
		leaf(Ref{Node: "vault", Attr: []string{"id"}}, pathStorage...))
	vault := &Node{ID: "vault", Type: vaultType, Form: FormRaw, Existing: true, Name: text("kv-shared")}
	g := func() *Graph { return &Graph{Nodes: []*Node{s, subnetNode(), vault}} }
	subnet := `          # Left for the Release, since the source cannot supply it: infra.overrides.delegated_subnet_id, the pointer to the resource subnet (Microsoft.Network/virtualNetworks/subnets), which the draft leaves to the operator and the source names as "` + subnetPointer + `"; the catalog Recipe takes the resource's literal identifier: write it
          delegated_subnet_id: ""
`
	storage := `          # Left for the Release, since the source cannot supply it: infra.overrides.storage_size_gb, the pointer to the resource vault (Microsoft.KeyVault/vaults), which the draft leaves to the operator; the catalog Recipe takes the resource's literal identifier: write it
          storage_size_gb: ""
`

	in := edgeView()
	in.Target = "arm"
	_, _, release, _, _ := released(t, in, releaseFormat(), g())
	wantBlock(t, release, "server", `    server:
      infra:
        location: "westeurope"
        sku: "[resourceGroup().location]"
        overrides:
`+storage+subnet)

	in.Target = "terraform"
	_, _, release, _, _ = released(t, in, releaseFormat(), g())
	wantBlock(t, release, "server", `    server:
      infra:
        location: "westeurope"
        # Left for the Release, since the source cannot supply it: infra.sku, it is text the arm target evaluates when the source deploys, and the import drafts for the terraform target
        sku: ""
        overrides:
`+storage+subnet)
}

// TestPointerEnvEntryOfAMappedInstanceIsNamed warns reference-not-wired where an
// environment entry of a mapped instance reads a resource the Release points
// at: the Recipe takes its environment only through inject, so no Release key
// holds the pointer, and no connection is drawn.
func TestPointerEnvEntryOfAMappedInstanceIsNamed(t *testing.T) {
	in := edgeView()
	in.Target = "arm"
	vault := &Node{ID: "vault", Type: vaultType, Form: FormRaw, Existing: true, Name: text("kv-shared")}
	a := app(envOf(envEntry("VAULT_URI", Ref{Node: "vault", Attr: []string{"properties", "vaultUri"}})))
	_, aggregate, _, _, diags := released(t, in, releaseFormat(), &Graph{Nodes: []*Node{a, vault}})
	if strings.Contains(aggregate, "uses:") {
		t.Errorf("the Aggregate draws a connection:\n%s", aggregate)
	}
	warnsAbout(t, diags, CodeReferenceNotWired, "app", "VAULT_URI", "vault", "only through inject",
		"no Release key holds the pointer")
	onlyCode(t, diags, CodeReferenceNotWired, 1)
}
