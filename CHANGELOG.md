# Changelog

This file lists the releases of the Aggrega compiler, its documentation, its editor extension and its plugin libraries.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.10.4] - 2026-11-26

### Added

- The documentation gains a translated Changelog page beside the Roadmap, under "Changelog and roadmap", listing each release's changes.
- Each final release's Windows installer is submitted to the Microsoft Store listing automatically; a dispatch submits an already released version, with a dry run first.
- Each GitHub release page's changes come from aggrega-cli's `CHANGELOG.md`, which lists the changes of every release.
- `aggrega import` drafts an Aggregate, Release and Manifest from an Azure Resource Manager template, recognising Azure Verified Modules and drafting uncatalogued resources.
- An importer can declare the target its drafts default to; an ARM import builds for `arm` without `--target`.
- The Go importer kit attaches secrets in the forms the `arm`, `cloudformation` and `terraform` targets take.

### Changed

- A refused JSON-with-comments source is named by its line and column.

### Fixed

- A JSON-with-comments source that opens with a byte-order mark parses.
- A sourced CloudFormation template's unquoted dates, such as `AWSTemplateFormatVersion: 2010-09-09`, reach the stack as written.

## [0.10.3] - 2026-11-24

### Added

- The home site, documentation, catalog, editor extension and demo-video subtitles are in Thai.
- `aggrega draft --example terraform-cloud-run-cloud-sql` writes a Google Cloud starter: a Cloud Run server reading a Cloud SQL database, built with Terraform.
- Each release's Windows installer also downloads from a fixed address, `https://download.aggrega.ai/aggrega-cli/v<version>/`.
- The documentation's roadmap marks each item done, ongoing or not done with a green tick, a half-filled box or an empty box.
- `install.sh` and `install.ps1` show the binary's download progress when run in a terminal; captured or redirected runs print what they printed before.

### Fixed

- Tooltips on the catalog and home sites no longer show a stray "�" in Vietnamese, Chinese, Hindi and other non-Latin languages.
- The catalog search keeps a query written in any script: a non-Latin or accented word finds what holds it instead of every entity.
- A build stopped by an authoring error no longer shows its progress bar at 100%; the bar stops short and the failed step is marked.
- A JSON-with-comments source that opens with a UTF-8 byte-order mark now parses.

## [0.10.2] - 2026-11-22

### Added

- The catalog site lists Recipes by the target they build for.

### Changed

- Arm and CloudFormation builds refuse a values mapping's literals only when its output changes with a value declared sensitive, not whenever it reads one.

### Fixed

- The documentation search dialog shows its messages ("N results for …", "Load more results") in every published language.
- The documentation's sidebar shows its section and group names in every published language.
- The documentation's footer privacy link and version selector name read in every published language.
- On right-to-left pages of the home site, documentation and catalog, the accessibility button sits at the bottom left, clear of the documentation's sidebar.

### Security

- The Rego governance plugin and the Rust plugin kit use dependency releases that fix a known vulnerability.

## [0.10.1] - 2026-11-21

### Added

- `aggrega push --plugin` takes `--plugin-notices` and `--plugin-sbom` (MCP `plugin_notices`/`plugin_sbom`, language server `pluginNotices`/`pluginSbom`): notices in the manifest, an SPDX SBOM beside it.
- Every plugin Aggrega publishes carries its guest's third-party notices as a manifest layer and an SPDX SBOM as an OCI referrer.
- The home site, documentation, catalog, editor extension and demo-video subtitles are in Traditional Chinese, in Taiwan usage.
- The home site, documentation, catalog, editor extension and demo-video subtitles are in Arabic, laid out right to left.
- Each CLI release attaches its software bill of materials and build provenance you can check with gh attestation verify.
- The documentation's accessibility panel reads in the page's language, in every documentation language.

### Changed

- A component or feature instance compiles at the highest version every constraint on it admits; constraints admitting no common version halt with `conflicting-version-constraints`.
- A library reference's `:version` floor selects the highest held release every consumer admits within its major; a fresh pull may record a newer library version.
- E0168 on an undeclared property names a newer held version declaring it and the constraints holding the instance below that version.
- `aggrega pull --detail` lists each component and feature reference held below the newest version held, and the constraints holding it there.
- Go plugin authors need Go 1.25 or later to build against the plugin kit.
- The `terraform/redis-azure-managed` Recipe at 1.0.2 refuses Azure Managed Redis instance names over 44 characters, the limit that holds in every region.
- Halt messages that name a Manifest, its mappings or its annotation bindings now write Manifest with a capital.

### Fixed

- The editor extension drives the 0.10 compilers: Select Compiler Version offers them and its download installs the newest 0.10 release.

### Security

- The CLI and its plugins move to dependency releases that fix known vulnerabilities.

## [0.10.0] - 2026-11-19

### Added

- The `arm` provider renders Recipes into one Azure Resource Manager template per boundary at resource-group scope, deployable with `az deployment group create`.
- An `arm` Recipe can source a compiled ARM template from git, such as an Azure Verified Modules `main.json`, bound through its values mapping.
- `arm` Recipes and the `arm/key-vault-secret` FeatureRecipe deploy PostgreSQL, Redis, Azure OpenAI, API Management and Container Apps servers and workers on Azure.
- On `arm`, supply secrets as Key Vault references; `core/generated-secret` and `core/random-password` halt by name, because ARM has no secret generator.
- `arm` links a `uses` edge across boundaries by reading the producer's template outputs; deploy each producer first, named as its template's metadata records.
- Boundary annotations `aggrega.ai/azure-subscription` and `aggrega.ai/azure-resource-group` place an `arm` producer in another subscription or resource group.
- Annotate a boundary `aggrega.ai/azure-deployment-scope: subscription` to deploy its `arm` template with `az deployment sub create`, resource-group content nested.
- An `arm` boundary annotated `aggrega.ai/azure-deployment-scope: managementGroup` deploys with `az deployment mg create`, subscription and resource-group content nested.
- `arm` links to producers deployed at subscription and management-group scope; annotate `aggrega.ai/azure-management-group` to place one.
- A contributor can clone aggrega-cli alone and build and test it against the released kit, ABI and schemas modules; tests needing companions skip, saying why.
- A Recipe can source a published Bicep module from an OCI registry with `kind: bicep-module-oci`; the `arm` provider renders its compiled template.
- A Recipe can source an OpenTofu module package from an OCI registry with `kind: terraform-module-oci`; the vendored module applies under Terraform and OpenTofu.
- A Recipe's OCI module source with a tag, digest or query in `url:`, or no `version:`, is refused as it is read.
- The home site, documentation, catalog and demo-video subtitles are in Hindi; the editor extension shows Hindi when VS Code runs in Hindi.
- `aggrega import` drafts a Component and Recipe from a CloudFormation, Azure Resource Manager or Terraform resource schema you export, into your workspace catalog.
- `aggrega version --licenses` prints the third-party notices of everything compiled into the binary; the editor shows them with Show Compiler Third-Party Licenses.
- The Windows installer places `THIRD-PARTY-NOTICES.txt` beside `aggrega.exe` and removes it on uninstall.

### Changed

- An import no longer writes over a Component, Feature, Recipe or FeatureRecipe on disk; pass `--sync` to update one.
- An import with catalog folders also sees the published catalog behind them, as a build does; a `--sync` that cannot read its pinned catalog stops.
- Imports of different formats into one directory each keep their own record, so each can still `--sync`.
- The language pickers on the home site, documentation and catalog name each language without a flag and sit in the top bar on phones.

### Fixed

- A `helm-oci-chart` source naming a non-chart artifact halts with E0140, and a registry server error names its cause, instead of reporting the network unreachable.
- A `helm-oci-chart` source that writes `chart:` now builds after `aggrega pull`, instead of halting with "no lockfile entry".
- A registry's server error halts with E0380, and a transfer that breaks off halts with E0381, for every OCI-fetched source.
- On phones and tablets, the catalog's menu button reaches the documentation, the examples and GitHub.
- No home page scrolls sideways on a phone; a long word in a heading breaks instead.
- The documentation's top bar fits tablets, and every site's language list stays inside the screen with each name on one line.
- Escape closes the home site's and the catalog's menus, as it does the documentation's, and returns the focus to the menu button.

### Security

- Release binaries and their embedded Go plugins are built with Go 1.26.11, fixing standard-library vulnerabilities the CLI could reach.

## [0.9.3] - 2026-11-17

### Added

- A catalog directory may hold several versions of one library; each Release reads the version its references select, and `release.lock` records it with its digest.
- The Rust and TypeScript plugin kits install from crates.io and npm: `cargo add aggrega-pdk-rust`, `npm install @aggrega-ai/aggrega-pdk-typescript`.
- A plugin author can clone aggrega-pdk alone and build, test and run its examples; tests needing aggrega-abi or aggrega-schemas skip, saying why.

### Changed

- SchemaLibraries follow catalog directory order like every library kind: the first directory holding a name wins, and later copies are reported as shadowed.
- Run `aggrega pull` once where library references carry a `:version` floor, so lock rows record the selected version rather than the floor.

### Fixed

- A library reference whose `:version` floor no held version meets halts with E0103, naming the versions held, instead of building another version.

## [0.9.2] - 2026-11-15

### Added

- `aggrega draft` writes a new entity or a runnable example; `aggrega scaffold` still works and is removed in 0.10.0, so use `aggrega draft`.
- The assistant has a `draft` tool for new entities and examples; its `scaffold` name is a deprecated alias removed in 0.10.0.
- The editor has a Draft button that starts an entity, an example, a quickstart or an import, with no Aggrega folder needed.
- The editor's compiler version picker shows the newest release outside its supported range as not compatible, so you know to upgrade the extension.

### Changed

- The editor works with compilers from 0.9.0 up to, not including, 0.10.0.
- A relative `aggrega.server.binaryPath` now resolves against the first workspace folder, for every editor feature.
- The editor's publication and signature labels and cache names now appear in your language.

### Fixed

- The editor's welcome page marks the compiler step done once the configured compiler runs, even before an Aggrega folder is open.

## [0.9.1] - 2026-11-15

### Added

- A catalog publish refuses a Component version no Recipe realizes while the version below it has Recipes.
- The assistant set-up page has a Claude Desktop section: install on your computer, connect through the extension package.

### Fixed

- `aggrega import --sync` re-imports a folder you have built without `--format`: the build's `dist/` folder is not read.
- The task examples resolve `core/compute/task` 1.0.1 again, so a fresh pull finds Recipes for it.
- The editor's `aggrega.server.binaryPath` accepts `~/…` and `${userHome}`, and a failed start names the path it tried.
- The editor's Commands panel shows the attached workspace and Release after being hidden, and re-choosing the current workspace says so.

## [0.9.0] - 2026-11-15

### Added

- `aggrega import` drafts a Release and a complete Manifest, so an imported project builds and deploys without editing the drafts.
- `aggrega import` asks once for what no source holds, at a terminal or through an answers file, `--answers`, by default `import-answers.yaml`.
- `aggrega import` pulls the drafted Release's dependencies, so `validate` and `build` work at once; `--offline` skips the pull.
- Dify, MCP servers run from an image, and the HTTP-request and retrieval tools deploy on Docker Compose.
- On Docker Compose, an import publishes each platform's and agent's port on 127.0.0.1 through `published_port`, and every walkthrough shows how to test it.
- Dify sends nothing to its vendor unless a Release sets `telemetry_enabled` or `marketplace_enabled`; `console_url` names the console's address.
- The editor asks for the values an import still owes, writes them to the answers file and imports again.
- The language server and the assistant's import tool return owed values as `owed` and accept answers as `answered`.
- The Visual Studio Code extension shows its own text in the documentation's languages, and is published as `aggrega-ai.aggrega`.
- Every quickstart, import and example page offers a prompt for an AI assistant and shows the project open in Visual Studio Code.
- The Visual Studio Code manual is its own documentation section, with screenshots and where to install the extension.
- The documentation has a public roadmap page, linked from the home page footer.

### Changed

- An import references the catalog component a source matches strongly, and translates what a source states instead of leaving comments.
- A relative `workspace_root` on an assistant tool call is read from the server's Workspace folder; `scaffold` returns the folder it wrote.
- A development build made after a release candidate reads that candidate's documentation.
- `core/ai-tool/http-request` 1.0.1, `core/ai-tool/retrieval` 1.0.1 and `core/compute/server` 1.1.0 correct descriptions that denied existing Docker Compose realizations.

### Fixed

- A Langflow flow imported from Langflow's starter projects opens in Langflow's editor with all its connections.
- `helm/k8s-existing-secret` refuses an empty Secret name instead of rendering one, and NGINX's Compose health check probes 127.0.0.1.
- The desktop extension's empty Workspace folder message says to turn the extension off and on, or restart the app, after setting it.
- `aggrega build` and `aggrega pull` show each step once on a terminal, and count "1 file" in the singular.

## [0.9.0-rc.7] - 2026-11-13

### Changed

- A first build pulls faster again: the dependencies of a pull are fetched from the registry several at once.
- Go plugins import the plugin kit from `github.com/aggrega-ai/aggrega-pdk/go` and the codec from `github.com/aggrega-ai/aggrega-abi/go`.

## [0.9.0-rc.6] - 2026-11-13

### Added

- The `aggrega-skills` repository installs as a plugin in Claude Code, Codex, Cursor, Copilot, Gemini CLI, Antigravity, Kiro and OpenClaw, adding the skill and `aggrega mcp`.
- One command runs a quickstart step: `curl -fsSL https://aggrega.ai/quickstart-ai.sh | sh`, or `irm https://aggrega.ai/quickstart-ai.ps1 | iex` on Windows; quickstart-web likewise.
- The AI quickstart opens a chat page in the browser from its first step, and quickstart-web serves a page for the browser.
- With no catalog folder set, `aggrega import` fetches the hub's signed catalog snapshot, pins it, and refreshes it with `--upgrade`.

### Changed

- The set-up prompt for AI assistants installs the Aggrega plugin first, and falls back to the skill and a manual MCP connection.
- The installers print a short, clean summary ending with the installed `aggrega version` and the next steps.
- `aggrega scaffold --list` and the examples follow one teaching order, and every example page shows what `aggrega scaffold` prints.
- Importers name their drafts after the source, and `aggrega plugin gen-catalog` writes no licence or copyright lines into your files.
- A first build pulls faster: the trust root is fetched once per run, registry sign-ins are remembered, and signatures are checked several at once.
- The LangGraph Compose Recipe 1.0.1 runs the quickstart agent 1.0.1 by default.
- Comments in the files `aggrega scaffold` writes use plain ASCII, so they read correctly in Windows PowerShell.
- The copyright holder is written Cognitio s.r.l.

### Fixed

- The quickstart agent leaves a refused or failed turn out of its conversation, so the next turn is answered.
- `install.ps1` prints the version line's em dash correctly in Windows PowerShell 5.1.
- The editor no longer shows diagnostics from a validation of older text or from one cancelled partway.

## [0.9.0-rc.3] - 2026-11-11

### Added

- Anyone can read what the hub, `hub.aggrega.ai`, publishes without a login; publishing to it still needs credentials.
- Every import walkthrough ends by running what the build wrote and showing it answer a request.
- A Windows installer installs Aggrega without administrator rights, through a wizard offering the install folder, an Add to PATH choice and the next steps.
- The daemon's plugin-add answer carries `withdrawn: true` when the version you named has been retracted, as the command line says.

### Changed

- Documents, schemas, plugins and published artifacts use `aggrega.ai/v1beta1`, which changes only additively; a document or plugin still on `v1alpha1` is refused.
- Releases promise compatibility from 0.9.0 for schemas, commands, flags, assistant tools, error codes and plugin kits, as the Compatibility page states.
- The plugin kits and ABI codecs follow semantic versioning from 0.9.0, and a change breaking their API ships only in a new minor version.
- Catalog components running your own image deliver each own value under a default variable name, which a component-level `inject:` block can rename; Recipes read `.context.self_inject`.
- An inject block naming a mechanism `all` is refused with its reason, and a schema `not` branch's own description is printed as its refusal.
- Importing a folder no importer recognises suggests importing one of its files, or naming a format whose importer reads folders.
- A build whose catalog holds no parser for a format reads it through the compiler's built-in parser, as an import does.
- Commands show progress while they wait, print the workspace warning only where it applies, and `plugin add cli <name>[:<version>]` works outside a workspace.
- Commands wait as long as a busy registry asks, send it fewer requests at once, and stop by name after five minutes of slow-down answers.
- LangGraph Recipes hand the graph its recursion limit, its checkpointer store's password, and the store's user and database where it publishes them.
- The Claude Desktop package runs the `aggrega` on your PATH and asks you to set an empty Workspace folder.

### Fixed

- Kubernetes accepts builds whose Aggregate name or environment holds `/`; the label is rewritten and the exact value kept as an annotation.
- An unmapped Feature's refusal lists the FeatureRecipes implementing its type for the component's target, or those for other targets when none fits.
- With no catalog directory, the refusal for an unmapped component or Feature says it searched only the lockfile's pins.
- Lockfiles record each entity's kind, keeping same-named entities of different kinds apart, and `pull --detail` names each kind correctly.
- `aggrega pull` fetches and pins the base a Component or Feature extends, so wrappers build and validate without mapping their base.
- A governance plugin at a label or annotation transform role builds from the hub with no catalog directory.
- A verdict policy naming another release of a built-in governance plugin runs the compiler's own engine.
- A Feature attachment's inject entry written with `value:` is refused; write the literal in the component's own block.
- An attachment inject entry whose `from:` names a key its FeatureRecipe does not publish is refused, unless the FeatureRecipe reads `.inject` itself.
- A refusal whose own sentence quotes nothing sensitive is printed, and the duplicate-delivery-key refusal says how to give each attachment its own key.
- Helm compute Recipes refuse a credential Feature they cannot deliver, by name, and Helm secret realizations refuse a Compose-style `${…}` origin.
- Import drafts describe LangGraph images and `=Env.` references correctly, name Agent Framework MCP servers in comments, and leave non-Gemini ADK models unwired.
- The Google ADK import's draft header says ADK also serves some providers' models, such as `claude-…` and `gpt-…` names, through classes of its own.
- Dify built for Kubernetes starts every service, stays up and answers requests; its import step prints Dify's refusal and stops retrying.
- The Dify plugin runtime and LangGraph Compose Recipes run no shell syntax and accept quotes in a connected store's address.
- On Docker Compose, a LangGraph graph starts once its store accepts connections.
- Langflow on Docker Compose can write to its data volume, and Ollama defaults to 0.15.6, serving the Responses API.

## [0.8.11] - 2026-11-06


### Added

- The documentation, catalog and home sites are now published in Italian, German, French, Spanish, Brazilian Portuguese and European Portuguese.
- The home page explains how Aggrega works with translated diagrams and icons, and the documentation adds diagrams of Features, roles and AI gateways.
- A plugin diagnostic's location can name the component it concerns in an `instance` field, kept apart from `uri`, which names a document.
- The documentation's Import section teaches `aggrega import`, with a walkthrough for every format it reads that takes a project to a build.
- Getting started and two examples now show diagrams of how Aggrega works, of one application on many platforms, and of build-time policies.
- The import walkthroughs build without workarounds or a hub login, say where to clone the standard-library catalog, and run CrewAI and LangGraph on Docker Compose.

### Changed

- Compose sidecar and init services are now named `<component>--<container>`, so containers of different components no longer share a name; existing ones are recreated.
- Catalog credential slot descriptions now name the development value a Recipe uses when the slot is unattached, a value every such Release shares.
- A diagnostic about one component now names it as `(instance <name>)` after the file, or `instance <name>` alone, never where a file belongs.

### Fixed

- Imported drafts build without hand repair: each connection gets its own alias, Langflow's OpenAI model is wired, and drafted Releases place every model and datastore.
- `aggrega import` uses its built-in importer when the catalog holds importer entries without their module, and a registry refusal says to re-run the import.
- Compose builds of CrewAI, LangGraph and PostgreSQL accept a credential that only the Release attaches.
- `aggrega validate <entity>` and the assistant's `catalog://` resource now name the broken catalog file skipped at load instead of only reporting the entity not found.
- The RDS Recipes now document `subnet_group`, `parameter_group` and `vpc_security_groups` as override keys shared across AWS Recipes, not Terraform argument names.
- A Recipe with `source: ""` is now refused when loaded, so `pull`, `build` and `validate` all report `E0251`, advising to omit or fill the field.
- `aggrega validate` now refuses a Component or Feature whose socket or slot selector cannot be parsed, even when nothing wires that socket or slot.
- `aggrega lint <plugin.yaml>` now reads the `.wasm` beside the entity, where guest builds now write it, and notes what the entity leaves undeclared.
- An empty `inject: {}` on an attachment filling a slot that permits no delivery now builds inside imported modules and subsystem-wide attachments too.
- A boundary's `subDirectory: "{{ .component_id }}"` now names a boundary-scope Feature attachment's folder after its `aggrega.ai/component-id` label, not an internal marker.
- `aggrega inspect`, `aggrega validate --release` and the assistant's matching tools now refuse an out-of-date lockfile with the build's `E0130` halt.
- A template reading `.inject_raw` now sees a component's custom inject mechanisms, such as `ssm_param`, beside `env`, `file` and `arg`.
- A FeatureRecipe's `valuesMapping` now reads its own `source:` at `.source`, never the source of the component it is attached to.
- `aggrega registry update` shows its progress while it walks a registry, as `build` and `pull` do.
- In the editor, a Manifest mapping fault now appears on the Manifest, at the faulty mapping entry, instead of on the Aggregate.
- The Strimzi and RabbitMQ operators are now workers: broker sockets refuse them at `validate`, while Prometheus's `scrape_target`, `core/prometheus-exporter` and `core/caller-allowlist` accept workers.
- With nothing attached to its backend credential, Langfuse now presents the catalog's development database password and can sign in to a catalog PostgreSQL.
- Helm builds now refuse a component name giving a CronJob over 52 characters or a Service over 63, and shorten a Feature attachment's name instead.

## [0.8.10] - 2026-11-03


### Added

- Diagrams in the documentation are drawn, following the page's light or dark theme.
- Builds now warn when an `aggrega.ai/` annotation reaches a Terraform render, which applies none; provider plugins declare this with `readsNoWellKnownAnnotations: true`.

### Changed

- The catalog site's kind boxes open a filtered Browse, its filters live in the URL, and aggrega-ai's signed entities show as Official under one publisher.
- A listed catalog directory that does not exist, or is not a directory, now halts every command reading the catalog, naming its configuration line.
- Documentation examples list no catalog outside themselves, so a copy of the documentation alone builds them from the hub registry.

### Fixed

- On Docker Compose a service port publishes onto the container port it names; `adminer` keeps serving 8080 while `infra.port` sets its published or Service port.
- `aggrega build` names each file in its output it did not write, in one warning, and counts them on its closing line; nothing is deleted.
- A `plugins.linter` entry, or a verdict-role `plugins.governance` entry, naming a `function:` but no `uri:` now halts the configuration load, naming the entry.

## [0.8.9] - 2026-11-02


### Added

- Attach `core/manual-trigger` to a task to deploy it unstarted on Kubernetes, Docker Compose or Amazon ECS, and start each run yourself.
- Back up through Velero, K8up, KubeStash, Kasten, CloudNativePG, mariadb-operator or AWS Backup, onto a bucket or a volume, with optional point-in-time recovery.
- Two examples: a task run on demand, and one datastore backed up on every target from a single `core/backup` attachment.
- The documentation is published in Chinese, Japanese, Korean, Polish, Turkish, Ukrainian, Indonesian, Vietnamese and Russian, marked machine-translated, with a version selector by minor release.
- aggrega.ai serves a home page introducing Aggrega, with links to the documentation and the catalog.
- `aggrega scaffold --example <name>` writes a documentation example into a new folder, and `--example --list` lists them; the assistant and the editor can too.
- The install scripts print the next steps, and `--completion` and `--skills` (`-Completion`, `-Skills` on Windows) also set up shell completion and the assistant skill.
- The documentation opens with a Start here group: AI assistant pages with a translated set-up prompt, a Getting started page and a Learn Aggrega path.
- Two quickstart examples, a LangGraph agent on a local model and a web app with PostgreSQL, grow step by step from Docker Compose.
- The catalog runs `core/compute/server`, an agent and `core/gateway/litellm` on Docker Compose, with two published quickstart images under `tools/`.
- The hub counts pulls per catalog entity version and records nothing about who pulled; the privacy page states what is counted.
- Translated pages on every site say they are machine-translated, and each language picker names a language in your own language on hover.

### Changed

- `helm/mariadb-cluster` runs MariaDB replication from `infra.ha`, and `helm/mariadb` refuses `infra.ha` by name instead of ignoring it.
- `aggrega registry update` re-reads only what changed, walks registries in parallel, retries failed reads rather than recording versions unsigned, and names unreadable repositories.
- Code blocks in the documentation follow the light or dark theme, on a grey surface with syntax colours that are easier to read.
- `aggrega validate --release` now evaluates Recipe and FeatureRecipe assertions against the Release's values, failing where `aggrega build` would.
- The Start here pages live at `/docs/ai-assistants/`, `/docs/install/` and `/docs/learn/`; their old `/docs/authoring/` addresses redirect permanently.
- Example READMEs give the `aggrega build` commands that work in a copy made with `aggrega scaffold --example`; `./build-release.sh` stays for CI.
- The documentation's language picker is a drop-down menu that works with the arrow keys and Escape.
- The accessibility button floats at the bottom right of every page and no longer shortens the sidebars.
- A pull fetches each manifest once, by the tag or digest it names, and checks its bytes against that digest.

### Fixed

- An unknown command now answers immediately from the local registry index, and says how to install or search for a CLI plugin of that name.
- A value declared sensitive behind a `$ref` reference is now withheld from diagnostics, and a pillar written as a `#/$defs/` reference publishes the definition's keys.
- A connection's `values` and `infra` leaves its socket declares `sensitive` are now withheld from `--ir-graph`, the trace and governance plugins.
- A refusal now withholds a declared-sensitive value read from inside a list, such as a boundary connection's `provides` or `.context.used_by`.
- The trace now shows a component's `.context.inject` block when its only deliveries use a custom mechanism, withholding declared-sensitive values.
- A credential holding a double quote or a backslash now reaches the rendered output intact through the standard catalog's credential Features and Recipes.
- A FeatureRecipe `injectMapping` whose output cannot be read, or writes deliveries under `all:`, now halts the build naming the attachment, instead of silently delivering nothing.
- A bare annotation value its binding cannot place halts with `E0367` where written, and `aggrega validate` reports an unsupported workload input kind with `E0265`.
- On Docker Compose, a volume mount's `subPath` now mounts that part of a persistent volume, and is refused by name on other volume kinds.
- A sourced Docker Compose service keeps every label its file writes, in list form or with number and true/false values, beside the provenance labels.
- An imported module's components now meet the `inject:` allowlist and delivery-key rules, and its subsystem and Release-written attachments may name only declared sockets.
- A Feature an imported module attaches to one of its own subsystems now reaches the module's components there, and meets the Feature's `appliesTo`.
- An `annotation-transform` policy and boundary annotation bindings now reach every component routed in through `aggregates:`, named or not, addressed by graph id like `documents.backend`.
- Platform annotations such as `aggrega.ai/namespace` declared on an Aggregate subsystem or component, or in a module, now reach rendered output; the Release's value wins.
- Registry access reports a malformed credential entry or `config.json`, and `aggrega login` and `aggrega logout` a malformed `config.json`, naming registry and file, never contents.
- On Docker Compose, a credential attached to an external service, as on `compose/twilio`, is refused by name; its slots are optional there, required on Kubernetes.
- A build reaching a Component or Feature whose `extends:` chain loops now halts with the cyclic extends chain error instead of crashing.
- Diagnostics and lockfile references name top-level components at `.spec.components`; findings on imported module components point at the written key in the Release or module.
- An imported module's attachment whose delivery its slot refuses now halts saying how to fix it, within the mechanisms a slot can declare.
- `compose/langflow` and `helm/langflow` keep Langflow's data at `/app/langflow`, the directory its image creates, so Docker Compose volumes are writable by Langflow.
- `helm/langflow`, `helm/flowise` and `compose/flowise` keep data writable by the non-root user current images run as; Flowise's data moves to `/home/node/.flowise`.
- A pull's unfinished deprecation-marker lookup is now reported as a lookup that did not complete, with its reason, not as a marker not honored.
- A schema library only the artifact cache holds is now named, with the action that works there, by `validate`, entity validation, `pull` and `build --auto-pull`.
- The public catalog's Capability filter now lists each plugin's published capabilities, and plugin pages show their plugin type and drop two always-empty rows.
- A reference that resolves to nothing now names any catalog file declaring an Aggrega apiVersion that does not parse, with the parser's reason.
- The editor, the assistant's `describe` and `validate`, and `inspect --closure` now report a withdrawn plugin recorded in the plugin trust record, as the build does.
- `aggrega inspect` prints a catalog Component as YAML under `-o yaml`, and draws extending Releases and Manifests as a build composes them.
- `aggrega deprecate --from` and the daemon's `aggrega/deprecateArtifact` now refuse a severity, reason, replacement or message passed beside the batch file, instead of ignoring it.
- An `aggrega registry update` stopped while writing an index now leaves it complete or absent, never a partial index shown as current.
- A schema halt over content read through `$import:` now names the imported file beside its line, instead of the entity's file.
- A top-level template field holding nothing renders empty; `$.` ranges match dot ranges; `(.locals).key` halts like `.locals.key`; printing and ranging unset paths halts.
- On Docker Compose, the build now warns about a persistent volume's dropped `class` or `accessModes`, and a `uses` edge into a later sync wave.
- Warnings for an inline `x-aggrega-*` schema keyword and for an unverified plugin now print once, untimestamped, beside a build's other warnings, and reach the editor.
- A Recipe or FeatureRecipe whose pipeline repeats a step `id` is now refused with `E0370`, naming both steps' lines.
- `aggrega validate` and `aggrega lint` halt with `E0371` on a catalog file with a misspelled `kind:`, and builds name it when its entity is missing.
- Managed database Recipes refuse a password attachment the cloud service would ignore, and PostgreSQL and MariaDB Recipes refuse `infra.credentials` settings they cannot honour.
- A failed `provides:` entry now shows the condition that stopped it beneath `E0056`.
- `aggrega plugin add provider` now writes `provider/<name>`, which the next pull resolves; an `oci://` provider binding is refused naming the form to write.
- A refused workspace configuration now reads "workspace configuration is invalid" instead of claiming a schema failure where the schema passed.

## [0.8.8] - 2026-10-29


### Added

- Boundary-scope FeatureRecipe templates can read `.boundary.assembly_path`, the directory their own output is assembled into.
- A Compose service can list component ids under `x-aggrega-networks-of` to join those components' networks in its own compose file.

### Changed

- A FeatureRecipe's injectMapping reads the component it runs for at either execution scope, and a `.boundary` read in it halts the build.
- A boundary-scope FeatureRecipe's locals and provides are evaluated once for its boundary, so every component reads the same values, `.boundary` reads included.
- The build halts when a boundary-level Feature's compose or CloudFormation output would reach no assembled file, where it was dropped without a message.
- The compose bridge sets a connection's host variable to `aggrega-gw` and its port variable to the listen port, relaying only within its own file.
- Assertions read an authored number as value templates do: an authored `0` is empty to `if` and `default`, and `0.1` equals the literal `0.1`.
- Every model Component refuses a `context_window` below 1 when its values are validated; leave it out to keep the model's default.
- The Plugin, Recipe, Workload and compact-form reference pages now describe the forms a build accepts and what `gen-catalog` records.
- An entity lock names its ignore rules' plugin without a version, so re-pinning with another release leaves it unchanged; delete older locks and pin again.
- Provider plugins no longer carry the compiler's WebAssembly runtime, halving most of them, and `aggrega plugin run-local` now starts the helm plugin.

### Removed

- Error `E0322` is retired: a boundary-level Feature's provider plugin no longer rewrites consumers' environment; the compose bridge's own `injectMapping` does.
- The provider render socket's `boundary_env_overrides` export and its two wire shapes are removed from the schema, the ABI codecs and the plugin kits.

## [0.8.7] - 2026-10-29


### Added

- The documentation site publishes `llms.txt` and `llms-full.txt`, a page map and the full text for AI assistants, at `aggrega.ai/llms.txt`.
- Install Aggrega with one command, `curl -fsSL https://aggrega.ai/install.sh | sh` or `irm https://aggrega.ai/install.ps1 | iex`, into a per-user folder without administrator rights.
- `aggrega skills install --agent <assistant>` adds the Aggrega skill to your AI assistant, and one documented prompt has the assistant set Aggrega up.
- The editor extension downloads the compiler when none is found, checked against the release's checksums, lets you choose its version and never replaces yours.
- Install Aggrega with Homebrew on macOS and Linux (`brew install aggrega-ai/tap/aggrega`) or with Scoop on Windows (`scoop install aggrega` after adding the Aggrega bucket).
- The tooling image `hub.aggrega.ai/tools/aggrega` serves Linux on ARM (`linux/arm64`) as well as x86-64, under the same tags.
- Install Aggrega on Windows with a signed installer, `aggrega-<version>-windows-amd64-setup.exe`, or with `winget install Cognitio.Aggrega`, per user, without administrator rights.
- Every public Aggrega repository, the published `aggrega-skills` included, carries the Apache License 2.0 text at its root.
- Boundary-scope FeatureRecipe templates read each connection endpoint's `source_assembly_path` and `target_assembly_path`, the directory its output is assembled into.
- Governance examples: a render-verdict policy judging four targets in Rego and CEL, label and annotation transforms, and organization-wide policy through `governance_append`.
- Documentation adds a reference page for the built-in tagging plugins `label-injector` and `selector-tagger`, and a walkthrough for writing, testing and attaching a governance plugin.
- The governance page states the path a render-verdict policy reads a Terraform resource at, and teaches testing a policy before it gates a build.
- Each plugin kit's governance skeleton ships one payload file per role, for `aggrega plugin run-local`.
- The tutorial's programmable-policies example attaches runnable Rego policies, and the examples index now lists examples that name no product.
- A `label-transform` plugin receives each node's declared `values`, sensitive ones withheld, so a configuration such as an enabled vector extension can earn a capability label.
- An `annotation-transform` plugin can inject an object of subkeys as well as a string, and a refused injected annotation names the plugin that injected it.
- `aggrega build --policy-input <dir>` writes the exact payload each governance role hands its plugin, so you can test a policy offline.
- A `form: scheduled` Workload can declare `suspend: true`, rendering a CronJob on helm that runs only when someone starts a Job from it.
- Run the catalog's databases, caches, search and messaging as managed AWS services: RDS, ElastiCache, OpenSearch Service, Amazon MSK and Amazon MQ.
- Run generic servers, workers, tasks and functions on Amazon ECS Fargate, scheduled ECS tasks, AWS Lambda, Google Cloud Run and Azure Container Apps.
- Use Cloud SQL, Memorystore, Cloud Storage, Azure Database for PostgreSQL flexible server, Azure Managed Redis and Azure OpenAI models through Terraform.
- Run the upstream Helm charts of KEDA, MariaDB, MySQL, Strimzi, n8n, LibreChat, Langflow, LiteLLM, Weaviate, Jaeger, Fluentd and NVIDIA NIM.
- Serve models with Docker Model Runner on Compose or Helm, and NVIDIA NIM on Compose with a GPU reservation.
- A Release can opt a Kafka Component in to encrypted client connections, realized by every Kafka Recipe.
- Agents, tools and flow platforms take the connection variables the author dictates through `inject`, with the catalog's variable names as defaults.
- A helm FeatureRecipe reads its host's pod volumes, claim templates and container volume mounts in the `pod_template_volumes` render fact.
- An `omits` entry can name the `subchart` a chart's document came from, leaving out one of two same-named copies and keeping the other.
- `aggrega sign --jobs N` (and `jobs` on the daemon and assistant tool) signs up to N references at once; rate-limited keyless signing services are retried.

### Changed

- Releases attach the binary without debug information as `aggrega-<version>-…`, the debug build as `aggrega-debug-…`, Linux ARM binaries, and `SHA256SUMS`.
- Downloaded macOS binaries run without removing the quarantine mark: they are signed with Cognitio s.r.l.'s Developer ID and notarized by Apple.
- A source URL carrying a credential is refused; Helm chart repositories, compose fragments and Git repositories over HTTP(S) read credentials from the netrc file.
- The compose provider warns when it drops a Workload's `podSecurityContext.fsGroup` or `supplementalGroups`, as it already does for other fields it cannot carry.
- A `required: true` on a property inside an explicit `properties:` block is now enforced, so a value missing that property is refused.
- A `$ref` pointer into a property resolves from its own pillar, as `#/$defs/` does, and failures through it name the field holding the pointer.
- Publishing a Plugin, and `aggrega validate --plugin`, refuse a capability list naming a capability the plugin's own describe() does not report.
- A component's own `inject:` block writing one delivery key twice under one mechanism halts the build, naming the mechanism and the key.
- A `value:` entry in a `uses` connection's `inject:` list halts the build, naming the entry; a connection delivers the target's published keys, named with `from:`.
- The compose provider warns when it drops a container's `allowPrivilegeEscalation`, or a `podSecurityContext.runAsGroup` written without `runAsUser`.
- Compose runs sidecars and init containers as the pod's user and warns on a container `runAsGroup` without `runAsUser`; shape errors name the field written.
- A `sensitive: true` written inside a `$jsonschema:` block, or in properties beside a `$ref`, now masks that value wherever Aggrega withholds sensitive data.
- The compose provider now warns when it drops a health probe's `successThreshold`, which a compose healthcheck cannot express.
- `aggrega validate <recipe>` now prints the short code of a failed assertion in its warning, as a build does.
- Keyed signing and the pinned-key check now refuse an ECDSA key on any curve but P-256, naming the curve.
- A CloudFormation short-form reference now means the same quoted or bare, in a fragment and in a values mapping.
- A value template that fails while it runs now halts with short code E0364, so the failure can be looked up.
- A misspelled subkey in an annotation value now halts with E0363 at the line written, naming the paths its binding routes.
- A governance attachment at a transform role can name the `aggrega` binary's own version, which resolves from the binary as at a verdict role.
- Back up a datastore by attaching `core/backup` in the Release; the postgres, mysql and mariadb Components no longer accept `infra.backup`.
- Value templates read authored numbers as numbers: `if` and `default` treat 0 as empty, and a number compares with number literals, never quoted text.
- A value template prints a decimal number in its shortest exact form, such as `0.0000001`, never in exponent form.
- `core/storage/minio` is retired; use `core/storage/s3-object-store` instead.
- An `inject` delivery must be a list; a map, or an entry carrying both `value` and `from`, now stops the build.
- A compose service's `profiles` now reaches the assembled file, and any other compose key the provider cannot carry stops the build naming it.
- `aggrega plugin run-local` prints a socket export's answer as JSON instead of its binary encoding, and refuses an answer that is not the export's shape.
- A render-verdict policy reads `format: hcl` (or `json`) on every Terraform module file, not only on the boundary's root configuration.
- The governance socket page states when a policy finds `input.settings` at each role, and how a policy tests whether a setting was configured.
- A helm FeatureRecipe attached to a helm-chart component now installs: its child chart gets a `Chart.yaml` and a `templates/rendered.yaml` emitting the rendered manifests.
- A Manifest `name` mapping key naming no component instance or attachment in the build now stops the build, suggesting the name you probably meant.
- `aggrega inspect`, its `--closure` listing and the assistant's tools refuse a reference no catalog holds, even inside a module, exactly as `aggrega validate` does.
- A `uses` target reaching a module's component by a name the module's `exportedComponents` does not list now stops the build, naming what it exports.
- A module's components can reach a module it imports through that module's exported aliases.
- A list parameter declaring `min` or `max` now stops the build when the Release supplies fewer or more items.
- A string parameter's `min` and `max` now bound its length, and `min` or `max` on a boolean or object parameter stops the build.
- A capability version pin never renames a Recipe or FeatureRecipe: a Manifest maps `compose/svc:1.0.0`, and a mapping spelling the pin is refused.
- A Rego policy calling `http.send`, in the compiler's engine or the plugin kit's Rego examples, now stops the build naming it.
- A governance attachment's `settings:` are now checked against the plugin's declared schema at every role, `label-transform` and `annotation-transform` included.
- `aggrega cache status`, `prune`, `evict` and `clear` stop when the workspace configuration file is a link leading out of the workspace, as other commands do.
- `aggrega validate --release` prints each finding with its bracketed head, short code and position, as `build` and `pull` print it.
- A pull refused by a registry now suggests `aggrega login` or `docker login`, the logins whose credentials a pull reads.
- `aggrega validate <component>` and every catalog read refuse a socket `valueSchema` or `infraSchema` that does not compile, not only a Release reaching it.
- A component type, Manifest mapping or `extends:` pin naming an entity the catalog holds at other versions only reports `E0103`, not a missing entity.
- An assertion that stops on a key nothing supplies is now headed `E0033`, as an assertion calling `fail` is.
- A Recipe or FeatureRecipe whose `source:` names a kind no adapter serves is refused with `E0186` as it is read, before anything is fetched.
- `aggrega pull` shows each advisory once, and import diagnostics name the imported file.
- Importer plugins receive the imported file's name in the import request; the Go kit's `importer.Named` names it in diagnostics.
- The `aggrega serve` protocol's TypeScript binding marks `workspaceRoot` required on every request acting on a workspace, as the daemon already enforces.
- Templates receive the annotations an Aggregate writes on its subsystems and components in `.context.annotations.aggregate`, a component's own over its subsystems'.
- Assertions compare a number from `values` with a number written in the template, and refuse comparing a number with text, naming both.
- The provider plugin schema maps each export to its input and output definitions in an `x-aggrega-exports` table; validate a payload against its export's definition.
- A provider plugin receives the parameters, the three annotation buckets and the boundary in its render context, as a pipeline step does.
- A boundary-scope FeatureRecipe renders by its boundary's `aggrega.ai/namespace` and `aggrega.ai/image-registry`, never by one component's Release entry.
- A Manifest mapping key now stops the build when its Recipe does not implement the version of an instance the key reaches.
- Render-verdict warnings print like ir-verdict ones, and `aggrega pull` heads a render failure it survives with its short code.
- An object whose chart writes `metadata:`, `labels:` or `annotations:` with nothing under it now gets the helm provider's labels and annotations like every other object.

### Removed

- Plugin kits drop the IR provenance's always-empty `source_identifiers` field; rebuild a Rust plugin built with an earlier kit, which cannot read newer graphs.
- The `eval` capability is removed: model a service the build does not provision with a `form: reference` workload.
- Provider plugin kits drop the assembly input's `boundary` field, which the compiler never filled.

### Fixed

- A property referencing a non-object SchemaLibrary entry, such as a string with a pattern, now validates values against that entry instead of refusing every value.
- A `$jsonschema:` block accepts ordinary JSON Schema 2020-12, such as `patternProperties` beside `allOf` or a recursive `$dynamicRef`, instead of refusing it.
- Keys beside an in-document `$ref` are read as compact form, and `required: true` under `when:` applies only while the predicate holds.
- A mapping or list written as `default`, `const`, `enum` or `examples` in compact form is read as that value, not compiled as a schema.
- A component's own `inject:` `value:` template reads `.metadata` — the Release's name, version and environment, and the Aggregate's name — instead of halting.
- Template halts name the root read and a missing sensitive field; boundary Features label output with their provider; `gen-catalog` headers permit narrowing.
- Compose output keeps a `null` from a Recipe's `valuesMapping` or source file as `key: null`, so a value-less environment variable comes from the shell.
- The helm and helm-chart providers now write sync-wave annotations on artifact documents as strings (`"3"`), so a cluster accepts them.
- A schema property written as `{ const: value }` alone now loads and admits exactly that value; a property named `const` belongs under `properties:`.
- A SchemaLibrary referenced only by a socket contract is now pinned at pull, and a missing one is reported by name; re-pull existing lockfiles.
- A compose bridge relays only connections within one compose file, and adds no gateway networks or rewrites for unannotated boundaries sharing a file.
- `aggrega inspect <release> --ir-graph` runs label-transform and annotation-transform attachments with their source files, so its graph matches `aggrega build --ir-graph`.
- A render-verdict policy reads a Terraform file a Recipe writes with its declared format, `hcl` for `.tf` and `json` for `.tf.json`.
- Compose output keeps a `null` written for a service label, a healthcheck setting or a deploy setting instead of dropping or emptying it.
- `example/storage-alarm` refuses an `evaluation_periods` below 1 by name instead of silently using 2.

## [0.8.6] - 2026-10-22

### Added

- Catalog: runtimes for agents built on Google's Agent Development Kit and on Mastra.
- Catalog: Langflow, Flowise and n8n declare sockets for the vector stores their flows use, and Flowise and n8n for their tools.
- Catalog: a runtime for agents built on Microsoft Agent Framework.
- Catalog: Dify imports its app definition at deploy, carried by `app_definition` on the server and the `core/dify` module.
- Catalog: Components serving Prometheus metrics publish their address as `metrics_endpoint`, and many take a `metrics_enabled` value that turns those metrics on.
- Catalog: the Envoy AI Gateway operator takes its webhook certificate from a Secret the Release names in `webhook_tls_secret`, without cert-manager.
- The documentation gains an advanced example showing how a Recipe's `omits:` leaves a fetched chart's generated Secret out.
- The `aggrega.ai/kubernetes-version` annotation sets the Kubernetes version helm renders a chart for; unset, the version `helm template` uses.
- Catalog: Envoy Gateway as a Gateway API control plane, with `core/http-route` routing a workload through it.
- Catalog: Tyk on helm and compose, and `core/upstream-key-pinning`, which pins the public key an upstream must present.
- Catalog: RabbitMQ runs through its cluster operator, and controller-driven datastores take `infra.credentials` as `generated` or `supplied`.
- Catalog: gateways carry `dataplane.aggrega.ai/*` labels naming what they can do to traffic, and gateway Features select on them.
- Catalog: an agent's model socket admits a gateway serving the model API that `model.aggrega.ai/api` names on each model and gateway.
- Catalog: `core/prompt-deny-patterns` refuses a model request whose text matches listed expressions.
- Catalog: `core/prompt-injection-shield` builds on the LiteLLM and NGINX gateways, screening model requests through a NeMo Guardrails guard.
- Catalog: `core/data-masking` gains a `refuse` action and `infra` relaxations whose defaults keep the protection.
- Catalog: a Release names APISIX's callers in `infra.callers`, with keys in `caller_keys`, for per-caller budgets and semantic caching.
- Catalog: Envoy Gateway's model route admits the AI gateway Features, building each one it honours and refusing the rest by name.
- Catalog: the Keycloak, Harbor, Thanos, Agenta and Dify modules take their credentials as parameters a Release fulfils.
- Catalog: servers publishing their own address take `infra.external_base_url`, and Keycloak imports the realms in `values.realm_imports`.
- The documentation gains an example of policies halting a build where classified data reaches a model without data masking.
- Helm render facts report the namespace each emitted resource is placed in.
- Catalog: Prometheus scrapes stateful servers, discovers a target's replicas or pods on Kubernetes, and presents a connection's `credential` as a bearer token from a file.
- `aggrega import` reads n8n, MCP client and registry, Compose, Dify, CrewAI, LangGraph, Google ADK and Agent Framework sources through core importers.
- A core importer is fetched from the default registry and verified on the first import needing it; later imports run offline from the cache.
- MCP, Compose, CrewAI, LangGraph, Google ADK and Agent Framework imports also draft a `release.yaml` carrying the operational inputs the source states.
- `aggrega import --sync` updates an earlier import's drafts from a changed source, recorded in `.aggrega-import.json`; `--sync --dry-run` exits 2 on drift.
- `aggrega import --sync` keeps what you add to a drafted Release, such as its environment, and reuses the `--target` the earlier import was given.
- `aggrega import --target <provider>` drafts a `manifest.yaml` mapping each drafted component type to the catalog's one Recipe for that provider.
- The importer setting `empty_credentials` (`none`, `marked`, `all`) writes located credential values empty; by default each is carried and named in a `credential-carried` warning.
- An import leaves a value that is wholly a variable reference in its format's syntax, such as `${TOKEN}`, as written and unwarned.
- An MCP server address's user part, query and fragment are carried in a comment above the server in the drafted Release unless `empty_credentials` removes them.
- The importer setting `attach_egress_allowlist` attaches `core/egress-allowlist` to each platform an import drafts.
- A core `parser/jsonc` reads JSON with comments; a commented `.vscode/mcp.json` or CrewAI `crew.jsonc` imports with no `--format`.
- An import reads its source through the compiler's own parsers where the workspace catalog serves none.
- The editor's import asks for a file or folder, destination, Preview, Sync and target, and the Explorer's Import into Aggrega imports the clicked item.
- The session's `aggrega/import` and the assistant's `import` tool take `target` and `sync`, and the destination is optional.
- A FeatureRecipe declaring `readsTargetRenderFacts` reads the render facts of each component its host is wired to.
- A Workload service of type `Loopback` publishes its ports on the host's 127.0.0.1 under compose; helm refuses it by name.
- The compose provider writes a fragment's top-level `models` and each service's `models` grant into the assembled file, halting on conflicting or undeclared models.
- Catalog: Milvus, OpenSearch and Elasticsearch as vector and search datastores on helm and compose, and Tempo and Loki on compose receiving OTLP.
- Catalog: `core/egress-allowlist` lets a component reach only what it is wired to, its own pods, cluster DNS and ranges the Release lists.
- Catalog: Langflow, Flowise and n8n load the flow their `flow_definition` value carries at deploy, on helm and compose.
- A Recipe's `spec.boundary.artifacts` writes files once beside a boundary's assembled output, for a provider whose contract declares `boundaryArtifacts`, as terraform's does.
- The terraform provider warns (`module-fetched-at-init`) on each module call in a vendored module or authored body that Terraform downloads at initialisation.
- The terraform provider page teaches sourcing a registry-published module from its git repository at its release tag, and what that vendors.
- A sourced CloudFormation template's `Parameters` take values from its Recipe's or FeatureRecipe's `valuesMapping` in every template of the source, literals checked against their constraints.
- Where a CloudFormation `valuesMapping` reads a value declared sensitive, every literal it binds is refused; an intrinsic is written as given, its text included.
- The assembled CloudFormation stack halts, naming each component involved, where other content in it references a parameter a `valuesMapping` filled in a sourced template.
- Provider plugins read `values_mapped` on the render input's node and on each assembly's rendered node: true when those values are a `valuesMapping`'s output.

### Changed

- Catalog: `core/ai-model/gemini` carries the model API value `gemini`, and the Google ADK runtime's model socket takes only such a model, never a gateway.
- Catalog: Compose Prometheus publishes its port on 127.0.0.1 only, unless the Release sets `publish_on_all_interfaces`.
- Catalog: the Google ADK and Mastra runtimes halt a public ingress or HTTP route until the Release sets `routes_authenticated`.
- Catalog: pgvector publishes a password-free connection URI as `endpoint`; Flowise and n8n also receive its host, port, database and user.
- Catalog: Langflow receives such a store as one connection string, its password the connection's credential by reference.
- Catalog: agent invocation halts the build where a credential or client secret would reach an external agent over plain HTTP.
- Helm charts render through the Helm 4 SDK; charts render for Kubernetes 1.37 by default and download through Helm's built-in getters only.
- Catalog: authored data keys are snake_case; `minimum`, `storage_size`, `external_url`, `username` and `verbose` become `min`, `storage.size`, `external_base_url`, `user` and `log_level`.
- The git source kind is `git-repository` rather than `terraform-module-git`, and fetches a chart or a module alike.
- Catalog: routing Features take `host` from the Release's `infra`, and `core/public-ingress` halts until `infra.control_plane_present` affirms an ingress controller.
- Catalog: `core/compose-bridge` takes `portBase` and `providesField` from `infra`, set in the Release.
- Catalog: a volume left without `infra.storage.type` takes the cluster's default storage class; managed databases keep `gp3`.
- Catalog: the Harbor module deploys the one release Harbor's own chart defines, replacing its separate service components.
- Catalog: the LiteLLM gateway and model proxy run LiteLLM v1.103.1 and take their database through a `database_url` credential slot.
- Catalog: `core/fallback-chain` names, per model alias in `chains`, the aliases a failed request falls back to.
- Catalog: `core/audit-logging` records each request's model and tokens, with `require_cost` and `require_verified_caller` defaulting true, and Envoy Gateway realizes it.
- Catalog: `core/concurrency-limit` takes a `scope` of `gateway` or `model`, realized on NGINX, LiteLLM and Envoy Gateway.
- Catalog: under a period budget, Envoy Gateway refuses model requests while their count cannot be checked.
- Catalog: managed model Recipes whose endpoint the Release supplies halt naming `infra.endpoint` when it is unset.
- Catalog: APISIX takes its Admin API key from the `admin_key` credential slot instead of the chart's published default.
- Catalog: Prometheus scrapes the `metrics_endpoint` each wired target publishes and refuses a target publishing none; the per-connection `metrics_path` is removed.
- Catalog: CloudNativePG, Strimzi Kafka, KubeAI, KServe and the operator-driven RabbitMQ, MariaDB and MySQL clusters place their resources in the Release's annotated namespace.
- `aggrega lint` reads the `observability.aggrega.ai/metrics-credential` annotation, skipping a scrape credential's key comparison for `none` or `separate-token`, and warns (`E0346`) where it disagrees with `metrics_credential_required`.
- A resolution halt prints each reason as `refused:` and counts them as `N refusal(s)`, replacing `guard:` and `guard violation(s)`.
- Langflow and Flowise imports draft the platform carrying the flow by default; importer setting `layout: nodes` keeps drafting each node as a Component.
- An import refuses to write over an existing Aggregate unless given `--sync`.
- A source no importer recognises is refused under `E0350` on every surface, and an unknown `--target` under `E0349`.
- A container's Secret-read environment entries are written before its plain variables, and one variable named both ways halts the build (`E0345`).
- The editor's palette command for importing is titled Aggrega: Import into Aggrega.
- The assembled CloudFormation stack carries its components' `Transform`s in CloudFormation's documented order, halting by name on a combination it cannot carry.
- An ArtifactLibrary bundle file name naming no file beneath its key, such as `.`, `x/..` or `a//b`, halts the build, naming the file and key.
- A boundary's output directory is compared once cleaned, and one climbing out of the output root or naming nothing halts (`boundary-directory-not-beneath-output-root`).
- A boundary-level Feature named `.` or `..` halts the build (`boundary-feature-name-not-a-directory`).
- Whole-catalog `aggrega lint` and `aggrega validate` halt (`E0175`), naming every Aggrega document they cannot load, instead of passing over it.

### Removed

- The `messaging` value of `component.aggrega.ai/type` is removed; `messaging` remains a category.
- Catalog: `core/apply-untrusted-label`, `core/tls-pinning` and `core/block-untrusted-payloads`; use `core/upstream-key-pinning` and `core/prompt-deny-patterns`.
- Catalog: `core/semantic-routing`, `core/token-budget` and LiteLLM's `routing_strategy`; use `core/target-selection` and `core/period-budget`.
- Catalog: the OpenAI model's Envoy AI Gateway Recipe; wire the model to Envoy Gateway's `models` socket.
- Catalog: Harbor's token overrides and its `core_secret` and `secret_key` infra values; use the `token_key`, `core_secret` and `secret_key` credential slots.

### Fixed

- Catalog: the agent runtimes halt when two tool connections' aliases would deliver one variable.
- Catalog: Langflow, Flowise and n8n on Compose render a model credential, and Langflow's admin password, that the Release alone attaches.
- Catalog: a Recipe reading a connection variable the Aggregate's inject block leaves out halts, naming the edge and the block to write.
- Catalog: the synthesized and Compose Grafana Recipes listen on the port they publish, not always on 3000.
- Catalog: `core/aws-irsa` and `core/gcp-workload-identity` attach to several components of one boundary, naming each ServiceAccount after its host.
- Catalog: `init_scripts` and `custom_config` take effect on the local PostgreSQL, MySQL and MariaDB Recipes, and TimescaleDB initialises a fresh volume.
- Catalog: the LiteLLM gateway's audit logging, retry, fallback, data masking and prompt deny pattern Features build, their configuration step served from the hub.
- Catalog: gateway Recipes treat a Feature type written with its version as the same type.
- Catalog: Redis pins its official image exactly, overridable through `infra.overrides.image`, and the Envoy AI Gateway operator pins v1.1.0.
- Catalog: chart Recipes publish hosts naming a Service their chart creates, valid for any Component name.
- Two components sourcing different directories of one git repository at one commit each render their own directory.
- A build resolves an unversioned registry reference through the version its pull pinned.
- Catalog: the LiteLLM request guard refuses caller-supplied identity fields its semantic cache reads, and NGINX with nowhere to forward answers 503.
- A policy refusal names the plugin by its reference's last segment without its tag or digest, such as `rego`, rather than by its version.
- The documentation examples that need a hub registry credential say so.
- Catalog: the Azure API Management Recipe configures `azurerm` beside the Terraform root, so it works in a root whose components carry different `syncWave` values.
- An attached Feature's sourced Terraform module receives its FeatureRecipe's `valuesMapping` output as arguments, instead of a bare `module` block.
- Resolving a sourced Recipe's coordinates evaluates only the locals its source fields read, and a failing source template names the field and the cause.
- A key a Recipe's values set to null removes the sourced chart's default, as a Helm install does.
- A registry read started to fill a dependency stops when the request that asked for it is cancelled.
- Several components in other CloudFormation stacks consuming one value build, each importing the producer's single export.
- A file-delivered attachment's Helm secret volume gets a name Kubernetes accepts whatever the slot is called, underscores included.

### Security

- Catalog: Harbor, Langfuse and LibreChat ship no default signing, encryption or service secrets; the build halts until each slot names a Secret you supply.
- Catalog: the Keycloak chart Recipe keeps its management port in-cluster, publishing the console alone on `<name>-external` for a LoadBalancer or NodePort `service_type`.

## [0.8.5] - 2026-10-15

### Added

- `omits` on a Recipe or FeatureRecipe leaves documents out of a sourced Helm chart, Compose file or CloudFormation template, halting on dangling references.
- `aggrega import` reads a whole directory, recognises the format from importer-declared signatures, and reports each mapping decision on every surface.
- Importers score source nodes against the workspace catalog's own Components; Langflow and Flowise ship as separate importer plugins.
- A `plugins.importer` entry attaches the importer its `uri` names with its own settings; `aggrega plugin add importer` moves the entry.
- `aggrega import --output-dir` names where drafts are written, and the assistant `build` tool takes `write` to write `dist/`.
- `aggrega push` warns about each published archive entry Windows cannot use (`windows-unusable-entry`, `E0344`).
- `aggrega pull --verify` reports every recorded signature a plain pull would rewrite (`E0333`).
- The assistant server reports its own version beside the desktop extension package's.
- Catalog: Higress, agentgateway, Traefik Hub, Azure API Management and Kong Gateway Enterprise as gateways in front of wired models.
- Catalog: OpenTelemetry Collector, pgvector, Chroma, remote A2A agents, remote and stdio MCP servers, and a caller allowlist Feature.
- Catalog: runtimes for agents built on the OpenAI Agents SDK and the Claude Agent SDK.
- Catalog: agent invocation reaches an A2A agent requiring OAuth 2.0 client credentials, OpenID Connect or mutual TLS, its secrets and certificate delivered by reference.
- Catalog: MinIO publishes its S3 endpoint, and Langfuse, Agenta and Thanos Store read it and the bucket from the store they are wired to.

### Changed

- Every plugin socket uses a canonical binary encoding instead of JSON; rebuild third-party plugins against the current kit and republish them.
- Maps a socket schema marks order-preserving keep authored order, read through `OrderedMap` in Go and Rust and `Map` in TypeScript.
- `aggrega plugin run-local` converts its JSON payload into the binary encoding the named export reads.
- `aggrega build`, `aggrega import` and assistant command tools write beneath the workspace root wherever they run.
- A command that finds no workspace names the folder it used; a named workspace root that does not exist is refused.
- The documentation bundle is cached once per machine and verified offline against the last fetched trust root.
- A recorded signature made for another artifact is refused (`E0332`), and the next `aggrega pull` repairs damaged or copied signatures.
- The helm provider compares cluster-scoped resource names across namespaces within one output (`E0038`).
- On Windows, a name Windows cannot hold is refused before anything is written (`E0340`).
- Catalog: Fluent Bit, Loki and Thanos Query halt where their wired target does not publish the protocol their client speaks.
- Catalog: a semantic-cache store is admitted by the vector search it publishes rather than by its component type.

### Removed

- `core/log-masking` and its realization; mask LiteLLM gateway logs through `values.log_masking`.
- The OpenAI agent Component and its Recipes, whose hosted target was shut down.

## [0.8.4] - 2026-10-10

### Added

- Under a signing requirement, a build holds every lockfile entry to its recorded signature (`E0331`); a re-pull records a missing one.
- `plugins-trust.yaml` records each verdict plugin's verified signature, and every surface loading that plugin holds it to the record.
- `aggrega push --dry-run` prints the digest a publication would land, as do the daemon and the assistant tool.
- Stripped macOS binaries ship beside the full and light ones.
- Catalog: Envoy Gateway serves wired models through Envoy AI Gateway with a token budget; APISIX, Kong and LiteLLM serve models wired to `models`.

### Changed

- No parameter value reaches a linter plugin on any scope; only parameter declaration keys are passed.
- Helm places namespaced chart resources that name no namespace in the namespace the Release declares.
- Republishing identical content completes an earlier partial publication, and registry writes are retried.
- A version keeps its earliest verifying signature as its date and signer; `aggrega sign` leaves a signed artifact alone.
- Entity archives and content digests carry no file mode, so digests match on every platform.
- `E0144` carries the short name `signature-bundle-digest-mismatch`.
- A signing or deprecation batch whose references need different signing credentials is refused before anything is signed (`E0330`).

### Fixed

- `aggrega pull` verifies the signature of an Aggregate a Release names by identity.
- Feature attachment findings are positioned at, and attributed to, the document that wrote the attachment.
- The daemon's `aggrega/validate` without `content` validates the file on disk.
- An offline `--auto-pull` build fills a missing lockfile from what the machine already holds.
- A schema library reached only through another library resolves from the registry at any depth.

## [0.8.3] - 2026-10-09

### Added

- `aggrega lint --release` advises where a connection's credential and its target's `api_key` name different secrets (`E0329`).
- Linter plugins on Release-scoped rules receive the Release and each instance's Component, with parameter values removed.
- `aggrega/connectionAtPosition` and the `connection-at-position` tool say which connection a document position falls inside.
- The editor attaches a Feature to the connection at the cursor and names each attachment's realizing FeatureRecipe.
- A `connection-credentials` worked example teaches credentials on connections end to end.
- Windows binaries are Authenticode-signed.

### Changed

- A template's `index` of an absent map key halts naming the key (`E0327`); read optional keys with `optional`, `get` or `hasKey`.
- An attachment under an undeclared name whose Feature fits an empty slot halts, naming that slot (`E0328`).
- A configured catalog directory that does not exist is read as empty, and every surface says so.
- Messages name environment variables in words, drop Go package prefixes, and print each catalog notice and lint notice once.
- Catalog: cognee carries a credential per model connection; period budgets and several gateway limits move to `infra`.

## [0.8.2] - 2026-10-08

### Added

- Each attachment in the build graph carries its `realization`, which `aggrega inspect` prints.
- Assertions read each connection's target labels, authored values and attachments.
- Helm names synthesized resources after the release name, which Recipes and attached Features read as the rendered name.
- The editor draws connection attachments on graph edges and attaches Features into a selected connection's slots.

### Changed

- Catalog: a connection to a `managed` target must carry a credential or set `unauthenticated: true`.

### Fixed

- On Windows, configuration and caches live under `APPDATA` and `LOCALAPPDATA`, and `aggrega config init` says where both are.
- Cache, lock and store names use one encoding that Windows, Linux and macOS all accept.
- Progress and colour draw on a Windows console, with plain lines wherever a console cannot draw.

## [0.8.1] - 2026-10-07


## [0.8.0] - 2026-10-07

### Added

- A connection carries attachments filling slots its socket declares under `usesSchema.<socket>.featuresSchema` (`E0315`, `E0316`, `E0317`).
- A connection attachment is named `<alias>.<slot>`, pinned and realized under that name, and read at `.context.uses.<alias>.features.<slot>`.
- Workload `attachments` and `envFrom` entries take `connection: <alias>` to deliver a connection's credential.
- The language server and assistant tools answer which slots and Features a connection accepts, and report the server `version`.
- `workspace.releases` gives each Release its own catalog directories on every surface.
- Checks, hover, completion and navigation can run in the terms of a named Release.
- Provider render facts reach FeatureRecipes, and `.context.used_by` lists a component's consumers.
- Releases ship Windows and macOS binaries and an `aggrega-<version>.mcpb` desktop extension package.
- The editor publishes to the VS Code Marketplace and Open VSX.
- Credential Features take `origin` from `values` or `infra`, exactly one; vLLM, TEI and TGI gain an `api_key` slot.
- Catalog: guardrail services, Cloudflare AI Gateway, and LiteLLM semantic caching, period budgets, log masking and a request guard.
- The daemon's `aggrega/getSchema` says, for each entity kind, whether single-entity validation, publication and inspection by name accept a document of it.
- Dependency resolution reports on the daemon and assistant tools give each entry's `source`, as `aggrega pull --detail` prints it.
- Linux releases attach `aggrega-stripped-<version>-linux-amd64`, the binary without debug information.

### Changed

- A catalog Component's credential for a connected service is set as `credential` on the connection; the earlier field is refused.
- LiteLLM's caller key slot is `api_key`, and `core/ai-model/openai-compatible` declares no slot.
- Assertions read a connection as `.uses.<socket>.<alias>.target`; the earlier path is no longer read.
- Policies, `--ir-graph` and `aggrega/irGraph` withhold declared-sensitive values; `--unredacted` reveals them where offered.
- Two Features delivering one key halt (`E0318`, `E0322`), as do two connections of one component under one alias (`E0319`).
- Every lockfile drift names one remedy, `aggrega pull --release <release>`.
- Offline registration of a registry other than the hub is refused unless its trust is pinned by hand.
- Reads and writes through a link leading out of the workspace are refused on every surface.
- `--config` and `--offline` reach the assistant server and the editor daemon.

### Fixed

- A realization the Manifest binds by instance name is pinned and verified like one bound by type.
- The trace's workload re-render withholds a component's own sensitive attachments and `provides`.
- Large plugin guests start on arm64.
- A plugin's call ceiling counts the guest's processor time on Linux, so a busy machine no longer cancels it.

## [0.7.4] - 2026-09-28

### Added

- `aggrega pin` records an entity's derived pins in an `entity.lock` beside it; `--pin-sources` runs it during pull or push.
- `.aggregaignore`, with each provider's ecosystem ignore rules, decides which files under a source directory ship.
- `aggrega inspect oci://…` reads a published entity straight from a registry.
- Features listing each other in `conflictsWith` halt when both reach one component.
- The registry index records each version's publication date, manifest digest and verified signer.
- The default registry may carry a repository path or name a `registries` entry.
- `aggrega mcp --transport` (or `mcp.transport`) chooses stdio or HTTPS.
- Catalog: documented health checks everywhere, generated-password credentials, HTTPS ingress, existing-Secret references, and chart realizations beside authored ones.
- Catalog: AI gateway data masking with Presidio, request mirroring and verified caller identity.

### Changed

- `aggrega pull` no longer writes pins into Recipes or FeatureRecipes; a document still carrying a pin block is refused.
- Pinnable and publishable entities need a directory of their own, and their content may not climb out of it.
- A publication carries the files its entity declares; republishing a reshaped entity at an existing version is refused.
- The lockfile records the Release by its content and each entity by its publication digest.
- Pull and build resolve through one catalog view and one version rule; `aggrega pull --verify` honours recorded pins.
- The artifacts library kind is spelled `ArtifactLibrary`, with lockfile section `artifact_libraries`.
- Workload quantities are checked against one grammar; selectors refuse empty requirements and malformed keys.

### Removed

- The provider contract's unused source-file filter is removed.

### Fixed

- A CLI plugin with an undeclarable flag is skipped with a warning instead of breaking every command.
- `aggrega registry update` stops on a registry whose listing repeats, keeping the previous index.

## [0.7.3] - 2026-09-22

## [0.7.2] - 2026-09-22

## [0.7.1] - 2026-09-22

## [0.7.0] - 2026-09-22

### Added

- `governance/rego` and `governance/cel` ship in the binary, serving all four governance roles with a configurable result `translation`.
- `plugins-trust.yaml` records each verdict plugin's resolved identity; changing it requires `aggrega pull --upgrade`.
- `sensitive: true` on schema leaves and slots; trace, inspect and refusals withhold such values, with `--trace-unredacted` to reveal.
- Every halting diagnostic leads with a stable short code, explained by `aggrega explain`; retired codes are never reused.
- `aggrega mcp` also serves over HTTPS with OAuth bearer tokens, scopes and one fixed workspace.
- `aggrega scaffold`, `aggrega edit-document`, `aggrega build --dry-run`, and Release `extends`.
- `WorkloadLibrary`, an artifacts library and `BaseWorkloads`, importable at every templated site, locally or from a registry.
- Workloads gain port `protocol`, `hostNetwork`, extended resources and `runtimeClassName`.
- Boundary annotations `aggrega.ai/image-registry`, `aggrega.ai/image-pull-secret` and `aggrega.ai/service-account`.
- Compose realizes `initContainers`, `configmap` volumes and `syncWave`; CloudFormation orders stacks by `syncWave`.
- Advisories for unread values and parameters, undeclared Recipe `provides`, near-miss socket names, and unhonoured annotations.
- The VS Code extension gains an activity bar, topology graph, catalog, registry and plugin panels, a welcome page and attach-from-graph.
- Catalog: an AI layer of managed and self-hosted models, agents, tools, pipelines and gateways, with GPU sizing and controller realizations.
- Catalog: scale-to-zero substrates, compute primitives, a realtime voice module, telecom operators, vector and time-series datastores, and database controllers.

### Changed

- `mutator` and `policy` unify into one `governance` socket whose required `role` is `ir-verdict`, `render-verdict`, `label-transform` or `annotation-transform`.
- Verdict roles call `verdict_ir` and `verdict_render`; plugin diagnostic severities are `halt`, `warn` and `info`.
- Credential slots become attachment slots declared under `featuresSchema`.
- Structured output uses `-o`/`--output`, the directory flag is `--output-dir`, and diagnostic class codes are kebab-case.
- Diagnostics name compilation stages instead of step numbers; `--render-diagnostics` becomes `--build-on-inspect`.
- Derived Helm, Compose and CloudFormation names are encoded injectively; renamed resources redeploy rather than update.
- An attached Feature's output sits beside its host and is declared to the target, so its charts and modules load.
- On Terraform, a sourced component's values become its `module` block arguments in the boundary root.
- Hosting is the `component.aggrega.ai/hosting` label: `self-hosted`, `managed`, `external` or `mixed`.
- An extending Component inherits its base's schema pillars verbatim and authors none of its own.
- `aggrega lint` runs every configured linter, and `aggrega validate --release` enforces the plugin trust record.

### Removed

- The monotonic-narrowing lint rule, with its configuration keys and flags, is removed.
- The author-time `deprecated` metadata marker is removed; use `aggrega deprecate`.

### Fixed

- Schema contracts imported from a file are read and enforced.
- Two contributors writing one output path or one derived name halt instead of overwriting each other.
- Attachment and per-edge `infra` defaults reach rendered output.
- A FeatureRecipe whose only effect is its `pipeline:` runs it.
- `locals:` evaluate in author order, and `provides` reads resolved locals rather than raw template text.
- Failing assertions show their name and `fail` message; `aggrega pull --upgrade` keeps the old lockfile when it fails.

## [0.6.1] - 2026-07-22

### Added

- An `importer` plugin type and `aggrega import <file>`, drafting Aggregate and Component files from a foreign agent definition.
- `aggrega plugin run-local` invokes a guest export without a build.
- The MCP `validate` tool checks single entities, Plugin `.wasm` files and whole catalogs.

### Changed

- `aggrega validate --release`, the MCP tool and the editor share one full Release validation, with `--response-format detailed|concise`.
- A plugin's version comes from the guest's self-report, and publishing refuses an entity whose version disagrees.
- `aggrega lint --pre-publish` classifies changes to a plugin's CLI command surface.

### Removed

- The `plugin_sidecar_dir` setting is removed.

### Fixed

- A linter attachment's `function:` selects its export.
- Binding a cross-cutting socket by `capability:` halts instead of resolving to nothing.
- `aggrega push --artifact` refuses to overwrite a published version with different content.
- `aggrega inspect <name>` resolves any entity kind by bare name.
- A purely pulled linter resolves at `aggrega lint`, and `aggrega cache prune` keeps in-use plugin describe entries.

## [0.6.0] - 2026-07-20

### Added

- `aggrega config init`, `aggrega config show` (with `--origin`) and a global `--config` flag.
- A `parser` plugin type with embedded HCL, TOML, YAML and JSON parsers; render-stage policies read native Terraform HCL.
- Pipeline steps declare typed inputs and outputs, pass outputs as `.steps.<id>.outputs.<key>`, and skip on a falsy `when`.
- CLI plugins declare per-command flags shown by `aggrega help`, and plugin kits build `describe()` with a typed builder.
- Modules re-export nested exported components through multi-hop chains.
- Core provider entities ship in the binary, so a build without a catalog directory renders offline.
- `aggrega pull --upgrade <target>` re-resolves one dependency.
- Mutator attachments take `with.settings` and `with.sources`, and label-injector and selector-tagger mutators ship.

### Changed

- `--regenerate` is renamed `--upgrade` on `aggrega pull` and `aggrega build`; `build --upgrade` implies `--auto-pull`.
- The lockfile `providers` key `target` becomes `capability`.
- The lockfile pins output-shaping plugins and `aggrega serve` renders from it; gate plugins are cached, not pinned.
- `aggrega pull` verifies every socket plugin's signature and host functions.
- The `loaded config from:` line is gone; `aggrega config show` reports the cascade.

### Fixed

- `aggrega plugin add cli` upgrades an existing plugin in place.
- `aggrega plugin gen-catalog` records host functions and commands, so generated entities publish directly.
- A plugin whose type does not match its socket halts with one diagnostic, the linter socket included.
- A keyless signature verification failure names the certificate issuer.

## [0.5.0] - 2026-07-16

### Added

- `aggrega mcp` runs a Model Context Protocol server over stdio, and `aggrega get-schema <kind>` prints any embedded schema.
- `aggrega validate` checks entities, Plugin files and catalogs, leaving `aggrega lint` to publishing conventions.
- `cli` plugins contribute sandboxed top-level commands, installed after a capability prompt.
- `aggrega search` queries a local registry index built by `aggrega registry update`; `registry list`, `remove` and `--name` aliases join it.
- `aggrega plugin add` and `aggrega plugin remove` attach and detach plugins in configuration.
- A global configuration layer, `aggrega config capture`, and a CI-owned `.aggrega-config-append-ci.yaml` overlay.
- A `signing` block can require signed artifacts organization-wide.
- Policy plugins return `deny`, `warn` or `audit` and may declare a settings schema; Rego, Kubewarden and OPA reference guests ship.
- `aggrega plugin gen-catalog`, `aggrega plugin sync`, `aggrega push-artifact` and `aggrega cache evict`.
- Workload mounts take `subPath`, init containers a `name`, and one secret slot may feed several keys.
- Components and modules whose presence toggle resolves false are pruned.
- A versioned documentation site with a generated schema reference, an authoring guide and the `aggrega-author` skill.
- The Rust and TypeScript plugin kits reach parity with Go.
- Catalog: synthesized datastore, broker, gateway, observability, storage and security families, plus FerretDB, Kong, Keycloak, Thanos and Harbor modules.

### Changed

- `aggrega build` is read-only by default; `--auto-pull` or `auto_pull: true` fills the lockfile.
- Plugins carry `metadata.pluginType` and a bare `metadata.name`, are addressed as `plugin/<type>/<name>`, and publish as one signed manifest.
- A Recipe selects a `capability:`; provider bindings live in `plugins.providers`, and provider behaviour in `spec.provider.contract`.
- `aggrega plugin install` folds into `aggrega plugin add`, and CLI plugins are pinned by `{uri, digest}` in configuration.
- `aggrega registry add` writes to the global configuration by default.
- Sourced Helm charts render one file per resource, including documents without a name.
- Catalog Recipes render products from their official images.

### Removed

- `aggrega catalog search` and `aggrega plugin search` are removed; use `aggrega search`.

### Fixed

- Repository-root Helm sources, signed OCI charts, comment-glued separators and chart `crds/` now render.
- Compose maps a container's `command` to `entrypoint` and `args` to `command`.

## [0.4.0] - 2026-07-10

### Added

- A Terraform provider renders HCL or JSON from artifacts or sourced modules, offline and without running Terraform.
- A CloudFormation provider assembles one JSON stack per boundary, placed with `aggrega.ai/aws-account`, `aggrega.ai/aws-region` and `aggrega.ai/aws-assume-role-arn`.
- Providers can wire `uses` edges crossing boundaries through paired link exports.
- Workloads choose a controller shape with `form:` (`stateless`, `stateful`, `daemon`, `scheduled`, `task`) and `schedule:`.
- `aggrega pull` records source and import digests for Recipes, and the build verifies them.
- `aggrega cache status|prune|clear`, `aggrega plugin list`, and a commit timestamp in `aggrega version`.
- `aggrega lint` works without configuration through an embedded publishing linter.
- `aggrega build --verify-determinism` renders each node twice and halts on any difference.
- A `helm-chart` Recipe may place standalone `artifacts:` files at the chart root.
- Shell completion is documented for bash, zsh, fish and PowerShell.

### Changed

- Providers are bound in the workspace and pinned in the lockfile, and plugins are identified without digests.
- Providers render from their guest's self-description.

### Fixed

- Multi-line YAML values patch without corrupting neighbouring content.
- Concurrent compilation of one plugin guest no longer crashes the runtime.
- MariaDB Recipes read the port from `infra`.

## [0.3.0] - 2026-07-06

### Added

- `aggrega deprecate` attaches signed deprecation markers to published artifacts, honoured by pull and build.
- `aggrega registry add` fetches and pins a registry's advertised trust policy on first use.
- `aggrega build` shows phase progress, with `--quiet`, `--debug`, `--no-color`, `--plain` and `NO_COLOR` support.
- Mutator and policy-validator plugins run during builds.
- Plugins fetch remote content through a pinned, content-addressed channel.

### Changed

- Helm, Compose and helm-chart render through embedded WebAssembly provider plugins, pinned in the lockfile.
- A `Plugin` declares exactly one `pluginType`.
- Cached content is verified against manifest digests, and catalog entity signatures are verified at pull.

### Removed

- The in-tree Go rendering engines, the `Provider` entity kind and provider `baseSchema` are removed.

### Fixed

- `aggrega serve` answers `initialize` promptly in large workspaces.
- External-input pins carry across a re-pull.

## [0.2.0] - 2026-07-01

### Added

- `aggrega sign` signs artifacts keyless or with `--key`; `aggrega pull` verifies them against per-registry trust policies.
- `aggrega login`, `aggrega logout` and `aggrega auth token`.
- The `Plugin` entity: a Wasm plugin published, signed, pinned in the lockfile and capability-checked at load.
- Recipe and FeatureRecipe `pipeline:` steps run Wasm plugins that patch or emit rendered documents.
- `aggrega inspect` covers every kind, with `--list`, `--detail`, `--color` and `--output`.
- `aggrega lint --release` checks a Release's dependency closure without rendering.
- Repeatable `--catalog` and `catalog_dirs` layer several catalog directories.
- An Aggregate and a Release may list `components` directly, without subsystems or boundaries.
- Feature `appliesTo` and `appliesToAnyOf` take label-selector strings.
- Credential Features `core/secret-value`, `core/referenced-secret` and `core/secret-provided`, read through `.secrets.<slot>.provides`.
- `core/k8s-dns-resolver` qualifies service hosts across namespaces.
- A three-axis component label scheme: type, category and capability.
- `aggrega serve` validates `.aggrega-config.yaml` and the active lockfile live.
- `AGGREGA_` environment variables override configuration.

### Changed

- `aggrega pull` honours the lockfile by default; `--regenerate` floats versions.
- Manifest mappings are the only way a Recipe is resolved.
- Entity documents are validated against their schema at load, rejecting unknown keys.
- Version selection follows strict SemVer everywhere.
- Compose environment variables render in author order.
- `core/datastore/postgres` renders as a StatefulSet and gains configurable values, infra and a credential slot.
- The credential Feature field `source` is renamed `origin`.

### Removed

- The `ai-*` type values, `appliesToExpressions` and the nested selector shapes are removed.

### Fixed

- Registry-backed builds resolve every catalog kind.
- `requiredFeatures` is enforced on sub-aggregate components.
- Registry credentials in source URLs no longer reach cache paths.

### Security

- Interpolation-time parameter maps are cleared once resolution completes.

## [0.1.0] - 2026-06-11

### Added

- First release of the `aggrega` CLI, compiling Aggregates and Releases through Manifest-mapped Recipes to Helm, helm-chart and Compose output.
- `aggrega pull` and `aggrega push`, a per-Release lockfile and a content-addressed cache.
- Aggregates are publishable and resolve by identity from a registry.
- `aggrega serve`, a JSON-RPC daemon for editors, and a VS Code extension driving it.
- `aggrega lint` with the publishing-discipline linter plugin and `--pre-publish` version-bump checks.
- A sandboxed Wasm plugin host and Go, Rust and TypeScript plugin kits.
- `.aggrega-config.yaml` workspace configuration, and `aggrega version` with `--json`.
- Recipe inputs `workload:`, `source:` with `valuesMapping:`, and `artifacts:`, also available to FeatureRecipes.
- Remote `source:` charts, Compose fragments and Terraform modules are fetched and pinned.
- Annotation-driven Feature attachment through Manifest `mappings.features.annotations`, with cascades and provenance in `aggrega inspect`.
- `uses` edges carry per-edge `values` and `infra`, and a Release may wire edges within declared sockets.
- Custom inject mechanisms beyond `env`, `file` and `arg`.
- The `optional` template helper, typed parameter contracts with defaults, and Component schema defaults.
- `core/compose-bridge` routes across Compose networks; a boundary `syncWave` emits Argo CD sync-wave annotations.
- `aggrega build --trace` writes per-node intermediate state to `.trace/`.
- Errors carry a category: `[DEVELOPER_ERROR]`, `[OPS_ERROR]`, `[CATALOG_ERROR]`, `[TRANSLATION_ERROR]` or `FETCH_ERROR`.

### Changed

- Catalog schemas are written in JSON Schema 2020-12 compact form; CUE is not used.
- Every engine stamps one `aggrega.ai/*` provenance label set.

### Fixed

- A `uses` edge whose target does not exist halts the build.
- Sub-aggregate components take part in topology, parameters and checks like top-level ones.
- Inconsistent Manifest mappings, multi-document Compose sources and multi-document `Chart.yaml` files halt.
