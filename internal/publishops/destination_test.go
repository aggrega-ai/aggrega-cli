// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package publishops

import (
	"os"
	"path/filepath"
	"testing"

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
			got, err := destinationHost(cfg, tc.requested, "")
			if err != nil {
				t.Fatalf("destinationHost(%q): %v", tc.requested, err)
			}
			if got != tc.want {
				t.Errorf("destinationHost(%q) = %q, want %q", tc.requested, got, tc.want)
			}
		})
	}
}
