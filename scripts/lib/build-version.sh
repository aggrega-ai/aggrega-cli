#!/usr/bin/env bash
# aggrega-cli/scripts/lib/build-version.sh
#
# The single home for the build-provenance version recipe, SOURCED (never executed)
# by every script that stamps one into an output: the shipped aggrega binary, the
# embedded guests' describe() self-report, the CLI generating the embedded plugin
# entities, and the editor extension's compile step. Sourcing one recipe means none
# of them invokes git on its own, so outputs stamped from one tree at one moment agree.
#
# It does NOT make outputs produced at different moments agree, and nothing here
# should be read as promising that. Each output is stamped when it is produced, so
# an artifact whose regeneration was skipped as already-current keeps the version
# it was stamped with while a freshly-produced one carries the current one.
# Agreement between separately-produced artifacts is asserted by the freshness
# checks that compare them (check-embedded-guests.sh,
# check-embedded-plugin-entities.sh), never inferred from this shared recipe. The
# generated plugin entities are the case worth knowing: they do not stamp
# themselves from this recipe at all — each copies the version its guest reports.
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
# The caller must have changed into the root of the git working tree whose build it
# stamps — the aggrega-cli module root for the compiler's outputs, the extension's
# own root for the extension — before sourcing this file, so `git describe` resolves there.
_ver="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"; _ver="${_ver#v}"
_sha="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
