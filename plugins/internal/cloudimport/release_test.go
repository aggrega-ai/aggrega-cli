// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"strings"
	"testing"

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
	f.Pointer = func(v Value) (string, bool) {
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
          # Left for the Release, since the source cannot supply it: infra.overrides.delegated_subnet_id, the pointer to the resource subnet (Microsoft.Network/virtualNetworks/subnets), which the draft leaves to the operator; write the target's pointer to it
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
