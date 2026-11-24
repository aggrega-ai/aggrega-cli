// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"encoding/json"
	"testing"
)

func TestReadSettingsReadsRoot(t *testing.T) {
	got, d := ReadSettings(json.RawMessage(`{"target_namespace": "acme", "root": "env/main.json", "reuse_mode": 3}`))
	if d != nil || got.Root != "env/main.json" {
		t.Fatalf("settings = %+v, diagnostic = %+v", got, d)
	}
}

func TestReadSettingsAbsentRootIsNone(t *testing.T) {
	for _, raw := range []string{``, `{}`, `[1]`, `not json`, `{"target_namespace": "acme"}`} {
		got, d := ReadSettings(json.RawMessage(raw))
		if d != nil || got.Root != "" {
			t.Fatalf("%q: settings = %+v, diagnostic = %+v", raw, got, d)
		}
	}
}

func TestReadSettingsRefusesARootThatIsNotText(t *testing.T) {
	for name, raw := range map[string]string{
		"a number":  `{"root": 31337}`,
		"null":      `{"root": null}`,
		"a list":    `{"root": ["main.json"]}`,
		"an object": `{"root": {"path": "main.json"}}`,
		"a boolean": `{"root": true}`,
	} {
		t.Run(name, func(t *testing.T) {
			got, d := ReadSettings(json.RawMessage(raw))
			sourceRefused(t, d, "root")
			if got.Root != "" {
				t.Fatalf("settings = %+v alongside a refusal", got)
			}
		})
	}
}
