#!/usr/bin/env bash
# aggrega-cli/scripts/lib/build-version.sh
#
# The single home for the build-provenance version recipe. This file is SOURCED
# (never executed) by the scripts that stamp a build-provenance version into an
# output: the shipped aggrega binary, the embedded guests' describe()
# self-report, and the CLI that generates the embedded plugin entities. Sourcing
# one recipe keeps those three in lockstep — an embedded entity's stamped
# version always matches the binary that carries it — because none of them
# invokes git on its own.
#
# Sourcing sets two variables in the caller's shell:
#   _ver — git describe, with any leading "v" stripped: the tag, the bare commit
#          before any tag, or a "-dirty" suffix when the working tree has
#          uncommitted changes; "dev" when git is unavailable.
#   _sha — the short HEAD commit, or "unknown" when git is unavailable.
#
# The recipe carries the working-tree --dirty marker on purpose: a build made
# from an edited tree advertises its exact state rather than masquerading as a
# clean commit. Callers that need a clean-tree version for committed artifacts
# regenerate from a clean working tree; they do not strip --dirty.
#
# The caller must have changed into the aggrega-cli module root (a git working
# tree) before sourcing this file, so `git describe` resolves against it.
_ver="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"; _ver="${_ver#v}"
_sha="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
