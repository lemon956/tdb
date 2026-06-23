//! Command-line option parsing. Port of `internal/cli`.
//!
//! Only one flag exists: `--config <path>` (also accepts `-config` for parity
//! with the Go binary). The default location follows the XDG spec.

use std::path::PathBuf;

#[derive(Debug, Clone)]
pub struct Options {
    pub config_path: PathBuf,
}

impl Options {
    pub fn parse(args: impl IntoIterator<Item = String>) -> Options {
        let mut config_path: Option<PathBuf> = None;
        let mut it = args.into_iter();
        while let Some(arg) = it.next() {
            match arg.as_str() {
                "--config" | "-config" => {
                    if let Some(v) = it.next() {
                        config_path = Some(PathBuf::from(v));
                    }
                }
                other => {
                    if let Some(v) = other.strip_prefix("--config=") {
                        config_path = Some(PathBuf::from(v));
                    } else if let Some(v) = other.strip_prefix("-config=") {
                        config_path = Some(PathBuf::from(v));
                    }
                }
            }
        }
        Options {
            config_path: config_path.unwrap_or_else(default_config_path),
        }
    }
}

/// `$XDG_CONFIG_HOME/tdb/tdb.enc` or `~/.config/tdb/tdb.enc`.
pub fn default_config_path() -> PathBuf {
    if let Ok(xdg) = std::env::var("XDG_CONFIG_HOME") {
        if !xdg.is_empty() {
            return PathBuf::from(xdg).join("tdb").join("tdb.enc");
        }
    }
    let home = std::env::var("HOME").unwrap_or_else(|_| ".".to_string());
    PathBuf::from(home).join(".config").join("tdb").join("tdb.enc")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_config_flag() {
        let o = Options::parse(["--config".to_string(), "/tmp/x.enc".to_string()]);
        assert_eq!(o.config_path, PathBuf::from("/tmp/x.enc"));
    }

    #[test]
    fn accepts_single_dash_and_equals() {
        let o = Options::parse(["-config=/tmp/y.enc".to_string()]);
        assert_eq!(o.config_path, PathBuf::from("/tmp/y.enc"));
    }

    #[test]
    fn falls_back_to_default() {
        std::env::remove_var("XDG_CONFIG_HOME");
        std::env::set_var("HOME", "/home/test");
        let o = Options::parse(Vec::<String>::new());
        assert_eq!(
            o.config_path,
            PathBuf::from("/home/test/.config/tdb/tdb.enc")
        );
    }
}
