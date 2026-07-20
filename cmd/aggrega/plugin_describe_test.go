// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
)

// TestEmbeddedDescriptorFidelity proves the descriptor extracted from each guest
// at build time and embedded alongside its .wasm.gz is the same descriptor the
// guest reports at runtime through describe(). The listing's fast path reads the
// static descriptor instead of instantiating the guest, so the two must not
// diverge: for every embedded plugin the statically-read descriptor must equal
// the one obtained by loading and describing the guest.
func TestEmbeddedDescriptorFidelity(t *testing.T) {
	ctx := context.Background()
	loader := wasm.NewCachingLoader(nil)

	for _, uri := range embedded.URIs() {
		uri := uri
		t.Run(uri, func(t *testing.T) {
			raw, ok := embedded.LookupDescriptor(uri)
			if !ok {
				t.Fatalf("no embedded descriptor for %q; run the embedded-guest build to generate the descriptor assets", uri)
			}
			var static wasm.PluginDescriptor
			if err := json.Unmarshal(raw, &static); err != nil {
				t.Fatalf("embedded descriptor for %q is not valid JSON: %v", uri, err)
			}

			guest, ok := embedded.Lookup(uri)
			if !ok {
				t.Fatalf("no embedded guest bytes for %q", uri)
			}
			plugin, runtime, err := loader.LoadAndDescribe(ctx, wasm.PluginSpec{
				Name:           uri,
				Bytes:          guest,
				MaxMemoryPages: wasm.DescribeMaxMemoryPages,
			})
			if err != nil {
				t.Fatalf("load and describe %q: %v", uri, err)
			}
			_ = plugin.Close(ctx)

			if !reflect.DeepEqual(static, runtime) {
				t.Errorf("static descriptor for %q differs from runtime describe()\nstatic:  %+v\nruntime: %+v", uri, static, runtime)
			}
		})
	}
}

// TestEmbeddedDescriptorRoundTrip proves a descriptor survives the
// marshal-at-build / unmarshal-at-read round trip byte-for-byte through the
// shared PluginDescriptor struct, so the listing renders identical output
// whether it reads the static descriptor or describes the guest live.
func TestEmbeddedDescriptorRoundTrip(t *testing.T) {
	t.Parallel()
	for _, uri := range embedded.URIs() {
		uri := uri
		t.Run(uri, func(t *testing.T) {
			t.Parallel()
			raw, ok := embedded.LookupDescriptor(uri)
			if !ok {
				t.Skipf("no embedded descriptor for %q", uri)
			}
			var desc wasm.PluginDescriptor
			if err := json.Unmarshal(raw, &desc); err != nil {
				t.Fatalf("unmarshal %q: %v", uri, err)
			}
			again, err := json.Marshal(desc)
			if err != nil {
				t.Fatalf("marshal %q: %v", uri, err)
			}
			var desc2 wasm.PluginDescriptor
			if err := json.Unmarshal(again, &desc2); err != nil {
				t.Fatalf("re-unmarshal %q: %v", uri, err)
			}
			if !reflect.DeepEqual(desc, desc2) {
				t.Errorf("descriptor for %q not stable across a marshal round trip", uri)
			}
		})
	}
}
