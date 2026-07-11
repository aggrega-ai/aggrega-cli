# syntax=docker/dockerfile:1
#
# Tooling image for the aggrega CLI.
#
# This image packages a pre-built aggrega binary so consumer CI can run
# `aggrega push`, `aggrega pull`, `aggrega sign`, and the rest of the CLI
# without a Go toolchain. It is published to the registry at the reserved
# tools/ namespace (tools/aggrega) and is signed by reference after push.
#
# WHY copy a pre-built binary rather than build from source here:
# the embedded build regenerates the provider and linter Wasm guests from
# source (a TinyGo + Rust + wasm toolchain) and links the CLI against a
# sibling module resolved through the workspace's local replace. That build
# lives in the repository's build scripts and cannot be reproduced from an
# aggrega-cli-only build context without duplicating the guest-build logic
# (which would let the two copies drift). Instead, the build scripts produce
# the embedded, version-stamped binary first; this Dockerfile packages that
# exact artifact. scripts/build-tooling-image.sh runs the build and verifies
# the binary is embedded and stamped before invoking docker.
#
# The binary MUST be the embedded build (guests compiled in, resolves with no
# network) and MUST carry the git-describe version stamp. The build script
# enforces both by running `aggrega plugin list` (embedded guests present) and
# `aggrega version --json` (version + abiVersion present) before the image is
# built. The build context is the aggrega-cli directory, where the build
# scripts leave the `aggrega` binary.

# Version string reported by the binary (git-describe, v-less), stamped as a
# label so a consumer can gate at the registry/manifest level without running
# the image.
ARG VERSION=unknown

# Plugin ABI version the binary speaks. Sourced from the binary's own
# `aggrega version --json` abiVersion field, stamped as a label so a consumer
# CI can gate on ABI compatibility from the manifest alone.
ARG ABI_VERSION=unknown

# A minimal Debian base carrying the tools the consumer publisher CIs run
# INSIDE this image. This image's documented job is to be the environment those
# CIs execute in (`container: <registry>/tools/aggrega`), and they run publish
# shell scripts plus a repository-checkout step, which need: a POSIX shell and
# bash (the publish scripts use `#!/usr/bin/env bash`), git (both the publish
# scripts and the checkout step shell out to git), awk (the publish scripts
# parse with it), and CA certificates for TLS to the registry. bash is present
# in the base by default; git, awk (gawk), and ca-certificates are installed
# below.
#
# The aggrega binary itself is a static, cgo-free Go binary (its Wasm runtime is
# pure Go) and needs none of these tools — it would run on any base. The base is
# sized for the CI tooling that runs alongside the binary, not for the binary's
# own linkage.
#
# The image runs as root. Where the CI runner mounts and owns the checked-out
# workspace as root, running git and the checkout step as a non-root user
# triggers git "dubious ownership" and workspace-permission friction; root is a
# CI-ergonomics choice for this tooling context, not a requirement of the
# binary, which needs no elevated privileges.
FROM debian:bookworm-slim

# git + awk (gawk) + CA certificates for the consumer CIs described above.
# --no-install-recommends and cleaning the apt lists in the same layer keep the
# image minimal.
RUN apt-get update \
 && apt-get install --no-install-recommends -y \
      ca-certificates \
      gawk \
      git \
 && rm -rf /var/lib/apt/lists/*

ARG VERSION
ARG ABI_VERSION

# Standard OCI annotations plus the aggrega-specific label namespace consumers
# gate on. The abiVersion and version labels mirror the binary's own
# `aggrega version --json` fields so a registry-level check and a run-time
# check agree.
LABEL org.opencontainers.image.title="aggrega" \
      org.opencontainers.image.description="The aggrega CLI: an ahead-of-time compiler and registry client for cloud-native deployment intent." \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.licenses="Apache-2.0" \
      ai.aggrega.version="${VERSION}" \
      ai.aggrega.abi-version="${ABI_VERSION}"

# The embedded, version-stamped binary produced by the build scripts.
COPY aggrega /usr/local/bin/aggrega

ENTRYPOINT ["/usr/local/bin/aggrega"]

# Default to reporting build identity, so `docker run <image>` with no
# arguments prints the git-describe version the image was built from.
CMD ["version"]
