//! Local AI CLI integration. Port of `internal/ai`. Shells out to `claude` or
//! `codex` (each handles its own auth); this layer just feeds a prompt and reads
//! the reply. Stateless — the caller maintains conversation history.

use std::path::PathBuf;

use anyhow::{anyhow, Result};

const KNOWN_PROVIDERS: &[&str] = &["claude", "codex"];

#[derive(Debug, Clone)]
pub struct Provider {
    pub name: String,
    pub bin: PathBuf,
}

impl Provider {
    /// Resolve a provider: the preferred one if present, else the first of the
    /// known providers found on PATH.
    pub fn detect(preferred: &str) -> Result<Provider> {
        if !preferred.is_empty() {
            if let Some(bin) = which(preferred) {
                return Ok(Provider {
                    name: preferred.to_string(),
                    bin,
                });
            }
        }
        for name in KNOWN_PROVIDERS {
            if let Some(bin) = which(name) {
                return Ok(Provider {
                    name: (*name).to_string(),
                    bin,
                });
            }
        }
        Err(anyhow!("no AI CLI found on PATH (looked for claude, codex)"))
    }

    pub fn available() -> Vec<String> {
        KNOWN_PROVIDERS
            .iter()
            .filter(|n| which(n).is_some())
            .map(|n| n.to_string())
            .collect()
    }

    fn args_for(&self, prompt: &str) -> Vec<String> {
        match self.name.as_str() {
            "codex" => vec!["exec".into(), prompt.into()],
            _ => vec!["-p".into(), prompt.into()],
        }
    }

    /// Send `prompt` to the CLI and return the trimmed reply.
    pub async fn ask(&self, prompt: &str) -> Result<String> {
        let output = tokio::process::Command::new(&self.bin)
            .args(self.args_for(prompt))
            .output()
            .await
            .map_err(|e| anyhow!("{}: {e}", self.bin.display()))?;
        if !output.status.success() {
            let stderr = String::from_utf8_lossy(&output.stderr);
            let first = stderr.lines().next().unwrap_or("").trim();
            return Err(anyhow!(
                "{}: exited {}: {first}",
                self.name,
                output.status
            ));
        }
        Ok(String::from_utf8_lossy(&output.stdout).trim().to_string())
    }
}

pub fn known_providers() -> Vec<String> {
    KNOWN_PROVIDERS.iter().map(|s| s.to_string()).collect()
}

/// Providers found on PATH, in lookup order.
pub fn available() -> Vec<String> {
    Provider::available()
}

fn which(name: &str) -> Option<PathBuf> {
    let path = std::env::var_os("PATH")?;
    for dir in std::env::split_paths(&path) {
        let candidate = dir.join(name);
        if candidate.is_file() {
            return Some(candidate);
        }
    }
    None
}

/// First SQL statement: a ```sql block, else the first bare ``` block, else the
/// whole reply.
pub fn extract_sql(reply: &str) -> String {
    let blocks = extract_blocks(reply, &["sql"]);
    blocks.into_iter().next().unwrap_or_default()
}

/// Every fenced block matching the first language in `langs`, else bare fences,
/// else the whole trimmed reply.
pub fn extract_blocks(reply: &str, langs: &[&str]) -> Vec<String> {
    for lang in langs {
        let blocks = all_fenced_blocks(reply, lang);
        if !blocks.is_empty() {
            return blocks;
        }
    }
    let bare = all_fenced_blocks(reply, "");
    if !bare.is_empty() {
        return bare;
    }
    let trimmed = reply.trim();
    if trimmed.is_empty() {
        Vec::new()
    } else {
        vec![trimmed.to_string()]
    }
}

fn all_fenced_blocks(text: &str, lang: &str) -> Vec<String> {
    let lines: Vec<&str> = text.split('\n').collect();
    let mut out = Vec::new();
    let mut i = 0;
    while i < lines.len() {
        let trimmed = lines[i].trim();
        let opened = if lang.is_empty() {
            trimmed == "```"
        } else {
            trimmed.eq_ignore_ascii_case(&format!("```{lang}"))
        };
        if !opened {
            i += 1;
            continue;
        }
        let mut body = Vec::new();
        let mut j = i + 1;
        while j < lines.len() {
            if lines[j].trim() == "```" {
                break;
            }
            body.push(lines[j]);
            j += 1;
        }
        let block = body.join("\n");
        let block = block.trim();
        if !block.is_empty() {
            out.push(block.to_string());
        }
        i = j + 1;
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn extract_sql_prefers_sql_fence() {
        assert_eq!(extract_sql("Sure:\n```sql\nSELECT 1;\n```\nHope"), "SELECT 1;");
        assert_eq!(extract_sql("```SQL\nSELECT 2\n```"), "SELECT 2");
        assert_eq!(extract_sql("here:\n```\nSELECT 3\n```"), "SELECT 3");
        assert_eq!(extract_sql("SELECT 4"), "SELECT 4");
    }

    #[test]
    fn extract_blocks_returns_all() {
        let r = "```sql\nA\n```\ntext\n```sql\nB\n```";
        assert_eq!(extract_blocks(r, &["sql"]), vec!["A".to_string(), "B".to_string()]);
    }
}
