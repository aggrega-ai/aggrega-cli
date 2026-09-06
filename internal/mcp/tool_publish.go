// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/publishops"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
)

// The four publication tools: publishing a catalog entity, publishing a
// standalone data artifact, signing an already-published artifact, and attaching
// a deprecation marker.
//
// # These are not new capabilities on this surface
//
// Every one of them was already reachable here as the tool generated from its
// command, which re-runs that command in a separate process and answers with the
// text it printed. What these add is the in-process form: the same operation run
// inside this server, through the shared publication layer, answering with the
// facts rather than with prose a caller would have to parse. Both forms stay
// reachable and each says what the other is — see [commandTwins] — because a
// caller meeting two tools for one capability is otherwise choosing blind.
//
// # Why this surface carries them at all
//
// They write, and what they write is not this machine's: published bytes are what
// every consumer of that registry resolves, and a signature cannot be recalled
// from anyone who already fetched it. That is the widest reach anything here has.
//
// They are carried on the same rule every other operation here runs under, and the
// rule is about WHEN an operation runs rather than about which surface runs it: a
// registry-reaching or state-writing operation runs on EXPLICIT INVOCATION and
// never as a side effect of validating or inspecting a workspace. A caller that
// called one of these has asked for the publication, exactly as a caller calling
// the build tool has asked for a render. What stays out of bounds is a write
// performed while somebody was inspecting, and none of these is that.
//
// # What every answer states
//
// A reach: the registry host the call addressed, and whether the call reached
// beyond this machine — which a dry run did not, and everything else did. It is
// derived from what the operation DID rather than from what the arguments asked
// for, so an answer cannot describe a dry run as a publication or the reverse.
//
// # It never prompts, and it never hangs
//
// Nobody is watching a protocol stream, so the shared layer is given no way to ask
// a person anything: neither the interactive identity rung nor the deprecation
// content prompt is supplied, which is what makes them absent rather than merely
// unused. Identity resolves from a credential the arguments supplied or one the
// environment already carries, and when neither yields anything the operation
// REFUSES, naming what to supply. A hang would be the observable failure
// otherwise, and a caller here has no way to answer a prompt.
//
// # Paths and keys are addressed the way this surface addresses everything
//
// Every path argument is absolute or relative to the workspace root, resolved
// through the one helper every other tool here uses, and a call names the
// workspace it means on the call itself. A signing key is named the same way, by
// `key_path`, for the same reason: this surface's callers run on the machine that
// holds the key, and withholding the argument would leave offline keyed signing —
// the one signing path that completes with nobody watching — reachable only
// through configuration while the generated twin accepts it as a flag.

// publicationReach states how far one publication reached: the registry host it
// addressed, and whether anything landed somewhere other people resolve from.
type publicationReach struct {
	Registry          string `json:"registry"`
	BeyondThisMachine bool   `json:"beyond_this_machine"`
}

// reachOf builds the reach statement for a reference the operation addressed.
func reachOf(reference string, landed bool) publicationReach {
	return publicationReach{Registry: registryhost.Of(reference), BeyondThisMachine: landed}
}

// The four tools' advertised names, spelled once each so the handlers, the
// refusals and the guidance cannot drift from what the availability table
// advertises. The table remains the authority — a tool is named from it — and
// these are what the tool's own messages call it.
const (
	publishEntityToolName     = "publish-entity"
	publishArtifactToolName   = "publish-artifact"
	signArtifactsToolName     = "sign-artifacts"
	deprecateArtifactToolName = "deprecate-artifact"
)

// missingDestination refuses a publication that has nowhere to go, in this
// surface's own vocabulary.
//
// The shared layer refuses the same condition, and it refuses it naming a command
// line flag, which is correct where a person typed one and unusable here. Catching
// it at this boundary is what keeps a caller from being told to pass an option it
// has no way to pass.
func missingDestination(tool, requested string, tw *toolWorkspace) string {
	if requested != "" || tw.cfg.DefaultRegistry != "" {
		return ""
	}
	return tool + " has no destination registry: send 'registry', or set the workspace's default " +
		"registry in its configuration."
}

// publishEntityArgs is the catalog-entity publication tool's argument shape.
//
// sign is a pointer so an absent argument and an explicit false are
// distinguishable: an absent one follows the destination registry's own policy,
// while an explicit value is the caller's choice. Neither relaxes the refusal — a
// registry or workspace policy that requires a signature refuses an unsigned
// publication whatever this carries, and there is no argument that bypasses it.
//
// identity_token is a supplied credential VALUE rather than a path. Leaving it out
// falls through to whatever the environment already carries, and when nothing does
// the publication refuses rather than asking anybody.
type publishEntityArgs struct {
	Entity        string `json:"entity"`
	Registry      string `json:"registry"`
	DryRun        bool   `json:"dry_run"`
	Sign          *bool  `json:"sign"`
	Plugin        string `json:"plugin"`
	KeyPath       string `json:"key_path"`
	FulcioURL     string `json:"fulcio_url"`
	RekorURL      string `json:"rekor_url"`
	TimestampURL  string `json:"timestamp_url"`
	IdentityToken string `json:"identity_token"`
	WorkspaceRoot string `json:"workspace_root"`
}

// publishEntityPayload is the catalog-entity publication's structured result: the
// facts themselves rather than the status lines a terminal reads.
//
// digest is empty on a dry run, which stops before a digest is computed, and the
// two booleans say which path ran — a dry run published nothing, and an
// already-published call found the tag pointing at byte-identical content and
// landed nothing.
type publishEntityPayload struct {
	Reference        string           `json:"reference"`
	Tag              string           `json:"tag"`
	Kind             string           `json:"kind"`
	MediaType        string           `json:"media_type"`
	Size             int              `json:"size"`
	PluginSize       int              `json:"plugin_size,omitempty"`
	Digest           string           `json:"digest"`
	Signed           bool             `json:"signed"`
	AlreadyPublished bool             `json:"already_published"`
	DryRun           bool             `json:"dry_run"`
	Reach            publicationReach `json:"reach"`
}

var publishEntityToolSpec = toolSpec{
	description: "Publish a catalog entity to an OCI registry: it writes, and what it writes reaches beyond this " +
		"machine — the bytes every consumer of that registry resolves, and a signature that cannot be recalled from " +
		"anyone who has already fetched it. Pass the entity document as `entity`, absolute or relative to the " +
		"workspace root; a Plugin entity also needs `plugin`, the executable published inside its manifest. The " +
		"destination is `registry`, defaulting to the workspace's configured one. Set `dry_run` to compute the " +
		"bundle and contact nothing. Signing follows the destination registry's policy unless `sign` says " +
		"otherwise, and a registry that requires signatures refuses an unsigned publication whatever you send — " +
		"there is no argument that bypasses that refusal. Keyless signing resolves an identity from " +
		"`identity_token` or from the environment and refuses loudly when neither carries one, because nothing " +
		"here can prompt; `key_path` selects offline keyed signing instead. The result carries where the entity " +
		"landed, the tag, the published digest, whether it was signed, and how far the call reached.",
	inputSchema: `{
  "type": "object",
  "properties": {
    "entity": {
      "type": "string",
      "minLength": 1,
      "description": "Path to the entity document to publish, absolute or relative to the workspace root."
    },
    "plugin": {
      "type": "string",
      "minLength": 1,
      "description": "Path to the .wasm executable, required when the entity is a Plugin. Absolute or relative to the workspace root."
    },
    "registry": {
      "type": "string",
      "description": "Destination registry host. Defaults to the workspace's configured default registry."
    },
    "dry_run": {
      "type": "boolean",
      "description": "Compute the bundle and report what would be published without contacting the registry."
    },
    "sign": {
      "type": "boolean",
      "description": "Sign the publication. Absent follows the destination registry's own policy. A registry that requires signatures refuses an unsigned publication regardless."
    },
    "key_path": {
      "type": "string",
      "minLength": 1,
      "description": "Path to a private key for offline keyed signing. Absent selects keyless signing when a signature is produced."
    },
    "identity_token": {
      "type": "string",
      "minLength": 1,
      "description": "Identity token value for keyless signing. Absent falls through to the environment; when nothing carries one the publication refuses rather than prompting."
    },
    "fulcio_url": {
      "type": "string",
      "description": "Certificate authority endpoint for keyless signing. Absent uses the configured or public-good default."
    },
    "rekor_url": {
      "type": "string",
      "description": "Transparency log endpoint for keyless signing. Absent uses the configured or public-good default."
    },
    "timestamp_url": {
      "type": "string",
      "description": "Timestamp authority endpoint for keyless signing. Absent uses the configured or public-good default."
    },
    "workspace_root": {
      "type": "string",
      "description": "Optional workspace root override. Defaults to discovery from the server's working directory."
    }
  },
  "required": ["entity"]
}`,
	invoke: publishEntityInvoke,
}

func publishEntityInvoke(s *server, _ context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
	var a publishEntityArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return invalidToolArguments(publishEntityToolName, err)
		}
	}
	cfg, refusal := s.publishEntityConfig(a)
	if refusal != "" {
		return errorResult(refusal)
	}
	payload, err := runPublishEntity(cfg)
	if err != nil {
		return errorResult(explainToolError("The publication did not happen.", err, ""))
	}
	return textResult(renderPublishEntityText(payload), payload)
}

// publishEntityConfig projects the arguments onto the publication's input
// contract, returning the corrective text for anything that cannot be turned into
// one.
//
// The credential seams are left NIL, and that is the mechanism rather than an
// omission: a nil token getter means the shared identity ladder has no rung that
// needs a person, so it refuses instead of blocking on an answer nobody can give.
func (s *server) publishEntityConfig(a publishEntityArgs) (publishops.PushConfig, string) {
	if strings.TrimSpace(a.Entity) == "" {
		return publishops.PushConfig{}, publishEntityToolName + " requires an 'entity' path: the entity document to publish."
	}
	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, nil)
	if err != nil {
		return publishops.PushConfig{}, explainToolError("Could not resolve the workspace to publish from.", err, "")
	}
	if refusal := missingDestination(publishEntityToolName, a.Registry, tw); refusal != "" {
		return publishops.PushConfig{}, refusal
	}
	pluginPath := ""
	if strings.TrimSpace(a.Plugin) != "" {
		pluginPath = tw.absWorkspacePath(strings.TrimSpace(a.Plugin))
	}
	return publishops.PushConfig{
		EntityPath:   tw.absWorkspacePath(strings.TrimSpace(a.Entity)),
		Registry:     a.Registry,
		DryRun:       a.DryRun,
		Sign:         a.Sign,
		PluginPath:   pluginPath,
		KeyPath:      a.KeyPath,
		FulcioURL:    a.FulcioURL,
		RekorURL:     a.RekorURL,
		TimestampURL: a.TimestampURL,
		IDToken:      a.IdentityToken,
		Workspace:    tw.rw,
		Out:          io.Discard,
	}, ""
}

// runPublishEntity runs the publication and shapes the answer.
//
// The operation's status lines are discarded rather than folded into the result.
// They are prose written for a person watching a terminal, and this surface
// answers with the facts themselves — which is the whole difference between this
// tool and the one that runs the command in a separate process.
func runPublishEntity(cfg publishops.PushConfig) (publishEntityPayload, error) {
	res, err := publishops.Push(cfg)
	if err != nil {
		return publishEntityPayload{}, err
	}
	return publishEntityPayload{
		Reference:        res.Reference,
		Tag:              res.Tag,
		Kind:             res.Kind,
		MediaType:        res.MediaType,
		Size:             res.Size,
		PluginSize:       res.PluginSize,
		Digest:           res.Digest,
		Signed:           res.Signed,
		AlreadyPublished: res.AlreadyPublished,
		DryRun:           res.DryRun,
		Reach:            reachOf(res.Reference, !res.DryRun),
	}, nil
}

// renderPublishEntityText renders the human-readable summary of a publication.
func renderPublishEntityText(p publishEntityPayload) string {
	var b strings.Builder
	switch {
	case p.DryRun:
		fmt.Fprintf(&b, "Dry run: %s:%s would be published as %s (%s, %d bytes). Nothing was contacted.",
			p.Reference, p.Tag, p.Kind, p.MediaType, p.Size)
	case p.AlreadyPublished:
		fmt.Fprintf(&b, "Already published: %s:%s already points at %s. Nothing was landed.",
			p.Reference, p.Tag, p.Digest)
	default:
		fmt.Fprintf(&b, "Published %s:%s as %s (%s, %d bytes) at %s.",
			p.Reference, p.Tag, p.Kind, p.MediaType, p.Size, p.Digest)
	}
	if p.Signed {
		b.WriteString(" A signature was attached and cannot be recalled from anyone who fetches it.")
	}
	return b.String()
}

// publishArtifactArgs is the data-artifact publication tool's argument shape. The
// bytes are opaque: no entity envelope is parsed and no kind is dispatched on.
type publishArtifactArgs struct {
	File          string `json:"file"`
	Name          string `json:"name"`
	Tag           string `json:"tag"`
	MediaType     string `json:"media_type"`
	Registry      string `json:"registry"`
	DryRun        bool   `json:"dry_run"`
	WorkspaceRoot string `json:"workspace_root"`
}

// publishArtifactPayload is the data-artifact publication's structured result,
// carrying the same facts and the same reach a catalog-entity publication carries.
type publishArtifactPayload struct {
	Reference        string           `json:"reference"`
	Tag              string           `json:"tag"`
	MediaType        string           `json:"media_type"`
	Size             int              `json:"size"`
	Digest           string           `json:"digest"`
	AlreadyPublished bool             `json:"already_published"`
	DryRun           bool             `json:"dry_run"`
	Reach            publicationReach `json:"reach"`
}

var publishArtifactToolSpec = toolSpec{
	description: "Publish a standalone data artifact — a documentation bundle, or any opaque file — to an OCI " +
		"registry: it writes, and what it writes reaches beyond this machine exactly as a catalog-entity " +
		"publication does, because where the bytes land and what resolving that tag returns are just as public. " +
		"Pass the file as `file`, absolute or relative to the workspace root, with `name` and `tag` naming what it " +
		"is published as. The bytes are opaque — no entity envelope is parsed — and `media_type` defaults to the " +
		"documentation-bundle type. Set `dry_run` to compute the bundle and contact nothing. It signs nothing: a " +
		"published artifact is signed afterwards by reference with the sign-artifacts tool, which needs only the " +
		"reference this result reports. The result carries where the artifact landed, its published digest, and " +
		"how far the call reached.",
	inputSchema: `{
  "type": "object",
  "properties": {
    "file": {
      "type": "string",
      "minLength": 1,
      "description": "Path to the file to publish, absolute or relative to the workspace root. Its bytes are published verbatim."
    },
    "name": {
      "type": "string",
      "minLength": 1,
      "description": "The artifact name it is published under."
    },
    "tag": {
      "type": "string",
      "minLength": 1,
      "description": "The version tag it is published under."
    },
    "media_type": {
      "type": "string",
      "minLength": 1,
      "description": "Media type the opaque bytes are published under. Defaults to the documentation-bundle media type."
    },
    "registry": {
      "type": "string",
      "description": "Destination registry host. Defaults to the workspace's configured default registry."
    },
    "dry_run": {
      "type": "boolean",
      "description": "Compute the bundle and report what would be published without contacting the registry."
    },
    "workspace_root": {
      "type": "string",
      "description": "Optional workspace root override. Defaults to discovery from the server's working directory."
    }
  },
  "required": ["file", "name", "tag"]
}`,
	invoke: publishArtifactInvoke,
}

func publishArtifactInvoke(s *server, _ context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
	var a publishArtifactArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return invalidToolArguments(publishArtifactToolName, err)
		}
	}
	cfg, refusal := s.publishArtifactConfig(a)
	if refusal != "" {
		return errorResult(refusal)
	}
	payload, err := runPublishArtifact(cfg)
	if err != nil {
		return errorResult(explainToolError("The artifact publication did not happen.", err, ""))
	}
	return textResult(renderPublishArtifactText(payload), payload)
}

// publishArtifactConfig projects the arguments onto the data-artifact
// publication's input contract, returning the corrective text for anything that
// cannot be turned into one.
func (s *server) publishArtifactConfig(a publishArtifactArgs) (publishops.PushArtifactConfig, string) {
	if strings.TrimSpace(a.File) == "" {
		return publishops.PushArtifactConfig{}, publishArtifactToolName + " requires a 'file' path: the artifact to publish."
	}
	if strings.TrimSpace(a.Name) == "" {
		return publishops.PushArtifactConfig{}, publishArtifactToolName + " requires a 'name': the artifact name it is published under."
	}
	if strings.TrimSpace(a.Tag) == "" {
		return publishops.PushArtifactConfig{}, publishArtifactToolName + " requires a 'tag': the version it is published under."
	}
	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, nil)
	if err != nil {
		return publishops.PushArtifactConfig{}, explainToolError("Could not resolve the workspace to publish from.", err, "")
	}
	if refusal := missingDestination(publishArtifactToolName, a.Registry, tw); refusal != "" {
		return publishops.PushArtifactConfig{}, refusal
	}
	return publishops.PushArtifactConfig{
		FilePath:  tw.absWorkspacePath(strings.TrimSpace(a.File)),
		Name:      strings.TrimSpace(a.Name),
		Tag:       strings.TrimSpace(a.Tag),
		MediaType: a.MediaType,
		Registry:  a.Registry,
		DryRun:    a.DryRun,
		Workspace: tw.rw,
		Out:       io.Discard,
	}, ""
}

// runPublishArtifact runs the data-artifact publication and shapes the answer.
func runPublishArtifact(cfg publishops.PushArtifactConfig) (publishArtifactPayload, error) {
	res, err := publishops.PushArtifact(cfg)
	if err != nil {
		return publishArtifactPayload{}, err
	}
	return publishArtifactPayload{
		Reference:        res.Reference,
		Tag:              res.Tag,
		MediaType:        res.MediaType,
		Size:             res.Size,
		Digest:           res.Digest,
		AlreadyPublished: res.AlreadyPublished,
		DryRun:           res.DryRun,
		Reach:            reachOf(res.Reference, !res.DryRun),
	}, nil
}

// renderPublishArtifactText renders the human-readable summary of a data-artifact
// publication.
func renderPublishArtifactText(p publishArtifactPayload) string {
	switch {
	case p.DryRun:
		return fmt.Sprintf("Dry run: %s:%s would be published as %s (%d bytes). Nothing was contacted.",
			p.Reference, p.Tag, p.MediaType, p.Size)
	case p.AlreadyPublished:
		return fmt.Sprintf("Already published: %s:%s already points at %s. Nothing was landed.",
			p.Reference, p.Tag, p.Digest)
	default:
		return fmt.Sprintf("Published %s:%s as %s (%d bytes) at %s.",
			p.Reference, p.Tag, p.MediaType, p.Size, p.Digest)
	}
}

// signArtifactsArgs is the signing tool's argument shape: the already-published
// references to sign, and the keyless inputs identity resolution may need.
//
// The references are addresses in a registry rather than paths, so they carry no
// workspace-relative contract. One authentication serves the whole batch.
type signArtifactsArgs struct {
	References    []string `json:"references"`
	KeyPath       string   `json:"key_path"`
	FulcioURL     string   `json:"fulcio_url"`
	RekorURL      string   `json:"rekor_url"`
	TimestampURL  string   `json:"timestamp_url"`
	IdentityToken string   `json:"identity_token"`
	WorkspaceRoot string   `json:"workspace_root"`
}

// signedArtifact is one signed reference and the digest its signature commits to.
// The digest is what makes the answer checkable: a signature commits to a digest
// string, so naming the reference alone would not say what was signed.
type signedArtifact struct {
	Reference string `json:"reference"`
	Digest    string `json:"digest"`
}

// signArtifactsPayload is the signing tool's structured result: every reference
// signed, with the published digest its signature commits to.
//
// It reports SUCCESSES only. A batch in which any reference failed is answered as
// a failure naming each one rather than as a result with a short list — a partial
// batch reported as a result is how a caller comes to read a refusal as a pass,
// and signing is the operation where that reading costs most.
type signArtifactsPayload struct {
	Signed []signedArtifact `json:"signed"`
	Reach  publicationReach `json:"reach"`
}

var signArtifactsToolSpec = toolSpec{
	description: "Sign already-published artifacts and attach each signature to the registry: it writes beyond " +
		"this machine, and a signature is the one thing here that cannot be taken back at all — the artifact it " +
		"vouches for is untouched, but anyone who fetches that artifact afterwards fetches the signature with it. " +
		"Pass the " +
		"already-published references as `references`; each is a registry address rather than a path. One " +
		"authentication serves the whole batch, resolved before any reference is signed, so a batch with no usable " +
		"credential refuses at the start rather than partway through. Keyless signing takes its identity from " +
		"`identity_token` or from the environment and refuses loudly when neither carries one, because nothing " +
		"here can prompt; `key_path` selects offline keyed signing instead. Signing a reference nothing published " +
		"is refused rather than attached to nothing. The result names every reference signed with the digest its " +
		"signature commits to.",
	inputSchema: `{
  "type": "object",
  "properties": {
    "references": {
      "type": "array",
      "minItems": 1,
      "items": {"type": "string", "minLength": 1},
      "description": "The already-published references to sign, each a registry address such as registry.example/component/core/api-server:1.0.0."
    },
    "key_path": {
      "type": "string",
      "minLength": 1,
      "description": "Path to a private key for offline keyed signing. Absent selects keyless signing."
    },
    "identity_token": {
      "type": "string",
      "minLength": 1,
      "description": "Identity token value for keyless signing. Absent falls through to the environment; when nothing carries one the batch refuses rather than prompting."
    },
    "fulcio_url": {
      "type": "string",
      "description": "Certificate authority endpoint for keyless signing. Absent uses the configured or public-good default."
    },
    "rekor_url": {
      "type": "string",
      "description": "Transparency log endpoint for keyless signing. Absent uses the configured or public-good default."
    },
    "timestamp_url": {
      "type": "string",
      "description": "Timestamp authority endpoint for keyless signing. Absent uses the configured or public-good default."
    },
    "workspace_root": {
      "type": "string",
      "description": "Optional workspace root override. Defaults to discovery from the server's working directory."
    }
  },
  "required": ["references"]
}`,
	invoke: signArtifactsInvoke,
}

func signArtifactsInvoke(s *server, _ context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
	var a signArtifactsArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return invalidToolArguments(signArtifactsToolName, err)
		}
	}
	cfg, refusal := s.signArtifactsConfig(a)
	if refusal != "" {
		return errorResult(refusal)
	}
	payload, err := runSignArtifacts(cfg)
	if err != nil {
		return errorResult(explainToolError("The signing did not happen.", err, ""))
	}
	return textResult(renderSignArtifactsText(payload), payload)
}

// signArtifactsConfig projects the arguments onto the signing operation's input
// contract, returning the corrective text for anything that cannot be turned into
// one.
func (s *server) signArtifactsConfig(a signArtifactsArgs) (publishops.SignConfig, string) {
	if len(a.References) == 0 {
		return publishops.SignConfig{}, signArtifactsToolName +
			" requires 'references': one or more already-published references to sign."
	}
	for _, ref := range a.References {
		if strings.TrimSpace(ref) == "" {
			return publishops.SignConfig{}, signArtifactsToolName +
				" was sent an empty entry in 'references'; every entry names one already-published artifact."
		}
	}
	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, nil)
	if err != nil {
		return publishops.SignConfig{}, explainToolError("Could not resolve the workspace to sign against.", err, "")
	}
	return publishops.SignConfig{
		References:   a.References,
		KeyPath:      a.KeyPath,
		FulcioURL:    a.FulcioURL,
		RekorURL:     a.RekorURL,
		TimestampURL: a.TimestampURL,
		IDToken:      a.IdentityToken,
		Workspace:    tw.rw,
		Out:          io.Discard,
	}, ""
}

// runSignArtifacts runs the signing batch and shapes the answer.
func runSignArtifacts(cfg publishops.SignConfig) (signArtifactsPayload, error) {
	res, err := publishops.Sign(cfg)
	if err != nil {
		return signArtifactsPayload{}, err
	}
	// Every collection is an array rather than a null, so a caller iterates
	// unconditionally.
	signed := make([]signedArtifact, 0, len(res.Signed))
	for _, a := range res.Signed {
		signed = append(signed, signedArtifact{Reference: a.Reference, Digest: a.Digest})
	}
	host := ""
	if len(cfg.References) > 0 {
		host = cfg.References[0]
	}
	return signArtifactsPayload{Signed: signed, Reach: reachOf(host, len(signed) > 0)}, nil
}

// renderSignArtifactsText renders the human-readable summary of a signing batch.
func renderSignArtifactsText(p signArtifactsPayload) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Signed %d artifact(s); each signature is attached and cannot be recalled from anyone who fetches it:\n",
		len(p.Signed))
	for _, a := range p.Signed {
		fmt.Fprintf(&b, "  %s -> %s\n", a.Reference, a.Digest)
	}
	return strings.TrimRight(b.String(), "\n")
}

// deprecateArtifactArgs is the deprecation tool's argument shape.
//
// Two shapes, and exactly one of them: a single reference with its severity and
// reason, or `from` naming a batch file. The content arguments are required in the
// single shape — nobody is watching, so there is nothing to prompt, and a missing
// one is refused naming what to send.
type deprecateArtifactArgs struct {
	Reference     string `json:"reference"`
	Severity      string `json:"severity"`
	Reason        string `json:"reason"`
	Replacement   string `json:"replacement"`
	Message       string `json:"message"`
	From          string `json:"from"`
	KeyPath       string `json:"key_path"`
	FulcioURL     string `json:"fulcio_url"`
	RekorURL      string `json:"rekor_url"`
	TimestampURL  string `json:"timestamp_url"`
	IdentityToken string `json:"identity_token"`
	WorkspaceRoot string `json:"workspace_root"`
}

// deprecationMarked is one attached marker: the artifact it withdraws, the
// severity it carries, when it was stamped, and the marker's own manifest digest.
//
// The digest is the marker's identity — a marker is attached as an untagged
// referrer of the artifact it withdraws, so nothing else addresses it.
type deprecationMarked struct {
	Reference string `json:"reference"`
	Severity  string `json:"severity"`
	IssuedOn  string `json:"issued_on"`
	Digest    string `json:"digest"`
}

// deprecateArtifactPayload is the deprecation tool's structured result: every
// marker attached, with the marker's own manifest digest.
type deprecateArtifactPayload struct {
	Marked []deprecationMarked `json:"marked"`
	Reach  publicationReach    `json:"reach"`
}

var deprecateArtifactToolSpec = toolSpec{
	description: "Attach a signed deprecation marker to an already-published artifact: it writes beyond this " +
		"machine, and it is the sharpest of these to read wrongly — the artifact it withdraws is untouched, but " +
		"every consumer resolving that reference afterwards sees the withdrawal, and a retraction a workspace " +
		"trusts takes the version out of what a resolution will choose. Send either `reference` with `severity` " +
		"and `reason`, or `from` naming " +
		"a batch file, absolute or relative to the workspace root — not both. The content is required rather than " +
		"prompted for, because nothing here can ask. The marker is signed by its own digest, so it carries the " +
		"same irrevocability a signature does: keyless signing takes its identity from `identity_token` or from " +
		"the environment and refuses loudly when neither carries one, and `key_path` selects offline keyed signing " +
		"instead. A marker against a reference nothing published is refused rather than attached. The result names " +
		"every marker attached with its own digest.",
	inputSchema: `{
  "type": "object",
  "properties": {
    "reference": {
      "type": "string",
      "minLength": 1,
      "description": "The already-published reference to withdraw, a registry address rather than a path. Mutually exclusive with from."
    },
    "severity": {
      "type": "string",
      "minLength": 1,
      "description": "How strongly the withdrawal binds a consumer. Required alongside reference."
    },
    "reason": {
      "type": "string",
      "minLength": 1,
      "description": "Why the artifact is withdrawn. Required alongside reference."
    },
    "replacement": {
      "type": "string",
      "description": "Optional reference consumers should move to instead."
    },
    "message": {
      "type": "string",
      "description": "Optional additional text carried on the marker."
    },
    "from": {
      "type": "string",
      "minLength": 1,
      "description": "Path to a batch file of deprecation entries, absolute or relative to the workspace root. Mutually exclusive with reference."
    },
    "key_path": {
      "type": "string",
      "minLength": 1,
      "description": "Path to a private key for offline keyed signing of the marker. Absent selects keyless signing."
    },
    "identity_token": {
      "type": "string",
      "minLength": 1,
      "description": "Identity token value for keyless signing. Absent falls through to the environment; when nothing carries one the marking refuses rather than prompting."
    },
    "fulcio_url": {
      "type": "string",
      "description": "Certificate authority endpoint for keyless signing. Absent uses the configured or public-good default."
    },
    "rekor_url": {
      "type": "string",
      "description": "Transparency log endpoint for keyless signing. Absent uses the configured or public-good default."
    },
    "timestamp_url": {
      "type": "string",
      "description": "Timestamp authority endpoint for keyless signing. Absent uses the configured or public-good default."
    },
    "workspace_root": {
      "type": "string",
      "description": "Optional workspace root override. Defaults to discovery from the server's working directory."
    }
  },
  "anyOf": [
    {"required": ["reference", "severity", "reason"], "not": {"required": ["from"]}},
    {
      "required": ["from"],
      "not": {"anyOf": [
        {"required": ["reference"]},
        {"required": ["severity"]},
        {"required": ["reason"]},
        {"required": ["replacement"]},
        {"required": ["message"]}
      ]}
    }
  ]
}`,
	invoke: deprecateArtifactInvoke,
}

func deprecateArtifactInvoke(s *server, _ context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
	var a deprecateArtifactArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return invalidToolArguments(deprecateArtifactToolName, err)
		}
	}
	cfg, refusal := s.deprecateArtifactConfig(a)
	if refusal != "" {
		return errorResult(refusal)
	}
	payload, err := runDeprecateArtifact(cfg)
	if err != nil {
		return errorResult(explainToolError("The deprecation marking did not happen.", err, ""))
	}
	return textResult(renderDeprecateArtifactText(payload), payload)
}

// contentAlongsideBatch names the marker-content arguments a call sent that a
// batch file would override, in a fixed order so one call always draws one
// refusal. It returns the empty string when the call sent none.
func contentAlongsideBatch(a deprecateArtifactArgs) string {
	var sent []string
	for _, f := range []struct{ name, value string }{
		{"severity", a.Severity},
		{"reason", a.Reason},
		{"replacement", a.Replacement},
		{"message", a.Message},
	} {
		if strings.TrimSpace(f.value) != "" {
			sent = append(sent, "'"+f.name+"'")
		}
	}
	return strings.Join(sent, ", ")
}

// deprecateArtifactConfig projects the arguments onto the deprecation operation's
// input contract, returning the corrective text for anything that cannot be turned
// into one.
//
// The content check here is this surface's own, and it is what keeps the shared
// layer's terminal fork unreachable: with nobody watching there is nothing to
// prompt, so the call must carry what a person would have been asked for, and one
// that does not is refused naming the arguments it is missing.
func (s *server) deprecateArtifactConfig(a deprecateArtifactArgs) (publishops.DeprecateConfig, string) {
	reference := strings.TrimSpace(a.Reference)
	from := strings.TrimSpace(a.From)
	switch {
	case from != "" && reference != "":
		return publishops.DeprecateConfig{}, deprecateArtifactToolName +
			" takes 'from' or 'reference', not both: send a batch file or a single reference."
	// A batch file carries its own content per entry, so content sent alongside it
	// would be read by nothing. It is refused rather than dropped: an argument
	// silently ignored reads to a caller as an argument that was honoured, and on a
	// withdrawal the caller would be wrong about what every consumer is now told.
	case from != "" && contentAlongsideBatch(a) != "":
		return publishops.DeprecateConfig{}, fmt.Sprintf(
			"%s reads its content from the batch file, so %s would be ignored: put it in the file's own entry, "+
				"or send a single 'reference' with its content instead.",
			deprecateArtifactToolName, contentAlongsideBatch(a))
	case from == "" && reference == "":
		return publishops.DeprecateConfig{}, deprecateArtifactToolName +
			" requires a 'reference' — the already-published artifact to withdraw — or 'from' naming a batch file."
	case from == "" && (strings.TrimSpace(a.Severity) == "" || strings.TrimSpace(a.Reason) == ""):
		return publishops.DeprecateConfig{}, deprecateArtifactToolName +
			" requires 'severity' and 'reason' alongside a 'reference'; nothing here can ask for them, so send both" +
			" or send 'from' naming a batch file."
	}
	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, nil)
	if err != nil {
		return publishops.DeprecateConfig{}, explainToolError(
			"Could not resolve the workspace to attach a deprecation marker from.", err, "")
	}
	batchPath := ""
	if from != "" {
		batchPath = tw.absWorkspacePath(from)
	}
	return publishops.DeprecateConfig{
		Reference:    reference,
		Severity:     strings.TrimSpace(a.Severity),
		Reason:       strings.TrimSpace(a.Reason),
		Replacement:  a.Replacement,
		Message:      a.Message,
		From:         batchPath,
		KeyPath:      a.KeyPath,
		FulcioURL:    a.FulcioURL,
		RekorURL:     a.RekorURL,
		TimestampURL: a.TimestampURL,
		IDToken:      a.IdentityToken,
		Workspace:    tw.rw,
		Out:          io.Discard,
	}, ""
}

// runDeprecateArtifact runs the deprecation and shapes the answer.
func runDeprecateArtifact(cfg publishops.DeprecateConfig) (deprecateArtifactPayload, error) {
	res, err := publishops.Deprecate(cfg)
	if err != nil {
		return deprecateArtifactPayload{}, err
	}
	marked := make([]deprecationMarked, 0, len(res.Marked))
	for _, m := range res.Marked {
		marked = append(marked, deprecationMarked{
			Reference: m.Reference,
			Severity:  m.Severity,
			IssuedOn:  m.IssuedOn,
			Digest:    m.Digest,
		})
	}
	host := cfg.Reference
	if host == "" && len(marked) > 0 {
		host = marked[0].Reference
	}
	return deprecateArtifactPayload{Marked: marked, Reach: reachOf(host, len(marked) > 0)}, nil
}

// renderDeprecateArtifactText renders the human-readable summary of a deprecation
// marking.
func renderDeprecateArtifactText(p deprecateArtifactPayload) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Attached %d deprecation marker(s); every consumer resolving these references now sees the withdrawal:\n",
		len(p.Marked))
	for _, m := range p.Marked {
		fmt.Fprintf(&b, "  %s (%s, issued %s) -> %s\n", m.Reference, m.Severity, m.IssuedOn, m.Digest)
	}
	return strings.TrimRight(b.String(), "\n")
}
