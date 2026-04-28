# Authoring oprun flows

Step-by-step reference for writing flow YAML files. Read this end-to-end the
first time, then keep it open as a lookup. The guide is the source of truth for
flow semantics — when `flow.go`, `runner.go`, or `flow.schema.json` change, this
document changes too.

> **Quick links**: [field reference](#field-reference) · [cookbook](#cookbook) · [troubleshooting](#troubleshooting)

---

## 1. Where flows live

oprun resolves flow names by searching, in order (first match wins):

1. `./.oprun/flows/<name>.yaml`
2. `./.flows/<name>.yaml`
3. `./flows/<name>.yaml`
4. `~/.oprun/flows/<name>.yaml`

Both `.yaml` and `.yml` are accepted. A name containing `/` or ending in
`.yaml`/`.yml` is treated as a literal path and bypasses the search — useful
while iterating: `oprun run ./draft.yaml`.

**Recommendation**: project-specific recipes go in `./.oprun/flows/`, personal
recipes go in `~/.oprun/flows/`.

---

## 2. Anatomy of a flow

Every flow has the same top-level shape:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/wufe/oprun/main/flow.schema.json
name: my-flow                  # required; also the state filename
description: One-line summary  # optional; shown in `oprun list`

vars:                          # optional; prompted at the very start
  - name: env
    prompt: Which environment?
    default: staging

nodes:                         # required; runs top-to-bottom
  - run: echo hello {env}      # `type: exec` is the default
```

Three things to internalize:

- The first line is a `yaml-language-server` directive that gives editors
  schema-driven completion and validation. Always include it.
- `vars:` are asked **before** the first node runs. They are good for inputs
  the whole flow depends on (target environment, username, version tag).
- `nodes:` is an ordered list. Execution flows top-to-bottom; nested branch
  subtrees fall through to the parent's next sibling when they finish.

---

## 3. Variables

### 3.1 Three ways to set a variable

| Mechanism                     | When it fires            | Example                                              |
|-------------------------------|--------------------------|------------------------------------------------------|
| Top-level `vars:`             | Once, at flow start      | `vars: [{name: env, prompt: Env?}]`                  |
| `input` node                  | When the node runs       | `- {type: input, store: tag, prompt: Tag?}`          |
| `exec` with `capture:`        | After the command exits  | `- {run: git rev-parse HEAD, capture: sha}`          |
| `choose` with `store:`        | After selection          | `- {type: choose, ..., store: target}`               |
| Lazy `{name}` reference       | First read of an unset var | `run: deploy {env}`  (prompts if `env` is unset)   |

### 3.2 Referencing variables

Use `{name}` in any of these fields:

- `run` (exec command)
- `dir` (exec working directory)
- `prompt` (confirm/choose/input title)
- `options_cmd` (choose dynamic options)
- `when` (conditional gate — see section 5)

The substitution is plain text replacement. **There is no escaping**: if you
need a literal `{` followed by a name-shaped token, restructure or pre-escape
in your shell.

### 3.3 Lazy prompting (and how to avoid it)

If `{name}` is referenced but not yet set, oprun **prompts the user for it on
the spot** and stores the answer. This is a feature, not a bug — it lets you
write `deploy {extra_args}` without declaring `extra_args` upfront. But it
means a typo in a variable name surfaces as an unexpected prompt, not an
error. Two ways to avoid surprises:

- Declare every variable you mean to use in `vars:` (with `default: ""` if you
  want it optional).
- Run the flow once and check `~/.local/state/oprun/<name>.json` to see what
  variables actually got set.

### 3.4 Storage shape (string vs list)

`r.vars` stores values by runtime type. A `choose` with `multi: true` writes a
**list**; a single-select writes a **string**. Reusing the same name with both
shapes will silently clobber the persisted entry on save. **Rule of thumb**:
do not reuse a variable name across single-select and multi-select nodes —
or across any pair of "string-ish" and "list-ish" producers.

---

## 4. Nodes — step by step

Every node accepts these top-level fields:

- `id` — optional; required only if a `goto` targets it, **or** if you want a
  `confirm` answer / static `choose` selection persisted across runs (state is
  keyed by id).
- `type` — one of `exec`, `confirm`, `choose`, `input`, `goto`, `foreach`.
  Defaults to `exec` when omitted.
- `description` — free-form note for humans; ignored by the runner.
- `when` — conditional gate (see [section 5](#5-conditional-execution-with-when)).

### 4.1 `exec` — run a shell command

```yaml
- run: make build
  dir: /path/to/project           # optional cwd
  capture: build_output           # optional; trimmed stdout → variable
```

- Runs via `bash -c` (falls back to `sh` if bash is missing).
- Stdin/stderr are inherited; stdout is also inherited unless `capture:` is
  set, in which case stdout is teed to the terminal **and** captured.
- Captured value is `strings.TrimSpace`d.
- A non-zero exit code aborts the flow. Use shell-level `|| true` if you want
  to ignore failures: `run: rm -f /tmp/x || true`.
- `type: exec` may be omitted — bare `- run: ...` works.

### 4.2 `confirm` — yes/no question

```yaml
- id: rebuild              # id needed for cross-run persistence
  type: confirm
  prompt: Rebuild image?
  on_yes:
    - run: make build
  on_no:
    - run: echo skipped
```

- Both `on_yes` and `on_no` are optional. Omit either to "do nothing, fall
  through to the next sibling on that answer".
- Answer is persisted **only if the node has an `id`**. Without one, the next
  run starts unbiased.
- A `confirm` itself does **not** write a variable. If you need to act on the
  answer downstream, capture a sentinel in the branches:

  ```yaml
  - id: rebuild
    type: confirm
    prompt: Rebuild?
    on_yes:
      - run: make build
      - {run: echo yes, capture: rebuilt}
    on_no:
      - {run: echo no, capture: rebuilt}
  - run: deploy
    when: "{rebuilt}"        # gates a later step on the answer
  ```

### 4.3 `choose` — pick from a menu (static)

```yaml
- id: target                 # id needed for cross-run persistence
  type: choose
  prompt: Build target?
  options:
    - label: linux/amd64
      do:
        - run: GOOS=linux GOARCH=amd64 go build
    - label: darwin/arm64
      do:
        - run: GOOS=darwin GOARCH=arm64 go build
    - label: skip
      goto: deploy           # jump to top-level node id "deploy"
```

- Each option may have a `do:` subtree, a `goto:` (jumps to a top-level id),
  or neither (just records the selection and falls through).
- With `multi: true`, all selected options' `do:` subtrees run in selection
  order.
- The selection is persisted under `state.Choices[<node id>]` — **id is
  required** for that to work.
- An optional `store:` on a static choose writes the selection to a variable
  too (string for single-select, list for multi-select).

### 4.4 `choose` — dynamic options (`options_cmd`)

```yaml
- type: choose
  prompt: Which test files?
  multi: true
  options_cmd: ls tests/*.yaml | awk -F/ '{print $NF "\t" $0}'
  store: test_file
```

- The shell command's stdout is split by `\n`; each non-empty line becomes an
  option.
- A line containing a tab splits into `<display label>\t<stored value>`.
  Without a tab, the whole line is used for both.
- Dynamic choose has **no per-option `do:` subtrees** — selections are written
  to `store` and that's it. Iterate over them with `foreach`.
- Defaults are restored from the persisted variable, then **filtered against
  the current option list** so stale entries don't poison the prompt.

### 4.5 `input` — free-text string

```yaml
- type: input
  store: ticket
  prompt: Ticket id (e.g. ENG-123)
```

- `store` is required; `prompt` defaults to the store name.
- Last value is offered as the editable default on the next run.

### 4.6 `foreach` — loop over a list

```yaml
- type: foreach
  var: test_file              # name of a list variable (usually a multi-choose store)
  as: f                       # optional; defaults to `var`
  do:
    - run: go test {f}
```

- The loop variable shadows any same-named binding for the duration of the
  loop and is restored after.
- A string variable is treated as a single-element list; an empty string is
  treated as zero iterations.

### 4.7 `goto` — jump

```yaml
- type: goto
  goto: cleanup
```

- Targets a **top-level** `id` only. Jumping to a node that doesn't exist at
  the top level is a runtime error.
- Implemented as a sentinel that bubbles up through any nested subtrees, so a
  `goto` from inside `on_yes` / `foreach.do` / `option.do` aborts the
  surrounding subtree on its way out — that's intentional.

---

## 5. Conditional execution with `when:`

Any node can carry `when:`. The string is run through `{var}` substitution and
evaluated as truthy:

| Result of `subst(when)`         | Outcome    |
|---------------------------------|------------|
| empty / `no` / `false` / `0` / `off` (case-insensitive, trimmed) | **skip** |
| anything else                                                    | **run**  |

```yaml
- id: rebuild
  type: confirm
  prompt: Rebuild?
  on_yes:
    - run: make build
    - {run: echo yes, capture: rebuilt}
  on_no:
    - {run: echo no, capture: rebuilt}

# Only loads the image if step "rebuild" said yes.
- run: minikube image load my/app
  when: "{rebuilt}"
```

Two semantics worth remembering:

1. **`when:` short-circuits at the top of every node**, before the type-specific
   logic. So `when:` on a `confirm` skips the prompt entirely; `when:` on a
   `foreach` skips the whole loop; `when:` on a `goto` skips the jump.
2. **`when:` goes through the same lazy-prompt substitution as `run`/`prompt`**.
   If `{rebuilt}` was never captured, the user gets prompted for it. If you want
   "missing → skip" semantics, capture a default upstream:
   ```yaml
   - id: defaults
     run: echo no
     capture: rebuilt
   ```

---

## 6. Control flow rules

Recap, because these come up constantly:

- **Top-level nodes run in declaration order.** A subtree (`on_yes`, `on_no`,
  `option.do`, `foreach.do`) finishes and execution returns to the parent's
  next sibling.
- **`goto` only targets top-level ids.** Jumping into a nested subtree is not
  possible; jumping out of one is fine and aborts the surrounding subtree.
- **Confirm `on_yes`/`on_no` are optional** — leaving one off is the idiom for
  "do nothing on that answer".
- **`when:` is evaluated before everything else** for the node it sits on.

---

## 7. State and re-runs

After every run (success **or** failure), oprun writes
`$XDG_STATE_HOME/oprun/<flow>.json` (default `~/.local/state/oprun/<flow>.json`).
What gets persisted:

| Field in state | Source                                    | Keyed by             |
|----------------|-------------------------------------------|----------------------|
| `string_vars`  | string-shaped vars in `r.vars`            | variable name        |
| `list_vars`    | list-shaped vars in `r.vars`              | variable name        |
| `confirms`     | `confirm` answers                         | **node id** (no id → not stored) |
| `choices`      | static `choose` selections                | **node id** (no id → not stored) |

On the next run:

- `vars:` and `input` nodes pre-fill with the saved value (still re-asked, but
  the field is editable).
- Static `choose` pre-highlights the saved selection (filtered against current
  options).
- Dynamic `choose` (with `store:`) pre-selects the saved value(s), filtered.
- `confirm` pre-highlights the saved Yes/No when the node has an `id`.
- Lazy `{var}` references **skip the prompt entirely** when the variable is
  already in the saved string vars.

To "reset" a flow, delete its state file:
`rm ~/.local/state/oprun/<name>.json`.

---

## 8. Cookbook

### 8.1 Gate a step on an earlier yes/no

`confirm` doesn't write a variable, so capture a sentinel and gate with
`when:`. See [section 4.2](#42-confirm--yesno-question) and section 5.

### 8.2 Multi-select then iterate

```yaml
- type: choose
  prompt: Which tests?
  multi: true
  options_cmd: ls tests/*_test.go | awk -F/ '{print $NF "\t" $0}'
  store: test_file

- type: foreach
  var: test_file
  do:
    - run: go test -run "$(basename {test_file} _test.go)" ./...
```

### 8.3 Dynamic options with separate label and value

The `\t` (literal tab) splits each stdout line. Show short basenames in the
menu, store full paths for downstream commands:

```yaml
options_cmd: ls /opt/configs/*.json | awk -F/ '{print $NF "\t" $0}'
```

### 8.4 Capture a value, then branch on it

```yaml
- run: kubectl config current-context
  capture: ctx

- type: confirm
  prompt: "Context is {ctx} — proceed?"
  on_no:
    - {type: goto, goto: end}
```

### 8.5 "Local override" of an autodetected value

The lazy-prompt rule means you can have a step that *tries* to fill a
variable, with a manual fallback:

```yaml
- run: minikube image ls | grep my-app | head -n1 || echo my/app:latest
  capture: image_name
# downstream: `{image_name}` — no further prompt unless capture failed
```

---

## 9. Iterating on a flow

- **Run a draft by path**: `oprun run ./draft.yaml`. No need to install it
  anywhere.
- **See what got persisted**: `cat ~/.local/state/oprun/<name>.json`.
- **Reset answers**: `rm ~/.local/state/oprun/<name>.json`.
- **List discovered flows**: `oprun list`.
- **Validate as you write**: keep the `yaml-language-server` directive at the
  top of the file and use a YAML LSP-aware editor — typos and missing required
  fields surface as squiggles.

---

## 10. Editor setup

A JSON Schema lives at [`flow.schema.json`](./flow.schema.json). Per-file:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/wufe/oprun/main/flow.schema.json
```

Or project-wide in `.vscode/settings.json`:

```json
{
  "yaml.schemas": {
    "https://raw.githubusercontent.com/wufe/oprun/main/flow.schema.json": "**/.oprun/flows/*.yaml"
  }
}
```

The schema gives field completion, hover documentation for every field,
required-field enforcement, and per-type discrimination (e.g. `run:` is only
valid on `exec`).

---

## 11. Field reference

### Flow

| Field         | Type       | Required | Notes                                            |
|---------------|------------|----------|--------------------------------------------------|
| `name`        | string     | yes      | Identifier; also the state filename basename.    |
| `description` | string     | no       | Shown in `oprun list`.                           |
| `vars`        | list       | no       | Prompted at flow start.                          |
| `nodes`       | list       | yes      | Top-level node sequence.                         |

### Var

| Field     | Type   | Required | Notes                                                |
|-----------|--------|----------|------------------------------------------------------|
| `name`    | string | yes      | Referenced as `{name}`.                              |
| `prompt`  | string | no       | Defaults to `name`.                                  |
| `default` | string | no       | Used only if no prior run saved a value.             |

### Node (common)

| Field         | Type                                                       | Required | Notes                                          |
|---------------|------------------------------------------------------------|----------|------------------------------------------------|
| `id`          | string                                                     | no       | Required for `goto` targets and for persisting `confirm`/static-`choose` answers. |
| `type`        | `exec` \| `confirm` \| `choose` \| `input` \| `goto` \| `foreach` | no | Defaults to `exec`.                            |
| `description` | string                                                     | no       | Free-form note.                                |
| `when`        | string                                                     | no       | Truthy-string gate; see [section 5](#5-conditional-execution-with-when). |

### Per-type fields

| Type      | Required          | Optional                              |
|-----------|-------------------|---------------------------------------|
| `exec`    | `run`             | `dir`, `capture`                      |
| `confirm` | `prompt`          | `on_yes`, `on_no`                     |
| `choose`  | `prompt` + (`options` xor `options_cmd`) | `multi`, `store`            |
| `input`   | `store`           | `prompt`                              |
| `goto`    | `goto`            | —                                     |
| `foreach` | `var`, `do`       | `as`                                  |

### Option (under static `choose.options`)

| Field   | Type   | Notes                                                        |
|---------|--------|--------------------------------------------------------------|
| `label` | string | Displayed and stored value. Required.                        |
| `do`    | list   | Subtree run on selection. Mutually exclusive with `goto`.    |
| `goto`  | string | Top-level node id to jump to on selection.                   |

---

## Troubleshooting

| Symptom                                           | Likely cause                                                                                  |
|---------------------------------------------------|-----------------------------------------------------------------------------------------------|
| Unexpected prompt for an unfamiliar variable      | Typo in a `{var}` reference triggered a lazy prompt. Check spelling against `vars:`/`capture:`/`store:`. |
| `goto: unknown id` at runtime                     | Target id only exists in a nested subtree, or doesn't exist at all. Move the target to a top-level node. |
| `confirm` answer not remembered between runs      | Node has no `id`. Add one — persistence is keyed by id.                                       |
| Multi-select selection lost / showing wrong type  | Same variable name used by both a single-select and multi-select. Rename one.                 |
| `when:` always runs the node                      | The captured value is something other than the falsy set (`""`, `no`, `false`, `0`, `off`). Echo `no` (not e.g. `0` then later `disabled`) to be safe. |
| `options_cmd` shows no options                    | The command produced no stdout lines. oprun raises `options_cmd produced no options` rather than running the choose with an empty list. |

---

## When this document goes stale

Anyone changing flow YAML semantics — adding a node type, changing
substitution rules, changing persistence shape, adding fields — must update:

1. `flow.go` — the `Node` struct.
2. `runner.go` — the `runNode` switch and helpers.
3. `flow.schema.json` — the schema branch.
4. **This file (`FLOWS.md`)** — sections 4 (per-type), 7 (persistence), 11
   (field reference).
5. The README's "Node types" table.

A change that lands in 1–3 but not 4–5 will silently rot user-facing docs.
