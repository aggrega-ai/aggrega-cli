// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
)

// TestUpsertPluginSocketRef_PreservesExistingContent proves the config-socket
// writer adds a plugins.<socket> attachment into an existing workspace config
// without disturbing the other keys, is idempotent (re-adding the same URI does
// not duplicate it), appends a distinct URI, and produces a schema-valid file
// the resolver accepts.
func TestUpsertPluginSocketRef_PreservesExistingContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ConfigFilename)
	if err := os.WriteFile(path, []byte(`workspace: {}
offline: true
default_registry: hub.aggrega.ai
`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := UpsertPluginSocketRef(path, "governance", "governance/tag-injector:1.0.0", plugintype.RoleLabelTransform); err != nil {
		t.Fatalf("UpsertPluginSocketRef (add): %v", err)
	}

	// Existing unrelated content must survive byte-for-byte.
	rw, err := Resolve(dir, "")
	if err != nil {
		t.Fatalf("resolve after add: %v", err)
	}
	cfg := rw.Config()
	if cfg.DefaultRegistry != "hub.aggrega.ai" || !cfg.Offline {
		t.Errorf("existing keys not preserved: default_registry=%q offline=%v", cfg.DefaultRegistry, cfg.Offline)
	}
	if len(cfg.Plugins.Governance) != 1 || cfg.Plugins.Governance[0].URI != "governance/tag-injector:1.0.0" {
		t.Fatalf("attachment not written; got %+v", cfg.Plugins.Governance)
	}

	// Idempotent: re-adding the identical URI must not duplicate it.
	if err := UpsertPluginSocketRef(path, "governance", "governance/tag-injector:1.0.0", plugintype.RoleLabelTransform); err != nil {
		t.Fatalf("UpsertPluginSocketRef (re-add): %v", err)
	}
	rw, err = Resolve(dir, "")
	if err != nil {
		t.Fatalf("resolve after re-add: %v", err)
	}
	if got := rw.Config().Plugins.Governance; len(got) != 1 {
		t.Errorf("re-adding the same URI must not duplicate it; got %+v", got)
	}

	// A distinct URI appends a second attachment.
	if err := UpsertPluginSocketRef(path, "governance", "governance/other:2.0.0", plugintype.RoleLabelTransform); err != nil {
		t.Fatalf("UpsertPluginSocketRef (append distinct): %v", err)
	}
	rw, err = Resolve(dir, "")
	if err != nil {
		t.Fatalf("resolve after append: %v", err)
	}
	if got := rw.Config().Plugins.Governance; len(got) != 2 {
		t.Fatalf("a distinct URI must append; got %+v", got)
	}
}

// TestUpsertPluginSocketRef_CreatesMissingFile proves the writer creates a fresh,
// schema-valid config (with a workspace marker) when no file exists yet.
func TestUpsertPluginSocketRef_CreatesMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ConfigFilename)
	if err := UpsertPluginSocketRef(path, "governance", "governance/deny-gate:1.0.0", plugintype.RoleIRVerdict); err != nil {
		t.Fatalf("UpsertPluginSocketRef: %v", err)
	}
	rw, err := Resolve(dir, "")
	if err != nil {
		t.Fatalf("resolve created file: %v", err)
	}
	got := rw.Config().Plugins.Governance
	if len(got) != 1 || got[0].URI != "governance/deny-gate:1.0.0" {
		t.Fatalf("attachment not written to fresh file; got %+v", got)
	}
}

// TestUpsertPluginSocketRef_RejectsUnknownSocket proves an unknown socket key is
// refused by the merged-document schema validation rather than silently written,
// so the writer stays a loud guardrail against a malformed plugins block.
func TestUpsertPluginSocketRef_RejectsUnknownSocket(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ConfigFilename)
	if err := UpsertPluginSocketRef(path, "bogus", "bogus/x:1.0.0", ""); err == nil {
		t.Fatal("UpsertPluginSocketRef accepted an unknown socket key; the schema gate must reject it")
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Error("a rejected write left a config file on disk; the malformed document must never reach disk")
	}
}

// socketWriterSeed is a workspace configuration attaching two importers — the
// first carrying a with block and followed by the second, so its list position is
// observable, the second pinned by a range — and one governance and one linter
// plugin, every entry naming its plugin by identity and version.
const socketWriterSeed = `workspace: {}
plugins:
  importer:
    - uri: importer/x:1.0.0
      with:
        settings:
          target_namespace: payments
    - uri: importer/y:^1.0.0
  governance:
    - uri: governance/x:1.0.0
      role: label-transform
  linter:
    - uri: linter/x:1.0.0
`

// socketWriterLists is the plugins block of a configuration document, decoded as
// the writer encodes it, so a case reads the entries the file holds rather than
// the entries a resolution derived from them.
type socketWriterLists struct {
	Plugins struct {
		Importer   []PluginRef `yaml:"importer"`
		Governance []PluginRef `yaml:"governance"`
		Linter     []PluginRef `yaml:"linter"`
	} `yaml:"plugins"`
}

// TestUpsertPluginSocketRef_AnImporterKeepsOneEntryWhileTheOtherListsAppend proves
// what the config-socket writer does when the plugin it is handed is already
// attached, list by list.
//
// On the importer list an entry naming the same importer is that importer's one
// entry, so adding the importer at another version moves that entry to the
// version — in place, its with block and its position kept — rather than
// appending a second entry the loader refuses. On the governance and linter lists
// one plugin may be attached more than once, so another version is a new entry
// appended beside the first. On every list a uri already present changes no
// entry.
//
// Each row also holds the reporting twin to the write — the document it reports
// is the document the write leaves, and reporting writes nothing — and holds the
// write to idempotence: the same addition made again leaves the file's bytes as
// the first one left them. And the loader reads what was written, which is where
// a second entry naming one importer would be refused.
//
// 🔴 PLANTED DEFECT: match importer entries by exact uri, as the other lists do,
// and the two rows moving an importer's entry fail — the importer list comes back
// with the added version appended after the entries it should have replaced, and
// the loader refuses the file it was written to. The rows adding a uri already
// present, a new importer, and another version on the governance and linter lists
// stay green: they are what the exact match already did.
func TestUpsertPluginSocketRef_AnImporterKeepsOneEntryWhileTheOtherListsAppend(t *testing.T) {
	paymentsWith := &PluginRefWith{Settings: map[string]any{"target_namespace": "payments"}}
	seedImporter := []PluginRef{{URI: "importer/x:1.0.0", With: paymentsWith}, {URI: "importer/y:^1.0.0"}}
	seedGovernance := []PluginRef{{URI: "governance/x:1.0.0", Role: plugintype.RoleLabelTransform}}
	seedLinter := []PluginRef{{URI: "linter/x:1.0.0"}}

	for _, tc := range []struct {
		scenario       string
		socket, uri    string
		role           plugintype.Role
		wantImporter   []PluginRef
		wantGovernance []PluginRef
		wantLinter     []PluginRef
	}{
		{
			scenario:       "an attached importer added at a newer version has its one entry moved, with and position kept",
			socket:         "importer",
			uri:            "importer/x:2.0.0",
			wantImporter:   []PluginRef{{URI: "importer/x:2.0.0", With: paymentsWith}, {URI: "importer/y:^1.0.0"}},
			wantGovernance: seedGovernance,
			wantLinter:     seedLinter,
		},
		{
			scenario:       "an importer attached by a range and added at an exact version has that entry moved",
			socket:         "importer",
			uri:            "importer/y:1.4.0",
			wantImporter:   []PluginRef{{URI: "importer/x:1.0.0", With: paymentsWith}, {URI: "importer/y:1.4.0"}},
			wantGovernance: seedGovernance,
			wantLinter:     seedLinter,
		},
		{
			scenario:       "an importer added at the version it is attached at changes no entry",
			socket:         "importer",
			uri:            "importer/x:1.0.0",
			wantImporter:   seedImporter,
			wantGovernance: seedGovernance,
			wantLinter:     seedLinter,
		},
		{
			scenario:       "an importer not yet attached is appended",
			socket:         "importer",
			uri:            "importer/z:1.0.0",
			wantImporter:   append(slices.Clone(seedImporter), PluginRef{URI: "importer/z:1.0.0"}),
			wantGovernance: seedGovernance,
			wantLinter:     seedLinter,
		},
		{
			scenario:     "a governance plugin added at another version is appended beside the first",
			socket:       "governance",
			uri:          "governance/x:2.0.0",
			role:         plugintype.RoleLabelTransform,
			wantImporter: seedImporter,
			wantGovernance: append(slices.Clone(seedGovernance),
				PluginRef{URI: "governance/x:2.0.0", Role: plugintype.RoleLabelTransform}),
			wantLinter: seedLinter,
		},
		{
			scenario:       "a linter plugin added at another version is appended beside the first",
			socket:         "linter",
			uri:            "linter/x:2.0.0",
			wantImporter:   seedImporter,
			wantGovernance: seedGovernance,
			wantLinter:     append(slices.Clone(seedLinter), PluginRef{URI: "linter/x:2.0.0"}),
		},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, ConfigFilename)
			seed := []byte(socketWriterSeed)
			if err := os.WriteFile(path, seed, 0o644); err != nil {
				t.Fatalf("seed: %v", err)
			}
			read := func(what string) []byte {
				t.Helper()
				b, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("read the file %s: %v", what, err)
				}
				return b
			}

			reported, err := PluginSocketRefDocument(path, tc.socket, tc.uri, tc.role)
			if err != nil {
				t.Fatalf("PluginSocketRefDocument: %v", err)
			}
			if got := read("after the report"); !bytes.Equal(got, seed) {
				t.Fatalf("reporting the document changed the file:\n%s", got)
			}

			if err := UpsertPluginSocketRef(path, tc.socket, tc.uri, tc.role); err != nil {
				t.Fatalf("UpsertPluginSocketRef: %v", err)
			}
			written := read("after the write")
			if !bytes.Equal(reported, written) {
				t.Errorf("the reported document differs from the one the write left\n--- reported ---\n%s\n--- written ---\n%s",
					reported, written)
			}

			var got socketWriterLists
			if err := yaml.Unmarshal(written, &got); err != nil {
				t.Fatalf("decode the written file: %v", err)
			}
			for _, list := range []struct {
				key       string
				got, want []PluginRef
			}{
				{"plugins.importer", got.Plugins.Importer, tc.wantImporter},
				{"plugins.governance", got.Plugins.Governance, tc.wantGovernance},
				{"plugins.linter", got.Plugins.Linter, tc.wantLinter},
			} {
				if !reflect.DeepEqual(list.got, list.want) {
					t.Errorf("%s holds %+v, want %+v", list.key, list.got, list.want)
				}
			}

			if _, err := Resolve(dir, ""); err != nil {
				t.Errorf("the loader refuses the file the writer left: %v", err)
			}

			if err := UpsertPluginSocketRef(path, tc.socket, tc.uri, tc.role); err != nil {
				t.Fatalf("UpsertPluginSocketRef (again): %v", err)
			}
			if again := read("after the same write again"); !bytes.Equal(again, written) {
				t.Errorf("the same addition made again changed the file\n--- first ---\n%s\n--- again ---\n%s", written, again)
			}
		})
	}
}
