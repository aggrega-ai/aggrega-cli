// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
)

// TestPointerParameterIsNeverAnAggregateParameter writes nothing in the
// Aggregate for a parameter naming an existing resource, read into a values
// key, and declares it under no spec.parameters: the instance's Release entry
// names the value, with the value the parameters file supplies, and leaves it
// where the file supplies none. The same parameter not marked a pointer is a
// parameter of the Aggregate.
func TestPointerParameterIsNeverAnAggregateParameter(t *testing.T) {
	in := edgeView()
	in.Target = "arm"
	graph := func(p Parameter) *Graph {
		s := pgServer()
		s.Body = merged(leaf(text("westeurope"), pathLocation...), leaf(Param{Name: "SubnetId"}, pathVersion...))
		p.Name, p.Type = "SubnetId", "AWS::EC2::Subnet::Id"
		return &Graph{Nodes: []*Node{s}, Params: []Parameter{p}}
	}

	t.Run("supplied", func(t *testing.T) {
		_, aggregate, release, _, diags := released(t, in, releaseFormat(),
			graph(Parameter{Pointer: true, Supplied: text("subnet-0abc")}))
		if strings.Contains(aggregate, "parameters:") || strings.Contains(release, "parameters:") {
			t.Errorf("a draft declares the pointer under spec.parameters:\n%s\n%s", aggregate, release)
		}
		wantBlock(t, aggregate, "server", "    server:\n      type: core/datastore/postgres\n")
		warnsAbout(t, diags, CodeValueNotCarried, "server", "values.version", "SubnetId")
		block := instanceBlock(t, release, "server")
		for _, want := range []string{`values.version takes "subnet-0abc", from the template parameter SubnetId, ` +
			"the value the parameters file supplies", "never a parameter of the Aggregate"} {
			if !strings.Contains(block, want) {
				t.Errorf("the entry lacks %q:\n%s", want, block)
			}
		}
	})

	t.Run("not supplied", func(t *testing.T) {
		_, aggregate, release, _, _ := released(t, in, releaseFormat(), graph(Parameter{Pointer: true}))
		if strings.Contains(aggregate, "parameters:") || strings.Contains(release, "parameters:") {
			t.Errorf("a draft declares the pointer under spec.parameters:\n%s\n%s", aggregate, release)
		}
		block := instanceBlock(t, release, "server")
		for _, want := range []string{"# Left for the Release, since the source cannot supply it:",
			"#   - values.version, from the template parameter SubnetId, which names an existing resource"} {
			if !strings.Contains(block, want) {
				t.Errorf("the entry lacks %q:\n%s", want, block)
			}
		}
	})

	t.Run("not a pointer", func(t *testing.T) {
		_, aggregate, _, _, _ := released(t, in, releaseFormat(), graph(Parameter{Supplied: text("subnet-0abc")}))
		if !strings.Contains(aggregate, "  parameters:\n") || !strings.Contains(aggregate, "{{ .parameters.subnet_id }}") {
			t.Errorf("the Aggregate does not declare the parameter that names no existing resource:\n%s", aggregate)
		}
	})
}

// TestNotesOpenTheAggregate writes the notes a format adds to the graph in the
// Aggregate's opening comment, after the files read and not read, in order:
// each line of a note a comment line, escaped so it reads back as written once
// the Aggregate is evaluated as a template.
func TestNotesOpenTheAggregate(t *testing.T) {
	g := openingGraph()
	g.Notes = []string{"The transform AWS::Serverless-2016-10-31 is read.", "Rules are not read:\nnone holds {{ x }}."}
	_, text, _ := drafted(t, edgeView(), edgeFormat(nil), g)
	want := "# Sources read: main.json, nested/db.json.\n" +
		"# Not read: notes.json.\n" +
		"# The transform AWS::Serverless-2016-10-31 is read.\n" +
		"# Rules are not read:\n" +
		"# none holds {{`{{`}} x }}.\n" +
		"# Outputs of the source:\n"
	if !strings.HasPrefix(text, want) {
		t.Errorf("the Aggregate opens\n%s\nwant it to open\n%s", text, want)
	}
}

// TestEnvListReadsAMap reads an environment listed as an object, each member a
// variable named by its key and holding its value, where the row's EnvList
// names no member for either: an entry reading an attribute its producer's row
// provides is delivered from the connection under its variable's name, and an
// entry reading no other resource is named in the instance's Release entry and
// warned value-not-carried, as an item of a list is.
func TestEnvListReadsAMap(t *testing.T) {
	f := edgeFormat(nil)
	variables := []string{"properties", "environment", "variables"}
	f.Table.Generated[2].Env = &EnvList{Path: variables}
	a := app(leaf(fqdn, append(variables, "DB_HOST")...), leaf(text("prod"), append(variables, "MODE")...))
	in := edgeView()
	in.Target = "arm"
	_, aggregate, release, _, diags := released(t, in, f, &Graph{Nodes: []*Node{pgServer(), a}})
	wantBlock(t, aggregate, "app", `    app:
      type: core/compute/server
      uses:
        server:
          server:
            target: server
            inject:
              env:
                - in: "DB_HOST"
                  from: host
`)
	onlyCode(t, diags, CodeReferenceNotWired, 0)
	if want := "    server: {}\n" +
		"    # Environment variables the source sets and no connection delivers: MODE = prod.\n" +
		"    app: {}\n"; !strings.Contains(release, want) {
		t.Errorf("the entry of app does not name MODE alone, want\n%s\nin\n%s", want, release)
	}
	warnsAbout(t, diags, CodeValueNotCarried, "app", "MODE")
	onlyCode(t, diags, CodeValueNotCarried, 1)
}

// TestDraftProvidesOfTheFormat publishes what the format's DraftProvides lists
// from each resource drafted from the source, before the attributes its
// neighbours read: id alone publishes no name. A format listing none publishes
// the id and the name.
func TestDraftProvidesOfTheFormat(t *testing.T) {
	id := schemadraft.Provide{Key: "id", Attribute: []string{"id"}, Identifier: true}
	for _, c := range []struct {
		name  string
		list  []schemadraft.Provide
		wants []schemadraft.Provide
	}{
		{"id alone", []schemadraft.Provide{id}, []schemadraft.Provide{id}},
		{"none listed", nil, []schemadraft.Provide{id, {Key: "name", Attribute: []string{"name"}}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var written []schemadraft.Resource
			f := edgeFormat(&written)
			f.DraftProvides = c.list
			out, _, _ := drafted(t, edgeView(), f, &Graph{Nodes: []*Node{staticSite()}})
			if len(written) != 1 || !reflect.DeepEqual(written[0].Provides, c.wants) {
				t.Fatalf("the drafted Recipe publishes %+v, want %+v", written, c.wants)
			}
			if got := classOf(t, out, "site").Provides; !reflect.DeepEqual(got, c.wants) {
				t.Errorf("the drafted node publishes %+v, want %+v", got, c.wants)
			}
		})
	}
}
