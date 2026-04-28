# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

> **Authoring flows?** See [`FLOWS.md`](./FLOWS.md) — the step-by-step guide to writing flow YAML, the canonical reference for every field, and the troubleshooting list. **Any change to flow YAML semantics must update `FLOWS.md` alongside `flow.go` / `runner.go` / `flow.schema.json`** — see the "When editing" section below.

## Build / test / run

```bash
go build ./...        # produces ./oprun
go test ./...         # no tests in tree yet, but this is the entry point
go vet ./...
gofmt -w .

./oprun list          # list discoverable flows
./oprun <flow-name>   # run a flow by name (shorthand for `run`)
./oprun run ./path/to/flow.yaml   # run a yaml file directly
```

There is no test suite currently. When adding tests, follow standard Go conventions (`*_test.go` next to the file under test) — `go test ./...` is already the canonical entry point.

The repo ships a checked-in binary (`./oprun`). Rebuild it with `go build ./...` after touching any `.go` file; don't assume it reflects the current source.

## Architecture

This is a small CLI (~600 LoC across 5 files in `package main`) that interprets YAML "flows" as interactive shell-driven recipes. The pieces are tightly coupled — understanding any non-trivial change requires reading several files together.

### The `Node` discriminated union (`flow.go`)

`Node` is a single struct that holds the union of every node type's fields, discriminated by `Type`. Fields irrelevant to the chosen type are simply ignored by the runner. This means:

- Adding a new node kind = adding fields to `Node`, a `case` in `Runner.runNode`, and an `if/then` branch in `flow.schema.json`. All three must stay in sync — the schema is the only validation layer (the Go side just skips unset fields).
- `defaultNodeTypes` recurses into every nested node list (`OnYes`, `OnNo`, `Do`, `Options[].Do`) and fills `Type="exec"` when omitted. Any new field that holds a `[]Node` subtree must be added to that recursion or its children won't get the default.

### Execution model (`runner.go`)

Top-level nodes execute in declaration order. Branch subtrees (`on_yes`, `on_no`, `options[].do`, `foreach.do`) are nested node lists — completing one falls through to the parent's next sibling. Three non-obvious mechanics:

- **`goto` is implemented as an error/sentinel.** `runNode` returns `*gotoSignal{id}`, `Run`'s top loop unwraps it via `errors.As` and resets the index. Consequence: **`goto` can only target top-level nodes** (`findTopLevel` walks `flow.Nodes` only). A `goto` from inside `on_yes` or `foreach.do` aborts the entire surrounding subtree on its way out — that's intentional.
- **`{var}` substitution lazily prompts.** `subst` runs over `run`/`dir`/`prompt`/`options_cmd`/`when` strings; an unknown `{name}` triggers `prompt.Input(name, "")` and persists the answer. So you can reference a var that no upstream node defined and the user gets prompted at first use. Don't add "missing variable" errors here — laziness is the contract.
- **`when:` is the only conditional.** Set on any node, it's run through `subst` and tested by `truthy()` (empty / `no` / `false` / `0` / `off` skip; anything else runs). It short-circuits at the top of `runNode` *before* the type switch — so `when:` on a `confirm` skips the prompt entirely, `when:` on a `foreach` skips the whole loop, etc. Because it goes through `subst`, a `when` that references an unset var will lazily prompt; if you want "missing = skip" semantics, capture the var with a default upstream.

### Variable storage and persistence (`runner.go` + `state.go`)

Three storage layers exist; conflating them causes subtle bugs:

| Where it lives | Set by | Keyed by | Persisted? |
|----------------|--------|----------|------------|
| `r.vars` (live `map[string]any`) | `vars:`, `input.store`, `exec.capture`, `choose.store`, lazy substitution | variable name | Yes, on every `Run()` exit (including errors) |
| `state.Confirms` | `confirm` answers | **node `id`** (no id → not persisted) | Yes |
| `state.Choices` | static `choose` selections | **node `id`** (no id → not persisted) | Yes |

`r.persist()` snapshots `r.vars` into `state.StringVars`/`ListVars` *by runtime type* — switching a value from `string` to `[]string` (or vice versa) deletes it from the other map. So a `choose store: foo` with `multi: true` writes `[]string`; the same name later as a single-select string would clobber the list shape. Keep variable names disjoint by intended type.

State files live at `$XDG_STATE_HOME/oprun/<flow>.json` (default `~/.local/state/oprun/...`), one per flow. Defaults are seeded into `r.vars` *before* the flow runs, so even `vars:` declared up front are pre-filled.

### Static vs dynamic `choose` (`runChoose` in `runner.go`)

These behave differently in ways that aren't obvious from the YAML surface:

- **Static** (`options:`): each option carries its own subtree (`do`) or `goto`; on `multi: true`, all selected options' subtrees run in selection order. Defaults come from `state.Choices[node.id]`.
- **Dynamic** (`options_cmd:`): no per-option subtrees — the command's stdout lines are just labels (or `label\tvalue` pairs). The selection is written to `store` and that's it. Defaults come from `state.ListVars[store]` / `state.StringVars[store]`. Saved defaults are filtered against current options before being applied (so stale entries don't poison the prompt).

### Flow discovery (`main.go`)

Search order, first match wins: `./.oprun/flows/`, `./.flows/`, `./flows/`, `<repo-root>/.oprun/flows/`, `<repo-root>/.flows/`, `<repo-root>/flows/`, `~/.oprun/flows/`. Specificity goes cwd → repo-root → home. The `<repo-root>` entries reuse `findRepoRoot` (same bounded `.git` walk that backs `from_repo_root`) and are silently skipped when cwd is not inside a repo. `flowSearchDirs` dedupes by cleaned path so cwd-equals-repo-root doesn't double-count. A name containing `/` or ending in `.yaml`/`.yml` is treated as a literal path and bypasses the search. **Note**: discovery runs *before* the flow YAML is loaded, so it can't depend on the flow's `from_repo_root` field — discovery and `from_repo_root` are independent features that happen to share `findRepoRoot`.

### Working directory resolution (`runner.go`)

`Runner.baseDir` is the base for resolving `dir:` and the default cwd for `exec`. It is **empty** by default — meaning current behaviour is preserved exactly: relative dirs are passed through to bash, empty dir means cwd is inherited from the parent process. When `flow.from_repo_root: true`, `resolveBaseDir` walks up from cwd looking for `.git` (capped at 10 levels, blocked at the system-parent set in `systemParentBlocklist`) and stores the result in `baseDir`. The flow errors out at start if the walk fails. After that, `resolveDir` joins relative `dir:` values onto baseDir, keeps absolutes unchanged, and substitutes baseDir for empty dirs. The same path goes through `runExec` and `shellCapture` (which serves `options_cmd`), so both get consistent behaviour. Any new node type that runs commands must call `r.resolveDir(...)` rather than using a `dir:` field raw.

## When editing

- Changes to flow YAML grammar must land in **four** places: `Node` struct (`flow.go`), `Runner.runNode` switch (`runner.go`), `flow.schema.json`, and **`FLOWS.md`** (per-type sections, field reference, persistence table). The schema drives editor completion; `FLOWS.md` is the human/agent-facing authoring reference. A change that lands in code+schema but not in `FLOWS.md` will silently rot user-facing docs.
- The README's "Node types" table and "Project layout" section also mirror the above — keep them in sync.
- `prompt.go` is a thin wrapper over `charmbracelet/huh`. It's the only TTY-touching code; keep terminal logic out of `runner.go`.
