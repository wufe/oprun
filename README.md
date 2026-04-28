# oprun

A small, configurable task runner for interactive developer workflows.

Flows are declared in YAML. Each step is a node in a tree — exec commands, ask
yes/no, pick from a menu, collect free-text input, loop over a selection, or
jump to another node by id. Prompts use
[charmbracelet/huh](https://github.com/charmbracelet/huh), so the UX feels like
`gum` without shelling out to `gum`. Inputs and selections are persisted between
runs so the next invocation defaults to the last answers you gave.

oprun is meant for the kind of recipe you used to keep in a scratch file and
run by copy-pasting: build an image, optionally push it, optionally redeploy,
pick one or more test files, then run them. Turning that into a YAML flow gives
you a repeatable, documented, reviewable routine without having to write a
real CLI tool.

---

## Install

### `go install`

```bash
go install github.com/wufe/oprun@latest
```

Make sure `$(go env GOBIN)` (or `$(go env GOPATH)/bin`, typically `~/go/bin`)
is on your `$PATH`.

### From source

```bash
git clone https://github.com/wufe/oprun.git
cd oprun
go build ./...
# the binary is ./oprun
```

---

## Quick start

Put a flow YAML in one of the search paths below, then:

```bash
oprun list                  # see what's available
oprun <flow-name>           # run it
oprun run <flow-name>       # same thing, explicit
oprun run ./my-flow.yaml    # run a yaml file directly by path
```

### Flow search order

First match wins. Local directories shadow `$HOME`, so you can keep
project-specific flows next to the code they automate.

1. `./.oprun/flows/<name>.yaml`
2. `./.flows/<name>.yaml`
3. `./flows/<name>.yaml`
4. `~/.oprun/flows/<name>.yaml`

Both `.yaml` and `.yml` extensions are accepted.

### Saved answers

After each run — including runs that fail — oprun writes the inputs you
submitted to `~/.local/state/oprun/<flow-name>.json` (respecting
`$XDG_STATE_HOME` if set). On the next run those values are pre-filled:

- **declared `vars:`** and `input` nodes are re-asked with the last value as the editable default
- **`choose`** pre-selects what you picked last time (filtered against the currently available options)
- **`confirm`** pre-highlights your last Yes/No (keyed by node `id` — no id, no persistence)
- **`{foo}` lazy references** skip the prompt entirely if a value was saved

---

## Writing flows

> **Full step-by-step guide**: [`FLOWS.md`](./FLOWS.md). It covers every node type
> in detail, variable lifecycle, persistence rules, conditional execution with
> `when:`, a cookbook of common patterns, and a troubleshooting table. The
> section below is a tour; reach for `FLOWS.md` when you're actually authoring.

A flow has a name, an optional set of variables prompted up-front, and an
ordered list of nodes. The example below tags a release, optionally runs
tests, builds for one or more targets, and uploads the resulting artifacts —
it exercises every node type (`confirm`, `choose`, `foreach`, `exec`) and both
flavours of `choose` (static options and dynamic `options_cmd`).

```yaml
name: release
description: Tag a release and publish build artifacts

vars:
  - name: version
    prompt: Version tag (e.g. v1.2.3)

nodes:
  - id: prechecks
    type: confirm
    prompt: Run the test suite first?
    on_yes:
      - run: make test

  - type: choose
    prompt: Build target?
    options:
      - label: linux/amd64
        do:
          - run: GOOS=linux GOARCH=amd64 go build -o dist/app-linux-amd64
      - label: darwin/arm64
        do:
          - run: GOOS=darwin GOARCH=arm64 go build -o dist/app-darwin-arm64
      - label: both
        do:
          - run: GOOS=linux GOARCH=amd64 go build -o dist/app-linux-amd64
          - run: GOOS=darwin GOARCH=arm64 go build -o dist/app-darwin-arm64

  - type: confirm
    prompt: Create tag {version} and push it?
    on_yes:
      - run: git tag {version}
      - run: git push origin {version}

  - type: choose
    prompt: Which artifacts to attach to the release?
    multi: true
    options_cmd: ls dist/ | awk '{print $0 "\t" "dist/" $0}'
    store: artifact

  - type: foreach
    var: artifact
    do:
      - run: gh release upload {version} {artifact}
```

Reading top to bottom: ask for a version, optionally run tests, pick one or
more build targets (each option has its own subtree), confirm tagging, then
multi-select artifacts from `dist/` (displayed as filenames, stored as paths
via the tab-separator) and upload each one.

### Control flow

- Top-level nodes run in declaration order.
- Branch subtrees (`on_yes`, `on_no`, `options[].do`, `foreach.do`) are nested node lists — completing one falls through to the parent's next sibling.
- `type: goto` jumps anywhere by `id`; execution resumes linearly from there.
- Omitting `on_yes`/`on_no` on a confirm is equivalent to "do nothing on that answer, fall through" — a common pattern.
- `when:` on any node gates whether it runs. The string is run through `{var}` substitution and evaluated as truthy: empty / `no` / `false` / `0` / `off` (case-insensitive) skip the node; anything else runs it. Useful for gating on a flag captured by an earlier `exec` — e.g. `when: "{rebuilt}"` after capturing `rebuilt: yes|no` in a `confirm`'s `on_yes`/`on_no` branches.

### Node types

| type      | required fields                             | notes                                                                         |
|-----------|---------------------------------------------|-------------------------------------------------------------------------------|
| `exec`    | `run`                                       | Default when `type` is omitted. Runs via `bash -c`. Optional `dir`, `capture`.|
| `confirm` | `prompt`                                    | Optional `on_yes`, `on_no`. Answer persisted if node has `id`.                |
| `choose`  | `prompt` + one of `options` or `options_cmd`| `multi: true` for multi-select. Store selection with `store:`.                |
| `input`   | `store`                                     | Prompts for a string; stored in the named variable.                           |
| `goto`    | `goto`                                      | Target `id` must exist at the top level.                                      |
| `foreach` | `var`, `do`                                 | Iterates a list variable (typically from a multi-select `choose`).            |

### Variables and substitution

- Declare upfront in `vars:` — each one is prompted at flow start.
- Set inline with `input`, `capture:` on an `exec` node, or `store:` on a `choose` node.
- Reference as `{name}` in `run`, `dir`, `prompt`, or `options_cmd`.
- An unset `{name}` is prompted lazily the first time it's referenced.

### Dynamic options (`options_cmd`)

Each line of the command's stdout becomes an option. A line containing a tab
splits into `<display label>\t<stored value>`; a line with no tab is used for
both. This lets you show short labels but store richer values (full paths,
JSON, etc.):

```yaml
options_cmd: ls /tests/*.yaml | awk -F/ '{print $NF "\t" $0}'
#            ^ stdout lines like:  test-a.yaml  <TAB>  /tests/test-a.yaml
```

### Schema / editor support

A JSON Schema lives at [`flow.schema.json`](./flow.schema.json). Point the
[Red Hat YAML extension](https://marketplace.visualstudio.com/items?itemName=redhat.vscode-yaml)
at it either per-file:

```yaml
# yaml-language-server: $schema=../flow.schema.json
name: my-flow
...
```

...or project-wide in `.vscode/settings.json`:

```json
{
  "yaml.schemas": {
    "./flow.schema.json": "flows/*.yaml"
  }
}
```

You get field completion, hover docs, and early errors on typos and missing
required fields.

---

## Contributing

Pull requests and issues welcome.

### Local setup

```bash
git clone https://github.com/wufe/oprun.git
cd oprun
go build ./...
./oprun list
```

Standard Go tooling applies:

```bash
go test ./...
go vet ./...
gofmt -w .
```

### Project layout

```
.
├── main.go           # CLI entry; flow search and dispatch
├── flow.go           # YAML types + loader + type defaulting
├── runner.go         # execution engine (sequencing, branches, goto, foreach)
├── prompt.go         # huh-based Confirm/Choose/Input wrappers
├── state.go          # per-flow JSON state (~/.local/state/oprun/<flow>.json)
└── flow.schema.json  # JSON schema for editor tooling
```

### Adding a node type

1. Add the field(s) to `Node` in `flow.go`.
2. Add a `case` in `Runner.runNode` in `runner.go`.
3. Extend `flow.schema.json` with an `if/then` branch describing the new type.
4. Document it in the README table and, if needed, add a short example.

### Reporting issues

When filing a bug, a minimal reproducing flow YAML and the state JSON (if any)
from `~/.local/state/oprun/<flow>.json` make debugging much easier.

---

## License

[MIT](./LICENSE).
