//! Headless driver-layer verification against a real Apache Doris.
//!
//! Exercises every Doris-specific code path of the SQL adapter without the TUI:
//! catalog tree (`SHOW CATALOGS` + per-catalog `SWITCH`), the three key models'
//! metadata parsing (DUPLICATE/UNIQUE/AGGREGATE + RANGE partition), type
//! decoding (LARGEINT/DECIMAL/ARRAY/MAP/JSON/NULL/CJK/BOOLEAN), pagination, and
//! external-catalog error surfacing.
//!
//! Usage:
//!   cargo run --example doris_probe -- <host> <port> <user> <password> <database>

use tdb::config::{Driver, Profile};
use tdb::db::{self, Page, Query, Scope, Target};
use tdb::result::Set;

fn profile(a: &[String]) -> Profile {
    Profile {
        id: "probe".into(),
        name: "probe".into(),
        driver: Driver::Doris,
        host: a[1].clone(),
        port: a[2].parse().unwrap(),
        user: a[3].clone(),
        password: a[4].clone(),
        database: a.get(5).cloned().unwrap_or_default(),
        auth_db: String::new(),
        redis_db: 0,
        uri_params: String::new(),
        read_only: false,
    }
}

fn print_set(set: &Set, max_rows: usize) {
    let Some(t) = &set.table else {
        println!("  (no table; value={:?})", set.value);
        return;
    };
    let header: Vec<String> = t
        .columns
        .iter()
        .map(|c| format!("{}({})", c.name, c.type_))
        .collect();
    println!("  cols: {}", header.join(" | "));
    for (i, _) in t.rows.iter().enumerate().take(max_rows) {
        let cells: Vec<String> = (0..t.columns.len())
            .map(|c| t.cell_string(i, c))
            .collect();
        println!("  r{}: {}", i, cells.join(" | "));
    }
    println!(
        "  rows={} has_more={} truncated={}",
        t.rows.len(),
        set.has_more,
        set.truncated
    );
}

fn target(catalog: &str, database: &str, name: &str) -> Target {
    Target {
        catalog: catalog.into(),
        database: database.into(),
        schema: String::new(),
        name: name.into(),
        type_: tdb::db::ObjectType::Table,
    }
}

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    let a: Vec<String> = std::env::args().collect();
    let p = profile(&a);
    let db_name = p.database.clone();
    let adapter = db::connect(&p).await?;

    println!("== test() ==");
    adapter.test().await?;
    println!("  OK connected");

    println!("\n== list_catalogs() (Doris SHOW CATALOGS) ==");
    let catalogs = adapter.list_catalogs().await?;
    println!("  {:?}", catalogs);

    println!("\n== list_databases() (internal catalog) ==");
    let dbs = adapter.list_databases().await?;
    println!("  {:?}", dbs);

    println!("\n== list_databases_in_catalog(\"jdbc_self\") (SWITCH scope) ==");
    match adapter.list_databases_in_catalog("jdbc_self").await {
        Ok(d) => println!("  {:?}", d),
        Err(e) => println!("  ERR: {e}"),
    }

    println!("\n== list_objects(internal/{db_name}) ==");
    let objs = adapter
        .list_objects(Scope {
            catalog: String::new(),
            database: db_name.clone(),
            schema: String::new(),
        })
        .await?;
    for o in &objs {
        println!("  {} [{:?}]", o.name, o.type_);
    }

    for tbl in ["events", "user_profile", "sales_agg", "complex_types"] {
        println!("\n== preview internal/{db_name}/{tbl} ==");
        let set = adapter
            .preview(target("", &db_name, tbl), Query::default(), Page::new(100, 0))
            .await?;
        print_set(&set, 10);
    }

    println!("\n== preview big (page limit 100, expect has_more) ==");
    let set = adapter
        .preview(target("", &db_name, "big"), Query::default(), Page::new(100, 0))
        .await?;
    print_set(&set, 2);
    println!("\n== preview big page 2 (offset 100) ==");
    let set = adapter
        .preview(target("", &db_name, "big"), Query::default(), Page::new(100, 100))
        .await?;
    print_set(&set, 2);

    println!("\n== preview VIEW active_users ==");
    let set = adapter
        .preview(target("", &db_name, "active_users"), Query::default(), Page::new(100, 0))
        .await?;
    print_set(&set, 10);

    for tbl in ["events", "user_profile", "sales_agg"] {
        println!("\n== metadata internal/{db_name}/{tbl} (key model + partition) ==");
        let md = adapter.metadata(target("", &db_name, tbl)).await?;
        for f in &md.fields {
            println!(
                "  field {} type={} null={} default={:?} comment={:?}",
                f.name, f.type_, f.nullable, f.default, f.comment
            );
        }
        for idx in &md.indexes {
            println!("  index {} cols={:?} unique={}", idx.name, idx.columns, idx.unique);
        }
        println!("  attributes: {:?}", md.attributes);
    }

    println!("\n== execute scoped SELECT with WHERE ==");
    let set = adapter
        .execute(tdb::db::Command {
            text: "SELECT user_id,name,age FROM user_profile WHERE age > 25 ORDER BY user_id".into(),
            catalog: String::new(),
            database: db_name.clone(),
        })
        .await?;
    print_set(&set, 10);

    println!("\n== external catalog: list_objects(jdbc_self/{db_name}) ==");
    match adapter
        .list_objects(Scope {
            catalog: "jdbc_self".into(),
            database: db_name.clone(),
            schema: String::new(),
        })
        .await
    {
        Ok(objs) => {
            for o in &objs {
                println!("  {} [{:?}]", o.name, o.type_);
            }
        }
        Err(e) => println!("  ERR: {e}"),
    }

    println!("\n== external catalog: preview(jdbc_self/{db_name}/user_profile) ==");
    match adapter
        .preview(target("jdbc_self", &db_name, "user_profile"), Query::default(), Page::new(10, 0))
        .await
    {
        Ok(set) => print_set(&set, 10),
        Err(e) => println!("  (expected external-driver error) ERR: {e}"),
    }

    println!("\n== error handling: query a missing table ==");
    match adapter
        .execute(tdb::db::Command {
            text: "SELECT * FROM no_such_table".into(),
            catalog: String::new(),
            database: db_name.clone(),
        })
        .await
    {
        Ok(_) => println!("  unexpected success"),
        Err(e) => println!("  ERR surfaced: {e}"),
    }

    println!("\nALL PROBES DONE");
    Ok(())
}
