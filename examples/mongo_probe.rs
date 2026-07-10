//! Headless driver-layer verification against a real MongoDB.
//!
//! Exercises every Mongo-specific code path without the TUI: the document result
//! model, BSON decoding (ObjectId/Decimal128/Int64/DateTime/array/nested/null/
//! CJK), the mongosh command parser (find/findOne/countDocuments/aggregate +
//! CRUD), Extended-JSON normalization, index/field metadata, pagination, and
//! read-only interception.
//!
//! Usage:
//!   cargo run --example mongo_probe -- <host> <port> <database>

use tdb::config::{Driver, Profile};
use tdb::db::{self, Command, Page, Query, Scope, Target};
use tdb::result::Set;

fn profile(a: &[String], read_only: bool) -> Profile {
    Profile {
        id: "probe".into(),
        name: "probe".into(),
        driver: Driver::Mongo,
        host: a[1].clone(),
        port: a[2].parse().unwrap(),
        user: String::new(),
        password: String::new(),
        database: a.get(3).cloned().unwrap_or_default(),
        auth_db: String::new(),
        redis_db: 0,
        uri_params: String::new(),
        read_only,
    }
}

fn print_set(set: &Set, max: usize) {
    if let Some(t) = &set.table {
        let header: Vec<String> = t.columns.iter().map(|c| c.name.clone()).collect();
        println!("  table cols: {}", header.join(" | "));
        for i in 0..t.rows.len().min(max) {
            let cells: Vec<String> = (0..t.columns.len()).map(|c| t.cell_string(i, c)).collect();
            println!("  r{i}: {}", cells.join(" | "));
        }
        return;
    }
    println!("  documents={} has_more={} truncated={}", set.documents.len(), set.has_more, set.truncated);
    for (i, d) in set.documents.iter().enumerate().take(max) {
        let json = serde_json::to_string(&d.data).unwrap_or_default();
        println!("  [{i}] _id={} {}", d.id, json);
    }
}

fn coll_target(db_name: &str, name: &str) -> Target {
    Target {
        database: db_name.into(),
        name: name.into(),
        type_: tdb::db::ObjectType::Collection,
        ..Default::default()
    }
}

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    let a: Vec<String> = std::env::args().collect();
    let db_name = a.get(3).cloned().unwrap_or_default();
    let adapter = db::connect(&profile(&a, false)).await?;

    println!("== test() (ping) ==");
    adapter.test().await?;
    println!("  OK connected");

    println!("\n== list_databases() ==");
    println!("  {:?}", adapter.list_databases().await?);

    println!("\n== list_objects({db_name}) (collections) ==");
    for o in adapter
        .list_objects(Scope { database: db_name.clone(), ..Default::default() })
        .await?
    {
        println!("  {} [{:?}]", o.name, o.type_);
    }

    println!("\n== preview {db_name}/users (BSON types) ==");
    let set = adapter
        .preview(coll_target(&db_name, "users"), Query::default(), Page::new(100, 0))
        .await?;
    print_set(&set, 10);

    println!("\n== preview {db_name}/big page1 (limit 100, expect has_more) ==");
    let set = adapter
        .preview(coll_target(&db_name, "big"), Query::default(), Page::new(100, 0))
        .await?;
    print_set(&set, 1);
    println!("\n== preview {db_name}/big page2 (skip 100) ==");
    let set = adapter
        .preview(coll_target(&db_name, "big"), Query::default(), Page::new(100, 100))
        .await?;
    print_set(&set, 1);

    println!("\n== metadata {db_name}/users (indexes + sampled fields) ==");
    let md = adapter.metadata(coll_target(&db_name, "users")).await?;
    for idx in &md.indexes {
        println!("  index {} cols={:?} unique={}", idx.name, idx.columns, idx.unique);
    }
    println!("  fields: {:?}", md.fields.iter().map(|f| f.name.clone()).collect::<Vec<_>>());
    println!("  attributes: {:?}", md.attributes);

    let run = |text: &str| {
        let adapter = &adapter;
        let db_name = db_name.clone();
        let text = text.to_string();
        async move {
            adapter.execute(Command { text, catalog: String::new(), database: db_name }).await
        }
    };

    println!("\n== execute: db.users.find({{active: true}}) ==");
    match run("db.users.find({active: true})").await {
        Ok(s) => print_set(&s, 10),
        Err(e) => println!("  ERR: {e}"),
    }

    println!("\n== execute: db.users.findOne({{name: \"王小明\"}}) ==");
    match run("db.users.findOne({name: \"王小明\"})").await {
        Ok(s) => print_set(&s, 10),
        Err(e) => println!("  ERR: {e}"),
    }

    println!("\n== execute: db.orders.countDocuments({{status: \"paid\"}}) ==");
    match run("db.orders.countDocuments({status: \"paid\"})").await {
        Ok(s) => print_set(&s, 10),
        Err(e) => println!("  ERR: {e}"),
    }

    println!("\n== execute: db.orders.aggregate([group by city sum amount]) ==");
    match run("db.orders.aggregate([{$group: {_id: \"$city\", total: {$sum: \"$amount\"}, n: {$sum: 1}}}, {$sort: {total: -1}}])").await {
        Ok(s) => print_set(&s, 10),
        Err(e) => println!("  ERR: {e}"),
    }

    println!("\n== execute: ObjectId filter (Extended JSON) ==");
    match run("db.users.find({_id: ObjectId(\"64b7f0000000000000000001\")})").await {
        Ok(s) => {
            print_set(&s, 10);
            // The filter must actually apply: exactly the one matching doc, not
            // the whole collection (which is what a silently-dropped filter does).
            assert_eq!(s.documents.len(), 1, "ObjectId filter should match exactly 1 doc, got {}", s.documents.len());
            assert_eq!(s.documents[0].id, "64b7f0000000000000000001", "wrong _id returned");
            println!("  PASS: exactly 1 doc, _id matches");
        }
        Err(e) => println!("  ERR: {e}"),
    }

    println!("\n== execute: invalid ObjectId must error, not return all ==");
    match run("db.users.find({_id: ObjectId(\"not-a-valid-oid\")})").await {
        Ok(s) => panic!("invalid ObjectId should error, but returned {} docs", s.documents.len()),
        Err(e) => println!("  PASS: errored as expected: {e}"),
    }

    println!("\n== mutation round-trip on probe_tmp (insert/update/delete) ==");
    match run("db.probe_tmp.insertOne({k: \"v\", n: NumberInt(1)})").await {
        Ok(s) => print_set(&s, 1),
        Err(e) => println!("  insert ERR: {e}"),
    }
    match run("db.probe_tmp.updateOne({k: \"v\"}, {$set: {n: NumberInt(2)}})").await {
        Ok(s) => print_set(&s, 1),
        Err(e) => println!("  update ERR: {e}"),
    }
    match run("db.probe_tmp.deleteMany({})").await {
        Ok(s) => print_set(&s, 1),
        Err(e) => println!("  delete ERR: {e}"),
    }

    println!("\n== JSON request form: {{collection, filter}} ==");
    match run("{\"collection\": \"users\", \"filter\": {\"age\": {\"$gt\": 29}}}").await {
        Ok(s) => print_set(&s, 10),
        Err(e) => println!("  ERR: {e}"),
    }

    println!("\n== error: query a missing collection ==");
    let set = adapter
        .preview(coll_target(&db_name, "no_such_coll"), Query::default(), Page::new(10, 0))
        .await;
    match set {
        Ok(s) => println!("  (empty ok) documents={}", s.documents.len()),
        Err(e) => println!("  ERR: {e}"),
    }

    println!("\n== read-only interception ==");
    let ro = db::connect(&profile(&a, true)).await?;
    match ro
        .execute(Command {
            text: "db.users.deleteMany({})".into(),
            catalog: String::new(),
            database: db_name.clone(),
        })
        .await
    {
        Ok(_) => println!("  UNEXPECTED: mutation allowed on read-only!"),
        Err(e) => println!("  blocked: {e}"),
    }

    println!("\nALL PROBES DONE");
    Ok(())
}
