# Third-Party Licenses

This is the attribution index for every third-party dependency compiled into
the shipped Aggrega CLI artifacts.

Shipped artifacts covered:

- the Go host binary (`cmd/aggrega`)
- the Go WebAssembly provider guests (`plugins/helm-provider`,
  `plugins/compose-provider`, `plugins/helmchart-provider`,
  `plugins/terraform-provider`, `plugins/cloudformation-provider`), embedded into
  the host binary
- the Rust WebAssembly guest (`plugins/publishing-linter`)

**These files are generated. Do not edit them by hand.** Regenerate with:

```sh
./scripts/gen-third-party-licenses.sh
```

run from the `aggrega-cli` module root.

Attribution is split across three artifacts, in a REUSE/SPDX-style layout:

- `LICENSES/<SPDX>.txt` — the full text of each distinct license, stored once
  per SPDX identifier. For license families that embed a per-package copyright
  line (MIT, ISC, BSD, zlib) the stored body is the canonical template; the
  real per-package copyright is recorded in the SBOM below.
- `THIRD-PARTY.spdx` — a machine-readable SPDX 2.3 SBOM with one entry per
  dependency: name, version, SPDX license identifier, per-package copyright, and
  package locator.
- this file — a human-readable index linking each dependency to its license
  text.

The generator enumerates the actual compiled dependency set (`go list -deps`
for Go, `Cargo.lock` for Rust), resolves each license from the local module and
registry caches, and emits all three artifacts deterministically (stable sort,
fixed SBOM timestamp; two runs are byte-identical). First-party Aggrega modules
and crates are excluded.

For Go modules the SPDX identifier is classified from the license text and is
best-effort. For Rust crates it is read verbatim from the crate's `Cargo.toml`
`license` field.

## Summary

| Ecosystem | Third-party dependencies |
| --- | --- |
| Go | 178 |
| Rust | 44 |
| TypeScript | 0 |

Distinct licenses: 9 (see `LICENSES/`).

## Dependencies

| Component | Version | Ecosystem | Compiled into | SPDX | License text |
| --- | --- | --- | --- | --- | --- |
| `dario.cat/mergo` | `v1.0.1` | go | cli-host, wasm-provider-guests | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `github.com/BurntSushi/toml` | `v1.6.0` | go | wasm-provider-guests | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/MakeNowJust/heredoc` | `v1.0.0` | go | cli-host | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/Masterminds/goutils` | `v1.1.1` | go | cli-host, wasm-provider-guests | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/Masterminds/semver/v3` | `v3.4.0` | go | cli-host, wasm-provider-guests | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/Masterminds/sprig/v3` | `v3.3.0` | go | cli-host, wasm-provider-guests | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/ProtonMail/go-crypto` | `v1.1.6` | go | cli-host | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `github.com/agext/levenshtein` | `v1.2.1` | go | wasm-provider-guests | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/apparentlymart/go-textseg/v15` | `v15.0.0` | go | wasm-provider-guests | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/asaskevich/govalidator` | `v0.0.0-20230301143203-a9d515a09cc2` | go | cli-host | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/awslabs/goformation/v7` | `v7.14.9` | go | wasm-provider-guests | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/blang/semver/v4` | `v4.0.0` | go | cli-host | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/blang/semver` | `v3.5.1+incompatible` | go | cli-host | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/cenkalti/backoff/v5` | `v5.0.3` | go | cli-host | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/cespare/xxhash/v2` | `v2.3.0` | go | cli-host | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/chai2010/gettext-go` | `v1.0.2` | go | cli-host | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `github.com/cloudflare/circl` | `v1.6.3` | go | cli-host | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `github.com/containerd/containerd` | `v1.7.30` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/containerd/errdefs` | `v1.0.0` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/containerd/log` | `v0.1.0` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/containerd/platforms` | `v0.2.1` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/coreos/go-oidc/v3` | `v3.17.0` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/cyberphone/json-canonicalization` | `v0.0.0-20241213102144-19d51d7fe467` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/cyphar/filepath-securejoin` | `v0.6.1` | go | cli-host, wasm-provider-guests | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `github.com/davecgh/go-spew` | `v1.1.2-0.20180830191138-d8f796af33cc` | go | cli-host, wasm-provider-guests | `ISC` | [ISC](LICENSES/ISC.txt) |
| `github.com/digitorus/pkcs7` | `v0.0.0-20230818184609-3a137a874352` | go | cli-host | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/digitorus/timestamp` | `v0.0.0-20231217203849-220c5c2851b7` | go | cli-host | `BSD-2-Clause` | [BSD-2-Clause](LICENSES/BSD-2-Clause.txt) |
| `github.com/dylibso/observe-sdk/go` | `v0.0.0-20240819160327-2d926c5d788a` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/emicklei/go-restful/v3` | `v3.12.2` | go | cli-host, wasm-provider-guests | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/emirpasic/gods` | `v1.18.1` | go | cli-host | `BSD-2-Clause` | [BSD-2-Clause](LICENSES/BSD-2-Clause.txt) |
| `github.com/evanphx/json-patch` | `v5.9.11+incompatible` | go | cli-host | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `github.com/exponent-io/jsonpath` | `v0.0.0-20210407135951-1de76d718b3f` | go | cli-host | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/extism/go-pdk` | `v1.1.3` | go | wasm-provider-guests | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `github.com/extism/go-sdk` | `v1.7.1` | go | cli-host | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `github.com/fxamacker/cbor/v2` | `v2.9.0` | go | cli-host, wasm-provider-guests | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/go-errors/errors` | `v1.4.2` | go | cli-host | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/go-git/gcfg` | `v1.5.1-0.20230307220236-3a3c6141e376` | go | cli-host | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `github.com/go-git/go-billy/v5` | `v5.9.0` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-git/go-git/v5` | `v5.19.1` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-jose/go-jose/v4` | `v4.1.4` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-logr/logr` | `v1.4.3` | go | cli-host, wasm-provider-guests | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-logr/stdr` | `v1.2.2` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/analysis` | `v0.25.2` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/errors` | `v0.22.7` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/jsonpointer` | `v0.23.1` | go | cli-host, wasm-provider-guests | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/jsonreference` | `v0.21.6` | go | cli-host, wasm-provider-guests | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/loads` | `v0.23.3` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/runtime/server-middleware` | `v0.30.0` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/runtime` | `v0.32.3` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/spec` | `v0.22.5` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/strfmt` | `v0.26.3` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/swag/cmdutils` | `v0.26.0` | go | cli-host, wasm-provider-guests | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/swag/conv` | `v0.26.0` | go | cli-host, wasm-provider-guests | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/swag/fileutils` | `v0.26.0` | go | cli-host, wasm-provider-guests | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/swag/jsonname` | `v0.26.0` | go | cli-host, wasm-provider-guests | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/swag/jsonutils` | `v0.26.0` | go | cli-host, wasm-provider-guests | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/swag/loading` | `v0.26.0` | go | cli-host, wasm-provider-guests | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/swag/mangling` | `v0.26.0` | go | cli-host, wasm-provider-guests | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/swag/netutils` | `v0.26.0` | go | cli-host, wasm-provider-guests | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/swag/stringutils` | `v0.26.0` | go | cli-host, wasm-provider-guests | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/swag/typeutils` | `v0.26.0` | go | cli-host, wasm-provider-guests | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/swag/yamlutils` | `v0.26.0` | go | cli-host, wasm-provider-guests | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/swag` | `v0.26.0` | go | cli-host, wasm-provider-guests | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-openapi/validate` | `v0.25.3` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/go-viper/mapstructure/v2` | `v2.5.0` | go | cli-host | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/gobwas/glob` | `v0.2.3` | go | cli-host, wasm-provider-guests | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/golang/groupcache` | `v0.0.0-20241129210726-2c02b8208cf8` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/google/btree` | `v1.1.3` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/google/certificate-transparency-go` | `v1.3.3` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/google/gnostic-models` | `v0.7.0` | go | cli-host, wasm-provider-guests | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/google/go-containerregistry` | `v0.21.6` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/google/jsonschema-go` | `v0.4.3` | go | cli-host | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/google/uuid` | `v1.6.0` | go | cli-host, wasm-provider-guests | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `github.com/gregjones/httpcache` | `v0.0.0-20190611155906-901d90724c79` | go | cli-host | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/grpc-ecosystem/grpc-gateway/v2` | `v2.29.0` | go | cli-host | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `github.com/hashicorp/errwrap` | `v1.1.0` | go | cli-host | `MPL-2.0` | [MPL-2.0](LICENSES/MPL-2.0.txt) |
| `github.com/hashicorp/go-cleanhttp` | `v0.5.2` | go | cli-host | `MPL-2.0` | [MPL-2.0](LICENSES/MPL-2.0.txt) |
| `github.com/hashicorp/go-multierror` | `v1.1.1` | go | cli-host | `MPL-2.0` | [MPL-2.0](LICENSES/MPL-2.0.txt) |
| `github.com/hashicorp/go-retryablehttp` | `v0.7.8` | go | cli-host | `MPL-2.0` | [MPL-2.0](LICENSES/MPL-2.0.txt) |
| `github.com/hashicorp/hcl/v2` | `v2.24.0` | go | wasm-provider-guests | `MPL-2.0` | [MPL-2.0](LICENSES/MPL-2.0.txt) |
| `github.com/huandu/xstrings` | `v1.5.0` | go | cli-host, wasm-provider-guests | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/ianlancetaylor/demangle` | `v0.0.0-20240805132620-81f5be970eca` | go | cli-host | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `github.com/in-toto/attestation` | `v1.2.0` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/in-toto/in-toto-golang` | `v0.11.0` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/jbenet/go-context` | `v0.0.0-20150711004518-d14ea06fba99` | go | cli-host | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/jedisct1/go-minisign` | `v0.0.0-20211028175153-1c139d1cc84b` | go | cli-host | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/json-iterator/go` | `v1.1.12` | go | cli-host, wasm-provider-guests | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/kevinburke/ssh_config` | `v1.2.0` | go | cli-host | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/klauspost/compress` | `v1.18.6` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/klauspost/cpuid/v2` | `v2.3.0` | go | cli-host | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/liggitt/tabwriter` | `v0.0.0-20181228230101-89fcab3d43de` | go | cli-host | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `github.com/mitchellh/copystructure` | `v1.2.0` | go | cli-host, wasm-provider-guests | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/mitchellh/go-wordwrap` | `v1.0.1` | go | cli-host, wasm-provider-guests | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/mitchellh/reflectwalk` | `v1.0.2` | go | cli-host, wasm-provider-guests | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/moby/term` | `v0.5.2` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/modern-go/concurrent` | `v0.0.0-20180306012644-bacd9c7ef1dd` | go | cli-host, wasm-provider-guests | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/modern-go/reflect2` | `v1.0.3-0.20250322232337-35a7c28c31ee` | go | cli-host, wasm-provider-guests | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/monochromegane/go-gitignore` | `v0.0.0-20200626010858-205db1a8cc00` | go | cli-host | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/munnerz/goautoneg` | `v0.0.0-20191010083416-a7dc8b61c822` | go | cli-host, wasm-provider-guests | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `github.com/oklog/ulid/v2` | `v2.1.1` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/opencontainers/go-digest` | `v1.0.0` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/opencontainers/image-spec` | `v1.1.1` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/peterbourgon/diskv` | `v2.0.1+incompatible` | go | cli-host | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/pjbgf/sha1cd` | `v0.6.0` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/pkg/browser` | `v0.0.0-20240102092130-5ac0b6a4141c` | go | cli-host | `BSD-2-Clause` | [BSD-2-Clause](LICENSES/BSD-2-Clause.txt) |
| `github.com/pkg/errors` | `v0.9.1` | go | cli-host, wasm-provider-guests | `BSD-2-Clause` | [BSD-2-Clause](LICENSES/BSD-2-Clause.txt) |
| `github.com/pmezard/go-difflib` | `v1.0.1-0.20181226105442-5d4384ee4fb2` | go | cli-host | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `github.com/russross/blackfriday/v2` | `v2.1.0` | go | cli-host | `BSD-2-Clause` | [BSD-2-Clause](LICENSES/BSD-2-Clause.txt) |
| `github.com/santhosh-tekuri/jsonschema/v6` | `v6.0.2` | go | wasm-provider-guests | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/sassoftware/relic` | `v7.2.1+incompatible` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/secure-systems-lab/go-securesystemslib` | `v0.11.0` | go | cli-host | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/sergi/go-diff` | `v1.4.0` | go | cli-host | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/shibumi/go-pathspec` | `v1.3.0` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/shopspring/decimal` | `v1.4.0` | go | cli-host, wasm-provider-guests | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/sigstore/protobuf-specs` | `v0.5.1` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/sigstore/rekor-tiles/v2` | `v2.2.2-0.20260601073857-5d098a2b6443` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/sigstore/rekor` | `v1.5.2` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/sigstore/sigstore-go` | `v1.2.1` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/sigstore/sigstore` | `v1.10.8` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/sigstore/timestamp-authority/v2` | `v2.1.2` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/sirupsen/logrus` | `v1.9.4` | go | cli-host | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/skeema/knownhosts` | `v1.3.1` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/spf13/cast` | `v1.10.0` | go | cli-host, wasm-provider-guests | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/spf13/cobra` | `v1.10.2` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/spf13/pflag` | `v1.0.10` | go | cli-host | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `github.com/tetratelabs/wabin` | `v0.0.0-20230304001439-f6f874872834` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/tetratelabs/wazero` | `v1.9.0` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/theupdateframework/go-tuf/v2` | `v2.4.2-0.20260407074541-7e8f69f906ef` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/theupdateframework/go-tuf` | `v0.7.0` | go | cli-host | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `github.com/transparency-dev/formats` | `v0.1.1` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/transparency-dev/merkle` | `v0.0.2` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/x448/float16` | `v0.8.4` | go | cli-host, wasm-provider-guests | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/xanzy/ssh-agent` | `v0.3.3` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `github.com/xlab/treeprint` | `v1.2.0` | go | cli-host | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/youmark/pkcs8` | `v0.0.0-20240726163527-a2c0da244d78` | go | cli-host | `MIT` | [MIT](LICENSES/MIT.txt) |
| `github.com/zclconf/go-cty` | `v1.16.3` | go | wasm-provider-guests | `MIT` | [MIT](LICENSES/MIT.txt) |
| `go.opentelemetry.io/auto/sdk` | `v1.2.1` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `go.opentelemetry.io/otel/metric` | `v1.44.0` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `go.opentelemetry.io/otel/trace` | `v1.44.0` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `go.opentelemetry.io/otel` | `v1.44.0` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `go.opentelemetry.io/proto/otlp` | `v1.7.1` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `go.yaml.in/yaml/v2` | `v2.4.4` | go | cli-host, wasm-provider-guests | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `go.yaml.in/yaml/v3` | `v3.0.4` | go | cli-host, wasm-provider-guests | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `golang.org/x/crypto` | `v0.52.0` | go | cli-host, wasm-provider-guests | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `golang.org/x/mod` | `v0.36.0` | go | cli-host | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `golang.org/x/net` | `v0.55.0` | go | cli-host, wasm-provider-guests | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `golang.org/x/oauth2` | `v0.36.0` | go | cli-host, wasm-provider-guests | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `golang.org/x/sync` | `v0.20.0` | go | cli-host | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `golang.org/x/sys` | `v0.45.0` | go | cli-host | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `golang.org/x/term` | `v0.43.0` | go | cli-host, wasm-provider-guests | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `golang.org/x/text` | `v0.37.0` | go | cli-host, wasm-provider-guests | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `golang.org/x/time` | `v0.15.0` | go | cli-host, wasm-provider-guests | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `google.golang.org/genproto/googleapis/api` | `v0.0.0-20260526163538-3dc84a4a5aaa` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `google.golang.org/genproto/googleapis/rpc` | `v0.0.0-20260523011958-0a33c5d7ca68` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `google.golang.org/grpc` | `v1.81.1` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `google.golang.org/protobuf` | `v1.36.11` | go | cli-host, wasm-provider-guests | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `gopkg.in/evanphx/json-patch.v4` | `v4.13.0` | go | cli-host | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `gopkg.in/inf.v0` | `v0.9.1` | go | cli-host, wasm-provider-guests | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `gopkg.in/warnings.v0` | `v0.1.2` | go | cli-host | `BSD-2-Clause` | [BSD-2-Clause](LICENSES/BSD-2-Clause.txt) |
| `gopkg.in/yaml.v3` | `v3.0.1` | go | wasm-provider-guests | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `helm.sh/helm/v3` | `v3.20.2` | go | cli-host, wasm-provider-guests | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `k8s.io/apiextensions-apiserver` | `v0.35.1` | go | cli-host, wasm-provider-guests | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `k8s.io/apimachinery` | `v0.35.1` | go | cli-host, wasm-provider-guests | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `k8s.io/api` | `v0.35.1` | go | cli-host, wasm-provider-guests | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `k8s.io/cli-runtime` | `v0.35.1` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `k8s.io/client-go` | `v0.35.1` | go | cli-host, wasm-provider-guests | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `k8s.io/component-base` | `v0.35.1` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `k8s.io/klog/v2` | `v2.140.0` | go | cli-host, wasm-provider-guests | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `k8s.io/kube-openapi` | `v0.0.0-20250910181357-589584f1c912` | go | cli-host, wasm-provider-guests | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `k8s.io/kubectl` | `v0.35.1` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `k8s.io/utils` | `v0.0.0-20251002143259-bc988d571ff4` | go | cli-host, wasm-provider-guests | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `oras.land/oras-go/v2` | `v2.6.0` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `sigs.k8s.io/json` | `v0.0.0-20250730193827-2d320260d730` | go | cli-host, wasm-provider-guests | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `sigs.k8s.io/kustomize/api` | `v0.20.1` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `sigs.k8s.io/kustomize/kyaml` | `v0.20.1` | go | cli-host | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `sigs.k8s.io/randfill` | `v1.0.0` | go | cli-host, wasm-provider-guests | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `sigs.k8s.io/structured-merge-diff/v6` | `v6.3.0` | go | cli-host, wasm-provider-guests | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `sigs.k8s.io/yaml` | `v1.6.0` | go | cli-host, wasm-provider-guests | `MIT` | [MIT](LICENSES/MIT.txt) |
| `aho-corasick` | `1.1.4` | rust | wasm-provider-guest (publishing-linter) | `Unlicense OR MIT` | [MIT](LICENSES/MIT.txt), [Unlicense](LICENSES/Unlicense.txt) |
| `anyhow` | `1.0.102` | rust | wasm-provider-guest (publishing-linter) | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `autocfg` | `1.5.0` | rust | wasm-provider-guest (publishing-linter) | `Apache-2.0 OR MIT` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `base64` | `0.22.1` | rust | wasm-provider-guest (publishing-linter) | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `bytemuck` | `1.25.0` | rust | wasm-provider-guest (publishing-linter) | `Zlib OR Apache-2.0 OR MIT` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt), [Zlib](LICENSES/Zlib.txt) |
| `bytes` | `1.11.1` | rust | wasm-provider-guest (publishing-linter) | `MIT` | [MIT](LICENSES/MIT.txt) |
| `either` | `1.16.0` | rust | wasm-provider-guest (publishing-linter) | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `equivalent` | `1.0.2` | rust | wasm-provider-guest (publishing-linter) | `Apache-2.0 OR MIT` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `extism-convert` | `1.21.0` | rust | wasm-provider-guest (publishing-linter) | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `extism-convert-macros` | `1.21.0` | rust | wasm-provider-guest (publishing-linter) | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `extism-manifest` | `1.21.0` | rust | wasm-provider-guest (publishing-linter) | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `extism-pdk` | `1.4.1` | rust | wasm-provider-guest (publishing-linter) | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `extism-pdk-derive` | `1.4.1` | rust | wasm-provider-guest (publishing-linter) | `BSD-3-Clause` | [BSD-3-Clause](LICENSES/BSD-3-Clause.txt) |
| `hashbrown` | `0.17.1` | rust | wasm-provider-guest (publishing-linter) | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `indexmap` | `2.14.0` | rust | wasm-provider-guest (publishing-linter) | `Apache-2.0 OR MIT` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `itertools` | `0.14.0` | rust | wasm-provider-guest (publishing-linter) | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `itoa` | `1.0.18` | rust | wasm-provider-guest (publishing-linter) | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `manyhow` | `0.11.4` | rust | wasm-provider-guest (publishing-linter) | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `manyhow-macros` | `0.11.4` | rust | wasm-provider-guest (publishing-linter) | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `memchr` | `2.8.0` | rust | wasm-provider-guest (publishing-linter) | `Unlicense OR MIT` | [MIT](LICENSES/MIT.txt), [Unlicense](LICENSES/Unlicense.txt) |
| `num-traits` | `0.2.19` | rust | wasm-provider-guest (publishing-linter) | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `proc-macro-crate` | `3.5.0` | rust | wasm-provider-guest (publishing-linter) | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `proc-macro-utils` | `0.10.0` | rust | wasm-provider-guest (publishing-linter) | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `proc-macro2` | `1.0.106` | rust | wasm-provider-guest (publishing-linter) | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `prost` | `0.14.3` | rust | wasm-provider-guest (publishing-linter) | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `prost-derive` | `0.14.3` | rust | wasm-provider-guest (publishing-linter) | `Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt) |
| `quote` | `1.0.45` | rust | wasm-provider-guest (publishing-linter) | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `regex` | `1.12.3` | rust | wasm-provider-guest (publishing-linter) | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `regex-automata` | `0.4.14` | rust | wasm-provider-guest (publishing-linter) | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `regex-syntax` | `0.8.10` | rust | wasm-provider-guest (publishing-linter) | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `rmp` | `0.8.15` | rust | wasm-provider-guest (publishing-linter) | `MIT` | [MIT](LICENSES/MIT.txt) |
| `rmp-serde` | `1.3.1` | rust | wasm-provider-guest (publishing-linter) | `MIT` | [MIT](LICENSES/MIT.txt) |
| `serde` | `1.0.228` | rust | wasm-provider-guest (publishing-linter) | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `serde_core` | `1.0.228` | rust | wasm-provider-guest (publishing-linter) | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `serde_derive` | `1.0.228` | rust | wasm-provider-guest (publishing-linter) | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `serde_json` | `1.0.149` | rust | wasm-provider-guest (publishing-linter) | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `smallvec` | `1.15.1` | rust | wasm-provider-guest (publishing-linter) | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `syn` | `2.0.117` | rust | wasm-provider-guest (publishing-linter) | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `toml_datetime` | `1.1.1+spec-1.1.0` | rust | wasm-provider-guest (publishing-linter) | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `toml_edit` | `0.25.11+spec-1.1.0` | rust | wasm-provider-guest (publishing-linter) | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `toml_parser` | `1.1.2+spec-1.1.0` | rust | wasm-provider-guest (publishing-linter) | `MIT OR Apache-2.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt) |
| `unicode-ident` | `1.0.24` | rust | wasm-provider-guest (publishing-linter) | `(MIT OR Apache-2.0) AND Unicode-3.0` | [Apache-2.0](LICENSES/Apache-2.0.txt), [MIT](LICENSES/MIT.txt), [Unicode-3.0](LICENSES/Unicode-3.0.txt) |
| `winnow` | `1.0.3` | rust | wasm-provider-guest (publishing-linter) | `MIT` | [MIT](LICENSES/MIT.txt) |
| `zmij` | `1.0.21` | rust | wasm-provider-guest (publishing-linter) | `MIT` | [MIT](LICENSES/MIT.txt) |

## TypeScript

No TypeScript dependencies are compiled into the CLI. The CLI ships no
TypeScript-derived artifact; TypeScript plugin code in the plugin
development kit is example material, not part of any shipped CLI artifact.
