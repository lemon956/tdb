//! Navigation-tree flattening. Both rendering and input operate on the same
//! flat list of visible nodes, so a click row and a keyboard cursor index always
//! refer to the same node.

use crate::db::Object;

use super::state::{scope_key, NavState};

#[derive(Clone)]
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

fn push_filtered(out: &mut Vec<NavNode>, nav: &NavState, node: NavNode) {
    if nav.search_active && !nav.search_query.is_empty() {
        // Keep structural nodes (connection/catalog/database) always; filter
        // object leaves by name so search narrows the visible objects.
        if let NavKind::Object { .. } = node.kind {
            if !node
                .label
                .to_lowercase()
                .contains(&nav.search_query.to_lowercase())
            {
                return;
            }
        }
    }
    out.push(node);
}
