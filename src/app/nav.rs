//! Navigation-tree flattening. Both rendering and input operate on the same
//! flat list of visible nodes, so a click row and a keyboard cursor index always
//! refer to the same node.

use crate::db::Object;

use super::state::{scope_key, NavState};

#[derive(Clone, PartialEq)]
pub enum NavKind {
    Connection,
    Catalog { catalog: String },
    Database { catalog: String, database: String },
    Object { catalog: String, database: String, object: Object },
}

#[derive(Clone)]
pub struct NavNode {
    pub depth: u16,
    pub label: String,
    pub kind: NavKind,
    pub expandable: bool,
    pub expanded: bool,
}

/// Flatten the tree into the currently visible nodes, honoring expand/collapse
/// state and the optional search filter. Takes `NavState` + label (not the whole
/// `Session`) so it is unit-testable without a live adapter.
pub fn visible_nodes(nav: &NavState, conn_label: &str) -> Vec<NavNode> {
    let mut out = Vec::new();
    let conn_expanded = !nav.connection_collapsed;
    out.push(NavNode {
        depth: 0,
        label: conn_label.to_string(),
        kind: NavKind::Connection,
        expandable: true,
        expanded: conn_expanded,
    });
    if !conn_expanded {
        return out;
    }

    if !nav.catalogs.is_empty() {
        for catalog in &nav.catalogs {
            let expanded = nav.expanded_catalogs.contains(catalog);
            push_filtered(
                &mut out,
                nav,
                NavNode {
                    depth: 1,
                    label: catalog.clone(),
                    kind: NavKind::Catalog {
                        catalog: catalog.clone(),
                    },
                    expandable: true,
                    expanded,
                },
            );
            if expanded {
                if let Some(dbs) = nav.catalog_databases.get(catalog) {
                    for db in dbs {
                        push_db_and_objects(&mut out, nav, catalog, db, 2);
                    }
                }
            }
        }
    } else {
        for db in &nav.databases {
            push_db_and_objects(&mut out, nav, "", db, 1);
        }
    }
    out
}

fn push_db_and_objects(out: &mut Vec<NavNode>, nav: &NavState, catalog: &str, db: &str, depth: u16) {
    let key = scope_key(catalog, db);
    let expanded = nav.expanded_dbs.contains(&key);
    push_filtered(
        out,
        nav,
        NavNode {
            depth,
            label: db.to_string(),
            kind: NavKind::Database {
                catalog: catalog.to_string(),
                database: db.to_string(),
            },
            expandable: true,
            expanded,
        },
    );
    if expanded {
        if let Some(objects) = nav.db_objects.get(&key) {
            for obj in objects {
                push_filtered(
                    out,
                    nav,
                    NavNode {
                        depth: depth + 1,
                        label: obj.name.clone(),
                        kind: NavKind::Object {
                            catalog: catalog.to_string(),
                            database: db.to_string(),
                            object: obj.clone(),
                        },
                        expandable: false,
                        expanded: false,
                    },
                );
            }
        }
    }
}

fn push_filtered(out: &mut Vec<NavNode>, _nav: &NavState, node: NavNode) {
    // Navigation search is jump-style (cursor moves to matches), not filter-style,
    // so the tree always shows every node. See `search_matches` / `nav_search_jump`.
    out.push(node);
}

/// Node kinds whose label contains `query_lc` (lowercase substring), in the same
/// top-to-bottom order as `visible_nodes` but descending into every catalog/db
/// regardless of expand state. Objects are only considered where already loaded.
/// Drives jump-style `/` search: the cursor moves to these matches with n/N.
pub fn search_matches(nav: &NavState, conn_label: &str, query_lc: &str) -> Vec<NavKind> {
    let mut out = Vec::new();
    if query_lc.is_empty() {
        return out;
    }
    if conn_label.to_lowercase().contains(query_lc) {
        out.push(NavKind::Connection);
    }
    if !nav.catalogs.is_empty() {
        for catalog in &nav.catalogs {
            if catalog.to_lowercase().contains(query_lc) {
                out.push(NavKind::Catalog { catalog: catalog.clone() });
            }
            if let Some(dbs) = nav.catalog_databases.get(catalog) {
                for db in dbs {
                    push_db_matches(&mut out, nav, catalog, db, query_lc);
                }
            }
        }
    } else {
        for db in &nav.databases {
            push_db_matches(&mut out, nav, "", db, query_lc);
        }
    }
    out
}

fn push_db_matches(out: &mut Vec<NavKind>, nav: &NavState, catalog: &str, db: &str, query_lc: &str) {
    if db.to_lowercase().contains(query_lc) {
        out.push(NavKind::Database { catalog: catalog.to_string(), database: db.to_string() });
    }
    if let Some(objects) = nav.db_objects.get(&scope_key(catalog, db)) {
        for obj in objects {
            if obj.name.to_lowercase().contains(query_lc) {
                out.push(NavKind::Object {
                    catalog: catalog.to_string(),
                    database: db.to_string(),
                    object: obj.clone(),
                });
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::db::ObjectType;

    fn obj(name: &str) -> Object {
        Object { name: name.to_string(), type_: ObjectType::Table, sub_type: String::new() }
    }

    #[test]
    fn search_matches_finds_objects_and_is_case_insensitive() {
        let mut nav = NavState::default();
        nav.databases = vec!["app".to_string(), "users_db".to_string()];
        nav.db_objects.insert("app".to_string(), vec![obj("users"), obj("orders")]);

        // Case-insensitive substring over db names and loaded objects.
        let m = search_matches(&nav, "conn", "user");
        // Matches: database "users_db", object "users". Order: app's objects
        // come before users_db (databases order), but only "users" object matches
        // and "users_db" database matches.
        assert!(m.iter().any(|k| matches!(k, NavKind::Object { object, .. } if object.name == "users")));
        assert!(m.iter().any(|k| matches!(k, NavKind::Database { database, .. } if database == "users_db")));
        assert!(!m.iter().any(|k| matches!(k, NavKind::Object { object, .. } if object.name == "orders")));

        // No match → empty; empty query → empty.
        assert!(search_matches(&nav, "conn", "zzz").is_empty());
        assert!(search_matches(&nav, "conn", "").is_empty());
    }
}
