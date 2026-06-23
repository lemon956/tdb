//! DSN / connection-option helpers. Port of `internal/db/sqlutil`.

use crate::config::Profile;

/// Build a sqlx-style MySQL URL: `mysql://user:pass@host:port/db?params`.
/// (The Go binary used the `user:pass@tcp(...)/db` form for go-sql-driver; sqlx
/// wants a URL, so we emit that instead while preserving the same defaults.)
pub fn build_mysql_url(profile: &Profile) -> String {
    build_url(profile, 3306)
}

pub fn build_doris_url(profile: &Profile) -> String {
    build_url(profile, 9030)
}

fn build_url(profile: &Profile, default_port: i32) -> String {
    let host = if profile.host.is_empty() {
        "127.0.0.1"
    } else {
        &profile.host
    };
    let port = if profile.port == 0 {
        default_port
    } else {
        profile.port
    };
    let database = profile.database.trim_start_matches('/');
    let user = urlencode(&profile.user);
    let pass = urlencode(&profile.password);
    format!("mysql://{user}:{pass}@{host}:{port}/{database}")
}

/// Backtick-quote an identifier, doubling embedded backticks.
pub fn quote_identifier(identifier: &str) -> String {
    format!("`{}`", identifier.replace('`', "``"))
}

/// `" LIMIT n OFFSET m"` with the same clamping as [`crate::db::Page`].
pub fn limit_offset_clause(limit: i32, offset: i32) -> String {
    let mut page_limit = limit;
    if page_limit <= 0 {
        page_limit = 100;
    }
    if page_limit > 500 {
        page_limit = 500;
    }
    let offset = offset.max(0);
    format!(" LIMIT {page_limit} OFFSET {offset}")
}

fn urlencode(s: &str) -> String {
    let mut out = String::with_capacity(s.len());
    for b in s.bytes() {
        match b {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' => {
                out.push(b as char)
            }
            _ => out.push_str(&format!("%{b:02X}")),
        }
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::config::Driver;

    fn p() -> Profile {
        Profile {
            id: "x".into(),
            name: "x".into(),
            driver: Driver::Mysql,
            host: "db.example".into(),
            port: 0,
            user: "root".into(),
            password: "p@ss".into(),
            database: "app".into(),
            auth_db: String::new(),
            redis_db: 0,
            uri_params: String::new(),
            read_only: false,
        }
    }

    #[test]
    fn mysql_url_uses_default_port_and_encodes_password() {
        let url = build_mysql_url(&p());
        assert_eq!(url, "mysql://root:p%40ss@db.example:3306/app");
    }

    #[test]
    fn doris_url_uses_9030() {
        assert!(build_doris_url(&p()).contains(":9030/"));
    }

    #[test]
    fn quote_identifier_doubles_backticks() {
        assert_eq!(quote_identifier("a`b"), "`a``b`");
    }

    #[test]
    fn limit_offset_clamps() {
        assert_eq!(limit_offset_clause(0, -3), " LIMIT 100 OFFSET 0");
        assert_eq!(limit_offset_clause(9000, 5), " LIMIT 500 OFFSET 5");
    }
}
