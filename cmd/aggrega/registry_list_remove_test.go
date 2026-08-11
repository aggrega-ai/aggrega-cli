// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestRegistryListDeterministicOutput proves `registry list` prints registries
// in ascending host order, shows the alias, renders a trust-posture summary, and
// marks the default registry.
func TestRegistryListDeterministicOutput(t *testing.T) {
	t.Parallel()
	content := "workspace: {}\ndefault_registry: hub.aggrega.ai\n" +
		"registries:\n" +
		"  - host: zed.example.com\n    name: zed\n    advisory: true\n" +
		"  - host: hub.aggrega.ai\n    signing:\n      required: true\n" +
		"    trusted_identities:\n      - identity: a@example.com\n        issuer: https://accounts.google.com\n" +
		"      - identity: b@example.com\n        issuer: https://accounts.google.com\n"
	rw := resolveWorkspaceWithConfig(t, t.TempDir(), content)

	var out bytes.Buffer
	if err := registryListRun(registryListConfig{ws: rw, Out: &out}); err != nil {
		t.Fatalf("registryListRun: %v", err)
	}
	got := out.String()

	// Ascending host order: hub before zed.
	hubIdx := strings.Index(got, "hub.aggrega.ai")
	zedIdx := strings.Index(got, "zed.example.com")
	if hubIdx < 0 || zedIdx < 0 || hubIdx > zedIdx {
		t.Fatalf("hosts not in ascending order; output:\n%s", got)
	}
	// Alias shown for zed.
	if !strings.Contains(got, "zed") {
		t.Errorf("alias 'zed' not shown; output:\n%s", got)
	}
	// Trust summaries.
	if !strings.Contains(got, "signatures required") || !strings.Contains(got, "keyless (2 identities)") {
		t.Errorf("hub trust summary missing; output:\n%s", got)
	}
	if !strings.Contains(got, "signatures optional") || !strings.Contains(got, "advisory") {
		t.Errorf("zed trust summary missing; output:\n%s", got)
	}
	// Default marker on the hub line only.
	hubLine := lineContaining(t, got, "hub.aggrega.ai")
	if !strings.Contains(hubLine, "*") {
		t.Errorf("default marker missing on hub line: %q", hubLine)
	}
	zedLine := lineContaining(t, got, "zed.example.com")
	if strings.Contains(zedLine, "*") {
		t.Errorf("non-default zed line should carry no marker: %q", zedLine)
	}
}

// TestRegistryListEmpty proves `registry list` prints a short notice when no
// registries are configured.
func TestRegistryListEmpty(t *testing.T) {
	t.Parallel()
	rw := resolveTempWorkspace(t, tempWorkspaceOptions{})
	var out bytes.Buffer
	if err := registryListRun(registryListConfig{ws: rw, Out: &out}); err != nil {
		t.Fatalf("registryListRun: %v", err)
	}
	if !strings.Contains(out.String(), "No registries configured") {
		t.Errorf("empty notice missing; output: %q", out.String())
	}
}

// TestRegistryRemoveByHost proves `registry remove <host>` drops the entry and
// preserves the default pointer and sibling entries.
func TestRegistryRemoveByHost(t *testing.T) {
	t.Parallel()
	content := "workspace: {}\ndefault_registry: hub.aggrega.ai\n" +
		"registries:\n  - host: hub.aggrega.ai\n  - host: gone.example.com\n"
	rw := resolveWorkspaceWithConfig(t, t.TempDir(), content)

	var out bytes.Buffer
	if err := registryRemoveRun(registryRemoveConfig{
		Token:     "gone.example.com",
		Workspace: true,
		ws:        rw,
		Out:       &out,
	}); err != nil {
		t.Fatalf("registryRemoveRun: %v", err)
	}
	if !strings.Contains(out.String(), "Removed registry gone.example.com") {
		t.Errorf("confirmation line missing; output: %q", out.String())
	}
	reloaded, _ := workspaceconfig.Resolve(rw.Root(), "")
	if _, found := reloaded.Config().RegistryTrustPolicy("gone.example.com"); found {
		t.Errorf("gone.example.com should be removed")
	}
	if _, found := reloaded.Config().RegistryTrustPolicy("hub.aggrega.ai"); !found {
		t.Errorf("hub should survive")
	}
	if reloaded.Config().DefaultRegistry != "hub.aggrega.ai" {
		t.Errorf("default_registry cleared; got %q", reloaded.Config().DefaultRegistry)
	}
}

// TestRegistryRemoveByAlias proves `registry remove <alias>` resolves the alias
// to its host and drops that entry.
func TestRegistryRemoveByAlias(t *testing.T) {
	t.Parallel()
	content := "workspace: {}\nregistries:\n  - host: dev.local:5000\n    name: dev\n  - host: hub.aggrega.ai\n"
	rw := resolveWorkspaceWithConfig(t, t.TempDir(), content)

	var out bytes.Buffer
	if err := registryRemoveRun(registryRemoveConfig{
		Token:     "dev",
		Workspace: true,
		ws:        rw,
		Out:       &out,
	}); err != nil {
		t.Fatalf("registryRemoveRun: %v", err)
	}
	// The confirmation names the resolved host, not the alias.
	if !strings.Contains(out.String(), "Removed registry dev.local:5000") {
		t.Errorf("confirmation should name the resolved host; output: %q", out.String())
	}
	reloaded, _ := workspaceconfig.Resolve(rw.Root(), "")
	if _, found := reloaded.Config().RegistryTrustPolicy("dev.local:5000"); found {
		t.Errorf("dev.local:5000 should be removed via its alias")
	}
	if _, found := reloaded.Config().RegistryTrustPolicy("hub.aggrega.ai"); !found {
		t.Errorf("hub should survive")
	}
}

// TestRegistryRemoveAbsentFailsLoudly proves removing a token that matches
// nothing returns an error naming the token and the file, and leaves the file
// untouched.
func TestRegistryRemoveAbsentFailsLoudly(t *testing.T) {
	t.Parallel()
	content := "workspace: {}\nregistries:\n  - host: hub.aggrega.ai\n"
	rw := resolveWorkspaceWithConfig(t, t.TempDir(), content)
	cfgPath := filepath.Join(rw.Root(), workspaceconfig.ConfigFilename)
	before, _ := os.ReadFile(cfgPath)

	var out bytes.Buffer
	err := registryRemoveRun(registryRemoveConfig{
		Token:     "absent.example.com",
		Workspace: true,
		ws:        rw,
		Out:       &out,
	})
	if err == nil {
		t.Fatal("removing an absent registry should fail loudly")
	}
	if !strings.Contains(err.Error(), "absent.example.com") || !strings.Contains(err.Error(), cfgPath) {
		t.Errorf("error should name the token and the file; got %v", err)
	}
	after, _ := os.ReadFile(cfgPath)
	if string(before) != string(after) {
		t.Errorf("no-match remove must not rewrite the file")
	}
}

// lineContaining returns the first line of text that contains sub, failing the
// test when none does.
func lineContaining(t *testing.T, text, sub string) string {
	t.Helper()
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, sub) {
			return line
		}
	}
	t.Fatalf("no line contains %q in:\n%s", sub, text)
	return ""
}
