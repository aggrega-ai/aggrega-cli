// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// setActiveReleaseParams is what aggrega/setActiveRelease takes: the Release to
// record, under the contract every Release-naming parameter here carries — a path
// relative to the workspace root, naming a Release inside it.
type setActiveReleaseParams struct {
	Release string `json:"release"`
}

// dispatchSetActiveRelease records a Release in the workspace's active-Release
// marker, the operator's own declaration the disambiguation cascade reads after
// the configuration's authoring Release, and answers what aggrega/activeRelease
// answers once it is written.
//
// It is the writing pair of that reader, and the only writer of the marker the
// product has: a client recording its selection asks for the write here rather
// than writing the file itself, so the one judgement every write the product
// performs inside the workspace is held to holds this one too. A marker written
// by hand is still read; this method is how a client writes one without deciding
// for itself where the path leads.
//
// 🔴 It WRITES, and it is admitted on the rule every writing method here runs
// under: a caller that sent it has asked for the change. What it writes is one
// file inside the workspace, and what the file names is held twice, each time by
// the judgement that owns the question. The Release named is resolved as every
// Release a request names is, so a value leading out of the workspace — absolute,
// climbing out, or through a link the configuration does not reach — is refused
// as an inadmissible parameter, naming the value. Where the marker is written is
// writeActiveReleaseMarker's, and a write it refuses is answered with the
// condition the refusal names, its short code ahead of the sentence.
//
// The answer is the reader's, computed after the write rather than assumed from
// the request, because the two can differ: a configured authoring Release
// outranks the marker, and a marker naming a Release that does not load is passed
// over. A client therefore learns in one exchange which Release the workspace now
// settles on, and why where it settles on none.
func (s *Server) dispatchSetActiveRelease(ctx context.Context, req *jsonRPCRequest) error {
	var params setActiveReleaseParams
	if err := decodeParams(req, &params); err != nil {
		return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "invalid params: ", err)
	}
	release, errEnvelope := resolveReleaseParam(s.workspaceRoot, s.wsConfig, params.Release)
	if errEnvelope != nil {
		return s.writeErrorEnvelope(ctx, req.ID, errEnvelope)
	}
	if err := writeActiveReleaseMarker(s.workspaceRoot, release); err != nil {
		// A write the boundary refuses is answered as the configuration's own
		// writes answer the same refusal: the request asked for a write the
		// workspace's layout does not admit. Anything else is a failure while doing
		// the work — a `.aggrega` that is a file, a directory nobody may write.
		code := jsonRPCInternalError
		if errors.Is(err, writescope.ErrWriteLeadsOutOfWorkspace()) {
			code = jsonRPCInvalidParams
		}
		return s.writeRefusal(ctx, req.ID, code, "", err)
	}
	return s.writeResult(ctx, req.ID, s.activeReleaseAnswer(ctx))
}

// writeActiveReleaseMarker records release, the absolute path of a Release inside
// workspaceRoot, in the workspace's active-Release marker: one line naming it
// relative to the root, the form the cascade reads.
//
// The marker and the directory holding it are paths the product works out inside
// the workspace, so each is held to the workspace root by the one write judgement
// before anything is created: a `.aggrega` directory that is a link leading out of
// the workspace, a marker that is one, and a marker that is a link leading nowhere
// whose far end lies outside — which a write would create — all halt with that
// judgement's condition, and nothing is written. The directory is judged as well as
// the file because a directory link leading nowhere passes the file's judgement,
// which is taken at the root beneath it; judged itself, it is where creating the
// directory would land that is held.
func writeActiveReleaseMarker(workspaceRoot, release string) error {
	marker := filepath.Join(workspaceRoot, markerFilePath)
	dir := filepath.Dir(marker)
	for _, path := range []string{dir, marker} {
		if err := writescope.CheckWrite(workspaceRoot, path); err != nil {
			return err
		}
	}
	rel, err := filepath.Rel(workspaceRoot, release)
	if err != nil {
		return fmt.Errorf("record %s in the active-Release marker: %w", release, err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create the directory of the active-Release marker: %w", err)
	}
	if err := os.WriteFile(marker, []byte(filepath.ToSlash(rel)+"\n"), 0o644); err != nil {
		return fmt.Errorf("write the active-Release marker: %w", err)
	}
	return nil
}
