// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci_test

import (
	"context"
	"encoding/base64"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

const (
	heldManifest  = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	otherManifest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// keyedBundleFor returns a bundle the toolchain's keyed signer makes over madeFor.
func keyedBundleFor(t *testing.T, madeFor string) []byte {
	t.Helper()
	priv, _, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(priv, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	out, _, err := signer.Sign(context.Background(), madeFor)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return out
}

// cacheBundle writes bundle where a pull caches a verified one and returns its
// digest.
func cacheBundle(t *testing.T, ws, cacheDir string, bundle []byte) string {
	t.Helper()
	d := digest.ContentDigest(bundle)
	path := oci.SignatureBundlePath(ws, cacheDir, d)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bundle, 0o644); err != nil {
		t.Fatal(err)
	}
	return d
}

// TestHoldRecordedSignatureHoldsAFactToTheArtifactItWasMadeFor is the decision's
// table: one fault per row beside the controls, and each bound it states as a row
// of its own.
func TestHoldRecordedSignatureHoldsAFactToTheArtifactItWasMadeFor(t *testing.T) {
	t.Parallel()
	const cacheDir = ".aggrega/cache"
	madeForHeld := keyedBundleFor(t, heldManifest)
	rows := []struct {
		name    string
		bundle  []byte
		madeFor oci.MadeFor
		tamper  bool
		missing bool
		want    error
	}{
		{name: "made for the manifest read: accepted", bundle: madeForHeld, madeFor: oci.MadeForManifest(heldManifest)},
		{name: "made for another manifest than the one read: refused", bundle: madeForHeld,
			madeFor: oci.MadeForManifest(otherManifest), want: oci.ErrSignatureForAnotherArtifact()},
		{name: "no manifest read: held to the re-hash alone", bundle: madeForHeld, madeFor: oci.NoManifestRead()},
		{name: "an empty manifest digest is no manifest read", bundle: madeForHeld, madeFor: oci.MadeForManifest("")},
		{name: "a form the reading does not recognise: held to the re-hash alone",
			bundle:  []byte(`{"mediaType":"application/vnd.dev.sigstore.bundle.v0.3+json","dsseEnvelope":{}}`),
			madeFor: oci.MadeForManifest(otherManifest)},
		{name: "the cached bundle rewritten: refused as a mismatch, before anything is read from it", bundle: madeForHeld,
			madeFor: oci.MadeForManifest(heldManifest), tamper: true, want: oci.ErrSignatureBundleDigestMismatch()},
		{name: "the cached bundle missing: refused as a mismatch", bundle: madeForHeld,
			madeFor: oci.MadeForManifest(heldManifest), missing: true, want: oci.ErrSignatureBundleDigestMismatch()},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			ws := t.TempDir()
			bundleDigest := cacheBundle(t, ws, cacheDir, row.bundle)
			path := oci.SignatureBundlePath(ws, cacheDir, bundleDigest)
			if row.tamper {
				if err := os.WriteFile(path, []byte("rewritten after the pull"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if row.missing {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			gotPath, err := oci.HoldRecordedSignature(ws, cacheDir, oci.RecordedBundle{Digest: bundleDigest, RecordedIn: "release.lock"}, row.madeFor)
			if gotPath != path {
				t.Errorf("returned path %q, want the bundle's %q", gotPath, path)
			}
			if row.want == nil {
				if err != nil {
					t.Fatalf("the fact must be accepted: %v", err)
				}
				return
			}
			if !errors.Is(err, row.want) {
				t.Fatalf("want %v, got %v", row.want, err)
			}
			if errors.Is(row.want, oci.ErrSignatureForAnotherArtifact()) {
				if errors.Is(err, oci.ErrSignatureBundleDigestMismatch()) {
					t.Errorf("a bundle made for another artifact was reported as a damaged one: %v", err)
				}
				for _, text := range []string{
					oci.ErrSignatureForAnotherArtifact().Error(),
					"release.lock records it",
					"cached signature bundle " + path,
					"the artifact read has manifest " + otherManifest,
				} {
					if !strings.Contains(err.Error(), text) {
						t.Errorf("the refusal does not carry %q:\n%v", text, err)
					}
				}
				if !strings.HasPrefix(err.Error(), oci.ErrSignatureForAnotherArtifact().Error()) {
					t.Errorf("the refusal does not open with the condition's sentence:\n%v", err)
				}
			}
			// The remedy names the Release, which only the reporting surface holds,
			// so the decision's own refusal names none (WithPullRemedy adds it).
			if strings.Contains(err.Error(), "remediation:") {
				t.Errorf("the decision's refusal names a remedy of its own:\n%v", err)
			}
		})
	}
}

// TestARefusalNamesNoFileWhereNoneIsRecorded holds the one optional value: a
// caller that names no file gets an occurrence that names none, rather than one
// reading " records it" after nothing.
func TestARefusalNamesNoFileWhereNoneIsRecorded(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	bundleDigest := cacheBundle(t, ws, ".aggrega/cache", keyedBundleFor(t, heldManifest))
	_, err := oci.HoldRecordedSignature(ws, ".aggrega/cache", oci.RecordedBundle{Digest: bundleDigest}, oci.MadeForManifest(otherManifest))
	if !errors.Is(err, oci.ErrSignatureForAnotherArtifact()) {
		t.Fatalf("want the another-artifact refusal, got %v", err)
	}
	if strings.Contains(err.Error(), "records it") {
		t.Errorf("an occurrence with no file named one anyway: %v", err)
	}
}

// TestSignedArtifactOfReadsEverySpellingTheFormatAdmits holds the reading to the
// spellings the bundle format's JSON mapping accepts for the one field it reads,
// and to refusing everything else as not recognised.
func TestSignedArtifactOfReadsEverySpellingTheFormatAdmits(t *testing.T) {
	t.Parallel()
	raw := []byte{0x01, 0xfe, 0xff, 0x10}
	std := base64.StdEncoding.EncodeToString(raw)
	url := base64.RawURLEncoding.EncodeToString(raw)
	rows := []struct {
		name       string
		bundle     string
		algorithm  string
		recognised bool
	}{
		{name: "JSON names, algorithm by name, standard base64", recognised: true, algorithm: "SHA2_256",
			bundle: `{"messageSignature":{"messageDigest":{"algorithm":"SHA2_256","digest":"` + std + `"}}}`},
		{name: "declared names, algorithm by number, URL-safe unpadded base64", recognised: true, algorithm: "SHA2_384",
			bundle: `{"message_signature":{"message_digest":{"algorithm":2,"digest":"` + url + `"}}}`},
		{name: "SHA2_512 by name", recognised: true, algorithm: "SHA2_512",
			bundle: `{"messageSignature":{"messageDigest":{"algorithm":"SHA2_512","digest":"` + std + `"}}}`},
		{name: "an algorithm a message signature is not verified under", bundle: `{"messageSignature":{"messageDigest":{"algorithm":"SHA3_256","digest":"` + std + `"}}}`},
		{name: "an unspecified algorithm", bundle: `{"messageSignature":{"messageDigest":{"algorithm":0,"digest":"` + std + `"}}}`},
		{name: "no message digest", bundle: `{"messageSignature":{"signature":"` + std + `"}}`},
		{name: "a null message signature", bundle: `{"messageSignature":null}`},
		{name: "an empty digest", bundle: `{"messageSignature":{"messageDigest":{"algorithm":"SHA2_256","digest":""}}}`},
		{name: "a signed envelope", bundle: `{"dsseEnvelope":{"payload":"e30=","payloadType":"application/vnd.in-toto+json"}}`},
		{name: "not JSON", bundle: `a signature bundle`},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			got, recognised := oci.SignedArtifactOf([]byte(row.bundle))
			if recognised != row.recognised {
				t.Fatalf("recognised = %v, want %v for %s", recognised, row.recognised, row.bundle)
			}
			if !row.recognised {
				return
			}
			if got.Algorithm != row.algorithm {
				t.Errorf("algorithm %q, want %q", got.Algorithm, row.algorithm)
			}
			if string(got.Digest) != string(raw) {
				t.Errorf("digest %x, want %x", got.Digest, raw)
			}
		})
	}
}

// TestNoProductCodeAsksTheReHashAloneWhetherAFactIsAccepted keeps the decision
// whether a recorded fact is accepted in the one function that makes it. The
// re-hash on its own stays exported for a reader whose question is the cache's
// own, and a product site calling it where it means "would a build accept this"
// would be a second, weaker answer to that question — which is the shape this
// guard refuses. It reads call names syntactically over every non-test file of
// the module, and it asserts its own reach: the walk must find the one decision
// called, or it is inert rather than the tree clean.
func TestNoProductCodeAsksTheReHashAloneWhetherAFactIsAccepted(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)
	var reHashCallers, decisionCallers []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "vendor" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, perr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if perr != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		dir := filepath.ToSlash(filepath.Dir(rel))
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch calleeName(call.Fun) {
			case "VerifyCachedSignatureBundle":
				if dir != "pkg/oci" {
					reHashCallers = append(reHashCallers, rel)
				}
			case "HoldRecordedSignature":
				decisionCallers = append(decisionCallers, rel)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(decisionCallers) == 0 {
		t.Fatal("the walk found no call of HoldRecordedSignature anywhere, so it matched nothing and proves nothing")
	}
	if len(reHashCallers) > 0 {
		t.Errorf("product code outside the package asks the re-hash alone whether a recorded fact is accepted — ask "+
			"oci.HoldRecordedSignature, which re-hashes and then holds the fact to the artifact it was made for:\n  %s",
			strings.Join(reHashCallers, "\n  "))
	}
}
