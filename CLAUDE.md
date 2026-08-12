# Capsula

A terminal UI for managing `~/.ssh/config`. Go 1.26, Bubble Tea v1, huh v1,
`kevinburke/ssh_config` for parsing. Module `github.com/adriandeleon/Capsula`,
binary `capsula`.

## Commands

- Run: `go run ./cmd/capsula` (add `-config testdata/whatever` to avoid touching
  your real config)
- Build: `make build` ⇒ `./capsula`
- Test: `make test`; `make race` (the probe package fans out a goroutine per
  host); `make lint` = `gofmt -l .` + `go vet ./...`

Run `make lint` and `make test` before proposing a change. Both are fast.

## The one rule everything else serves

**A `~/.ssh/config` is hand-written, and Capsula must give back byte for byte
everything the user did not explicitly ask to change.** A tool that reformats
this file is a tool people uninstall, and a tool that mangles it can lock
someone out of every host they have.

`TestRoundTrip` in `internal/sshconf` is the load-bearing test. If it ever
fails, stop and fix that before anything else.

## Architecture

```
cmd/capsula        flags, load, hand off to the UI
internal/sshconf   parse, edit, write. The only package that touches ssh_config
internal/effective  "ssh -G" wrapper — resolved configuration
internal/keys      IdentityFile inspection, permission audit and repair
internal/probe     bounded-concurrency reachability
internal/ui        Bubble Tea models
```

### Two representations, deliberately

The parse tree in `sshconf` is for **editing**. `ssh -G` is for **truth**.

Do not add precedence logic to `sshconf` — no "what port does this host really
use". Resolution in ssh_config is subtle (first value wins per keyword, `Match`
blocks evaluate in order and can run commands, `Include` splices inline, tokens
expand late) and reimplementing it produces confident wrong answers. Ask ssh.

### Writing is byte-splicing, not rendering

`File` keeps the lines it loaded plus a `spans` table, one `[start, end)` line
range per block, index-aligned with `cfg.Hosts`. Writing joins the lines. An
edit re-renders only the block that changed and splices it in via
`File.splice`, shifting later spans.

The invariant that makes this safe: **within a block, node *k* corresponds to
exactly one raw line** — the line after the header for *k*=0, and so on.
`checkAlignment` verifies it at load; a file that fails becomes read-only
(`File.Editable()` false) rather than being spliced against the wrong lines.
If you change how blocks are split or parsed, keep that invariant or the check
will start failing files it should accept.

## Traps that have already bitten, with the tests that pin them

**`ssh_config` cannot be used to write.** `Config.String()` collapses tab
indentation to a single space (`leadingSpace` is a *column count* re-emitted as
spaces) and rewrites `Key=Value` as `Key = Value` (`hasEquals` is a bool).
Never reach for `cfg.String()` as a shortcut.

**`kv.Value = x` is a silent no-op.** `KV.String()` prefers an unexported
`rawValue`, which the parser populates for *every* line. Assigning to the public
`Value` field compiles, runs, reports success and writes the old value. This is
why edits go through render-then-reparse. → `TestUpdateActuallyWritesTheNewValue`

**Hand-built `&ssh_config.KV{}` is written unindented**, since `leadingSpace` is
unexported with no setter. New content is rendered as text and re-parsed so the
library's own lexer fills in the private fields.

**An edit must be committed into `f.lines` immediately.** An earlier design kept
rendered text in a side map; that works exactly once, because the second edit
indexes the new node list against the old lines.
→ `TestRepeatedEditsOfTheSameBlock`

**`return m, action(&m)` is undefined behaviour** when `action` mutates `m`. Go
does not specify the order between a plain operand and a call. Call first, then
return. → `TestDeleteAsksFirstThenRemoves`

**huh forms are message-driven.** Enter returns *commands*; the field advance
happens when the resulting messages are fed back. A test that calls `Update` and
discards commands leaves the form on field one, concatenating everything into
it — which looks exactly like an app bug. Use `pump`/`sendKey`/`typeText` from
`internal/ui/pump_test.go`. It recognises command batches *structurally*,
because `tea.Batch` yields an exported `BatchMsg` but `tea.Sequence` does not.

**Widgets do not truncate to the width you give them.** `bubbles/list` renders
its help footer at natural width regardless of `SetSize`, which overflows the
pane and wraps every row below it. Anything rendered must be truncated
explicitly. → `TestFrameNeverExceedsTerminalWidth`, which covers list, confirm,
form and empty modes.

## Conventions

- **All I/O lives in a `tea.Cmd`. `Update` stays a pure function of
  `(model, message)`.** That is what makes the interface testable without a TTY
  and keeps a slow subprocess off the render path. Never call `exec`, `os.Stat`
  or a network dial from `Update`.
- **Report uncertainty as uncertainty.** A `ProxyJump` host is unanswerable, not
  down. A chmod that may have failed is re-audited, not assumed. A confident
  wrong answer about someone's production infrastructure costs more than a blank.
- **Prefer a pure function plus a unit test** over logic embedded in a model.
  `MergeParams`, `blockSpans`, `Audit`, `InlineDiff`-style helpers all exist so
  the interesting decisions can be tested without a terminal.
- **New keywords in the edit form** go in `managedKeys` (`internal/ui/form.go`).
  Everything not listed there is preserved untouched by `sshconf.MergeParams`;
  never rebuild a block from form fields alone. → `TestEditThroughFormKeepsUnmanagedKeywords`
- **Fixtures should be ugly.** `internal/sshconf/testdata` deliberately contains
  tab indentation, `=` separators, inline comments after a `Host` line, negated
  patterns, quoted values, CRLF and a missing trailing newline. Add to them when
  touching the parser or writer.
- Keep the docs current in the same change: `CHANGELOG.md` for behaviour,
  `README.md` for anything user-facing, `NOTICE` when dependencies change
  (regeneration command is at the bottom of that file).

## Safety properties not to regress

- Saves are atomic (temp file in the same directory, `fsync`, rename) with the
  previous contents kept at `<file>.capsula.bak`.
- The config is written `0600`; `~/.ssh` is created `0700`.
- Values containing newlines are rejected — one would splice arbitrary
  directives into the file, and values get pasted from elsewhere.
- `Match` blocks are shown but never rewritten.
- Block order is never sorted; order is meaning.
- Capsula never chmods on its own initiative; it warns and offers.

## Git

Do not commit, push or merge unless asked. When asked, end commit messages with:

```
Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
```
