// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// The paths the Release fixtures' server row places.
var (
	pathLocation  = []string{"location"}
	pathSkuName   = []string{"properties", "sku", "name"}
	pathSkuTier   = []string{"properties", "sku", "tier"}
	pathStorage   = []string{"properties", "storage", "storageSizeGB"}
	pathSubnetRef = []string{"properties", "network", "delegatedSubnetResourceId"}
	pathPassword  = []string{"properties", "administratorLoginPassword"}
	pathVersion   = []string{"properties", "version"}
)

// subnetPointer is the pointer the fixtures' format writes for the subnet.
const subnetPointer = "[resourceId('Microsoft.Network/virtualNetworks/subnets', 'vnet-core', 'snet-db')]"

// armText writes a literal string as an ARM template writes one.
func armText(s string) string {
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		return "[" + s
	}
	return s
}

// releaseFormat is the format whose server row places a version in values, a
// location, a size and a tier in infra, a storage size and the delegated
// subnet in infra.overrides and the administrator's password in a credential
// slot, and which writes the subnet's pointer.
func releaseFormat() Format {
	f := edgeFormat(nil)
	f.Text = armText
	pg := f.Table.Generated[0]
	pg.Values = []Placement{{From: pathVersion, Key: "version", Conv: "string"}}
	pg.Infra = []Placement{{From: pathLocation, Key: "location"}, {From: pathSkuName, Key: "sku"},
		{From: pathSkuTier, Key: "tier"}}
	pg.Overrides = []Placement{{From: pathStorage, Key: "storage_size_gb"}, {From: pathSubnetRef, Key: "delegated_subnet_id"}}
	pg.Secrets = []Placement{{From: pathPassword, Key: "admin_password"}}
	pg.Equivalences = []Equivalence{{Raw: pathLocation, Module: []string{"location"}},
		{Raw: pathSkuName, Module: []string{"skuName"}}, {Raw: pathSkuTier, Module: []string{"tier"}},
		{Raw: pathStorage, Module: []string{"storageSizeInGB"}}, {Raw: pathVersion, Module: []string{"version"}}}
	f.Table.Generated[0] = pg
	f.Pointer = func(v Value, _ *Graph) (string, bool) {
		if r, ok := v.(Ref); ok && r.Node == "subnet" && len(r.Attr) == 0 {
			return subnetPointer, true
		}
		return "", false
	}
	return f
}

// subnetNode is the subnet the source creates, which the draft leaves to the
// operator.
func subnetNode() *Node {
	return &Node{ID: "subnet", Type: subnetType, Form: FormRaw, Name: text("snet-db")}
}

// released classifies g in the view in with f, writes its Aggregate and its
// Release, failing on a halt, and returns the classified nodes, the texts of
// the Aggregate, the Release and the Manifest ("" where none is drafted) and
// the diagnostics of all three.
func released(t *testing.T, in aggrega.ImportInput, f Format, g *Graph) (out []Classified, aggregate, release,
	manifest string, diags []aggrega.Diagnostic) {
	t.Helper()
	out, diags = Classify(in, f, g)
	noHalt(t, diags)
	entity, more := Aggregate(in, f, g, out)
	diags = append(diags, more...)
	entities, more := Release(in, f, g, out, entity)
	diags = append(diags, more...)
	noHalt(t, diags)
	if len(entities) == 0 || entities[0].Kind != "Release" || entities[0].Mode != "draft" {
		t.Fatalf("Release returns %+v, want a drafted Release first", entities)
	}
	for _, e := range entities[1:] {
		if e.Kind != "Manifest" {
			t.Fatalf("Release returns a %s beside the Release, want a Manifest at most", e.Kind)
		}
		manifest = e.Body
	}
	return out, entity.Body, entities[0].Body, manifest, diags
}

// warnsAbout fails unless one of the diagnostics of code names each of names.
func warnsAbout(t *testing.T, diags []aggrega.Diagnostic, code Code, names ...string) {
	t.Helper()
	for _, d := range withCode(diags, code) {
		all := true
		for _, name := range names {
			all = all && strings.Contains(d.Message, name)
		}
		if all {
			return
		}
	}
	t.Errorf("no %s warning names %q among %+v", code, names, withCode(diags, code))
}

// mappedServer is the PostgreSQL server of the Release fixtures, writing each
// property its row places.
func mappedServer(body ...Prop) *Node {
	s := pgServer()
	s.Body = merged(append([]Prop{
		leaf(text("westeurope"), pathLocation...),
		leaf(text("16"), pathVersion...),
	}, body...)...)
	return s
}

// TestReleaseEntryForAMappedInstance writes the infra and infra.overrides a
// mapped instance's row places, from its row and from the header keys its
// Recipe reads, each left pointer, the secret its credential slot takes and the
// Recipe realizing it, and names every module parameter set and not translated
// in the entry's comment.
func TestReleaseEntryForAMappedInstance(t *testing.T) {
	in := edgeView()
	in.Target = "arm"
	f := releaseFormat()
	s := mappedServer(
		leaf(text("Standard_B1ms"), pathSkuName...),
		leaf(literal("64"), pathStorage...),
		leaf(Ref{Node: "subnet", Attr: []string{"id"}}, pathSubnetRef...),
		leaf(Param{Name: "adminPassword"}, pathPassword...),
	)
	g := &Graph{Nodes: []*Node{s, subnetNode()}, Params: []Parameter{
		{Name: "adminPassword", Type: "string", Secure: true},
	}}
	out, _, release, manifest, diags := released(t, in, f, g)
	if cl := classOf(t, out, "server"); cl.Row == nil || cl.Row.Recipe != "postgres-flexible-server" {
		t.Fatalf("the server is classed %+v", cl)
	}
	wantBlock(t, release, "server", `    server:
      infra:
        location: "westeurope"
        sku: "Standard_B1ms"
        overrides:
          storage_size_gb: 64
          # Left for the Release, since the source cannot supply it: infra.overrides.delegated_subnet_id, the pointer to the resource subnet (Microsoft.Network/virtualNetworks/subnets), which the draft leaves to the operator; the catalog Recipe takes the resource's literal identifier: write it
          delegated_subnet_id: ""
      features:
        admin_password:
          type: core/referenced-secret
          infra:
            # Left for the Release, since the source cannot supply it: the resource ID of the Azure Key Vault secret holding the template parameter adminPassword, /subscriptions/<subscription>/resourceGroups/<group>/providers/Microsoft.KeyVault/vaults/<vault>/secrets/<secret>; the value the template parameter adminPassword receives goes into that secret.
            origin: ""
`)
	if !strings.Contains(manifest, "server: arm/postgres-flexible-server") {
		t.Errorf("the Manifest does not map the server to its row's Recipe:\n%s", manifest)
	}
	// The row's Recipe name is the realization on every target; the host
	// confirms it against that target's Recipes.
	in.Target = "terraform"
	_, _, _, manifest, _ = released(t, in, f, g)
	if !strings.Contains(manifest, "server: terraform/postgres-flexible-server") {
		t.Errorf("on terraform the Manifest does not map the server to its row's Recipe name:\n%s", manifest)
	}
	onlyCode(t, diags, CodeValueNotCarried, 0)
}

// TestReleaseEntryNamesModuleParametersNotTranslated writes the infra a module
// node's parameters place, through the row's equivalences, and names each
// parameter the node sets that the row does not translate in the entry's
// comment, each once and without its value.
func TestReleaseEntryNamesModuleParametersNotTranslated(t *testing.T) {
	in := edgeView()
	in.Target = "arm"
	s := &Node{ID: "flexibleServer", Type: serverType, Form: FormAVM, Name: text("pg"), Body: []Prop{
		leaf(text("westeurope"), "location"), leaf(text("Standard_B1ms"), "skuName"),
		leaf(literal(`{"mode": "ZoneRedundant"}`), "highAvailability"), leaf(literal("7"), "backupRetentionDays"),
	}}
	_, _, release, _, _ := released(t, in, releaseFormat(), &Graph{Nodes: []*Node{s}})
	wantBlock(t, release, "flexibleserver", `    # Module parameters the source sets and the draft does not translate: highAvailability, backupRetentionDays.
    flexibleserver:
      infra:
        location: "westeurope"
        sku: "Standard_B1ms"
`)
}

// TestReleaseEntryRealizesADraftedInstance names the drafted Recipe as the
// instance's realization in the Manifest where the import's target is the
// format's own, and maps nothing on another target.
func TestReleaseEntryRealizesADraftedInstance(t *testing.T) {
	site := staticSite(leaf(text("westeurope"), "location"), leaf(text("Free"), "properties", "sku"))
	for target, mapped := range map[string]bool{"arm": true, "terraform": false} {
		in := edgeView()
		in.Target = target
		out, _, release, manifest, _ := released(t, in, edgeFormat(nil), &Graph{Nodes: []*Node{site}})
		cl := classOf(t, out, "site")
		if cl.Recipe == "" {
			t.Fatalf("the drafted node names no Recipe: %+v", cl)
		}
		if got := strings.Contains(manifest, "site: "+target+"/"+cl.Recipe); got != mapped {
			t.Errorf("on %s the Manifest maps the instance to its Recipe: %v, want %v\n%s", target, got, mapped, manifest)
		}
		if !strings.Contains(release, `location: "westeurope"`) || !strings.Contains(release, `sku: "Free"`) {
			t.Errorf("the Release does not write the drafted resource's infra:\n%s", release)
		}
	}
}

// TestReleaseEntryIsDeterministic writes one Release and one set of
// diagnostics twice, and from a graph whose nodes are listed in reverse.
func TestReleaseEntryIsDeterministic(t *testing.T) {
	in := edgeView()
	in.Target = "arm"
	build := func(reverse bool) (string, []string) {
		nodes := []*Node{mappedServer(leaf(Param{Name: "sku"}, pathSkuName...), leaf(Ref{Node: "subnet"}, pathSubnetRef...)),
			subnetNode(), staticSite(leaf(Param{Name: "sku"}, "properties", "sku"))}
		if reverse {
			nodes[0], nodes[2] = nodes[2], nodes[0]
		}
		_, _, release, manifest, diags := released(t, in, releaseFormat(),
			&Graph{Nodes: nodes, Params: []Parameter{{Name: "sku", Type: "string", Default: text("S")}}})
		var msgs []string
		for _, d := range diags {
			msgs = append(msgs, d.Code+d.Message)
		}
		return release + manifest, msgs
	}
	a, am := build(false)
	b, bm := build(false)
	c, cm := build(true)
	if a != b || a != c || strings.Join(am, "\n") != strings.Join(bm, "\n") || strings.Join(am, "\n") != strings.Join(cm, "\n") {
		t.Errorf("the Release differs between runs:\n%s\n---\n%s\n---\n%s", a, b, c)
	}
}

// TestReleaseNeverPanicsOnMalformedInput writes a Release for a graph with a
// nil node, a reference naming no node and a parameter the template does not
// declare.
func TestReleaseNeverPanicsOnMalformedInput(t *testing.T) {
	in := edgeView()
	in.Target = "arm"
	s := mappedServer(leaf(Param{Name: "undeclared"}, pathSkuName...), leaf(Ref{Node: "nowhere"}, pathSubnetRef...),
		leaf(Param{Name: "undeclared"}, pathPassword...))
	s.Condition = Param{Name: "undeclared"}
	for _, g := range []*Graph{nil, {Nodes: []*Node{nil, s}}} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("panics: %v", r)
				}
			}()
			out, _ := Classify(in, releaseFormat(), g)
			entity, _ := Aggregate(in, releaseFormat(), g, out)
			Release(in, releaseFormat(), g, out, entity)
		}()
	}
}

// TestReleaseEntryMapsAVendorSpelling writes a value a map converter places as
// the scalar its spelling maps to, regardless of case, and warns
// value-not-carried, naming the key, the property and the converter, where the
// map holds no spelling of the value.
func TestReleaseEntryMapsAVendorSpelling(t *testing.T) {
	const conv = "map:ZoneRedundant=true,Disabled=false"
	mode := []string{"properties", "highAvailability", "mode"}
	f := releaseFormat()
	pg := f.Table.Generated[0]
	pg.Overrides = append(slices.Clone(pg.Overrides), Placement{From: mode, Key: "high_availability_enabled", Conv: conv})
	f.Table.Generated[0] = pg
	in := edgeView()
	in.Target = "arm"

	_, _, release, _, diags := released(t, in, f, &Graph{Nodes: []*Node{mappedServer(leaf(text("zoneredundant"), mode...))}})
	if !strings.Contains(release, "\n          high_availability_enabled: true\n") {
		t.Errorf("the Release does not write the mapped scalar:\n%s", release)
	}
	onlyCode(t, diags, CodeValueNotCarried, 0)

	_, _, release, _, diags = released(t, in, f, &Graph{Nodes: []*Node{mappedServer(leaf(text("SameZone"), mode...))}})
	if strings.Contains(release, "high_availability_enabled") {
		t.Errorf("the Release writes a value the map holds no spelling of:\n%s", release)
	}
	warnsAbout(t, onlyCode(t, diags, CodeValueNotCarried, 1), CodeValueNotCarried, "server",
		"properties.highAvailability.mode", "infra.overrides.high_availability_enabled", conv)
}

// TestPointerOfADraftedInstanceIsWrittenWhole writes the format's pointer whole
// in the entry of an instance drafted from the source, whose Recipe passes the
// property to the resource as the source writes it.
func TestPointerOfADraftedInstanceIsWrittenWhole(t *testing.T) {
	in := edgeView()
	in.Target = "arm"
	site := staticSite(leaf(Ref{Node: "subnet"}, "properties", "subnetId"))
	_, _, release, _, _ := released(t, in, releaseFormat(), &Graph{Nodes: []*Node{site, subnetNode()}})
	if !strings.Contains(release, `: "`+subnetPointer+`"`) {
		t.Errorf("the drafted instance's entry does not hold the pointer whole:\n%s", release)
	}
}

// TestReleaseNamesEnvironmentEntriesNoConnectionDelivers names in a mapped
// instance's Release entry each entry of its environment list no connection
// delivers — the variable and its value as the source writes it, the variable
// alone where the value is a secret — and warns value-not-carried of each
// once; an entry reading another resource is the wiring's, and a parameter
// read only in such an entry is carried nowhere.
func TestReleaseNamesEnvironmentEntriesNoConnectionDelivers(t *testing.T) {
	in := edgeView()
	in.Target = "arm"
	a := app(envOf(
		envEntry("DB_HOST", fqdn),
		envEntry("DB_NAME", text("appdb")),
		envEntry("LOG_LEVEL", Param{Name: "logLevel"}),
		envEntry("API_KEY", Param{Name: "apiKey"}),
		envEntry("BASE_URL", Compose{Parts: []Value{text("https://"), Param{Name: "logLevel"}}}),
		envEntry("STORAGE_KEY", Credential{Node: "server", What: "listKeys"}),
	))
	g := &Graph{Nodes: []*Node{pgServer(), a}, Params: []Parameter{
		{Name: "logLevel", Type: "string", Default: text("info")},
		{Name: "apiKey", Type: "securestring", Secure: true},
	}}
	_, aggregate, release, _, diags := released(t, in, edgeFormat(nil), g)
	want := `Environment variables the source sets and no connection delivers: DB_NAME = appdb; ` +
		`LOG_LEVEL = {the template parameter logLevel}; API_KEY, a secret value the draft never writes; ` +
		`BASE_URL = https://{the template parameter logLevel}.`
	if !strings.Contains(release, "    # "+want+"\n    app:") {
		t.Errorf("the app's Release entry does not name its environment entries as\n%s\n%s", want, release)
	}
	if strings.Contains(release, "DB_HOST") || strings.Contains(release, "STORAGE_KEY") {
		t.Errorf("the Release names an entry the wiring reports:\n%s", release)
	}
	notCarried := withCode(diags, CodeValueNotCarried)
	for _, variable := range []string{"DB_NAME", "LOG_LEVEL", "API_KEY", "BASE_URL"} {
		n := 0
		for _, d := range notCarried {
			if strings.Contains(d.Message, "the variable "+variable+" ") {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s is warned value-not-carried %d times, want once: %+v", variable, n, notCarried)
		}
	}
	if !strings.Contains(aggregate, "# Parameters the draft carries nowhere: logLevel, apiKey.\n") {
		t.Errorf("the parameters an environment entry reads are counted as carried:\n%s", aggregate)
	}
}

// TestReleaseEntryNamesPropertiesTheRowDoesNotPlace names in a mapped
// instance's Release entry each property its node writes that its row does
// not place, at the outermost member nothing under which is placed — a
// property its When tests equal to a value being the row's — and each
// property a child folded into it writes that its fold does not.
func TestReleaseEntryNamesPropertiesTheRowDoesNotPlace(t *testing.T) {
	in := edgeView()
	in.Target = "arm"
	f := releaseFormat()
	f.Table.Generated[0].When = []Pred{{Path: []string{"kind"}, Op: OpEquals, Value: "flexible"}}
	s := mappedServer(leaf(text("Disabled"), "properties", "network", "publicNetworkAccess"),
		leaf(text("orders"), "tags", "team"), leaf(text("flexible"), "kind"))
	db := &Node{ID: "ordersDb", Type: databaseType, Form: FormRaw, Parent: "server", Name: text("orders"),
		Body: []Prop{leaf(text("UTF8"), "properties", "charset")}}
	_, _, release, _, _ := released(t, in, f, &Graph{Nodes: []*Node{s, db}})
	block := instanceBlock(t, release, "server")
	for _, want := range []string{
		"# Properties the source writes and the draft does not place: properties.network.publicNetworkAccess, tags.\n",
		"# Properties ordersDb, folded into this instance, writes and the draft does not place: properties.\n",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the server's Release entry does not hold %q:\n%s", want, block)
		}
	}
}

// TestPointerReadsTheGraph hands the format's pointer writer the graph the
// value is read from, so it names a resource the source declares by what the
// source writes of it.
func TestPointerReadsTheGraph(t *testing.T) {
	f := releaseFormat()
	f.Pointer = func(v Value, g *Graph) (string, bool) {
		r, ok := v.(Ref)
		if !ok || g == nil {
			return "", false
		}
		for _, n := range g.Nodes {
			if n.ID == r.Node {
				name, _ := literalText(n.Name.(Literal).JSON)
				return "subnet:" + name, true
			}
		}
		return "", false
	}
	in := edgeView()
	in.Target = "arm"
	site := staticSite(leaf(Ref{Node: "subnet"}, "properties", "subnetId"))
	_, _, release, _, _ := released(t, in, f, &Graph{Nodes: []*Node{site, subnetNode()}})
	if !strings.Contains(release, `: "subnet:snet-db"`) {
		t.Errorf("the pointer writer is not handed the graph:\n%s", release)
	}
}

// TestReleaseEntryNamesWhatATypeMappedByAnOverrideWrites writes the Release of a
// cache the mapping_overrides setting maps to a Component the catalog holds, with
// no row: the instance's entry names every property the node writes in a
// comment, places nothing and names no realization, which the host maps.
func TestReleaseEntryNamesWhatATypeMappedByAnOverrideWrites(t *testing.T) {
	f := fixtureFormat()
	held := schemadraft.NameFor(fixtureView(""), f.Draft, cacheType)
	settings := `{"mapping_overrides": {"arm": {"` + cacheType + `": {"component": "` + held + `",
		"category": "datastore", "traits": {"component.aggrega.ai/type": "cache"}}}}}`
	in := fixtureView(settings, component(held, cacheLabels))
	in.Target = "arm"
	cache := cacheGraph().Nodes[0]
	cache.Body = merged(cache.Body...)
	g := &Graph{Nodes: []*Node{cache}}
	out, _, release, manifest, _ := released(t, in, f, g)
	if cl := classOf(t, out, "cache"); cl.Class != ClassMapped || cl.Row != nil {
		t.Fatalf("the cache is %s with row %v, want mapped with no row", cl.Class, cl.Row)
	}
	want := "    # Properties the source writes and the draft does not place: location, properties.\n    cache: {}\n"
	if !strings.Contains(release, want) {
		t.Errorf("the Release does not hold %q:\n%s", want, release)
	}
	if strings.Contains(manifest, "cache: arm/") {
		t.Errorf("the Manifest names a realization the host maps:\n%s", manifest)
	}
	if _, _, again, _, _ := released(t, in, f, g); again != release {
		t.Errorf("a second run writes the Release\n%s\nthe first wrote\n%s", again, release)
	}
}

// TestDeployTimeTextNamesWhatIsWrittenInPlaceAndLeavesWhatItOwes writes
// deploy-time text holding a parameter's value written in place beneath a
// comment naming the parameter, in the Aggregate and in the Release, the
// parameter carried; and leaves text owing what it reads for the Release's
// answers file, warning value-not-carried with why.
func TestDeployTimeTextNamesWhatIsWrittenInPlaceAndLeavesWhatItOwes(t *testing.T) {
	in := edgeView()
	in.Target = "arm"
	owed := "it reads the variable settings, which the draft cannot write in place"
	inPlace := func(text string) Opaque { return Opaque{Text: text, Target: "arm", Params: []string{"tier"}} }
	s := pgServer()
	s.Body = merged(
		leaf(text("westeurope"), pathLocation...),
		leaf(inPlace("[string(16)]"), pathVersion...),
		leaf(inPlace("[toUpper('b1')]"), pathSkuName...),
		leaf(Opaque{Text: "[mul(variables('settings').size, 2)]", Target: "arm", Owed: owed}, pathStorage...),
	)
	g := &Graph{Nodes: []*Node{s}, Params: []Parameter{{Name: "tier", Type: "string", Default: text("b1")}}}
	_, aggregate, release, _, diags := released(t, in, releaseFormat(), g)
	if want := "      values:\n        # From the template parameter tier, its literal default.\n" +
		"        version: \"[string(16)]\"\n"; !strings.Contains(aggregate, want) {
		t.Errorf("the Aggregate does not hold\n%s\n%s", want, aggregate)
	}
	if strings.Contains(aggregate, "carries nowhere") {
		t.Errorf("the Aggregate names the parameter carried nowhere:\n%s", aggregate)
	}
	wantBlock(t, release, "server", `    server:
      infra:
        location: "westeurope"
        # From the template parameter tier, its literal default.
        sku: "[toUpper('b1')]"
        overrides:
          # Left for the Release, since the source cannot supply it: infra.overrides.storage_size_gb, the source's value, which the draft does not write
          storage_size_gb: ""
`)
	got := onlyCode(t, diags, CodeValueNotCarried, 1)
	if !strings.Contains(got[0].Message, "infra.overrides.storage_size_gb, and "+owed+", so it is not written") {
		t.Errorf("value-not-carried does not say why the storage size is not written: %s", got[0].Message)
	}
}
