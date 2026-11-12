// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package composecore

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
)

// TestComposeProfiles_SurviveTheSourcedRenderAndTheMerge drives a sourced compose
// file through the render and the merge: a service's profiles reach the
// assembled file in the order the source lists them, and a service without
// profiles emits none.
func TestComposeProfiles_SurviveTheSourcedRenderAndTheMerge(t *testing.T) {
	t.Parallel()

	source := `services:
  migrate:
    image: example/migrate:1
    profiles:
      - manual
      - ops
  app:
    image: example/app:1
`
	rendered, err := RenderSourcedCore([]byte(source), "compose.yaml", nil, nodeCtxFor("app"))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	assembled, err := assembleOne(t, renderer.EmittedFile{
		Name: "app.compose-fragment.yaml", Origin: renderer.EmitOriginSource,
		Body: rendered.Files["app.compose-fragment.yaml"], Intermediate: true,
	})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if !strings.Contains(assembled, "    profiles:\n      - manual\n      - ops\n") {
		t.Errorf("assembled file does not carry the profiles in author order:\n%s", assembled)
	}
	if strings.Count(assembled, "profiles:") != 1 {
		t.Errorf("want profiles on the one service that wrote them:\n%s", assembled)
	}
}

// TestDecodeFragment_RefusesAKeyTheTypesDoNotCarry pins the refusal at every
// depth the types model, one uncarried key per row: the halt names the
// condition a plugin reports for a construct it has no expression for, the key,
// its path in the fragment and the fragment, and it is an error rather than a
// panic.
func TestDecodeFragment_RefusesAKeyTheTypesDoNotCarry(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, body, key, path string
	}{
		{"on a service", "services:\n  web:\n    image: a\n    stdin_open: true\n", "stdin_open", "services.web.stdin_open"},
		{"at the top level", "name: app\nservices:\n  web:\n    image: a\n", "name", "name"},
		// An extension member is carried only where a type holds one: the top
		// level and a service. Inside a top-level volume it is refused.
		{"an extension member inside a top-level volume", "volumes:\n  data:\n    x-backup: nightly\n", "x-backup", "volumes.data.x-backup"},
		{"an extension member inside healthcheck", "services:\n  web:\n    healthcheck:\n      test: [CMD, true]\n      x-note: a\n", "x-note", "services.web.healthcheck.x-note"},
		{"inside deploy", "services:\n  web:\n    deploy:\n      replicas: 2\n", "replicas", "services.web.deploy.replicas"},
		{"inside deploy.resources.reservations", "services:\n  web:\n    deploy:\n      resources:\n        reservations:\n          memory: 1g\n          pids: 10\n", "pids", "services.web.deploy.resources.reservations.pids"},
		{"inside a reservations device entry", "services:\n  web:\n    deploy:\n      resources:\n        reservations:\n          devices:\n            - driver: nvidia\n              count: 1\n              capabilities: [gpu]\n              shared: true\n", "shared", "services.web.deploy.resources.reservations.devices[0].shared"},
		{"inside healthcheck", "services:\n  web:\n    healthcheck:\n      test: [CMD, true]\n      start_interval: 5s\n", "start_interval", "services.web.healthcheck.start_interval"},
		{"inside a volume entry", "services:\n  web:\n    volumes:\n      - type: bind\n        source: ./a\n        target: /a\n        bind:\n          create_host_path: true\n", "bind", "services.web.volumes[0].bind"},
		{"inside a long-form depends_on entry", "services:\n  db:\n    image: a\n  web:\n    depends_on:\n      db:\n        condition: service_started\n        restart: true\n", "restart", "services.web.depends_on.db.restart"},
		{"on a top-level volume", "volumes:\n  data:\n    external: true\n", "external", "volumes.data.external"},
		{"on a top-level config", "configs:\n  c:\n    content: x\n    name: c\n", "name", "configs.c.name"},
		// The anchor sits inside a model body, which carries every member, so the
		// one key refused is the one the merge key brings into the service.
		{"brought in by a merge key", "models:\n  m:\n    model: ai/m\n    x-base: &base\n      stdin_open: true\nservices:\n  web:\n    <<: *base\n    image: a\n", "stdin_open", "services.web.stdin_open"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var err error
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("decode panicked: %v", r)
					}
				}()
				_, err = decodeFragment([]byte(tc.body), "web", `fragment "web.compose-fragment.yaml"`)
			}()
			if err == nil {
				t.Fatalf("decode accepted a fragment writing %q", tc.key)
			}
			if !errors.Is(err, plugincondition.ErrPluginCannotExpressInput()) {
				t.Errorf("refusal does not carry the cannot-express condition: %v", err)
			}
			msg := err.Error()
			for _, want := range []string{
				`"` + tc.key + `" at ` + tc.path,
				`fragment "web.compose-fragment.yaml"`,
				`node "web"`,
				// Both places such a key comes from, each with its remedy.
				"where it is written in the fragment, remove it from it",
				"whose Recipe declares no valuesMapping:, the node's values are laid over that file as they are, so declare a valuesMapping: that places each value under a key the provider carries",
			} {
				if !strings.Contains(msg, want) {
					t.Errorf("refusal does not name %q:\n%s", want, msg)
				}
			}
		})
	}
}

// TestDecodeFragment_NamesEveryUncarriedKeyInDocumentOrder pins that one refusal
// names every key the fragment would lose, in the order the fragment writes
// them, so an author fixes them in one pass.
func TestDecodeFragment_NamesEveryUncarriedKeyInDocumentOrder(t *testing.T) {
	t.Parallel()

	_, err := decodeFragment([]byte("services:\n  web:\n    tty: true\n    image: a\n    stdin_open: true\n"), "web", `fragment "f.yaml"`)
	if err == nil {
		t.Fatal("decode accepted two uncarried keys")
	}
	msg := err.Error()
	first := strings.Index(msg, `"tty" at services.web.tty`)
	second := strings.Index(msg, `"stdin_open" at services.web.stdin_open`)
	if first < 0 || second < 0 || first > second {
		t.Errorf("want both keys named in document order:\n%s", msg)
	}
	if !strings.Contains(msg, "writes keys the compose provider") || !strings.Contains(msg, "where they are written in the fragment, remove them from it") {
		t.Errorf("want the refusal worded for several keys:\n%s", msg)
	}
}

// TestDecodeFragment_RefusesANetworkTheFragmentDeclares pins that a fragment's
// own top-level networks: entry is refused as a whole, whatever its body holds,
// one entry per row: the halt names the cannot-express condition, the entry and
// its path, the fragment and the node, and points at the namespace annotation
// and the compose-bridge Feature. An entry carrying a key no network declaration
// models is refused as an entry, not reported key by key.
func TestDecodeFragment_RefusesANetworkTheFragmentDeclares(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, body string }{
		{"an entry with an empty body", "networks:\n  back: {}\n"},
		{"an entry with a null body", "networks:\n  back:\n"},
		{"an entry writing only what a network declaration models", "networks:\n  back:\n    driver: bridge\n    labels:\n      tier: back\n"},
		{"an entry writing a key no network declaration models", "networks:\n  back:\n    internal: true\n"},
		{"an entry beside a service", "services:\n  web:\n    image: a\n    networks: [back]\nnetworks:\n  back: {}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var err error
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("decode panicked: %v", r)
					}
				}()
				_, err = decodeFragment([]byte(tc.body), "web", `fragment "web.compose-fragment.yaml"`)
			}()
			if err == nil {
				t.Fatal("decode accepted a fragment declaring a network of its own")
			}
			if !errors.Is(err, plugincondition.ErrPluginCannotExpressInput()) {
				t.Errorf("refusal does not carry the cannot-express condition: %v", err)
			}
			msg := err.Error()
			for _, want := range []string{
				`node "web" fragment "web.compose-fragment.yaml" declares a network of its own under networks:, "back" at networks.back,`,
				"remove the entry from the fragment",
				"set aggrega.ai/namespace to choose the network a node joins",
				"attach the compose-bridge Feature to reach a service on another network",
			} {
				if !strings.Contains(msg, want) {
					t.Errorf("refusal does not carry %q:\n%s", want, msg)
				}
			}
			if strings.Contains(msg, "networks.back.") || strings.Contains(msg, "does not carry into the assembled file") {
				t.Errorf("the entry is refused as a whole, so nothing inside it is reported as a key:\n%s", msg)
			}
		})
	}
}

// TestDecodeFragment_OneRefusalNamesNetworksAndKeys pins the plural wording and
// that a fragment with both faults is refused once, naming every network entry
// and every uncarried key, so an author fixes both in one pass.
func TestDecodeFragment_OneRefusalNamesNetworksAndKeys(t *testing.T) {
	t.Parallel()

	_, err := decodeFragment([]byte("networks:\n  front: {}\n  back: {}\nservices:\n  web:\n    image: a\n    tty: true\n"), "web", `fragment "f.yaml"`)
	if err == nil {
		t.Fatal("decode accepted two network entries and an uncarried key")
	}
	if !errors.Is(err, plugincondition.ErrPluginCannotExpressInput()) {
		t.Errorf("refusal does not carry the cannot-express condition: %v", err)
	}
	msg := err.Error()
	for _, want := range []string{
		`declares networks of its own under networks:, "front" at networks.front; "back" at networks.back,`,
		"remove the entries from the fragment",
		`; and it writes a key the compose provider does not carry into the assembled file: "tty" at services.web.tty;`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal does not carry %q:\n%s", want, msg)
		}
	}
}

// TestDecodeFragment_NoNetworkDeclaredIsNoRefusal is the networks positive
// control: an empty networks: block, and a service naming a network in its own
// networks: list, declare no network of the fragment's own and decode.
func TestDecodeFragment_NoNetworkDeclaredIsNoRefusal(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, body string }{
		{"an empty block", "networks: {}\nservices:\n  web:\n    image: a\n"},
		{"a service's own networks list", "services:\n  web:\n    image: a\n    networks: [back]\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := decodeFragment([]byte(tc.body), "web", `fragment "f.yaml"`); err != nil {
				t.Fatalf("decode refused a fragment declaring no network of its own: %v", err)
			}
		})
	}
}

// TestComposeAssemble_AFragmentNetworkHaltsTheMerge drives the refusal through
// the merge an artifacts: fragment reaches: the assembly halts naming the entry
// and the fragment rather than writing a file without it.
func TestComposeAssemble_AFragmentNetworkHaltsTheMerge(t *testing.T) {
	t.Parallel()

	_, err := assembleOne(t, modelFragment("alpha", "services:\n  web:\n    image: a\nnetworks:\n  back: {}\n"))
	if !errors.Is(err, plugincondition.ErrPluginCannotExpressInput()) {
		t.Fatalf("got %v, want the cannot-express refusal of a fragment's own network", err)
	}
	if !strings.Contains(err.Error(), `node "alpha" fragment "alpha.compose-fragment.yaml" declares a network of its own under networks:, "back" at networks.back,`) {
		t.Errorf("refusal does not name the entry and the fragment:\n%v", err)
	}
}

// TestDecodeFragment_CarriesEveryDeclaredShape is the positive control: a
// fragment exercising every shape the walk reads — inline structs, inline maps
// taking every member, both forms of the self-decoding fields, the environment
// mapping, a merge key bringing in carried keys, and an empty document — decodes
// without a refusal.
func TestDecodeFragment_CarriesEveryDeclaredShape(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, body string }{
		{"empty", ""},
		{"every service shape", `services:
  db:
    image: a
  web:
    image: app:1
    profiles: [manual]
    environment:
      ANY_NAME_AT_ALL: "1"
    depends_on:
      db:
        condition: service_healthy
    models:
      llm:
        endpoint_var: U
        x-anything: kept
    deploy:
      resources:
        limits: {cpus: "1", memory: "1g"}
        reservations:
          cpus: "0.5"
          devices:
            - driver: nvidia
              count: all
              capabilities: [gpu]
              options: {any: thing}
    healthcheck:
      test: [CMD, "true"]
      interval: 5s
    volumes:
      - type: volume
        source: data
        target: /data
        read_only: true
models:
  llm:
    model: ai/smollm2
    x-extension: kept
volumes:
  data:
    driver: local
`},
		{"short forms", "services:\n  db:\n    image: a\n  web:\n    depends_on: [db]\n    models: [llm]\nmodels:\n  llm: {model: m}\n"},
		{"merge key bringing carried keys", "services:\n  base: &base\n    image: a\n    restart: always\n  web:\n    <<: *base\n"},
		// The common Compose idiom: a top-level extension member holds the anchor
		// a service's merge key brings in.
		{"merge key from a top-level extension member", "x-base: &base\n  image: a\n  restart: always\nservices:\n  web:\n    <<: *base\n"},
		{"extension members at the top level and on a service", "x-zeta: {b: 2}\nservices:\n  web:\n    image: a\n    x-note: kept\nx-alpha: kept\n"},
		// A trailing separator opens a document holding nothing, which is not a
		// second document of the fragment.
		{"a trailing document separator", "services:\n  web:\n    image: a\n---\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := decodeFragment([]byte(tc.body), "web", `fragment "f.yaml"`); err != nil {
				t.Fatalf("decode refused a fragment writing only carried keys: %v", err)
			}
		})
	}
}

// TestFragmentKeyShapes_EverySelfDecodingTypeDeclaresOne guards the one place the
// walk cannot read tags: a type that decodes itself has none, so it must declare
// its shape or be listed as opaque. A self-decoding type that does neither would
// have every key it is given refused; this test names it before any fragment
// meets it.
func TestFragmentKeyShapes_EverySelfDecodingTypeDeclaresOne(t *testing.T) {
	t.Parallel()

	unmarshaler := reflect.TypeOf((*yaml.Unmarshaler)(nil)).Elem()
	seen := map[reflect.Type]bool{}
	var undeclared []string
	var visit func(reflect.Type)
	visit = func(t reflect.Type) {
		if seen[t] {
			return
		}
		seen[t] = true
		if t.Kind() == reflect.Pointer {
			visit(t.Elem())
			return
		}
		if reflect.PointerTo(t).Implements(unmarshaler) || t.Implements(unmarshaler) {
			if !t.Implements(fragmentKeyShapeType) && !opaqueFragmentTypes[t] {
				undeclared = append(undeclared, t.String())
			}
			return
		}
		switch t.Kind() {
		case reflect.Struct:
			fields, _, _ := fragmentFields(t)
			for _, ft := range fields {
				visit(ft)
			}
		case reflect.Map, reflect.Slice, reflect.Array:
			visit(t.Elem())
		}
	}
	// ComposeFile and Service decode themselves to keep their extension members,
	// and Healthcheck and Deploy to carry a key written as null, so the visit
	// stops at each of them; the structs they walk through are visited as roots
	// of their own.
	visit(composeFileType)
	visit(composeFileFieldsType)
	visit(serviceFieldsType)
	visit(healthcheckFieldsType)
	visit(deployFieldsType)
	visit(reflect.TypeOf(dependsOnBody{}))
	visit(reflect.TypeOf(ServiceModelBinding{}))

	if len(undeclared) > 0 {
		t.Errorf("self-decoding types reachable from ComposeFile declare no key shape and are not opaque: %v", undeclared)
	}
	for _, reached := range []reflect.Type{
		reflect.TypeOf(DependsOn{}), reflect.TypeOf(ServiceModels{}),
		reflect.TypeOf(Service{}), reflect.TypeOf(Volume{}),
		reflect.TypeOf(Healthcheck{}), reflect.TypeOf(Deploy{}),
		reflect.TypeOf(DeviceRequest{}),
	} {
		if !seen[reached] {
			t.Errorf("the visit did not reach %v, so it proves nothing about it", reached)
		}
	}
}
