// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
)

// The test in this file drives what an author who declares a Loopback service
// entry meets, through the real pull and build and the providers embedded in the
// binary: compose publishes the entry's ports on the host's loopback address, a
// Kubernetes target refuses the entry by name, a workload with no Loopback entry
// renders the ports it always did, and a misspelled type is refused when the
// Workload is validated. Every refusal is matched by the condition it names and
// by the sentence a reader is shown.

// loopbackBaseServices is the services block every row starts from: a NodePort
// entry publishing a stream port and a datagram port, and a ClusterIP entry.
const loopbackBaseServices = `    services:
      public:
        type: NodePort
        ports:
          http: 8080
          syslog: 5514
      internal:
        type: ClusterIP
        ports:
          metrics: 9090
`

// loopbackEntry adds a Loopback entry naming the ClusterIP entry's port and the
// datagram port the NodePort entry already publishes.
const loopbackEntry = `      local:
        type: Loopback
        ports:
          metrics: 9090
          syslog: 5514
`

// loopbackFixture lays down a workspace whose one component is realized by a
// synthesized Recipe for provider, with services as its workload's services
// block. Every entity is in the workspace's catalog directory, so nothing
// reaches a registry.
func loopbackFixture(t *testing.T, root, provider, services string) (catalogDir, releasePath string) {
	t.Helper()
	catalogDir = filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(catalogDir, "component/example/compute/web/1.0.0/component.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: example/compute/web
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
    component.aggrega.ai/category: compute
spec:
  schema:
    infra:
      image:
        type: string
        default: "nginx:1.27"
`)
	writeFixture(t, filepath.Join(catalogDir, "recipe/"+provider+"/web/1.0.0/recipe.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: web
  version: 1.0.0
spec:
  capability: `+provider+`
  implements: example/compute/web:1.0.0
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    containers:
      main:
        image: "{{ optional .infra `+"`image` `nginx:1.27`"+` }}"
        ports:
          http: 8080
          metrics: 9090
          syslog:
            port: 5514
            protocol: UDP
`+services)
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: site
  version: "1.0.0"
spec:
  components:
    web:
      type: example/compute/web
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: site-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        example/compute/web:1.0.0: `+provider+`/web:1.0.0
`)
	releasePath = filepath.Join(root, "release.yaml")
	writeFixture(t, releasePath, `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: site-release
  environment: testing
  version: "1.0.0"
spec:
  aggregate: site
  manifest: site-manifest
  boundaries:
    app:
      directory: app
      components:
        web: {}
`)
	return catalogDir, releasePath
}

// buildLoopbackFixture pulls and builds the fixture, returning the workspace
// root and the build's error. A pull failure fails the test: every row's fault is
// one the build meets, not one the pull does.
func buildLoopbackFixture(t *testing.T, provider, services string) (string, error) {
	t.Helper()
	root := t.TempDir()
	catalogDir, rel := loopbackFixture(t, root, provider, services)
	if err := pullRun(context.Background(), pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: rel,
		CatalogDirs: []string{catalogDir},
		DiagOut:     io.Discard,
		Out:         io.Discard,
		diag:        buildDiagOptions{quiet: true},
	}); err != nil {
		t.Fatalf("pull: %v", err)
	}
	return root, dispatchBuildInRoot(t, resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		rel, []string{catalogDir}, false, io.Discard, io.Discard)
}

// composePublishedPorts reads the web service's ports off the docker-compose.yaml
// the build assembled.
func composePublishedPorts(t *testing.T, root string) []string {
	t.Helper()
	body := mustReadFile(t, filepath.Join(root, "dist", "app", "compose", "docker-compose.yaml"))
	var doc struct {
		Services map[string]struct {
			Ports []string `yaml:"ports"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse docker-compose.yaml: %v\n%s", err, body)
	}
	web, ok := doc.Services["web"]
	if !ok {
		t.Fatalf("docker-compose.yaml declares no web service:\n%s", body)
	}
	return web.Ports
}

// TestLoopbackServiceEntryThroughTheBuild holds each row against what the build
// wrote or the condition it halted with. The two no-Loopback rows are the
// controls: they render exactly what a workload declaring no Loopback entry
// always rendered, so the rows beside them read a difference the value made.
func TestLoopbackServiceEntryThroughTheBuild(t *testing.T) {
	wasmtest.RequireEmbeddedEntities(t)
	t.Parallel()

	t.Run("compose without a Loopback entry publishes every port on every interface", func(t *testing.T) {
		t.Parallel()
		root, err := buildLoopbackFixture(t, "compose", loopbackBaseServices)
		if err != nil {
			t.Fatalf("build:\n%s", formatCompilerError(err))
		}
		if got, want := composePublishedPorts(t, root), []string{"5514:5514/udp", "8080:8080"}; !slices.Equal(got, want) {
			t.Errorf("published ports = %q, want %q", got, want)
		}
	})

	t.Run("compose publishes a Loopback entry's ports on the loopback address", func(t *testing.T) {
		t.Parallel()
		root, err := buildLoopbackFixture(t, "compose", loopbackBaseServices+loopbackEntry)
		if err != nil {
			t.Fatalf("build:\n%s", formatCompilerError(err))
		}
		// metrics is published by the Loopback entry alone, so it binds the
		// loopback address; syslog is also published by the NodePort entry, so it
		// keeps every interface rather than being narrowed.
		want := []string{"5514:5514/udp", "8080:8080", "127.0.0.1:9090:9090"}
		if got := composePublishedPorts(t, root); !slices.Equal(got, want) {
			t.Errorf("published ports = %q, want %q", got, want)
		}
	})

	t.Run("helm without a Loopback entry renders each Service with its own type", func(t *testing.T) {
		t.Parallel()
		root, err := buildLoopbackFixture(t, "helm", loopbackBaseServices)
		if err != nil {
			t.Fatalf("build:\n%s", formatCompilerError(err))
		}
		for file, want := range map[string]string{
			"web-public-service.yaml":   "type: NodePort",
			"web-internal-service.yaml": "type: ClusterIP",
		} {
			body := string(mustReadFile(t, filepath.Join(root, "dist", "app", "helm", file)))
			if !strings.Contains(body, want) {
				t.Errorf("%s does not carry %q:\n%s", file, want, body)
			}
		}
	})

	t.Run("helm refuses a Loopback entry by name", func(t *testing.T) {
		t.Parallel()
		root, err := buildLoopbackFixture(t, "helm", loopbackBaseServices+loopbackEntry)
		if err == nil {
			t.Fatal("a Loopback service entry built on helm; a Kubernetes Service cannot bind to a host's loopback address")
		}
		printed := formatCompilerError(err)
		if !errors.Is(err, plugincondition.ErrPluginValueNotRepresentable()) {
			t.Fatalf("the build's error does not name the value-not-representable condition:\n%s", printed)
		}
		want := `services.local: type "Loopback" publishes the service's ports on the host's loopback address only, ` +
			`and a Kubernetes Service has no type that does`
		if !strings.Contains(printed, want) {
			t.Errorf("the printed diagnostic does not name the entry, the value and the target.\n  want: %s\n  got:\n%s", want, printed)
		}
		// Never a silent downgrade: no Service is written for the entry at all.
		if _, statErr := os.Stat(filepath.Join(root, "dist", "app", "helm", "web-local-service.yaml")); statErr == nil {
			t.Errorf("a Service was written for the refused Loopback entry")
		}
	})

	t.Run("a misspelled type is refused when the Workload is validated", func(t *testing.T) {
		t.Parallel()
		misspelled := strings.Replace(loopbackBaseServices, "type: NodePort", "type: Loopbak", 1)
		_, err := buildLoopbackFixture(t, "compose", misspelled)
		if err == nil {
			t.Fatal("a Workload whose service type is misspelled built")
		}
		printed := formatCompilerError(err)
		if !errors.Is(err, validator.ErrWorkloadValidation()) {
			t.Fatalf("the build's error does not name the Workload validation condition:\n%s", printed)
		}
		if !strings.Contains(printed, "Loopbak") {
			t.Errorf("the printed diagnostic does not quote the misspelled value:\n%s", printed)
		}
	})
}
