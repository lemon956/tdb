//! Test helper: write an encrypted vault with one connection profile, used by
//! the manual integration checks.
//!
//! Usage:
//!   cargo run --example seed_vault -- <path> <pw> redis <port>
//!   cargo run --example seed_vault -- <path> <pw> mysql <host> <port> <user> <password> <database> [ro]

use tdb::config::{Driver, Profile, Store, Vault};

fn main() {
    let a: Vec<String> = std::env::args().collect();
    let (path, pw, driver) = (&a[1], &a[2], a[3].as_str());
    let mut vault = Vault::default();
    let profile = match driver {
        "redis" => Profile {
            id: "local-redis".into(),
            name: "local-redis".into(),
            driver: Driver::Redis,
            host: "127.0.0.1".into(),
            port: a[4].parse().unwrap(),
            user: String::new(),
            password: String::new(),
            database: String::new(),
            auth_db: String::new(),
            redis_db: 0,
            uri_params: String::new(),
            read_only: false,
        },
        "mysql" | "doris" => Profile {
            id: format!("local-{driver}"),
            name: format!("local-{driver}"),
            driver: if driver == "doris" { Driver::Doris } else { Driver::Mysql },
            host: a[4].clone(),
            port: a[5].parse().unwrap(),
            user: a[6].clone(),
            password: a[7].clone(),
            database: a.get(8).cloned().unwrap_or_default(),
            auth_db: String::new(),
            redis_db: 0,
            uri_params: String::new(),
            read_only: a.get(9).map(|s| s == "ro").unwrap_or(false),
        },
        other => panic!("unknown driver {other}"),
    };
    vault.upsert_profile(profile);
    Store::new(path).save(pw, &vault).unwrap();
    println!("seeded {path}");
}
