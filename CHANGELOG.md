# Changelog

All notable changes to Capsula are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Nothing has been tagged yet, so everything below sits under Unreleased.

## [Unreleased]

### Added

- Host list with a detail pane, showing each block **as written** alongside the
  **effective** configuration resolved by `ssh -G`, and flagging where the two
  disagree — the case a user cannot see by reading their own file.
- Add, edit and delete hosts. Edits are held in memory until saved, so the file
  on disk is untouched until you ask for it.
- Connect to the selected host with `ssh`, handing over the terminal and taking
  it back afterwards.
- Reachability checks with bounded concurrency, reporting a host behind a
  `ProxyJump` as unanswerable rather than as down.
- Identity file inspection: missing keys, and keys whose permissions ssh will
  reject.
- A permission banner for a config file or `~/.ssh` directory others can write,
  with a confirmed one-key fix that clears only the offending bits.
- Filtering by alias, hostname or user.
- `Include` support: hosts are listed across every included file and edited in
  the file they actually live in.

### Notes on behaviour

- **Files are never reformatted.** Writing splices the bytes that were loaded,
  so anything untouched comes back out exactly as it went in — tab indentation,
  `Key=Value` spacing, CRLF endings, a missing trailing newline. Even inside an
  edited block, only lines whose value actually changed are re-rendered.
- **Keywords the form has no field for are preserved.** Editing a host keeps its
  `ControlMaster`, `SetEnv` and everything else, in their original positions.
- **`Match` blocks are shown but never rewritten**, since their conditions
  decide when they fire.
- **Block order is never sorted**, because within ssh's resolution the first
  matching value for a keyword wins.
- **Connecting is refused while there are unsaved changes**, since ssh reads the
  file from disk and would otherwise use settings that differ from the screen.
- A file whose line scan and parse disagree is displayed but not edited.
- Saves are atomic, keep the previous contents at `<file>.capsula.bak`, write
  the config `0600` and create `~/.ssh` as `0700`.
- Values containing newlines are rejected: one would splice arbitrary directives
  into the config, and values are routinely pasted from elsewhere.

[Unreleased]: https://github.com/adriandeleon/Capsula/commits/master
