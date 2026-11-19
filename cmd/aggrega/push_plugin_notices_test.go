// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/publishops"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

const (
	noticesFixture = "# Third-party notices\n\nexample-dependency 1.0 (MIT)\n"
	sbomFixture    = `{"spdxVersion":"SPDX-2.3","name":"guest"}`
)

// writeNoticesFiles writes the notices and bill-of-materials fixtures beside each
// other and returns their paths.
func writeNoticesFiles(t *testing.T) (notices, sbom string) {
	t.Helper()
	dir := t.TempDir()
	notices = filepath.Join(dir, "NOTICES.md")
	sbom = filepath.Join(dir, "sbom.spdx.json")
	if err := os.WriteFile(notices, []byte(noticesFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sbom, []byte(sbomFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	return notices, sbom
}

// manifestLayerTypes returns the media types of the layers of the manifest tag
// resolves to, in order.
func manifestLayerTypes(t *testing.T, store *memory.Store, tag string) []string {
	t.Helper()
	desc, err := store.Resolve(t.Context(), tag)
	if err != nil {
		t.Fatalf("resolve %s: %v", tag, err)
	}
	raw, err := content.FetchAll(t.Context(), store, desc)
	if err != nil {
		t.Fatalf("fetch manifest: %v", err)
	}
	var m ocispec.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	var types []string
	for _, l := range m.Layers {
		types = append(types, l.MediaType)
	}
	return types
}

// TestPushPluginNoticesAndSBOMRoundTrip publishes a Plugin with both files and a
// keyed signature: the manifest carries the notices layer after the .wasm, one
// application/spdx+json referrer carries the file, the signature still verifies,
// and listing by the signature type returns the signature alone.
func TestPushPluginNoticesAndSBOMRoundTrip(t *testing.T) {
	wsRoot, entityPath, pluginPath, _ := writeAgreeingPlugin(t)
	notices, sbom := writeNoticesFiles(t)
	privPEM, _, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatal(err)
	}
	store := memory.New()
	wantSign := true
	var out bytes.Buffer
	if err := pushRun(t.Context(), pushConfig{
		Workspace:         resolveTempWorkspace(t, tempWorkspaceOptions{Root: wsRoot}),
		EntityPath:        entityPath,
		PluginPath:        pluginPath,
		PluginNoticesPath: notices,
		PluginSBOMPath:    sbom,
		Registry:          "hub.aggrega.ai",
		PushTarget:        store,
		Sign:              &wantSign,
		Signer:            signer,
		Out:               &out,
	}); err != nil {
		t.Fatalf("pushRun: %v", err)
	}

	layers := manifestLayerTypes(t, store, "1.0.0")
	if len(layers) != 2 || layers[0] != oci.MediaTypeWasm || layers[1] != oci.MediaTypePluginNotices {
		t.Fatalf("manifest layers = %v, want [wasm, notices]", layers)
	}

	boms, err := oci.ListReferrers(t.Context(), store, "1.0.0", oci.MediaTypeSPDXJSON)
	if err != nil || len(boms) != 1 {
		t.Fatalf("spdx referrers = %d, err %v; want exactly one", len(boms), err)
	}
	payload, err := oci.FetchReferrerPayload(t.Context(), store, boms[0])
	if err != nil || string(payload) != sbomFixture {
		t.Fatalf("referrer payload = %q, err %v; want the file", payload, err)
	}
	if !strings.Contains(out.String(), "SBOM attached: ") {
		t.Errorf("no SBOM attached line in:\n%s", out.String())
	}

	sigs, err := oci.ListReferrers(t.Context(), store, "1.0.0", signing.BundleMediaType)
	if err != nil || len(sigs) != 1 {
		t.Fatalf("signature referrers = %d, err %v; want exactly the signature", len(sigs), err)
	}
	_, _, manifestDigest, err := oci.FetchPluginManifest(t.Context(), store, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := oci.FetchReferrerPayload(t.Context(), store, sigs[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := signing.VerifyKeyedBundle(bundle, signer.PublicKeyPEM(), manifestDigest); err != nil {
		t.Fatalf("signature does not verify against the manifest digest: %v", err)
	}
}

// TestPushPluginNoticesRefusals gives one fault per row and asserts the refusal
// names the flag and that nothing reached the registry.
func TestPushPluginNoticesRefusals(t *testing.T) {
	wsRoot, entityPath, pluginPath, _ := writeAgreeingPlugin(t)
	notices, sbom := writeNoticesFiles(t)
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "absent")

	rows := []struct {
		name    string
		cfg     pushConfig
		flag    string
		missing bool
	}{
		{"notices without plugin", pushConfig{PluginNoticesPath: notices}, "--plugin-notices", false},
		{"sbom without plugin", pushConfig{PluginSBOMPath: sbom}, "--plugin-sbom", false},
		{"missing notices", pushConfig{PluginPath: pluginPath, PluginNoticesPath: missing}, "--plugin-notices", true},
		{"missing sbom", pushConfig{PluginPath: pluginPath, PluginSBOMPath: missing}, "--plugin-sbom", true},
		{"empty notices", pushConfig{PluginPath: pluginPath, PluginNoticesPath: empty}, "--plugin-notices", false},
		{"empty sbom", pushConfig{PluginPath: pluginPath, PluginSBOMPath: empty}, "--plugin-sbom", false},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			store := memory.New()
			cfg := row.cfg
			cfg.Workspace = resolveTempWorkspace(t, tempWorkspaceOptions{Root: wsRoot})
			cfg.EntityPath = entityPath
			cfg.Registry = "hub.aggrega.ai"
			cfg.PushTarget = store
			err := pushRun(t.Context(), cfg)
			var pie *publishops.PluginInputError
			if !errors.As(err, &pie) {
				t.Fatalf("error = %v, want a PluginInputError", err)
			}
			if pie.Flag() != row.flag || !strings.Contains(err.Error(), row.flag) {
				t.Errorf("refusal names %q (%v), want %s", pie.Flag(), err, row.flag)
			}
			if got := errors.Is(err, os.ErrNotExist); got != row.missing {
				t.Errorf("errors.Is(ErrNotExist) = %v, want %v", got, row.missing)
			}
			if _, exists, _ := oci.Resolve(t.Context(), store, "1.0.0"); exists {
				t.Error("a refused publication reached the registry")
			}
		})
	}
}

// TestPushPluginNoticesDryRunDigest proves the dry-run digest includes the notices
// layer: it differs from the digest without the notices and equals the one the
// publication lands.
func TestPushPluginNoticesDryRunDigest(t *testing.T) {
	wsRoot, entityPath, pluginPath, _ := writeAgreeingPlugin(t)
	notices, _ := writeNoticesFiles(t)
	run := func(noticesPath string, dry bool, store *memory.Store) string {
		t.Helper()
		var out bytes.Buffer
		cfg := pushConfig{
			Workspace:         resolveTempWorkspace(t, tempWorkspaceOptions{Root: wsRoot}),
			EntityPath:        entityPath,
			PluginPath:        pluginPath,
			PluginNoticesPath: noticesPath,
			Registry:          "hub.aggrega.ai",
			DryRun:            dry,
			Out:               &out,
		}
		if store != nil {
			cfg.PushTarget = store
		}
		if err := pushRun(t.Context(), cfg); err != nil {
			t.Fatalf("pushRun: %v", err)
		}
		prefix := "Push complete: "
		if dry {
			prefix = "aggrega push (dry-run): "
		}
		f := statusLineFields(t, out.String(), prefix)
		return parenthesised(t, f[len(f)-1])
	}
	plain := run("", true, nil)
	with := run(notices, true, nil)
	if plain == with {
		t.Fatalf("the dry-run digest ignores the notices: %s", with)
	}
	if landed := run(notices, false, memory.New()); landed != with {
		t.Errorf("dry run printed %s, the publication landed %s", with, landed)
	}
}

// TestPushPluginSBOMAttachedOnceWhenAlreadyPublished proves that publishing the
// same content again attaches the bill of materials when the version carries none
// and never a second.
func TestPushPluginSBOMAttachedOnceWhenAlreadyPublished(t *testing.T) {
	wsRoot, entityPath, pluginPath, _ := writeAgreeingPlugin(t)
	notices, sbom := writeNoticesFiles(t)
	store := memory.New()
	push := func(sbomPath string) string {
		t.Helper()
		var out bytes.Buffer
		if err := pushRun(t.Context(), pushConfig{
			Workspace:         resolveTempWorkspace(t, tempWorkspaceOptions{Root: wsRoot}),
			EntityPath:        entityPath,
			PluginPath:        pluginPath,
			PluginNoticesPath: notices,
			PluginSBOMPath:    sbomPath,
			Registry:          "hub.aggrega.ai",
			PushTarget:        store,
			Out:               &out,
		}); err != nil {
			t.Fatalf("pushRun: %v", err)
		}
		return out.String()
	}
	count := func() int {
		t.Helper()
		refs, err := oci.ListReferrers(t.Context(), store, "1.0.0", oci.MediaTypeSPDXJSON)
		if err != nil {
			t.Fatal(err)
		}
		return len(refs)
	}

	push("")
	if n := count(); n != 0 {
		t.Fatalf("a push without the file attached %d bills of materials", n)
	}
	if out := push(sbom); !strings.Contains(out, "Already published") || !strings.Contains(out, "SBOM attached: ") || count() != 1 {
		t.Fatalf("the re-publication did not attach exactly one bill of materials (count %d):\n%s", count(), out)
	}
	if out := push(sbom); strings.Contains(out, "SBOM attached: ") || count() != 1 {
		t.Fatalf("a second re-publication attached another (count %d):\n%s", count(), out)
	}
}
