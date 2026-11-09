// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginstrust

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	schemas "github.com/aggrega-ai/aggrega-schemas"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// testSignature is a keyed fact of the shape a pull records.
func testSignature() *lockfile.VerifiedSignature {
	return &lockfile.VerifiedSignature{Mode: "keyed", Identity: "keyed:sha256:probe", BundleDigest: testDigest}
}

// TestASignatureFactRoundTripsThroughTheRecord pins that a recorded signature fact
// reads back as it was written, under the lockfile's own key names, and that a
// record carrying none writes no `signature` key at all — which is what keeps a
// record for an unsigned artifact byte-identical to one written before the field
// existed.
func TestASignatureFactRoundTripsThroughTheRecord(t *testing.T) {
	t.Parallel()

	signed := &Trust{Version: CurrentVersion, Plugins: []TrustedPlugin{{
		Plugin: "governance/baseline", ResolvedVersion: "1.0.0", Digest: testDigest, Signature: testSignature(),
	}}}
	path := write(t, signed)
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load a record carrying a signature fact: %v", err)
	}
	if len(got.Plugins) != 1 || !got.Plugins[0].Equal(signed.Plugins[0]) {
		t.Fatalf("the record read back as %+v; want %+v", got.Plugins, signed.Plugins)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the written record: %v", err)
	}
	for _, key := range []string{"signature:", "bundle_digest:", "identity:", "mode: keyed"} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("the written record does not carry %q:\n%s", key, raw)
		}
	}

	unsigned := &Trust{Version: CurrentVersion, Plugins: []TrustedPlugin{{
		Plugin: "governance/baseline", ResolvedVersion: "1.0.0", Digest: testDigest,
	}}}
	bytesOf, err := CanonicalBytes(unsigned)
	if err != nil {
		t.Fatalf("CanonicalBytes: %v", err)
	}
	// The header mentions a "signature" block in prose, so the body is what is read.
	body := strings.TrimPrefix(string(bytesOf), header)
	if strings.Contains(body, "signature") {
		t.Errorf("a record carrying no signature fact must write no signature key:\n%s", body)
	}
}

// TestRecordSignatureFollowsArtifactIdentity walks the one home of where a
// signature fact is recorded: on the entry already naming the verified bytes, and
// nowhere else — and, unlike a withdrawal, a nil fact clears what the entry held,
// because every pull answers the question afresh.
func TestRecordSignatureFollowsArtifactIdentity(t *testing.T) {
	t.Parallel()

	const coordinate = "governance/baseline"
	other := "sha256:" + strings.Repeat("e", 64)
	held := func(sig *lockfile.VerifiedSignature) *Trust {
		return &Trust{Version: CurrentVersion, Plugins: []TrustedPlugin{{
			Plugin: coordinate, ResolvedVersion: "1.0.0", Digest: testDigest, Signature: sig,
		}}}
	}

	cases := []struct {
		name       string
		record     *Trust
		coordinate string
		digest     string
		sig        *lockfile.VerifiedSignature
		changed    bool
		want       *lockfile.VerifiedSignature
	}{
		{name: "a fact recorded on the entry naming those bytes", record: held(nil), coordinate: coordinate,
			digest: testDigest, sig: testSignature(), changed: true, want: testSignature()},
		{name: "the same fact again changes nothing", record: held(testSignature()), coordinate: coordinate,
			digest: testDigest, sig: testSignature(), changed: false, want: testSignature()},
		{name: "a nil fact clears the one held", record: held(testSignature()), coordinate: coordinate,
			digest: testDigest, sig: nil, changed: true, want: nil},
		{name: "a nil fact over none changes nothing", record: held(nil), coordinate: coordinate,
			digest: testDigest, sig: nil, changed: false, want: nil},
		{name: "a fact about other bytes is not recorded", record: held(nil), coordinate: coordinate,
			digest: other, sig: testSignature(), changed: false, want: nil},
		{name: "an unheld coordinate records nothing", record: held(nil), coordinate: "linter/other",
			digest: testDigest, sig: testSignature(), changed: false, want: nil},
		{name: "an empty coordinate records nothing", record: held(nil), coordinate: "",
			digest: testDigest, sig: testSignature(), changed: false, want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.record.RecordSignature(tc.coordinate, tc.digest, tc.sig); got != tc.changed {
				t.Errorf("RecordSignature reported changed=%t; want %t", got, tc.changed)
			}
			entry, _ := tc.record.Find(coordinate)
			if !sameAddressed(entry.Signature, tc.want) {
				t.Errorf("the entry holds %+v; want %+v", entry.Signature, tc.want)
			}
		})
	}

	// The fact is copied in, so a caller reusing its value cannot reach into the
	// record through it.
	record := held(nil)
	sig := testSignature()
	record.RecordSignature(coordinate, testDigest, sig)
	sig.Mode = "keyless"
	if entry, _ := record.Find(coordinate); entry.Signature.Mode != "keyed" {
		t.Errorf("mutating the caller's fact reached the record: %+v", entry.Signature)
	}

	var absent *Trust
	if absent.RecordSignature(coordinate, testDigest, testSignature()) {
		t.Error("a nil Trust records nothing")
	}
}

// TestEqualComparesTheSignatureByWhatItRecords holds the record comparison to the
// new pointer field: two equal facts at different addresses are one record, and a
// fact against none is two.
func TestEqualComparesTheSignatureByWhatItRecords(t *testing.T) {
	t.Parallel()
	base := TrustedPlugin{Plugin: "governance/baseline", ResolvedVersion: "1.0.0", Digest: testDigest}
	a, b := base, base
	a.Signature, b.Signature = testSignature(), testSignature()
	if !a.Equal(b) {
		t.Error("two records carrying equal signature facts at different addresses must be equal")
	}
	if a.Equal(base) || base.Equal(a) {
		t.Error("a record carrying a signature fact and one carrying none must differ")
	}
	b.Signature.Mode = "keyless"
	if a.Equal(b) {
		t.Error("records carrying different signature facts must differ")
	}
}

// TestCanonicalBytesDoesNotShareTheSignatureWithTheCaller pins the deep copy the
// writer owes every pointer field: the canonical form must not hand the caller's
// own fact back to be mutated.
func TestCanonicalBytesDoesNotShareTheSignatureWithTheCaller(t *testing.T) {
	t.Parallel()
	in := &Trust{Version: CurrentVersion, Plugins: []TrustedPlugin{{
		Plugin: "governance/baseline", ResolvedVersion: "1.0.0", Digest: testDigest, Signature: testSignature(),
	}}}
	out := canonicalise(in)
	if out.Plugins[0].Signature == in.Plugins[0].Signature {
		t.Fatal("the canonical form shares the caller's signature fact rather than copying it")
	}
	out.Plugins[0].Signature.Mode = "keyless"
	if in.Plugins[0].Signature.Mode != "keyed" {
		t.Error("mutating the canonical form reached the caller's record")
	}
}

// TestTheSignatureFactIsDeclaredAlikeInBothRecords holds the one Go fact type's
// two schema declarations together. The lockfile and this record each declare the
// `VerifiedSignature` shape in their own document, while the Go type both decode
// into is one. So the two declarations are compared structurally — every keyword
// but the prose — and a property, a constraint or an enum member present in one and
// not the other fails here, naming both.
func TestTheSignatureFactIsDeclaredAlikeInBothRecords(t *testing.T) {
	t.Parallel()
	declaration := func(t *testing.T, name, schema string) any {
		t.Helper()
		var doc map[string]any
		if err := json.Unmarshal([]byte(schema), &doc); err != nil {
			t.Fatalf("decode the %s schema: %v", name, err)
		}
		defs, _ := doc["$defs"].(map[string]any)
		def, ok := defs["VerifiedSignature"]
		if !ok {
			t.Fatalf("the %s schema declares no $defs.VerifiedSignature, so there is nothing to compare", name)
		}
		return withoutDescriptions(def)
	}
	lock := declaration(t, "lockfile", schemas.LockfileV1beta1)
	trust := declaration(t, "plugin trust record", schemas.PluginsTrustV1beta1)
	// Calibration: a declaration reduced to nothing would make the comparison below
	// hold over two empty values.
	props, _ := lock.(map[string]any)["properties"].(map[string]any)
	if len(props) == 0 {
		t.Fatal("the lockfile's declaration reduced to no properties, so the comparison would assert nothing")
	}
	if !reflect.DeepEqual(lock, trust) {
		l, _ := json.MarshalIndent(lock, "", "  ")
		r, _ := json.MarshalIndent(trust, "", "  ")
		t.Errorf("the two declarations of the signature fact differ:\nlockfile:\n%s\nplugin trust record:\n%s", l, r)
	}

	// And the trust record's per-plugin key points at its declaration, so the key a
	// lockfile row and a trust entry carry the fact under is the same one.
	var doc struct {
		Properties struct {
			Plugins struct {
				Items struct {
					Properties map[string]struct {
						Ref string `json:"$ref"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"plugins"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(schemas.PluginsTrustV1beta1), &doc); err != nil {
		t.Fatalf("decode the plugin trust record schema: %v", err)
	}
	if ref := doc.Properties.Plugins.Items.Properties["signature"].Ref; ref != "#/$defs/VerifiedSignature" {
		t.Errorf("plugins[].signature references %q; want the record's own #/$defs/VerifiedSignature", ref)
	}
}

// withoutDescriptions returns v with every `description` keyword removed at every
// depth, so two declarations of one shape compare on structure alone.
func withoutDescriptions(v any) any {
	switch node := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(node))
		for k, child := range node {
			if k == "description" {
				continue
			}
			out[k] = withoutDescriptions(child)
		}
		return out
	case []any:
		out := make([]any, len(node))
		for i, child := range node {
			out[i] = withoutDescriptions(child)
		}
		return out
	default:
		return v
	}
}
