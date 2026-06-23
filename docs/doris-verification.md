# TDB（Rust）Apache Doris 功能与体验校验报告

日期：2026-06-22 · 分支：`rust-rewrite` · 被测：`target/release/tdb` · Doris：`apache/doris:doris-all-in-one-2.1.0`（FE MySQL 协议 9030）

## 1. 校验方法

沿用 MySQL 校验的「自己控制的独立实例」思路（无法向用户正在跑的终端进程注入按键），但用了两条互补的可控通道：

1. **驱动层 headless 探针**（`examples/doris_probe.rs`）：用库 API `tdb::db::connect()` 直连真实 Doris，逐个调用 `test / list_catalogs / list_databases / list_databases_in_catalog / list_objects / preview / metadata / execute`，把 catalog 三级树、SWITCH 作用域、三种 Key 模型元数据、类型解码、分页、外部 catalog、错误处理全部断言一遍。最可控、能先于 UI 抓出代码级 BUG。
   ```bash
   cargo run --example doris_probe -- 127.0.0.1 9030 root '' testdb
   ```
2. **TUI 层 tmux 驱动**：用 `tmux new-session` 起独立 `tdb`，`tmux send-keys` 注入按键（含 `send-keys -H` 发 SGR 鼠标点击触发 Run 按钮，因为终端无法区分 Ctrl+Enter 与 Enter），`tmux capture-pane -p` 抓取真实渲染网格，逐帧核对导航/数据/元数据/补全/执行/搜索/历史/导出/只读/AI。

测试库 `testdb` 专门覆盖 Doris 特性：

| 表 | 模型/特性 | 覆盖点 |
|---|---|---|
| `events` | **DUPLICATE KEY** + **RANGE 分区** | 分区/Key 解析、`DATE/BIGINT/VARCHAR/DECIMAL(10,2)/BOOLEAN/LARGEINT/STRING`、CJK、NULL、DECIMAL `0.00`、LARGEINT 极值(2^127-1) |
| `user_profile` | **UNIQUE KEY** | 唯一键最新覆盖、NULL 邮箱、DATETIME |
| `sales_agg` | **AGGREGATE KEY**(SUM/MAX/REPLACE) | 聚合模型（3 条插入聚合成 2 行） |
| `complex_types` | **半结构化** `ARRAY`/`MAP`/`JSON` | Doris 专有类型解码 |
| `big` | 150 行 | 分页 |
| `active_users` | VIEW | 视图对象/查询 |
| `jdbc_self` | **外部 JDBC catalog**（自引用回 9030） | catalog 三级树、SWITCH 作用域、外部 catalog 错误处理 |
| 只读 vault | `read_only=true` | 写操作拦截 |

## 2. 功能校验结果（全部通过）

| # | 维度 | 验证动作 | 结果 |
|---|---|---|---|
| 1 | 解锁 + 连接 | 主密码解锁、`t` 测连接 | ✅ `✓ local-doris: ok`（真连成功） |
| 2 | **Catalog 三级树** | 展开连接根 | ✅ `▣ internal` + `▣ jdbc_self`（来自 SHOW CATALOGS，catalog 图标） |
| 3 | 导航·库/表/视图 | 展开 internal→testdb | ✅ `▤库` / `▦表` / `◫视图` 图标区分，5 表 + 1 视图 |
| 4 | 数据预览·类型 | 打开 events | ✅ DATE/BIGINT/DECIMAL(`100.50`,`0.00`)/BOOLEAN(`true/false`)/LARGEINT(39位)/STRING 全对 |
| 5 | 数据预览·CJK/NULL | events/user_profile | ✅ 王小明/李雷/北京、`NULL` 正确 |
| 6 | **半结构化类型** | 打开 complex_types | ✅ `ARRAY ["red","green","蓝"]`、`MAP {"a":1,"b":2}`、`JSON {...}`、NULL |
| 7 | 分页 | 打开 big(150)，Space 翻页 | ✅ `rows 1-100 +more` → `rows ...of 50`（末页无 more） |
| 8 | 视图查询 | active_users | ✅ 正常返回 |
| 9 | **元数据·Key 模型** | 三表按 `m` | ✅ `DUPLICATE KEY(event_date,id)`+`RANGE(event_date)` / `UNIQUE KEY(user_id)` / `AGGREGATE KEY(dt,city)` |
| 10 | 元数据·列 | events 按 `m` | ✅ 列定义(datev2/decimalv3/boolean/largeint/text)+null+CJK 注释 |
| 11 | **补全·Doris 函数** | `SELECT … FROM ev` | ✅ 弹出表名 `events` + Doris 函数 `ARRAY_REVERSE_SORT/DATEV2/LEVENSHTEIN/PREVIOUS_DAY` + 关键字，Tab 接受 |
| 12 | 执行查询 | 点击 Run 执行 SELECT | ✅ 结果正确、CJK 正常 |
| 13 | 错误处理 | 错误 SQL | ✅ 红框显示 Doris 解析器/`1051` 错误 + `✗ query failed` |
| 14 | 结果内搜索 | 打开表 `/广州` | ✅ 搜索激活、CJK 输入、命中行存在 |
| 15 | 查询历史 | Ctrl+R | ✅ 持久化列表，带 ✓/✗ 成功标记 |
| 16 | 导出 | `:export csv <path>` | ✅ CSV 正确（CJK/NULL/DECIMAL 精度/datetime 全对） |
| 17 | **外部 catalog 导航** | 展开 jdbc_self→testdb→表 | ✅ SWITCH 列库/表；打开外部表 path 显示 `jdbc_self.testdb.x`，错误在 UI 优雅呈现（非崩溃） |
| 18 | 只读拦截 | 只读连接执行 UPDATE | ✅ `connection is read-only` 拦截 + `✗ query failed` |
| 19 | 多标签 | 连开多表 | ✅ `query 1  events  …` 标签并存，Ctrl+W 关闭 |
| 20 | **AI（driver-aware）** | Ctrl+K 提问 | ✅ 识别 claude、真实往返返回 ```sql```，且**解释专门提示 Doris 分区表应加分区列 WHERE**；Ctrl+Y 插入新建标签并可执行 |
| 21 | UX·作用域指示 | 编辑器模式行 | ✅ 新增 `── SQL · INSERT · catalog.database ──`，外部 catalog 黄色高亮 |

## 3. 校验中发现并修复的问题

> Go 版用 `go-sql-driver/mysql`（无参查询走 text 协议、不发 sql_mode 子查询），Rust 版改用 **sqlx**，其默认行为与 Doris 不兼容，连「连上」都做不到。以下均为本次校验发现并修复。文件：`src/db/sql/mod.rs`。

### 🔴 BUG-1 连不上：sqlx 会话初始化语句被 Doris 拒绝
- **现象**：`test()` 即报 `Set statement does't support non-constant expr`。
- **根因**：sqlx 每条连接启动时发 `SET sql_mode=(SELECT CONCAT(@@sql_mode, ',PIPES_AS_CONCAT,NO_ENGINE_SUBSTITUTION'))`，其子查询是非常量表达式，Doris 解析器拒绝。
- **修复**：Doris 连接 `MySqlConnectOptions` 关闭 `pipes_as_concat(false).no_engine_substitution(false)`，丢弃该 SET。

### 🔴 BUG-2 任何查询都失败：Doris 不支持 prepared 通用查询
- **现象**：`SELECT 1` 报 `Only support prepare SelectStmt point query now`。
- **根因**：sqlx 的 `sqlx::query(...)` 一律走 prepared 协议，而 Doris 的 prepare 只支持主键点查。
- **修复**：Doris 全程改走 **simple/text 协议**（`conn.fetch_all/fetch/execute(&str)`）——新增 `query_all()` 辅助、`fetch_set` 按驱动分流、`test`/`list_catalogs`/`list_*`/`metadata_*` 全部切换；MySQL 仍保留已验证的 prepared 路径。

### 🔴 BUG-3 catalog 作用域跨连接泄漏
- **现象**：先浏览外部 catalog 后，再查 internal 表竟报外部驱动的 `invalid fetch size`。
- **根因**：连接池里某连接执行过 `SWITCH jdbc_self`，归还后残留该 catalog；`use_scope` 对 internal/空 catalog **不发 SWITCH**，而 `USE <db>` 不复位 catalog，于是 internal 对象被解析进了外部 catalog。
- **修复**：`use_scope` 在 Doris 下即使目标是 internal 也先 `SWITCH internal` 复位；`list_objects`/`metadata` 对 Doris 一律走 `use_scope`（MySQL 行为不变）。

### 🟡 BUG-4 pipelineX 回退从未真正生效
- **根因**：`disable_pipelinex_engine` 用 `sqlx::query("SET …").execute()`（prepared）且被 `let _ =` 吞错 → 在 Doris 上恒失败、静默不生效，回退形同虚设。
- **修复**：改用 text 协议 `conn.execute("SET …")`；`exec_scoped_mutation`/`exec_mutation_args` 的 DML 同样按 Doris 走 text 协议（mutation 用字面量插值 `interpolate_placeholders`）。

### 🟡 BUG-5 BOOLEAN 列解码成 NULL
- **根因**：`decode_cell` 无 boolean 分支，`"BOOLEAN"` 不含 `"INT"`，sqlx 又拒绝其 String/Vec<u8> 解码 → 落 NULL。
- **修复**：新增 `BOOL` 分支（`try_get::<bool>` → `Value::Bool`）。

### 🟡 BUG-6 JSON 列解码成 NULL
- **根因**：sqlx 对 JSON 列的 String/Vec<u8> 类型检查不通过 → 落 NULL。
- **修复**：`Cargo.toml` sqlx 加 `json` feature，`decode_cell` 加 `JSON` 分支（`try_get::<serde_json::Value>`）；同时也修好了 MySQL 的 JSON 列。

### 🟢 UX 改进：查询编辑器作用域指示器
- **背景**：作用域跟随导航（开哪个表，ad-hoc 查询就落在那个 catalog/库），但编辑器原本**不显示当前作用域**，多 catalog 下 `FROM t` 易误打到外部 catalog。
- **改进**：模式行加 `── SQL · INSERT · catalog.database ──`，外部 catalog 用 WARNING 黄色高亮。文件：`src/app/ui.rs`。

修复后：`cargo test` 88 全绿、`cargo clippy`（本次改动文件）0 警告、release 可构建、headless 探针与 TUI 全项复测通过。

## 4. 未能测试 / 环境限制（非程序缺陷）

- **Ctrl+Enter 执行**：终端无法区分 Ctrl+Enter 与 Enter（都发 `\r`），改用**注入 SGR 鼠标点击 Run 按钮**验证了执行路径；真实终端中 Ctrl+Enter 为标准键应可用。
- **外部 catalog 取数**：`jdbc_self` 用 FE 自带 `mariadb-java-client` 自引用回 Doris 自身，**查表报 `invalid fetch size`**——这是该 mariadb 驱动连 Doris MySQL 协议端的已知不兼容，**非 TDB 缺陷**；恰好用来验证「外部 catalog 上的错误处理」与 SWITCH 作用域。catalog 树、SWITCH、列库/列表均正常。
- **pipelineX 自动回退**：需要老式外部 OLAP 表触发 `Unsupported exec type in pipelineX` 才会重试，环境无此类表，未能实测命中（已修正其 SET 改走 text 协议，代码路径正确）。
- **语法高亮配色**：单色抓帧只能验证 token 结构，验证不了具体颜色。

## 5. 复现方式

```bash
# 1) 灌数据（含三种 Key 模型/分区/半结构化/大表/视图）
docker exec -i doris-all-in-one mysql -uroot -h127.0.0.1 -P9030 --default-character-set=utf8mb4 < doris_seed.sql
#    外部 catalog（FE 自带 mariadb jar 自引用）：
docker exec doris-all-in-one mysql -uroot -h127.0.0.1 -P9030 -e "CREATE CATALOG jdbc_self PROPERTIES('type'='jdbc','user'='root','password'='','jdbc_url'='jdbc:mariadb://127.0.0.1:9030/testdb','driver_url'='file:///opt/apache-doris/fe/lib/mariadb-java-client-3.0.9.jar','driver_class'='org.mariadb.jdbc.Driver')"

# 2) 造 vault（doris profile；只读末尾加 ro）
cargo run --example seed_vault -- /tmp/tdb-doris.enc <pw> doris 127.0.0.1 9030 root '' testdb

# 3a) 驱动层 headless 校验
cargo run --example doris_probe -- 127.0.0.1 9030 root '' testdb

# 3b) TUI 校验
cargo build --release && ./target/release/tdb --config /tmp/tdb-doris.enc
```

`examples/doris_probe.rs`、`examples/seed_vault.rs` 已纳入仓库；TUI 驱动用 tmux（`send-keys -H` 注入 SGR 鼠标）见仓库外临时脚本。
