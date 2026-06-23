//! Query result sets and CSV/JSON export. Port of `internal/result`.
//!
//! Dynamic cell values are represented with [`serde_json::Value`]; database
//! adapters convert their native values (including byte strings) into this type
//! when materializing a result.

use std::io::{self, Write};

use serde::{Deserialize, Serialize};
use serde_json::{Map, Value};

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Column {
    pub name: String,
    #[serde(default, skip_serializing_if = "String::is_empty", rename = "type")]
    pub type_: String,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Row {
    pub values: Vec<Value>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Table {
    pub columns: Vec<Column>,
    pub rows: Vec<Row>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Document {
    pub id: String,
    pub data: Map<String, Value>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Set {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub table: Option<Table>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub documents: Vec<Document>,
    #[serde(default, skip_serializing_if = "Value::is_null")]
    pub value: Value,
    pub has_more: bool,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub next_cursor: String,
    #[serde(default, skip_serializing_if = "is_false")]
    pub truncated: bool,
}

fn is_false(b: &bool) -> bool {
    !*b
}

#[derive(Debug, Clone, Copy, Default, Serialize, Deserialize)]
pub struct MutationResult {
    pub affected_rows: i64,
}

pub fn new_mutation_result(affected_rows: i64) -> MutationResult {
    MutationResult { affected_rows }
}

impl Table {
    /// Render a cell as display text: `NULL` for nulls, the raw string for
    /// strings (no JSON quotes), and the natural form for scalars.
    pub fn cell_string(&self, row_index: usize, column_index: usize) -> String {
        let Some(row) = self.rows.get(row_index) else {
            return String::new();
        };
        match row.values.get(column_index) {
            None => String::new(),
            Some(v) => cell_value_string(v),
        }
    }
}

pub fn cell_value_string(v: &Value) -> String {
    match v {
        Value::Null => "NULL".to_string(),
        Value::String(s) => s.clone(),
        Value::Bool(b) => b.to_string(),
        Value::Number(n) => n.to_string(),
        other => serde_json::to_string(other).unwrap_or_default(),
    }
}

impl Set {
    /// Write the result as CSV. Field quoting matches Go's `encoding/csv`: a
    /// field is quoted when it contains `,`, `"`, `\n`, or `\r`, with embedded
    /// quotes doubled. Rows are terminated with `\n`.
    pub fn write_csv<W: Write>(&self, w: &mut W) -> io::Result<()> {
        if let Some(table) = &self.table {
            let header: Vec<String> = table.columns.iter().map(|c| c.name.clone()).collect();
            write_csv_record(w, &header)?;
            for r in 0..table.rows.len() {
                let record: Vec<String> = (0..table.columns.len())
                    .map(|c| table.cell_string(r, c))
                    .collect();
                write_csv_record(w, &record)?;
            }
        } else if !self.documents.is_empty() {
            write_csv_record(w, &["document".to_string()])?;
            for doc in &self.documents {
                let raw = serde_json::to_string(&doc.data).unwrap_or_default();
                write_csv_record(w, &[raw])?;
            }
        } else {
            write_csv_record(w, &["value".to_string()])?;
            write_csv_record(w, &[cell_value_string(&self.value)])?;
        }
        Ok(())
    }

    /// Write the result as pretty JSON: a table becomes an array of objects
    /// keyed by column name, documents become an array of their data, otherwise
    /// the scalar value.
    pub fn write_json<W: Write>(&self, w: &mut W) -> io::Result<()> {
        let payload: Value = if let Some(table) = &self.table {
            let rows: Vec<Value> = table
                .rows
                .iter()
                .map(|row| {
                    let mut obj = Map::new();
                    for (c, col) in table.columns.iter().enumerate() {
                        let v = row.values.get(c).cloned().unwrap_or(Value::Null);
                        obj.insert(col.name.clone(), v);
                    }
                    Value::Object(obj)
                })
                .collect();
            Value::Array(rows)
        } else if !self.documents.is_empty() {
            Value::Array(
                self.documents
                    .iter()
                    .map(|d| Value::Object(d.data.clone()))
                    .collect(),
            )
        } else {
            self.value.clone()
        };
        let s = serde_json::to_string_pretty(&payload)?;
        w.write_all(s.as_bytes())?;
        w.write_all(b"\n")
    }
}

fn write_csv_record<W: Write>(w: &mut W, fields: &[String]) -> io::Result<()> {
    for (i, f) in fields.iter().enumerate() {
        if i > 0 {
            w.write_all(b",")?;
        }
        if needs_quote(f) {
            w.write_all(b"\"")?;
            w.write_all(f.replace('"', "\"\"").as_bytes())?;
            w.write_all(b"\"")?;
        } else {
            w.write_all(f.as_bytes())?;
        }
    }
    w.write_all(b"\n")
}

fn needs_quote(s: &str) -> bool {
    s.contains(',') || s.contains('"') || s.contains('\n') || s.contains('\r')
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn sample_set() -> Set {
        Set {
            table: Some(Table {
                columns: vec![
                    Column {
                        name: "id".into(),
                        type_: String::new(),
                    },
                    Column {
                        name: "name".into(),
                        type_: String::new(),
                    },
                ],
                rows: vec![
                    Row {
                        values: vec![json!(1), json!("a,b\"c")],
                    },
                    Row {
                        values: vec![json!(2), Value::Null],
                    },
                ],
            }),
            ..Default::default()
        }
    }

    #[test]
    fn csv_quotes_and_nulls() {
        let mut buf = Vec::new();
        sample_set().write_csv(&mut buf).unwrap();
        let out = String::from_utf8(buf).unwrap();
        assert!(out.starts_with("id,name\n"), "{out}");
        assert!(out.contains("\"a,b\"\"c\""), "{out}");
        assert!(out.contains("2,NULL"), "{out}");
    }

    #[test]
    fn json_emits_objects_with_nulls() {
        let mut buf = Vec::new();
        sample_set().write_json(&mut buf).unwrap();
        let out = String::from_utf8(buf).unwrap();
        assert!(out.contains("\"id\"") && out.contains("\"name\""), "{out}");
        assert!(out.contains("null"), "{out}");
    }

    #[test]
    fn cell_string_formats_nil_bytes_and_scalars() {
        let table = Table {
            columns: vec![
                Column { name: "id".into(), type_: String::new() },
                Column { name: "payload".into(), type_: String::new() },
                Column { name: "missing".into(), type_: String::new() },
            ],
            rows: vec![Row {
                values: vec![json!(7), json!("hello"), Value::Null],
            }],
        };
        assert_eq!(table.cell_string(0, 0), "7");
        assert_eq!(table.cell_string(0, 1), "hello");
        assert_eq!(table.cell_string(0, 2), "NULL");
    }

    #[test]
    fn mutation_result_captures_affected_rows() {
        assert_eq!(new_mutation_result(3).affected_rows, 3);
    }
}
