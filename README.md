# TDB (Rust)

A full-screen terminal database manager for MongoDB, MySQL, Redis, and Doris —
the Rust rewrite of the Go TDB, built on [ratatui](https://ratatui.rs) +
[crossterm](https://docs.rs/crossterm) with a [tokio](https://tokio.rs) async
core.

This is the Rust rewrite of the original Go TDB (the Go sources were removed on
the `rust-rewrite` branch and remain on `main`). The encrypted vault format is
**byte-for-byte compatible** with the Go binary: an existing `tdb.enc` unlocks
here with the same master password, and vice-versa.

## Build and run

```bash
cargo test          # unit tests (no database needed)
cargo build --release
./target/release/tdb --config ~/.config/tdb/tdb.enc
```

The first run asks for a master password and creates the encrypted vault. Then:

- `n` new connection · `e` edit · `d` delete · `t` test · `Enter` open
- `Ctrl+H` / `Ctrl+L` switch panel focus · `j/k` move · `Enter` open/expand
- `Alt+←/→`, `Alt+1..9` switch connection tabs
- In a query tab: type SQL, `Ctrl+Enter` to run, `Tab`/`Shift+Tab` to complete
- `Ctrl+K` AI assistant · `Ctrl+R` query history · `/` search the nav tree
- `:export csv|json [path]` · `:copy csv|json` · `:game` · `:q` quit

## Architecture

Immediate-mode UI: every frame recomputes layout into a set of `Rect`s that are
used both to draw widgets and to resolve mouse clicks, so a click can never
drift from what was drawn. Async DB/AI work runs on tokio tasks that send typed
results back to the event loop over a channel; a `CancellationToken` makes any
in-flight operation cancellable with `Esc`, with a separate slot for AI.

```
src/
  config/      encrypted vault (AES-256-GCM, Go-compatible key derivation)
  db/          adapter enum + sql (MySQL/Doris) / redis / mongo drivers
  suggest/     completion engine + embedded syntax data (include_str!)
  history/     query history with fingerprint de-duplication
  validate/    non-blocking syntax checks
  ai/ aichat/  local AI CLI (claude/codex) + conversation persistence
  result.rs    result sets + CSV/JSON export
  app/         immediate-mode TUI: event loop, layout/render, input, actions,
               theme, syntax highlighting, AI panel, game, crash recovery
```

## Status

Full feature parity with the Go version:

- Encrypted vault (bidirectional Go compatibility), all four drivers
- Connection manager + form, navigation tree with Nerd Font / Unicode icons
- Query editor with **Vim modes** (normal/insert/visual, motions, `dd`/`yy`/`p`,
  `u` undo), syntax highlighting, and Tab completion
- Result table/document views with pagination and in-result `/` `n`/`N` search
- Multi-connection sessions, async with cancellation / timeout / spinner
- Mouse hitboxes + **drag-to-select copy** (box-drawing glyphs stripped)
- AI assistant panel (slash commands, SQL extraction + `Ctrl+Y` insert)
- Query history recording and recall (`Ctrl+R`)
- CSV/JSON export and clipboard copy, non-blocking validation, panic recovery,
  and the dino game (`:game`)
