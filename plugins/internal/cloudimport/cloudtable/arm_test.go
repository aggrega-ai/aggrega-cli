// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
)

// symbolLocal is the locals entry holding the instance's symbol, as the hub's
// arm Recipes declare it.
const symbolLocal = "{{ capitalizedWords .context.instance_id }}"

// fragmentOf writes a fragment whose resources block holds one resource keyed
// key, its lines body indented beneath it.
func fragmentOf(key, body string) string {
	var b strings.Builder
	b.WriteString("resources:\n  " + key + ":\n")
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		b.WriteString("    " + line + "\n")
	}
	return b.String()
}

// TestAResourceKeyedByTheSymbolIsReadFromItsTypeLine reads the resource a
// fragment keys by the instance's symbol, spelled as the provider folds the
// instance's name, as the locals entry holding that, and as a variable the
// fragment assigns either to: each gives the resource's literal type as the
// key. A locals entry holding anything else is not the symbol, a resource the
// fragment reads as existing is not the instance, and a key the symbol only
// begins is another resource; none gives a row.
func TestAResourceKeyedByTheSymbolIsReadFromItsTypeLine(t *testing.T) {
	t.Parallel()
	body := "type: Microsoft.Acme/stores\nname: \"{{ .context.instance_id }}\"\n"
	for _, c := range []struct {
		name, fragment string
		locals         map[string]string
		want           string
	}{
		{"the folded name", fragmentOf("{{ capitalizedWords .context.instance_id }}", body), nil,
			"Microsoft.Acme/stores"},
		{"the locals entry", fragmentOf("{{ .locals.symbol }}", body), map[string]string{"symbol": symbolLocal},
			"Microsoft.Acme/stores"},
		{"a variable", "{{- $sym := .locals.symbol }}\n" + fragmentOf("{{ $sym }}", body),
			map[string]string{"symbol": symbolLocal}, "Microsoft.Acme/stores"},
		{"a variable of the folded name", "{{- $s := capitalizedWords .context.instance_id }}\n" +
			fragmentOf("{{ $s }}", body), nil, "Microsoft.Acme/stores"},
		{"a locals entry holding another name", fragmentOf("{{ .locals.symbol }}", body),
			map[string]string{"symbol": "{{ .context.instance_id }}"}, ""},
		{"an existing resource", fragmentOf("{{ capitalizedWords .context.instance_id }}", "existing: true\n"+body),
			nil, ""},
		{"a key the symbol begins", fragmentOf("{{ capitalizedWords .context.instance_id }}Api", body), nil, ""},
	} {
		row, why := extractARM(recipeSource{name: "r", locals: c.locals, fragments: []string{c.fragment}})
		if row.Key != c.want {
			t.Errorf("%s: the key is %q (%s), want %q", c.name, row.Key, why, c.want)
		}
		if c.want == "" && why == "" {
			t.Errorf("%s: no row and no reason why", c.name)
		}
		if c.want != "" && row.Form != cloudimport.FormRaw {
			t.Errorf("%s: the row's form is %q, want raw", c.name, row.Form)
		}
	}
}

// TestADictBuiltResourceGivesItsKeyAndKindAlone reads fragments that build
// their resource with dict and write it with toJson: the one resource type
// the fragment builds that is not the resource wrapping its template, a child of
// another, a resource read as existing or a relative child type is the key,
// and the literal kind its dict call sets the When. Its whole values, its
// provides and the overrides header give nothing. A fragment building two
// resources of unrelated types gives no row.
func TestADictBuiltResourceGivesItsKeyAndKindAlone(t *testing.T) {
	t.Parallel()
	fragment := "{{- $sym := .locals.symbol }}\n" +
		"{{- $site := dict \"type\" \"Microsoft.Web/sites\" \"kind\" \"app,linux\" \"identity\" (dict \"type\" \"UserAssigned\") \"properties\" (dict \"tier\" .values.tier) }}\n" +
		"{{- $child := dict \"type\" \"Microsoft.Web/sites/config\" \"name\" \"web\" }}\n" +
		"{{- $rel := dict \"type\" \"slots\" }}\n" +
		"{{- $plan := dict \"existing\" true \"type\" \"Microsoft.Web/serverfarms\" }}\n" +
		"{{- $nested := dict \"type\" \"Microsoft.Resources/deployments\" \"properties\" (dict \"template\" (dict \"resources\" (list $site $child $rel $plan))) }}\n" +
		"resources:\n  {{ $sym }}: {{ toJson $nested }}\n"
	r := recipeSource{name: "r", locals: map[string]string{"symbol": symbolLocal}, fragments: []string{fragment},
		provides: []entry{{"host", "[reference('{{ .locals.symbol }}').outputs.host.value]"}},
		document: "spec:\n  # This Recipe reads from infra.overrides:\n  #   plan_id - string (properties.serverFarmId)\n"}
	row, why := extractARM(r)
	want := cloudimport.Row{Key: "Microsoft.Web/sites", Form: cloudimport.FormRaw,
		When: []cloudimport.Pred{{Path: []string{"kind"}, Op: cloudimport.OpEquals, Value: "app,linux"}}}
	if !reflect.DeepEqual(row, want) {
		t.Errorf("the dict-built fragment gave %#v (%s), want %#v", row, why, want)
	}

	two := "{{- $a := dict \"type\" \"Microsoft.Web/sites\" }}\n{{- $b := dict \"type\" \"Microsoft.Storage/storageAccounts\" }}\n" +
		"resources:\n  {{ .locals.symbol }}: {{ toJson (dict \"type\" \"Microsoft.Resources/deployments\" \"properties\" (dict \"template\" (dict \"resources\" (list $a $b)))) }}\n"
	row, why = extractARM(recipeSource{name: "r", locals: map[string]string{"symbol": symbolLocal},
		fragments: []string{two}})
	if row.Key != "" || !strings.Contains(why, "Microsoft.Storage/storageAccounts") {
		t.Errorf("a fragment building two unrelated resources gave %q (%s), want no row naming both", row.Key, why)
	}
}

// TestAModuleIsKnownByItsAzureVerifiedModulesIdentity reads the module a Recipe
// sources by its identity: the module_subdir a git-repository source names
// under avm/res/, and the path an oci reference to the Azure Verified Modules
// registry names. A git-repository source naming another directory, and an oci
// reference to another registry, give no row.
func TestAModuleIsKnownByItsAzureVerifiedModulesIdentity(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		source map[string]any
		want   string
	}{
		{"git-repository", map[string]any{"kind": "git-repository", "url": "https://github.com/Azure/bicep-registry-modules.git",
			"ref": "avm/res/db-for-postgre-sql/flexible-server/0.16.2", "module_subdir": "avm/res/db-for-postgre-sql/flexible-server"},
			"avm/res/db-for-postgre-sql/flexible-server"},
		{"bicep-module-oci", map[string]any{"kind": "bicep-module-oci",
			"url": "oci://mcr.microsoft.com/bicep/avm/res/app/container-app", "version": "0.23.1"}, "avm/res/app/container-app"},
		{"another directory", map[string]any{"kind": "git-repository", "url": "https://example.com/m.git",
			"module_subdir": "modules/app"}, ""},
		{"another registry", map[string]any{"kind": "bicep-module-oci", "url": "oci://example.com/bicep/avm/res/app/x"}, ""},
		{"a pattern module", map[string]any{"kind": "bicep-module-oci", "url": "oci://mcr.microsoft.com/bicep/avm/ptn/app/x"}, ""},
	} {
		row, why := extractARM(recipeSource{name: "r", source: c.source})
		if row.Key != c.want {
			t.Errorf("%s: the key is %q (%s), want %q", c.name, row.Key, why, c.want)
		}
		if c.want != "" && row.Form != cloudimport.FormAVM {
			t.Errorf("%s: the form is %q, want avm", c.name, row.Form)
		}
	}
}

// TestAModulesParametersAreReadFromItsValuesMapping reads a module Recipe's
// valuesMapping: a parameter written as one action reading a value or an infra
// key whole is placed under that key, beneath a list item by its index, inside
// an if branch and in the else branch of a with alike; a parameter composed,
// read from locals, from infra.overrides, inside a range, or inside a with's
// body, where the dot is the with's value and not the data, and a line of a
// block scalar, give nothing; the literal kind written outside any branch is
// the When, and one inside a branch is not.
func TestAModulesParametersAreReadFromItsValuesMapping(t *testing.T) {
	t.Parallel()
	mapping := "{{- $o := .infra.overrides }}\n" +
		"name: \"{{ .context.instance_id }}\"\n" +
		"kind: OpenAI\n" +
		"sku: \"{{ .locals.sku }}\"\n" +
		"administratorLogin: \"{{ .values.user }}\"\n" +
		"storageSizeGB: {{ int (trimSuffix \"Gi\" .infra.storage.size) }}\n" +
		"subnet: \"{{ .infra.overrides.subnet_id }}\"\n" +
		"region: '{{ .infra.region }}'\n" +
		"{{- if .infra.ha.enabled }}\n" +
		"replicas: {{ .infra.ha.replicas }}\n" +
		"kind: Premium\n" +
		"{{- end }}\n" +
		"deployments:\n" +
		"  - name: \"{{ .values.deployment_name }}\"\n" +
		"    model:\n" +
		"      name: \"{{ .values.model }}\"\n" +
		"  - name: second\n" +
		"    capacity: {{ .infra.capacity }}\n" +
		"configurations:\n" +
		"  {{- range $k, $e := .values.settings }}\n" +
		"  - name: \"{{ .values.ignored }}\"\n" +
		"  {{- end }}\n" +
		"policy: |\n" +
		"  login: {{ .values.user }}\n" +
		"{{- with .values.x }}\n" +
		"rebound: \"{{ .values.y }}\"\n" +
		"{{- else }}\n" +
		"fallback: \"{{ .values.z }}\"\n" +
		"{{- end }}\n" +
		"inline: {{ with .values.x }}{{ .values.y }}{{ end }}\n" +
		"enableTelemetry: false\n"
	row, why := extractARM(recipeSource{name: "r", valuesMapping: mapping,
		source: map[string]any{"kind": "bicep-module-oci", "url": "oci://mcr.microsoft.com/bicep/avm/res/cognitive-services/account"}})
	place := func(key string, from ...string) cloudimport.Placement {
		return cloudimport.Placement{From: from, Key: key}
	}
	want := cloudimport.Row{Key: "avm/res/cognitive-services/account", Form: cloudimport.FormAVM,
		When: []cloudimport.Pred{{Path: []string{"kind"}, Op: cloudimport.OpEquals, Value: "OpenAI"}},
		Values: []cloudimport.Placement{place("user", "administratorLogin"),
			place("deployment_name", "deployments", "0", "name"), place("model", "deployments", "0", "model", "name"),
			place("z", "fallback")},
		Infra: []cloudimport.Placement{place("region", "region"), place("ha.replicas", "replicas"),
			place("capacity", "deployments", "1", "capacity")}}
	if !reflect.DeepEqual(row, want) {
		t.Errorf("the module Recipe gave\n%#v (%s)\nwant\n%#v", row, why, want)
	}
}

// TestOnlyOnePointerFormIsAProvides reads a Recipe's provides: a module output
// read whole off the instance's symbol is placed under its key at the output's
// path, and the resource's identifier — resourceId of its own type, or the id a
// reference to its symbol reads — at an empty path; a value wrapped in an
// operator's override, composed with format or split, reading another
// resource, or reading a resource type not its own, gives nothing.
func TestOnlyOnePointerFormIsAProvides(t *testing.T) {
	t.Parallel()
	module := recipeSource{name: "r", locals: map[string]string{"symbol": symbolLocal},
		source: map[string]any{"kind": "bicep-module-oci", "url": "oci://mcr.microsoft.com/bicep/avm/res/insights/component"},
		provides: []entry{
			{"connection_string", "[reference('{{ .locals.symbol }}').outputs.connectionString.value]"},
			{"host", "{{ if .locals.operator_host }}{{ .locals.operator_host }}{{ else }}[reference('{{ capitalizedWords .context.instance_id }}').outputs.fqdn.value]{{ end }}"},
			{"endpoint", "[format('https://{0}', reference('{{ .locals.symbol }}').outputs.fqdn.value)]"},
			{"name", "[split(reference('{{ .locals.symbol }}').outputs.id.value, '/')[8]]"},
			{"peer", "[reference('{{ .locals.symbol }}Peer').outputs.fqdn.value]"},
			{"port", "443"},
			{"fqdn", "[reference('{{ capitalizedWords .context.instance_id }}').outputs.fqdn.value]"},
		}}
	row, _ := extractARM(module)
	want := []cloudimport.Placement{
		{From: []string{"outputs", "connectionString", "value"}, Key: "connection_string"},
		{From: []string{"outputs", "fqdn", "value"}, Key: "fqdn"},
	}
	if !reflect.DeepEqual(row.Provides, want) {
		t.Errorf("the module Recipe's provides gave %#v, want %#v", row.Provides, want)
	}

	raw := recipeSource{name: "r", fragments: []string{fragmentOf("{{ capitalizedWords .context.instance_id }}",
		"type: Microsoft.Acme/stores\n")},
		provides: []entry{
			{"id", "[resourceId('microsoft.acme/stores', '{{ .context.instance_id }}')]"},
			{"self", "[reference('{{ capitalizedWords .context.instance_id }}', '2025-01-01', 'Full').id]"},
			{"other", "[resourceId('Microsoft.Acme/vaults', '{{ .context.instance_id }}')]"},
			{"output", "[reference('{{ capitalizedWords .context.instance_id }}').outputs.fqdn.value]"},
		}}
	row, why := extractARM(raw)
	if want := []cloudimport.Placement{{Key: "id"}, {Key: "self"}}; !reflect.DeepEqual(row.Provides, want) {
		t.Errorf("the raw Recipe's provides gave %#v (%s), want %#v", row.Provides, why, want)
	}
}

// TestTheOverridesHeaderIsReadLiterally reads the header block a Recipe writes
// above its provides: each key whose parenthesis opens with a path alone, or a
// path followed by a semicolon or a colon, is placed from that path, a list
// index read as its own segment and a nested key under its parent's name; a
// parenthesis opening with words, or with a path a comma follows, a refused
// key with none, and a line after the block, give nothing.
func TestTheOverridesHeaderIsReadLiterally(t *testing.T) {
	t.Parallel()
	document := "spec:\n" +
		"  capability: arm\n" +
		"  # This Recipe reads from infra.overrides:\n" +
		"  #   subnet_id        - string (delegatedSubnetResourceId), required\n" +
		"  #   sku_name         - string (skuName and tier; default \"GP\")\n" +
		"  #   location         - string (location; the module's default is the resource group's)\n" +
		"  #   registry_server  - string (registries[0].server)\n" +
		"  #   access           - bool (publicNetworkAccess, Enabled or Disabled;\n" +
		"  #                      default true)\n" +
		"  #   acr_sku          - string (acrSku: Basic, Standard or Premium)\n" +
		"  #   custom           - object of these fields and no other:\n" +
		"  #     vnet_id        - string (customVirtualNetworkResourceId)\n" +
		"  #     no_public_ip   - bool (disablePublicIp, inverted)\n" +
		"  #   long_named_key_with_no_room\n" +
		"  #                    - bool (database.accessKeysAuthentication)\n" +
		"  #   host             - string (published as provides.host; not passed to the module)\n" +
		"  #   registry_password - refused: the password is read from Key Vault\n" +
		"  #\n" +
		"  #   later            - string (later)\n" +
		"  provides: {}\n"
	row, _ := extractARM(recipeSource{name: "r", document: document,
		source: map[string]any{"kind": "bicep-module-oci", "url": "oci://mcr.microsoft.com/bicep/avm/res/a/b"}})
	place := func(key string, from ...string) cloudimport.Placement {
		return cloudimport.Placement{From: from, Key: key}
	}
	want := []cloudimport.Placement{place("subnet_id", "delegatedSubnetResourceId"), place("location", "location"),
		place("registry_server", "registries", "0", "server"), place("acr_sku", "acrSku"),
		place("custom.vnet_id", "customVirtualNetworkResourceId"),
		place("long_named_key_with_no_room", "database", "accessKeysAuthentication")}
	if !reflect.DeepEqual(row.Overrides, want) {
		t.Errorf("the header gave\n%#v\nwant\n%#v", row.Overrides, want)
	}
}

// TestARecipeWritingNoResourceGivesNoRow reads a reference realization whose
// fragment writes an empty resources block, and a Recipe with neither a module
// nor a fragment: neither gives a row, each saying why.
func TestARecipeWritingNoResourceGivesNoRow(t *testing.T) {
	t.Parallel()
	for _, r := range []recipeSource{
		{name: "reference", fragments: []string{"resources: {}\noutputs:\n  endpoint:\n    type: string\n"}},
		{name: "empty"},
	} {
		row, why := extractARM(r)
		if row.Key != "" || why == "" {
			t.Errorf("%s gave the key %q (%q), want no row and a reason", r.name, row.Key, why)
		}
	}
}
