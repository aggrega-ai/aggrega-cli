// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// writeIndexFixture lays out one host's index files under the temp workspace
// using the same path helpers and per-repo byte layout the index writer uses, so
// the loader is exercised against the real on-disk shape: one file per record in
// the host directory, plus the freshness sibling beside it.
func writeIndexFixture(t *testing.T, root, cacheDir, host string, idx RegistryIndex, stamp string) {
	t.Helper()
	hostDir := oci.IndexBasePath(root, cacheDir, host)
	if err := os.MkdirAll(hostDir, 0o755); err != nil {
		t.Fatalf("mkdir host index dir: %v", err)
	}
	for _, rec := range idx.Records {
		data, err := json.MarshalIndent(rec, "", "  ")
		if err != nil {
			t.Fatalf("marshal record %q: %v", rec.Name, err)
		}
		data = append(data, '\n')
		if err := os.WriteFile(oci.IndexRepoPath(root, cacheDir, host, rec.Name), data, 0o644); err != nil {
			t.Fatalf("write record %q: %v", rec.Name, err)
		}
	}
	if stamp != "" {
		if err := os.WriteFile(oci.IndexBasePath(root, cacheDir, host)+".updated", []byte(stamp), 0o644); err != nil {
			t.Fatalf("write timestamp: %v", err)
		}
	}
}

func TestLoadHostIndex(t *testing.T) {
	root := t.TempDir()
	const cacheDir = "cache"
	const host = "registry.example.com"
	idx := RegistryIndex{
		Host: host,
		Records: []registryindex.Record{
			{URI: "oci://h/component/postgres", Name: "postgres", Kind: "Component"},
			{URI: "oci://h/plugin/helm-render", Name: "helm-render", Kind: "Plugin"},
		},
	}
	want, err := time.Parse(time.RFC3339, "2026-07-16T12:00:00Z")
	if err != nil {
		t.Fatalf("parse want time: %v", err)
	}
	writeIndexFixture(t, root, cacheDir, host, idx, "2026-07-16T12:00:00Z\n")

	got, err := registryindex.LoadHost(root, cacheDir, host)
	if err != nil {
		t.Fatalf("loadHostIndex: %v", err)
	}
	if got.Host != host {
		t.Errorf("host = %q; want %q", got.Host, host)
	}
	if len(got.Records) != 2 || got.Records[0].Name != "postgres" || got.Records[1].Name != "helm-render" {
		t.Errorf("records = %+v; want the two seeded records in order", got.Records)
	}
	if !got.HasTimestamp {
		t.Error("HasTimestamp = false; want true when the sibling file is present")
	}
	if !got.LastUpdated.Equal(want) {
		t.Errorf("LastUpdated = %v; want %v", got.LastUpdated, want)
	}
}

func TestLoadHostIndexAbsent(t *testing.T) {
	root := t.TempDir()
	got, err := registryindex.LoadHost(root, "cache", "registry.example.com")
	if !registryindex.IsNotBuilt(err) {
		t.Fatalf("loadHostIndex on absent index: err = %v; want registryindex.IsNotBuilt", err)
	}
	if got.Records != nil {
		t.Errorf("records = %+v; want nil on absent index", got.Records)
	}
}

func TestLoadHostIndexMissingTimestamp(t *testing.T) {
	root := t.TempDir()
	const cacheDir = "cache"
	const host = "registry.example.com"
	idx := RegistryIndex{
		Host:    host,
		Records: []registryindex.Record{{URI: "oci://h/component/postgres", Name: "postgres", Kind: "Component"}},
	}
	writeIndexFixture(t, root, cacheDir, host, idx, "")

	got, err := registryindex.LoadHost(root, cacheDir, host)
	if err != nil {
		t.Fatalf("loadHostIndex: %v", err)
	}
	if len(got.Records) != 1 || got.Records[0].Name != "postgres" {
		t.Errorf("records = %+v; want the single seeded record", got.Records)
	}
	if got.HasTimestamp {
		t.Error("HasTimestamp = true; want false when the sibling file is absent")
	}
	if !got.LastUpdated.IsZero() {
		t.Errorf("LastUpdated = %v; want the zero time when the sibling file is absent", got.LastUpdated)
	}
}

func TestLoadHostIndexUnparseable(t *testing.T) {
	root := t.TempDir()
	const cacheDir = "cache"
	const host = "registry.example.com"
	hostDir := oci.IndexBasePath(root, cacheDir, host)
	if err := os.MkdirAll(hostDir, 0o755); err != nil {
		t.Fatalf("mkdir host index dir: %v", err)
	}
	if err := os.WriteFile(oci.IndexRepoPath(root, cacheDir, host, "broken"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write record: %v", err)
	}
	_, err := registryindex.LoadHost(root, cacheDir, host)
	if err == nil {
		t.Fatal("loadHostIndex: want error for unparseable records, got nil")
	}
	if registryindex.IsNotBuilt(err) {
		t.Fatal("loadHostIndex: unparseable records must not report as an unbuilt index")
	}
}
