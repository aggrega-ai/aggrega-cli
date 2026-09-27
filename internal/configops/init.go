// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package configops

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/registryops"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// ConfigFileExistsError reports that the configuration file a seeding resolved to
// already exists, so the seeding refused and left it untouched.
//
// It is a type rather than a bare sentinel so a surface that recognises the
// condition is handed the file it is about, and it is exported so a surface can
// word this one refusal in the vocabulary its own callers can act on. The
// condition is the same everywhere; how to proceed is not — a flag at a terminal,
// a request field over a protocol — and matching the type is what lets a surface
// re-word without a second existence guard beside this one, which would be two
// implementations of one rule that agree until either moves.
//
// 🔴 Recognise it with errors.As rather than by reading the message. The message
// is the sentence a person at a terminal has always read, kept verbatim so lifting
// this operation left the command line's output unchanged — which makes it prose
// that may be reworded, not a contract to match against.
type ConfigFileExistsError struct{ path string }

func (e *ConfigFileExistsError) Error() string {
	return fmt.Sprintf("aggrega config init: %s already exists; pass --force to proceed anyway, "+
		"which merges the registry entry into what is already there", e.path)
}

// Path is the configuration file that already exists, so a surface re-wording the
// refusal names the same file this one does rather than resolving it again.
func (e *ConfigFileExistsError) Path() string { return e.path }

// InitOptions is what seeding a configuration decides, independently of who
// asked for it.
//
// Registry is the host to seed with its trust policy. Empty is left to the
// registration's own host resolution rather than defaulted here, so the two
// surfaces cannot disagree about what an unnamed registry means.
//
// Global and Workspace are the write-target overrides. A registry is
// machine-wide tooling, so seeding defaults to the global configuration file;
// Workspace forces this project's own file. Setting both is a usage error the
// write-target policy refuses.
//
// Force permits the seeding to proceed where the target file already exists.
// Without it the seeding refuses rather than touch an existing file, and it
// refuses under every delivery — a delivery that writes nothing still rehearses
// the write it was asked about, refusals included.
//
// Delivery selects whether the seeding writes the configuration file or hands
// back the content that write would have produced. The zero value writes, so a
// caller that says nothing about delivery seeds exactly as before. Set alongside
// Force it composes: the content handed back is the merge an overwriting write
// would produce.
type InitOptions struct {
	Registry  string
	Force     bool
	Global    bool
	Workspace bool

	Delivery authoredcontent.Delivery

	// TrustClient transports the advertised-trust fetch. Nil takes the network-egress
	// gate's client in the registration; a test injects one so no live network is reached.
	TrustClient *http.Client
}

// InitResult is what seeding a configuration did.
//
// It carries the registration as a named value rather than being one. Seeding is
// not a registration: it resolves its own write target first, refuses an existing
// file the registration would have merged into, and only then registers. Handing
// a caller the registration's own result type would make this operation's
// contract the other one's, so a field on the registration that means something
// only to `aggrega registry add` would arrive here as part of this answer.
type InitResult struct {
	// Registration is what the registration did: the normalised host, the
	// configuration file it resolved to together with that file's reach, what the
	// advertised-trust fetch found, and — under a reporting delivery — the document
	// a write would have produced. The file that document is for is the target on
	// this same value, so nothing here carries a second copy of the path.
	Registration registryops.AddResult
}

// Init seeds a working configuration by registering Registry and its signature
// trust policy, and reports what it wrote and where.
//
// The registration itself — the write-target resolution, the trust-policy
// precedence, the advertised-trust fetch with its offline behaviour, the schema
// gate and the merge into an existing document — comes from the shared
// registration layer, so seeding a configuration and registering a registry
// perform one registration rather than each performing its own. That includes
// the offline refusal: seeding supplies no identities or key, so seeding a
// registry other than the official hub while offline is refused exactly as the
// registration refuses it, and the hub is seeded with its built-in baseline.
//
// What this decides on top of that is the refuse-if-exists guard, which is local
// to seeding: the shared writer merges into an existing document by design, which
// is right for adding one registry to a configuration somebody is already using
// and wrong for an operation whose whole subject is a configuration that does not
// exist yet. So the destination is resolved before anything is written and an
// existing file refuses unless Force is set.
//
// The guard reads Force alone, so it applies under every delivery. A delivery
// that writes nothing differs from a write in exactly one respect — it does not
// write — and every other behaviour, refusals included, is identical. That is
// what lets a caller use a non-writing run as a predicate for the real one: if it
// refuses, the write would have refused, for the same reason and with the same
// message. Excusing the guard here would have bought the merge document at the
// cost of that prediction, and the merge is reachable anyway by asking for it —
// Force alongside a non-writing delivery reports the document an overwriting
// write would produce.
//
// ctx ends the seeding where the caller stops wanting it. Seeding is not a local
// operation: the registration it delegates to fetches the registry's advertised
// trust policy over the network, and that fetch is what a caller who has given
// up is waiting on. Threading the context is what makes a stop reach it — and,
// at the same time, what keeps a withdrawn seeding from writing a configuration
// file whose trust policy is empty because the fetch was interrupted rather than
// because the registry advertises none.
func Init(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, opts InitOptions) (InitResult, error) {
	var res InitResult
	rw, err := workspaceOrRefuse(rw)
	if err != nil {
		return res, err
	}

	// The destination is resolved from the same write-target site the registration
	// resolves its own from, with the same operation nature, so the file the guard
	// protects is the file the registration would write. A second resolution taken
	// on different inputs would guard a path nothing was going to touch.
	dest, err := rw.WriteDestination(workspaceconfig.OpRegistry, opts.Global, opts.Workspace)
	if err != nil {
		return res, err
	}
	if !opts.Force {
		switch _, statErr := os.Stat(dest); {
		case statErr == nil:
			return res, &ConfigFileExistsError{path: dest}
		case !os.IsNotExist(statErr):
			return res, fmt.Errorf("aggrega config init: stat %s: %w", dest, statErr)
		}
	}

	// The registration's result is returned even alongside an error: the
	// advertised-trust fetch is a read that happened either way, and a caller told
	// nothing about it cannot tell a skipped fetch from a failed one.
	registration, addErr := registryops.Add(ctx, rw, registryops.AddOptions{
		Host:        opts.Registry,
		FetchTrust:  true,
		Global:      opts.Global,
		Workspace:   opts.Workspace,
		Delivery:    opts.Delivery,
		TrustClient: opts.TrustClient,
	})
	res.Registration = registration
	return res, addErr
}
