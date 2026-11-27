// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// The types of the toy format's secrets and of a server's backup.
const (
	toySecretType = "Toy.Secrets/secrets"
	toyBackupType = toyServer + "/backups"
)

// generatedSecret is the toy format's attach rule for a secret the source
// generates: core/random-password on each server reading it at a credential
// slot, each reader after the first taking a referenced secret.
func generatedSecret() AttachRule {
	return AttachRule{Type: toySecretType, When: []Pred{{Path: []string{"generate"}, Op: OpPresent}},
		Feature: "core/random-password", Via: AttachReader, Others: "core/referenced-secret",
		Values: []Placement{
			{From: []string{"generate", "key"}, Key: "key"},
			{From: []string{"generate", "length"}, Key: "length"},
			{From: []string{"generate", "excludePunctuation"}, Key: "symbols", Conv: "map:true=false,false=true"},
			{From: []string{"name"}, Key: "name"},
		}}
}

// keptSecret is the toy format's attach rule for a secret the source keeps and
// does not generate: the kit's referenced secret on each server reading it at
// a credential slot, its origin the secret's name.
func keptSecret() AttachRule {
	return AttachRule{Type: toySecretType, When: []Pred{{Path: []string{"generate"}, Op: OpAbsent}},
		Feature: "core/referenced-secret", Secret: true,
		Values: []Placement{{From: []string{"name"}, Key: "origin"}}}
}

// backup is the toy format's attach rule writing a server's backup as
// core/backup on the server's instance.
func backup() AttachRule {
	return AttachRule{Type: toyBackupType, Feature: "core/backup", Via: AttachParent, Slot: "backup",
		Values: []Placement{{From: []string{"retentionDays"}, Key: "retention_days"},
			{From: []string{"schedule"}, Key: "schedule"}}}
}

// toyReader is a toy server reading its administrator's password as password.
func toyReader(id string, password Value, dependsOn ...string) *Node {
	return &Node{ID: id, Type: toyServer, Form: FormRaw, Source: "main.json#/" + id, DependsOn: dependsOn,
		Body: []Prop{{Name: "location", Value: text("westeurope")}, {Name: "version", Value: text("16")},
			{Name: "password", Value: password}}}
}

// toySecret is a toy secret named name, where name is not empty, that the
// source generates where generate is set.
func toySecret(id, name string, generate bool) *Node {
	n := &Node{ID: id, Type: toySecretType, Form: FormRaw, Source: "main.json#/" + id}
	if name != "" {
		n.Body = append(n.Body, Prop{Name: "name", Value: text(name)})
	}
	if generate {
		n.Body = append(n.Body, Prop{Name: "generate", Props: []Prop{{Name: "key", Value: text("password")},
			{Name: "length", Value: literal("32")}, {Name: "excludePunctuation", Value: literal("true")}}})
	}
	return n
}

// readsSecret is the credential a server reads from the secret id.
func readsSecret(id string) Credential { return Credential{Node: id, What: "SecretString:password"} }

// attached classifies the graph of nodes in the toy format with the attach
// rules given, and imports it for target, failing on a halt; nodes is called
// once for each, so neither sees what the other does to them.
func attached(t *testing.T, target string, rules []AttachRule, nodes func() []*Node) ([]Classified,
	aggrega.ImportOutput) {
	t.Helper()
	return attachedGraph(t, target, rules, func() *Graph { return &Graph{Nodes: nodes()} })
}

// attachedGraph is attached over the graph graph returns, called once for the
// classification and once for the import.
func attachedGraph(t *testing.T, target string, rules []AttachRule, graph func() *Graph) ([]Classified,
	aggrega.ImportOutput) {
	t.Helper()
	f := toyFormat(nil)
	f.Attach = rules
	f.Read = func(aggrega.ImportInput, Settings, ParseFunc) (*Graph, []aggrega.Diagnostic) {
		g := graph()
		g.Format, g.Read = "toy", []string{"main.json"}
		return g, nil
	}
	in := toyInput(t, target, "", []string{"main.json"})
	g, _ := f.Read(in, Settings{}, toyParse)
	cls, diags := Classify(in, f, g)
	noHalt(t, diags)
	out := Import(in, f, toyParse)
	noHalt(t, out.Diagnostics)
	return cls, out
}

// draftsFromSource reports whether the import drafts a Component from the
// source.
func draftsFromSource(out aggrega.ImportOutput) bool {
	return slices.ContainsFunc(filePaths(out), func(p string) bool { return strings.HasPrefix(p, "catalog/") })
}

// TestAttachReaderOneConsumer writes a secret the source generates, read by
// one server at its credential slot, as core/random-password at that slot of
// the server's instance, its values read from the secret through the rule's
// placements: the secret is classed attached, drafts nothing, and is reported
// resource-folded naming the Feature; the slot attaches no other secret, and
// no credential is reported flowing.
func TestAttachReaderOneConsumer(t *testing.T) {
	cls, out := attached(t, "toy", []AttachRule{generatedSecret()}, func() []*Node {
		return []*Node{toyReader("db", readsSecret("dbSecret")), toySecret("dbSecret", "orders-db", true)}
	})
	cl := classOf(t, cls, "dbSecret")
	if cl.Class != ClassAttached || cl.Key != "" || len(cl.Drafts) != 0 {
		t.Fatalf("the secret is classed %s, keyed %q, drafting %d, want attached, with no key and no draft",
			cl.Class, cl.Key, len(cl.Drafts))
	}
	if !strings.HasPrefix(cl.Decision.Rationale, "attached: ") {
		t.Errorf("the decision says %q, want it to open with the class", cl.Decision.Rationale)
	}
	aggregate := fileOf(t, out, "aggregate.yaml")
	wantBlock(t, aggregate, "db", `    db:
      type: core/datastore/postgres
      values:
        version: "16"
      features:
        admin_password:
          type: core/random-password
          values:
            key: "password"
            length: 32
            symbols: false
            name: "orders-db"
`)
	if strings.Contains(aggregate, "dbsecret:") || draftsFromSource(out) {
		t.Errorf("the secret is drafted, files %q:\n%s", filePaths(out), aggregate)
	}
	wantBlock(t, fileOf(t, out, "release.yaml"), "db", "    db:\n      infra:\n        location: \"westeurope\"\n")
	folded := onlyCode(t, out.Diagnostics, CodeResourceFolded, 1)
	if folded[0].Severity != aggrega.SeverityInfo {
		t.Errorf("resource-folded is a %s, want info", folded[0].Severity)
	}
	mentions(t, "resource-folded", folded[0].Message, "dbSecret", "core/random-password", "admin_password")
	onlyCode(t, out.Diagnostics, CodeCredentialFlow, 0)
	onlyCode(t, out.Diagnostics, CodeReferenceNotWired, 0)
}

// TestAttachReaderSeveralConsumers attaches a secret the source generates,
// read by several servers, as core/random-password on the first reader by
// instance order, its values.name pinned — the secret's own name, else the
// key the format gives it — and as a referenced secret on each other reader,
// in its Release entry, its origin that name.
func TestAttachReaderSeveralConsumers(t *testing.T) {
	for _, c := range []struct {
		case_, name, pinned string
	}{
		{"named", "orders-db", "orders-db"},
		{"unnamed", "", "dbsecret"},
	} {
		t.Run(c.case_, func(t *testing.T) {
			nodes := func() []*Node {
				return []*Node{toyReader("dbA", readsSecret("dbSecret"), "dbB"), toyReader("dbB", readsSecret("dbSecret")),
					toySecret("dbSecret", c.name, true)}
			}
			cls, out := attached(t, "cloudformation", []AttachRule{generatedSecret()}, nodes)
			if cl := classOf(t, cls, "dbSecret"); cl.Class != ClassAttached || len(cl.Attach) != 2 {
				t.Fatalf("the secret is classed %s with attachments %+v, want attached on both readers", cl.Class,
					cl.Attach)
			}
			aggregate, release := fileOf(t, out, "aggregate.yaml"), fileOf(t, out, "release.yaml")
			wantBlock(t, aggregate, "dbb", `    # The resource dbB.
    dbb:
      type: core/datastore/postgres
      values:
        version: "16"
      features:
        admin_password:
          type: core/random-password
          values:
            key: "password"
            length: 32
            symbols: false
            name: "`+c.pinned+`"
`)
			if block := instanceBlock(t, aggregate, "dba"); strings.Contains(block, "features:") {
				t.Errorf("the second reader declares an attachment in the Aggregate:\n%s", block)
			}
			wantBlock(t, release, "dba", `    dba:
      infra:
        location: "westeurope"
      features:
        admin_password:
          type: core/referenced-secret
          infra:
            # Attached by reference, never as a value: the secret dbSecret the source declares, which the instance dbb attaches as core/random-password, at the locator the source states.
            origin: "`+c.pinned+`"
`)
			if block := instanceBlock(t, release, "dbb"); strings.Contains(block, "features:") {
				t.Errorf("the first reader attaches a secret in its Release entry:\n%s", block)
			}
			folded := onlyCode(t, out.Diagnostics, CodeResourceFolded, 1)
			mentions(t, "resource-folded", folded[0].Message, "core/random-password", "core/referenced-secret", "dba",
				"dbb")
			if c.name != "" && strings.Contains(folded[0].Message, c.name) {
				t.Errorf("resource-folded quotes the secret's name: %s", folded[0].Message)
			}

			_, again := attached(t, "cloudformation", []AttachRule{generatedSecret()}, nodes)
			a, err := json.Marshal(out)
			if err != nil {
				t.Fatal(err)
			}
			b, err := json.Marshal(again)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(a, b) {
				t.Errorf("two imports differ:\n%s\n%s", a, b)
			}
		})
	}
}

// TestAttachParent writes a child an AttachParent rule takes as the rule's
// Feature on the instance of its parent, at the rule's slot, its values read
// from the child.
func TestAttachParent(t *testing.T) {
	cls, out := attached(t, "toy", []AttachRule{backup()}, func() []*Node {
		return []*Node{
			{ID: "db", Type: toyServer, Form: FormRaw, Body: []Prop{{Name: "version", Value: text("16")}}},
			{ID: "dbBackup", Type: toyBackupType, Form: FormRaw, Parent: "db",
				Body: []Prop{{Name: "retentionDays", Value: literal("7")}, {Name: "schedule", Value: text("daily")}}},
		}
	})
	if cl := classOf(t, cls, "dbBackup"); cl.Class != ClassAttached {
		t.Fatalf("the backup is classed %s, want attached", cl.Class)
	}
	aggregate := fileOf(t, out, "aggregate.yaml")
	wantBlock(t, aggregate, "db", `    db:
      type: core/datastore/postgres
      values:
        version: "16"
      features:
        backup:
          type: core/backup
          values:
            retention_days: 7
            schedule: "daily"
`)
	if strings.Contains(aggregate, "dbbackup:") || draftsFromSource(out) {
		t.Errorf("the backup is drafted:\n%s", aggregate)
	}
}

// TestAttachReaderAsReferencedSecret attaches a secret the source keeps, read
// at a server's credential slot, as the kit's referenced secret in the
// server's Release entry, its origin what the rule places under origin, and
// left for the operator where the secret holds nothing there; the Aggregate
// declares nothing of it.
func TestAttachReaderAsReferencedSecret(t *testing.T) {
	nodes := func(name string) func() []*Node {
		return func() []*Node {
			return []*Node{toyReader("db", Ref{Node: "dbSecret"}), toySecret("dbSecret", name, false)}
		}
	}

	t.Run("named", func(t *testing.T) {
		_, out := attached(t, "cloudformation", []AttachRule{keptSecret()}, nodes("prod/orders/db"))
		wantBlock(t, fileOf(t, out, "release.yaml"), "db", `    db:
      infra:
        location: "westeurope"
      features:
        admin_password:
          type: core/referenced-secret
          infra:
            # Attached by reference, never as a value: the secret dbSecret the source declares, at the locator the source states.
            origin: "prod/orders/db"
`)
		if block := instanceBlock(t, fileOf(t, out, "aggregate.yaml"), "db"); strings.Contains(block, "features:") {
			t.Errorf("the Aggregate declares the referenced secret:\n%s", block)
		}
		onlyCode(t, out.Diagnostics, CodeReferenceNotWired, 0)
	})

	t.Run("unnamed", func(t *testing.T) {
		_, out := attached(t, "terraform", []AttachRule{keptSecret()}, nodes(""))
		block := instanceBlock(t, fileOf(t, out, "release.yaml"), "db")
		for _, want := range []string{"type: core/referenced-secret", "# Left for the Release", `origin: ""`} {
			if !strings.Contains(block, want) {
				t.Errorf("the entry lacks %q:\n%s", want, block)
			}
		}
	})

	t.Run("unnamed on cloudformation", func(t *testing.T) {
		_, out := attached(t, "cloudformation", []AttachRule{keptSecret()}, nodes(""))
		wantBlock(t, fileOf(t, out, "release.yaml"), "db", `    db:
      infra:
        location: "westeurope"
      features:
        admin_password:
          type: core/referenced-secret
          infra:
            # Attached by reference, never as a value: the secret dbSecret the source declares, read from the variable dbSecret, which a target substituting variable references, as Docker Compose does when the stack starts, reads from the environment, so no file carries the value.
            origin: "${dbSecret}"
`)
	})
}

// TestAttachedResourceNamesWhatItLeavesOut names each property an attached
// node writes that its rule places nowhere — not by its values' placements,
// nor by a predicate of its When testing it equals a value — in the node's
// decision and in a comment above the Release entry of the instance taking
// its first attachment, as a folded child's are named; a predicate testing
// that a property is present places nothing inside it.
func TestAttachedResourceNamesWhatItLeavesOut(t *testing.T) {
	rule := generatedSecret()
	rule.When = append(rule.When, Pred{Path: []string{"generate", "kind"}, Op: OpEquals, Value: "random"})
	cls, out := attached(t, "toy", []AttachRule{rule}, func() []*Node {
		s := toySecret("dbSecret", "orders-db", true)
		s.Body[1].Props = append(s.Body[1].Props, Prop{Name: "kind", Value: text("random")},
			Prop{Name: "template", Value: text(`{"username":"admin"}`)}, Prop{Name: "excludeCharacters", Value: text("/@")})
		s.Body = append(s.Body, Prop{Name: "kmsKeyId", Value: text("alias/orders")})
		return []*Node{toyReader("dbA", readsSecret("dbSecret"), "dbB"), toyReader("dbB", readsSecret("dbSecret")), s}
	})
	decision := classOf(t, cls, "dbSecret").Decision.Rationale
	if !strings.Contains(decision, "the attachment does not carry what it writes at generate.template, "+
		"generate.excludeCharacters and kmsKeyId") || strings.Contains(decision, "username") {
		t.Errorf("the decision does not name what the rule leaves out, or quotes a value: %s", decision)
	}
	release := fileOf(t, out, "release.yaml")
	want := "    # Properties dbSecret, attached to this instance as core/random-password at the slot " +
		"admin_password, writes and the draft does not place: generate.template, generate.excludeCharacters, " +
		"kmsKeyId.\n    dbb:\n"
	if !strings.Contains(release, want) {
		t.Errorf("the first reader's entry does not name what the rule leaves out, want\n%s\nin\n%s", want, release)
	}
	if block := instanceBlock(t, release, "dba"); strings.Contains(block, "attached to this instance") {
		t.Errorf("a later reader's entry names what the rule leaves out:\n%s", block)
	}
}

// TestAttachPinnedNameCarriesNoParameter pins the name of a secret several
// servers read to the key the format gives it where the secret's name is a
// parameter: the parameter is read nowhere else, so the draft carries it
// nowhere and declares no parameter for it.
func TestAttachPinnedNameCarriesNoParameter(t *testing.T) {
	_, out := attachedGraph(t, "toy", []AttachRule{generatedSecret()}, func() *Graph {
		s := toySecret("dbSecret", "", true)
		s.Body = append([]Prop{{Name: "name", Value: Param{Name: "secretName"}}}, s.Body...)
		return &Graph{Nodes: []*Node{toyReader("dbA", readsSecret("dbSecret"), "dbB"),
			toyReader("dbB", readsSecret("dbSecret")), s},
			Params: []Parameter{{Name: "secretName", Type: "string", Default: text("orders-db")}}}
	})
	aggregate := fileOf(t, out, "aggregate.yaml")
	if !strings.Contains(aggregate, "# Parameters the draft carries nowhere: secretName.\n") ||
		strings.Contains(aggregate, "  parameters:\n") {
		t.Errorf("the Aggregate does not name the parameter as carried nowhere, or declares it:\n%s", aggregate)
	}
	if block := instanceBlock(t, aggregate, "dbb"); !strings.Contains(block, `            name: "dbsecret"`+"\n") {
		t.Errorf("the first reader's attachment does not pin the format's key:\n%s", block)
	}
}

// TestAttachRuleWhenNotHolding classes a node of a rule's type whose When does
// not hold, or which no instance reads at a credential slot, as any other
// node: here drafted from the source, the reader's credential reported
// flowing.
func TestAttachRuleWhenNotHolding(t *testing.T) {
	for _, c := range []struct {
		case_ string
		nodes func() []*Node
	}{
		{"its When fails", func() []*Node {
			return []*Node{toyReader("db", readsSecret("dbSecret")), toySecret("dbSecret", "orders-db", false)}
		}},
		{"no reader at a slot", func() []*Node {
			return []*Node{toySecret("dbSecret", "orders-db", true)}
		}},
	} {
		t.Run(c.case_, func(t *testing.T) {
			cls, out := attached(t, "toy", []AttachRule{generatedSecret()}, c.nodes)
			if cl := classOf(t, cls, "dbSecret"); cl.Class != ClassDrafted {
				t.Fatalf("the secret is classed %s, want drafted", cl.Class)
			}
			if !draftsFromSource(out) || !strings.Contains(fileOf(t, out, "aggregate.yaml"), "    dbsecret:\n") {
				t.Errorf("the secret is not drafted, files %q", filePaths(out))
			}
			onlyCode(t, out.Diagnostics, CodeResourceFolded, 0)
		})
	}
}

// TestAttachRuleThatCannotBeReadHalts halts source-refused, classing nothing,
// on an attach rule naming no Feature, an AttachParent rule naming no slot, a
// rule giving the readers after the first another type than the kit's
// referenced secret, and a rule attaching on no instance the core knows.
func TestAttachRuleThatCannotBeReadHalts(t *testing.T) {
	for _, c := range []struct {
		case_ string
		edit  func(r *AttachRule)
		names string
	}{
		{"no feature", func(r *AttachRule) { r.Feature = "" }, "no Feature"},
		{"a parent rule with no slot", func(r *AttachRule) { r.Via = AttachParent }, "no slot"},
		{"other readers of another type", func(r *AttachRule) { r.Others = "core/secret-value" }, "core/secret-value"},
		{"no instance", func(r *AttachRule) { r.Via = AttachVia(7) }, "no instance"},
	} {
		t.Run(c.case_, func(t *testing.T) {
			rule := generatedSecret()
			c.edit(&rule)
			f := toyFormat(nil)
			f.Attach = []AttachRule{rule}
			g := &Graph{Nodes: []*Node{toyReader("db", readsSecret("dbSecret")), toySecret("dbSecret", "", true)}}
			cls, diags := Classify(toyInput(t, "toy", "", nil), f, g)
			if cls != nil || len(diags) != 1 || diags[0].Code != string(CodeSourceRefused) {
				t.Fatalf("Classify returns %v and %+v, want nothing classed and one source-refused halt", classes(cls),
					diags)
			}
			mentions(t, "the halt", diags[0].Message, toySecretType, c.names, "importer's authors")
		})
	}
}

// TestSecretRuleTakesADataSource attaches a secret the source reads and does
// not create, read at a credential slot, as the kit's referenced secret in the
// Release entry of the instance reading it, its origin the placement keyed
// origin; a rule without Secret takes no such node, which is classed existing.
func TestSecretRuleTakesADataSource(t *testing.T) {
	read := func() []*Node {
		secret := toySecret("dbSecret", "orders-db", false)
		secret.Existing = true
		return []*Node{toyReader("db", readsSecret("dbSecret")), secret}
	}

	t.Run("a rule with Secret", func(t *testing.T) {
		cls, out := attached(t, "cloudformation", []AttachRule{keptSecret()}, read)
		cl := classOf(t, cls, "dbSecret")
		if cl.Class != ClassAttached || len(cl.Drafts) != 0 {
			t.Fatalf("the secret is classed %s, drafting %d, want attached, drafting nothing", cl.Class, len(cl.Drafts))
		}
		if len(cl.Attach) != 1 || cl.Attach[0].Into != "db" || cl.Attach[0].Slot != "admin_password" ||
			cl.Attach[0].Secret == nil || cl.Attach[0].Secret.Origin != "orders-db" {
			t.Errorf("the secret is attached as %+v, want a referenced secret of origin orders-db on db's admin_password",
				cl.Attach)
		}
		if aggregate := fileOf(t, out, "aggregate.yaml"); strings.Contains(aggregate, "referenced-secret") {
			t.Errorf("the Aggregate declares the referenced secret:\n%s", aggregate)
		}
		mentions(t, "the Release entry of db", instanceBlock(t, fileOf(t, out, "release.yaml"), "db"),
			"admin_password", "referenced-secret", "orders-db")
	})

	t.Run("a rule without Secret", func(t *testing.T) {
		rule := AttachRule{Type: toySecretType, Feature: "core/referenced-secret"}
		cls, _ := attached(t, "toy", []AttachRule{rule}, read)
		if cl := classOf(t, cls, "dbSecret"); cl.Class != ClassExisting || len(cl.Attach) != 0 {
			t.Errorf("the secret is classed %s attaching %+v, want existing and attached nowhere", cl.Class, cl.Attach)
		}
	})
}
