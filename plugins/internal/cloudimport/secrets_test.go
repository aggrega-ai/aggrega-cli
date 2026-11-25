// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
)

// vaultSecretID is the resource ID of a Key Vault secret a parameters file
// gives a reference to.
const vaultSecretID = "/subscriptions/s/resourceGroups/g/providers/Microsoft.KeyVault/vaults/v/secrets/db-admin"

// passwordServer is the server reading its administrator's password from the
// template parameter name.
func passwordServer(name string) *Node {
	return mappedServer(leaf(Param{Name: name}, pathPassword...))
}

// TestSecretFromAKeyVaultReferenceIsReferenced attaches a core/referenced-secret
// with the secret's resource ID as its origin where the parameters file gives a
// Key Vault reference, and the Manifest maps the type to its realization.
func TestSecretFromAKeyVaultReferenceIsReferenced(t *testing.T) {
	in := edgeView()
	in.Target = "arm"
	g := &Graph{Nodes: []*Node{passwordServer("adminPassword")}, Params: []Parameter{
		{Name: "adminPassword", Type: "string", Secure: true, StoreRef: vaultSecretID}}}
	_, _, release, manifest, diags := released(t, in, releaseFormat(), g)
	wantBlock(t, release, "server", `    server:
      infra:
        location: "westeurope"
      features:
        admin_password:
          type: core/referenced-secret
          infra:
            # Attached by reference, never as a value: the template parameter adminPassword, at the locator the source states.
            origin: "`+vaultSecretID+`"
`)
	if !strings.Contains(manifest, "core/referenced-secret: arm/key-vault-secret") {
		t.Errorf("the Manifest does not map the secret's type to its realization:\n%s", manifest)
	}
	onlyCode(t, diags, CodeValueNotCarried, 0)
}

// TestSecretWithoutAStoreReferenceTakesTheTargetsForm attaches the form the kit
// gives the import's target: on arm a referenced secret whose origin the
// Release owes, elsewhere the variable reference.
func TestSecretWithoutAStoreReferenceTakesTheTargetsForm(t *testing.T) {
	g := func() *Graph {
		return &Graph{Nodes: []*Node{passwordServer("adminPassword")}, Params: []Parameter{
			{Name: "adminPassword", Type: "string", Secure: true}}}
	}
	in := edgeView()
	in.Target = "arm"
	_, _, release, _, _ := released(t, in, releaseFormat(), g())
	block := instanceBlock(t, release, "server")
	for _, want := range []string{"type: core/referenced-secret", `origin: ""`, "the template parameter adminPassword"} {
		if !strings.Contains(block, want) {
			t.Errorf("on arm the entry lacks %q:\n%s", want, block)
		}
	}
	in.Target = "compose"
	_, _, release, _, _ = released(t, in, releaseFormat(), g())
	block = instanceBlock(t, release, "server")
	for _, want := range []string{"type: core/secret-value", `origin: "${adminPassword}"`} {
		if !strings.Contains(block, want) {
			t.Errorf("on compose the entry lacks %q:\n%s", want, block)
		}
	}
}

// TestSecretLiteralDefaultIsADecisionNamedInAWarning judges a literal a secure
// parameter's default and a parameters file's value hold through the
// empty_credentials setting, naming it in a warning that never shows it, and
// writes it nowhere.
func TestSecretLiteralDefaultIsADecisionNamedInAWarning(t *testing.T) {
	const literalSecret = "hunter2-literal"
	for _, c := range []struct {
		setting, code string
		param         Parameter
	}{
		{"marked", "credential-emptied", Parameter{Default: text(literalSecret)}},
		{"none", "credential-carried", Parameter{Default: text(literalSecret)}},
		{"marked", "credential-emptied", Parameter{Supplied: text(literalSecret)}},
	} {
		in := edgeView()
		in.Target = "arm"
		in.Settings = json.RawMessage(`{"empty_credentials": "` + c.setting + `"}`)
		c.param.Name, c.param.Type, c.param.Secure = "adminPassword", "string", true
		g := &Graph{Nodes: []*Node{passwordServer("adminPassword")}, Params: []Parameter{c.param}}
		_, aggregate, release, _, diags := released(t, in, releaseFormat(), g)
		got := 0
		for _, d := range diags {
			if d.Code == c.code {
				got++
				if !strings.Contains(d.Message, "adminPassword") {
					t.Errorf("the %s warning does not name the parameter: %s", c.code, d.Message)
				}
			}
			if strings.Contains(d.Message, literalSecret) {
				t.Errorf("a diagnostic shows the value: %+v", d)
			}
		}
		if got != 1 {
			t.Errorf("under %s: %d diagnostics of %s, want one in %+v", c.setting, got, c.code, diags)
		}
		if strings.Contains(aggregate+release, literalSecret) {
			t.Errorf("a draft writes the value:\n%s\n%s", aggregate, release)
		}
	}
}

// TestSecretNewGuidDefaultWarnsThatNoSecretIsGenerated attaches the secret by
// its form and warns that the template generates no secret where the default
// is newGuid().
func TestSecretNewGuidDefaultWarnsThatNoSecretIsGenerated(t *testing.T) {
	in := edgeView()
	in.Target = "arm"
	g := &Graph{Nodes: []*Node{passwordServer("adminPassword")}, Params: []Parameter{
		{Name: "adminPassword", Type: "string", Secure: true, Default: Opaque{Text: "[newGuid()]", Target: "arm"}}}}
	_, _, release, _, diags := released(t, in, releaseFormat(), g)
	warnsAbout(t, diags, CodeValueNotCarried, "adminPassword", "newGuid()", "generates no secret")
	if !strings.Contains(instanceBlock(t, release, "server"), "type: core/referenced-secret") {
		t.Errorf("the secret is not attached:\n%s", release)
	}
}

// TestSecretOfADraftedNodeStaysADeployTimeParameter attaches no secret to a
// resource drafted from the source: the entry's comment names the deploy-time
// parameter its Recipe declares and, where the parameters file gave a Key Vault
// reference, says to pass that reference in the parameters file the template is deployed with.
func TestSecretOfADraftedNodeStaysADeployTimeParameter(t *testing.T) {
	in := edgeView()
	in.Target = "arm"
	site := staticSite(leaf(Param{Name: "adminPassword"}, "properties", "adminPassword"))
	g := &Graph{Nodes: []*Node{site}, Params: []Parameter{
		{Name: "adminPassword", Type: "string", Secure: true, StoreRef: vaultSecretID}}}
	_, _, release, _, _ := released(t, in, edgeFormat(nil), g)
	block := release
	for _, want := range []string{"deploy-time parameter SiteAdminPassword", "Key Vault reference",
		"parameters file"} {
		if !strings.Contains(block, want) {
			t.Errorf("the entry's comment lacks %q:\n%s", want, block)
		}
	}
	if strings.Contains(block, "features:") || strings.Contains(release, vaultSecretID) || !strings.Contains(block, "site: {}") {
		t.Errorf("the entry attaches the secret:\n%s", release)
	}
	_ = schemadraft.PillarSecret
}

// TestSecretReadInsideAnOpaqueExpressionIsNotWritten leaves the key for the
// answers file, with a warning naming the parameter, where deploy-time text
// reads a secure parameter, in the Release and in the Aggregate's values.
func TestSecretReadInsideAnOpaqueExpressionIsNotWritten(t *testing.T) {
	in := edgeView()
	in.Target = "arm"
	concat := Opaque{Text: "[concat('Server=x;Password=', parameters('adminPassword'))]", Target: "arm"}
	s := mappedServer(leaf(concat, pathSkuName...), leaf(concat, "properties", "region"))
	g := &Graph{Nodes: []*Node{s}, Params: []Parameter{{Name: "adminPassword", Type: "string", Secure: true}}}
	f := valuesServerFormat()
	_, aggregate, release, _, diags := released(t, in, f, g)
	if strings.Contains(aggregate+release, "Password=") {
		t.Errorf("a draft writes the text:\n%s\n%s", aggregate, release)
	}
	if !strings.Contains(release, "          # Left") && !strings.Contains(release, "sku: \"\"") {
		t.Errorf("the Release does not leave infra.sku:\n%s", release)
	}
	warnsAbout(t, diags, CodeValueNotCarried, "adminPassword", "infra.sku")
	warnsAbout(t, diags, CodeValueNotCarried, "adminPassword", "values.region")
}

// TestSecretCredentialFlowNamesTheFormOfTheImportsTarget names the type the
// referenced secret takes in the credential-flow warning whatever target the
// import drafts for.
func TestSecretCredentialFlowNamesTheFormOfTheImportsTarget(t *testing.T) {
	for _, target := range []string{"arm", "compose", ""} {
		in := edgeView()
		in.Target = target
		a := app(leaf(Credential{Node: "storage", What: "listKeys"}, "properties", "configuration", "secrets", "0", "value"))
		storage := &Node{ID: "storage", Type: "Microsoft.Storage/storageAccounts", Form: FormRaw, Name: text("st")}
		_, _, release, _, diags := released(t, in, edgeFormat(nil), &Graph{Nodes: []*Node{a, storage}})
		warnsAbout(t, diags, CodeCredentialFlow, "core/referenced-secret", "storage_key")
		if !strings.Contains(release, "type: core/referenced-secret") {
			t.Errorf("target %q: the Release attaches no referenced secret:\n%s", target, release)
		}
	}
}
