# TDB（Rust）MySQL 功能与体验校验报告

日期：2026-06-23 · 分支：`rust-rewrite` · 被测：`target/release/tdb`

## 1. 校验方法

无法向用户正在运行的终端进程注入按键（它的输入由终端模拟器持有），因此全程使用**自己控制的独立 `tdb` 实例**进行验证：

- 用 `pty.fork()` 起一个伪终端跑 `tdb`，按脚本发送按键 / SGR 鼠标事件；
- 自写的最小 VT 模拟器把程序输出的转义序列还原成字符网格，对每个场景抓帧核对；
- 数据库为用户本地 docker MySQL：`-p 3306 -e MYSQL_ROOT_PASSWORD=123456`，镜像 `mysql`（实际为 Valkey/MySQL 兼容）。

测试库 `testdb` 覆盖多种类型与边界：

| 表 | 内容 | 覆盖点 |
|---|---|---|
| `users` | id(INT) name/email(VARCHAR) age(INT) balance(DECIMAL(10,2)) status(ENUM,带注释) created_at(DATETIME) | 类型解码、ENUM 注释、CJK（王小明）、DECIMAL 边界（0.00） |
| `orders` | id user_id amount(DECIMAL) note(TEXT, 含 NULL) | NULL 显示 |
| `active_users` | VIEW | 视图对象/查询 |
| `big` | 150 行 | 分页 |
| 只读连接 | `read_only=true` 的 profile | 写操作拦截 |

## 2. 功能校验结果（全部通过）

| # | 维度 | 验证动作 | 结果 |
|---|---|---|---|
| 1 | 解锁 + 连接 | 主密码解锁、打开连接 | ✅ header 显示连接标签，`✓ connected` |
| 2 | 导航·连接节点 | Enter/h/l 折叠/展开连接根 | ✅ 折叠隐藏子树、展开恢复 |
| 3 | 导航·库/表/视图 | 展开 testdb | ✅ 列出 users/orders/active_users，**图标区分** ▦表 / ◫视图 / ▤库 |
| 4 | 数据预览·类型 | 打开 users | ✅ INT、VARCHAR、`100.50` DECIMAL、ENUM、`2026-06-23 02:35:14` DATETIME 全部正确 |
| 5 | 数据预览·NULL | 打开 orders | ✅ note 列 `NULL` |
| 6 | 数据预览·CJK | users 第3行 | ✅ 王小明（HEX + CSV 双重确认） |
| 7 | 结果·行列滚动 | j/k、h/l | ✅ 状态行 `cols 1-7 of 7` → `cols 4-7 of 7` |
| 8 | 结果·分页 | 打开 big(150行)，Space 翻页 | ✅ `rows 1-100 +more` → `rows 101-150` |
| 9 | 视图查询 | 打开 active_users | ✅ 2 行 2 列 |
| 10 | 查询编辑器·输入 | 直接键入 SQL | ✅ 默认 INSERT 模式，模式指示 INSERT/NORMAL，Esc 切换 |
| 11 | 补全·关键字/表名 | `SELECT name,age FROM us` | ✅ 弹出 users / active_users + 关键字，Tab 接受 |
| 12 | 补全·列名 | `… FROM users WHERE ema` | ✅ 弹出 `email`（后台抓取字段缓存） |
| 13 | 执行查询 | 点击 Run 执行 `SELECT … WHERE age>26` | ✅ 结果 alice/王小明（WHERE 生效） |
| 14 | 错误处理 | 查询不存在的表 | ✅ 红框 `1146 … doesn't exist` + `✗ query failed` |
| 15 | 结果内搜索 | `/second` | ✅ 搜索激活并跳转 |
| 16 | 查询历史 | 执行后 Ctrl+R | ✅ 历史弹窗（持久化、带 ✓） |
| 17 | 导出 | `:export csv <path>` | ✅ 文件正确（CJK/小数/datetime 全对） |
| 18 | 剪贴板 | `:copy csv` | ✅ `xclip -o` 读回内容一致 |
| 19 | 命令栏 | `:` 输入 | ✅ 输入可见、命令执行 |
| 20 | 连接管理·测试 | 连接页按 `t` | ✅ `✓ local-mysql: ok`（真连成功） |
| 21 | 连接管理·编辑 | 连接页按 `e` | ✅ 表单预填、密码脱敏 `••••••` |
| 22 | 只读拦截 | 只读连接执行 UPDATE | ✅ `connection is read-only` 拦截 |
| 23 | 元数据视图 | 数据页按 `m` | ✅ 列(名/类型/null/默认/注释) + 索引 + 属性(PRIMARY KEY) |
| 24 | AI | Ctrl+K → 提问 | ✅ 识别 claude、真实往返返回 ```sql…```、Ctrl+Y 插入编辑器 |
| 25 | UX | 整体 | ✅ 左右面板边框对齐、面板底色、状态色 ✓/✗、命令栏可见 |

## 3. 校验中发现并修复的问题

### 🔴 严重 BUG（必修）：MySQL 表预览全部失败
- **现象**：打开任意 MySQL 表预览报 `1295 (HY000): This command is not supported in the prepared statement protocol yet`。
- **根因**：作用域设置 `use_scope` 用 sqlx 的 **prepared 协议**执行 `USE <db>`（Doris 为 `SWITCH`），而 MySQL 的 `USE`/`SWITCH` 不允许 prepare。
- **修复**：改为 **simple/text 协议**（`conn.execute(stmt.as_str())`）执行 `USE`/`SWITCH`。
- 文件：`src/db/sql/mod.rs::use_scope`。

### 🟡 体验缺口（已补齐）
1. **列名补全缺失** → 现在编辑器解析 FROM/JOIN 引用的表，后台懒加载其列名并缓存，输入时弹出列名（`src/app/actions.rs` 的 `editor_update_completion`/`load_fields`/`referenced_tables`，`Session.field_cache`）。
2. **DECIMAL `0.00` 显示成 `0`** → BigDecimal 会把零规范化；改用 **rust_decimal** 解码（保留列精度），现在显示 `0.00`（`Cargo.toml` 加 `rust_decimal` feature，`decode_cell` 优先 `sqlx::types::Decimal`）。
3. **元数据视图未接线** → 数据页按 `m` 切换“数据/元数据”视图，展示列定义、索引、属性（`toggle_metadata` + `render_metadata`，新增 `AsyncMsg::Metadata`）。

修复后：`cargo test` 88 全绿、`cargo clippy` 0 警告、release 可构建。

## 4. 未能测试 / 环境限制（非程序缺陷）

- **Ctrl+Enter 执行**：终端无法区分 Ctrl+Enter 与 Enter（都发 `\r`），改用**鼠标点击 Run 按钮**验证了执行路径；真实终端中 Ctrl+Enter 为标准键应可用。
- **拖拽框选复制**：未专测鼠标拖拽选区（改用 `:copy` 验证了剪贴板写入）；鼠标**点击**（Run / 连接节点 / 标签）已验证。
- **多查询 tab / Ctrl+W 关闭**：未专测（自动开的单 tab 已验证）。
- **语法高亮配色**：单色抓帧只能验证 token 结构，验证不了具体颜色。
- 无法驱动用户自己的 `tdb` 进程（输入由其终端持有），全程用独立实例。

## 5. 复现方式

```bash
# 1) 起 MySQL（用户已起）并灌数据
docker exec -i mysql-server mysql -uroot -p123456 --default-character-set=utf8mb4 < seed.sql

# 2) 造 vault（含 mysql profile）
cargo run --example seed_vault -- /tmp/tdb-mysql.enc <pw> mysql 127.0.0.1 3306 root 123456 testdb
#    只读：末尾加 ro

# 3) 启动
cargo build --release
./target/release/tdb --config /tmp/tdb-mysql.enc
```

驱动脚本（PTY + VT 网格还原）见仓库外的临时脚本；`examples/seed_vault.rs` 已纳入仓库，支持 `redis` 与 `mysql`/`doris`。
