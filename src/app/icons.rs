//! Navigation / object icons. Per project preference: use official brand
//! glyphs (Nerd Font devicon/seti) when available, otherwise a text/Unicode
//! fallback — never emoji. Style is auto-detected and overridable via
//! `TDB_ICON_STYLE=nerd|unicode`.

use crate::config::Driver;
use crate::db::ObjectType;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum IconStyle {
    Nerd,
    Unicode,
}

pub struct Icons {
    pub style: IconStyle,
}

impl Icons {
    pub fn detect() -> Icons {
        let style = match std::env::var("TDB_ICON_STYLE").as_deref() {
            Ok("nerd") => IconStyle::Nerd,
            Ok("unicode") => IconStyle::Unicode,
            _ => {
                // Heuristic: terminals that commonly ship a Nerd Font. Default to
                // Unicode so glyphs never render as tofu when the font is absent.
                let prog = std::env::var("TERM_PROGRAM").unwrap_or_default();
                if matches!(prog.as_str(), "WezTerm" | "kitty") {
                    IconStyle::Nerd
                } else {
                    IconStyle::Unicode
                }
            }
        };
        Icons { style }
    }

    fn nerd(&self) -> bool {
        self.style == IconStyle::Nerd
    }

    pub fn expanded(&self) -> &'static str {
        if self.nerd() { "\u{f078} " } else { "▾ " }
    }
    pub fn collapsed(&self) -> &'static str {
        if self.nerd() { "\u{f054} " } else { "▸ " }
    }
    pub fn database(&self) -> &'static str {
        if self.nerd() { "\u{f1c0} " } else { "▤ " }
    }
    pub fn catalog(&self) -> &'static str {
        if self.nerd() { "\u{f0c4} " } else { "▣ " }
    }
    pub fn connection(&self) -> &'static str {
        if self.nerd() { "\u{f1e6} " } else { "◈ " }
    }
    pub fn lock(&self) -> &'static str {
        if self.nerd() { " \u{f023}" } else { " ⊘" }
    }

    pub fn object(&self, t: ObjectType) -> &'static str {
        match (self.nerd(), t) {
            (true, ObjectType::Table) => "\u{f0ce} ",
            (true, ObjectType::View) => "\u{f06e} ",
            (true, ObjectType::Collection) => "\u{f1c0} ",
            (true, ObjectType::Key) => "\u{f084} ",
            (false, ObjectType::Table) => "▦ ",
            (false, ObjectType::View) => "◫ ",
            (false, ObjectType::Collection) => "◆ ",
            (false, ObjectType::Key) => "• ",
        }
    }

    /// Brand glyph for a driver (Nerd devicon), else a short text tag.
    pub fn driver(&self, d: Driver) -> &'static str {
        match (self.nerd(), d) {
            (true, Driver::Mysql) => "\u{e704}",
            (true, Driver::Doris) => "\u{e7c4}",
            (true, Driver::Mongo) => "\u{e7a4}",
            (true, Driver::Redis) => "\u{e76d}",
            (false, _) => "",
        }
    }
}
