# Minimal developer targets for the aggrega-cli module.

.PHONY: docs

# Regenerate the single-sourced reference documentation into the sibling
# aggrega-docs tree. Neither generator is part of the shipped binary — the first
# is a build-tag-gated entry point, the second a standalone command — and both
# only write markdown to the directory named by -out.
#
#   - the CLI reference, rendered from the live cobra command tree;
#   - the sentinel reference's tables, rendered from the halting-condition
#     identities the module declares and the messages registered beside them.
#
# Each has a drift gate at the umbrella root that regenerates into a throwaway
# copy and diffs, so a stale committed page fails the build rather than shipping.
DOCS_DIR ?= ../aggrega-docs
docs:
	go run -tags clidocs ./cmd/aggrega -out $(DOCS_DIR)
	go run ./internal/tools/errindex -out $(DOCS_DIR)
