// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
)

// The conditions on which a validation sent without the document's content has
// no file to read, so nothing is validated.
//
// They are unexported values rather than registered halting conditions, as the
// insertion point's are: none halts a build. Each says that one request's
// arguments name no document the daemon reads, and travels as the text of an
// invalid-params refusal.
var (
	errValidateNoDocument = errors.New("no document was named and no content was sent, so there is " +
		"nothing to validate")

	errValidateDocumentOutsideWorkspace = errors.New("the document named is not inside the workspace, and a " +
		"validation sent without content reads only a document the workspace carries")

	errValidateDocumentLeadsOut = errors.New("the document named leads outside the workspace through a link, " +
		"and a validation sent without content reads only a document the workspace carries")
)

// validationContent is the text a validation checks: the content the request
// carried, or — where it carried none — the bytes of the file it named, as they
// are on disk.
//
// The disk read is held to what the workspace carries, by the judgements every
// path a request names is held to: the canonical spelling the file was resolved
// to (canonicalFile), containment in the root as spelled, and containment with
// its links resolved (leadsOutOfWorkspace). A request that carried its content is
// held to none of them, because nothing is read: the buffer it sent is the
// document, wherever the file it names sits.
//
// file is the request's file in that canonical spelling.
func (s *Server) validationContent(params validateParams, file string) ([]byte, *jsonRPCError) {
	if !params.contentAbsent {
		return []byte(params.Content), nil
	}
	refuse := func(err error) *jsonRPCError {
		return &jsonRPCError{Code: jsonRPCInvalidParams, Message: "invalid params: " + err.Error(), cause: err}
	}
	if file == "" {
		return nil, refuse(errValidateNoDocument)
	}
	rel, inside := pathscope.Relative(s.workspaceRoot, file)
	if !inside {
		return nil, refuse(errValidateDocumentOutsideWorkspace)
	}
	abs := filepath.Join(s.workspaceRoot, rel)
	if leadsOutOfWorkspace(s.workspaceRoot, abs, nil) {
		return nil, refuse(errValidateDocumentLeadsOut)
	}
	content, err := os.ReadFile(abs)
	if err != nil {
		return nil, refuse(fmt.Errorf("the document named could not be read: %w", err))
	}
	return content, nil
}
