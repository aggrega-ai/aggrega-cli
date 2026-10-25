// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"github.com/aggrega-ai/aggrega-cli/internal/buildops"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
)

// policyInputDir resolves the directory a build request names for the
// governance payloads: empty where it names none, and otherwise the path inside
// the workspace this session opened, judged by the rule every destination on this
// surface takes, so a value that is absolute or climbs out of the workspace is
// refused before anything runs. method names the request in the refusal.
func (s *Server) policyInputDir(method, raw string) (string, *jsonRPCError) {
	if raw == "" {
		return "", nil
	}
	dir, err := s.workspacePath(method+": policyInput", raw)
	if err != nil {
		return "", &jsonRPCError{
			Code:    jsonRPCInvalidParams,
			Message: "invalid params: " + err.Error(),
			cause:   err,
		}
	}
	return dir, nil
}

// newPolicyInputCollector returns the collector a render records the governance
// payloads into, or nil — which asks for none — where no directory was named.
func newPolicyInputCollector(dir string) *pipeline.PolicyInputs {
	if dir == "" {
		return nil
	}
	return pipeline.NewPolicyInputs()
}

// writePolicyInputs writes the payloads a render recorded into dir through the
// write the command line's build uses, and returns that write's account as
// notices. Nothing was asked for where dir is empty, and nothing is written.
//
// The account carries no notice class: it reports a write this request made
// rather than an advisory any surface raised, and a class this daemon does not
// name is answered with the empty one, which a client renders as the bare text.
func (s *Server) writePolicyInputs(dir string, collected *pipeline.PolicyInputs) ([]buildNotice, *jsonRPCError) {
	if dir == "" {
		return nil, nil
	}
	lines, err := buildops.WritePolicyInputs(s.workspaceRoot, dir, collected)
	if err != nil {
		return nil, &jsonRPCError{
			Code:    errEntityNotResolvable,
			Message: err.Error(),
			cause:   err,
		}
	}
	notices := make([]buildNotice, 0, len(lines))
	for _, line := range lines {
		notices = append(notices, buildNotice{Text: line})
	}
	return notices, nil
}
