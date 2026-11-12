// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package netrc

import (
	"fmt"
	"strconv"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// ErrNetrcFileUnreadable reports that the netrc file a fetch reads its credentials
// from exists and could not be used: it could not be read, or it does not follow
// the netrc grammar. The diagnostic names the file, and the line where the
// grammar failed, and never anything the file contains.
func ErrNetrcFileUnreadable() error { return errNetrcFileUnreadable }

var errNetrcFileUnreadable = cerrors.Register("netrc-file-unreadable", cerrors.Meta{
	ShortCode: "E0360",
	Message:   "the netrc file could not be read",
	RulePlace: cerrors.NotARule("A netrc file the program reads for source credentials could not be read, or does " +
		"not follow the netrc grammar. The file is not an authored document and no evaluator judges it: what this " +
		"reports is a read of the user's own credential file that could not yield credentials."),
})

// ErrSourceURLCarriesCredential reports a Recipe or FeatureRecipe whose source
// URL carries a credential of its own — a user, or a user and password, written
// before the host ([URLCredential] decides which). Such a URL is refused as the
// document is read, before anything is fetched: the credential would be sent, but
// it would also be written into every lockfile and cache record that keeps the
// URL as authored, and into the document itself, which is committed and may be
// published. The credential belongs in the netrc file instead.
func ErrSourceURLCarriesCredential() error { return errSourceURLCarriesCredential }

var errSourceURLCarriesCredential = cerrors.Register("source-url-carries-credential", cerrors.Meta{
	ShortCode: "E0361",
	Message:   "the source URL carries a credential",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// unreadableAdvice is what a reader does about either form of the condition.
const unreadableAdvice = "Correct the file, or set NETRC to the path of another netrc file to read instead"

// unreadableError is one occurrence of ErrNetrcFileUnreadable: the file, the
// line where the grammar failed (zero when the file could not be read at all),
// and what was wrong, described without quoting the file.
type unreadableError struct {
	message string
}

func (e *unreadableError) Error() string { return e.message }

func (e *unreadableError) Unwrap() error { return errNetrcFileUnreadable }

// grammarError reports a netrc file that does not follow the grammar. what
// describes the fault in terms of the grammar alone, so the diagnostic carries
// no word the file holds — a misplaced word may be somebody's password.
func grammarError(path string, line int, what string) error {
	values := []string{"file " + path, "line " + strconv.Itoa(line), what}
	return &unreadableError{message: cerrors.ComposeMessage(errNetrcFileUnreadable, values, unreadableAdvice)}
}

// readError reports a netrc file that exists and could not be read.
func readError(path string, cause error) error {
	values := []string{"file " + path, fmt.Sprint(cause)}
	return &unreadableError{message: cerrors.ComposeMessage(errNetrcFileUnreadable, values, unreadableAdvice)}
}
