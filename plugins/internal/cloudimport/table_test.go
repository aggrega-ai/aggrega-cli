// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

const sitesType = "Microsoft.Web/sites"

// generatedSitesRow is a generated row of sitesType, as the generator writes
// one from a Recipe.
func generatedSitesRow() Row {
	return Row{
		Key:       sitesType,
		Component: "core/compute/server",
		Category:  "compute",
		Recipe:    "server-app-service",
		Traits:    map[string]string{"component.aggrega.ai/type": "server"},
		Form:      FormRaw,
		Values: []Placement{
			{From: []string{"properties", "siteConfig", "linuxFxVersion"}, Key: "image"},
		},
		Origin: "generated arm/server-app-service/1.0.0/recipe.yaml",
	}
}

func TestHandRowsNeverContradictSilently(t *testing.T) {
	t.Run("a hand value over a generated one without a reason is refused", func(t *testing.T) {
		table := Table{
			Generated: []Row{generatedSitesRow()},
			Hand:      []Row{{Key: sitesType, Recipe: "server-web-app", Origin: "hand"}},
		}
		rows, err := table.Rows()
		var c *Contradiction
		if !errors.As(err, &c) {
			t.Fatalf("Rows() = %v, %v; want a *Contradiction", rows, err)
		}
		want := Contradiction{Key: sitesType, Field: "Recipe", Generated: "server-app-service", Hand: "server-web-app"}
		if !reflect.DeepEqual(*c, want) {
			t.Fatalf("contradiction = %+v, want %+v", *c, want)
		}
		for _, part := range []string{sitesType, "Recipe", "server-app-service", "server-web-app", "Supersedes"} {
			if !strings.Contains(err.Error(), part) {
				t.Errorf("error %q does not name %q", err.Error(), part)
			}
		}
	})

	t.Run("a hand placement over a generated one of its key without a reason is refused", func(t *testing.T) {
		table := Table{
			Generated: []Row{generatedSitesRow()},
			Hand: []Row{{Key: sitesType, Origin: "hand", Values: []Placement{
				{From: []string{"properties", "siteConfig", "windowsFxVersion"}, Key: "image"},
			}}},
		}
		_, err := table.Rows()
		var c *Contradiction
		if !errors.As(err, &c) {
			t.Fatalf("Rows() error = %v, want a *Contradiction", err)
		}
		want := Contradiction{Key: sitesType, Field: "Values.image",
			Generated: "properties.siteConfig.linuxFxVersion", Hand: "properties.siteConfig.windowsFxVersion"}
		if !reflect.DeepEqual(*c, want) {
			t.Fatalf("contradiction = %+v, want %+v", *c, want)
		}
	})

	t.Run("a hand value with a written reason supersedes the generated one", func(t *testing.T) {
		reason := "the Recipe reads the image from the container settings, not the runtime stack"
		hand := Row{Key: sitesType, Origin: "hand",
			Values:     []Placement{{From: []string{"properties", "siteConfig", "windowsFxVersion"}, Key: "image"}},
			Supersedes: map[string]string{"Values.image": reason}}
		rows, err := Table{Generated: []Row{generatedSitesRow()}, Hand: []Row{hand}}.Rows()
		if err != nil {
			t.Fatalf("Rows() error = %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("rows = %+v, want the one merged row", rows)
		}
		wantValues := []Placement{{From: []string{"properties", "siteConfig", "windowsFxVersion"}, Key: "image"}}
		if !reflect.DeepEqual(rows[0].Values, wantValues) {
			t.Errorf("values = %+v, want the hand placement %+v", rows[0].Values, wantValues)
		}
		if rows[0].Recipe != "server-app-service" || rows[0].Supersedes["Values.image"] != reason {
			t.Errorf("merged row = %+v, want the generated Recipe and the hand reason", rows[0])
		}
	})

	t.Run("a hand field over an unset one is merged", func(t *testing.T) {
		gen := generatedSitesRow()
		gen.When = []Pred{{Path: []string{"kind"}, Op: OpContains, Value: "app"},
			{Path: []string{"properties", "siteConfig"}, Op: OpPresent}}
		hand := Row{Key: sitesType, Origin: "hand",
			// The same When, written in another order.
			When: []Pred{{Path: []string{"properties", "siteConfig"}, Op: OpPresent},
				{Path: []string{"kind"}, Op: OpContains, Value: "app"}},
			// The generated value again needs no reason.
			Recipe: "server-app-service",
			Traits: map[string]string{"compute.aggrega.ai/runtime": "container"},
			Values: []Placement{{From: []string{"properties", "siteConfig", "appCommandLine"}, Key: "command"}},
			Env:    &EnvList{Path: []string{"properties", "siteConfig", "appSettings"}, Name: "name", Value: "value"},
		}
		other := Row{Key: "Microsoft.Web/staticSites", Component: "core/web/static-site", Recipe: "static-site",
			Origin: "hand"}
		rows, err := Table{Generated: []Row{gen}, Hand: []Row{other, hand}}.Rows()
		if err != nil {
			t.Fatalf("Rows() error = %v", err)
		}
		want := gen
		want.Traits = map[string]string{"component.aggrega.ai/type": "server", "compute.aggrega.ai/runtime": "container"}
		want.Values = append(append([]Placement(nil), gen.Values...), hand.Values...)
		want.Env = hand.Env
		want.Origin = gen.Origin + " and hand"
		if len(rows) != 2 {
			t.Fatalf("rows = %+v, want the merged row and the hand row of another key", rows)
		}
		if !reflect.DeepEqual(rows[0], want) {
			t.Errorf("merged row = %+v,\nwant %+v", rows[0], want)
		}
		if !reflect.DeepEqual(rows[1], other) {
			t.Errorf("second row = %+v, want the hand row no generated row shares a key with, %+v", rows[1], other)
		}
	})

	t.Run("two hand rows of one key and When are refused", func(t *testing.T) {
		when := []Pred{{Path: []string{"kind"}, Op: OpContains, Value: "functionapp"}}
		table := Table{Hand: []Row{
			{Key: sitesType, When: when, Component: "core/compute/function", Origin: "hand"},
			{Key: sitesType, When: when, Component: "core/compute/server", Origin: "hand"},
		}}
		_, err := table.Rows()
		var d *DuplicateRow
		if !errors.As(err, &d) {
			t.Fatalf("Rows() error = %v, want a *DuplicateRow", err)
		}
		if d.Key != sitesType || d.Origin != "hand" || !reflect.DeepEqual(d.When, when) {
			t.Fatalf("duplicate = %+v, want hand rows of %s when kind contains functionapp", *d, sitesType)
		}
	})
}

func TestHandRowsMergeWithTheirGeneratedRowRegardlessOfCase(t *testing.T) {
	gen := generatedSitesRow()
	gen.When = []Pred{{Path: []string{"kind"}, Op: OpContains, Value: "functionapp"}}
	hand := Row{Key: "microsoft.web/sites", Origin: "hand",
		When: []Pred{{Path: []string{"Kind"}, Op: OpContains, Value: "FunctionApp"}},
		Env:  &EnvList{Path: []string{"properties", "siteConfig", "appSettings"}, Name: "name", Value: "value"}}
	rows, err := Table{Generated: []Row{gen}, Hand: []Row{hand}}.Rows()
	if err != nil {
		t.Fatalf("Rows() error = %v", err)
	}
	if len(rows) != 1 || rows[0].Key != sitesType || rows[0].Env != hand.Env {
		t.Fatalf("rows = %+v, want the generated row of %s with the hand row's Env merged into it", rows, sitesType)
	}
}

// databasesFold folds the databases of a flexible server, the child type
// written as typ, writing the child's from into values.database.
func databasesFold(typ string, from ...string) Fold {
	return Fold{Type: typ, Key: []string{"values", "database"}, From: from, One: true}
}

func TestHandRowsMergeFoldsAndEquivalencesRegardlessOfCase(t *testing.T) {
	const (
		genChild  = "Microsoft.DBforPostgreSQL/flexibleServers/databases"
		handChild = "Microsoft.DBForPostgreSQL/flexibleServers/databases"
	)
	rows := func(gen, hand Row) ([]Row, error) {
		gen.Key, gen.Component, gen.Recipe, gen.Origin = serverType, postgres, "postgres-flexible-server", "generated"
		hand.Key, hand.Origin = serverType, "hand"
		return Table{Generated: []Row{gen}, Hand: []Row{hand}}.Rows()
	}
	genFold, handFold := databasesFold(genChild, "name"), databasesFold(handChild, "properties", "charset")
	genEq := Equivalence{Raw: []string{"properties", "version"}, Module: []string{"version"}}
	handEq := Equivalence{Raw: []string{"Properties", "Version"}, Module: []string{"serverVersion"}}

	t.Run("a hand fold over the generated fold of its child type without a reason is refused", func(t *testing.T) {
		_, err := rows(Row{Folds: []Fold{genFold}}, Row{Folds: []Fold{handFold}})
		var c *Contradiction
		if !errors.As(err, &c) {
			t.Fatalf("Rows() error = %v, want a *Contradiction", err)
		}
		want := Contradiction{Key: serverType, Field: "Folds." + genChild,
			Generated: "into values.database from name, one value",
			Hand:      "into values.database from properties.charset, one value"}
		if !reflect.DeepEqual(*c, want) {
			t.Fatalf("contradiction = %+v, want %+v", *c, want)
		}
	})

	t.Run("a hand fold with a reason supersedes the generated fold of its child type", func(t *testing.T) {
		got, err := rows(Row{Folds: []Fold{genFold}}, Row{Folds: []Fold{handFold},
			Supersedes: map[string]string{"Folds." + handChild: "the Recipe names the database by its charset"}})
		if err != nil {
			t.Fatalf("Rows() error = %v", err)
		}
		if want := []Fold{handFold}; len(got) != 1 || !reflect.DeepEqual(got[0].Folds, want) {
			t.Fatalf("rows = %+v, want one row folding %+v", got, want)
		}
	})

	t.Run("a hand fold differing from the generated one only in case is the same fold", func(t *testing.T) {
		same := databasesFold(handChild, "Name")
		got, err := rows(Row{Folds: []Fold{genFold}}, Row{Folds: []Fold{same}})
		if err != nil {
			t.Fatalf("Rows() error = %v", err)
		}
		if len(got) != 1 || len(got[0].Folds) != 1 {
			t.Fatalf("rows = %+v, want one row holding one fold", got)
		}
	})

	t.Run("a hand equivalence over the generated one of its raw path without a reason is refused", func(t *testing.T) {
		_, err := rows(Row{Equivalences: []Equivalence{genEq}}, Row{Equivalences: []Equivalence{handEq}})
		var c *Contradiction
		if !errors.As(err, &c) {
			t.Fatalf("Rows() error = %v, want a *Contradiction", err)
		}
		want := Contradiction{Key: serverType, Field: "Equivalences.properties.version", Generated: "version",
			Hand: "serverVersion"}
		if !reflect.DeepEqual(*c, want) {
			t.Fatalf("contradiction = %+v, want %+v", *c, want)
		}
	})

	t.Run("a hand equivalence with a reason supersedes the generated one of its raw path", func(t *testing.T) {
		got, err := rows(Row{Equivalences: []Equivalence{genEq}}, Row{Equivalences: []Equivalence{handEq},
			Supersedes: map[string]string{"Equivalences.Properties.Version": "the module names it serverVersion"}})
		if err != nil {
			t.Fatalf("Rows() error = %v", err)
		}
		if want := []Equivalence{handEq}; len(got) != 1 || !reflect.DeepEqual(got[0].Equivalences, want) {
			t.Fatalf("rows = %+v, want one row holding %+v", got, want)
		}
	})
}

func TestHandRowsOfOneOriginDifferingOnlyInCaseAreDuplicates(t *testing.T) {
	table := Table{Generated: []Row{
		{Key: sitesType, Component: "core/compute/server", Origin: "generated"},
		{Key: "microsoft.web/sites", Component: "core/compute/server", Origin: "generated"},
	}}
	_, err := table.Rows()
	var d *DuplicateRow
	if !errors.As(err, &d) {
		t.Fatalf("Rows() error = %v, want a *DuplicateRow", err)
	}
	if d.Key != "microsoft.web/sites" || d.Origin != "generated" || d.When != nil {
		t.Fatalf("duplicate = %+v, want the second generated row, microsoft.web/sites", *d)
	}
}
