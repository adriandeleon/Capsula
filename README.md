# Capsula

A terminal UI for managing `~/.ssh/config`.

Status: **usable**. Browse, add, edit, delete, save, connect and reachability
checks all work. Key generation and `ssh-copy-id` are not wired up yet.

```
make build
./capsula
```

| key     | action                                             |
| ------- | -------------------------------------------------- |
| `a`     | add a host                                         |
| `e`     | edit the selected host                             |
| `d`     | delete it (asks first)                             |
| `s`     | save to disk                                       |
| `enter` | connect with `ssh`                                 |
| `r`     | check reachability                                 |
| `p`     | fix config permissions (asks first)                |
| `/`     | filter, by alias, hostname or user                 |
| `q`     | quit (asks first if there are unsaved changes)     |

Edits are held in memory until you press `s`, so the file on disk is untouched
until you say so. Connecting is refused while there are unsaved changes, since
ssh reads the file from disk and would otherwise use settings that differ from
what is on screen.

## Design notes

Three decisions shape everything else.

### Writing splices the original bytes

`~/.ssh/config` is hand-edited, so a tool that reformats it is a tool people
uninstall. Capsula keeps the bytes it loaded, and writing means emitting the
original lines for every block the user did not touch. Round-tripping an
unedited file is exact *by construction* rather than by any library's diligence
— which also makes CRLF files, missing trailing newlines and unusual spacing
correct for free.

This matters concretely. Capsula parses with
[`kevinburke/ssh_config`](https://github.com/kevinburke/ssh_config), whose
parser is excellent, but whose `Config.String()` is lossy in two ways that show
up immediately on a real config:

| written        | `Config.String()` returns |
| -------------- | ------------------------- |
| `\tUser ops`   | `&nbsp;User ops` (tab collapsed to one space) |
| `HostName=box` | `HostName = box`          |

Both come from unexported fields — `leadingSpace` is a column count re-emitted
as spaces, and `hasEquals` is a bool. There is a third, sharper edge: `KV.String()`
prefers an unexported `rawValue` over the public `Value` field, and `rawValue` is
set for *every* parsed line, so mutating the parse tree directly compiles, runs,
reports success, and writes the old value.

Even within an edited block, only lines whose value actually changed are
re-rendered; the rest are emitted verbatim, so one edit does not restyle its
neighbours.

`TestRoundTrip` pins all of this against fixtures containing tab indentation,
`Key=Value` forms, inline comments, negated patterns, quoted values, CRLF
endings and a file with no trailing newline.

### Truth comes from `ssh -G`, not from our parser

Precedence in `ssh_config` is subtle: first value wins per keyword, `Match`
blocks are evaluated in order and can run commands, `Include` splices files
inline, and tokens like `%h` are expanded late. Reimplementing that reliably
produces confident wrong answers — showing `Port 22` because that is what the
block on screen says, when a `Host *` further up already set `2222`.

So there are two representations. The parse tree is for **editing**; `ssh -G` is
for **truth**. The detail pane shows both and flags where they disagree:

```
Effective  (ssh -G)
  hostname bastion.example.com
  user ops
  port 2222      ← block says 22
  key missing: ~/.ssh/id_ed25519_prod
```

### An edit never drops what the form cannot show

There are well over a hundred ssh keywords and the form has six fields. A form
that rebuilt a block from its own inputs would delete every keyword it has no
field for — someone's `ControlMaster`, `SetEnv`, `LocalForward` — which is data
loss wearing the costume of an edit. So the block's existing parameters are the
base and only managed keywords are touched, keeping their original position
(within a block, ssh uses the first value it finds for a keyword). Repeated
keywords such as `IdentityFile` keep their later occurrences, since a
single-valued field has nothing sensible to say about the second one.

### Permission problems are shown, not fixed behind your back

ssh refuses a config file other users can write ("Bad owner or permissions"),
and every host stops working at once with a message that does not obviously
point at a permission bit. A `~/.ssh` others can write is not checked by ssh at
all, but it lets a local user swap your config or keys out — so locking down the
keys inside does not save you.

Both are reported in a banner that stays put, with `p` to fix. Capsula does not
chmod on its own initiative: permissions on a directory you created are yours to
choose. The fix clears only the offending bits, and the banner clears only
because a re-audit found nothing, never because the chmod was assumed to have
worked.

Group- or world-*readable* is deliberately not reported. ssh does not care, it
leaks nothing but filenames, and warning about it would train you to ignore the
line that carries the two problems that matter.

### Probes report Unknown rather than guess

A red dot next to a host the user knows is up costs more trust than showing
nothing. A host behind `ProxyJump` cannot be dialled directly from here even
though ssh connects to it fine, so it is reported as skipped, never as down.

## Safety

- Writes are atomic: temp file in the same directory, `fsync`, rename. A crash
  mid-save leaves the old file or the new one, never half of either.
- The previous contents are kept at `<file>.capsula.bak`.
- The config is written `0600` and `~/.ssh` is created `0700`, since ssh refuses
  group- or world-accessible files.
- Values containing newlines are rejected. A value is often pasted from
  elsewhere, and a newline would splice arbitrary directives into the config.
- `Match` blocks are shown but never rewritten — their conditions decide when
  they fire.
- If the line scan and the parser disagree about a file's structure, that file
  becomes read-only rather than being spliced against the wrong lines.

## Layout

```
cmd/capsula        entry point
internal/sshconf   parse, edit, write; byte-exact round-trip
internal/effective ssh -G wrapper
internal/keys      IdentityFile resolution and permission checks
internal/probe     bounded-concurrency reachability
internal/ui        Bubble Tea models
```

All I/O happens inside a `tea.Cmd`; `Update` is a pure function of
`(model, message)`. That is what lets the whole interface — including layout at
awkward terminal sizes — be tested in-process without a TTY and without spawning
`ssh`.

## Not done yet

- `ssh-copy-id` and key generation (`internal/keys` inspects and repairs
  permissions, but does not create keys).
- Choosing which included file a new host goes into — new hosts go to the root
  config, existing ones are edited where they already live.
- Editing the global defaults block and `Match` blocks.
- Reordering hosts, which matters because order changes meaning.
