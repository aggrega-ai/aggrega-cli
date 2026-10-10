// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"strings"
	"testing"
)

// TestTheSignatureCheckIsTheWholeHaltDecision walks the grid the halt decision is
// a function of — where the requirement comes from, which switch stands it down,
// whether the host is advisory, and whether the kind is in scope or undetermined —
// and holds HaltsWithoutSignature to the rule a pull applies to a missing
// signature, cell by cell.
//
// The cells that interact are the reason it is a grid rather than one case per
// setting: a host's own skip stands down the host's own stance and NOT the
// top-level policy, and an undetermined kind is required under an active policy
// at either scope even where the policy's kinds list would not name it.
func TestTheSignatureCheckIsTheWholeHaltDecision(t *testing.T) {
	t.Parallel()
	const host = "registry.example.com"
	entry := func(mod func(*RegistryConfig)) []RegistryConfig {
		r := RegistryConfig{Host: host}
		if mod != nil {
			mod(&r)
		}
		return []RegistryConfig{r}
	}
	rows := []struct {
		name      string
		cfg       Config
		kind      string
		halts     bool
		required  bool
		skip      SignatureSkip
		sourceHas []string
	}{
		{name: "nothing configured requires nothing", cfg: Config{}, kind: "Component"},
		{name: "a host entry with no stance requires nothing", cfg: Config{Registries: entry(nil)}, kind: "Component"},
		{
			name: "the top-level policy requires an in-scope kind from any host",
			cfg:  Config{Signing: SigningConfig{Required: true}}, kind: "Component",
			halts: true, required: true, sourceHas: []string{"top level of the workspace configuration"},
		},
		{
			name: "the top-level policy scoped to other kinds leaves this one",
			cfg:  Config{Signing: SigningConfig{Required: true, Kinds: []string{"Plugin"}}}, kind: "Component",
		},
		{
			name: "the top-level policy never exempts an undetermined kind",
			cfg:  Config{Signing: SigningConfig{Required: true, Kinds: []string{"Plugin"}}}, kind: "",
			halts: true, required: true, sourceHas: []string{"top level"},
		},
		{
			name: "the host's own stance requires its artifacts",
			cfg:  Config{Registries: entry(func(r *RegistryConfig) { r.Signing.Required = true })}, kind: "Component",
			halts: true, required: true, sourceHas: []string{"`registries` entry for \"" + host + "\""},
		},
		{
			name: "the host's own stance never exempts an undetermined kind",
			cfg: Config{Registries: entry(func(r *RegistryConfig) {
				r.Signing.Required = true
				r.Signing.Kinds = []string{"Plugin"}
			})}, kind: "",
			halts: true, required: true, sourceHas: []string{"`registries` entry"},
		},
		{
			name: "an advisory host reports and does not halt",
			cfg: Config{Registries: entry(func(r *RegistryConfig) {
				r.Signing.Required = true
				r.Advisory = true
			})}, kind: "Component",
			required: true,
		},
		{
			name: "an advisory host does not halt under the top-level policy either",
			cfg: Config{Signing: SigningConfig{Required: true}, Registries: entry(func(r *RegistryConfig) {
				r.Advisory = true
			})}, kind: "Component",
			required: true,
		},
		{
			name: "a host's own skip stands down the host's own stance",
			cfg: Config{Registries: entry(func(r *RegistryConfig) {
				r.Signing.Required = true
				r.SkipSignatureVerification = true
			})}, kind: "Component",
			required: true, skip: SignatureSkipRegistry,
		},
		{
			name: "a host's own skip does not stand down the top-level policy",
			cfg: Config{Signing: SigningConfig{Required: true}, Registries: entry(func(r *RegistryConfig) {
				r.SkipSignatureVerification = true
			})}, kind: "Component",
			halts: true, required: true, sourceHas: []string{"top level"},
		},
		{
			name: "the global skip stands down every requirement",
			cfg: Config{SkipSignatureVerification: true, Signing: SigningConfig{Required: true},
				Registries: entry(func(r *RegistryConfig) { r.Signing.Required = true })}, kind: "Component",
			required: true, skip: SignatureSkipGlobal,
		},
		{
			name: "both scopes requiring are both named",
			cfg: Config{Signing: SigningConfig{Required: true},
				Registries: entry(func(r *RegistryConfig) { r.Signing.Required = true })}, kind: "Component",
			halts: true, required: true, sourceHas: []string{"top level", "`registries` entry"},
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			check := row.cfg.SignatureCheckFor(host, row.kind)
			if got := check.HaltsWithoutSignature(); got != row.halts {
				t.Errorf("HaltsWithoutSignature() = %v, want %v (check %+v)", got, row.halts, check)
			}
			if got := check.Required(); got != row.required {
				t.Errorf("Required() = %v, want %v", got, row.required)
			}
			if check.Skip != row.skip {
				t.Errorf("Skip = %v, want %v", check.Skip, row.skip)
			}
			source := check.RequirementSource()
			if !row.required && source != "" {
				t.Errorf("nothing requires a signature, yet the source names %q", source)
			}
			for _, want := range row.sourceHas {
				if !strings.Contains(source, want) {
					t.Errorf("RequirementSource() = %q, want it to name %q", source, want)
				}
			}
		})
	}
}

// TestTheHubBaselineIsNamedAsTheHubsOwnRequirement holds the requirement source
// to the official hub's built-in policy where no registries entry overrides it,
// and to the entry where one does: the two remedies differ, so the refusal has to
// say which one the reader is looking at.
func TestTheHubBaselineIsNamedAsTheHubsOwnRequirement(t *testing.T) {
	t.Parallel()
	if !DefaultBaseline().Signing.Required {
		t.Fatal("the built-in baseline requires no signature, so this case cannot tell its source from nothing")
	}
	baseline := Config{}.SignatureCheckFor(HubBaselineHost, "Component")
	if !baseline.HaltsWithoutSignature() {
		t.Fatalf("the official hub's built-in requirement must halt a missing signature: %+v", baseline)
	}
	if src := baseline.RequirementSource(); !strings.Contains(src, "official hub's built-in signature requirement") {
		t.Errorf("the baseline is not named as the hub's own requirement: %q", src)
	}

	overridden := Config{Registries: []RegistryConfig{{Host: HubBaselineHost, Signing: SigningConfig{Required: true}}}}.
		SignatureCheckFor(HubBaselineHost, "Component")
	if src := overridden.RequirementSource(); !strings.Contains(src, "`registries` entry") {
		t.Errorf("an entry for the hub is not named as the entry the reader wrote: %q", src)
	}
}
