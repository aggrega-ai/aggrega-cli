// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cacheslot_test

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
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

// TestSlotSeparatesGitModuleSubdirectories pins the property that two
// directories of one repository, at one commit, occupy distinct cache slots.
// The digest differs between them in practice, so the assertion holds the
// digest CONSTANT: that isolates the subdirectory as the only varying term and
// fails if the subdirectory stops reaching the slot path, which a differing
// digest would otherwise mask.
func TestSlotSeparatesGitModuleSubdirectories(t *testing.T) {
	t.Parallel()
	const (
		url    = "git://git.example.com/infra/modules.git"
		commit = "b149cd9719865f0afdb19df745c5458c2cd45cc8"
		dg     = "sha256:abcdef0123456789"
	)
	alpha, err := cacheslot.Slot("/ws", ".aggrega/cache", "git-repository",
		url, "", "modules/alpha", commit, dg)
	if err != nil {
		t.Fatalf("Slot alpha: %v", err)
	}
	beta, err := cacheslot.Slot("/ws", ".aggrega/cache", "git-repository",
		url, "", "modules/beta", commit, dg)
	if err != nil {
		t.Fatalf("Slot beta: %v", err)
	}
	if alpha == beta {
		t.Errorf("two module subdirectories collapsed onto one slot: %s", alpha)
	}

	// An empty subdirectory must leave every other kind's layout untouched.
	whole, err := cacheslot.Slot("/ws", ".aggrega/cache", "git-repository",
		url, "", "", commit, dg)
	if err != nil {
		t.Fatalf("Slot whole: %v", err)
	}
	if want := filepath.Join("/ws", ".aggrega/cache", "source-artifacts",
		"git-repository", "git.example.com", "infra/modules.git",
		commit, "abcdef012345"); whole != want {
		t.Errorf("empty module subdirectory changed the slot layout:\n  got  %s\n  want %s", whole, want)
	}

	// A subdirectory climbing above its own place is refused, even where the
	// directories above it would absorb the climb and leave the slot under the
	// cache root: three parent steps from infra/modules.git land in another host's
	// place, which is no slot of this source's.
	escaped, err := cacheslot.Slot("/ws", ".aggrega/cache", "git-repository",
		url, "", "../../../etc", commit, dg)
	if !errors.Is(err, oci.ErrCacheLocationOutsideCache()) {
		t.Fatalf("Slot with module_subdir ../../../etc: err = %v, slot = %q; want the cache-location refusal", err, escaped)
	}
	if escaped != "" {
		t.Errorf("a refused slot came back with a path: %q", escaped)
	}
	if !strings.Contains(err.Error(), `module_subdir "../../../etc"`) {
		t.Errorf("refusal does not name the value under its key: %v", err)
	}
}

// TestSlotRefusesAValueThatLeavesItsPlace holds the source-artifact slot builder
// to refusing every value that would place the slot somewhere other than the place
// its position names: a version carrying a separator, a URL whose path climbs above
// its host's place, and a module subdirectory that climbs above its own. Each is
// asserted against the cache-location condition and against the value being named
// under the source key it was written as. TestSlotShape is the positive control for
// a well-formed set of values.
func TestSlotRefusesAValueThatLeavesItsPlace(t *testing.T) {
	t.Parallel()
	const (
		host = "helm-oci-chart"
		url  = "oci://hub.aggrega.ai/charts/mariadb"
		dg   = "sha256:abcdef0123456789"
	)
	for _, tc := range []struct {
		name         string
		kind         string
		url          string
		chart        string
		moduleSubdir string
		version      string
		wantKey      string
		wantValue    string
	}{
		{
			name: "a version with a separator", kind: host, url: url, version: "1.0.0/x",
			wantKey: "version", wantValue: "1.0.0/x",
		},
		{
			name: "a version with a backslash", kind: host, url: url, version: `1.0.0\x`,
			wantKey: "version", wantValue: `1.0.0\x`,
		},
		{
			name: "a compose URL path that climbs with ../", kind: "compose-https-fragment",
			url:     "https://example.com/a/../../../../../../escape-target/f.yaml",
			version: "1.0.0", wantKey: "url",
			wantValue: "https://example.com/a/../../../../../../escape-target/f.yaml",
		},
		{
			name: "a module subdirectory that climbs above its place", kind: "git-repository",
			url: "git://git.example.com/infra/modules.git", moduleSubdir: "../../../../../../escape-target/subdir",
			version: "b149cd97", wantKey: "module_subdir", wantValue: "../../../../../../escape-target/subdir",
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := cacheslot.Slot("/ws", ".aggrega/cache", tc.kind, tc.url, tc.chart, tc.moduleSubdir, tc.version, dg)
			if !errors.Is(err, oci.ErrCacheLocationOutsideCache()) {
				t.Fatalf("Slot: err = %v, slot = %q; want the cache-location refusal", err, got)
			}
			if got != "" {
				t.Errorf("a refused slot came back with a path: %q", got)
			}
			if want := tc.wantKey + " " + strconv.Quote(tc.wantValue); !strings.Contains(err.Error(), want) {
				t.Errorf("refusal does not name the value under its key %q: %v", want, err)
			}
		})
	}
}

func TestSlotDeterministic(t *testing.T) {
	t.Parallel()
	a, err := cacheslot.Slot("/ws", ".aggrega/cache", "helm-https-chart",
		"https://charts.example.com/mariadb-25.0.8.tgz", "", "", "25.0.8", "sha256:abcdef0123456789")
	if err != nil {
		t.Fatalf("Slot a: %v", err)
	}
	b, err := cacheslot.Slot("/ws", ".aggrega/cache", "helm-https-chart",
		"https://charts.example.com/mariadb-25.0.8.tgz", "", "", "25.0.8", "sha256:abcdef0123456789")
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
		"https://example.com/some path/with spaces.yaml", "", "", "1.0.0", "sha256:deadbeef")
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
		"oci://hub.aggrega.ai/charts/mariadb", "", "", "25.0.8", "sha256:abcdef0123456789")
	if err != nil {
		t.Fatalf("Slot clean: %v", err)
	}
	withCreds, err := cacheslot.Slot("/ws", ".aggrega/cache", "helm-oci-chart",
		"oci://user:pw@hub.aggrega.ai/charts/mariadb", "", "", "25.0.8", "sha256:abcdef0123456789")
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
		"oci://hub.aggrega.ai/charts/mariadb", "", "", "25.0.8", "sha256:abcd")
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
		"https://example.com/fragment.yaml", "", "", "1.0.0", "sha256:deadbeefcafedeadbeefcafedeadbeefcafedeadbeefcafedeadbeefcafedead")
	if err != nil {
		t.Fatalf("Slot: %v", err)
	}

	body := []byte("archive-bytes-here")
	if err := cacheslot.WriteBlobAndMetadata("", "", slot, body,
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
