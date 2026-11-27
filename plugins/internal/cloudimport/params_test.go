// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"encoding/json"
	"strings"
	"testing"
)

// intp returns a pointer to n.
func intp(n int) *int { return &n }

// number returns the JSON number text as a pointer.
func numberp(text string) *json.Number { n := json.Number(text); return &n }

// valuesServerFormat is the Release fixtures' format whose server row places
// four properties in values.
func valuesServerFormat() Format {
	f := releaseFormat()
	pg := f.Table.Generated[0]
	pg.Values = []Placement{
		{From: pathVersion, Key: "version"},
		{From: []string{"properties", "highAvailability", "standbyCount"}, Key: "replicas", Conv: "integer"},
		{From: []string{"properties", "region"}, Key: "region"},
		{From: []string{"properties", "tags"}, Key: "tags"},
	}
	f.Table.Generated[0] = pg
	return f
}

// TestParameterFeedingValuesIsAnAggregateParameter declares a parameter an
// instance reads into a values key under spec.parameters with its type,
// description, allowed values, bounds and literal default, and writes the value
// as a reference to it; a default that is an expression is dropped and named in
// the parameter's comment.
func TestParameterFeedingValuesIsAnAggregateParameter(t *testing.T) {
	in := edgeView()
	in.Target = "arm"
	s := mappedServer()
	s.Body = merged(leaf(Param{Name: "dbVersion"}, pathVersion...),
		leaf(Param{Name: "replicas"}, "properties", "highAvailability", "standbyCount"),
		leaf(Param{Name: "region"}, "properties", "region"))
	g := &Graph{Nodes: []*Node{s}, Params: []Parameter{
		{Name: "dbVersion", Type: "string", Description: "Major version", Allowed: []json.RawMessage{
			json.RawMessage(`"15"`), json.RawMessage(`"16"`)}, MinLength: intp(1), MaxLength: intp(4),
			Default: text("16")},
		{Name: "replicas", Type: "int", Min: numberp("1"), Max: numberp("5"), Default: literal("2")},
		{Name: "region", Type: "string", Default: Opaque{Text: "[resourceGroup().location]", Target: "arm"}},
	}}
	_, aggregate, release, _, diags := released(t, in, valuesServerFormat(), g)
	want := `  parameters:
    # The template parameter dbVersion.
    db_version:
      type: string
      description: "Major version"
      default: "16"
      enum: ["15", "16"]
      minLength: 1
      maxLength: 4
    replicas:
      type: integer
      default: 2
      min: 1
      max: 5
    # Its default in the template is an expression, which an Aggregate parameter cannot hold, so it is not carried.
    region:
      type: string
  components:
`
	if !strings.Contains(aggregate, want) {
		t.Errorf("the Aggregate does not declare the parameters as\n%swant in\n%s", want, aggregate)
	}
	wantBlock(t, aggregate, "server", `    server:
      type: core/datastore/postgres
      values:
        version: "{{ .parameters.db_version }}"
        replicas: {{ .parameters.replicas }}
        region: "{{ .parameters.region }}"
`)
	if !strings.Contains(release, "  parameters:\n    # Left for the Release, since the source cannot supply it: region, ") ||
		strings.Contains(release, "db_version") || strings.Contains(release, "replicas:") {
		t.Errorf("the Release leaves other than the parameter with no default:\n%s", release)
	}
	onlyCode(t, diags, CodeValueNotCarried, 0)
}

// TestParameterSuppliedDefaultSaysWhereEnvironmentValuesBelong writes the
// value a parameters file supplies as the Aggregate parameter's default, with
// a comment saying that a value differing per environment belongs in the
// Release.
func TestParameterSuppliedDefaultSaysWhereEnvironmentValuesBelong(t *testing.T) {
	in := edgeView()
	in.Target = "arm"
	s := pgServer()
	s.Body = []Prop{leaf(Param{Name: "dbVersion"}, pathVersion...)}
	g := &Graph{Nodes: []*Node{s}, Params: []Parameter{{Name: "dbVersion", Type: "string", Supplied: text("15")}}}
	_, aggregate, _, _, _ := released(t, in, releaseFormat(), g)
	want := `    # The template parameter dbVersion.
    # Its default is the value the parameters file supplies; a value that differs per environment belongs in the Release, which fulfils the parameter.
    db_version:
      type: string
      default: "15"
`
	if !strings.Contains(aggregate, want) {
		t.Errorf("the Aggregate does not declare the parameter as\n%swant in\n%s", want, aggregate)
	}
}

// TestParameterFeedingValuesWritesMembersAndWarnsOfWhatIsNot writes a
// parameter read inside an object, and warns value-not-carried naming the
// values key where a member cannot be written, where the value is a secure
// parameter, a reference, a credential or an object parameter.
func TestParameterFeedingValuesWritesMembersAndWarnsOfWhatIsNot(t *testing.T) {
	in := edgeView()
	in.Target = "arm"
	s := mappedServer()
	s.Body = merged(
		leaf(literal(`{"team": "x"}`), "properties", "tags", "static"),
		leaf(Param{Name: "team"}, "properties", "tags", "team"),
		leaf(Param{Name: "secret"}, pathVersion...),
		leaf(Credential{Node: "vault", What: "listKeys"}, "properties", "highAvailability", "standbyCount"),
		leaf(Param{Name: "settings"}, "properties", "region"))
	g := &Graph{Nodes: []*Node{s}, Params: []Parameter{
		{Name: "team", Type: "string", Default: text("core")},
		{Name: "secret", Type: "string", Secure: true},
		{Name: "settings", Type: "object"},
	}}
	_, aggregate, _, _, diags := released(t, in, valuesServerFormat(), g)
	wantBlock(t, aggregate, "server", `    server:
      type: core/datastore/postgres
      values:
        tags: {"static": {"team": "x"}, "team": "{{ .parameters.team }}"}
`)
	warnsAbout(t, diags, CodeValueNotCarried, "values.version", "secure")
	warnsAbout(t, diags, CodeValueNotCarried, "values.replicas", "credential")
	warnsAbout(t, diags, CodeValueNotCarried, "values.region", "object")

	// One member of an object that cannot be written drops the whole key, and
	// says so.
	s.Body = merged(leaf(Param{Name: "team"}, "properties", "tags", "team"),
		leaf(Ref{Node: "other"}, "properties", "tags", "peer"))
	_, aggregate, _, _, diags = released(t, in, valuesServerFormat(), &Graph{Nodes: []*Node{s}, Params: g.Params})
	if strings.Contains(aggregate, "tags:") || strings.Contains(aggregate, "parameters:") {
		t.Errorf("the Aggregate writes a part of the object or declares the parameter it did not write:\n%s", aggregate)
	}
	warnsAbout(t, diags, CodeValueNotCarried, "values.tags")
}

// TestParameterFeedingInfraIsWrittenInEachEntry writes a parameter that only
// feeds infra keys in each Release entry that reads it: the parameters file's
// value, else the literal default, else the key left for the answers file, the
// comment naming the parameter; it declares no parameter of the Aggregate.
func TestParameterFeedingInfraIsWrittenInEachEntry(t *testing.T) {
	in := edgeView()
	in.Target = "arm"
	first := mappedServer(leaf(Param{Name: "sku"}, pathSkuName...), leaf(Param{Name: "tier"}, pathSkuTier...),
		leaf(Param{Name: "storage"}, pathStorage...))
	second := mappedServer(leaf(Param{Name: "sku"}, pathSkuName...))
	second.ID, second.Name = "replica", text("pg-replica")
	g := &Graph{Nodes: []*Node{first, second}, Params: []Parameter{
		{Name: "sku", Type: "string", Supplied: text("Standard_D2s"), Default: text("Standard_B1ms")},
		{Name: "tier", Type: "string", Default: text("GeneralPurpose")},
		{Name: "storage", Type: "int"},
	}}
	_, aggregate, release, _, _ := released(t, in, releaseFormat(), g)
	if strings.Contains(aggregate, "parameters:") {
		t.Errorf("the Aggregate declares a parameter that feeds infra alone:\n%s", aggregate)
	}
	wantBlock(t, release, "server", `    server:
      infra:
        location: "westeurope"
        # From the template parameter sku, the value the parameters file supplies.
        sku: "Standard_D2s"
        # From the template parameter tier, its literal default.
        tier: "GeneralPurpose"
        overrides:
          # Left for the Release, since the source cannot supply it: infra.overrides.storage_size_gb, the template parameter storage has no value in the parameters file and no literal default
          storage_size_gb: ""
`)
	wantBlock(t, release, "replica", `    replica:
      infra:
        location: "westeurope"
        # From the template parameter sku, the value the parameters file supplies.
        sku: "Standard_D2s"
`)
}

// TestParameterReadNowhereIsNamed names in the Aggregate's opening comment the
// parameters no key the draft carries reads, and not one a value, an infra key
// or a condition read as active reads.
func TestParameterReadNowhereIsNamed(t *testing.T) {
	in := edgeView()
	in.Target = "arm"
	s := mappedServer(leaf(Param{Name: "dbVersion"}, pathVersion...), leaf(Param{Name: "sku"}, pathSkuName...))
	s.Condition = Param{Name: "deployDb"}
	subnet := subnetNode()
	subnet.Name = Param{Name: "subnetName"}
	g := &Graph{Nodes: []*Node{s, subnet}, Params: []Parameter{
		{Name: "dbVersion", Type: "string"}, {Name: "sku", Type: "string"}, {Name: "deployDb", Type: "bool"},
		{Name: "subnetName", Type: "string"}, {Name: "unused", Type: "int"},
	}}
	_, aggregate, _, _, _ := released(t, in, releaseFormat(), g)
	if !strings.Contains(aggregate, "# Parameters the draft carries nowhere: subnetName, unused.\n") {
		t.Errorf("the opening comment does not name exactly subnetName and unused:\n%s", aggregate)
	}
}

// TestParameterNamesThatCollideDraftDistinct gives parameters whose names take
// one snake_case key the numeric suffixes of the kit, each value reading its
// own.
func TestParameterNamesThatCollideDraftDistinct(t *testing.T) {
	in := edgeView()
	in.Target = "arm"
	s := mappedServer()
	s.Body = merged(leaf(Param{Name: "dbName"}, pathVersion...), leaf(Param{Name: "db_name"}, "properties", "region"),
		leaf(Param{Name: "DbName"}, "properties", "tags"))
	g := &Graph{Nodes: []*Node{s}, Params: []Parameter{
		{Name: "dbName", Type: "string", Default: text("a")}, {Name: "db_name", Type: "string", Default: text("b")},
		{Name: "DbName", Type: "string", Default: text("c")},
	}}
	_, aggregate, _, _, _ := released(t, in, valuesServerFormat(), g)
	wantBlock(t, aggregate, "server", `    server:
      type: core/datastore/postgres
      values:
        version: "{{ .parameters.db_name }}"
        region: "{{ .parameters.db_name_2 }}"
        tags: "{{ .parameters.db_name_3 }}"
`)
	for _, key := range []string{"    db_name:\n", "    db_name_2:\n", "    db_name_3:\n"} {
		if !strings.Contains(aggregate, key) {
			t.Errorf("the Aggregate declares no parameter %q:\n%s", strings.TrimSpace(key), aggregate)
		}
	}
}

// TestConditionOnOneBooleanParameter writes active reading the parameter a
// node's condition is where it is one boolean parameter and no connection
// reaches the instance, and warns condition-not-carried for any other
// condition.
func TestConditionOnOneBooleanParameter(t *testing.T) {
	in := edgeView()
	in.Target = "arm"
	s := mappedServer()
	s.Condition = Param{Name: "deployDb"}
	g := &Graph{Nodes: []*Node{s}, Params: []Parameter{{Name: "deployDb", Type: "bool", Default: literal("true")}}}
	_, aggregate, _, _, diags := released(t, in, releaseFormat(), g)
	wantBlock(t, aggregate, "server", `    server:
      type: core/datastore/postgres
      active: "{{ .parameters.deploy_db }}"
      values:
        version: "16"
`)
	if !strings.Contains(aggregate, "    deploy_db:\n      type: boolean\n      default: true\n") {
		t.Errorf("the Aggregate does not declare the boolean parameter:\n%s", aggregate)
	}
	onlyCode(t, diags, CodeConditionNotCarried, 0)

	for name, cond := range map[string]Value{
		"an expression": Opaque{Text: "[not(parameters('deployDb'))]", Target: "arm"},
		"a string":      Param{Name: "mode"},
	} {
		s.Condition = cond
		g.Params = append(g.Params, Parameter{Name: "mode", Type: "string"})
		_, aggregate, _, _, diags = released(t, in, releaseFormat(), g)
		if strings.Contains(aggregate, "active:") {
			t.Errorf("%s: the Aggregate writes active:\n%s", name, aggregate)
		}
		warnsAbout(t, diags, CodeConditionNotCarried, "server", "not one boolean parameter")
	}
}

// TestConditionOnATargetOfAnEdgeIsNotCarried drafts a conditional node a
// connection of any kind reaches with no active, and says it is drafted active
// because another instance's edge points at it.
func TestConditionOnATargetOfAnEdgeIsNotCarried(t *testing.T) {
	in := edgeView()
	in.Target = "arm"
	for name, edge := range map[string]func(*Node){
		"a reference": func(a *Node) {
			a.Body = merged(envOf(envEntry("DB_HOST", fqdn)))
		},
		"a dependency alone": func(a *Node) { a.DependsOn = []string{"server"} },
	} {
		s := mappedServer()
		s.Condition = Param{Name: "deployDb"}
		a := app()
		edge(a)
		g := &Graph{Nodes: []*Node{s, a}, Params: []Parameter{{Name: "deployDb", Type: "bool"}}}
		_, aggregate, _, _, diags := released(t, in, releaseFormat(), g)
		if strings.Contains(aggregate, "active:") || strings.Contains(aggregate, "parameters:") {
			t.Errorf("%s: the Aggregate writes active or declares the parameter:\n%s", name, aggregate)
		}
		warnsAbout(t, diags, CodeConditionNotCarried, "server", "drafted active",
			"another instance's edge points at it", "switched-off target halts the build")
	}
}

// requiringFormat is the Release fixtures' format whose server row requires
// the values keys version, database, replicas and tags, of the types string,
// string, integer and object, and the infra keys location, zone and image.
func requiringFormat() Format {
	f := releaseFormat()
	pg := f.Table.Generated[0]
	pg.Requires = []Requirement{
		{Key: "values.version", Type: "string"},
		{Key: "values.database", Type: "string"},
		{Key: "values.replicas", Type: "integer"},
		{Key: "values.tags", Type: "object"},
		{Key: "infra.location", Why: "the region the server runs in"},
		{Key: "infra.zone", Why: "the zone the server runs in, which the Recipe requires and no template states"},
		{Key: "infra.image", Why: "the image the server runs, which the Recipe requires and the source does not name"},
	}
	f.Table.Generated[0] = pg
	return f
}

// TestARequiredStringValueTheSourceDoesNotGiveIsAParameterTheReleaseSupplies
// declares, for a string values key the row's Component requires and the
// instance's values do not hold, an Aggregate parameter of no default written
// as the value, and leaves it under the Release's spec.parameters for the
// answers file. A required key the node writes stays as written, and a
// required key that is no string is not declared.
func TestARequiredStringValueTheSourceDoesNotGiveIsAParameterTheReleaseSupplies(t *testing.T) {
	in := edgeView()
	in.Target = "arm"
	_, aggregate, release, _, diags := released(t, in, requiringFormat(), &Graph{Nodes: []*Node{mappedServer()}})
	wantParams := `  parameters:
    server_database:
      type: string
      description: "values.database of the instance server, which its Component core/datastore/postgres requires and the source does not give."
  components:
`
	if !strings.Contains(aggregate, wantParams) {
		t.Errorf("the Aggregate does not declare the parameter as\n%swant in\n%s", wantParams, aggregate)
	}
	wantBlock(t, aggregate, "server", `    server:
      type: core/datastore/postgres
      values:
        version: "16"
        database: "{{ .parameters.server_database }}"
`)
	wantLeft := `  parameters:
    # Left for the Release, since the source cannot supply it: server_database, values.database of the instance server, which its Component core/datastore/postgres requires and the source does not give
    server_database: ""
`
	if !strings.Contains(release, wantLeft) {
		t.Errorf("the Release does not leave the parameter as\n%swant in\n%s", wantLeft, release)
	}
	onlyCode(t, diags, CodeValueNotCarried, 0)
}

// TestARequiredInfraKeyTheEntryDoesNotWriteIsLeftForTheAnswersFile leaves, in
// a mapped instance's Release entry, each infra key its row requires that the
// entry writes no value for, its comment saying what the value is, the image
// as every importer leaves an image; a required key the row places from the
// node is written as placed.
func TestARequiredInfraKeyTheEntryDoesNotWriteIsLeftForTheAnswersFile(t *testing.T) {
	in := edgeView()
	in.Target = "arm"
	_, _, release, _, _ := released(t, in, requiringFormat(), &Graph{Nodes: []*Node{mappedServer()}})
	wantBlock(t, release, "server", `    server:
      infra:
        location: "westeurope"
        # Left for the Release, since the source cannot supply it: infra.zone, the zone the server runs in, which the Recipe requires and no template states
        zone: ""
        # Left for the Release, since the source cannot supply it: infra.image, the image the server runs, which the Recipe requires and the source does not name
        image: ""
`)
}
