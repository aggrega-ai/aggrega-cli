# Vendored JSON Patch conformance suite

`tests.json` and `spec_tests.json` are the community RFC 6902 conformance
suite from [json-patch/json-patch-tests](https://github.com/json-patch/json-patch-tests),
vendored verbatim. Licensed Apache-2.0 (see the upstream repository).

`conformance_test.go` runs every case against the apply engine. Cases
whose document — or expected result — is not a JSON object are skipped
as out of domain: this engine patches rendered manifests, whose root is
always an object, and is deliberately narrower than general RFC 6902
(which permits array- and scalar-rooted documents). The skip count is
logged so the coverage is explicit, never silent.
