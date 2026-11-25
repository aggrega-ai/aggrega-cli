# Test data for the arm importer's end-to-end cases

Every template here is written for these tests.

| Path | What it holds |
|---|---|
| `build-back/` | A template and its parameters file: a storage account and an Event Hubs namespace the catalog maps, a virtual network the operator owns, and a static web app no row maps, whose properties carry a pointer and a string written with an escaped bracket. The case imports it, builds the drafts and reads the template the build writes. |
| `topology/` | A PostgreSQL flexible server, a virtual network and a container app, with a parameters file naming the server. The case imports it for two targets. |
| `roots/two/` | Two templates, `a.json` and `b.json`, neither linking the other. |
| `roots/shared/` | Two templates, `a/main.json` and `b/main.json`, each linking `shared/net.json` by the relative path `../shared/net.json`. |
| `wrapped-module/` | An Azure Data Explorer cluster deployed through the Azure Verified Modules module `avm/res/kusto/cluster` 0.11.1, written `{"$avm": "kusto/cluster/0.11.1"}` for the compiled template of the module that the case writes in its place from `plugins/internal/armimport/testdata/avm`, and a static web app no row maps reading the cluster's `resourceId` output. |
