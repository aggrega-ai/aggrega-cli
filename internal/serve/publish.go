// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"encoding/json"
	"io"

	"github.com/aggrega-ai/aggrega-cli/internal/cacheops"
	"github.com/aggrega-ai/aggrega-cli/internal/publishops"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
)

// The four publication methods: publishing a catalog entity, publishing a
// standalone data artifact, signing an already-published artifact, and attaching
// a deprecation marker.
//
// # Why a session carries them at all
//
// They write, and what they write is not this machine's: published bytes are what
// every consumer of that registry resolves, and a signature cannot be recalled
// from anyone who already fetched it. That is the widest reach anything here has.
//
// They are carried on the same rule every other writing method here runs under,
// and the rule is about WHEN an operation runs rather than about which surface
// runs it: a registry-reaching or state-writing operation runs on EXPLICIT
// INVOCATION and never as a side effect of validating or inspecting a workspace. A
// caller that sent one of these has asked for the publication, exactly as a caller
// sending a build has asked for artifacts. What stays out of bounds is a write
// performed while somebody was typing, and none of these is that.
//
// The rule is stated over the shared layer's own invocation path — see
// [publishops] — rather than restated per method, because an enumeration of the
// methods it covers is what goes stale.
//
// # What every answer states, and why it is on every answer
//
// A reach. A session is opened over a workspace, so an answer that said nothing
// would be read as being about that workspace, and for a publication that reading
// is wrong by the widest margin of anything here. So each response names the
// registry host it addressed and whether the call reached beyond this machine —
// which a dry run did not, and everything else did.
//
// # It never prompts, and it never hangs
//
// Nobody is watching a protocol stream, so the shared layer is given no way to ask
// a person anything: neither the interactive identity rung nor the deprecation
// content prompt is supplied, which is what makes them absent rather than merely
// unused. Identity resolves from a credential the request supplied or one the
// environment already carries, and when neither yields anything the operation
// REFUSES, naming what to supply. A hang would be the observable failure
// otherwise, and a caller on this surface has no way to answer a prompt.
//
// # No request names a key
//
// The command line takes a path to a held signing key. A request here does not,
// and that is an addressing decision rather than an omission: a session locates
// what it reads inside the workspace it opened — the ground on which this table
// declines an operation whose subject is a file the request names by a filesystem
// path of its own — and a private key is never kept inside a workspace, so such a
// field could only ever name a path outside the root. Keyed signing on this
// surface resolves its key from the workspace's own configured signing block,
// which is where a key path belongs.
//
// Every path a request DOES name — the entity document, its executable, the
// artifact file, a batch file — is relative to the workspace root and is refused
// if it is absolute or climbs out of it, under the same contract every
// path-naming parameter here carries.

// publicationReach states how far one publication reached.
//
// Registry is the host addressed. BeyondThisMachine is derived from what the
// operation actually did rather than from what the request asked for, so a
// response cannot describe a dry run as a publication or the reverse: it is true
// exactly when bytes, a signature or a marker landed somewhere other people
// resolve from.
type publicationReach struct {
	Registry          string `json:"registry"`
	BeyondThisMachine bool   `json:"beyondThisMachine"`
}

// reachOf builds the reach statement for a reference the operation addressed.
func reachOf(reference string, landed bool) publicationReach {
	return publicationReach{Registry: registryhost.Of(reference), BeyondThisMachine: landed}
}

// publishEntityParams is what aggrega/publishEntity takes.
//
// entity is required and is a path relative to the workspace root naming the
// entity document to publish, under the same contract every path-naming parameter
// here carries. plugin is the same kind of path and is required only for a Plugin,
// whose executable is published inside the entity's own manifest.
//
// sign is a pointer so an absent key and an explicit false are distinguishable: an
// absent one follows the destination registry's own policy, which is the command
// line's behaviour when its flag is not passed, while an explicit value is the
// caller's choice. Neither can relax the refusal — a registry or a workspace
// policy that requires a signature refuses an unsigned publication whatever this
// field says, and there is no field that bypasses it.
//
// identityToken is the supplied credential for keyless signing. It is a value
// rather than a path, which is what makes it the channel this surface accepts;
// leaving it out falls through to whatever the environment already carries, and
// when nothing does the publication refuses rather than asking anybody.
type publishEntityParams struct {
	Entity        string `json:"entity"`
	Registry      string `json:"registry,omitempty"`
	DryRun        bool   `json:"dryRun,omitempty"`
	Sign          *bool  `json:"sign,omitempty"`
	Plugin        string `json:"plugin,omitempty"`
	FulcioURL     string `json:"fulcioUrl,omitempty"`
	RekorURL      string `json:"rekorUrl,omitempty"`
	TimestampURL  string `json:"timestampUrl,omitempty"`
	IdentityToken string `json:"identityToken,omitempty"`
}

// publishEntityResult is the aggrega/publishEntity response.
//
// It answers with the facts rather than with the status lines a terminal reads:
// where the entity landed, what it landed as, and what the destination now
// resolves that tag to. Digest is empty on a dry run, which stops before a digest
// is computed, and the two booleans say which path ran — dryRun published nothing,
// and alreadyPublished found the tag already pointing at byte-identical content
// and landed nothing.
//
// Each size is carried twice: as the byte count a caller computes with, and as
// the same quantity written in the binary-unit vocabulary every surface spells
// sizes in, so a caller displaying it does not have to reproduce the rounding
// rule. PluginSizeText is absent exactly when PluginSize is, so the pair is never
// half-present.
type publishEntityResult struct {
	Reference        string           `json:"reference"`
	Tag              string           `json:"tag"`
	Kind             string           `json:"kind"`
	MediaType        string           `json:"mediaType"`
	Size             int              `json:"size"`
	SizeText         string           `json:"sizeText"`
	PluginSize       int              `json:"pluginSize,omitempty"`
	PluginSizeText   string           `json:"pluginSizeText,omitempty"`
	Digest           string           `json:"digest"`
	Signed           bool             `json:"signed"`
	AlreadyPublished bool             `json:"alreadyPublished"`
	DryRun           bool             `json:"dryRun"`
	Reach            publicationReach `json:"reach"`
}

// publishArtifactParams is what aggrega/publishArtifact takes: the file to
// publish, relative to the workspace root, and the name and version tag it is
// published under. mediaType is optional and falls through to the
// documentation-bundle media type, matching the command line's default.
type publishArtifactParams struct {
	File      string `json:"file"`
	Name      string `json:"name"`
	Tag       string `json:"tag"`
	MediaType string `json:"mediaType,omitempty"`
	Registry  string `json:"registry,omitempty"`
	DryRun    bool   `json:"dryRun,omitempty"`
}

// publishArtifactResult is the aggrega/publishArtifact response, carrying the same
// facts and the same reach a catalog-entity publication carries.
type publishArtifactResult struct {
	Reference        string           `json:"reference"`
	Tag              string           `json:"tag"`
	MediaType        string           `json:"mediaType"`
	Size             int              `json:"size"`
	SizeText         string           `json:"sizeText"`
	Digest           string           `json:"digest"`
	AlreadyPublished bool             `json:"alreadyPublished"`
	DryRun           bool             `json:"dryRun"`
	Reach            publicationReach `json:"reach"`
}

// signArtifactsParams is what aggrega/signArtifacts takes: the already-published
// references to sign, and the keyless inputs identity resolution may need.
//
// The references are addresses in a registry rather than paths, so they carry no
// workspace-relative contract. One authentication serves the whole batch.
type signArtifactsParams struct {
	References    []string `json:"references"`
	FulcioURL     string   `json:"fulcioUrl,omitempty"`
	RekorURL      string   `json:"rekorUrl,omitempty"`
	TimestampURL  string   `json:"timestampUrl,omitempty"`
	IdentityToken string   `json:"identityToken,omitempty"`
}

// signArtifactsResult is the aggrega/signArtifacts response: every reference
// signed, with the published digest its signature commits to.
//
// It reports SUCCESSES only, and a batch in which any reference failed is answered
// as a failure naming each one rather than as a result with an empty list. A
// partial batch reported as a result is how a caller comes to read a refusal as a
// pass, and signing is the operation where that reading costs most.
type signArtifactsResult struct {
	Signed []signedArtifact `json:"signed"`
	Reach  publicationReach `json:"reach"`
}

// signedArtifact is one signed reference and the digest its signature commits to.
type signedArtifact struct {
	Reference string `json:"reference"`
	Digest    string `json:"digest"`
}

// deprecateArtifactParams is what aggrega/deprecateArtifact takes.
//
// Two shapes, and exactly one of them: a single reference with its severity and
// reason, or `from` naming a batch file relative to the workspace root. The
// content fields are required in the single shape — nobody is watching, so there
// is nothing to prompt, and a missing one is refused naming what to send.
type deprecateArtifactParams struct {
	Reference     string `json:"reference,omitempty"`
	Severity      string `json:"severity,omitempty"`
	Reason        string `json:"reason,omitempty"`
	Replacement   string `json:"replacement,omitempty"`
	Message       string `json:"message,omitempty"`
	From          string `json:"from,omitempty"`
	FulcioURL     string `json:"fulcioUrl,omitempty"`
	RekorURL      string `json:"rekorUrl,omitempty"`
	TimestampURL  string `json:"timestampUrl,omitempty"`
	IdentityToken string `json:"identityToken,omitempty"`
}

// deprecateArtifactResult is the aggrega/deprecateArtifact response: every marker
// attached, with the marker's own manifest digest.
//
// The digest is the marker's identity — a marker is attached as an untagged
// referrer of the artifact it withdraws, so nothing else addresses it.
type deprecateArtifactResult struct {
	Marked []deprecationMarked `json:"marked"`
	Reach  publicationReach    `json:"reach"`
}

// deprecationMarked is one attached marker.
type deprecationMarked struct {
	Reference string `json:"reference"`
	Severity  string `json:"severity"`
	IssuedOn  string `json:"issuedOn"`
	Digest    string `json:"digest"`
}

// dispatchPublishEntity publishes a catalog entity to a registry.
//
// 🔴 It WRITES, and what it writes reaches beyond this machine: the bytes it lands
// are what every consumer of that registry resolves, and re-publishing over them is
// refused rather than offered, so a publication is not something a later call takes
// back. Where a signature is attached, it cannot be recalled from anyone who has
// already fetched it.
//
// It is carried here on the rule stated over this file: a caller that sent this has
// asked for the publication. What is refused is a write performed while validating
// or inspecting, and this is not that.
//
// Nothing is prompted for. A keyless signature needs an identity, and the identity
// comes from the request or from the environment; when neither carries one the
// publication refuses, naming what to supply, before any bytes reach the registry.
func (s *Server) dispatchPublishEntity(ctx context.Context, req *jsonRPCRequest) error {
	var params publishEntityParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
		}
	}
	cfg, paramErr := s.publishEntityConfig(params)
	if paramErr != nil {
		return s.writeErrorEnvelope(ctx, req.ID, paramErr)
	}
	result, errEnvelope := runPublishEntity(cfg)
	if ctx.Err() != nil {
		return nil
	}
	if errEnvelope != nil {
		return s.writeErrorEnvelope(ctx, req.ID, errEnvelope)
	}
	return s.writeResult(ctx, req.ID, result)
}

// publishEntityConfig projects the request onto the publication's input contract,
// refusing everything that cannot be turned into one.
//
// It is where this surface's own narrowings live: the workspace-relative path
// contract on the two paths a request may name, the destination check, and the
// deliberate absence of the interactive rung. The credential seams are left NIL,
// and that is the mechanism rather than an omission — a nil token getter means the
// shared identity ladder has no rung that needs a person, so it refuses instead of
// blocking on an answer nobody can give.
func (s *Server) publishEntityConfig(params publishEntityParams) (publishops.PushConfig, *jsonRPCError) {
	if s.resolvedWorkspace == nil {
		return publishops.PushConfig{}, &jsonRPCError{
			Code:    jsonRPCInternalError,
			Message: "the daemon holds no resolved workspace to publish from",
		}
	}
	entityPath, paramErr := resolveWorkspacePathParam("entity", "an entity document",
		"the request named no entity", s.workspaceRoot, params.Entity)
	if paramErr != nil {
		return publishops.PushConfig{}, paramErr
	}
	pluginPath := ""
	if params.Plugin != "" {
		resolved, perr := resolveWorkspacePathParam("plugin", "a plugin executable",
			"the request named no plugin executable", s.workspaceRoot, params.Plugin)
		if perr != nil {
			return publishops.PushConfig{}, perr
		}
		pluginPath = resolved
	}
	if jerr := s.requireDestination(params.Registry); jerr != nil {
		return publishops.PushConfig{}, jerr
	}
	return publishops.PushConfig{
		EntityPath:   entityPath,
		Registry:     params.Registry,
		DryRun:       params.DryRun,
		Sign:         params.Sign,
		FulcioURL:    params.FulcioURL,
		RekorURL:     params.RekorURL,
		TimestampURL: params.TimestampURL,
		IDToken:      params.IdentityToken,
		PluginPath:   pluginPath,
		Workspace:    s.resolvedWorkspace,
		Out:          io.Discard,
	}, nil
}

// runPublishEntity runs the publication and shapes the answer.
//
// The operation's status lines are discarded rather than folded into the response.
// They are prose written for a person watching a terminal, and this surface answers
// with the facts themselves.
func runPublishEntity(cfg publishops.PushConfig) (publishEntityResult, *jsonRPCError) {
	res, err := publishops.Push(cfg)
	if err != nil {
		return publishEntityResult{}, &jsonRPCError{
			Code:    jsonRPCInternalError,
			Message: err.Error(),
			cause:   err,
		}
	}
	out := publishEntityResult{
		Reference:        res.Reference,
		Tag:              res.Tag,
		Kind:             res.Kind,
		MediaType:        res.MediaType,
		Size:             res.Size,
		SizeText:         cacheops.HumanSize(int64(res.Size)),
		PluginSize:       res.PluginSize,
		Digest:           res.Digest,
		Signed:           res.Signed,
		AlreadyPublished: res.AlreadyPublished,
		DryRun:           res.DryRun,
		Reach:            reachOf(res.Reference, !res.DryRun),
	}
	// The plugin size is carried only for a plugin entity, so its rendering is
	// held to the same presence: a caller never reads a rendered size with no
	// number beside it, in either direction.
	if res.PluginSize != 0 {
		out.PluginSizeText = cacheops.HumanSize(int64(res.PluginSize))
	}
	return out, nil
}

// dispatchPublishArtifact publishes a standalone data artifact to a registry.
//
// 🔴 It WRITES, and it reaches beyond this machine for the same reason the
// catalog-entity publication does. The bytes are opaque to the compiler — no entity
// envelope is parsed and no kind is dispatched on — but where they land and what
// resolving that tag returns are exactly as public.
//
// It signs nothing: a published artifact is signed by reference with the signing
// method, which needs only the reference this answer reports.
func (s *Server) dispatchPublishArtifact(ctx context.Context, req *jsonRPCRequest) error {
	var params publishArtifactParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
		}
	}
	cfg, paramErr := s.publishArtifactConfig(params)
	if paramErr != nil {
		return s.writeErrorEnvelope(ctx, req.ID, paramErr)
	}
	result, errEnvelope := runPublishArtifact(cfg)
	if ctx.Err() != nil {
		return nil
	}
	if errEnvelope != nil {
		return s.writeErrorEnvelope(ctx, req.ID, errEnvelope)
	}
	return s.writeResult(ctx, req.ID, result)
}

// publishArtifactConfig projects the request onto the data-artifact publication's
// input contract, refusing everything that cannot be turned into one.
func (s *Server) publishArtifactConfig(params publishArtifactParams) (publishops.PushArtifactConfig, *jsonRPCError) {
	if s.resolvedWorkspace == nil {
		return publishops.PushArtifactConfig{}, &jsonRPCError{
			Code:    jsonRPCInternalError,
			Message: "the daemon holds no resolved workspace to publish from",
		}
	}
	filePath, paramErr := resolveWorkspacePathParam("file", "the artifact file",
		"the request named no file", s.workspaceRoot, params.File)
	if paramErr != nil {
		return publishops.PushArtifactConfig{}, paramErr
	}
	if params.Name == "" {
		return publishops.PushArtifactConfig{}, invalidPublicationParam(
			"publishArtifact: name is required (the artifact name it is published under)")
	}
	if params.Tag == "" {
		return publishops.PushArtifactConfig{}, invalidPublicationParam(
			"publishArtifact: tag is required (the version the artifact is published under)")
	}
	if jerr := s.requireDestination(params.Registry); jerr != nil {
		return publishops.PushArtifactConfig{}, jerr
	}
	return publishops.PushArtifactConfig{
		FilePath:  filePath,
		Name:      params.Name,
		Tag:       params.Tag,
		MediaType: params.MediaType,
		Registry:  params.Registry,
		DryRun:    params.DryRun,
		Workspace: s.resolvedWorkspace,
		Out:       io.Discard,
	}, nil
}

// runPublishArtifact runs the data-artifact publication and shapes the answer.
func runPublishArtifact(cfg publishops.PushArtifactConfig) (publishArtifactResult, *jsonRPCError) {
	res, err := publishops.PushArtifact(cfg)
	if err != nil {
		return publishArtifactResult{}, &jsonRPCError{
			Code:    jsonRPCInternalError,
			Message: err.Error(),
			cause:   err,
		}
	}
	return publishArtifactResult{
		Reference:        res.Reference,
		Tag:              res.Tag,
		MediaType:        res.MediaType,
		Size:             res.Size,
		SizeText:         cacheops.HumanSize(int64(res.Size)),
		Digest:           res.Digest,
		AlreadyPublished: res.AlreadyPublished,
		DryRun:           res.DryRun,
		Reach:            reachOf(res.Reference, !res.DryRun),
	}, nil
}

// dispatchSignArtifacts signs already-published artifacts and attaches each
// signature.
//
// 🔴 It WRITES, and a signature is the one thing here that cannot be taken back at
// all: the artifact it vouches for is untouched — each signature is a sibling
// manifest, so signing is immutability-safe — but anyone who fetches the artifact
// afterwards fetches the signature with it.
//
// One authentication serves the whole batch, and nothing is prompted for: the
// identity is resolved once, from the request or from the environment, before any
// reference is signed, so a batch with no usable credential refuses at the start
// rather than partway through.
func (s *Server) dispatchSignArtifacts(ctx context.Context, req *jsonRPCRequest) error {
	var params signArtifactsParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
		}
	}
	cfg, paramErr := s.signArtifactsConfig(params)
	if paramErr != nil {
		return s.writeErrorEnvelope(ctx, req.ID, paramErr)
	}
	result, errEnvelope := runSignArtifacts(cfg)
	if ctx.Err() != nil {
		return nil
	}
	if errEnvelope != nil {
		return s.writeErrorEnvelope(ctx, req.ID, errEnvelope)
	}
	return s.writeResult(ctx, req.ID, result)
}

// signArtifactsConfig projects the request onto the signing operation's input
// contract.
func (s *Server) signArtifactsConfig(params signArtifactsParams) (publishops.SignConfig, *jsonRPCError) {
	if s.resolvedWorkspace == nil {
		return publishops.SignConfig{}, &jsonRPCError{
			Code:    jsonRPCInternalError,
			Message: "the daemon holds no resolved workspace to sign against",
		}
	}
	if len(params.References) == 0 {
		return publishops.SignConfig{}, invalidPublicationParam(
			"signArtifacts: references is required (one or more already-published references to sign)")
	}
	for _, ref := range params.References {
		if ref == "" {
			return publishops.SignConfig{}, invalidPublicationParam(
				"signArtifacts: references carries an empty entry; every entry names one already-published artifact")
		}
	}
	return publishops.SignConfig{
		References:   params.References,
		FulcioURL:    params.FulcioURL,
		RekorURL:     params.RekorURL,
		TimestampURL: params.TimestampURL,
		IDToken:      params.IdentityToken,
		Workspace:    s.resolvedWorkspace,
		Out:          io.Discard,
	}, nil
}

// runSignArtifacts runs the signing batch and shapes the answer.
func runSignArtifacts(cfg publishops.SignConfig) (signArtifactsResult, *jsonRPCError) {
	res, err := publishops.Sign(cfg)
	if err != nil {
		return signArtifactsResult{}, &jsonRPCError{
			Code:    jsonRPCInternalError,
			Message: err.Error(),
			cause:   err,
		}
	}
	// Every collection is an array rather than a null, so a client iterates
	// unconditionally.
	signed := make([]signedArtifact, 0, len(res.Signed))
	for _, a := range res.Signed {
		signed = append(signed, signedArtifact{Reference: a.Reference, Digest: a.Digest})
	}
	host := ""
	if len(cfg.References) > 0 {
		host = cfg.References[0]
	}
	return signArtifactsResult{Signed: signed, Reach: reachOf(host, len(signed) > 0)}, nil
}

// dispatchDeprecateArtifact attaches a signed deprecation marker to an
// already-published artifact.
//
// 🔴 It WRITES, and it is the sharpest of these to read wrongly: the artifact it
// withdraws is untouched, but every consumer resolving that reference afterwards
// sees the withdrawal, and a retraction this workspace trusts takes the version out
// of what a resolution will choose. The marker is signed by its own digest, so it
// carries the same irrevocability a signature does.
//
// Nothing is prompted for, on either of the two forks that could ask. The identity
// resolves from the request or the environment and refuses otherwise; and the
// content a terminal would have been asked for is required here, so a request
// missing it is refused naming what to send rather than blocked on an answer
// nobody can give.
func (s *Server) dispatchDeprecateArtifact(ctx context.Context, req *jsonRPCRequest) error {
	var params deprecateArtifactParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
		}
	}
	cfg, paramErr := s.deprecateArtifactConfig(params)
	if paramErr != nil {
		return s.writeErrorEnvelope(ctx, req.ID, paramErr)
	}
	result, errEnvelope := runDeprecateArtifact(cfg)
	if ctx.Err() != nil {
		return nil
	}
	if errEnvelope != nil {
		return s.writeErrorEnvelope(ctx, req.ID, errEnvelope)
	}
	return s.writeResult(ctx, req.ID, result)
}

// deprecateArtifactConfig projects the request onto the deprecation operation's
// input contract.
//
// The content check here is this surface's own, and it is what keeps the shared
// layer's terminal fork unreachable: with nobody watching there is nothing to
// prompt, so the request must carry what a person would have been asked for, and a
// request that does not is refused naming the fields it is missing.
func (s *Server) deprecateArtifactConfig(params deprecateArtifactParams) (publishops.DeprecateConfig, *jsonRPCError) {
	if s.resolvedWorkspace == nil {
		return publishops.DeprecateConfig{}, &jsonRPCError{
			Code:    jsonRPCInternalError,
			Message: "the daemon holds no resolved workspace to attach a deprecation marker from",
		}
	}
	batchPath := ""
	if params.From != "" {
		if params.Reference != "" {
			return publishops.DeprecateConfig{}, invalidPublicationParam(
				"deprecateArtifact: from and reference are alternatives; send a batch file or a single reference, not both")
		}
		resolved, perr := resolveWorkspacePathParam("from", "a batch file",
			"the request named no batch file", s.workspaceRoot, params.From)
		if perr != nil {
			return publishops.DeprecateConfig{}, perr
		}
		batchPath = resolved
	} else {
		if params.Reference == "" {
			return publishops.DeprecateConfig{}, invalidPublicationParam(
				"deprecateArtifact: reference is required (the already-published artifact to withdraw), or send from naming a batch file")
		}
		// Nobody is watching, so the content a terminal would have been asked for is
		// required rather than prompted.
		if params.Severity == "" || params.Reason == "" {
			return publishops.DeprecateConfig{}, invalidPublicationParam(
				"deprecateArtifact: severity and reason are required; nothing here can ask for them, so send both or send from naming a batch file")
		}
	}
	return publishops.DeprecateConfig{
		Reference:    params.Reference,
		Severity:     params.Severity,
		Reason:       params.Reason,
		Replacement:  params.Replacement,
		Message:      params.Message,
		From:         batchPath,
		FulcioURL:    params.FulcioURL,
		RekorURL:     params.RekorURL,
		TimestampURL: params.TimestampURL,
		IDToken:      params.IdentityToken,
		Workspace:    s.resolvedWorkspace,
		Out:          io.Discard,
	}, nil
}

// runDeprecateArtifact runs the deprecation and shapes the answer.
func runDeprecateArtifact(cfg publishops.DeprecateConfig) (deprecateArtifactResult, *jsonRPCError) {
	res, err := publishops.Deprecate(cfg)
	if err != nil {
		return deprecateArtifactResult{}, &jsonRPCError{
			Code:    jsonRPCInternalError,
			Message: err.Error(),
			cause:   err,
		}
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
	return deprecateArtifactResult{Marked: marked, Reach: reachOf(host, len(marked) > 0)}, nil
}

// requireDestination refuses a publication that has nowhere to go, in this
// surface's own vocabulary.
//
// The shared layer refuses the same condition, and it refuses it naming a command
// line flag, which is correct on the surface a person types at and wrong on this
// one. Catching it here is what keeps a caller from being told to pass an option it
// has no way to pass.
func (s *Server) requireDestination(requested string) *jsonRPCError {
	if requested != "" || s.resolvedWorkspace.Config().DefaultRegistry != "" {
		return nil
	}
	return invalidPublicationParam("no destination registry: send registry, or set the workspace's " +
		"default registry in its configuration")
}

// invalidPublicationParam builds the refusal a malformed publication request
// draws. Each message names what to send rather than only what was wrong, so a
// client author reading one learns the contract without consulting anything else.
func invalidPublicationParam(fault string) *jsonRPCError {
	return &jsonRPCError{Code: jsonRPCInvalidParams, Message: "invalid params: " + fault}
}
