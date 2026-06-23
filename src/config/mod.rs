//! Connection profiles and the encrypted on-disk vault. Port of
//! `internal/config`.

mod store;

pub use store::Store;

use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Driver {
    Mysql,
    Doris,
    Mongo,
    Redis,
}

impl Driver {
    pub fn as_str(self) -> &'static str {
        match self {
            Driver::Mysql => "mysql",
            Driver::Doris => "doris",
            Driver::Mongo => "mongo",
            Driver::Redis => "redis",
        }
    }

    pub fn parse(s: &str) -> Option<Driver> {
        match s.to_ascii_lowercase().as_str() {
            "mysql" => Some(Driver::Mysql),
            "doris" => Some(Driver::Doris),
            "mongo" => Some(Driver::Mongo),
            "redis" => Some(Driver::Redis),
            _ => None,
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct Profile {
    pub id: String,
    pub name: String,
    pub driver: Driver,
    #[serde(default)]
    pub host: String,
    #[serde(default)]
    pub port: i32,
    #[serde(default)]
    pub user: String,
    #[serde(default)]
    pub password: String,
    #[serde(default)]
    pub database: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub auth_db: String,
    #[serde(default, skip_serializing_if = "is_zero")]
    pub redis_db: i32,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub uri_params: String,
    #[serde(default)]
    pub read_only: bool,
}

fn is_zero(n: &i32) -> bool {
    *n == 0
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Vault {
    #[serde(default)]
    pub profiles: Vec<Profile>,
    /// Preferred local AI CLI ("claude" or "codex"); empty = auto-detect.
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub ai_provider: String,
}

impl Vault {
    pub fn upsert_profile(&mut self, profile: Profile) {
        if let Some(slot) = self.profiles.iter_mut().find(|p| p.id == profile.id) {
            *slot = profile;
        } else {
            self.profiles.push(profile);
        }
    }

    pub fn delete_profile(&mut self, id: &str) -> bool {
        if let Some(idx) = self.profiles.iter().position(|p| p.id == id) {
            self.profiles.remove(idx);
            true
        } else {
            false
        }
    }

    pub fn get_profile(&self, id: &str) -> Option<&Profile> {
        self.profiles.iter().find(|p| p.id == id)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn profile(id: &str) -> Profile {
        Profile {
            id: id.into(),
            name: id.into(),
            driver: Driver::Mysql,
            host: "127.0.0.1".into(),
            port: 3306,
            user: "root".into(),
            password: "secret".into(),
            database: "app".into(),
            auth_db: String::new(),
            redis_db: 0,
            uri_params: String::new(),
            read_only: false,
        }
    }

    #[test]
    fn upsert_replaces_or_appends() {
        let mut v = Vault::default();
        v.upsert_profile(profile("a"));
        v.upsert_profile(profile("b"));
        assert_eq!(v.profiles.len(), 2);
        let mut a2 = profile("a");
        a2.host = "10.0.0.1".into();
        v.upsert_profile(a2);
        assert_eq!(v.profiles.len(), 2);
        assert_eq!(v.get_profile("a").unwrap().host, "10.0.0.1");
    }

    #[test]
    fn delete_removes_by_id() {
        let mut v = Vault::default();
        v.upsert_profile(profile("a"));
        assert!(v.delete_profile("a"));
        assert!(!v.delete_profile("a"));
    }
}
