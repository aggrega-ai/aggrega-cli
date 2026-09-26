// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package publishops

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestADefaultRegistryIsPublishedToAsTheHostItResolvedTo pins that a publication
// with no requested destination uses the workspace's default registry as the host
// resolution made it, and does not read that host through the alias match a
// second time.
//
// The configuration below is the shape a second reading would get wrong: its
// default is written as the name `local` and resolves to that entry's host, and
// another entry is named after that very host. Read again as a token, the host
// would select the second entry and publish somewhere a pull from the same
// default never reads. A requested destination, by contrast, is a token the
// resolver has not seen, so it is resolved here — the remaining cases hold that
// half.
func TestADefaultRegistryIsPublishedToAsTheHostItResolvedTo(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	doc := "workspace: {}\ndefault_registry: local\nregistries:\n" +
		"  - name: local\n    host: 127.0.0.1:5000\n" +
		"  - name: 127.0.0.1:5000\n    host: other.example.com\n"
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(doc), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	rw, err := workspaceconfig.Resolve(root, "", func(string) string { return "" })
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	cfg := rw.Config()
	if cfg.DefaultRegistry != "127.0.0.1:5000" {
		t.Fatalf("the default resolved to %q, want the host of the entry named local", cfg.DefaultRegistry)
	}
	cases := []struct {
		name, requested, want string
	}{
		{name: "no destination requested: the resolved default, as it is", want: "127.0.0.1:5000"},
		{name: "a requested alias is resolved", requested: "local", want: "127.0.0.1:5000"},
		{name: "a requested token matching an entry's name selects that entry", requested: "127.0.0.1:5000", want: "other.example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := destination(cfg, tc.requested, "")
			if err != nil {
				t.Fatalf("destination(%q): %v", tc.requested, err)
			}
			if got != (registryhost.Location{Host: tc.want}) {
				t.Errorf("destination(%q) = %+v, want the host %q with no path", tc.requested, got, tc.want)
			}
		})
	}
}

// TestADefaultRegistrysPathIsWhereAPublicationLandsAndItsHostIsWhatItIsTrustedBy
// pins what a publication reads from a default registry carrying a repository
// path: the location prefix it publishes under is the host followed by the path,
// taken from the configuration's one reading of the default — the reading a pull
// binds under — and the host alone is what the publication's signing decisions
// are taken against, so the entry for the host governs the prefixed default.
//
// A requested destination names a host, and its publication lands at that
// host's root whatever the default carries.
func TestADefaultRegistrysPathIsWhereAPublicationLandsAndItsHostIsWhatItIsTrustedBy(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	doc := "workspace: {}\ndefault_registry: 127.0.0.1:5000/acme/catalog\nregistries:\n" +
		"  - host: 127.0.0.1:5000\n    signing:\n      required: true\n"
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(doc), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	rw, err := workspaceconfig.Resolve(root, "", func(string) string { return "" })
	if err != nil {
		t.Fatalf("resolve a default carrying a repository path: %v", err)
	}
	cfg := rw.Config()

	got, err := destination(cfg, "", "")
	if err != nil {
		t.Fatalf("destination with no request: %v", err)
	}
	want := registryhost.Location{Host: "127.0.0.1:5000", Path: "acme/catalog"}
	if got != want {
		t.Fatalf("destination = %+v, want %+v", got, want)
	}
	if got != cfg.DefaultRegistryLocation() {
		t.Errorf("destination = %+v, but the configuration's reading of the default is %+v; publication and resolution must share one", got, cfg.DefaultRegistryLocation())
	}
	if ref := BuildPushReference(got.Prefix(), "Component", "", "core/api-server"); ref != "127.0.0.1:5000/acme/catalog/component/core/api-server" {
		t.Errorf("the published reference is %q, want it under the default's path", ref)
	}
	if !cfg.RegistrySigningRequiredForKind(got.Host, "Component") {
		t.Errorf("the host %q of the prefixed default does not carry the signing requirement its entry states", got.Host)
	}

	requested, err := destination(cfg, "127.0.0.1:5000", "")
	if err != nil {
		t.Fatalf("destination with a requested host: %v", err)
	}
	if requested != (registryhost.Location{Host: "127.0.0.1:5000"}) {
		t.Errorf("a requested destination = %+v, want the host alone, publishing at its root", requested)
	}
}
