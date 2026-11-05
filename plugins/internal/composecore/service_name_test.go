// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package composecore

import (
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/pkg/namesegmenttest"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// withNonMainContainer adds one container named name to a workload carrying a
// main container, as a sidecar or as an init container, so a case can state the
// role once and exercise both through one fixture.
func withNonMainContainer(w *types.Workload, role, name string) {
	c := types.ContainerSpec{Image: "extra:1.0"}
	switch role {
	case "sidecar":
		w.Containers.Set(name, c)
	case "init":
		c.Name = name
		w.InitContainers = append(w.InitContainers, c)
	default:
		panic("unknown role " + role)
	}
}

// TestContainerServiceName_PairsThatJoinedToOneNameAssembleApart pins the pair a
// single-hyphen join folds, for a sidecar and for an init container: node `api`
// with a container `warm-cache` beside node `api-warm` with a container `cache`.
// Joined on one hyphen both spell `api-warm-cache`, so assembling the two nodes
// into one file halted on a service-name collision neither author declared. The
// two must now assemble into one file carrying two distinct services.
func TestContainerServiceName_PairsThatJoinedToOneNameAssembleApart(t *testing.T) {
	t.Parallel()
	for _, role := range []string{"sidecar", "init"} {
		role := role
		t.Run(role, func(t *testing.T) {
			t.Parallel()
			api := baseDegradeWorkload()
			withNonMainContainer(api, role, "warm-cache")
			apiWarm := baseDegradeWorkload()
			withNonMainContainer(apiWarm, role, "cache")

			files, _, err := AssembleBoundaryCore(
				map[string][]rendercore.EmittedFile{
					"api":      makeFragment(t, "api", api, nil),
					"api-warm": makeFragment(t, "api-warm", apiWarm, nil),
				},
				map[string]map[string]string{},
				nil, nil, "default", 0,
			)
			if err != nil {
				t.Fatalf("two nodes whose %s names differ must assemble into one file, got: %v", role, err)
			}
			var doc ComposeFile
			if err := yaml.Unmarshal(files[AssembledOutputFile], &doc); err != nil {
				t.Fatalf("parse assembled file: %v", err)
			}
			for _, want := range []string{"api", "api-warm", "api--warm-cache", "api-warm--cache"} {
				if _, ok := doc.Services[want]; !ok {
					t.Errorf("no service %q; services: %v", want, serviceNames(doc))
				}
			}
			if len(doc.Services) != 4 {
				t.Errorf("got %d services, want 4 — two main services and one per %s: %v",
					len(doc.Services), role, serviceNames(doc))
			}
		})
	}
}

// TestContainerServiceName_AnUnsafeContainerNameIsWrittenOut proves a container
// name carrying characters a compose service name does not admit — a space and
// a path separator — reaches the emitted file written out rather than as
// authored, for a sidecar and for an init container alike, and that every byte
// of the emitted service names is one compose admits.
func TestContainerServiceName_AnUnsafeContainerNameIsWrittenOut(t *testing.T) {
	t.Parallel()
	for _, role := range []string{"sidecar", "init"} {
		role := role
		t.Run(role, func(t *testing.T) {
			t.Parallel()
			w := baseDegradeWorkload()
			withNonMainContainer(w, role, "warm cache/x")

			files, _, err := SynthesizeWorkloadCore(w, nodeCtxFor("api"))
			if err != nil {
				t.Fatalf("synth: %v", err)
			}
			var frag ComposeFile
			if err := yaml.Unmarshal(files[0].Body, &frag); err != nil {
				t.Fatalf("parse fragment: %v", err)
			}
			const want = "api--warm-zx000020cache-zsx"
			if _, ok := frag.Services[want]; !ok {
				t.Errorf("no service %q; services: %v", want, serviceNames(frag))
			}
			for name := range frag.Services {
				if !isComposeServiceName(name) {
					t.Errorf("service name %q carries a character compose does not admit", name)
				}
			}
		})
	}
}

// isComposeServiceName reports whether every byte of name is one a compose
// service name admits: a letter, a digit, a dot, an underscore or a hyphen. It is
// written out here rather than read from the encoding's alphabet, because a
// check that called into the encoder would agree with it whatever it admitted.
func isComposeServiceName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '.' || c == '_' || c == '-' {
			continue
		}
		return false
	}
	return true
}

// containerServiceNamePairs returns every (component identity, container name)
// pair the derivation's corpus assertions run over: the shared segment corpus
// crossed with itself, so the pairs that move a hyphen across the join are
// present — a corpus varying one position at a time could be entirely distinct
// and still never contain the pair a single-hyphen join folds.
func containerServiceNamePairs() [][2]string {
	corpus := namesegmenttest.Corpus('-')
	out := make([][2]string, 0, len(corpus)*len(corpus))
	for _, identity := range corpus {
		for _, name := range corpus {
			out = append(out, [2]string{identity, name})
		}
	}
	return out
}

// TestContainerServiceName_IsInjectiveAndRecoverable asserts no two distinct
// (component identity, container name) pairs in the corpus reach one service
// name, that each name reads back into exactly the pair it was built from, and
// that every byte it emits is one compose admits in a service name. The reader
// shares no code with the encoder, so it cannot agree with the encoder by
// construction.
func TestContainerServiceName_IsInjectiveAndRecoverable(t *testing.T) {
	t.Parallel()
	pairs := containerServiceNamePairs()
	seen := make(map[string][2]string, len(pairs))
	for _, pair := range pairs {
		name := containerServiceName(pair[0], pair[1])
		if prior, clash := seen[name]; clash {
			t.Fatalf("(%q,%q) and (%q,%q) both reach the service name %q",
				prior[0], prior[1], pair[0], pair[1], name)
		}
		seen[name] = pair
		if !isComposeServiceName(name) {
			t.Errorf("service name %q for (%q,%q) carries a character compose does not admit",
				name, pair[0], pair[1])
		}
		got := namesegmenttest.ReadName(t, '-', name)
		if len(got) != 2 || got[0] != pair[0] || got[1] != pair[1] {
			t.Errorf("service name %q read back as %q, want (%q,%q)", name, got, pair[0], pair[1])
		}
	}
}

// TestContainerServiceName_MatchesItsDocumentedExamples pins every worked example
// the compose provider reference gives for this derivation. The expected strings
// are written out rather than computed, because an expectation computed from the
// encoder would agree with it whatever it did.
func TestContainerServiceName_MatchesItsDocumentedExamples(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		identity, container string
		want                string
	}{
		{"api", "logger", "api--logger"},
		{"platform", "import-seed", "platform--import-seed"},
		{"api", "init-0", "api--init-0"},
		{"api", "warm-cache", "api--warm-cache"},
		{"api-warm", "cache", "api-warm--cache"},
		{"api", "warm cache/x", "api--warm-zx000020cache-zsx"},
	} {
		if got := containerServiceName(tc.identity, tc.container); got != tc.want {
			t.Errorf("containerServiceName(%q, %q) = %q, want %q", tc.identity, tc.container, got, tc.want)
		}
	}
}

// TestContainerServiceName_MainServiceKeepsTheIdentity pins the half of the
// naming the derivation deliberately does not cover: the main service carries
// the component identity as it is, beside a sidecar and an init container named
// through the derivation.
func TestContainerServiceName_MainServiceKeepsTheIdentity(t *testing.T) {
	t.Parallel()
	w := baseDegradeWorkload()
	withNonMainContainer(w, "sidecar", "logger")
	withNonMainContainer(w, "init", "prep")

	files, _, err := SynthesizeWorkloadCore(w, nodeCtxFor("db-server"))
	if err != nil {
		t.Fatalf("synth: %v", err)
	}
	var frag ComposeFile
	if err := yaml.Unmarshal(files[0].Body, &frag); err != nil {
		t.Fatalf("parse fragment: %v", err)
	}
	got := serviceNames(frag)
	want := []string{"db-server", "db-server--logger", "db-server--prep"}
	if len(got) != len(want) {
		t.Fatalf("services = %v, want %v", got, want)
	}
	for _, name := range want {
		if _, ok := frag.Services[name]; !ok {
			t.Errorf("no service %q; services: %v", name, got)
		}
	}
	assertWaitsFor(t, "db-server", frag.Services["db-server"], "db-server--prep")
	assertWaitsFor(t, "db-server--logger", frag.Services["db-server--logger"], "db-server--prep")
}

// TestContainerServiceName_AComponentNamedLikeADerivedNameKeepsItsOwn pins the
// reference page's example of why a component's own service cannot take a
// sidecar's or init container's name: a component written `api--prep` has its
// doubled hyphen written out in its main service name, so it and `api` with a
// sidecar `prep` assemble into two services.
func TestContainerServiceName_AComponentNamedLikeADerivedNameKeepsItsOwn(t *testing.T) {
	t.Parallel()
	api := baseDegradeWorkload()
	withNonMainContainer(api, "sidecar", "prep")

	files, _, err := AssembleBoundaryCore(
		map[string][]rendercore.EmittedFile{
			"api":       makeFragment(t, "api", api, nil),
			"api--prep": makeFragment(t, "api--prep", baseDegradeWorkload(), nil),
		},
		map[string]map[string]string{},
		nil, nil, "default", 0,
	)
	if err != nil {
		t.Fatalf("a component named like another's derived service name must still assemble, got: %v", err)
	}
	var doc ComposeFile
	if err := yaml.Unmarshal(files[AssembledOutputFile], &doc); err != nil {
		t.Fatalf("parse assembled file: %v", err)
	}
	for _, want := range []string{"api", "api--prep", "api-zh-prep"} {
		if _, ok := doc.Services[want]; !ok {
			t.Errorf("no service %q; services: %v", want, serviceNames(doc))
		}
	}
}
