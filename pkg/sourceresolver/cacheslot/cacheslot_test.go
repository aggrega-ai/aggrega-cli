// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cacheslot_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/cacheslot"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/metadata"
)

func TestSlotShape(t *testing.T) {
	t.Parallel()
	got, err := cacheslot.Slot(
		"/workspace", ".aggrega/cache",
		"helm-oci-chart",
		"oci://hub.aggrega.ai/charts/mariadb",
		"",
		"25.0.8",
		"sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
	)
	if err != nil {
		t.Fatalf("Slot: %v", err)
	}
	want := filepath.Join(
		"/workspace", ".aggrega/cache", "source-artifacts",
		"helm-oci-chart", "hub.aggrega.ai", "charts/mariadb",
		"25.0.8", "abcdef012345",
	)
	if got != want {
		t.Errorf("Slot:\n  got  %s\n  want %s", got, want)
	}
}

func TestSlotDeterministic(t *testing.T) {
	t.Parallel()
	a, err := cacheslot.Slot("/ws", ".aggrega/cache", "helm-https-chart",
		"https://charts.example.com/mariadb-25.0.8.tgz", "", "25.0.8", "sha256:abcdef0123456789")
	if err != nil {
		t.Fatalf("Slot a: %v", err)
	}
	b, err := cacheslot.Slot("/ws", ".aggrega/cache", "helm-https-chart",
		"https://charts.example.com/mariadb-25.0.8.tgz", "", "25.0.8", "sha256:abcdef0123456789")
	if err != nil {
		t.Fatalf("Slot b: %v", err)
	}
	if a != b {
		t.Errorf("two Slot calls produced different paths:\n  a %s\n  b %s", a, b)
	}
}

func TestSlotURLPathPercentEncoded(t *testing.T) {
	t.Parallel()
	got, err := cacheslot.Slot("/ws", ".aggrega/cache", "compose-https-fragment",
		"https://example.com/some path/with spaces.yaml", "", "1.0.0", "sha256:deadbeef")
	if err != nil {
		t.Fatalf("Slot: %v", err)
	}
	if strings.Contains(got, " ") {
		t.Errorf("slot path should percent-encode spaces; got %s", got)
	}
}

// TestSlotStripsUserinfoFromHost proves the userinfo fix and that it does not
// silently re-key conformant caches: a URL carrying credentials produces the
// SAME slot path as the credential-free URL (so the cache key is the host, not
// the credentials), and no credentials appear anywhere in the on-disk path.
// TestSlotShape above pins the conformant path verbatim, so a regression that
// changed host extraction for a normal URL would fail there.
func TestSlotStripsUserinfoFromHost(t *testing.T) {
	t.Parallel()
	clean, err := cacheslot.Slot("/ws", ".aggrega/cache", "helm-oci-chart",
		"oci://hub.aggrega.ai/charts/mariadb", "", "25.0.8", "sha256:abcdef0123456789")
	if err != nil {
		t.Fatalf("Slot clean: %v", err)
	}
	withCreds, err := cacheslot.Slot("/ws", ".aggrega/cache", "helm-oci-chart",
		"oci://user:pw@hub.aggrega.ai/charts/mariadb", "", "25.0.8", "sha256:abcdef0123456789")
	if err != nil {
		t.Fatalf("Slot withCreds: %v", err)
	}
	if withCreds != clean {
		t.Errorf("userinfo URL keyed a different slot than the clean URL:\n  creds %s\n  clean %s", withCreds, clean)
	}
	if strings.Contains(withCreds, "user:pw") || strings.Contains(withCreds, "@") {
		t.Errorf("credentials leaked into cache-slot path: %s", withCreds)
	}
}

func TestSlotEmptyWorkspaceRootRejected(t *testing.T) {
	t.Parallel()
	_, err := cacheslot.Slot("", ".aggrega/cache", "helm-oci-chart",
		"oci://hub.aggrega.ai/charts/mariadb", "", "25.0.8", "sha256:abcd")
	if err == nil {
		t.Fatal("expected error for empty workspaceRoot; got nil")
	}
	if !errors.Is(err, cacheslot.ErrEmptyWorkspaceRoot()) {
		t.Errorf("error should wrap ErrEmptyWorkspaceRoot; got %v", err)
	}
}

func TestWriteBlobAndMetadataPopulatesSlot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	slot, err := cacheslot.Slot(root, ".aggrega/cache", "compose-https-fragment",
		"https://example.com/fragment.yaml", "", "1.0.0", "sha256:deadbeefcafedeadbeefcafedeadbeefcafedeadbeefcafedeadbeefcafedead")
	if err != nil {
		t.Fatalf("Slot: %v", err)
	}

	body := []byte("archive-bytes-here")
	if err := cacheslot.WriteBlobAndMetadata(slot, body,
		"compose-https-fragment", "https://example.com/fragment.yaml",
		"1.0.0", "sha256:deadbeefcafedeadbeefcafedeadbeefcafedeadbeefcafedeadbeefcafedead", "application/x-yaml", "", nil); err != nil {
		t.Fatalf("WriteBlobAndMetadata: %v", err)
	}

	blob, err := os.ReadFile(filepath.Join(slot, "blob.tgz"))
	if err != nil {
		t.Fatalf("read blob: %v", err)
	}
	if string(blob) != string(body) {
		t.Errorf("blob bytes mismatch")
	}

	// A nil manifest writes no manifest file — a non-OCI source has no manifest.
	if _, err := os.Stat(filepath.Join(slot, cacheslot.ManifestFile)); !os.IsNotExist(err) {
		t.Errorf("nil manifest should not create a manifest file; stat err = %v", err)
	}

	m, err := metadata.Read(filepath.Join(slot, "metadata.yaml"))
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if m.Kind != "compose-https-fragment" {
		t.Errorf("Kind = %q; want compose-https-fragment", m.Kind)
	}
	if m.URL != "https://example.com/fragment.yaml" {
		t.Errorf("URL = %q", m.URL)
	}
	if m.Digest != "sha256:deadbeefcafedeadbeefcafedeadbeefcafedeadbeefcafedeadbeefcafedead" {
		t.Errorf("Digest = %q", m.Digest)
	}
	if m.PulledAt.IsZero() {
		t.Errorf("PulledAt should be populated")
	}
}
