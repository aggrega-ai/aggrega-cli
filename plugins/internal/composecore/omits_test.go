// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package composecore

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
)

// omitsSource declares two units of every kind an entry can name, and nothing in
// any of them names another unit, so leaving any one out leaves a file compose
// accepts.
const omitsSource = `services:
  web:
    image: web:1
  db:
    image: postgres:16
volumes:
  data: {}
  cache: {}
configs:
  app:
    content: a
  extra:
    content: b
`

// omitEntries builds the entries a render is handed, each as kind/name.
func omitEntries(pairs ...string) []rendercore.ResolvedOmit {
	out := make([]rendercore.ResolvedOmit, 0, len(pairs))
	for i, p := range pairs {
		kind, name, _ := strings.Cut(p, "/")
		out = append(out, rendercore.ResolvedOmit{Entry: rendercore.OmitEntry{Kind: kind, Name: name}, Position: i})
	}
	return out
}

// renderOmitting renders source with values through the sourced core, handed
// entries, and returns the render and its one fragment parsed.
func renderOmitting(t *testing.T, source string, values map[string]any, entries []rendercore.ResolvedOmit) (SourcedRender, map[string]any, error) {
	t.Helper()
	nodeCtx := nodeCtxFor("app")
	nodeCtx.Omits = entries
	rendered, err := RenderSourcedCore([]byte(source), "compose.yaml", values, nodeCtx)
	if err != nil {
		return rendered, nil, err
	}
	body, ok := rendered.Files["app.compose-fragment.yaml"]
	if !ok || len(rendered.Files) != 1 {
		t.Fatalf("want the one fragment app.compose-fragment.yaml, got %d file(s)", len(rendered.Files))
	}
	var doc map[string]any
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse the fragment: %v\n%s", err, body)
	}
	return rendered, doc, nil
}

// sectionKeys returns the keys a fragment declares under one section, sorted;
// absent when the fragment carries no such section.
func sectionKeys(doc map[string]any, section string) ([]string, bool) {
	m, ok := doc[section].(map[string]any)
	if !ok {
		return nil, false
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys, true
}

// TestOmits_EachKindLeavesItsUnitOut leaves one unit of each kind out. The entry
// names the unit by its key; that unit is absent from the fragment, its count is
// one, and every other unit — of its own section and of the other two — is written
// exactly as the render handed no entries writes it.
func TestOmits_EachKindLeavesItsUnitOut(t *testing.T) {
	t.Parallel()

	_, control, err := renderOmitting(t, omitsSource, nil, nil)
	if err != nil {
		t.Fatalf("the control render: %v", err)
	}
	for _, tc := range []struct {
		entry, section, leftOut string
	}{
		{"service/db", "services", "db"},
		{"volume/cache", "volumes", "cache"},
		{"config/extra", "configs", "extra"},
	} {
		t.Run(tc.entry, func(t *testing.T) {
			t.Parallel()
			rendered, doc, err := renderOmitting(t, omitsSource, nil, omitEntries(tc.entry))
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if !slices.Equal(rendered.Omitted, []int{1}) {
				t.Errorf("counts %v, want [1]", rendered.Omitted)
			}
			for _, section := range []string{"services", "volumes", "configs"} {
				want := control[section].(map[string]any)
				got, _ := doc[section].(map[string]any)
				for name, body := range want {
					unit, kept := got[name]
					switch {
					case section == tc.section && name == tc.leftOut:
						if kept {
							t.Errorf("%s %q is still in the fragment", section, name)
						}
					case !kept:
						t.Errorf("%s %q was left out, and no entry names it", section, name)
					case !reflect.DeepEqual(unit, body):
						t.Errorf("%s %q differs from the control's: %v, want %v", section, name, unit, body)
					}
				}
			}
		})
	}
}

// TestOmits_TheCountsSayWhatEachEntryLeftOut reads the counts: an entry naming a
// key the document does not declare counts zero, which the host halts on, and its
// render is the control's; two entries naming one unit each count it; and a render
// handed no entries returns no counts at all, so its output carries none.
func TestOmits_TheCountsSayWhatEachEntryLeftOut(t *testing.T) {
	t.Parallel()

	control, _, err := renderOmitting(t, omitsSource, nil, nil)
	if err != nil {
		t.Fatalf("the control render: %v", err)
	}
	if control.Omitted != nil {
		t.Errorf("a render handed no entries returned counts %v, want none", control.Omitted)
	}

	stale, _, err := renderOmitting(t, omitsSource, nil, omitEntries("service/ghost"))
	if err != nil {
		t.Fatalf("an entry naming nothing: %v", err)
	}
	if !slices.Equal(stale.Omitted, []int{0}) {
		t.Errorf("an entry naming nothing counted %v, want [0]", stale.Omitted)
	}
	if !reflect.DeepEqual(stale.Files, control.Files) {
		t.Errorf("an entry naming nothing changed the fragment")
	}

	twice, doc, err := renderOmitting(t, omitsSource, nil, omitEntries("service/db", "volume/data", "service/db"))
	if err != nil {
		t.Fatalf("two entries naming one unit: %v", err)
	}
	if !slices.Equal(twice.Omitted, []int{1, 1, 1}) {
		t.Errorf("counts %v, want [1 1 1]", twice.Omitted)
	}
	if keys, _ := sectionKeys(doc, "services"); !slices.Equal(keys, []string{"web"}) {
		t.Errorf("services %v, want [web]", keys)
	}

	wrongSection, _, err := renderOmitting(t, omitsSource, nil, omitEntries("volume/db", "config/data"))
	if err != nil {
		t.Fatalf("entries naming a key under another section: %v", err)
	}
	if !slices.Equal(wrongSection.Omitted, []int{0, 0}) {
		t.Errorf("an entry matched a key of another kind's section: counts %v, want [0 0]", wrongSection.Omitted)
	}
}

// TestOmits_AnEntryNamingAKindComposeDoesNotDeclareIsRefused refuses every kind but
// the three, before anything is left out, under the condition naming a construct the
// provider has no expression for, with a sentence naming the entry and the kinds
// compose accepts. Kinds are compared exactly, so a capitalized spelling is refused.
// A model is refused too: the provider carries a file's models: section into the
// assembled file, and leaving one out is not among the kinds an entry may name.
func TestOmits_AnEntryNamingAKindComposeDoesNotDeclareIsRefused(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{"Service", "network", "secret", "Deployment", "model", ""} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			_, _, err := renderOmitting(t, omitsSource, nil, omitEntries("service/db", kind+"/web"))
			if !errors.Is(err, plugincondition.ErrPluginCannotExpressInput()) {
				t.Fatalf("got %v, want the condition naming a construct the provider cannot express", err)
			}
			for _, want := range []string{
				`compose: an omits entry (name "web") names kind "` + kind + `"`,
				"compose leaves out a service, a volume or a config, each named by its key under services:, volumes: or configs:, and no other kind",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not carry %q:\n%v", want, err)
				}
			}
		})
	}
}

// TestOmits_AnEntryWithANamespaceIsRefused refuses an entry naming a namespace,
// whatever its kind, because a compose file has none.
func TestOmits_AnEntryWithANamespaceIsRefused(t *testing.T) {
	t.Parallel()

	entries := omitEntries("service/db")
	entries[0].Entry.Namespace = "prod"
	_, _, err := renderOmitting(t, omitsSource, nil, entries)
	if !errors.Is(err, plugincondition.ErrPluginCannotExpressInput()) {
		t.Fatalf("got %v, want the condition naming a construct the provider cannot express", err)
	}
	for _, want := range []string{
		`compose: an omits entry (kind "service", name "db") names namespace "prod", and a compose file has no namespaces`,
		"remove the namespace from the entry",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not carry %q:\n%v", want, err)
		}
	}
}

// TestOmits_AnEntryWithASubchartIsRefused refuses an entry naming a subchart,
// whatever its kind, because a compose file has none.
func TestOmits_AnEntryWithASubchartIsRefused(t *testing.T) {
	t.Parallel()

	entries := omitEntries("service/db")
	entries[0].Entry.Subchart = "operator"
	_, _, err := renderOmitting(t, omitsSource, nil, entries)
	if !errors.Is(err, plugincondition.ErrPluginCannotExpressInput()) {
		t.Fatalf("got %v, want the condition naming a construct the provider cannot express", err)
	}
	for _, want := range []string{
		`compose: an omits entry (kind "service", name "db") names subchart "operator", and a compose file has no subcharts`,
		"remove the subchart from the entry",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not carry %q:\n%v", want, err)
		}
	}
}

// TestOmits_AUnitTheOverlayAddedIsLeftOut leaves out a service the values overlay
// added rather than the source: the match runs over the merged document.
func TestOmits_AUnitTheOverlayAddedIsLeftOut(t *testing.T) {
	t.Parallel()

	values := map[string]any{"services": map[string]any{"extra": map[string]any{"image": "extra:1"}}}
	_, control, err := renderOmitting(t, omitsSource, values, nil)
	if err != nil {
		t.Fatalf("the control render: %v", err)
	}
	if keys, _ := sectionKeys(control, "services"); !slices.Equal(keys, []string{"db", "extra", "web"}) {
		t.Fatalf("the overlay did not add the service, so the row below proves nothing: services %v", keys)
	}
	rendered, doc, err := renderOmitting(t, omitsSource, values, omitEntries("service/extra"))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !slices.Equal(rendered.Omitted, []int{1}) {
		t.Errorf("counts %v, want [1]", rendered.Omitted)
	}
	if keys, _ := sectionKeys(doc, "services"); !slices.Equal(keys, []string{"db", "web"}) {
		t.Errorf("services %v, want [db web]", keys)
	}
}

// TestOmits_ASectionEveryUnitOfWhichIsLeftOutIsRemoved leaves out the only volume a
// file declares: the fragment then carries no volumes section at all, as one that
// never declared one.
func TestOmits_ASectionEveryUnitOfWhichIsLeftOutIsRemoved(t *testing.T) {
	t.Parallel()

	const source = "services:\n  web:\n    image: web:1\nvolumes:\n  data: {}\n"
	_, doc, err := renderOmitting(t, source, nil, omitEntries("volume/data"))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if _, present := doc["volumes"]; present {
		t.Errorf("the fragment still carries a volumes section: %v", doc["volumes"])
	}
	if keys, _ := sectionKeys(doc, "services"); !slices.Equal(keys, []string{"web"}) {
		t.Errorf("services %v, want [web]", keys)
	}
}

// TestOmits_LeavingOutChangesNoInputOfTheRender holds that the values a render is
// handed are not changed by it: a section the overlay alone supplied is the very map
// the values carry, and leaving a unit of it out must not remove the unit from them.
func TestOmits_LeavingOutChangesNoInputOfTheRender(t *testing.T) {
	t.Parallel()

	values := map[string]any{"volumes": map[string]any{"scratch": map[string]any{}, "keep": map[string]any{}}}
	const source = "services:\n  web:\n    image: web:1\n"
	if _, _, err := renderOmitting(t, source, values, omitEntries("volume/scratch")); err != nil {
		t.Fatalf("render: %v", err)
	}
	volumes := values["volumes"].(map[string]any)
	if _, still := volumes["scratch"]; !still {
		t.Errorf("leaving a unit out removed it from the values the render was handed: %v", volumes)
	}
}

// referenceSource is a file with one referring service, web, whose body is webBody,
// beside the units a reference may name: the service db, the volumes data and d, and
// the configs app and spare. Nothing names spare, so an entry leaving it out is the
// control every row is read against.
func referenceSource(webBody string) string {
	return "services:\n  web:\n    image: web:1\n" + webBody +
		"  db:\n    image: postgres:16\n" +
		"volumes:\n  data: {}\n  d: {}\n" +
		"configs:\n  app:\n    content: a\n  spare:\n    content: b\n"
}

// TestOmits_ARemainingReferenceToALeftOutUnitHalts holds every place the compose
// specification defines for one entry of a file naming a service, a volume or a
// config of the same file. Each row writes one such reference, leaves the unit it
// names out, and requires the halt under the condition naming something the
// provider could not resolve, with the sentence naming the referring entry, the
// unit and where the reference is made. Every row is a control for the others: the
// same file builds once the entry names a unit nothing refers to.
func TestOmits_ARemainingReferenceToALeftOutUnitHalts(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		webBody string
		jobs    string
		entry   string
		finding string
	}{
		{"depends_on, the list", "    depends_on: [db]\n", "", "service/db", `service "web" names service "db" at depends_on[0]`},
		{"depends_on, the mapping", "    depends_on:\n      db:\n        condition: service_healthy\n        required: false\n", "", "service/db",
			`service "web" names service "db" at depends_on.db`},
		{"links, the service", "    links: [db]\n", "", "service/db", `service "web" names service "db" at links[0]`},
		{"links, the service and an alias", "    links: [\"db:database\"]\n", "", "service/db", `service "web" names service "db" at links[0]`},
		{"volumes_from, the service", "    volumes_from: [db]\n", "", "service/db", `service "web" names service "db" at volumes_from[0]`},
		{"volumes_from, the service and a mode", "    volumes_from: [\"db:ro\"]\n", "", "service/db", `service "web" names service "db" at volumes_from[0]`},
		{"network_mode", "    network_mode: \"service:db\"\n", "", "service/db", `service "web" names service "db" at network_mode`},
		{"ipc", "    ipc: \"service:db\"\n", "", "service/db", `service "web" names service "db" at ipc`},
		{"pid", "    pid: \"service:db\"\n", "", "service/db", `service "web" names service "db" at pid`},
		{"uts", "    uts: \"service:db\"\n", "", "service/db", `service "web" names service "db" at uts`},
		{"extends, the bare name", "    extends: db\n", "", "service/db", `service "web" names service "db" at extends`},
		{"extends, the mapping with no file", "    extends:\n      service: db\n", "", "service/db", `service "web" names service "db" at extends`},
		{"build.additional_contexts, the mapping", "    build:\n      context: .\n      additional_contexts:\n        base: \"service:db\"\n", "", "service/db",
			`service "web" names service "db" at build.additional_contexts.base`},
		{"build.additional_contexts, the list", "    build:\n      context: .\n      additional_contexts:\n        - \"base=service:db\"\n", "", "service/db",
			`service "web" names service "db" at build.additional_contexts[0]`},
		{"volumes, the short form", "    volumes: [\"data:/var/lib/data:ro\"]\n", "", "volume/data", `service "web" names volume "data" at volumes[0]`},
		{"volumes, the long form", "    volumes:\n      - type: volume\n        source: data\n        target: /var/lib/data\n", "", "volume/data",
			`service "web" names volume "data" at volumes[0]`},
		{"configs, the short form", "    configs: [app]\n", "", "config/app", `service "web" names config "app" at configs[0]`},
		{"configs, the long form", "    configs:\n      - source: app\n        target: /etc/app\n", "", "config/app", `service "web" names config "app" at configs[0]`},
		{"a pre_start hook", "    pre_start:\n      - image: busybox\n        volumes: [\"data:/seed\"]\n", "", "volume/data",
			`service "web" pre_start[0] names volume "data" at volumes[0]`},
		{"a job", "", "jobs:\n  seed:\n    image: busybox\n    triggers:\n      manual: true\n    depends_on: [db]\n", "service/db",
			`job "seed" names service "db" at depends_on[0]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source := referenceSource(tc.webBody) + tc.jobs
			_, _, err := renderOmitting(t, source, nil, omitEntries(tc.entry))
			if !errors.Is(err, plugincondition.ErrPluginRequiredInputUnresolved()) {
				t.Fatalf("got %v, want the halt on a reference to what an entry left out", err)
			}
			for _, want := range []string{
				"compose: what an omits entry left out is still named by the file: " + tc.finding,
				"compose refuses a file naming a service, a volume or a config it does not declare",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the halt does not carry %q:\n%v", want, err)
				}
			}

			// The control: the same file, an entry leaving out the one unit nothing
			// names, so what halted above is the reference and not the file.
			control, _, err := renderOmitting(t, source, nil, omitEntries("config/spare"))
			if err != nil {
				t.Fatalf("the control is refused, so the halt above is not the reference's: %v", err)
			}
			if !slices.Equal(control.Omitted, []int{1}) {
				t.Errorf("the control left out %v, want [1]", control.Omitted)
			}
		})
	}
}

// TestOmits_EveryRemainingReferenceIsReportedInOneOrder reports every reference in
// one message, services in key order and then jobs, each entry's hooks after it, so
// one file yields one message.
func TestOmits_EveryRemainingReferenceIsReportedInOneOrder(t *testing.T) {
	t.Parallel()

	const source = `services:
  zeta:
    image: z:1
    depends_on: [db]
  alpha:
    image: a:1
    volumes: ["data:/d"]
    depends_on: [db]
    pre_start:
      - image: busybox
        configs: [app]
  db:
    image: postgres:16
volumes:
  data: {}
configs:
  app:
    content: a
jobs:
  seed:
    image: busybox
    triggers:
      manual: true
    volumes_from: [db]
`
	_, _, err := renderOmitting(t, source, nil, omitEntries("service/db", "volume/data", "config/app"))
	if !errors.Is(err, plugincondition.ErrPluginRequiredInputUnresolved()) {
		t.Fatalf("got %v, want the halt on a reference to what an entry left out", err)
	}
	want := `service "alpha" names service "db" at depends_on[0]; ` +
		`service "alpha" names volume "data" at volumes[0]; ` +
		`service "alpha" pre_start[0] names config "app" at configs[0]; ` +
		`service "zeta" names service "db" at depends_on[0]; ` +
		`job "seed" names service "db" at volumes_from[0]; compose refuses`
	if !strings.Contains(err.Error(), want) {
		t.Errorf("the halt does not list every reference in order:\n%v\nwant it to carry:\n%s", err, want)
	}
}

// TestOmits_WhatNamesNoUnitOfTheFileIsNotAReference holds the places a value looks
// like a reference to a left-out unit and is not one: a container, a service of
// another file, a host path, a mount that is not a named volume, a config the
// loader accepts naming a platform config, and a unit the file never declared.
// Each renders, and the entry still left its unit out.
func TestOmits_WhatNamesNoUnitOfTheFileIsNotAReference(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, webBody, entry string
	}{
		{"volumes_from a container", "    volumes_from: [\"container:db\"]\n", "service/db"},
		{"network_mode a container", "    network_mode: \"container:db\"\n", "service/db"},
		{"external_links", "    external_links: [db]\n", "service/db"},
		{"extends a service of another file", "    extends:\n      file: common.yaml\n      service: db\n", "service/db"},
		{"a volume source that is a host path", "    volumes: [\"./data:/d\", \"/data:/e\", \"~/data:/f\"]\n", "volume/data"},
		{"a volume source that is a drive letter", "    volumes: [\"d:/data\"]\n", "volume/d"},
		{"an anonymous volume", "    volumes: [\"data\", \"/data\"]\n", "volume/data"},
		{"a bind mount whose source matches a volume key", "    volumes:\n      - type: bind\n        source: data\n        target: /d\n", "volume/data"},
		{"credential_spec naming a config", "    credential_spec:\n      config: app\n", "config/app"},
		{"a reference to a unit the file never declared", "    depends_on: [ghost]\n", "service/db"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rendered, _, err := renderOmitting(t, referenceSource(tc.webBody), nil, omitEntries(tc.entry))
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if !slices.Equal(rendered.Omitted, []int{1}) {
				t.Errorf("counts %v, want [1]: the row did not leave its unit out", rendered.Omitted)
			}
		})
	}
}

// TestOmits_AWithheldFieldIsNeverQuoted renders the refusals of an entry whose fields
// are withheld — the shape a marked entry arrives in — and requires the placeholder
// where each field would be quoted and the value nowhere.
func TestOmits_AWithheldFieldIsNeverQuoted(t *testing.T) {
	t.Parallel()

	const secret = "withheld-plaintext"
	withheld := rendercore.OmitFieldsWithheld{Kind: true, Name: true, Namespace: true}

	t.Run("a namespace", func(t *testing.T) {
		t.Parallel()
		entries := []rendercore.ResolvedOmit{{Entry: rendercore.OmitEntry{Kind: "service", Name: secret, Namespace: secret}, Withheld: withheld}}
		_, _, err := renderOmitting(t, omitsSource, nil, entries)
		if !errors.Is(err, plugincondition.ErrPluginCannotExpressInput()) {
			t.Fatalf("got %v, want the refusal of a namespace", err)
		}
		if strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "names namespace "+redact.Placeholder()) {
			t.Errorf("the refusal quotes a withheld field, or does not show the placeholder:\n%v", err)
		}
	})
	t.Run("a kind", func(t *testing.T) {
		t.Parallel()
		entries := []rendercore.ResolvedOmit{{Entry: rendercore.OmitEntry{Kind: secret, Name: secret}, Withheld: withheld}}
		_, _, err := renderOmitting(t, omitsSource, nil, entries)
		if !errors.Is(err, plugincondition.ErrPluginCannotExpressInput()) {
			t.Fatalf("got %v, want the refusal of a kind", err)
		}
		if strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "(name "+redact.Placeholder()+") names kind "+redact.Placeholder()) {
			t.Errorf("the refusal quotes a withheld field, or does not show the placeholder:\n%v", err)
		}
	})
	t.Run("a reference to what it left out", func(t *testing.T) {
		t.Parallel()
		source := strings.ReplaceAll(referenceSource("    depends_on: [db]\n"), "db", secret)
		entries := []rendercore.ResolvedOmit{{Entry: rendercore.OmitEntry{Kind: "service", Name: secret}, Withheld: withheld}}
		_, _, err := renderOmitting(t, source, nil, entries)
		if !errors.Is(err, plugincondition.ErrPluginRequiredInputUnresolved()) {
			t.Fatalf("got %v, want the halt on a reference to what the entry left out", err)
		}
		want := `service "web" names ` + redact.Placeholder() + " " + redact.Placeholder() + " at depends_on[0]"
		if strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), want) {
			t.Errorf("the halt quotes a withheld field, or does not carry %q:\n%v", want, err)
		}
	})
}
