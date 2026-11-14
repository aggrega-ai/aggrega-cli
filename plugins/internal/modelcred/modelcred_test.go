// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package modelcred

import (
	"strings"
	"testing"
)

// TestTheCredentialIsReadFromTheVariableTheSourceOrTheProviderNames pins the
// variable an agent's model credential is read from: the one the source states,
// whatever the provider; else, for a provider whose client libraries read one by
// convention, that one, the secret's text saying the source names none; else the
// one importer.ConnectionCredential names after the model's key. Each is
// attached on the agent's connection to the model.
//
// 🔴 PLANTED DEFECT: in Credential, drop the stated branch: the stated row reads
// OPENAI_API_KEY instead of the source's variable and fails.
func TestTheCredentialIsReadFromTheVariableTheSourceOrTheProviderNames(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		scenario, component, stated, variable string
		saysNone                              bool
	}{
		{"stated by the source", "core/ai-model/openai", "MY_OPENAI_KEY", "MY_OPENAI_KEY", false},
		{"OpenAI by convention", "core/ai-model/openai", "", "OPENAI_API_KEY", true},
		{"Gemini by convention", "core/ai-model/gemini", "", "GOOGLE_API_KEY", true},
		{"Anthropic by convention", "core/ai-model/anthropic", "", "ANTHROPIC_API_KEY", true},
		{"a provider with no convention", "imported/ai-model/acme", "", "GPT_4O_CREDENTIAL", false},
	} {
		s := Credential("agent", "model", "gpt-4o", "gpt-4o", row.component, row.stated)
		if s.Variable != row.variable || s.Path != "uses.model.gpt-4o.features.credential" {
			t.Errorf("%s: the credential is read from %q at %q, want %q at uses.model.gpt-4o.features.credential",
				row.scenario, s.Variable, s.Path, row.variable)
		}
		if got := strings.Contains(s.Text, "the source names no variable for it"); got != row.saysNone {
			t.Errorf("%s: the text %q says the source names none: %v, want %v", row.scenario, s.Text, got, row.saysNone)
		}
	}
}
