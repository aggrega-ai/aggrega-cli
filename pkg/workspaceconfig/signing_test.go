// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package workspaceconfig

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestSigningConfigLoadsFromConfig proves the signing block round-trips from
// the workspace file through schema validation at both the global and the
// per-registry level.
func TestSigningConfigLoadsFromConfig(t *testing.T) {
	t.Parallel()
	content := `
workspace: {}
signing:
  fulcio_url: https://fulcio.global.example
  rekor_url: https://rekor.global.example
  key_path: /keys/global.pem
registries:
  - host: hub.aggrega.ai
    signing:
      fulcio_url: https://fulcio.hub.example
      key_path: /keys/hub.pem
`
	cfg, err := load(writeFixture(t, content))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Signing.FulcioURL != "https://fulcio.global.example" {
		t.Errorf("global fulcio_url = %q", cfg.Signing.FulcioURL)
	}
	if len(cfg.Registries) != 1 || cfg.Registries[0].Signing.FulcioURL != "https://fulcio.hub.example" {
		t.Errorf("per-registry signing did not load: %+v", cfg.Registries)
	}
}

// TestResolveSigningPrecedence proves per-registry signing overrides the
// global block field by field, with empty per-registry fields falling
// through to the global value. CLI-flag precedence is applied above this
// method by the cmd layer; defaults are applied below it in buildSigner.
func TestResolveSigningPrecedence(t *testing.T) {
	t.Parallel()
	cfg := Config{
		Signing: SigningConfig{
			FulcioURL:    "https://fulcio.global",
			RekorURL:     "https://rekor.global",
			TimestampURL: "https://tsa.global",
			KeyPath:      "/keys/global.pem",
		},
		Registries: []RegistryConfig{
			{
				Host: "hub.aggrega.ai",
				Signing: SigningConfig{
					FulcioURL: "https://fulcio.hub",
					KeyPath:   "/keys/hub.pem",
				},
			},
		},
	}

	// Per-registry wins where set; global fills the unset fields.
	got := cfg.ResolveSigning("hub.aggrega.ai")
	if got.FulcioURL != "https://fulcio.hub" {
		t.Errorf("FulcioURL = %q, want per-registry override", got.FulcioURL)
	}
	if got.KeyPath != "/keys/hub.pem" {
		t.Errorf("KeyPath = %q, want per-registry override", got.KeyPath)
	}
	if got.RekorURL != "https://rekor.global" {
		t.Errorf("RekorURL = %q, want global fallthrough", got.RekorURL)
	}
	if got.TimestampURL != "https://tsa.global" {
		t.Errorf("TimestampURL = %q, want global fallthrough", got.TimestampURL)
	}

	// An unconfigured host resolves to the global block unchanged.
	other := cfg.ResolveSigning("other.example")
	if !reflect.DeepEqual(other, cfg.Signing) {
		t.Errorf("unconfigured host = %+v, want global %+v", other, cfg.Signing)
	}
}

// TestSigningPolicyRequiredComposesMonotonicOR proves the org signing
// requirement is un-removable by an ordinary config edit: a CI-owned overlay
// setting required:true wins over a base config setting required:false, so a
// lower-precedence layer cannot shadow a higher one.
func TestSigningPolicyRequiredComposesMonotonicOR(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".aggrega-config.yaml"), "workspace: {}\nsigning:\n  required: false\n")
	writeFile(t, filepath.Join(root, ".aggrega-config-append-ci.yaml"), "signing:\n  required: true\n")

	rw, err := Resolve(root, "", fakeEnv(nil))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !rw.Config().Signing.Required {
		t.Errorf("required: got false; a CI-overlay required:true must compose monotonically over a base required:false")
	}
	if !rw.Config().SigningRequiredForKind("Component") {
		t.Errorf("SigningRequiredForKind(Component): got false; want true (required composed on, empty kinds = all)")
	}
}

// TestSigningPolicyKindsComposeUnion proves signing.kinds unions across layers:
// a base config scoping the policy to one kind and a CI overlay scoping it to
// another resolve to both, in first-appearance order. A layer widens the scope;
// it cannot shrink it.
func TestSigningPolicyKindsComposeUnion(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".aggrega-config.yaml"), "workspace: {}\nsigning:\n  required: true\n  kinds:\n    - Component\n")
	writeFile(t, filepath.Join(root, ".aggrega-config-append-ci.yaml"), "signing:\n  kinds:\n    - Feature\n")

	rw, err := Resolve(root, "", fakeEnv(nil))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	got := rw.Config().Signing.Kinds
	if !reflect.DeepEqual(got, []string{"Component", "Feature"}) {
		t.Errorf("kinds union: got %v; want [Component Feature]", got)
	}
	c := rw.Config()
	if !c.SigningRequiredForKind("Component") || !c.SigningRequiredForKind("Feature") {
		t.Errorf("both scoped kinds must require signing; got Component=%v Feature=%v",
			c.SigningRequiredForKind("Component"), c.SigningRequiredForKind("Feature"))
	}
	if c.SigningRequiredForKind("Recipe") {
		t.Errorf("Recipe is out of the scoped kinds; SigningRequiredForKind(Recipe) must be false")
	}
}

// TestSigningRequiredForKindDefaultAllKinds proves that required:true with an
// empty kinds list scopes the policy to every kind.
func TestSigningRequiredForKindDefaultAllKinds(t *testing.T) {
	t.Parallel()
	c := Config{Signing: SigningConfig{Required: true}}
	for _, kind := range []string{"Component", "Feature", "Recipe", "FeatureRecipe", "SchemaLibrary", "MappingLibrary", "Aggregate", "Plugin"} {
		if !c.SigningRequiredForKind(kind) {
			t.Errorf("empty kinds must mean all kinds; SigningRequiredForKind(%q) = false", kind)
		}
	}
	// The "all" wildcard is equivalent to an empty list.
	cAll := Config{Signing: SigningConfig{Required: true, Kinds: []string{"all"}}}
	if !cAll.SigningRequiredForKind("Plugin") {
		t.Errorf(`kinds: [all] must mean all kinds; SigningRequiredForKind(Plugin) = false`)
	}
}

// TestSigningRequiredForKindDefaultsOffWhenAbsent proves the non-regression
// case: with no signing policy present, no kind requires signing.
func TestSigningRequiredForKindDefaultsOffWhenAbsent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".aggrega-config.yaml"), "workspace: {}\n")
	rw, err := Resolve(root, "", fakeEnv(nil))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if rw.Config().Signing.Required {
		t.Errorf("required: got true; an absent signing policy must default to false")
	}
	if rw.Config().SigningRequiredForKind("Component") {
		t.Errorf("SigningRequiredForKind(Component): got true; want false when no policy is set")
	}
}

// TestSigningRequiredForKindMembership pins the per-kind decision: the required
// gate short-circuits false, an explicit kind list is exact membership, and the
// "all" wildcard matches every kind.
func TestSigningRequiredForKindMembership(t *testing.T) {
	t.Parallel()
	scoped := Config{Signing: SigningConfig{Required: true, Kinds: []string{"Component", "Plugin"}}}
	if !scoped.SigningRequiredForKind("Component") {
		t.Errorf("Component is in scope; want true")
	}
	if !scoped.SigningRequiredForKind("Plugin") {
		t.Errorf("Plugin is in scope; want true")
	}
	if scoped.SigningRequiredForKind("Feature") {
		t.Errorf("Feature is out of scope; want false")
	}

	// required:false short-circuits regardless of kinds.
	off := Config{Signing: SigningConfig{Required: false, Kinds: []string{"Component"}}}
	if off.SigningRequiredForKind("Component") {
		t.Errorf("required:false must short-circuit to false even for a listed kind")
	}
}

// TestSigningKindsUnknownFailsLoudly proves an unknown kind in signing.kinds
// fails config load with a wrapped ErrConfigInvalid naming the offending value.
func TestSigningKindsUnknownFailsLoudly(t *testing.T) {
	t.Parallel()
	_, err := load(writeFixture(t, "workspace: {}\nsigning:\n  required: true\n  kinds:\n    - Bogus\n"))
	if err == nil {
		t.Fatal("expected an unknown signing.kinds entry to be rejected; got nil")
	}
	if !errors.Is(err, ErrConfigInvalid()) {
		t.Errorf("error should wrap ErrConfigInvalid; got %v", err)
	}
	if !strings.Contains(err.Error(), "Bogus") {
		t.Errorf("error should name the offending kind %q; got %v", "Bogus", err)
	}
}

// TestPerRegistrySigningPolicyActivates proves the signing-block stance fields
// (required / kinds) are honored on a per-registry signing block: they are the
// one required/kinds concept the top-level policy carries, scoped by placement to
// that host. A per-host required with a kinds scope demands a signature for the
// listed kinds on that host and not for others.
func TestPerRegistrySigningPolicyActivates(t *testing.T) {
	t.Parallel()
	content := "workspace: {}\nregistries:\n  - host: reg.example\n    signing:\n      required: true\n      kinds:\n        - Component\n"
	cfg, err := load(writeFixture(t, content))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.RegistrySigningRequiredForKind("reg.example", "Component") {
		t.Error("per-registry signing.required must demand a signature for the in-scope kind")
	}
	if cfg.RegistrySigningRequiredForKind("reg.example", "Feature") {
		t.Error("per-registry kinds scope must exclude an out-of-scope kind")
	}
	if cfg.RegistrySigningRequiredForKind("other.example", "Component") {
		t.Error("an unlisted host must not inherit another host's required stance")
	}
}

// TestPerRegistrySigningKindsValidated proves a per-registry signing.kinds entry
// that is not a signable artifact kind is rejected at load, wrapping
// ErrConfigInvalid and naming the offending kind — the same validation the
// top-level kinds list receives.
func TestPerRegistrySigningKindsValidated(t *testing.T) {
	t.Parallel()
	content := "workspace: {}\nregistries:\n  - host: reg.example\n    signing:\n      kinds:\n        - Bogus\n"
	_, err := load(writeFixture(t, content))
	if err == nil {
		t.Fatal("expected an unknown per-registry signing.kinds entry to be rejected; got nil")
	}
	if !errors.Is(err, ErrConfigInvalid()) {
		t.Errorf("error should wrap ErrConfigInvalid; got %v", err)
	}
	if !strings.Contains(err.Error(), "Bogus") {
		t.Errorf("error should name the offending kind %q; got %v", "Bogus", err)
	}
}

// TestPerRegistrySigningEndpointsAccepted is the control case: a per-registry
// signing block carrying only endpoint fields (no policy fields) loads fine,
// since those fields are honored per registry.
func TestPerRegistrySigningEndpointsAccepted(t *testing.T) {
	t.Parallel()
	content := "workspace: {}\nregistries:\n  - host: reg.example\n    signing:\n      fulcio_url: https://fulcio.reg.example\n"
	cfg, err := load(writeFixture(t, content))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.Registries) != 1 || cfg.Registries[0].Signing.FulcioURL != "https://fulcio.reg.example" {
		t.Errorf("per-registry signing endpoint did not load: %+v", cfg.Registries)
	}
}

// TestRetiredRegistryKeysRejected proves the clean break: the retired
// per-registry keys are no longer accepted. The schema's additionalProperties:
// false rejects requires_signatures (folded into signing.required) and
// skip_verification (renamed skip_signature_verification) loudly at load.
func TestRetiredRegistryKeysRejected(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"requires_signatures: true", "skip_verification: true"} {
		key := key
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			content := "workspace: {}\nregistries:\n  - host: reg.example\n    " + key + "\n"
			_, err := load(writeFixture(t, content))
			if err == nil {
				t.Fatalf("retired key %q must be rejected; got nil", key)
			}
			if !errors.Is(err, ErrConfigInvalid()) {
				t.Errorf("rejection of %q should wrap ErrConfigInvalid; got %v", key, err)
			}
		})
	}
}

// TestSigningPolicyEnvComposesMonotonic proves the environment layer folds into
// the monotonic compose: AGGREGA_SIGNING_REQUIRED=true turns the requirement on
// over a config false, and AGGREGA_SIGNING_REQUIRED=false cannot shadow a
// config-set requirement.
func TestSigningPolicyEnvComposesMonotonic(t *testing.T) {
	t.Parallel()

	// Env true forces the requirement on over a config false.
	rootA := t.TempDir()
	writeFile(t, filepath.Join(rootA, ".aggrega-config.yaml"), "workspace: {}\nsigning:\n  required: false\n")
	rwA, err := Resolve(rootA, "", fakeEnv(map[string]string{"AGGREGA_SIGNING_REQUIRED": "true"}))
	if err != nil {
		t.Fatalf("Resolve (env true): %v", err)
	}
	if !rwA.Config().Signing.Required {
		t.Errorf("env AGGREGA_SIGNING_REQUIRED=true must turn the requirement on over config false")
	}

	// Env false cannot shadow a config-set requirement.
	rootB := t.TempDir()
	writeFile(t, filepath.Join(rootB, ".aggrega-config.yaml"), "workspace: {}\nsigning:\n  required: true\n")
	rwB, err := Resolve(rootB, "", fakeEnv(map[string]string{"AGGREGA_SIGNING_REQUIRED": "false"}))
	if err != nil {
		t.Fatalf("Resolve (env false): %v", err)
	}
	if !rwB.Config().Signing.Required {
		t.Errorf("env AGGREGA_SIGNING_REQUIRED=false must not shadow a config-set required:true")
	}

	// Env kinds widen the union.
	rootC := t.TempDir()
	writeFile(t, filepath.Join(rootC, ".aggrega-config.yaml"), "workspace: {}\nsigning:\n  required: true\n  kinds:\n    - Component\n")
	rwC, err := Resolve(rootC, "", fakeEnv(map[string]string{"AGGREGA_SIGNING_KINDS": "Plugin"}))
	if err != nil {
		t.Fatalf("Resolve (env kinds): %v", err)
	}
	got := rwC.Config().Signing.Kinds
	if !reflect.DeepEqual(got, []string{"Component", "Plugin"}) {
		t.Errorf("env kinds union: got %v; want [Component Plugin]", got)
	}

	// An unknown env kind fails loudly, naming the variable.
	rootD := t.TempDir()
	writeFile(t, filepath.Join(rootD, ".aggrega-config.yaml"), "workspace: {}\n")
	_, err = Resolve(rootD, "", fakeEnv(map[string]string{"AGGREGA_SIGNING_KINDS": "Bogus"}))
	if err == nil {
		t.Fatal("expected an unknown AGGREGA_SIGNING_KINDS entry to be rejected; got nil")
	}
	if !errors.Is(err, ErrConfigInvalid()) {
		t.Errorf("env unknown-kind error should wrap ErrConfigInvalid; got %v", err)
	}
}
