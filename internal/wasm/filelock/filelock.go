// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package filelock takes exclusive advisory locks on sidecar lock files, so
// that several processes sharing one on-disk directory can take turns writing
// it instead of interleaving.
//
// The lock is held on a file the caller names, and the caller chooses that
// name: the mechanic being shared here is the locking, not the naming. Keeping
// path derivation with the caller is deliberate, because callers key their
// locks on different things — one per content digest, one per index directory,
// one per build artifact — and a helper that derived the path itself would have
// to know all of them.
//
// Acquire is best-effort and never fails its caller. Where the platform has no
// POSIX advisory locking, or the lock file cannot be opened or locked, it
// returns a release function that does nothing and the caller proceeds with
// whatever weaker serialization it already has. A caller that needs the lock
// for correctness rather than efficiency should still prefer this degradation:
// refusing to run at all on a platform that cannot lock trades a rare wrong
// result for a certain failure.
package filelock
