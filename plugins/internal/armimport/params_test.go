// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armimport

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
)

// param returns the parameter of g named name, failing where there is none.
func param(t *testing.T, g *cloudimport.Graph, name string) cloudimport.Parameter {
	t.Helper()
	for _, p := range g.Params {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("the graph declares no parameter %s", name)
	return cloudimport.Parameter{}
}

// TestParamsReadTheirDeclarations reads the topology's parameters: each with
// its type — a secure string read as a string marked secure — its
// description, its allowed values, its bounds and its default, an expression
// default lowered.
func TestParamsReadTheirDeclarations(t *testing.T) {
	t.Parallel()
	g, diags := readAll("", src("main.json", fixture(t, "topology/one.json")))
	if g == nil || len(diags) != 0 {
		t.Fatalf("diagnostics %+v", diags)
	}
	var names []string
	for _, p := range g.Params {
		names = append(names, p.Name)
	}
	if want := "administratorPassword deployStorage environmentId location serverName skuName"; strings.Join(names, " ") != want {
		t.Errorf("parameters %v, want %s in key order", names, want)
	}
	server := param(t, g, "serverName")
	if server.Type != "string" || server.Description != "The name of the PostgreSQL flexible server." ||
		!reflect.DeepEqual(server.Default, str("pg-main")) || server.MinLength == nil || *server.MinLength != 3 ||
		server.MaxLength == nil || *server.MaxLength != 63 || server.Secure {
		t.Errorf("serverName reads %+v", server)
	}
	if p := param(t, g, "administratorPassword"); p.Type != "string" || !p.Secure || p.Default != nil {
		t.Errorf("administratorPassword reads %+v", p)
	}
	if p := param(t, g, "location"); !reflect.DeepEqual(p.Default, cloudimport.Opaque{Text: "[resourceGroup().location]",
		Target: "arm"}) {
		t.Errorf("location's default reads %+v", p.Default)
	}
	if p := param(t, g, "skuName"); !reflect.DeepEqual(p.Allowed, []json.RawMessage{
		json.RawMessage(`"Standard_B1ms"`), json.RawMessage(`"Standard_D2s_v3"`)}) {
		t.Errorf("skuName allows %s", p.Allowed)
	}
	if p := param(t, g, "deployStorage"); p.Type != "bool" || !reflect.DeepEqual(p.Default, lit("true")) {
		t.Errorf("deployStorage reads %+v", p)
	}
}

// TestParamsBoundsAndTypeDefinitions reads numeric bounds, a secure object,
// and a type a 2.0 parameter takes from a type definition.
func TestParamsBoundsAndTypeDefinitions(t *testing.T) {
	t.Parallel()
	g, diags := readTemplate(`{"$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
		"languageVersion": "2.0",
		"definitions": {"sizeType": {"$ref": "#/definitions/countType"}, "countType": {"type": "int"}},
		"parameters": {"size": {"$ref": "#/definitions/sizeType", "minValue": 1, "maxValue": 10},
			"settings": {"type": "secureObject"}},
		"resources": {}}`)
	if g == nil || len(diags) != 0 {
		t.Fatalf("diagnostics %+v", diags)
	}
	size := param(t, g, "size")
	if size.Type != "int" || size.Min == nil || *size.Min != "1" || size.Max == nil || *size.Max != "10" {
		t.Errorf("size reads %+v", size)
	}
	if p := param(t, g, "settings"); p.Type != "object" || !p.Secure {
		t.Errorf("settings reads %+v", p)
	}
}

// TestParamsFileSuppliesValuesAndKeyVaultReferences reads the topology with
// its parameters file: a parameter the file gives a value is supplied it, and
// one the file gives a Key Vault reference holds the secret's resource ID as
// its store reference.
func TestParamsFileSuppliesValuesAndKeyVaultReferences(t *testing.T) {
	t.Parallel()
	g, diags := readAll("", src("main.json", fixture(t, "topology/one.json")),
		src("main.parameters.json", fixture(t, "parameters/one.parameters.json")))
	if g == nil || len(diags) != 0 {
		t.Fatalf("diagnostics %+v", diags)
	}
	if p := param(t, g, "serverName"); !reflect.DeepEqual(p.Supplied, lit(`"pg-prod"`)) || p.StoreRef != "" {
		t.Errorf("serverName reads %+v", p)
	}
	want := "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg-secrets/providers/" +
		"Microsoft.KeyVault/vaults/kv-prod/secrets/pg-admin-password"
	if p := param(t, g, "administratorPassword"); p.StoreRef != want || p.Supplied != nil {
		t.Errorf("administratorPassword reads %+v, want the store reference %s", p, want)
	}
	if p := param(t, g, "location"); p.Supplied != nil || p.StoreRef != "" {
		t.Errorf("location, which the file does not set, reads %+v", p)
	}
}

// TestParamsFileEntriesNotReadWarn reads parameters files each holding one
// entry the import does not read: a parameter the template does not declare,
// one given neither a value nor a reference, and a reference pinning a secret
// version, which is read without its version. Each warns value-not-carried
// naming the parameter.
func TestParamsFileEntriesNotReadWarn(t *testing.T) {
	t.Parallel()
	vault := `{"keyVault": {"id": "/subscriptions/0/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/kv"}, ` +
		`"secretName": "pw"`
	for _, row := range []struct{ entry, says, storeRef string }{
		{`"unknown": {"value": 1}`, "sets the parameter unknown, which the template declares no parameter of", ""},
		{`"administratorPassword": {"reference": {"secretName": "pw"}}`,
			"sets the parameter administratorPassword to neither a value nor a Key Vault reference", ""},
		{`"administratorPassword": {"reference": ` + vault + `, "secretVersion": "0123"}}`,
			"reads the parameter administratorPassword from a version of a Key Vault secret it pins",
			"/subscriptions/0/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/kv/secrets/pw"},
	} {
		file := `{"$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
			"parameters": {` + row.entry + `}}`
		g, diags := readAll("", src("main.json", fixture(t, "topology/one.json")), src("p.json", []byte(file)))
		if g == nil || len(diags) != 1 || diags[0].Code != "value-not-carried" || diags[0].Severity != "warn" ||
			!strings.HasPrefix(diags[0].Message, "the parameters file p.json "+row.says) {
			t.Errorf("%s: diagnostics %+v", row.entry, diags)
			continue
		}
		if p := param(t, g, "administratorPassword"); p.StoreRef != row.storeRef {
			t.Errorf("%s: the store reference is %q", row.entry, p.StoreRef)
		}
	}
}

// TestParamsOutputsAreLowered reads the topology's output as the reference it
// reads.
func TestParamsOutputsAreLowered(t *testing.T) {
	t.Parallel()
	g, _ := readAll("", src("main.json", fixture(t, "topology/one.json")))
	want := []cloudimport.Output{{Name: "serverAddress", Value: cloudimport.Ref{Node: "flexibleservers",
		Attr: []string{"properties", "fullyQualifiedDomainName"}}}}
	if g == nil || !reflect.DeepEqual(g.Outputs, want) {
		t.Fatalf("outputs %+v, want %+v", g, want)
	}
}
