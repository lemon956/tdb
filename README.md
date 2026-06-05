# TDB

TDB is a full-screen terminal database manager for MongoDB, MySQL, Redis, and Doris.

The interface uses a two-panel layout: a colored navigation panel, a workspace panel, a header that shows one tab per open connection (after the `TDB` badge, with a trailing `+` to open the connection manager), and a footer split into focus-aware keybinding hints on the left and a status message on the right.

Multiple connections can be open at once. Opening a connection adds a connection tab instead of replacing the current one (opening an already-open connection just switches to it). Switch connections with `Alt+←`/`Alt+→`, `Alt+1`..`Alt+9`, or by clicking a connection tab; the active connection tab is highlighted. Each connection keeps its own navigation tree and workspace tabs. `Ctrl+W` closes the active workspace tab, and once a connection has no workspace tabs left it closes the connection itself. The status message is color-coded by kind (`✓` success, `✗` error, `!` warning, `·` info). While a connection, query, or other database operation is running, the footer shows an animated spinner (e.g. `⠙ Running query…` / `⠹ Connecting…`) and the UI stays responsive.

At startup TDB checks the environment for Nerd Font support. If a Nerd Font is found it uses Nerd Font navigation icons; otherwise it falls back to Unicode icons. Set `TDB_ICON_STYLE=nerd` or `TDB_ICON_STYLE=unicode` to override detection.

## Build And Run

```bash
go test ./...
go build -o tdb ./cmd/tdb
./tdb --config ~/.config/tdb/tdb.enc
```

If the checkout has an invalid or empty `.git` directory, build with:

```bash
go build -buildvcs=false -o tdb ./cmd/tdb
```

## First Use

When the TUI opens, enter a master password. TDB uses it to encrypt and unlock the local config file. Connection passwords are stored inside that encrypted file.

Create connections from the TUI form:

```text
press n
choose mysql, doris, mongo, or redis
fill the fields
press Enter on the last field or click [Save]
```

Edit and delete also use sub-panels:

```text
press e    edit the selected connection in a form
press d    confirm deletion in a modal
```

MongoDB is created from a URI in the first version:

```text
mongodb://user:password@127.0.0.1:27017/app?authSource=admin
mongodb://user:password@127.0.0.1:27017
mongodb+srv://user:password@example.mongodb.net/app
```

The database part is optional for MongoDB. If it is omitted, open the connection first, expand a database in Navigation, then open a collection.

MySQL and Doris fields:

```text
id, host, port, user, password, database, readonly
```

Redis fields:

```text
id, host, port, user, password, db, readonly
```

The command bar is still available for advanced or scripted entry:

```text
new mysql local-mysql 127.0.0.1 3306 root secret app
new doris local-doris 127.0.0.1 9030 root secret app
new mongo local-mongo mongodb://root:secret@127.0.0.1:27017/app?authSource=admin
new mongo local-mongo mongodb://root:secret@127.0.0.1:27017
new redis local-redis 127.0.0.1 6379 default secret 0
```

Add `readonly` as the last argument to prevent writes through that connection.

## Core Commands

The command bar is global. Press `:` to open it, or use shortcuts and mouse actions directly. These commands work from any main page:

```text
help                     open searchable help
new                      create a connection in a modal
edit [profile-id]         edit selected or named connection in a modal
delete [profile-id]       confirm deletion in a modal
open <profile-id>
test <profile-id>
connections              return to the connection list
query                    create a query tab
history                  show current connection history in a modal
refresh                  refresh the active view
back                     go back
```

Browser commands:

```text
db <database>              expand or collapse a database
open <table|collection|key> open an object by name
/ <redis-pattern>
next
```

The `/ <redis-pattern>` command is typed in the command bar. When the Navigation panel is focused, `/` opens local tree search instead.

Data commands:

```text
insert <json>
update <id> <json>
delete <id>
```

All writes show a confirmation panel. Type `yes`, press the confirm action, or click `[ Confirm ]`.

## Workspace Tabs

The right Workspace is a tabbed area.

```text
click or open a collection/table/key  open or focus a data tab
query                                 create a query tab
ctrl+right / ctrl+l                   next tab
ctrl+left / ctrl+h                    previous tab
ctrl+w                                close current tab
```

Data tabs preview records in formatted read-only mode and keep their own scroll position. Opening the same object focuses and refreshes the existing data tab instead of creating duplicates. Query tabs use a different color from data tabs; executing a statement in a query tab keeps the statement and result inside that same tab and moves focus to the result area. Tab labels use equal widths. If there is not enough room for `database.object`, the tab hides the database name and keeps the object name.

## Panels, Keys, And Mouse

TDB supports keyboard-first navigation with mouse-equivalent actions for non-text operations.

Global keys:

```text
tab / shift+tab  move focus between Navigation and Workspace
up/down          move selection or scroll result rows
h / l            move focus left or right
H / L            scroll Navigation text left or right when Navigation is focused
shift+left/right scroll Navigation text left or right when Navigation is focused
j / k            move selection up or down
enter            expand/collapse a database, preview an object, then show metadata on the same object
/                search loaded Navigation nodes when Navigation is focused
:                show and focus the command bar
?                open the help sub-panel
q / esc          go back
ctrl+c           quit
ctrl+left/right  switch Workspace tabs
ctrl+h/l         switch Workspace tabs
ctrl+w           close the active modal, or close current Workspace tab when no modal is open
```

The command bar is hidden until it is focused with `:` or needed by search/help workflows. `Tab` never enters Command mode. While the command bar is open, Navigation and Workspace lose their active highlight and focus returns to the previous panel after the command runs. Type `help` in the command bar, or press `?`, to open the searchable help modal without leaving the current page. Status and shortcut errors are shown in the bottom footer.

Mouse behavior:

```text
click a panel          focus that panel
click a connection     select the connection
double-click connection open the connection
click a database       highlight the database
click highlighted database expand or collapse the database
double-click database  expand or collapse the database
click an object        highlight the table, collection, or Redis key
click highlighted object open/focus its data tab, then show metadata on the same object
double-click object    open/focus its data tab, then show metadata on the same object
click form fields      focus the selected field
click [Save]/[Cancel]  submit or close the form
mouse wheel            scroll the focused list, result area, or modal
drag the divider       drag the border between the Navigation and Workspace panels to resize their widths
click Confirm/Cancel   answer a pending write/delete confirmation
click [Close]          close a help, history, or error modal
```

Dragging the vertical divider between the two panels only changes their widths; heights and everything else stay the same. The split resets to its automatic proportion when the override is cleared.

After unlocking, focus starts on the left Navigation panel so arrow/`j`/`k` immediately move the selection there. The Workspace shows the active database name on the line above the tab bar.

Query and CRUD execution errors appear in a red workspace box with a `[ Close ]` button.

If a terminal does not support mouse events, all operations remain available through the keyboard and command bar.

Navigation is a tree. Connection, database, collection/table/key, and metadata rows use distinct prefixes. Moving the highlight across databases only changes the left panel. Press Enter or click the highlighted row again to expand a database, then choose a table, collection, or Redis key. The first open shows data in a Workspace tab; opening the same object again shows fields, indexes, or key metadata. Long rows do not wrap; use `H`/`L` or `shift+left/right` to move the horizontal viewport. Use `/` while Navigation is focused to search loaded tree nodes by keyword. Search jumps the cursor to the first match without hiding other rows; press `n` for the next match and `Esc` to exit search mode.

## Suggestions And Navigation

The command bar has local keyword suggestions. It does not send query text to any AI service.

```text
ctrl+space  show or hide query suggestions
tab         open command suggestions only after `:` has focused the command bar, then cycle them
up/down     move through visible suggestions
enter       accept the highlighted command suggestion; press enter again to run it
esc         close suggestions, or go back when suggestions are hidden
```

Query suggestions are driver-specific and also include the built-in functions/commands for that engine plus the field/column names of the table or collection in the current statement. Field suggestions show the field's data type after the name (e.g. `name  varchar(64)`, `age  int`). Matching is keyword/substring based (typing `order` surfaces `ALONE_ORDER_LOG`), with prefix matches ranked first.

- MySQL/Doris: SQL keywords plus a broad built-in function set grouped by category (aggregate, string, numeric, date, json, window, …). MySQL and Doris are differentiated — Doris adds its analytics functions (`APPROX_COUNT_DISTINCT(`, `BITMAP_UNION(`, `HLL_UNION_AGG(`, `ARRAY_*`, …) while MySQL adds its own (`FIND_IN_SET(`, `INET_ATON(`, `MD5(`, …). Each suggestion's category is shown after the name.
- Redis: commands across all data types — keys, string, hash, list, set, sorted-set, bitmap/HLL, pub/sub, and server (e.g. `INCR`, `HSCAN`, `ZRANGEBYSCORE`, `PFADD`, `SUBSCRIBE`).
- MongoDB: collection methods (`find`, `aggregate`, `findOneAndUpdate`, `bulkWrite`, …), query operators (`$in`, `$exists`, `$elemMatch`, …), update operators (`$set`, `$addToSet`, `$currentDate`, …), and — inside `aggregate([…])` — pipeline stages (`$match`, `$group`, `$lookup`, …) and expression operators (`$sum`, `$cond`, `$dateToString`, …).

The table/collection is detected from the statement (the table after `FROM`/`JOIN`/`UPDATE` for SQL, or `db.<collection>` for MongoDB), and its field list and types are loaded from metadata on demand.

Data results support windowed rendering:

```text
up/down/pageup/pagedown  scroll rows or documents
left/right               scroll table columns
```

## Vim Modes And Copy

Query tabs open as a ready-to-type input row. A new query tab starts in insert mode, shows a dim `Type a query…` placeholder while empty, and renders a status line with the active mode plus the `Enter run · Ctrl+J newline · Ctrl+S history · Tab complete` hint. They use a small Vim-style editor underneath:

```text
i           enter insert mode
esc         return to normal mode
v           enter visual mode
d           delete the selected query text in visual mode
y           copy the selected query text in visual mode
enter       execute the current query buffer (accepts a visible suggestion first)
ctrl+j      insert a newline (also alt+enter) for multi-line JSON/SQL
ctrl+s      search query history in a floating popup (insert, normal, or result focus)
```

In insert mode, `Enter` runs the query when no suggestion popup is showing; if a suggestion is visible, the first `Enter` accepts it and the next one runs the query. After a query executes, the input row is cleared and focus moves to the result area. Press `i` from the result area to return to insert mode in the editor.

Opening an object (Enter or click in the navigation tree) moves panel focus straight to the opened data tab, so you can scroll/copy without switching panels first. The workspace tab bar shows the active tab's database as a `▸ <db>` label on the right of the tab-bar line. For query tabs you can rebind which database the tab runs against: press `Ctrl+D` (or click the `▸ <db>` label) to open a floating database picker, type to filter, `↑/↓` to select, `Enter` to switch; the tab's execution and completions follow the new database (the navigation tree is unaffected, and each query tab keeps its own database).

The query input row is drawn as a color-tinted block so it stands apart from the panel, and the result block sits below a `▌ Results` header bar with aligned, alternately-shaded columns. `Ctrl+S` opens query history as a floating popup centered over the query view (the view stays visible behind it); type to filter, `↑/↓` to select, `Enter` to load the statement into the input row, `Esc` to close.

Data tabs are read-only but support Vim-style movement, visual selection, and copying:

```text
h/j/k/l or arrows  move the result cursor
0/Home, $/End      jump to line start / end
w, b               move to next / previous word
v                  start visual selection
y                  copy the selected display text
i/d/x/delete        rejected with a read-only status message
```

Data tabs show their current Vim state (`NORMAL` or `VISUAL`), result focus, character cursor row/column, and visual selection range. Copied text is stored internally and sent to the system clipboard first. If native clipboard commands are unavailable, TDB falls back to OSC52 when the terminal supports it.

For SQL (MySQL/Doris) query results, the result area is row-selectable: `j`/`k` (or up/down) move a highlighted row cursor and `Enter` opens a single-row detail subpage (visidata-style). The detail page lists every field on its own line as `FIELD  value`; long values are not truncated, so use `h`/`l` (or left/right) to scroll horizontally and see the full value. `v` starts a visual selection and `y` copies it; pressing `y` without a selection copies the current field's full value. `Esc` (or `q`) returns to the result list.

History is shown in a modal and is scoped to the current connection. Entries are newest first.

```text
history  open current connection history
up/down  select a history entry while the modal is open
pgup/down scroll the modal faster
esc      close the modal
ctrl+w   close the modal
```

## Redis Behavior

Redis key browsing uses cursor-based `SCAN`, not full keyspace loading. The first page loads up to 100 keys, `/ <pattern>` changes the pattern, and `next` loads the next cursor page.

Typed Redis CRUD supports string, hash, list, set, and zset. Examples:

```text
insert {"type":"string","value":"hello","ttl_seconds":60}
insert {"type":"hash","field":"name","value":"Ada"}
insert {"type":"list","value":"item"}
insert {"type":"set","member":"blue"}
insert {"type":"zset","member":"Ada","score":1}
```

## Query Console

MySQL and Doris execute SQL directly. Redis executes a Redis command such as `GET key` or `HGETALL user:1`. MongoDB accepts JSON:

```json
{"database":"app","collection":"users","filter":{"status":"active"},"limit":100}
```

History stores statements, filters, status, duration, and affected rows. It does not persist result payloads.
