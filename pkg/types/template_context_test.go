// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"encoding/json"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestInjectPipe_NestedOrderedMapRoundTrip pins the nested-OrderedMap shape
// on InjectPipe.Additional. The codebase had not previously shipped a
// production struct field of type OrderedMap[OrderedMap[V]]; the gamma
// reflection caches must fire correctly on the inner OrderedMap during
// marshal/unmarshal at both depths. Encode → decode → byte-equivalent round
// trip; assert the inner OrderedMap[string] preserves author key order.
func TestInjectPipe_NestedOrderedMapRoundTrip(t *testing.T) {
	t.Parallel()

	build := func() InjectPipe {
		var pipe InjectPipe
		ssmParam := OrderedMap[string]{}
		ssmParam.Set("DB_URL", "connection.url")
		ssmParam.Set("API_KEY", "connection.token")
		pipe.Additional.Set("ssm_param", ssmParam)
		return pipe
	}

	// YAML round-trip
	t.Run("YAML", func(t *testing.T) {
		pipe := build()
		out, err := yaml.Marshal(&pipe)
		if err != nil {
			t.Fatalf("YAML marshal: %v", err)
		}
		var pipe2 InjectPipe
		if err := yaml.Unmarshal(out, &pipe2); err != nil {
			t.Fatalf("YAML re-unmarshal: %v\noutput: %s", err, out)
		}
		assertNestedShape(t, &pipe2, out)
	})

	// JSON round-trip
	t.Run("JSON", func(t *testing.T) {
		pipe := build()
		out, err := json.Marshal(&pipe)
		if err != nil {
			t.Fatalf("JSON marshal: %v", err)
		}
		var pipe2 InjectPipe
		if err := json.Unmarshal(out, &pipe2); err != nil {
			t.Fatalf("JSON re-unmarshal: %v\noutput: %s", err, out)
		}
		assertNestedShape(t, &pipe2, out)
	})
}

func assertNestedShape(t *testing.T, pipe *InjectPipe, encoded []byte) {
	t.Helper()
	if pipe.Additional.Len() != 1 {
		t.Fatalf("Additional.Len = %d, want 1; encoded: %s", pipe.Additional.Len(), encoded)
	}
	inner, ok := pipe.Additional.Get("ssm_param")
	if !ok {
		t.Fatalf("Additional missing key ssm_param; encoded: %s", encoded)
	}
	if inner.Len() != 2 {
		t.Fatalf("inner ssm_param.Len = %d, want 2; encoded: %s", inner.Len(), encoded)
	}
	wantOrder := []string{"DB_URL", "API_KEY"}
	gotOrder := inner.Keys()
	for i, w := range wantOrder {
		if i >= len(gotOrder) || gotOrder[i] != w {
			t.Errorf("inner Keys[%d] = %v, want %q; encoded: %s", i, gotOrder, w, encoded)
		}
	}
	if v, _ := inner.Get("DB_URL"); v != "connection.url" {
		t.Errorf("inner.Get(DB_URL) = %q, want %q", v, "connection.url")
	}
	if v, _ := inner.Get("API_KEY"); v != "connection.token" {
		t.Errorf("inner.Get(API_KEY) = %q, want %q", v, "connection.token")
	}
}
