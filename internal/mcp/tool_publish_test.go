// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"errors"
	"io"
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

// publishFixture stands up a server over a real workspace holding an entity
// document and an artifact file, and returns the server with the workspace root.
//
// The global configuration layer is a file of this test's own, reached through the
// environment override. That is not tidiness: a publication resolves its
// destination and its signing policy through the cascade, and this surface resolves
// its workspace from the process environment, so a fixture that did not redirect
// the global layer would read the developer's real registries and could sign with a
// key they hold.
//
// Setting an environment variable is what makes every test here non-parallel. It is
// the price of driving the production resolution path rather than a seam around it.
func publishFixture(t *testing.T, workspaceYAML string) (*server, string) {
	t.Helper()

	root := t.TempDir()
	globalPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(globalPath, []byte("registries: []\n"), 0o644); err != nil {
		t.Fatalf("write the global layer: %v", err)
	}
	t.Setenv(workspaceconfig.EnvConfigOverride, globalPath)

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

	return &server{out: io.Discard, logw: io.Discard, serverVersion: "test", startDir: root}, root
}

// defaultRegistryWorkspace is the ordinary configuration: one destination, no
// signature requirement, no held key.
const defaultRegistryWorkspace = "workspace: {}\ndefault_registry: " + publishTestRegistry + "\n"

// signingRequiredWorkspace requires a signature for the destination registry.
const signingRequiredWorkspace = defaultRegistryWorkspace +
	"registries:\n  - host: " + publishTestRegistry + "\n    signing:\n      required: true\n"

// keyedSigningWorkspace requires a signature and points the workspace's signing
// block at a held key. The key path is returned as well, because this surface also
// accepts one as an argument and both routes are exercised.
func keyedSigningWorkspace(t *testing.T) (workspaceYAML, keyPath string, pubPEM []byte) {
	t.Helper()
	privPEM, pubPEM, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	keyPath = filepath.Join(t.TempDir(), "signing-key.pem")
	if err := os.WriteFile(keyPath, privPEM, 0o600); err != nil {
		t.Fatalf("write the signing key: %v", err)
	}
	return signingRequiredWorkspace + "signing:\n  key_path: " + keyPath + "\n", keyPath, pubPEM
}

// TestThePublicationToolsAnswerWithFactsRatherThanText drives all four tools
// through the real tools/call dispatch and asserts the answer is the operation's
// own values.
//
// It is the property this stage exists for. Every one of these capabilities was
// already reachable here as the tool generated from its command, which answers with
// the text that command printed; a caller wanting the digest had to parse a status
// line. What is asserted is that the in-process form carries the values.
//
// A dry run is what can be driven through the wire without a destination — every
// other path constructs a remote repository from the reference — so the reach is the
// load-bearing assertion: a dry run reached nothing, and an answer that said it had
// would be worse than one that said nothing at all.
func TestThePublicationToolsAnswerWithFactsRatherThanText(t *testing.T) {
	s, root := publishFixture(t, defaultRegistryWorkspace)

	t.Run("a catalog entity", func(t *testing.T) {
		res := callTool(t, s, publishEntityToolName, publishEntityArgs{
			Entity:        "1.0.0/entity.yaml",
			DryRun:        true,
			WorkspaceRoot: root,
		})
		var got publishEntityPayload
		decodeStructured(t, res, &got)

		if want := publishTestRegistry + "/component/core/api-server"; got.Reference != want {
			t.Errorf("reference: got %q, want %q", got.Reference, want)
		}
		if got.Tag != "1.0.0" {
			t.Errorf("tag: got %q, want 1.0.0", got.Tag)
		}
		if got.Kind != "Component" {
			t.Errorf("kind: got %q, want Component", got.Kind)
		}
		if got.MediaType == "" || got.Size == 0 {
			t.Errorf("a dry run must report what it computed; got mediaType=%q size=%d", got.MediaType, got.Size)
		}
		if !got.DryRun {
			t.Error("a dry run must say so")
		}
		if got.Digest != "" {
			t.Errorf("a dry run computes no digest; got %q", got.Digest)
		}
		if got.Reach.BeyondThisMachine {
			t.Errorf("a dry run reached nothing; the answer says otherwise: %+v", got.Reach)
		}
		if got.Reach.Registry != publishTestRegistry {
			t.Errorf("reach.registry: got %q, want %q", got.Reach.Registry, publishTestRegistry)
		}
	})

	t.Run("a data artifact", func(t *testing.T) {
		res := callTool(t, s, publishArtifactToolName, publishArtifactArgs{
			File:          "bundle.txt",
			Name:          "handbook",
			Tag:           "2.0.0",
			DryRun:        true,
			WorkspaceRoot: root,
		})
		var got publishArtifactPayload
		decodeStructured(t, res, &got)

		if want := publishTestRegistry + "/docs/handbook"; got.Reference != want {
			t.Errorf("reference: got %q, want %q", got.Reference, want)
		}
		if got.Tag != "2.0.0" {
			t.Errorf("tag: got %q, want 2.0.0", got.Tag)
		}
		if got.MediaType != oci.MediaTypeDocs {
			t.Errorf("an absent media type must take the default; got %q, want %q", got.MediaType, oci.MediaTypeDocs)
		}
		if got.Size == 0 {
			t.Error("the answer must report the byte count it computed")
		}
		if !got.DryRun || got.Reach.BeyondThisMachine {
			t.Errorf("a dry run reached nothing; got dry_run=%v reach=%+v", got.DryRun, got.Reach)
		}
	})

	// The signing and deprecation tools have no dry run: their whole product is an
	// attachment, so there is no shape of call that reaches the operation and
	// contacts nothing. The deprecation is drivable anyway, with legal arguments and
	// a MALFORMED credential: its signer is constructed before anything is attached,
	// and the shared layer checks a supplied token's shape locally, so the refusal
	// proves the call travelled dispatch → arguments → operation without a byte
	// leaving the machine.
	//
	// 🔴 The signing batch is deliberately NOT here, and the reason is a measured
	// fact rather than an omission. A supplied token is validated at SIGNER
	// CONSTRUCTION, which the shared layer documents; the batch resolves the
	// published digest of each reference first — that read is also how it learns
	// there is nothing to sign — so a malformed token reaches its check only after a
	// registry has been contacted. Driving it that way would make this test's
	// duration depend on how the machine's resolver answers a name, which is a test
	// that reports the resolver. The batch's routed proof is
	// TestPublicationToolsRefuseLoudlyWithNoCredentialAndDoNotHang, where an ABSENT
	// credential refuses in the batch's own preparation, before the loop and before
	// any registry is contacted.
	t.Run("a deprecation marking reaches the operation", func(t *testing.T) {
		res := callTool(t, s, deprecateArtifactToolName, deprecateArtifactArgs{
			Reference:     publishTestRegistry + "/component/core/api-server:1.0.0",
			Severity:      "warning",
			Reason:        "superseded",
			IdentityToken: "not-a-jwt",
			WorkspaceRoot: root,
		})
		if !res.IsError {
			t.Fatalf("a call carrying a malformed credential must fail; got %s", res.Content[0].Text)
		}
		// The operation's own diagnostic, not an argument-shape refusal: that is what
		// makes this evidence the tool is routed rather than merely resolved by name.
		if !strings.Contains(res.Content[0].Text, "not a well-formed JWT") {
			t.Errorf("the failure is not the operation's own credential check, so this proves nothing "+
				"about routing: %q", res.Content[0].Text)
		}
	})
}

// TestPublicationToolsRefuseAMissingSubject pins what each tool requires, and pins
// it in THIS surface's vocabulary.
//
// The shared layer refuses several of these too, and it refuses them naming a
// command line flag — correct where a person typed one, and a puzzle here, since a
// caller sends an arguments object and has no flags. So each message is asserted to
// name the argument rather than a flag, which is what makes catching these at this
// boundary worth doing at all.
func TestPublicationToolsRefuseAMissingSubject(t *testing.T) {
	s, _ := publishFixture(t, defaultRegistryWorkspace)

	for _, tc := range []struct {
		name string
		tool string
		args string
		want string
	}{
		{"publish with no entity", publishEntityToolName, `{}`, "requires an 'entity' path"},
		{"publish an artifact with no file", publishArtifactToolName, `{"name":"h","tag":"2.0.0"}`, "requires a 'file' path"},
		{"publish an artifact with no name", publishArtifactToolName, `{"file":"bundle.txt","tag":"2.0.0"}`, "requires a 'name'"},
		{"publish an artifact with no tag", publishArtifactToolName, `{"file":"bundle.txt","name":"h"}`, "requires a 'tag'"},
		{"sign nothing", signArtifactsToolName, `{}`, "requires 'references'"},
		{"sign an empty entry", signArtifactsToolName, `{"references":[""]}`, "empty entry in 'references'"},
		{"deprecate nothing", deprecateArtifactToolName, `{}`, "requires a 'reference'"},
		{"deprecate with no content", deprecateArtifactToolName,
			`{"reference":"` + publishTestRegistry + `/component/core/api-server:1.0.0"}`,
			"requires 'severity' and 'reason'"},
		{"deprecate both ways at once", deprecateArtifactToolName,
			`{"reference":"x:1","from":"batch.yaml"}`, "not both"},
		{"deprecate with content a batch file would override", deprecateArtifactToolName,
			`{"from":"batch.yaml","severity":"warning"}`, "reads its content from the batch file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := callToolRaw(t, s, tc.tool, tc.args)
			if !f.failed {
				t.Fatalf("expected a refusal; got %q", f.text)
			}
			if !strings.Contains(f.text, tc.want) {
				t.Errorf("the refusal must name what to send: got %q, want it to contain %q", f.text, tc.want)
			}
			if strings.Contains(f.text, "--") {
				t.Errorf("a refusal this surface owns must not name a command line flag: %q", f.text)
			}
		})
	}
}

// TestPublicationToolsRefuseWithNoDestinationRegistry pins the one refusal the
// shared layer would otherwise answer by naming a flag nobody here can pass.
//
// The fixture spells default_registry as an explicitly empty value rather than
// omitting the key, and that is load-bearing rather than verbose: an ABSENT key
// takes the built-in default, so a workspace that merely says nothing about a
// registry still has one and this refusal cannot arise. Simplifying the fixture to
// an absent key leaves a test that passes while asserting nothing.
func TestPublicationToolsRefuseWithNoDestinationRegistry(t *testing.T) {
	s, root := publishFixture(t, "workspace: {}\ndefault_registry: \"\"\n")

	for _, tc := range []struct {
		tool string
		args string
	}{
		{publishEntityToolName, `{"entity":"1.0.0/entity.yaml","dry_run":true,"workspace_root":"` + root + `"}`},
		{publishArtifactToolName, `{"file":"bundle.txt","name":"h","tag":"2.0.0","dry_run":true,"workspace_root":"` + root + `"}`},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			f := callToolRaw(t, s, tc.tool, tc.args)
			if !f.failed {
				t.Fatalf("expected a refusal; got %q", f.text)
			}
			if !strings.Contains(f.text, "no destination registry") {
				t.Errorf("the refusal must name what is missing: %q", f.text)
			}
			if strings.Contains(f.text, "--") {
				t.Errorf("a refusal this surface owns must not name a command line flag: %q", f.text)
			}
		})
	}
}

// TestPublicationToolsRefuseLoudlyWithNoCredentialAndDoNotHang is the credential
// proof, driven rather than asserted.
//
// Nothing supplies an identity: no argument carries one, the configuration names no
// held key, and the environment is emptied of every source the ladder reads. On a
// surface with a terminal this is where a browser would have opened; here there is
// nobody to ask, so the only two possible behaviours are a loud refusal and a HANG —
// and the hang is the worse failure, because nothing is watching it block.
//
// So each call is bounded by a LIVENESS CEILING rather than a performance budget: it
// separates "returned" from "did not return", which no amount of machine contention
// can blur. And the refusal must name something a caller on THIS surface can act on,
// which is the exported variable rather than the flag.
//
// The data-artifact publication is deliberately absent: it resolves no identity at
// all — a published artifact is signed afterwards, by reference — so there is no
// credential for it to refuse over, and claiming it as a fourth passing case would
// be a proof of nothing.
func TestPublicationToolsRefuseLoudlyWithNoCredentialAndDoNotHang(t *testing.T) {
	t.Setenv(signing.EnvIdentityToken, "")
	t.Setenv(signing.EnvSigstoreIDToken, "")
	if signing.GitHubActionsAmbientAvailable(os.Getenv) {
		t.Skip("the ambient OIDC endpoint is present in this environment, so an absent credential cannot be arranged here")
	}

	s, root := publishFixture(t, signingRequiredWorkspace)
	reference := publishTestRegistry + "/component/core/api-server:1.0.0"

	// The signing batch and the deprecation marking resolve their identity before
	// they touch a registry at all, so the tool carries the refusal with no
	// destination in play.
	for _, tc := range []struct {
		name string
		tool string
		args any
	}{
		{
			name: "a signing batch",
			tool: signArtifactsToolName,
			args: signArtifactsArgs{References: []string{reference}, WorkspaceRoot: root},
		},
		{
			name: "a deprecation marking",
			tool: deprecateArtifactToolName,
			args: deprecateArtifactArgs{
				Reference: reference, Severity: "warning", Reason: "superseded", WorkspaceRoot: root,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			done := make(chan *callToolResult, 1)
			go func() { done <- callTool(t, s, tc.tool, tc.args) }()

			select {
			case res := <-done:
				if !res.IsError {
					t.Fatalf("an operation with no credential must be refused, not answered; got %s", res.Content[0].Text)
				}
				assertNamesTheMissingCredential(t, res.Content[0].Text)
			case <-time.After(30 * time.Second):
				t.Fatal("the call did not return: a surface with nobody watching must refuse rather than block")
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
		cfg, refusal := s.publishEntityConfig(publishEntityArgs{Entity: "1.0.0/entity.yaml", WorkspaceRoot: root})
		if refusal != "" {
			t.Fatalf("publishEntityConfig: %s", refusal)
		}
		store := memory.New()
		cfg.PushTarget = store

		type outcome struct {
			payload publishEntityPayload
			err     error
		}
		done := make(chan outcome, 1)
		go func() {
			payload, err := runPublishEntity(t.Context(), cfg)
			done <- outcome{payload, err}
		}()

		select {
		case got := <-done:
			if got.err == nil {
				t.Fatalf("a publication that must sign with no credential must be refused; got %+v", got.payload)
			}
			assertNamesTheMissingCredential(t, got.err.Error())
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
// the caller can act on, and to naming nothing it cannot.
//
// A bare "failed" would leave a caller with nothing to do, which on a surface with
// nobody watching is the same as silence. Both halves are asserted because either
// alone passes on a broken message: naming the environment variable is satisfied
// by a message that ALSO tells the caller to pass a flag it has no way to pass,
// and forbidding the flag is satisfied by a message that names nothing at all.
func assertNamesTheMissingCredential(t *testing.T, message string) {
	t.Helper()
	// A substring check against an empty string succeeds against anything, so an
	// unset vocabulary would turn the loop below into a check that cannot fail
	// while still reading as one. Refuse that before believing either verdict.
	if toolIdentitySupply.AbsentInput == "" || toolIdentitySupply.Remedy == "" {
		t.Fatalf("this surface declares no identity vocabulary (%+v), so the assertions below would "+
			"pass against any message at all", toolIdentitySupply)
	}
	for _, want := range []string{
		"identity token",
		signing.EnvIdentityToken,
		toolIdentitySupply.AbsentInput,
		toolIdentitySupply.Remedy,
	} {
		if !strings.Contains(message, want) {
			t.Errorf("the refusal must name %q; got %q", want, message)
		}
	}
	for _, unwanted := range []string{"--", "terminal", "stdin"} {
		if strings.Contains(message, unwanted) {
			t.Errorf("the refusal names %q, which belongs to a caller with a command line and a "+
				"terminal; a caller here has neither: %q", unwanted, message)
		}
	}
	t.Logf("refusal: %s", message)
}

// TestPublishEntityToolRefusesUnsignedWhereASignatureIsRequired is the first half
// of the relocation guard: a publication that would land unsigned on a registry
// that requires a signature is refused, and NOTHING lands.
//
// It is asserted on two channels because neither covers the other. Through the tool
// dispatch, which is what a caller sees: the refusal reaches the tool result.
// Through the operation's own contract with a destination injected: no bytes
// reached it. A refusal that had already published would pass the first and fail
// the second, which is precisely the failure a positive test cannot see.
func TestPublishEntityToolRefusesUnsignedWhereASignatureIsRequired(t *testing.T) {
	s, root := publishFixture(t, signingRequiredWorkspace)
	unsigned := false

	f := callToolRaw(t, s, publishEntityToolName,
		`{"entity":"1.0.0/entity.yaml","sign":false,"workspace_root":"`+root+`"}`)
	if !f.failed {
		t.Fatalf("an unsigned publication to a signature-requiring registry must be refused; got %q", f.text)
	}
	if !strings.Contains(f.text, "requires signatures") {
		t.Errorf("the refusal must name why: %q", f.text)
	}

	// The same call against a destination this test holds, so what the refusal did
	// to the registry is readable rather than assumed.
	cfg, refusal := s.publishEntityConfig(publishEntityArgs{
		Entity: "1.0.0/entity.yaml", Sign: &unsigned, WorkspaceRoot: root,
	})
	if refusal != "" {
		t.Fatalf("publishEntityConfig: %s", refusal)
	}
	store := memory.New()
	cfg.PushTarget = store
	if _, err := runPublishEntity(t.Context(), cfg); err == nil {
		t.Fatal("the operation answered an unsigned publication that must be refused")
	} else if !errors.Is(err, signing.ErrUnsignedPushRefused()) {
		t.Errorf("the refusal must carry the unsigned-publication condition; got %v", err)
	}
	if _, exists, err := oci.Resolve(context.Background(), store, "1.0.0"); err != nil {
		t.Fatalf("Resolve: %v", err)
	} else if exists {
		t.Error("the guardrail let bytes through: the tag exists after a refused publication")
	}
}

// TestSignatureTheToolProtocolAttachedDoesNotVerifyAgainstAnotherKey is the second
// half of the relocation guard, and the one a positive test cannot see at all.
//
// A signature produced through this surface is read back off the destination and put
// to a verifier three ways: against the key that made it, which must PASS — the
// CONTROL, and it runs FIRST, because without it a verifier that refused everything
// would satisfy both negatives; against a different key, which must REFUSE; and
// against a different artifact digest, which must also REFUSE, because a signature
// that verified against bytes it never committed to would be no signature at all.
//
// Both routes to a key are driven, because this surface has two and a caller may use
// either: the workspace's own signing block, and the key_path argument. A keyless
// bundle cannot be minted offline — the keyless signer is live-only by construction
// — so the keyed path is what any hermetic proof of verification can use.
func TestSignatureTheToolProtocolAttachedDoesNotVerifyAgainstAnotherKey(t *testing.T) {
	workspaceYAML, keyPath, pubPEM := keyedSigningWorkspace(t)

	for _, tc := range []struct {
		name string
		args publishEntityArgs
	}{
		{
			name: "the key the workspace configures",
			args: publishEntityArgs{Entity: "1.0.0/entity.yaml"},
		},
		{
			name: "the key the call names",
			args: publishEntityArgs{Entity: "1.0.0/entity.yaml", KeyPath: keyPath},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, root := publishFixture(t, workspaceYAML)
			args := tc.args
			args.WorkspaceRoot = root

			cfg, refusal := s.publishEntityConfig(args)
			if refusal != "" {
				t.Fatalf("publishEntityConfig: %s", refusal)
			}
			store := memory.New()
			cfg.PushTarget = store
			got, err := runPublishEntity(t.Context(), cfg)
			if err != nil {
				t.Fatalf("publish: %v", err)
			}
			if !got.Signed {
				t.Fatal("a publication to a signature-requiring registry must report that it signed")
			}
			if !got.Reach.BeyondThisMachine {
				t.Errorf("a publication that landed must say it reached beyond this machine: %+v", got.Reach)
			}

			bundle := onlySignatureBundle(t, store, "1.0.0")

			if err := signing.VerifyKeyedBundle(bundle, pubPEM, got.Digest); err != nil {
				t.Fatalf("the control failed: the signature does not verify against the key that made it: %v", err)
			}

			_, otherPub, err := signing.GenerateKeyedPEM()
			if err != nil {
				t.Fatalf("GenerateKeyedPEM: %v", err)
			}
			if err := signing.VerifyKeyedBundle(bundle, otherPub, got.Digest); err == nil {
				t.Error("a signature verified against a key that did not make it")
			}

			const foreignDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
			if err := signing.VerifyKeyedBundle(bundle, pubPEM, foreignDigest); err == nil {
				t.Error("a signature verified against an artifact it never committed to")
			}
		})
	}
}

// TestPublishEntityToolLandsBytesAndReportsTheirDigest proves the answer's digest is
// the destination's, not a value the operation computed and then failed to use, and
// that a second identical publication is reported as the no-op it is.
func TestPublishEntityToolLandsBytesAndReportsTheirDigest(t *testing.T) {
	s, root := publishFixture(t, defaultRegistryWorkspace)
	store := memory.New()

	got := publishThroughTool(t, s, root, store)
	if got.DryRun || got.AlreadyPublished || got.Signed {
		t.Errorf("an ordinary publication reports none of those; got %+v", got)
	}
	landed, exists, err := oci.Resolve(context.Background(), store, "1.0.0")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !exists {
		t.Fatal("the answer reported a publication that did not land")
	}
	if landed != got.Digest {
		t.Errorf("the reported digest is not the one the destination holds: got %q, destination %q", got.Digest, landed)
	}

	again := publishThroughTool(t, s, root, store)
	if !again.AlreadyPublished {
		t.Errorf("a byte-identical re-publication is an idempotent no-op and must say so; got %+v", again)
	}
	if again.Digest != landed {
		t.Errorf("the no-op must report the digest already there: got %q, want %q", again.Digest, landed)
	}
}

// TestPublishArtifactToolLandsBytesAndReportsTheirDigest is the same proof for the
// data artifact, whose bytes are opaque.
func TestPublishArtifactToolLandsBytesAndReportsTheirDigest(t *testing.T) {
	s, root := publishFixture(t, defaultRegistryWorkspace)
	store := memory.New()

	cfg, refusal := s.publishArtifactConfig(publishArtifactArgs{
		File: "bundle.txt", Name: "handbook", Tag: "2.0.0", WorkspaceRoot: root,
	})
	if refusal != "" {
		t.Fatalf("publishArtifactConfig: %s", refusal)
	}
	cfg.PushTarget = store
	got, err := runPublishArtifact(t.Context(), cfg)
	if err != nil {
		t.Fatalf("publish the artifact: %v", err)
	}
	landed, exists, err := oci.Resolve(context.Background(), store, "2.0.0")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !exists {
		t.Fatal("the answer reported a publication that did not land")
	}
	if landed != got.Digest {
		t.Errorf("the reported digest is not the one the destination holds: got %q, destination %q", got.Digest, landed)
	}
	if !got.Reach.BeyondThisMachine {
		t.Errorf("an artifact that landed must say it reached beyond this machine: %+v", got.Reach)
	}
}

// TestSignArtifactsToolSignsWhatIsPublishedAndRefusesWhatIsNot covers both
// directions of the signing tool against a destination this test holds.
//
// The refusal half is the load-bearing one: signing a reference nothing published
// must fail rather than attach a signature to nothing, and the batch must report
// that failure rather than answer with an empty success.
func TestSignArtifactsToolSignsWhatIsPublishedAndRefusesWhatIsNot(t *testing.T) {
	workspaceYAML, _, pubPEM := keyedSigningWorkspace(t)
	s, root := publishFixture(t, workspaceYAML)
	store := memory.New()

	// Publish WITHOUT a signature so the signing tool is what attaches one. The
	// configured destination requires signatures, so an explicitly unsigned
	// publication is refused there — this case publishes to a second host that does
	// not.
	unsigned := false
	cfg, refusal := s.publishEntityConfig(publishEntityArgs{
		Entity: "1.0.0/entity.yaml", Sign: &unsigned, Registry: "plain.example.test", WorkspaceRoot: root,
	})
	if refusal != "" {
		t.Fatalf("publishEntityConfig: %s", refusal)
	}
	cfg.PushTarget = store
	published, err := runPublishEntity(t.Context(), cfg)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	reference := published.Reference + ":1.0.0"
	signCfg, signRefusal := s.signArtifactsConfig(signArtifactsArgs{
		References: []string{reference}, WorkspaceRoot: root,
	})
	if signRefusal != "" {
		t.Fatalf("signArtifactsConfig: %s", signRefusal)
	}
	signCfg.Target = store
	signed, err := runSignArtifacts(t.Context(), signCfg)
	if err != nil {
		t.Fatalf("sign: %v", err)
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
	missCfg, missRefusal := s.signArtifactsConfig(signArtifactsArgs{
		References: []string{absent}, WorkspaceRoot: root,
	})
	if missRefusal != "" {
		t.Fatalf("signArtifactsConfig: %s", missRefusal)
	}
	missCfg.Target = store
	if _, err := runSignArtifacts(t.Context(), missCfg); err == nil {
		t.Fatal("signing a reference nothing published must be refused")
	} else if !strings.Contains(err.Error(), "nothing to sign") {
		t.Errorf("the refusal must say what was wrong: %q", err.Error())
	}
}

// TestDeprecateArtifactToolAttachesAMarkerAndRefusesAnAbsentSubject covers both
// directions of the deprecation tool.
//
// A withdrawal never publishes what it withdraws, so a marker against a reference
// nothing published must be refused rather than attached — otherwise a caller could
// record a withdrawal for an artifact that does not exist, which every consumer
// would then read.
func TestDeprecateArtifactToolAttachesAMarkerAndRefusesAnAbsentSubject(t *testing.T) {
	workspaceYAML, _, _ := keyedSigningWorkspace(t)
	s, root := publishFixture(t, workspaceYAML)
	store := memory.New()

	published := publishThroughTool(t, s, root, store)
	reference := published.Reference + ":1.0.0"

	cfg, refusal := s.deprecateArtifactConfig(deprecateArtifactArgs{
		Reference:     reference,
		Severity:      "warning",
		Reason:        "superseded by 2.0.0",
		WorkspaceRoot: root,
	})
	if refusal != "" {
		t.Fatalf("deprecateArtifactConfig: %s", refusal)
	}
	cfg.Target = store
	got, err := runDeprecateArtifact(t.Context(), cfg)
	if err != nil {
		t.Fatalf("deprecate: %v", err)
	}
	if len(got.Marked) != 1 {
		t.Fatalf("the answer must name every marker it attached; got %+v", got.Marked)
	}
	marked := got.Marked[0]
	if marked.Reference != reference || marked.Severity != "warning" || marked.IssuedOn == "" || marked.Digest == "" {
		t.Errorf("the answer must carry the marker's own facts; got %+v", marked)
	}
	if !got.Reach.BeyondThisMachine {
		t.Errorf("an attached marker reached beyond this machine; the answer says otherwise: %+v", got.Reach)
	}

	absent := publishTestRegistry + "/component/core/absent:9.9.9"
	missCfg, missRefusal := s.deprecateArtifactConfig(deprecateArtifactArgs{
		Reference: absent, Severity: "warning", Reason: "never published", WorkspaceRoot: root,
	})
	if missRefusal != "" {
		t.Fatalf("deprecateArtifactConfig: %s", missRefusal)
	}
	missCfg.Target = store
	if _, err := runDeprecateArtifact(t.Context(), missCfg); err == nil {
		t.Fatal("a marker against a reference nothing published must be refused")
	} else if !strings.Contains(err.Error(), "nothing to deprecate") {
		t.Errorf("the refusal must say what was wrong: %q", err.Error())
	}
}

// TestTheFourPublicationToolsAndTheirCommandTwinsNameEachOther is the named proof
// that this stage did not leave two undistinguished tools for one capability.
//
// The general guard over every twice-exposed capability lives with the pairing
// mechanism. What this adds is the four this stage created, named rather than
// derived: the general guard passes over whatever pairs it happens to find, so a
// stage that registered none of these would leave it green while the capability it
// was asked to settle stayed silent on both sides.
func TestTheFourPublicationToolsAndTheirCommandTwinsNameEachOther(t *testing.T) {
	s := twinnedServer()

	served := map[string]string{}
	for _, spec := range s.curatedTools() {
		served[spec.name] = spec.description
	}
	for _, spec := range s.deferredTools() {
		served[spec.name] = spec.description
	}

	for _, tc := range []struct{ inProcess, generated string }{
		{publishEntityToolName, "cli-push"},
		{publishArtifactToolName, "cli-push-artifact"},
		{signArtifactsToolName, "cli-sign"},
		{deprecateArtifactToolName, "cli-deprecate"},
	} {
		t.Run(tc.inProcess, func(t *testing.T) {
			curated, ok := served[tc.inProcess]
			if !ok {
				t.Fatalf("this session serves no %q tool", tc.inProcess)
			}
			generated, ok := served[tc.generated]
			if !ok {
				t.Fatalf("this session serves no %q tool", tc.generated)
			}
			if !strings.Contains(curated, "`"+tc.generated+"`") {
				t.Errorf("%s does not name its command twin %s:\n%s", tc.inProcess, tc.generated, curated)
			}
			if !strings.Contains(generated, "`"+tc.inProcess+"`") {
				t.Errorf("%s does not name the in-process form %s:\n%s", tc.generated, tc.inProcess, generated)
			}
			// Which one to reach for is stated rather than left to the reader, and
			// the escape hatch is stated too: silence about either is what the
			// disclosure exists to prevent.
			if !strings.Contains(curated, "Prefer this tool") {
				t.Errorf("%s does not say it is the one to reach for:\n%s", tc.inProcess, curated)
			}
			if !strings.Contains(generated, "Prefer it") {
				t.Errorf("%s does not point the caller at the in-process form:\n%s", tc.generated, curated)
			}
			// The in-process form's opening sentence is what the generated tool
			// quotes, and a publication's caller needs it to say what it writes.
			lead := firstSentence(curated)
			if !strings.Contains(generated, lead) {
				t.Errorf("%s does not carry %s's opening sentence:\n want %q\n got %s",
					tc.generated, tc.inProcess, lead, generated)
			}
			if !strings.Contains(strings.ToLower(lead), "writ") {
				t.Errorf("%s's opening sentence does not say what it writes: %q", tc.inProcess, lead)
			}
			if !strings.Contains(strings.ToLower(lead), "beyond this machine") {
				t.Errorf("%s's opening sentence does not say the write reaches beyond this machine: %q",
					tc.inProcess, lead)
			}
		})
	}
}

// publishThroughTool publishes the fixture entity into store through the tool's own
// argument projection, so a test needing something already published gets there by
// the path under test rather than by a hand-built publication.
func publishThroughTool(t *testing.T, s *server, root string, store oras.Target) publishEntityPayload {
	t.Helper()
	cfg, refusal := s.publishEntityConfig(publishEntityArgs{Entity: "1.0.0/entity.yaml", WorkspaceRoot: root})
	if refusal != "" {
		t.Fatalf("publishEntityConfig: %s", refusal)
	}
	cfg.PushTarget = store
	got, err := runPublishEntity(t.Context(), cfg)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	return got
}

// onlySignatureBundle reads back the one signature referrer attached to tag,
// failing when there is not exactly one. The count is asserted rather than indexed
// past: a test that took the first of several would keep passing while something
// attached a second signature nobody asked for.
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
