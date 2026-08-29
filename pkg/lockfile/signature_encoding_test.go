// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lockfile_test

import (
	"encoding/json"
	"slices"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// codec pairs an encoding with the decoder that reads it back, so each format is
// round-tripped through its own implementation rather than through whichever one
// happens to tolerate the other's output.
type codec struct {
	name    string
	marshal func(any) ([]byte, error)
	keysOf  func([]byte) (map[string]any, error)
}

func jsonCodec() codec {
	return codec{
		name:    "json",
		marshal: func(v any) ([]byte, error) { return json.Marshal(v) },
		keysOf: func(b []byte) (map[string]any, error) {
			var m map[string]any
			return m, json.Unmarshal(b, &m)
		},
	}
}

func yamlCodec() codec {
	return codec{
		name:    "yaml",
		marshal: func(v any) ([]byte, error) { return yaml.Marshal(v) },
		keysOf: func(b []byte) (map[string]any, error) {
			var m map[string]any
			return m, yaml.Unmarshal(b, &m)
		},
	}
}

// TestVerifiedSignatureEncodesInBothConventions proves the one signature-fact
// shape emits the key convention of whichever document it lands in: snake_case in
// the lockfile's YAML, lowerCamel in the local registry index's JSON. A field
// carrying a tag for only one of the two would emit its Go field name into the
// other, which is what this pins.
func TestVerifiedSignatureEncodesInBothConventions(t *testing.T) {
	t.Parallel()
	sig := lockfile.VerifiedSignature{
		BundleDigest: "sha256:bbbb",
		Identity:     "publisher@example.com",
		Issuer:       "https://issuer.example.com",
		Mode:         "keyless",
	}

	for _, tc := range []struct {
		codec codec
		want  []string
	}{
		{codec: jsonCodec(), want: []string{"bundleDigest", "identity", "issuer", "mode"}},
		{codec: yamlCodec(), want: []string{"bundle_digest", "identity", "issuer", "mode"}},
	} {
		t.Run(tc.codec.name, func(t *testing.T) {
			t.Parallel()
			data, err := tc.codec.marshal(sig)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			got, derr := tc.codec.keysOf(data)
			if derr != nil {
				t.Fatalf("decode encoded form: %v", derr)
			}
			for _, key := range tc.want {
				if _, ok := got[key]; !ok {
					t.Errorf("emitted keys %v are missing %q", sortedKeys(got), key)
				}
			}
			if len(got) != len(tc.want) {
				t.Errorf("emitted %v; want exactly %v", sortedKeys(got), tc.want)
			}
		})
	}
}

// TestVerifiedSignatureOmitsEmptyIdentityInBothConventions proves a keyed
// signature — which carries no identity and no issuer — omits both keys from
// either encoding rather than writing an empty value into one of them. A field's
// optionality is one decision, so the two encodings express it alike.
func TestVerifiedSignatureOmitsEmptyIdentityInBothConventions(t *testing.T) {
	t.Parallel()
	keyed := lockfile.VerifiedSignature{BundleDigest: "sha256:cccc", Mode: "keyed"}

	for _, c := range []codec{jsonCodec(), yamlCodec()} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			data, err := c.marshal(keyed)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			got, derr := c.keysOf(data)
			if derr != nil {
				t.Fatalf("decode encoded form: %v", derr)
			}
			if len(got) != 2 {
				t.Errorf("keyed signature emitted %v; want only the bundle digest and the mode", sortedKeys(got))
			}
		})
	}
}

// TestVerifiedSignatureDecodesGoFieldNameKeys proves a JSON object written before
// the signature type declared its JSON key names — whose keys are therefore the Go
// field names — still decodes with every field intact. Without that, declaring the
// key names would strand every index record already on disk, and recovering one
// would mean rebuilding the index rather than reading it.
func TestVerifiedSignatureDecodesGoFieldNameKeys(t *testing.T) {
	t.Parallel()
	const goFieldNameShape = `{
	  "BundleDigest": "sha256:bbbb",
	  "Identity": "publisher@example.com",
	  "Issuer": "https://issuer.example.com",
	  "Mode": "keyless"
	}`
	var got lockfile.VerifiedSignature
	if err := json.Unmarshal([]byte(goFieldNameShape), &got); err != nil {
		t.Fatalf("decode Go-field-name shape: %v", err)
	}
	want := lockfile.VerifiedSignature{
		BundleDigest: "sha256:bbbb",
		Identity:     "publisher@example.com",
		Issuer:       "https://issuer.example.com",
		Mode:         "keyless",
	}
	if got != want {
		t.Errorf("Go-field-name shape decoded to %+v; want %+v", got, want)
	}
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
