// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armcore

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/armcore/armexpr"
)

// contribution is one contributor's fragment as a test writes it.
type contribution struct{ contributor, content string }

// levelOf builds a level from fragments: each entry is owned by the contributor
// that wrote it, under the pointer of the section and name it is written at.
func levelOf(t *testing.T, contributions ...contribution) *level {
	t.Helper()
	l := newLevel()
	for _, c := range contributions {
		frag, _, err := ReadFragment([]byte(c.content))
		if err != nil {
			t.Fatalf("ReadFragment(%s): %v", c.contributor, err)
		}
		for _, section := range sortedKeys(frag.Sections) {
			for _, name := range sortedKeys(frag.Sections[section]) {
				l.put(section, name, &levelEntry{
					body:        frag.Sections[section][name],
					contributor: c.contributor,
					pointer:     "/" + section + "/" + escapePointer(name),
				})
			}
		}
	}
	return l
}

// at returns the value a level holds under a JSON pointer whose first token is
// the section and whose second is the entry.
func at(t *testing.T, l *level, pointer string) any {
	t.Helper()
	tokens := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	entry, ok := l.sections[tokens[0]][tokens[1]]
	if !ok {
		t.Fatalf("no entry at %s", pointer)
	}
	v := entry.body
	for _, tok := range tokens[2:] {
		switch x := v.(type) {
		case map[string]any:
			v = x[tok]
		case []any:
			i, err := strconv.Atoi(tok)
			if err != nil || i >= len(x) {
				t.Fatalf("no item %s at %s", tok, pointer)
			}
			v = x[i]
		default:
			t.Fatalf("nothing under %s at %s", tok, pointer)
		}
	}
	return v
}

// checkLevel runs the checks the assembly runs over a merged level, in its
// order: composition, then the reference checks over what composition wrote.
func checkLevel(l *level) error {
	if _, err := l.composeLiterals(); err != nil {
		return err
	}
	return l.checkReferences()
}

// dbFragment declares the resource Db, a nested deployment, and the parameter
// location.
const dbFragment = `
parameters:
  location: {type: string}
resources:
  Db:
    type: Microsoft.Resources/deployments
    apiVersion: "2025-04-01"
    name: db
    properties:
      mode: Incremental
      template: {}
`

// appResource is a fragment holding the resource App with the given body lines,
// indented under it.
func appResource(body string) string {
	return "resources:\n  App:\n    type: Microsoft.App/containerApps\n    apiVersion: \"2024-03-01\"\n" + body
}

// appProperty is a fragment whose resource App writes value at properties.x.
func appProperty(value string) string {
	return appResource("    name: app\n    properties:\n      x: " + yamlQuote(value) + "\n")
}

func TestDanglingReferenceHalts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, content, pointer, missing string
	}{
		{"reference", appProperty("[reference('Nope').outputs.a.value]"), "/resources/App/properties/x", "Nope"},
		{"dependsOn", appResource("    name: app\n    dependsOn: [Nope]\n"), "/resources/App/dependsOn/0", "Nope"},
		{"listKeys", appProperty("[listKeys('Nope', '2023-01-01').keys[0].value]"), "/resources/App/properties/x", "Nope"},
		{"LISTKEYS in upper case", appProperty("[LISTKEYS('Nope', '2023-01-01').keys[0].value]"), "/resources/App/properties/x", "Nope"},
		{"list", appProperty("[list('Nope', '2023-01-01').properties]"), "/resources/App/properties/x", "Nope"},
		{"references", appProperty("[references('Nope')]"), "/resources/App/properties/x", "Nope"},
		{"resourceInfo", appProperty("[resourceInfo('Nope').id]"), "/resources/App/properties/x", "Nope"},
		{"parameters", appProperty("[parameters('missing')]"), "/resources/App/properties/x", "missing"},
		{"variables", appProperty("[variables('v')]"), "/resources/App/properties/x", "v"},
		{"symbolic name in another case", appProperty("[reference('db').outputs.a.value]"), "/resources/App/properties/x", "db"},
		{"rewritten literal", appProperty("x-[reference('Nope').outputs.a.value]"), "/resources/App/properties/x", "Nope"},
		{"output", "outputs:\n  host: {type: string, value: \"[reference('Nope').outputs.a.value]\"}\n", "/outputs/host/value", "Nope"},
		{"nested deployment parameters", `
resources:
  Nested:
    type: Microsoft.Resources/deployments
    apiVersion: "2025-04-01"
    name: nested
    properties:
      mode: Incremental
      parameters:
        p: {value: "[parameters('missing')]"}
      template: {}
`, "/resources/Nested/properties/parameters/p/value", "missing"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			l := levelOf(t, contribution{"payments-db", dbFragment}, contribution{"orders-api", c.content})
			err := checkLevel(l)
			if !errors.Is(err, plugincondition.ErrPluginRequiredInputUnresolved()) {
				t.Fatalf("want E0289, got %v", err)
			}
			for _, s := range []string{"arm-provider:", `"orders-api"`, c.pointer, strconv.Quote(c.missing)} {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("sentence lacks %s: %v", s, err)
				}
			}
		})
	}
}

func TestDanglingCheckResolvesWhatTheLevelDeclares(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, content string }{
		{"parameter in another case", appResource("    name: app\n    location: \"[PARAMETERS('Location')]\"\n")},
		{"resource ID", appProperty("[reference('/subscriptions/s/resourceGroups/r/providers/Microsoft.Storage/storageAccounts/a', '2023-01-01').primaryEndpoints.blob]")},
		{"list function by resource ID", appProperty("[listKeys('/subscriptions/s/resourceGroups/r/providers/Microsoft.Storage/storageAccounts/a', '2023-01-01').keys[0].value]")},
		{"symbolic name", appProperty("[reference('Db').outputs.fqdn.value]")},
		{"dependsOn symbolic name and resource ID", appResource("    name: app\n    dependsOn: [Db, /subscriptions/s/resourceGroups/r/providers/Microsoft.Storage/storageAccounts/a]\n")},
		{"variable in another case", "variables:\n  prefix: app\n" + appProperty("[Variables('Prefix')]")},
		{"variable declared by iteration", "variables:\n  copy:\n    - {name: disks, count: 2, input: \"[copyIndex('disks')]\"}\n" + appProperty("[variables('disks')]")},
		{"function reads its own parameter", `
functions:
  - namespace: my
    members:
      fn:
        parameters:
          - {name: n, type: int}
        output: {type: string, value: "[string(parameters('n'))]"}
`},
		{"rewritten literal", appProperty("postgres://[reference('Db').outputs.fqdn.value]:5432")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			l := levelOf(t, contribution{"payments-db", dbFragment}, contribution{"orders-api", c.content})
			if err := checkLevel(l); err != nil {
				t.Fatalf("want no halt, got %v", err)
			}
		})
	}
}

// The level's entries live in maps, so the halt reported first must not depend
// on map iteration: the entry first in name order halts on every run.
func TestDanglingReferenceHaltsTheSameWayEveryRun(t *testing.T) {
	t.Parallel()
	zed := "resources:\n  Zed:\n    type: Microsoft.Storage/storageAccounts\n    name: zed\n    properties:\n      x: \"[reference('Nope').outputs.a.value]\"\n"
	alpha := "resources:\n  Alpha:\n    type: Microsoft.Storage/storageAccounts\n    name: alpha\n    properties:\n      x: \"[parameters('missing')]\"\n"
	var first string
	for range 20 {
		l := levelOf(t, contribution{"zed-node", zed}, contribution{"alpha-node", alpha})
		err := l.checkReferences()
		if err == nil {
			t.Fatal("want a halt, got none")
		}
		if first == "" {
			first = err.Error()
			if !strings.Contains(first, "/resources/Alpha/properties/x") {
				t.Fatalf("the first halt is not the entry first in name order: %v", err)
			}
			continue
		}
		if err.Error() != first {
			t.Fatalf("halt differs between runs:\n%s\n%s", first, err)
		}
	}
}

func TestReferenceOutsidePropertiesHalts(t *testing.T) {
	t.Parallel()
	notRep := plugincondition.ErrPluginValueNotRepresentable()
	const ref = "\"[reference('Db').outputs.a.value]\""
	// Each halt names where the call may go instead.
	moveIt := []string{"properties", "output"}
	cases := []struct {
		name, content, pointer string
		cond                   error
		sentence               []string
	}{
		{"tags", appResource("    name: app\n    tags:\n      team: " + ref + "\n"), "/resources/App/tags/team", notRep, moveIt},
		{"name", appResource("    name: " + ref + "\n"), "/resources/App/name", notRep, moveIt},
		{"location", appResource("    name: app\n    location: " + ref + "\n"), "/resources/App/location", notRep, moveIt},
		{"variable", "variables:\n  v: " + ref + "\n", "/variables/v", notRep, moveIt},
		{"list function in tags", appResource("    name: app\n    tags:\n      team: \"[listKeys('Db', '2023-01-01').keys[0].value]\"\n"), "/resources/App/tags/team", notRep, moveIt},
		{"rewritten literal in tags", appResource("    name: app\n    tags:\n      team: \"team-[reference('Db').outputs.a.value]\"\n"), "/resources/App/tags/team", notRep, moveIt},
		{"output loop count", "outputs:\n  hosts: {type: array, copy: {count: \"[length(reference('Db').outputs.a.value)]\", input: x}}\n", "/outputs/hosts/copy/count", notRep, moveIt},
		{"user-defined function", `
functions:
  - namespace: my
    members:
      fn:
        parameters: []
        output: {type: string, value: "[reference('Db').outputs.a.value]"}
`, "/functions/my.fn/output/value", notRep, []string{"user-defined function", "argument"}},
		{"resource properties", appProperty("[reference('Db').outputs.a.value]"), "", nil, nil},
		{"output value", "outputs:\n  host: {type: string, value: " + ref + "}\n", "", nil, nil},
		{"output loop input", "outputs:\n  hosts: {type: array, copy: {count: 2, input: " + ref + "}}\n", "", nil, nil},
		{"nested deployment parameters", `
resources:
  Nested:
    type: Microsoft.Resources/deployments
    apiVersion: "2025-04-01"
    name: nested
    properties:
      mode: Incremental
      parameters:
        p: {value: "[reference('Db').outputs.a.value]"}
      template: {}
`, "", nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			l := levelOf(t, contribution{"payments-db", dbFragment}, contribution{"orders-api", c.content})
			err := checkLevel(l)
			if c.cond == nil {
				if err != nil {
					t.Fatalf("want no halt, got %v", err)
				}
				return
			}
			if !errors.Is(err, c.cond) {
				t.Fatalf("want E0291, got %v", err)
			}
			for _, s := range append([]string{"arm-provider:", `"orders-api"`, c.pointer}, c.sentence...) {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("sentence lacks %s: %v", s, err)
				}
			}
		})
	}
}

func TestNestedTemplateBodiesAreNotScanned(t *testing.T) {
	t.Parallel()
	for _, typ := range []string{"Microsoft.Resources/deployments", "microsoft.resources/Deployments"} {
		t.Run(typ, func(t *testing.T) {
			t.Parallel()
			content := `
resources:
  Inner:
    type: ` + typ + `
    apiVersion: "2025-04-01"
    name: inner
    properties:
      mode: Incremental
      expressionEvaluationOptions: {scope: inner}
      template:
        languageVersion: "2.0"
        parameters:
          inner: {type: string}
        resources:
          Thing:
            type: Microsoft.Storage/storageAccounts
            name: "[parameters('inner')]"
            location: "[reference('Elsewhere').location]"
            dependsOn: [Elsewhere]
            properties:
              note: "x-[reference('Elsewhere').outputs.a.value]"
`
			l := levelOf(t, contribution{"inner-node", content})
			diags, err := l.composeLiterals()
			if err != nil || len(diags) != 0 {
				t.Fatalf("compose: err %v diags %v", err, diags)
			}
			note := "/resources/Inner/properties/template/resources/Thing/properties/note"
			if got := at(t, l, note); got != "x-[reference('Elsewhere').outputs.a.value]" {
				t.Errorf("nested template text rewritten: %v", got)
			}
			if err := l.checkReferences(); err != nil {
				t.Fatalf("want no halt, got %v", err)
			}
		})
	}
}

func TestIsSecretExpression(t *testing.T) {
	t.Parallel()
	secure := map[string]bool{"adminPassword": true}
	cases := []struct {
		expr string
		want bool
	}{
		{"[listKeys('Db', '2023-01-01').keys[0].value]", true},
		{"[LISTSECRETS('Db', '2023-01-01')]", true},
		{"[listOutputsWithSecureValues('Db', '2025-04-01').password]", true},
		{"[list('Db', '2023-01-01').properties]", true},
		{"[concat('k=', listKeys('Db', '2023-01-01').keys[0].value)]", true},
		{"[parameters('adminPassword')]", true},
		{"[parameters('ADMINPASSWORD')]", true},
		{"[format('{0}', parameters('adminPassword'))]", true},
		{"[parameters('location')]", false},
		{"[reference('Db').outputs.fqdn.value]", false},
		{"[my.listKeys('Db')]", false},
		{"[concat('listKeys', 'x')]", false},
	}
	for _, c := range cases {
		n, err := armexpr.Parse(c.expr)
		if err != nil {
			t.Fatalf("Parse(%s): %v", c.expr, err)
		}
		if got := IsSecretExpression(n, secure); got != c.want {
			t.Errorf("IsSecretExpression(%s) = %v, want %v", c.expr, got, c.want)
		}
	}
	n, _ := armexpr.Parse("[parameters('adminPassword')]")
	if IsSecretExpression(n, map[string]bool{"adminPassword": false}) {
		t.Error("a parameter marked false is read as secure")
	}
	if IsSecretExpression(n, nil) {
		t.Error("a parameter is read as secure with no secure parameters")
	}
}
