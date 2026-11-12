// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package workspaceconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestOfflineSettingKeyMatchesTheWireTag holds the exported key against the
// yaml tag that defines it.
//
// The two are a deliberate near-duplicate: a struct tag cannot be written from
// a constant, so the wire name has to be spelled twice. The duplication is
// therefore the mechanism rather than an accident — and it is exactly the shape
// that agrees on the day it is written and diverges silently afterwards, which
// is what this compares. A divergence would not fail anything else: the loader
// would go on reading the tag while every message told a reader to write the
// constant.
func TestOfflineSettingKeyMatchesTheWireTag(t *testing.T) {
	t.Parallel()
	field, ok := reflect.TypeOf(Config{}).FieldByName("Offline")
	if !ok {
		t.Fatal("Config has no Offline field; the constant now names a setting the type does not carry")
	}
	tag := strings.Split(field.Tag.Get("yaml"), ",")[0]
	if tag != OfflineSettingKey {
		t.Errorf("OfflineSettingKey = %q but Config.Offline is authored as %q; every message naming the key is now telling a reader to write one the loader does not read",
			OfflineSettingKey, tag)
	}
}

// TestOfflineIsNotAMemberOfTheWorkspaceBlock pins the fact that was shipped
// wrong in a user-facing remediation: the posture is a top-level key, and the
// root-marker block does not admit it.
//
// It is asserted against the schema's own refusal rather than against the Go
// type, because the type would silently ignore a key in the wrong place while
// the product refuses the document — and a reader following a remediation into
// the wrong position meets that refusal, not the silence.
func TestOfflineIsNotAMemberOfTheWorkspaceBlock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, ConfigFilename)
	if err := os.WriteFile(path, []byte("workspace:\n  "+OfflineSettingKey+": true\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if _, err := load(path); err == nil {
		t.Fatalf("the %s block accepted %q; the remediation that named it there would have been correct after all, and this test is the thing that is wrong",
			"workspace", OfflineSettingKey)
	}

	// The control: the same setting at the top level, which is where it lives,
	// must load — otherwise the red above measures the fixture rather than the
	// position.
	top := filepath.Join(t.TempDir(), ConfigFilename)
	if err := os.WriteFile(top, []byte("workspace: {}\n"+OfflineSettingKey+": true\n"), 0o644); err != nil {
		t.Fatalf("write control fixture: %v", err)
	}
	cfg, err := load(top)
	if err != nil {
		t.Fatalf("the top-level key was refused too, so the refusal above is not about the position: %v", err)
	}
	if !cfg.Offline {
		t.Error("the top-level key loaded without setting the posture")
	}
}

// TestWithFlagsTopsTheLadderAndAttributesItself pins the three properties the
// command-line layer has to have: it wins over what resolution produced, an
// empty layer changes nothing, and a setting it determined is attributed to the
// flag a reader typed rather than to a file they never edited.
func TestWithFlagsTopsTheLadderAndAttributesItself(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, ConfigFilename)
	if err := os.WriteFile(path, []byte("workspace: {}\n"+OfflineSettingKey+": true\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	frozen := func(string) string { return "" }
	rw, err := Resolve(dir, "", frozen)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !rw.Config().Offline {
		t.Fatal("the fixture did not set the posture, so nothing below measures an override")
	}

	// An empty layer is the ordinary case — every surface with no command line
	// — and must be inert, including in the provenance report.
	if same := rw.WithFlags(FlagOverlay{}); same != rw {
		t.Error("an empty command-line layer produced a different resolution")
	}

	off := false
	flagged := rw.WithFlags(FlagOverlay{Offline: &off})
	if flagged.Config().Offline {
		t.Error("the command-line layer did not beat the configuration file")
	}
	if rw.Config().Offline != true {
		t.Error("applying the layer mutated the resolution it was applied to")
	}

	origins, err := flagged.ValueOrigins()
	if err != nil {
		t.Fatalf("ValueOrigins: %v", err)
	}
	var got string
	var pos bool
	for _, o := range origins {
		if o.Field != OfflineSettingKey {
			continue
		}
		got = o.Source
		pos = o.Position != nil
		kind, name := o.Layer()
		if kind != OriginCommandLineFlag {
			t.Errorf("Layer kind = %v; want the command-line flag", kind)
		}
		if name != "--"+OfflineSettingKey {
			t.Errorf("Layer name = %q; want the flag as a reader types it", name)
		}
	}
	if got != "--"+OfflineSettingKey {
		t.Errorf("origin of %s = %q; want the flag that determined it", OfflineSettingKey, got)
	}
	if pos {
		t.Error("a setting the command line determined carries a position inside a document; there is no document")
	}

	layers := flagged.ContributingLayers()
	if len(layers) == 0 || layers[len(layers)-1] != "--"+OfflineSettingKey {
		t.Errorf("contributing layers = %v; the command-line layer must be reported last, as the top of the ladder", layers)
	}
	// The file list is the other half: a flag is not a file, so it must not
	// have joined the list a reader is shown as the files that contributed.
	for _, f := range flagged.ContributingFiles() {
		if strings.HasPrefix(f, flagLayerPrefix) {
			t.Errorf("the contributing-FILES list carries %q, which is a flag rather than a file", f)
		}
	}
}
