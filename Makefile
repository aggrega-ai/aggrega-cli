# Minimal developer targets for the aggrega-cli module.

.PHONY: docs

# Regenerate the single-sourced documentation into the sibling aggrega-docs
# tree. No generator here is part of the shipped binary — the first is a
# build-tag-gated entry point, the rest standalone commands — and all of them
# only write markdown to the directory named by -out.
#
#   - the CLI reference, rendered from the live cobra command tree;
#   - the sentinel reference's tables, rendered from the halting-condition
#     identities the module declares and the messages registered beside them;
#   - the provider capability grid, rendered from the render-behavior contract
#     each provider guest declares.
DOCS_DIR ?= ../aggrega-docs
docs:
	go run -tags clidocs ./cmd/aggrega -out $(DOCS_DIR)
	go run ./internal/tools/errindex -out $(DOCS_DIR)
	go run ./internal/tools/providermatrix -out $(DOCS_DIR)
