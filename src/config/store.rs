//! AES-256-GCM encrypted vault storage. Byte-for-byte compatible with the Go
//! implementation in `internal/config/store.go` so an existing `tdb.enc`
//! written by the Go binary unlocks here with the same master password.
//!
//! On-disk format (pretty JSON): `{version, salt, nonce, ciphertext}` where the
//! three byte fields are Go-style standard base64 strings (that is how Go's
//! `encoding/json` marshals `[]byte`).

use std::path::{Path, PathBuf};

use aes_gcm::{
    aead::{Aead, KeyInit, Payload},
    Aes256Gcm, Key, Nonce,
};
use anyhow::{anyhow, Context, Result};
use base64::{engine::general_purpose::STANDARD as B64, Engine};
use rand::RngCore;
use serde::{Deserialize, Deserializer, Serialize, Serializer};
use sha2::{Digest, Sha256};

use super::Vault;

const ENCRYPTED_FILE_VERSION: i32 = 1;
const SALT_SIZE: usize = 16;
const NONCE_SIZE: usize = 12;
const KEY_ITERATIONS: u64 = 120_000;

pub struct Store {
    path: PathBuf,
}

#[derive(Serialize, Deserialize)]
struct EncryptedFile {
    version: i32,
    #[serde(serialize_with = "ser_b64", deserialize_with = "de_b64")]
    salt: Vec<u8>,
    #[serde(serialize_with = "ser_b64", deserialize_with = "de_b64")]
    nonce: Vec<u8>,
    #[serde(serialize_with = "ser_b64", deserialize_with = "de_b64")]
    ciphertext: Vec<u8>,
}

fn ser_b64<S: Serializer>(bytes: &[u8], s: S) -> std::result::Result<S::Ok, S::Error> {
    s.serialize_str(&B64.encode(bytes))
}

fn de_b64<'de, D: Deserializer<'de>>(d: D) -> std::result::Result<Vec<u8>, D::Error> {
    let s = String::deserialize(d)?;
    B64.decode(s.as_bytes()).map_err(serde::de::Error::custom)
}

impl Store {
    pub fn new(path: impl Into<PathBuf>) -> Store {
        Store { path: path.into() }
    }

    pub fn path(&self) -> &Path {
        &self.path
    }

    /// Reports whether an encrypted config file already exists (first-run vs
    /// unlock).
    pub fn exists(&self) -> bool {
        self.path.exists()
    }

    pub fn save(&self, master_password: &str, vault: &Vault) -> Result<()> {
        if master_password.is_empty() {
            return Err(anyhow!("master password is required"));
        }
        let plaintext = serde_json::to_vec(vault).context("marshal vault")?;

        let mut salt = vec![0u8; SALT_SIZE];
        let mut nonce = vec![0u8; NONCE_SIZE];
        rand::thread_rng().fill_bytes(&mut salt);
        rand::thread_rng().fill_bytes(&mut nonce);

        let key = derive_key(master_password, &salt);
        let cipher = Aes256Gcm::new(Key::<Aes256Gcm>::from_slice(&key));
        let ciphertext = cipher
            .encrypt(
                Nonce::from_slice(&nonce),
                Payload {
                    msg: &plaintext,
                    aad: &[],
                },
            )
            .map_err(|_| anyhow!("encrypt config"))?;

        let payload = EncryptedFile {
            version: ENCRYPTED_FILE_VERSION,
            salt,
            nonce,
            ciphertext,
        };
        let raw = serde_json::to_vec_pretty(&payload).context("marshal encrypted file")?;

        if let Some(dir) = self.path.parent() {
            std::fs::create_dir_all(dir).context("create config directory")?;
        }
        write_0600(&self.path, &raw).context("write config file")?;
        Ok(())
    }

    pub fn load(&self, master_password: &str) -> Result<Vault> {
        if master_password.is_empty() {
            return Err(anyhow!("master password is required"));
        }
        let raw = match std::fs::read(&self.path) {
            Ok(r) => r,
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(Vault::default()),
            Err(e) => return Err(anyhow::Error::from(e).context("read config file")),
        };

        let payload: EncryptedFile =
            serde_json::from_slice(&raw).context("parse config file")?;
        if payload.version != ENCRYPTED_FILE_VERSION {
            return Err(anyhow!("unsupported config version {}", payload.version));
        }
        if payload.salt.len() != SALT_SIZE || payload.nonce.len() != NONCE_SIZE {
            return Err(anyhow!("invalid encrypted config header"));
        }

        let key = derive_key(master_password, &payload.salt);
        let cipher = Aes256Gcm::new(Key::<Aes256Gcm>::from_slice(&key));
        let plaintext = cipher
            .decrypt(
                Nonce::from_slice(&payload.nonce),
                Payload {
                    msg: &payload.ciphertext,
                    aad: &[],
                },
            )
            .map_err(|_| anyhow!("decrypt config: invalid master password or corrupted file"))?;

        let vault: Vault = serde_json::from_slice(&plaintext).context("parse vault")?;
        Ok(vault)
    }
}

/// Replicates the Go key-stretching exactly:
/// `seed = SHA256(salt || pwd)`, then 120k rounds of
/// `key = SHA256(key || salt || pwd || big_endian_u64(i))`.
fn derive_key(master_password: &str, salt: &[u8]) -> [u8; 32] {
    let pwd = master_password.as_bytes();

    let mut seed = Sha256::new();
    seed.update(salt);
    seed.update(pwd);
    let mut key: [u8; 32] = seed.finalize().into();

    for i in 0u64..KEY_ITERATIONS {
        let mut h = Sha256::new();
        h.update(key);
        h.update(salt);
        h.update(pwd);
        h.update(i.to_be_bytes());
        key = h.finalize().into();
    }
    key
}

#[cfg(unix)]
fn write_0600(path: &Path, data: &[u8]) -> std::io::Result<()> {
    use std::os::unix::fs::OpenOptionsExt;
    use std::io::Write;
    let mut f = std::fs::OpenOptions::new()
        .write(true)
        .create(true)
        .truncate(true)
        .mode(0o600)
        .open(path)?;
    f.write_all(data)
}

#[cfg(not(unix))]
fn write_0600(path: &Path, data: &[u8]) -> std::io::Result<()> {
    std::fs::write(path, data)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::config::{Driver, Profile};

    fn sample_vault() -> Vault {
        let mut v = Vault::default();
        v.upsert_profile(Profile {
            id: "local".into(),
            name: "local".into(),
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
        });
        v
    }

    #[test]
    fn save_then_load_round_trips() {
        let dir = tempfile::tempdir().unwrap();
        let store = Store::new(dir.path().join("tdb.enc"));
        store.save("hunter2", &sample_vault()).unwrap();
        let loaded = store.load("hunter2").unwrap();
        assert_eq!(loaded.profiles.len(), 1);
        assert_eq!(loaded.profiles[0].password, "secret");
    }

    #[test]
    fn wrong_password_fails() {
        let dir = tempfile::tempdir().unwrap();
        let store = Store::new(dir.path().join("tdb.enc"));
        store.save("right", &sample_vault()).unwrap();
        assert!(store.load("wrong").is_err());
    }

    #[test]
    fn missing_file_loads_empty() {
        let dir = tempfile::tempdir().unwrap();
        let store = Store::new(dir.path().join("absent.enc"));
        let v = store.load("whatever").unwrap();
        assert!(v.profiles.is_empty());
    }

    // Cross-compatibility guard: a vault produced by the Go binary
    // (version 1, base64 salt/nonce/ciphertext, 120k-round SHA256 key) must
    // decrypt here. This fixture was generated by the Go `config.Store.Save`.
    #[test]
    fn decrypts_go_generated_fixture() {
        // Generated with master password "tdbtest" by the Go implementation.
        // If this ever fails, the key derivation or AEAD framing diverged.
        const FIXTURE: &str = include_str!("testdata/go_vault.json");
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("tdb.enc");
        std::fs::write(&path, FIXTURE).unwrap();
        let store = Store::new(&path);
        let vault = store
            .load("tdbtest")
            .expect("Go-generated vault must decrypt");
        assert!(!vault.profiles.is_empty());
    }
}
