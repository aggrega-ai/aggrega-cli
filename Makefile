# Minimal developer targets for the aggrega-cli module.

.PHONY: docs

# Regenerate the CLI reference documentation from the live cobra command
# tree into the sibling aggrega-docs tree. The generator is a build-tag-gated
# entry point, so it is never part of the shipped binary; it only writes
# markdown to the directory named by -out.
DOCS_DIR ?= ../aggrega-docs
docs:
	go run -tags clidocs ./cmd/aggrega -out $(DOCS_DIR)
