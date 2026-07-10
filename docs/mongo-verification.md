# TDB（Rust）MongoDB 功能与体验校验报告

日期：2026-06-23 · 分支：`rust-rewrite` · 被测：`target/release/tdb` · Mongo：`mongo`（docker，27017，无认证）

## 1. 校验方法

沿用 Doris 校验的两条互补可控通道（不向用户正在跑的进程注入按键）：

1. **驱动层 headless 探针**（`examples/mongo_probe.rs`）：库 API `tdb::db::connect()` 直连真实 Mongo，逐项断言 `test / list_databases / list_objects / preview / metadata / execute`（mongosh：find/findOne/countDocuments/aggregate/CRUD、JSON 请求形式、ObjectId 过滤）、BSON 解码、分页、缺失集合、只读拦截。
   ```bash
   cargo run --example mongo_probe -- 127.0.0.1 27017 testdb
   ```
2. **TUI 层 tmux 驱动**：`tmux send-keys` 注入按键（`send-keys -H` 发 SGR 鼠标点击 Run 按钮），`capture-pane -p` 抓真实渲染，核对导航/文档预览/查询执行/元数据/错误/导出/只读/AI。

测试库 `testdb` 覆盖 Mongo 特性：

| 集合 | 内容 | 覆盖点 |
|---|---|---|
| `users`（3 文档） | ObjectId、CJK、NumberInt、**NumberDecimal/Decimal128**、**NumberLong/Int64**(2^53+1 超 double)、Double、Boolean、**null**、**数组**、**嵌套文档**、ISODate | 全 BSON 类型解码、`balance 0.00` 边界 |
| `users` 索引 | `uniq_email`(unique)、`city_age`(复合 `address.city`,`age`) | 索引元数据、唯一标记、嵌套点路径 |
| `orders`（4 文档） | Decimal128 金额 | 聚合(`$group`/`$sum`) |
| `big`（150 文档） | seq/val | 分页 |
| 只读 vault | `read_only=true` | 写操作拦截 |

## 2. 功能校验结果（全部通过）

| # | 维度 | 验证动作 | 结果 |
|---|---|---|---|
| 1 | 解锁 + 连接 | 主密码解锁、`t` 测连接(ping) | ✅ `✓ local-mongo: ok` |
| 2 | 导航（无 catalog 层） | 展开连接根 | ✅ 库直接挂连接下：admin/config/local/testdb（▤ 库图标） |
| 3 | 导航·集合 | 展开 testdb | ✅ 列出集合（◆ 集合图标） |
| 4 | **文档预览·BSON 类型** | 打开 users | ✅ ObjectId→hex、Decimal128 `100.50`/`0.00`、Int64 `9007199254740993`(超 double 精度)、Double、Boolean、null、数组、嵌套文档、ISODate→RFC3339 |
| 5 | 文档预览·CJK | users | ✅ 王小明/李雷/北京、`bio: "CJK 用户"` |
| 6 | 文档逐条浏览 | j/k 翻文档 | ✅ `document 1-1 of 3` → `document 2-2 of 3` |
| 7 | 分页 | big(150)，skip 翻页 | ✅ `100 +more` → `50`，第二页 seq 从 101 起 |
| 8 | **元数据·索引** | users 按 `m` | ✅ `_id_` / `UNIQUE uniq_email` / `city_age (address.city, age)` |
| 9 | 元数据·采样字段 | users 按 `m` | ✅ 字段含嵌套点路径 `address.city`/`address.zip` |
| 10 | **mongosh·find** | `db.users.find({active: true})` | ✅ 返回 2 文档 |
| 11 | mongosh·findOne | `db.users.findOne({name:"王小明"})` | ✅ CJK 过滤命中 |
| 12 | mongosh·countDocuments | `db.orders.countDocuments({status:"paid"})` | ✅ 表结果 `count=3` |
| 13 | **mongosh·aggregate** | `$group` by city `$sum` amount | ✅ 分组结果（广州 500/北京 350.50/上海 80，Decimal 精度保留） |
| 14 | Extended JSON | `db.users.find({_id: ObjectId("…")})` | ✅ ObjectId 规整后命中 |
| 15 | CRUD 往返 | insertOne/updateOne/deleteMany | ✅ `affected_rows` 正确 |
| 16 | JSON 请求形式 | `{"collection":"users","filter":{"age":{"$gt":29}}}` | ✅ 返回 2 文档 |
| 17 | 错误处理 | 非法 pipeline stage | ✅ 红框 `Command failed: … Unrecognized pipeline stage name` + `✗ query failed` |
| 18 | 缺失集合 | 查不存在集合 | ✅ 返回空（Mongo 语义，非报错） |
| 19 | 导出 | `:export json <path>` | ✅ 文档数组 JSON（ObjectId/Decimal/嵌套/CJK/日期全对） |
| 20 | 只读拦截 | 只读连接执行 deleteMany | ✅ `connection is read-only` + `✗ query failed` |
| 21 | **AI（driver-aware）** | Ctrl+K 提问 | ✅ claude 真往返，返回 **mongosh 语法** `db.users.find({ age: { $gt: 30 } })`（非 SQL） |

## 3. 校验中发现并修复的问题

> 与 Doris 不同，MongoDB 的 Rust 驱动（`mongodb` 3.x）**开箱即用**，驱动层探针零 BUG。仅发现一个错误展示的 UX 瑕疵 + 一个工具缺口。

### 🟡 BUG：错误框尾部混入原始 BSON 十六进制 dump
- **现象**：执行报错时，错误框在可读信息后追加一大段 `Some(RawDocumentBuf { data: "7100000001…" })` 十六进制。
- **根因**：`mongodb::error::Error` 的 Display（thiserror `#[error("Kind: {kind}, labels: {labels:?}, source: {source:?}, server response: {server_response:?}")]`）把 `server_response` 原始 BSON 也打进了字符串，而 UI 用 `e.to_string()` 展示。
- **修复**：`src/db/mongo.rs` 加 `mongo_err()` 助手只取 `e.kind` 的 Display，在所有 mongodb 调用叶子处 `.map_err(mongo_err)`。修复后错误框为干净的 `Command failed: Error code 40324 (Location40324): Unrecognized pipeline stage name: '$badstage'`。

### 🟢 工具缺口：`seed_vault` 不支持 mongo
- `examples/seed_vault.rs` 原本只认 `redis`/`mysql`/`doris`，新增 `mongo` 分支以便造 mongo vault（含只读）。

修复后：`cargo test` 88 全绿、`cargo clippy`（改动文件）0 警告、release 可构建、headless 探针与 TUI 全项复测通过。

## 4. 体验观察（非缺陷，记录备查）

- **编辑器模式行仍写 `── SQL · …`**：Mongo 连接用的是 mongosh，标签 "SQL" 略不贴切（作用域指示器显示库名 `testdb` 正常）；AI 代码块也固定标 ```` ```sql ````，但内容是正确的 mongosh。属小标签问题，不影响功能。
- **元数据 COLUMNS 的 type/null 列为空**：Mongo 无 schema，适配器只采样字段名，复用 SQL 元数据布局时这几列空白/显示 `NO`。字段名与索引信息完整，可接受。

## 5. 未能测试 / 环境限制（非程序缺陷）

- **Ctrl+Enter 执行**：终端无法区分 Ctrl+Enter 与 Enter，改用注入 SGR 鼠标点击 Run 按钮验证执行路径。
- **认证连接**：本地 mongo 无认证；URI/authSource 构造逻辑由单元测试 `builds_uri_with_auth_source` 覆盖。

## 6. 复现方式

```bash
# 1) 灌数据（各 BSON 类型 + 索引 + 聚合集合 + 大集合）
mongosh "mongodb://127.0.0.1:27017" --file mongo_seed.js

# 2) 造 vault（mongo profile；只读末尾加 ro）
cargo run --example seed_vault -- /tmp/tdb-mongo.enc <pw> mongo 127.0.0.1 27017 '' '' testdb

# 3a) 驱动层 headless 校验
cargo run --example mongo_probe -- 127.0.0.1 27017 testdb

# 3b) TUI 校验
cargo build --release && ./target/release/tdb --config /tmp/tdb-mongo.enc
```

`examples/mongo_probe.rs`、`examples/seed_vault.rs`(已支持 mongo) 已纳入仓库；TUI 驱动用 tmux（`send-keys -H` 注入 SGR 鼠标）见仓库外临时脚本。
