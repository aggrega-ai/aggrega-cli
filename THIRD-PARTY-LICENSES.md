# Third-Party Licenses

This is the attribution index for every third-party dependency compiled into
the shipped Aggrega CLI artifacts.

Shipped artifacts covered:

- the Go host binary (`cmd/aggrega`), for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64
- the Go standard library, compiled into the host binary and every Go plugin below
- the Rust standard library and wasi-libc, compiled into every Rust plugin below
- `provider/helm`, a Go WebAssembly plugin (`plugins/helm-provider`), embedded into the host binary
- `provider/compose`, a Go WebAssembly plugin (`plugins/compose-provider`), embedded into the host binary
- `provider/helm-chart`, a Go WebAssembly plugin (`plugins/helmchart-provider`), embedded into the host binary
- `provider/terraform`, a Go WebAssembly plugin (`plugins/terraform-provider`), embedded into the host binary
- `provider/cloudformation`, a Go WebAssembly plugin (`plugins/cloudformation-provider`), embedded into the host binary
- `provider/arm`, a Go WebAssembly plugin (`plugins/arm-provider`), embedded into the host binary
- `linter/publishing`, a Rust WebAssembly plugin (`plugins/publishing-linter`), embedded into the host binary
- `parser/hcl`, a Go WebAssembly plugin (`plugins/hcl-parser`), embedded into the host binary
- `parser/toml`, a Go WebAssembly plugin (`plugins/toml-parser`), embedded into the host binary
- `parser/yaml`, a Go WebAssembly plugin (`plugins/yaml-parser`), embedded into the host binary
- `parser/json`, a Go WebAssembly plugin (`plugins/json-parser`), embedded into the host binary
- `parser/jsonc`, a Go WebAssembly plugin (`plugins/jsonc-parser`), embedded into the host binary
- `governance/label-injector`, a Go WebAssembly plugin (`plugins/label-injector-mutator`), embedded into the host binary
- `governance/selector-tagger`, a Go WebAssembly plugin (`plugins/selector-tagger-mutator`), embedded into the host binary
- `governance/rego`, a Rust WebAssembly plugin (`plugins/rego-governance`), embedded into the host binary
- `governance/cel`, a Rust WebAssembly plugin (`plugins/cel-governance`), embedded into the host binary

**These files are generated. Do not edit them by hand.** Regenerate with:

```sh
./scripts/gen-third-party-licenses.sh
```

run from the `aggrega-cli` module root.

Attribution is split across four artifacts, in a REUSE/SPDX-style layout:

- `LICENSES/<SPDX>.txt` — the full text of each distinct license, stored once
  per SPDX identifier. For license families that embed a per-package copyright
  line (MIT, ISC, BSD-2-Clause, BSD-3-Clause, Zlib, 0BSD, MIT-0) the stored body is the canonical
  template with a placeholder copyright line; the real per-package copyright is
  recorded in the SBOM and in the notices below.
- `THIRD-PARTY.spdx` — a machine-readable SPDX 2.3 SBOM with one entry per
  dependency: name, version, SPDX license identifier, per-package copyright, and
  package locator.
- `THIRD-PARTY-NOTICES.txt` — the plain-text notices `aggrega version --licenses`
  prints: per dependency its version, license, copyright lines, any notice file
  it ships and what it is compiled into, then each license text once.
- this file — a human-readable index linking each dependency to its license
  text.

The generator enumerates the dependency set from two sources: for Go, the
packages `go list -deps` reaches from `cmd/aggrega` for each of the release's
targets (linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64) and from each embedded Go plugin; for Rust, every
crate in each embedded plugin's `Cargo.lock`. It resolves each license from the
local module and registry caches, and emits all four artifacts deterministically
(stable sort, fixed SBOM timestamp; two runs are byte-identical). First-party
Aggrega modules and crates are excluded.

For Go modules the SPDX identifier is classified from the license text and is
best-effort. For Rust crates it is read verbatim from the crate's `Cargo.toml`
`license` field.

## Summary

| Ecosystem | Third-party dependencies |
| --- | --- |
| Go | 168 |
| Rust | 219 |
| TypeScript | 0 |

Distinct licenses: 12 (see `LICENSES/`).

## Dependencies

| Component | Version | Ecosystem | Compiled into | SPDX | License text |
| --- | --- | --- | --- | --- | --- |
| `Go standard library` | `go1.26.11` | go | aggrega, provider/helm, provider/compose, provider/helm-chart, provider/terraform, provider/cloudformation, provider/arm, parser/hcl, parser/toml, parser/yaml, parser/json, parser/jsonc, governance/label-injector, governance/selector-tagger | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `Rust standard library` | `1.85.1` | rust | linter/publishing | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `Rust standard library` | `1.98.1` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `wasi-libc` | `161b3195fc2558d2b1ba3eb9ffae3b2b47407623` | c | governance/rego, governance/cel | `Apache-2.0 WITH LLVM-exception OR Apache-2.0 OR MIT` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `musl (in wasi-libc)` | `161b3195fc2558d2b1ba3eb9ffae3b2b47407623` | c | governance/rego, governance/cel | `MIT` | [MIT](LICENSES/MIT.txt) |
| `cloudlibc (in wasi-libc)` | `161b3195fc2558d2b1ba3eb9ffae3b2b47407623` | c | governance/rego, governance/cel | `BSD-2-Clause` | [BSD-2-Clause](LICENSES/BSD-2-Clause.txt) |
| `musl-fts (in wasi-libc)` | `161b3195fc2558d2b1ba3eb9ffae3b2b47407623` | c | governance/rego, governance/cel | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `wasi-libc` | `574b88da481569b65a237cb80daf9a2d5aeaf82d` | c | linter/publishing | `Apache-2.0 WITH LLVM-exception OR Apache-2.0 OR MIT` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `musl (in wasi-libc)` | `574b88da481569b65a237cb80daf9a2d5aeaf82d` | c | linter/publishing | `MIT` | [MIT](LICENSES/MIT.txt) |
| `cloudlibc (in wasi-libc)` | `574b88da481569b65a237cb80daf9a2d5aeaf82d` | c | linter/publishing | `BSD-2-Clause` | [BSD-2-Clause](LICENSES/BSD-2-Clause.txt) |
| `musl-fts (in wasi-libc)` | `574b88da481569b65a237cb80daf9a2d5aeaf82d` | c | linter/publishing | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `dario.cat/mergo` | `v1.0.1` | go | aggrega, provider/helm | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `github.com/Azure/go-ansiterm` | `v0.0.0-20250102033503-faa5f7b0171c` | go | aggrega | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/BurntSushi/toml` | `v1.6.0` | go | provider/helm, parser/hcl, parser/toml, parser/yaml, parser/json, parser/jsonc, governance/label-injector, governance/selector-tagger | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/Masterminds/goutils` | `v1.1.1` | go | aggrega, provider/helm | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/Masterminds/semver/v3` | `v3.5.0` | go | aggrega, provider/helm, provider/compose, provider/helm-chart, provider/terraform, provider/cloudformation, provider/arm | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/Masterminds/sprig/v3` | `v3.3.0` | go | aggrega, provider/helm | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/Microsoft/go-winio` | `v0.6.2` | go | aggrega | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/ProtonMail/go-crypto` | `v1.4.1` | go | aggrega | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `github.com/agext/levenshtein` | `v1.2.1` | go | provider/terraform, parser/hcl, parser/toml, parser/yaml, parser/json, parser/jsonc, governance/label-injector, governance/selector-tagger | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/apparentlymart/go-textseg/v15` | `v15.0.0` | go | provider/terraform, parser/hcl, parser/toml, parser/yaml, parser/json, parser/jsonc, governance/label-injector, governance/selector-tagger | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/asaskevich/govalidator` | `v0.0.0-20230301143203-a9d515a09cc2` | go | aggrega | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/awslabs/goformation/v7` | `v7.14.9` | go | provider/cloudformation | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/blang/semver/v4` | `v4.0.0` | go | aggrega | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/blang/semver` | `v3.5.1+incompatible` | go | aggrega | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/cenkalti/backoff/v5` | `v5.0.3` | go | aggrega | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/cespare/xxhash/v2` | `v2.3.0` | go | aggrega | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/cloudflare/circl` | `v1.6.3` | go | aggrega | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `github.com/coreos/go-oidc/v3` | `v3.17.0` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/cyberphone/json-canonicalization` | `v0.0.0-20241213102144-19d51d7fe467` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/cyphar/filepath-securejoin` | `v0.7.0` | go | aggrega, provider/helm | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `github.com/davecgh/go-spew` | `v1.1.2-0.20180830191138-d8f796af33cc` | go | aggrega, provider/helm | `ISC` | [ISC](LICENSES/ISC.txt) |
| `github.com/digitorus/pkcs7` | `v0.0.0-20230818184609-3a137a874352` | go | aggrega | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/digitorus/timestamp` | `v0.0.0-20231217203849-220c5c2851b7` | go | aggrega | `BSD-2-Clause` | [BSD-2-Clause](LICENSES/BSD-2-Clause.txt) |
| `github.com/dylibso/observe-sdk/go` | `v0.0.0-20240819160327-2d926c5d788a` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/emicklei/go-restful/v3` | `v3.13.0` | go | aggrega, provider/helm | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/emirpasic/gods` | `v1.18.1` | go | aggrega | `BSD-2-Clause` | [BSD-2-Clause](LICENSES/BSD-2-Clause.txt) |
| `github.com/evanphx/json-patch` | `v5.9.11+incompatible` | go | aggrega | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `github.com/extism/go-pdk` | `v1.1.3` | go | provider/helm, provider/compose, provider/helm-chart, provider/terraform, provider/cloudformation, provider/arm, parser/hcl, parser/toml, parser/yaml, parser/json, parser/jsonc, governance/label-injector, governance/selector-tagger | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `github.com/extism/go-sdk` | `v1.7.1` | go | aggrega | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `github.com/fxamacker/cbor/v2` | `v2.9.1` | go | aggrega, provider/helm | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/go-errors/errors` | `v1.5.1` | go | aggrega | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/go-git/gcfg` | `v1.5.1-0.20230307220236-3a3c6141e376` | go | aggrega | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `github.com/go-git/go-billy/v5` | `v5.9.0` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-git/go-git/v5` | `v5.19.1` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-jose/go-jose/v4` | `v4.1.4` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-logr/logr` | `v1.4.4` | go | aggrega, provider/helm | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-logr/stdr` | `v1.2.2` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/analysis` | `v0.25.2` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/errors` | `v0.22.7` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/jsonpointer` | `v1.0.0` | go | aggrega, provider/helm | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/jsonreference` | `v1.0.0` | go | aggrega, provider/helm | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/loads` | `v0.23.3` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/runtime/server-middleware` | `v0.30.0` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/runtime` | `v0.32.3` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/spec` | `v0.22.5` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/strfmt` | `v0.26.3` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/swag/cmdutils` | `v0.27.1` | go | aggrega, provider/helm | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/swag/conv` | `v0.27.1` | go | aggrega, provider/helm | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/swag/fileutils` | `v0.27.1` | go | aggrega, provider/helm | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/swag/jsonname` | `v0.26.0` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/swag/jsonutils` | `v0.27.1` | go | aggrega, provider/helm | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/swag/loading` | `v0.27.1` | go | aggrega, provider/helm | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/swag/mangling` | `v0.27.1` | go | aggrega, provider/helm | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/swag/netutils` | `v0.27.1` | go | aggrega, provider/helm | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/swag/pools` | `v0.27.1` | go | aggrega, provider/helm | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/swag/stringutils` | `v0.27.1` | go | aggrega, provider/helm | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/swag/typeutils` | `v0.27.1` | go | aggrega, provider/helm | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/swag/yamlutils` | `v0.27.1` | go | aggrega, provider/helm | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/swag` | `v0.27.1` | go | aggrega, provider/helm | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/validate` | `v0.25.3` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-viper/mapstructure/v2` | `v2.5.0` | go | aggrega | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/gobwas/glob` | `v0.2.3` | go | aggrega, provider/helm | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/gofrs/flock` | `v0.13.0` | go | aggrega | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `github.com/golang/groupcache` | `v0.0.0-20241129210726-2c02b8208cf8` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/google/btree` | `v1.1.3` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/google/certificate-transparency-go` | `v1.3.3` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/google/gnostic-models` | `v0.7.1` | go | aggrega, provider/helm | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/google/go-containerregistry` | `v0.21.6` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/google/jsonschema-go` | `v0.4.3` | go | aggrega | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/google/uuid` | `v1.6.0` | go | aggrega, provider/helm | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `github.com/grpc-ecosystem/grpc-gateway/v2` | `v2.29.0` | go | aggrega | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `github.com/hashicorp/go-cleanhttp` | `v0.5.2` | go | aggrega | `MPL-2.0` | [MPL-2.0](LICENSES/MPL-2.0.txt) |
| `github.com/hashicorp/go-retryablehttp` | `v0.7.8` | go | aggrega | `MPL-2.0` | [MPL-2.0](LICENSES/MPL-2.0.txt) |
| `github.com/hashicorp/hcl/v2` | `v2.24.0` | go | provider/terraform, parser/hcl, parser/toml, parser/yaml, parser/json, parser/jsonc, governance/label-injector, governance/selector-tagger | `MPL-2.0` | [MPL-2.0](LICENSES/MPL-2.0.txt) |
| `github.com/huandu/xstrings` | `v1.5.0` | go | aggrega, provider/helm | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/ianlancetaylor/demangle` | `v0.0.0-20240805132620-81f5be970eca` | go | aggrega | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `github.com/in-toto/attestation` | `v1.2.0` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/in-toto/in-toto-golang` | `v0.11.0` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/inconshreveable/mousetrap` | `v1.1.0` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/jbenet/go-context` | `v0.0.0-20150711004518-d14ea06fba99` | go | aggrega | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/jedisct1/go-minisign` | `v0.0.0-20211028175153-1c139d1cc84b` | go | aggrega | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/json-iterator/go` | `v1.1.12` | go | aggrega, provider/helm | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/kevinburke/ssh_config` | `v1.2.0` | go | aggrega | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/klauspost/cpuid/v2` | `v2.3.0` | go | aggrega | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/liggitt/tabwriter` | `v0.0.0-20181228230101-89fcab3d43de` | go | aggrega | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `github.com/mitchellh/copystructure` | `v1.2.0` | go | aggrega, provider/helm | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/mitchellh/go-wordwrap` | `v1.0.1` | go | provider/terraform, parser/hcl, parser/toml, parser/yaml, parser/json, parser/jsonc, governance/label-injector, governance/selector-tagger | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/mitchellh/reflectwalk` | `v1.0.2` | go | aggrega, provider/helm | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/moby/term` | `v0.5.2` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/modern-go/concurrent` | `v0.0.0-20180306012644-bacd9c7ef1dd` | go | aggrega, provider/helm | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/modern-go/reflect2` | `v1.0.3-0.20250322232337-35a7c28c31ee` | go | aggrega, provider/helm | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/monochromegane/go-gitignore` | `v0.0.0-20200626010858-205db1a8cc00` | go | aggrega | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/munnerz/goautoneg` | `v0.0.0-20191010083416-a7dc8b61c822` | go | aggrega, provider/helm | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `github.com/oklog/ulid/v2` | `v2.1.1` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/opencontainers/go-digest` | `v1.0.0` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/opencontainers/image-spec` | `v1.1.1` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/peterbourgon/diskv` | `v2.0.1+incompatible` | go | aggrega | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/pjbgf/sha1cd` | `v0.6.0` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/pkg/browser` | `v0.0.0-20240102092130-5ac0b6a4141c` | go | aggrega | `BSD-2-Clause` | [BSD-2-Clause](LICENSES/BSD-2-Clause.txt) |
| `github.com/pkg/errors` | `v0.9.1` | go | aggrega | `BSD-2-Clause` | [BSD-2-Clause](LICENSES/BSD-2-Clause.txt) |
| `github.com/santhosh-tekuri/jsonschema/v6` | `v6.0.3` | go | aggrega, provider/helm | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/sassoftware/relic` | `v7.2.1+incompatible` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/secure-systems-lab/go-securesystemslib` | `v0.11.0` | go | aggrega | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/sergi/go-diff` | `v1.4.0` | go | aggrega | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/shibumi/go-pathspec` | `v1.3.0` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/shopspring/decimal` | `v1.4.0` | go | aggrega, provider/helm | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/sigstore/protobuf-specs` | `v0.5.1` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/sigstore/rekor-tiles/v2` | `v2.2.2-0.20260601073857-5d098a2b6443` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/sigstore/rekor` | `v1.5.2` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/sigstore/sigstore-go` | `v1.2.1` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/sigstore/sigstore` | `v1.10.8` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/sigstore/timestamp-authority/v2` | `v2.1.2` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/skeema/knownhosts` | `v1.3.1` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/spf13/cast` | `v1.10.0` | go | aggrega, provider/helm | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/spf13/cobra` | `v1.10.2` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/spf13/pflag` | `v1.0.10` | go | aggrega | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `github.com/tetratelabs/wabin` | `v0.0.0-20230304001439-f6f874872834` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/tetratelabs/wazero` | `v1.12.0` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/theupdateframework/go-tuf/v2` | `v2.4.2-0.20260407074541-7e8f69f906ef` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/theupdateframework/go-tuf` | `v0.7.0` | go | aggrega | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `github.com/transparency-dev/formats` | `v0.1.1` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/transparency-dev/merkle` | `v0.0.2` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/x448/float16` | `v0.8.4` | go | aggrega, provider/helm | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/xanzy/ssh-agent` | `v0.3.3` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/xlab/treeprint` | `v1.2.0` | go | aggrega | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/youmark/pkcs8` | `v0.0.0-20240726163527-a2c0da244d78` | go | aggrega | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/zclconf/go-cty` | `v1.16.3` | go | provider/terraform, parser/hcl, parser/toml, parser/yaml, parser/json, parser/jsonc, governance/label-injector, governance/selector-tagger | `MIT` | [MIT](LICENSES/MIT.txt) |
| `go.opentelemetry.io/auto/sdk` | `v1.2.1` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `go.opentelemetry.io/otel/metric` | `v1.45.0` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `go.opentelemetry.io/otel/trace` | `v1.45.0` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `go.opentelemetry.io/otel` | `v1.45.0` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `go.opentelemetry.io/proto/otlp` | `v1.11.0` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `go.yaml.in/yaml/v2` | `v2.4.4` | go | aggrega, provider/helm | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `go.yaml.in/yaml/v3` | `v3.0.5` | go | aggrega, provider/helm, provider/compose, provider/helm-chart, provider/terraform, provider/cloudformation, provider/arm, parser/hcl, parser/toml, parser/yaml, parser/json, parser/jsonc, governance/label-injector, governance/selector-tagger | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `golang.org/x/crypto` | `v0.58.0` | go | aggrega, provider/helm | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `golang.org/x/mod` | `v0.41.0` | go | aggrega | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `golang.org/x/net` | `v0.60.0` | go | aggrega, provider/helm | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `golang.org/x/oauth2` | `v0.36.0` | go | aggrega, provider/helm | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `golang.org/x/sync` | `v0.24.0` | go | aggrega | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `golang.org/x/sys` | `v0.49.0` | go | aggrega | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `golang.org/x/term` | `v0.47.0` | go | aggrega, provider/helm | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `golang.org/x/text` | `v0.43.0` | go | aggrega, provider/helm, provider/terraform, parser/hcl, parser/toml, parser/yaml, parser/json, parser/jsonc, governance/label-injector, governance/selector-tagger | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `golang.org/x/time` | `v0.15.0` | go | aggrega, provider/helm | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `google.golang.org/genproto/googleapis/api` | `v0.0.0-20260803160001-6ac0973c030d` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `google.golang.org/genproto/googleapis/rpc` | `v0.0.0-20260803160001-6ac0973c030d` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `google.golang.org/grpc` | `v1.83.1` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `google.golang.org/protobuf` | `v1.36.12` | go | aggrega, provider/helm | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `gopkg.in/evanphx/json-patch.v4` | `v4.13.0` | go | aggrega, provider/helm | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `gopkg.in/inf.v0` | `v0.9.1` | go | aggrega, provider/helm | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `gopkg.in/warnings.v0` | `v0.1.2` | go | aggrega | `BSD-2-Clause` | [BSD-2-Clause](LICENSES/BSD-2-Clause.txt) |
| `gopkg.in/yaml.v3` | `v3.0.1` | go | aggrega, provider/cloudformation | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `helm.sh/helm/v4` | `v4.3.1` | go | aggrega, provider/helm | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `k8s.io/apiextensions-apiserver` | `v0.37.1` | go | aggrega, provider/helm | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `k8s.io/apimachinery` | `v0.37.1` | go | aggrega, provider/helm | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `k8s.io/api` | `v0.37.1` | go | aggrega, provider/helm | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `k8s.io/cli-runtime` | `v0.37.1` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `k8s.io/client-go` | `v0.37.1` | go | aggrega, provider/helm | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `k8s.io/klog/v2` | `v2.140.0` | go | aggrega, provider/helm | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `k8s.io/kube-openapi` | `v0.0.0-20260721132016-d427ff9ee9ad` | go | aggrega, provider/helm | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `k8s.io/utils` | `v0.0.0-20260626114624-be93311217bd` | go | aggrega, provider/helm | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `oras.land/oras-go/v2` | `v2.6.2` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `sigs.k8s.io/json` | `v0.0.0-20250730193827-2d320260d730` | go | aggrega, provider/helm | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `sigs.k8s.io/kustomize/api` | `v0.21.1` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `sigs.k8s.io/kustomize/kyaml` | `v0.21.2` | go | aggrega | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `sigs.k8s.io/randfill` | `v1.0.0` | go | aggrega, provider/helm | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `sigs.k8s.io/structured-merge-diff/v6` | `v6.4.2` | go | aggrega, provider/helm | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `sigs.k8s.io/yaml` | `v1.6.0` | go | aggrega, provider/helm | `MIT` | [MIT](LICENSES/MIT.txt) |
| `adler2` | `2.0.1` | rust | governance/rego | `0BSD OR MIT OR Apache-2.0` | [0BSD](LICENSES/0BSD.txt), [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `ahash` | `0.8.12` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `aho-corasick` | `1.1.4` | rust | linter/publishing | `Unlicense OR MIT` | [MIT](LICENSES/MIT.txt), [Unlicense](LICENSES/Unlicense.txt) |
| `aho-corasick` | `1.1.5` | rust | governance/rego, governance/cel | `Unlicense OR MIT` | [MIT](LICENSES/MIT.txt), [Unlicense](LICENSES/Unlicense.txt) |
| `allocator-api2` | `0.2.21` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `android_system_properties` | `0.1.6` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `antlr4rust` | `0.5.2` | rust | governance/cel | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `anyhow` | `1.0.102` | rust | linter/publishing | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `anyhow` | `1.0.104` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `autocfg` | `1.5.0` | rust | linter/publishing | `Apache-2.0 OR MIT` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `autocfg` | `1.5.1` | rust | governance/rego, governance/cel | `Apache-2.0 OR MIT` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `base64` | `0.22.1` | rust | linter/publishing, governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `better_any` | `0.2.1` | rust | governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `bit-set` | `0.8.0` | rust | governance/rego, governance/cel | `Apache-2.0 OR MIT` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `bit-vec` | `0.8.0` | rust | governance/rego, governance/cel | `Apache-2.0 OR MIT` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `bitflags` | `2.13.1` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `borrow-or-share` | `0.2.4` | rust | governance/rego | `MIT-0` | [MIT-0](LICENSES/MIT-0.txt) |
| `bstr` | `1.13.1` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `bumpalo` | `3.20.3` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `bytecount` | `0.6.9` | rust | governance/rego | `Apache-2.0/MIT` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `bytemuck` | `1.25.0` | rust | linter/publishing | `Zlib OR Apache-2.0 OR MIT` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt), [Zlib](LICENSES/Zlib.txt) |
| `bytemuck` | `1.25.2` | rust | governance/rego, governance/cel | `Zlib OR Apache-2.0 OR MIT` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt), [Zlib](LICENSES/Zlib.txt) |
| `byteorder` | `1.5.0` | rust | governance/cel | `Unlicense OR MIT` | [MIT](LICENSES/MIT.txt), [Unlicense](LICENSES/Unlicense.txt) |
| `bytes` | `1.11.1` | rust | linter/publishing | `MIT` | [MIT](LICENSES/MIT.txt) |
| `bytes` | `1.12.1` | rust | governance/rego, governance/cel | `MIT` | [MIT](LICENSES/MIT.txt) |
| `cc` | `1.4.4` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `cel` | `0.14.3` | rust | governance/cel | `MIT` | [MIT](LICENSES/MIT.txt) |
| `cfg-if` | `1.0.4` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `chacha20` | `0.10.1` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `chrono` | `0.4.45` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `chrono-tz` | `0.10.4` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `cobs` | `0.3.0` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `core-foundation-sys` | `0.8.7` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `cpufeatures` | `0.3.0` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `crc32fast` | `1.5.0` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `data-encoding` | `2.11.1` | rust | governance/rego | `MIT` | [MIT](LICENSES/MIT.txt) |
| `displaydoc` | `0.2.7` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `either` | `1.16.0` | rust | linter/publishing | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `either` | `1.18.0` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `email_address` | `0.2.9` | rust | governance/rego | `MIT` | [MIT](LICENSES/MIT.txt) |
| `embedded-io` | `0.4.0` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `embedded-io` | `0.6.1` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `equivalent` | `1.0.2` | rust | linter/publishing, governance/rego, governance/cel | `Apache-2.0 OR MIT` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `extism-convert` | `1.21.0` | rust | linter/publishing | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `extism-convert` | `1.30.0` | rust | governance/rego, governance/cel | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `extism-convert-macros` | `1.21.0` | rust | linter/publishing | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `extism-convert-macros` | `1.30.0` | rust | governance/rego, governance/cel | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `extism-manifest` | `1.21.0` | rust | linter/publishing | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `extism-manifest` | `1.30.0` | rust | governance/rego, governance/cel | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `extism-pdk` | `1.4.1` | rust | linter/publishing, governance/rego, governance/cel | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `extism-pdk-derive` | `1.4.1` | rust | linter/publishing, governance/rego, governance/cel | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `fancy-regex` | `0.18.0` | rust | governance/rego | `MIT` | [MIT](LICENSES/MIT.txt) |
| `filetime` | `0.2.29` | rust | governance/rego | `MIT/Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `find-msvc-tools` | `0.1.11` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `flate2` | `1.1.9` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `fluent-uri` | `0.4.1` | rust | governance/rego | `MIT` | [MIT](LICENSES/MIT.txt) |
| `foldhash` | `0.2.0` | rust | governance/rego | `Zlib` | [Zlib](LICENSES/Zlib.txt) |
| `form_urlencoded` | `1.2.2` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `fraction` | `0.15.4` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `futures-core` | `0.3.34` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `futures-task` | `0.3.34` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `futures-util` | `0.3.34` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `getrandom` | `0.3.4` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `getrandom` | `0.4.3` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `globset` | `0.4.20` | rust | governance/rego | `Unlicense OR MIT` | [MIT](LICENSES/MIT.txt), [Unlicense](LICENSES/Unlicense.txt) |
| `hashbrown` | `0.16.1` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `hashbrown` | `0.17.1` | rust | linter/publishing, governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `iana-time-zone` | `0.1.65` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `iana-time-zone-haiku` | `0.1.2` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `icu_collections` | `2.3.0` | rust | governance/rego | `Unicode-3.0` | [Unicode-3.0](LICENSES/Unicode-3.0.txt) |
| `icu_locale_core` | `2.3.0` | rust | governance/rego | `Unicode-3.0` | [Unicode-3.0](LICENSES/Unicode-3.0.txt) |
| `icu_normalizer` | `2.3.0` | rust | governance/rego | `Unicode-3.0` | [Unicode-3.0](LICENSES/Unicode-3.0.txt) |
| `icu_normalizer_data` | `2.3.0` | rust | governance/rego | `Unicode-3.0` | [Unicode-3.0](LICENSES/Unicode-3.0.txt) |
| `icu_properties` | `2.3.0` | rust | governance/rego | `Unicode-3.0` | [Unicode-3.0](LICENSES/Unicode-3.0.txt) |
| `icu_properties_data` | `2.3.0` | rust | governance/rego | `Unicode-3.0` | [Unicode-3.0](LICENSES/Unicode-3.0.txt) |
| `icu_provider` | `2.3.1` | rust | governance/rego | `Unicode-3.0` | [Unicode-3.0](LICENSES/Unicode-3.0.txt) |
| `idna` | `1.1.0` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `idna_adapter` | `1.2.2` | rust | governance/rego | `Apache-2.0 OR MIT` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `indexmap` | `2.14.0` | rust | linter/publishing, governance/rego, governance/cel | `Apache-2.0 OR MIT` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `ipnet` | `2.12.1` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `itertools` | `0.14.0` | rust | linter/publishing, governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `itoa` | `1.0.18` | rust | linter/publishing, governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `js-sys` | `0.3.104` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `jsonschema` | `0.46.10` | rust | governance/rego | `MIT` | [MIT](LICENSES/MIT.txt) |
| `jsonschema-regex` | `0.46.10` | rust | governance/rego | `MIT` | [MIT](LICENSES/MIT.txt) |
| `lazy_static` | `1.5.0` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `libc` | `0.2.189` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `litemap` | `0.8.3` | rust | governance/rego | `Unicode-3.0` | [Unicode-3.0](LICENSES/Unicode-3.0.txt) |
| `lock_api` | `0.4.14` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `log` | `0.4.33` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `log` | `0.4.34` | rust | governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `lru` | `0.18.2` | rust | governance/rego | `MIT` | [MIT](LICENSES/MIT.txt) |
| `manyhow` | `0.11.4` | rust | linter/publishing, governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `manyhow-macros` | `0.11.4` | rust | linter/publishing, governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `memchr` | `2.8.0` | rust | linter/publishing | `Unlicense OR MIT` | [MIT](LICENSES/MIT.txt), [Unlicense](LICENSES/Unlicense.txt) |
| `memchr` | `2.8.3` | rust | governance/rego, governance/cel | `Unlicense OR MIT` | [MIT](LICENSES/MIT.txt), [Unlicense](LICENSES/Unlicense.txt) |
| `micromap` | `0.3.0` | rust | governance/rego | `MIT` | [MIT](LICENSES/MIT.txt) |
| `minimal-lexical` | `0.2.1` | rust | governance/cel | `MIT/Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `miniz_oxide` | `0.8.9` | rust | governance/rego | `MIT OR Zlib OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt), [Zlib](LICENSES/Zlib.txt) |
| `msvc_spectre_libs` | `0.1.3` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `murmur3` | `0.4.1` | rust | governance/cel | `MIT/Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `nom` | `7.1.3` | rust | governance/cel | `MIT` | [MIT](LICENSES/MIT.txt) |
| `num` | `0.4.3` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `num-bigint` | `0.4.8` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `num-cmp` | `0.1.0` | rust | governance/rego | `MIT/Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `num-complex` | `0.4.6` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `num-integer` | `0.1.47` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `num-iter` | `0.1.46` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `num-rational` | `0.4.2` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `num-traits` | `0.2.19` | rust | linter/publishing, governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `once_cell` | `1.21.4` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `outref` | `0.5.2` | rust | governance/rego | `MIT` | [MIT](LICENSES/MIT.txt) |
| `parking_lot` | `0.12.5` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `parking_lot_core` | `0.9.12` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `pastey` | `0.2.3` | rust | governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `percent-encoding` | `2.3.2` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `phf` | `0.12.1` | rust | governance/rego | `MIT` | [MIT](LICENSES/MIT.txt) |
| `phf_shared` | `0.12.1` | rust | governance/rego | `MIT` | [MIT](LICENSES/MIT.txt) |
| `pin-project-lite` | `0.2.17` | rust | governance/rego, governance/cel | `Apache-2.0 OR MIT` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `postcard` | `1.1.3` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `potential_utf` | `0.1.6` | rust | governance/rego | `Unicode-3.0` | [Unicode-3.0](LICENSES/Unicode-3.0.txt) |
| `proc-macro-crate` | `3.5.0` | rust | linter/publishing, governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `proc-macro-utils` | `0.10.0` | rust | linter/publishing, governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `proc-macro2` | `1.0.106` | rust | linter/publishing | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `proc-macro2` | `1.0.107` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `prost` | `0.14.3` | rust | linter/publishing | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `prost` | `0.14.4` | rust | governance/rego, governance/cel | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `prost-derive` | `0.14.3` | rust | linter/publishing | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `prost-derive` | `0.14.4` | rust | governance/rego, governance/cel | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `quote` | `1.0.45` | rust | linter/publishing | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `quote` | `1.0.47` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `r-efi` | `5.3.0` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `r-efi` | `6.0.0` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `rand` | `0.10.2` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `rand_core` | `0.10.1` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `redox_syscall` | `0.5.18` | rust | governance/rego, governance/cel | `MIT` | [MIT](LICENSES/MIT.txt) |
| `ref-cast` | `1.0.27` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `ref-cast-impl` | `1.0.27` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `referencing` | `0.46.10` | rust | governance/rego | `MIT` | [MIT](LICENSES/MIT.txt) |
| `regex` | `1.12.3` | rust | linter/publishing | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `regex` | `1.13.1` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `regex-automata` | `0.4.14` | rust | linter/publishing | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `regex-automata` | `0.4.18` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `regex-syntax` | `0.8.10` | rust | linter/publishing | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `regex-syntax` | `0.8.11` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `regorus` | `0.10.1` | rust | governance/rego | `MIT AND Apache-2.0 AND BSD-3-Clause` | [Apache-2.0](LICENSES/Apache-2.0.txt), [BSD-3-Clause](LICENSES/BSD-3-Clause.txt), [MIT](LICENSES/MIT.txt) |
| `rmp` | `0.8.15` | rust | linter/publishing, governance/rego, governance/cel | `MIT` | [MIT](LICENSES/MIT.txt) |
| `rmp-serde` | `1.3.1` | rust | linter/publishing, governance/rego, governance/cel | `MIT` | [MIT](LICENSES/MIT.txt) |
| `rustversion` | `1.0.23` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `ryu` | `1.0.23` | rust | governance/rego, governance/cel | `Apache-2.0 OR BSL-1.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [BSL-1.0](LICENSES/BSL-1.0.txt) |
| `scopeguard` | `1.2.0` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `semver` | `1.0.28` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `serde` | `1.0.228` | rust | linter/publishing | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `serde` | `1.0.229` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `serde_core` | `1.0.228` | rust | linter/publishing | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `serde_core` | `1.0.229` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `serde_derive` | `1.0.228` | rust | linter/publishing | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `serde_derive` | `1.0.229` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `serde_json` | `1.0.149` | rust | linter/publishing | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `serde_json` | `1.0.151` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `serde_yaml` | `0.9.34+deprecated` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `shlex` | `2.0.1` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `simd-adler32` | `0.3.10` | rust | governance/rego | `MIT` | [MIT](LICENSES/MIT.txt) |
| `siphasher` | `1.0.3` | rust | governance/rego | `MIT/Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `slab` | `0.4.12` | rust | governance/rego, governance/cel | `MIT` | [MIT](LICENSES/MIT.txt) |
| `smallvec` | `1.15.1` | rust | linter/publishing | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `smallvec` | `1.15.2` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `spin` | `0.10.1` | rust | governance/rego | `MIT` | [MIT](LICENSES/MIT.txt) |
| `stable_deref_trait` | `1.2.1` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `syn` | `2.0.117` | rust | linter/publishing | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `syn` | `2.0.119` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `syn` | `3.0.3` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `synstructure` | `0.13.2` | rust | governance/rego | `MIT` | [MIT](LICENSES/MIT.txt) |
| `tar` | `0.4.46` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `thiserror` | `2.0.20` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `thiserror-impl` | `2.0.20` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `tinystr` | `0.8.4` | rust | governance/rego | `Unicode-3.0` | [Unicode-3.0](LICENSES/Unicode-3.0.txt) |
| `toml_datetime` | `1.1.1+spec-1.1.0` | rust | linter/publishing, governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `toml_edit` | `0.25.11+spec-1.1.0` | rust | linter/publishing | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `toml_edit` | `0.25.13+spec-1.1.0` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `toml_parser` | `1.1.2+spec-1.1.0` | rust | linter/publishing | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `toml_parser` | `1.1.3+spec-1.1.0` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `typed-arena` | `2.0.2` | rust | governance/cel | `MIT` | [MIT](LICENSES/MIT.txt) |
| `unicode-general-category` | `1.1.0` | rust | governance/rego | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `unicode-ident` | `1.0.24` | rust | linter/publishing, governance/rego, governance/cel | `(MIT OR Apache-2.0) AND Unicode-3.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt), [Unicode-3.0](LICENSES/Unicode-3.0.txt) |
| `unsafe-libyaml` | `0.2.11` | rust | governance/rego, governance/cel | `MIT` | [MIT](LICENSES/MIT.txt) |
| `url` | `2.5.8` | rust | governance/rego | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `utf8_iter` | `1.0.4` | rust | governance/rego | `Apache-2.0 OR MIT` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `uuid` | `1.24.1` | rust | governance/rego | `Apache-2.0 OR MIT` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `uuid` | `1.25.0` | rust | governance/cel | `Apache-2.0 OR MIT` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `uuid-simd` | `0.8.0` | rust | governance/rego | `MIT` | [MIT](LICENSES/MIT.txt) |
| `version_check` | `0.9.5` | rust | governance/rego | `MIT/Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `vsimd` | `0.8.0` | rust | governance/rego | `MIT` | [MIT](LICENSES/MIT.txt) |
| `wasip2` | `1.0.4+wasi-0.2.12` | rust | governance/rego | `Apache-2.0 WITH LLVM-exception OR Apache-2.0 OR MIT` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `wasm-bindgen` | `0.2.127` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `wasm-bindgen-macro` | `0.2.127` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `wasm-bindgen-macro-support` | `0.2.127` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `wasm-bindgen-shared` | `0.2.127` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `windows-core` | `0.62.2` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `windows-implement` | `0.60.2` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `windows-interface` | `0.59.3` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `windows-link` | `0.2.1` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `windows-result` | `0.4.1` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `windows-strings` | `0.5.1` | rust | governance/rego, governance/cel | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `winnow` | `1.0.3` | rust | linter/publishing | `MIT` | [MIT](LICENSES/MIT.txt) |
| `winnow` | `1.0.4` | rust | governance/rego, governance/cel | `MIT` | [MIT](LICENSES/MIT.txt) |
| `wit-bindgen` | `0.57.1` | rust | governance/rego | `Apache-2.0 WITH LLVM-exception OR Apache-2.0 OR MIT` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `writeable` | `0.6.4` | rust | governance/rego | `Unicode-3.0` | [Unicode-3.0](LICENSES/Unicode-3.0.txt) |
| `yoke` | `0.8.3` | rust | governance/rego | `Unicode-3.0` | [Unicode-3.0](LICENSES/Unicode-3.0.txt) |
| `yoke-derive` | `0.8.2` | rust | governance/rego | `Unicode-3.0` | [Unicode-3.0](LICENSES/Unicode-3.0.txt) |
| `zerocopy` | `0.8.56` | rust | governance/rego | `BSD-2-Clause OR Apache-2.0 OR MIT` | [Apache-2.0](LICENSES/Apache-2.0.txt), [BSD-2-Clause](LICENSES/BSD-2-Clause.txt), [MIT](LICENSES/MIT.txt) |
| `zerocopy-derive` | `0.8.56` | rust | governance/rego | `BSD-2-Clause OR Apache-2.0 OR MIT` | [Apache-2.0](LICENSES/Apache-2.0.txt), [BSD-2-Clause](LICENSES/BSD-2-Clause.txt), [MIT](LICENSES/MIT.txt) |
| `zerofrom` | `0.1.8` | rust | governance/rego | `Unicode-3.0` | [Unicode-3.0](LICENSES/Unicode-3.0.txt) |
| `zerofrom-derive` | `0.1.7` | rust | governance/rego | `Unicode-3.0` | [Unicode-3.0](LICENSES/Unicode-3.0.txt) |
| `zerotrie` | `0.2.5` | rust | governance/rego | `Unicode-3.0` | [Unicode-3.0](LICENSES/Unicode-3.0.txt) |
| `zerovec` | `0.11.8` | rust | governance/rego | `Unicode-3.0` | [Unicode-3.0](LICENSES/Unicode-3.0.txt) |
| `zerovec-derive` | `0.11.6` | rust | governance/rego | `Unicode-3.0` | [Unicode-3.0](LICENSES/Unicode-3.0.txt) |
| `zmij` | `1.0.21` | rust | linter/publishing | `MIT` | [MIT](LICENSES/MIT.txt) |
| `zmij` | `1.0.23` | rust | governance/rego, governance/cel | `MIT` | [MIT](LICENSES/MIT.txt) |

## TypeScript

No TypeScript dependencies are compiled into the CLI. The CLI ships no
TypeScript-derived artifact; TypeScript plugin code in the plugin
development kit is example material, not part of any shipped CLI artifact.
