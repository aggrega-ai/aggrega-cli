// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package publishops

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing/signingtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// A key path a workspace's own configuration names is a pointer to a secret the
// machine running this process holds, and a caller that merely reached that
// process proves nothing by causing it to be spent. A caller for whom that
// distinction matters withholds it, and then only a key path it supplied itself,
// or an identity it supplied itself, can sign.
//
// Every case below is written as a pair, and the row where the key IS spent is
// never decoration: it is what stops the withholding row passing against a
// resolution that reached no configuration at all.

// withheldTestRegistry is the host every case here signs against. It is not a
// loopback address, so nothing here reaches a registry even by accident: no case
// gets as far as a target.
const withheldTestRegistry = "reg.example.test"

// withheldTestReference is the artifact every case names, on that host.
const withheldTestReference = withheldTestRegistry + "/component/core/api-server:1.0.0"

// heldKeyPEM writes a real ECDSA P-256 private key and returns its path.
//
// A real key rather than a placeholder path, because the control rows assert that
// a KEYED SIGNER was built. A path to nothing would fail to load and the control
// would then pass on the wrong failure — it would prove only that something went
// wrong, which is what the withheld rows already say.
func heldKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate the held key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal the held key: %v", err)
	}
	path := filepath.Join(t.TempDir(), "held-key.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write the held key: %v", err)
	}
	return path
}

// signingKeyEnvVar is the variable that supplies signing.key_path from the
// environment. It is spelled here because the configuration package keeps its
// table of variables unexported; the route below asserts the spelling reaches the
// resolved configuration rather than assuming it, so a rename fails loudly here
// instead of leaving this route measuring nothing.
const signingKeyEnvVar = "AGGREGA_SIGNING_KEY_PATH"

// configurationRoute is one of the ways a workspace's configuration comes to name
// a signing key. Each writes the setting in its own form and hands back the
// resolved workspace that form produced.
type configurationRoute struct {
	name    string
	resolve func(t *testing.T, keyPath string) *workspaceconfig.ResolvedWorkspace
}

// resolveIn resolves a workspace over a directory holding the given configuration
// document, with an environment that answers only what env carries.
//
// The frozen environment is what makes every case here hermetic: the global
// configuration layer is located through this same lookup, so a lookup that
// answers nothing for it cannot reach a file the developer running these tests
// owns — which, for a case about signing keys, would be a key they hold.
func resolveIn(t *testing.T, configYAML string, env map[string]string) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(configYAML), 0o644); err != nil {
		t.Fatalf("write the workspace configuration: %v", err)
	}
	rw, err := workspaceconfig.Resolve(root, "", lookMap(env))
	if err != nil {
		t.Fatalf("resolve the workspace: %v", err)
	}
	return rw
}

// theThreeConfigurationRoutes is every way this workspace configuration can come
// to name a signing key. All three converge on one resolved value, which is the
// reason they are worth walking together: closing that value closes all three, and
// a case that walked only one would pass while the other two stayed open.
func theThreeConfigurationRoutes() []configurationRoute {
	return []configurationRoute{
		{
			name: "named globally",
			resolve: func(t *testing.T, keyPath string) *workspaceconfig.ResolvedWorkspace {
				t.Helper()
				return resolveIn(t, "workspace: {}\nsigning:\n  key_path: "+keyPath+"\n", nil)
			},
		},
		{
			name: "named for this registry",
			resolve: func(t *testing.T, keyPath string) *workspaceconfig.ResolvedWorkspace {
				t.Helper()
				return resolveIn(t, "workspace: {}\nregistries:\n  - host: "+withheldTestRegistry+
					"\n    signing:\n      key_path: "+keyPath+"\n", nil)
			},
		},
		{
			name: "supplied from the environment",
			resolve: func(t *testing.T, keyPath string) *workspaceconfig.ResolvedWorkspace {
				t.Helper()
				return resolveIn(t, "workspace: {}\n", map[string]string{signingKeyEnvVar: keyPath})
			},
		},
	}
}

// resolvedKeylessWorkspace is a workspace whose configuration names no signing key
// at all. It is what the withheld rows are held against: a caller that may not
// spend the key must be answered exactly as it would be in a workspace that
// configured none, rather than merely being answered with some error or other.
func resolvedKeylessWorkspace(t *testing.T) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	return resolveIn(t, "workspace: {}\n", nil)
}

// signerSite is one of the places this layer builds a signer from a workspace's
// own configuration. There are four, they belong to three different verbs, and
// each resolves the configuration for itself — so a case driving one says nothing
// about the other three.
type signerSite struct {
	// site names the function, so a failure says which one.
	site string
	// build runs that site with the credential posture given and returns whatever
	// it produced. idToken is the identity the caller supplied, empty for a caller
	// that supplied none.
	build func(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, idToken string, configuredKeyNotConsulted bool) (signing.Signer, error)
}

// theFourSignerSites is every place in this package that asks a workspace
// configuration for a signing key.
//
// buildRefSigner is driven without prepare having run, and that is deliberate:
// prepare stores the batch's once-resolved posture and buildRefSigner resolves the
// configuration AGAIN for each reference, so driving them together would let
// either one pass on the other's work. What buildRefSigner does with a
// configuration is the property here, and it has to be read on its own.
func theFourSignerSites() []signerSite {
	emptyEnv := lookMap(map[string]string{})
	return []signerSite{
		{
			site: "resolvePublishSigner",
			build: func(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, idToken string, withheld bool) (signing.Signer, error) {
				t.Helper()
				cfg := PushConfig{
					Workspace:                 rw,
					IDToken:                   idToken,
					EnvLookup:                 emptyEnv,
					ConfiguredKeyNotConsulted: withheld,
				}
				return resolvePublishSigner(cfg.signerRequest(withheldTestReference))
			},
		},
		{
			site: "batchSigner.prepare",
			build: func(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, idToken string, withheld bool) (signing.Signer, error) {
				t.Helper()
				b := &batchSigner{cfg: SignConfig{
					References:                []string{withheldTestReference},
					Workspace:                 rw,
					IDToken:                   idToken,
					EnvLookup:                 emptyEnv,
					ConfiguredKeyNotConsulted: withheld,
				}}
				if err := b.prepare(); err != nil {
					return nil, err
				}
				// Built from what prepare STORED, so this row reads prepare's own
				// decision rather than a second resolution made afterwards.
				return buildSigner(signerParams{KeyPath: b.keyPath, IDToken: b.idToken, TokenSource: b.tokenSource})
			},
		},
		{
			site: "batchSigner.buildRefSigner",
			build: func(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, idToken string, withheld bool) (signing.Signer, error) {
				t.Helper()
				b := &batchSigner{
					cfg: SignConfig{
						References:                []string{withheldTestReference},
						Workspace:                 rw,
						EnvLookup:                 emptyEnv,
						ConfiguredKeyNotConsulted: withheld,
					},
					// The batch's identity as prepare would have stored it, set here
					// so this site is read without prepare having run.
					idToken: idToken,
				}
				return b.buildRefSigner(withheldTestReference)
			},
		},
		{
			site: "deprecateSigner",
			build: func(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, idToken string, withheld bool) (signing.Signer, error) {
				t.Helper()
				return deprecateSigner(DeprecateConfig{
					Reference:                 withheldTestReference,
					Workspace:                 rw,
					IDToken:                   idToken,
					EnvLookup:                 emptyEnv,
					ConfiguredKeyNotConsulted: withheld,
				})
			},
		},
	}
}

// TestAWithheldConfiguredKeyIsNotReachedByAnySignerSite walks every place this
// layer builds a signer against every way a workspace can name a signing key.
//
// The three rows per cell are one property read three ways. With the key
// reachable, it signs — which is what keeps the other two from passing against a
// resolution that never saw the configuration. With it withheld and no identity
// supplied, the outcome is the one a workspace configuring no key at all
// produces, compared as a whole rather than as a substring so a differently-worded
// refusal cannot pass for the same answer. With it withheld and an identity
// supplied, a keyless signer is built — which is what keeps the middle row from
// passing against a layer that simply stopped signing.
func TestAWithheldConfiguredKeyIsNotReachedByAnySignerSite(t *testing.T) {
	keyPath := heldKeyPEM(t)
	suppliedIdentity := signingtest.SubjectfulIDToken(t)

	for _, site := range theFourSignerSites() {
		t.Run(site.site, func(t *testing.T) {
			// The answer a workspace naming no key produces, read once per site and
			// held as the thing every withheld row must be indistinguishable from.
			_, wantErr := site.build(t, resolvedKeylessWorkspace(t), "", false)
			if wantErr == nil {
				t.Fatalf("a workspace naming no key and a caller supplying no identity produced a signer, so "+
					"the comparison below would accept anything; site %s", site.site)
			}

			for _, route := range theThreeConfigurationRoutes() {
				t.Run(route.name, func(t *testing.T) {
					t.Run("the configured key signs for a caller that may spend it", func(t *testing.T) {
						signer, err := site.build(t, route.resolve(t, keyPath), "", false)
						if err != nil {
							t.Fatalf("the configured key must build a signer for a caller that may spend it: %v", err)
						}
						if _, keyed := signer.(*signing.KeyedSigner); !keyed {
							t.Fatalf("the configured key built a %T; a key this caller may spend selects the keyed "+
								"signer, and without this row the withheld row below would pass against a "+
								"resolution that never read the configuration at all", signer)
						}
					})

					t.Run("a caller that may not spend it is answered as if none were configured", func(t *testing.T) {
						signer, err := site.build(t, route.resolve(t, keyPath), "", true)
						if err == nil {
							t.Fatalf("a caller that supplied no credential of its own was given a %T; the key the "+
								"workspace names is not that caller's to spend", signer)
						}
						if strings.Contains(err.Error(), keyPath) {
							t.Errorf("the answer names the configured key %q, so it was read: %v", keyPath, err)
						}
						if err.Error() != wantErr.Error() {
							t.Errorf("a withheld key must be answered exactly as no key at all.\n got: %v\nwant: %v",
								err, wantErr)
						}
					})

					t.Run("and an identity the caller supplied signs keyless", func(t *testing.T) {
						signer, err := site.build(t, route.resolve(t, keyPath), suppliedIdentity, true)
						if err != nil {
							t.Fatalf("an identity the caller supplied must sign: %v", err)
						}
						if _, keyless := signer.(*signing.KeylessSigner); !keyless {
							t.Fatalf("an identity the caller supplied built a %T, want the keyless signer", signer)
						}
					})
				})
			}
		})
	}
}

// TestTheEnvironmentRouteReachesTheResolvedConfiguration is the needle under the
// environment row above.
//
// That row supplies the key through a variable name spelled in this file, because
// the configuration package keeps its own table of them unexported. A rename there
// would leave the row configuring nothing — and a row that configures nothing
// passes its withheld case for the wrong reason, silently, while reporting that
// the withholding works. This fails instead.
func TestTheEnvironmentRouteReachesTheResolvedConfiguration(t *testing.T) {
	rw := resolveIn(t, "workspace: {}\n", map[string]string{signingKeyEnvVar: "/key/from/the/environment.pem"})
	if got := rw.Config().ResolveSigning(withheldTestRegistry).KeyPath; got != "/key/from/the/environment.pem" {
		t.Fatalf("%s did not reach the resolved signing configuration (key_path = %q) — the environment row of "+
			"the walk above is configuring nothing and proving nothing", signingKeyEnvVar, got)
	}
}

// TestTheWithholdingLeavesEveryOtherSigningSettingAlone pins what resolveSigning
// does NOT withhold.
//
// The endpoint URLs name services rather than secrets, so a caller that cannot
// select a key still signs against the endpoints the deployment configured. A
// withholding that took them too would silently move every such caller onto the
// public-good instances, which is a different deployment from the one the operator
// configured and no refusal would say so.
func TestTheWithholdingLeavesEveryOtherSigningSettingAlone(t *testing.T) {
	rw := resolveIn(t, "workspace: {}\nsigning:\n  key_path: /held/key.pem\n"+
		"  fulcio_url: https://fulcio.private\n  rekor_url: https://rekor.private\n"+
		"  timestamp_url: https://tsa.private\n", nil)

	withheld := resolveSigning(rw.Config(), withheldTestReference, true)
	if withheld.KeyPath != "" {
		t.Errorf("KeyPath = %q, want it withheld", withheld.KeyPath)
	}
	for _, kept := range []struct{ field, got, want string }{
		{"FulcioURL", withheld.FulcioURL, "https://fulcio.private"},
		{"RekorURL", withheld.RekorURL, "https://rekor.private"},
		{"TimestampURL", withheld.TimestampURL, "https://tsa.private"},
	} {
		if kept.got != kept.want {
			t.Errorf("%s = %q, want %q kept — it names a service, not a secret", kept.field, kept.got, kept.want)
		}
	}

	reachable := resolveSigning(rw.Config(), withheldTestReference, false)
	if reachable.KeyPath != "/held/key.pem" {
		t.Errorf("KeyPath = %q for a caller that may spend it, want the configured path — without this the "+
			"assertion above would pass against a resolution that reads no key under any posture", reachable.KeyPath)
	}
}
