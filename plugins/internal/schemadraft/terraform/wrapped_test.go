// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package terraform

import (
	"path"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

const (
	rdsURL    = "https://github.com/terraform-aws-modules/terraform-aws-rds.git"
	codeUnpin = schemadraft.CodeModuleVersionUnpinned
	codeLink  = schemadraft.CodeLinkedSourceNotRead
)

func wrappedInput() aggrega.ImportInput {
	return aggrega.ImportInput{Format: Format.Name, Name: "main.tf", Bytes: []byte("{}")}
}

// rdsModule is the RDS module as a configuration calls it: one override, one
// parameter a neighbour supplies, one output a neighbour reads.
func rdsModule(version string) schemadraft.WrappedModule {
	wp := func(name string) *schemadraft.Property {
		return &schemadraft.Property{Name: name, Path: []string{name}, Kinds: kString, Written: true}
	}
	return schemadraft.WrappedModule{
		Type:       &schemadraft.Type{Name: "aws_rds_aws"},
		SourceKind: "git-repository",
		URL:        rdsURL,
		Version:    version,
		Inputs: []schemadraft.Input{
			in(wp("engine_version"), schemadraft.PillarOverrides, "engine_version", false),
			injected(wp("db_subnet_group_name"), schemadraft.MechanismProperty, "db_subnet_group_name", true),
		},
		Outputs: []schemadraft.Provide{{Key: "endpoint", Attribute: []string{"db_instance_endpoint"}}},
	}
}

// specAt reads the node at path below spec of the Recipe body.
func specAt(t *testing.T, body string, path ...string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("the Recipe is not YAML: %v\n%s", err, body)
	}
	n := doc.Content[0]
	for _, key := range append([]string{"spec"}, path...) {
		var next *yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == key {
				next = n.Content[i+1]
			}
		}
		if next == nil {
			t.Fatalf("the Recipe has no %s:\n%s", strings.Join(path, "."), body)
		}
		n = next
	}
	return n
}

func codesOf(diags []aggrega.Diagnostic, code string) []aggrega.Diagnostic {
	var out []aggrega.Diagnostic
	for _, d := range diags {
		if d.Code == code {
			out = append(out, d)
		}
	}
	return out
}

// TestWriteWrappedGitGolden drafts a Recipe sourcing a published module from
// its git repository at a tag, binding the override only where present and the
// injected parameter always, admitting exactly the drafted override and
// publishing the output as a module reference, with no module file.
func TestWriteWrappedGitGolden(t *testing.T) {
	drafted, diags := schemadraft.DraftWrapped(wrappedInput(), Format, rdsModule("v6.1.0"), WriteWrapped)
	if halts := haltsOf(diags); len(halts) != 0 || len(diags) != 0 {
		t.Fatalf("diagnostics %+v", diags)
	}
	if len(drafted.Entities) != 2 {
		t.Fatalf("drafted %+v, want a Component and a Recipe", drafted.Entities)
	}
	recipe := drafted.Entities[1].Entity.Body
	golden(t, "../wrapped/rds.golden.yaml", recipe)
	for path, want := range map[string]string{
		"source.kind":       "git-repository",
		"source.url":        rdsURL,
		"source.ref":        "v6.1.0",
		"provides.endpoint": "${module.{{ .context.instance_id }}.db_instance_endpoint}",
	} {
		if got := specAt(t, recipe, strings.Split(path, ".")...).Value; got != want {
			t.Errorf("spec.%s = %q, want %q", path, got, want)
		}
	}
	if assertion := specAt(t, recipe, "assertions", "overrides_this_recipe_reads").Value; !strings.Contains(assertion, `(list "engine_version")`) {
		t.Errorf("the allowlist does not admit exactly the drafted override:\n%s", assertion)
	}
	mapping := specAt(t, recipe, "valuesMapping").Value
	if !strings.Contains(mapping, "{{- if (hasKey (optional .infra \"overrides\" dict) \"engine_version\") }}") ||
		!strings.Contains(mapping, `db_subnet_group_name: {{ toJson (index (default dict .context.inject) "property" "db_subnet_group_name") }}`) {
		t.Errorf("the valuesMapping binds the inputs as\n%s", mapping)
	}
	got, err := renderMapping(t, mapping, mappingContext(nil, map[string]any{"overrides": map[string]any{"engine_version": "16.1"}},
		map[string]any{"property": map[string]any{"db_subnet_group_name": "private"}}, nil))
	if err != nil || canon(t, got) != `{"db_subnet_group_name":"private","engine_version":"16.1"}` {
		t.Errorf("the valuesMapping renders %v, %v", got, err)
	}
	for _, e := range drafted.Entities {
		if strings.Contains(e.Entity.Body, "main.tf.json") {
			t.Errorf("the draft names a module file:\n%s", e.Entity.Body)
		}
	}
}

// TestWriteWrappedSubdirAndOCI writes module_subdir for a module inside a
// repository, and the version of a module in an OCI registry.
func TestWriteWrappedSubdirAndOCI(t *testing.T) {
	m := rdsModule("v6.1.0")
	m.Subdir = "modules/postgresql"
	drafted, _ := schemadraft.DraftWrapped(wrappedInput(), Format, m, WriteWrapped)
	if got := specAt(t, drafted.Entities[1].Entity.Body, "source", "module_subdir").Value; got != "modules/postgresql" {
		t.Errorf("module_subdir = %q", got)
	}

	m = rdsModule("1.2.0")
	m.Subdir = "modules/rds"
	m.SourceKind, m.URL = "terraform-module-oci", "oci://registry.example/modules/rds"
	drafted, diags := schemadraft.DraftWrapped(wrappedInput(), Format, m, WriteWrapped)
	if len(diags) != 0 {
		t.Fatalf("diagnostics %+v", diags)
	}
	source := specAt(t, drafted.Entities[1].Entity.Body, "source")
	var got struct {
		Kind, URL, Version, Ref string
		ModuleSubdir            string `yaml:"module_subdir"`
	}
	if err := source.Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Kind != "terraform-module-oci" || got.URL != m.URL || got.Version != "1.2.0" || got.Ref != "" || got.ModuleSubdir != "modules/rds" {
		t.Errorf("source %+v", got)
	}
}

// TestWriteWrappedWithoutVersionWarns leaves ref empty and warns naming the
// module.
func TestWriteWrappedWithoutVersionWarns(t *testing.T) {
	drafted, diags := schemadraft.DraftWrapped(wrappedInput(), Format, rdsModule(""), WriteWrapped)
	unpinned := codesOf(diags, codeUnpin)
	if len(unpinned) != 1 || !strings.Contains(unpinned[0].Message, rdsURL) {
		t.Fatalf("diagnostics %+v", diags)
	}
	if got := specAt(t, drafted.Entities[1].Entity.Body, "source", "ref"); got.Value != "" || got.Tag != "!!str" {
		t.Errorf("ref = %q (%s), want an empty string", got.Value, got.Tag)
	}
}

// TestWriteWrappedWithoutURLLeavesItForTheAuthor drafts a module whose address
// the source states and whose source cannot be derived, with its url empty.
func TestWriteWrappedWithoutURLLeavesItForTheAuthor(t *testing.T) {
	const address = "app.terraform.io/acme/vpc/aws"
	m := rdsModule("v1.4.0")
	m.URL, m.AddressOnly = "", address
	drafted, diags := schemadraft.DraftWrapped(wrappedInput(), Format, m, WriteWrapped)
	if halts := haltsOf(diags); len(halts) != 0 {
		t.Fatalf("halts %+v", halts)
	}
	body := drafted.Entities[1].Entity.Body
	if got := specAt(t, body, "source", "url"); got.Value != "" || got.Tag != "!!str" {
		t.Errorf("url = %q, want empty", got.Value)
	}
	if got := specAt(t, body, "source", "ref").Value; got != "v1.4.0" {
		t.Errorf("ref = %q", got)
	}
	if !strings.Contains(body, address) {
		t.Errorf("the Recipe does not name %s:\n%s", address, body)
	}
	linked := codesOf(diags, codeLink)
	if len(linked) != 1 || linked[0].Severity != aggrega.SeverityWarn || !strings.Contains(linked[0].Message, address) ||
		!strings.Contains(linked[0].Message, "spec.source.url") {
		t.Errorf("diagnostics %+v", diags)
	}
	if len(codesOf(diags, codeUnpin)) != 0 {
		t.Errorf("an exact version warns unpinned: %+v", diags)
	}

	m.Version = ""
	_, diags = schemadraft.DraftWrapped(wrappedInput(), Format, m, WriteWrapped)
	if len(codesOf(diags, codeUnpin)) != 1 || len(codesOf(diags, codeLink)) != 1 {
		t.Errorf("diagnostics %+v, want both warnings", diags)
	}

	m.AddressOnly = ""
	_, diags = schemadraft.DraftWrapped(wrappedInput(), Format, m, WriteWrapped)
	if len(diags) != 1 || diags[0].Code != schemadraft.CodeResourceSchemaUnreadable || diags[0].Severity != aggrega.SeverityHalt {
		t.Errorf("a module with neither address gives %+v, want the halt", diags)
	}
}

// TestWriteWrappedBindsFixedInputs writes a fixed parameter as a literal line
// no override admits.
func TestWriteWrappedBindsFixedInputs(t *testing.T) {
	m := rdsModule("v6.1.0")
	m.Fixed = []importer.Entry{{Key: "enable_telemetry", Value: "false"}}
	drafted, _ := schemadraft.DraftWrapped(wrappedInput(), Format, m, WriteWrapped)
	recipe := drafted.Entities[1].Entity.Body
	if mapping := specAt(t, recipe, "valuesMapping").Value; !strings.Contains(mapping, "enable_telemetry: false\n") {
		t.Errorf("the valuesMapping does not bind the fixed parameter:\n%s", mapping)
	}
	if assertion := specAt(t, recipe, "assertions", "overrides_this_recipe_reads").Value; strings.Contains(assertion, "enable_telemetry") {
		t.Errorf("an override admits the fixed parameter:\n%s", assertion)
	}
}

// TestWrappedNameOverridesTheSlug names the Component and Recipe by Name where
// it is set.
func TestWrappedNameOverridesTheSlug(t *testing.T) {
	m := rdsModule("v6.1.0")
	plain, _ := schemadraft.DraftWrapped(wrappedInput(), Format, m, WriteWrapped)
	if want := schemadraft.NameFor(wrappedInput(), Format, m.Type.Name); plain.Name != want {
		t.Errorf("without Name the Component is %q, want %q", plain.Name, want)
	}
	m.Name = "rds"
	named, _ := schemadraft.DraftWrapped(wrappedInput(), Format, m, WriteWrapped)
	if named.Name != path.Dir(plain.Name)+"/rds" ||
		named.Recipe != "rds" {
		t.Errorf("with Name the Component is %q and the Recipe %q", named.Name, named.Recipe)
	}
}
