// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The publication fixture and the workspace it stands up.

// publishComponentBody is the entity every publication test publishes. Its
// metadata.version matches the per-version directory the fixture writes it into,
// which the transport requires.
const publishComponentBody = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/api-server
  version: 1.0.0
spec:
  schema:
    values:
      type: object
`

// publishTestRegistry is the destination every fixture names. It is not a
// loopback address, so nothing here can reach it even by accident: every test
// that publishes injects its own target, and every test that does not stops
// before a target is constructed.
const publishTestRegistry = "registry.example.test"

// publishFixture stands up a server over a real resolved workspace holding an
// entity document and an artifact file, and returns the server with the
// workspace root.
//
// The global configuration layer is a file of this test's own, reached through
// the environment override rather than through the machine's own configuration.
// That is not tidiness: a publication resolves its registry and its signing
// policy through the cascade, so a fixture that did not redirect the global layer
// would read the developer's real registries and could sign with a key they hold.
func publishFixture(t *testing.T, workspaceYAML string) (*Server, string) {
	t.Helper()

	root := t.TempDir()
	globalPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(globalPath, []byte("registries: []\n"), 0o644); err != nil {
		t.Fatalf("write the global layer: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(workspaceYAML), 0o644); err != nil {
		t.Fatalf("write the workspace layer: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "1.0.0"), 0o755); err != nil {
		t.Fatalf("make the per-version directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "1.0.0", "entity.yaml"), []byte(publishComponentBody), 0o644); err != nil {
		t.Fatalf("write the entity: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "bundle.txt"), []byte("documentation bundle bytes\n"), 0o644); err != nil {
		t.Fatalf("write the artifact file: %v", err)
	}

	rw, err := workspaceconfig.Resolve(root, root, func(k string) string {
		if k == workspaceconfig.EnvConfigOverride {
			return globalPath
		}
		return ""
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	srv := newTestServer(t, root, nil, nil)
	srv.resolvedWorkspace = rw
	return srv, root
}

// defaultRegistryWorkspace is the ordinary configuration: one destination, no
// signature requirement, no held key.
const defaultRegistryWorkspace = "workspace: {}\ndefault_registry: " + publishTestRegistry + "\n"

// signingRequiredWorkspace requires a signature for the destination registry.
const signingRequiredWorkspace = defaultRegistryWorkspace +
	"registries:\n  - host: " + publishTestRegistry + "\n    signing:\n      required: true\n"

// keyedSigningWorkspace requires a signature and points the signing block at a
// held key, which is how keyed signing reaches this surface: a request names no
// key path, so the configuration is the only place one can come from.
func keyedSigningWorkspace(t *testing.T) (workspaceYAML string, pubPEM []byte) {
	t.Helper()
	privPEM, pubPEM, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	keyPath := filepath.Join(t.TempDir(), "signing-key.pem")
	if err := os.WriteFile(keyPath, privPEM, 0o600); err != nil {
		t.Fatalf("write the signing key: %v", err)
	}
	return signingRequiredWorkspace + "signing:\n  key_path: " + keyPath + "\n", pubPEM
}

// TestPublishEntityOnTheDaemonAnswersWithTheFactsOfADryRun drives the wire
// end-to-end and asserts the answer is the facts rather than an acknowledgement.
//
// A dry run is the whole of what can be driven through the wire without a
// destination: every other path constructs a remote repository from the reference,
// and this surface's request shape carries no way to point that elsewhere. What it
// proves is the part only the wire can prove — that the method is routed, that the
// request shape decodes, and that the response carries the operation's own values.
//
// The reach is the load-bearing assertion. A dry run reached nothing, and an
// answer that said it had would be worse than one that said nothing at all.
func TestPublishEntityOnTheDaemonAnswersWithTheFactsOfADryRun(t *testing.T) {
	t.Parallel()
	srv, _ := publishFixture(t, defaultRegistryWorkspace)

	resp := callDaemonMethod(t, srv, "aggrega/publishEntity", map[string]any{
		"entity": "1.0.0/entity.yaml",
		"dryRun": true,
	})
	var res publishEntityResult
	decodeDaemonResult(t, resp, &res)

	if want := publishTestRegistry + "/component/core/api-server"; res.Reference != want {
		t.Errorf("reference: got %q, want %q", res.Reference, want)
	}
	if res.Tag != "1.0.0" {
		t.Errorf("tag: got %q, want 1.0.0", res.Tag)
	}
	if res.Kind != "Component" {
		t.Errorf("kind: got %q, want Component", res.Kind)
	}
	if res.MediaType == "" || res.Size == 0 {
		t.Errorf("a dry run must report what it computed; got mediaType=%q size=%d", res.MediaType, res.Size)
	}
	if !res.DryRun {
		t.Error("a dry run must say so")
	}
	if res.Digest != "" {
		t.Errorf("a dry run computes no digest; got %q", res.Digest)
	}
	if res.Reach.BeyondThisMachine {
		t.Errorf("a dry run reached nothing; the answer says otherwise: %+v", res.Reach)
	}
	if res.Reach.Registry != publishTestRegistry {
		t.Errorf("reach.registry: got %q, want %q", res.Reach.Registry, publishTestRegistry)
	}
}

// TestPublishArtifactOnTheDaemonAnswersWithTheFactsOfADryRun is the same proof for
// the data artifact, whose bytes are opaque and whose media type falls through to
// a default when the request names none.
func TestPublishArtifactOnTheDaemonAnswersWithTheFactsOfADryRun(t *testing.T) {
	t.Parallel()
	srv, _ := publishFixture(t, defaultRegistryWorkspace)

	resp := callDaemonMethod(t, srv, "aggrega/publishArtifact", map[string]any{
		"file":   "bundle.txt",
		"name":   "handbook",
		"tag":    "2.0.0",
		"dryRun": true,
	})
	var res publishArtifactResult
	decodeDaemonResult(t, resp, &res)

	if want := publishTestRegistry + "/docs/handbook"; res.Reference != want {
		t.Errorf("reference: got %q, want %q", res.Reference, want)
	}
	if res.Tag != "2.0.0" {
		t.Errorf("tag: got %q, want 2.0.0", res.Tag)
	}
	if res.MediaType != oci.MediaTypeDocs {
		t.Errorf("an absent media type must take the default; got %q, want %q", res.MediaType, oci.MediaTypeDocs)
	}
	if res.Size == 0 {
		t.Error("the answer must report the byte count it computed")
	}
	if !res.DryRun || res.Reach.BeyondThisMachine {
		t.Errorf("a dry run reached nothing; got dryRun=%v reach=%+v", res.DryRun, res.Reach)
	}
}

// TestPublicationPathsAreConfinedToTheWorkspace is the addressing proof: every
// path a publication request may name is relative to the workspace root, and a
// value that is absolute or that climbs out of it is REFUSED rather than resolved.
//
// The refusal is the point rather than a repair. Normalising such a value here
// would hide the same mistake in every client that makes it, and resolving it
// would let a request point the session at a file the workspace does not hold —
// which is the addressing this surface declines for every other operation.
func TestPublicationPathsAreConfinedToTheWorkspace(t *testing.T) {
	t.Parallel()
	srv, root := publishFixture(t, defaultRegistryWorkspace)
	absolute := filepath.Join(root, "1.0.0", "entity.yaml")

	for _, tc := range []struct {
		name   string
		method string
		params map[string]any
		want   string
	}{
		{
			name:   "an absolute entity path",
			method: "aggrega/publishEntity",
			params: map[string]any{"entity": absolute},
			want:   "is an absolute path",
		},
		{
			name:   "an entity path that climbs out",
			method: "aggrega/publishEntity",
			params: map[string]any{"entity": "../elsewhere/entity.yaml"},
			want:   "resolves outside the workspace root",
		},
		{
			name:   "an executable path that climbs out",
			method: "aggrega/publishEntity",
			params: map[string]any{"entity": "1.0.0/entity.yaml", "plugin": "../elsewhere/guest.wasm"},
			want:   "resolves outside the workspace root",
		},
		{
			name:   "an absolute artifact path",
			method: "aggrega/publishArtifact",
			params: map[string]any{"file": absolute, "name": "handbook", "tag": "2.0.0"},
			want:   "is an absolute path",
		},
		{
			name:   "a batch file that climbs out",
			method: "aggrega/deprecateArtifact",
			params: map[string]any{"from": "../elsewhere/batch.yaml"},
			want:   "resolves outside the workspace root",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := callDaemonMethod(t, srv, tc.method, tc.params)
			if resp.Error == nil {
				t.Fatalf("expected a refusal; got result %s", resp.Result)
			}
			if !strings.Contains(resp.Error.Message, tc.want) {
				t.Errorf("the refusal must name the fault: got %q, want it to contain %q", resp.Error.Message, tc.want)
			}
			// Every one of these refusals states the contract as well as the
			// fault, so a client author learns what to send from one message.
			if !strings.Contains(resp.Error.Message, "relative to the workspace root") {
				t.Errorf("the refusal must state the contract: got %q", resp.Error.Message)
			}
		})
	}
}

// TestPublicationMethodsRefuseAMissingSubject pins what each method requires, and
// pins it in THIS surface's vocabulary.
//
// The shared layer refuses several of these too, and it refuses them naming a
// command line flag — correct where a person typed one, and a puzzle here. So each
// message is asserted to name the request field rather than the flag, which is
// what makes catching these at this boundary worth doing at all.
func TestPublicationMethodsRefuseAMissingSubject(t *testing.T) {
	t.Parallel()
	srv, _ := publishFixture(t, defaultRegistryWorkspace)

	for _, tc := range []struct {
		name   string
		method string
		params map[string]any
		want   string
	}{
		{"publish with no entity", "aggrega/publishEntity", map[string]any{}, "the request named no entity"},
		{"publish an artifact with no name", "aggrega/publishArtifact",
			map[string]any{"file": "bundle.txt", "tag": "2.0.0"}, "name is required"},
		{"publish an artifact with no tag", "aggrega/publishArtifact",
			map[string]any{"file": "bundle.txt", "name": "handbook"}, "tag is required"},
		{"sign nothing", "aggrega/signArtifacts", map[string]any{}, "references is required"},
		{"sign an empty entry", "aggrega/signArtifacts",
			map[string]any{"references": []string{""}}, "carries an empty entry"},
		{"deprecate nothing", "aggrega/deprecateArtifact", map[string]any{}, "reference is required"},
		{"deprecate with no content", "aggrega/deprecateArtifact",
			map[string]any{"reference": publishTestRegistry + "/component/core/api-server:1.0.0"},
			"severity and reason are required"},
		{"deprecate both ways at once", "aggrega/deprecateArtifact",
			map[string]any{"reference": "x:1", "from": "batch.yaml"}, "are alternatives"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := callDaemonMethod(t, srv, tc.method, tc.params)
			if resp.Error == nil {
				t.Fatalf("expected a refusal; got result %s", resp.Result)
			}
			if !strings.Contains(resp.Error.Message, tc.want) {
				t.Errorf("the refusal must name what to send: got %q, want it to contain %q", resp.Error.Message, tc.want)
			}
			if strings.Contains(resp.Error.Message, "--") {
				t.Errorf("a refusal on this surface must not name a command line flag: %q", resp.Error.Message)
			}
		})
	}
}

// TestPublicationRefusesWithNoDestinationRegistry pins the one refusal the shared
// layer would otherwise answer by naming a flag nobody here can pass.
//
// The fixture spells `default_registry` as an explicitly empty value rather than
// omitting the key, and that is load-bearing rather than verbose: an ABSENT key
// takes the built-in default, so a workspace that merely says nothing about a
// registry still has one and this refusal cannot arise. Simplifying the fixture to
// an absent key leaves a test that passes while asserting nothing.
func TestPublicationRefusesWithNoDestinationRegistry(t *testing.T) {
	t.Parallel()
	srv, _ := publishFixture(t, "workspace: {}\ndefault_registry: \"\"\n")

	for _, tc := range []struct {
		method string
		params map[string]any
	}{
		{"aggrega/publishEntity", map[string]any{"entity": "1.0.0/entity.yaml", "dryRun": true}},
		{"aggrega/publishArtifact", map[string]any{"file": "bundle.txt", "name": "handbook", "tag": "2.0.0", "dryRun": true}},
	} {
		t.Run(tc.method, func(t *testing.T) {
			resp := callDaemonMethod(t, srv, tc.method, tc.params)
			if resp.Error == nil {
				t.Fatalf("expected a refusal; got result %s", resp.Result)
			}
			if !strings.Contains(resp.Error.Message, "no destination registry") {
				t.Errorf("the refusal must name what is missing: %q", resp.Error.Message)
			}
			if strings.Contains(resp.Error.Message, "--registry") {
				t.Errorf("a refusal on this surface must not name a command line flag: %q", resp.Error.Message)
			}
		})
	}
}

// TestPublicationRefusesLoudlyWithNoCredentialAndDoesNotHang is the credential
// proof, driven rather than asserted.
//
// Nothing supplies an identity: no request field carries one, the configuration
// names no held key, and the environment is emptied of every source the ladder
// reads. On a surface with a terminal this is where a browser would have opened;
// here there is nobody to ask, so the only two possible behaviours are a loud
// refusal and a HANG — and the hang is the worse failure, because nothing is
// watching it block.
//
// So each call is bounded by a LIVENESS CEILING rather than a performance budget:
// it separates "returned" from "did not return", which no amount of machine
// contention can blur. And the refusal must name something a caller on THIS
// surface can act on, which is the exported variable rather than the flag.
//
// The data-artifact publication is deliberately absent: it resolves no identity at
// all — a published artifact is signed afterwards, by reference — so there is no
// credential for it to refuse over.
func TestPublicationRefusesLoudlyWithNoCredentialAndDoesNotHang(t *testing.T) {
	// Not parallel: it empties the process environment of every identity source.
	t.Setenv(signing.EnvIdentityToken, "")
	t.Setenv(signing.EnvSigstoreIDToken, "")
	if signing.GitHubActionsAmbientAvailable(os.Getenv) {
		t.Skip("the ambient OIDC endpoint is present in this environment, so an absent credential cannot be arranged here")
	}

	srv, _ := publishFixture(t, signingRequiredWorkspace)
	reference := publishTestRegistry + "/component/core/api-server:1.0.0"

	// The signing batch and the deprecation marking resolve their identity before
	// they touch a registry at all, so the wire carries the refusal with no
	// destination in play.
	for _, tc := range []struct {
		name   string
		method string
		params map[string]any
	}{
		{
			name:   "a signing batch",
			method: "aggrega/signArtifacts",
			params: map[string]any{"references": []string{reference}},
		},
		{
			name:   "a deprecation marking",
			method: "aggrega/deprecateArtifact",
			params: map[string]any{"reference": reference, "severity": "warning", "reason": "superseded"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			done := make(chan jsonRPCResponse, 1)
			go func() { done <- callDaemonMethod(t, srv, tc.method, tc.params) }()

			select {
			case resp := <-done:
				if resp.Error == nil {
					t.Fatalf("an operation with no credential must be refused, not answered; got result %s", resp.Result)
				}
				assertNamesTheMissingCredential(t, resp.Error.Message)
			case <-time.After(30 * time.Second):
				t.Fatal("the request did not return: a surface with nobody watching must refuse rather than block")
			}
		})
	}

	// A publication resolves its identity LATER than those two: the immutability
	// guard reads the destination first, so the credential refusal sits behind a
	// reachable registry. Measured rather than assumed — with no destination this
	// case refuses on the network before it ever reaches the ladder, which is a
	// different refusal and would have made the assertion below pass for the wrong
	// reason. So the destination is present and the credential is what is missing.
	t.Run("a publication that must sign", func(t *testing.T) {
		cfg, paramErr := srv.publishEntityConfig(publishEntityParams{Entity: "1.0.0/entity.yaml"})
		if paramErr != nil {
			t.Fatalf("publishEntityConfig: %s", paramErr.Message)
		}
		store := memory.New()
		cfg.PushTarget = store

		type outcome struct {
			res publishEntityResult
			err *jsonRPCError
		}
		done := make(chan outcome, 1)
		go func() {
			res, err := runPublishEntity(cfg)
			done <- outcome{res, err}
		}()

		select {
		case got := <-done:
			if got.err == nil {
				t.Fatalf("a publication that must sign with no credential must be refused; got %+v", got.res)
			}
			assertNamesTheMissingCredential(t, got.err.Message)
			if _, exists, err := oci.Resolve(context.Background(), store, "1.0.0"); err != nil {
				t.Fatalf("Resolve: %v", err)
			} else if exists {
				t.Error("the refusal published bytes: signing must refuse before anything lands")
			}
		case <-time.After(30 * time.Second):
			t.Fatal("the publication did not return: a surface with nobody watching must refuse rather than block")
		}
	})
}

// assertNamesTheMissingCredential holds a credential refusal to naming something
// the caller can act on.
//
// A bare "failed" would leave a caller with nothing to do, which on a surface with
// nobody watching is the same as silence. The environment variable is asserted
// rather than the command line flag the same message also names, because the
// variable is the half a caller here can actually set.
func assertNamesTheMissingCredential(t *testing.T, message string) {
	t.Helper()
	for _, want := range []string{"identity token", signing.EnvIdentityToken} {
		if !strings.Contains(message, want) {
			t.Errorf("the refusal must name %q; got %q", want, message)
		}
	}
	t.Logf("refusal: %s", message)
}

// TestPublishEntityOnTheDaemonRefusesUnsignedWhereASignatureIsRequired is the
// first half of the relocation guard: a publication that would land unsigned on a
// registry that requires a signature is refused, and NOTHING lands.
//
// It is asserted on two channels because neither covers the other. Through the
// wire, which is what a caller sees: the refusal reaches the response. Through the
// operation's own contract with a destination injected: no bytes reached it. A
// refusal that had already published would pass the first and fail the second,
// which is precisely the failure a positive test cannot see.
func TestPublishEntityOnTheDaemonRefusesUnsignedWhereASignatureIsRequired(t *testing.T) {
	t.Parallel()
	srv, _ := publishFixture(t, signingRequiredWorkspace)
	unsigned := false

	resp := callDaemonMethod(t, srv, "aggrega/publishEntity", map[string]any{
		"entity": "1.0.0/entity.yaml",
		"sign":   unsigned,
	})
	if resp.Error == nil {
		t.Fatalf("an unsigned publication to a signature-requiring registry must be refused; got result %s", resp.Result)
	}
	if !strings.Contains(resp.Error.Message, "requires signatures") {
		t.Errorf("the refusal must name why: %q", resp.Error.Message)
	}

	// The same request against a destination this test holds, so what the refusal
	// did to the registry is readable rather than assumed.
	cfg, paramErr := srv.publishEntityConfig(publishEntityParams{Entity: "1.0.0/entity.yaml", Sign: &unsigned})
	if paramErr != nil {
		t.Fatalf("publishEntityConfig: %s", paramErr.Message)
	}
	store := memory.New()
	cfg.PushTarget = store
	_, runErr := runPublishEntity(cfg)
	if runErr == nil {
		t.Fatal("the operation answered an unsigned publication that must be refused")
	}
	if !errors.Is(runErr.cause, signing.ErrUnsignedPushRefused()) {
		t.Errorf("the refusal must carry the unsigned-publication condition; got %v", runErr.cause)
	}
	if _, exists, err := oci.Resolve(context.Background(), store, "1.0.0"); err != nil {
		t.Fatalf("Resolve: %v", err)
	} else if exists {
		t.Error("the guardrail let bytes through: the tag exists after a refused publication")
	}
}

// TestSignatureTheDaemonAttachedDoesNotVerifyAgainstAnotherKey is the second half
// of the relocation guard, and the one a positive test cannot see at all.
//
// A signature produced through this surface is read back off the destination and
// put to a verifier three ways: against the key that made it, which must PASS —
// the control, without which a verifier that refused everything would satisfy the
// two negatives; against a different key, which must REFUSE; and against a
// different artifact digest, which must also REFUSE, because a signature that
// verified against bytes it never committed to would be no signature at all.
//
// Keyed signing is what reaches this surface: a request names no key path, so the
// key comes from the workspace's own signing block, and driving it that way proves
// the configured route works rather than bypassing it.
func TestSignatureTheDaemonAttachedDoesNotVerifyAgainstAnotherKey(t *testing.T) {
	t.Parallel()
	workspaceYAML, pubPEM := keyedSigningWorkspace(t)
	srv, _ := publishFixture(t, workspaceYAML)

	cfg, paramErr := srv.publishEntityConfig(publishEntityParams{Entity: "1.0.0/entity.yaml"})
	if paramErr != nil {
		t.Fatalf("publishEntityConfig: %s", paramErr.Message)
	}
	store := memory.New()
	cfg.PushTarget = store
	res, runErr := runPublishEntity(cfg)
	if runErr != nil {
		t.Fatalf("publish: %s", runErr.Message)
	}
	if !res.Signed {
		t.Fatal("a publication to a signature-requiring registry must report that it signed")
	}
	if !res.Reach.BeyondThisMachine {
		t.Errorf("a publication that landed must say it reached beyond this machine: %+v", res.Reach)
	}

	bundle := onlySignatureBundle(t, store, "1.0.0")

	if err := signing.VerifyKeyedBundle(bundle, pubPEM, res.Digest); err != nil {
		t.Fatalf("the control failed: the signature does not verify against the key that made it: %v", err)
	}

	_, otherPub, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	if err := signing.VerifyKeyedBundle(bundle, otherPub, res.Digest); err == nil {
		t.Error("a signature verified against a key that did not make it")
	}

	const foreignDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	if err := signing.VerifyKeyedBundle(bundle, pubPEM, foreignDigest); err == nil {
		t.Error("a signature verified against an artifact it never committed to")
	}
}

// TestPublishEntityOnTheDaemonLandsBytesAndReportsTheirDigest proves the answer's
// digest is the destination's, not a value the operation computed and then failed
// to use, and that a second identical publication is reported as the no-op it is.
func TestPublishEntityOnTheDaemonLandsBytesAndReportsTheirDigest(t *testing.T) {
	t.Parallel()
	srv, _ := publishFixture(t, defaultRegistryWorkspace)
	store := memory.New()

	res := publishThroughDaemon(t, srv, store)
	if res.DryRun || res.AlreadyPublished || res.Signed {
		t.Errorf("an ordinary publication reports none of those; got %+v", res)
	}
	landed, exists, err := oci.Resolve(context.Background(), store, "1.0.0")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !exists {
		t.Fatal("the answer reported a publication that did not land")
	}
	if landed != res.Digest {
		t.Errorf("the reported digest is not the one the destination holds: got %q, destination %q", res.Digest, landed)
	}

	again := publishThroughDaemon(t, srv, store)
	if !again.AlreadyPublished {
		t.Errorf("a byte-identical re-publication is an idempotent no-op and must say so; got %+v", again)
	}
	if again.Digest != landed {
		t.Errorf("the no-op must report the digest already there: got %q, want %q", again.Digest, landed)
	}
}

// TestSignArtifactsOnTheDaemonSignsWhatIsPublishedAndRefusesWhatIsNot covers both
// directions of the signing method against a destination this test holds.
//
// The refusal half is the load-bearing one: signing a reference nothing published
// must fail rather than attach a signature to nothing, and the batch must report
// that failure rather than answer with an empty success.
func TestSignArtifactsOnTheDaemonSignsWhatIsPublishedAndRefusesWhatIsNot(t *testing.T) {
	t.Parallel()
	workspaceYAML, pubPEM := keyedSigningWorkspace(t)
	srv, _ := publishFixture(t, workspaceYAML)
	store := memory.New()

	// Publish WITHOUT a signature so the signing method is what attaches one.
	unsigned := false
	cfg, paramErr := srv.publishEntityConfig(publishEntityParams{Entity: "1.0.0/entity.yaml", Sign: &unsigned})
	if paramErr != nil {
		t.Fatalf("publishEntityConfig: %s", paramErr.Message)
	}
	cfg.PushTarget = store
	// The destination requires signatures, so an explicitly unsigned publication is
	// refused there — this fixture publishes to a second host that does not.
	cfg.Registry = "plain.example.test"
	published, runErr := runPublishEntity(cfg)
	if runErr != nil {
		t.Fatalf("publish: %s", runErr.Message)
	}

	reference := published.Reference + ":1.0.0"
	signCfg, signParamErr := srv.signArtifactsConfig(signArtifactsParams{References: []string{reference}})
	if signParamErr != nil {
		t.Fatalf("signArtifactsConfig: %s", signParamErr.Message)
	}
	signCfg.Target = store
	signed, signErr := runSignArtifacts(signCfg)
	if signErr != nil {
		t.Fatalf("sign: %s", signErr.Message)
	}
	if len(signed.Signed) != 1 || signed.Signed[0].Reference != reference {
		t.Fatalf("the answer must name every reference it signed; got %+v", signed.Signed)
	}
	if signed.Signed[0].Digest != published.Digest {
		t.Errorf("the signature commits to the published digest: got %q, want %q",
			signed.Signed[0].Digest, published.Digest)
	}
	if err := signing.VerifyKeyedBundle(onlySignatureBundle(t, store, "1.0.0"), pubPEM, published.Digest); err != nil {
		t.Errorf("the attached signature does not verify against the configured key: %v", err)
	}

	absent := publishTestRegistry + "/component/core/absent:9.9.9"
	missCfg, missParamErr := srv.signArtifactsConfig(signArtifactsParams{References: []string{absent}})
	if missParamErr != nil {
		t.Fatalf("signArtifactsConfig: %s", missParamErr.Message)
	}
	missCfg.Target = store
	if _, err := runSignArtifacts(missCfg); err == nil {
		t.Fatal("signing a reference nothing published must be refused")
	} else if !strings.Contains(err.Message, "nothing to sign") {
		t.Errorf("the refusal must say what was wrong: %q", err.Message)
	}
}

// TestDeprecateArtifactOnTheDaemonAttachesAMarkerAndRefusesAnAbsentSubject covers
// both directions of the deprecation method.
//
// A withdrawal never publishes what it withdraws, so a marker against a reference
// nothing published must be refused rather than attached — otherwise a session
// could record a withdrawal for an artifact that does not exist, which every
// consumer would then read.
func TestDeprecateArtifactOnTheDaemonAttachesAMarkerAndRefusesAnAbsentSubject(t *testing.T) {
	t.Parallel()
	workspaceYAML, _ := keyedSigningWorkspace(t)
	srv, _ := publishFixture(t, workspaceYAML)
	store := memory.New()

	published := publishThroughDaemon(t, srv, store)
	reference := published.Reference + ":1.0.0"

	cfg, paramErr := srv.deprecateArtifactConfig(deprecateArtifactParams{
		Reference: reference,
		Severity:  "warning",
		Reason:    "superseded by 2.0.0",
	})
	if paramErr != nil {
		t.Fatalf("deprecateArtifactConfig: %s", paramErr.Message)
	}
	cfg.Target = store
	res, runErr := runDeprecateArtifact(cfg)
	if runErr != nil {
		t.Fatalf("deprecate: %s", runErr.Message)
	}
	if len(res.Marked) != 1 {
		t.Fatalf("the answer must name every marker it attached; got %+v", res.Marked)
	}
	marked := res.Marked[0]
	if marked.Reference != reference || marked.Severity != "warning" || marked.IssuedOn == "" || marked.Digest == "" {
		t.Errorf("the answer must carry the marker's own facts; got %+v", marked)
	}
	if !res.Reach.BeyondThisMachine {
		t.Errorf("an attached marker reached beyond this machine; the answer says otherwise: %+v", res.Reach)
	}

	absent := publishTestRegistry + "/component/core/absent:9.9.9"
	missCfg, missParamErr := srv.deprecateArtifactConfig(deprecateArtifactParams{
		Reference: absent, Severity: "warning", Reason: "never published",
	})
	if missParamErr != nil {
		t.Fatalf("deprecateArtifactConfig: %s", missParamErr.Message)
	}
	missCfg.Target = store
	if _, err := runDeprecateArtifact(missCfg); err == nil {
		t.Fatal("a marker against a reference nothing published must be refused")
	} else if !strings.Contains(err.Message, "nothing to deprecate") {
		t.Errorf("the refusal must say what was wrong: %q", err.Message)
	}
}

// publishThroughDaemon publishes the fixture entity into store through the
// daemon's own request projection, so a test needing something already published
// gets there by the path under test rather than by a hand-built push.
func publishThroughDaemon(t *testing.T, srv *Server, store oras.Target) publishEntityResult {
	t.Helper()
	cfg, paramErr := srv.publishEntityConfig(publishEntityParams{Entity: "1.0.0/entity.yaml"})
	if paramErr != nil {
		t.Fatalf("publishEntityConfig: %s", paramErr.Message)
	}
	cfg.PushTarget = store
	res, runErr := runPublishEntity(cfg)
	if runErr != nil {
		t.Fatalf("publish: %s", runErr.Message)
	}
	return res
}

// onlySignatureBundle reads back the one signature referrer attached to tag,
// failing when there is not exactly one. The count is asserted rather than
// indexed past: a test that took the first of several would keep passing while
// something attached a second signature nobody asked for.
func onlySignatureBundle(t *testing.T, store oras.Target, tag string) []byte {
	t.Helper()
	ctx := context.Background()
	refs, err := oci.ListReferrers(ctx, store, tag, signing.BundleMediaType)
	if err != nil {
		t.Fatalf("ListReferrers: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("got %d signature referrers, want exactly 1", len(refs))
	}
	bundle, err := oci.FetchReferrerPayload(ctx, store, refs[0])
	if err != nil {
		t.Fatalf("FetchReferrerPayload: %v", err)
	}
	return bundle
}
