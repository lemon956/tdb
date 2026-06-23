//! Connection form construction and submission.

use crate::config::{Driver, Profile};

use super::state::{App, ConnectionForm, FormField};
use super::theme::StatusKind;

const DRIVERS: [Driver; 4] = [Driver::Mysql, Driver::Doris, Driver::Mongo, Driver::Redis];

fn field(key: &str, label: &str, value: &str, secret: bool) -> FormField {
    FormField {
        key: key.into(),
        label: label.into(),
        value: value.into(),
        secret,
        cursor: value.chars().count(),
    }
}

fn fields_for_driver(driver: Driver, p: Option<&Profile>) -> Vec<FormField> {
    let g = |k: &str| p.map(|p| field_value(p, k)).unwrap_or_default();
    match driver {
        Driver::Mysql | Driver::Doris => vec![
            field("id", "id", &g("id"), false),
            field("host", "host", &g("host"), false),
            field("port", "port", &g("port"), false),
            field("user", "user", &g("user"), false),
            field("password", "password", &g("password"), true),
            field("database", "database", &g("database"), false),
        ],
        Driver::Mongo => vec![
            field("id", "id", &g("id"), false),
            field("uri", "uri", &g("uri"), false),
            field("database", "database", &g("database"), false),
        ],
        Driver::Redis => vec![
            field("id", "id", &g("id"), false),
            field("host", "host", &g("host"), false),
            field("port", "port", &g("port"), false),
            field("user", "user", &g("user"), false),
            field("password", "password", &g("password"), true),
            field("db", "db", &g("db"), false),
        ],
    }
}

fn field_value(p: &Profile, key: &str) -> String {
    match key {
        "id" => p.id.clone(),
        "host" => p.host.clone(),
        "port" => {
            if p.port == 0 {
                String::new()
            } else {
                p.port.to_string()
            }
        }
        "user" => p.user.clone(),
        "password" => p.password.clone(),
        "database" => p.database.clone(),
        "uri" => p.uri_params.clone(),
        "db" => p.redis_db.to_string(),
        _ => String::new(),
    }
}

impl App {
    pub fn open_new_form(&mut self) {
        self.form = Some(ConnectionForm {
            editing_id: None,
            selecting_driver: true,
            driver_index: 0,
            fields: fields_for_driver(Driver::Mysql, None),
            active_field: 0,
        });
        self.focus = super::state::Focus::Form;
    }

    pub fn open_edit_form(&mut self, profile_id: &str) {
        let Some(p) = self.vault.get_profile(profile_id).cloned() else { return };
        let driver_index = DRIVERS.iter().position(|d| *d == p.driver).unwrap_or(0);
        self.form = Some(ConnectionForm {
            editing_id: Some(p.id.clone()),
            selecting_driver: false,
            driver_index,
            fields: fields_for_driver(p.driver, Some(&p)),
            active_field: 0,
        });
        self.focus = super::state::Focus::Form;
    }

    /// Confirm the selected driver and switch the form to its field set.
    pub fn confirm_form_driver(&mut self) {
        let driver = self.form_driver();
        if let Some(form) = self.form.as_mut() {
            form.fields = fields_for_driver(driver, None);
            form.selecting_driver = false;
            form.active_field = 0;
        }
    }

    pub fn form_driver(&self) -> Driver {
        self.form
            .as_ref()
            .map(|f| DRIVERS[f.driver_index.min(3)])
            .unwrap_or(Driver::Mysql)
    }

    pub fn close_form(&mut self) {
        self.form = None;
        self.focus = if self.sessions.is_empty() {
            super::state::Focus::Command
        } else {
            super::state::Focus::Sidebar
        };
    }

    /// Build a profile from the current form and persist it.
    pub fn submit_form(&mut self) {
        let driver = self.form_driver();
        let Some(form) = &self.form else { return };
        let get = |k: &str| -> String {
            form.fields
                .iter()
                .find(|f| f.key == k)
                .map(|f| f.value.clone())
                .unwrap_or_default()
        };
        let id = get("id");
        if id.trim().is_empty() {
            self.set_status(StatusKind::Error, "id is required");
            return;
        }
        let mut profile = Profile {
            id: id.clone(),
            name: id.clone(),
            driver,
            host: get("host"),
            port: get("port").trim().parse().unwrap_or(0),
            user: get("user"),
            password: get("password"),
            database: get("database"),
            auth_db: String::new(),
            redis_db: get("db").trim().parse().unwrap_or(0),
            uri_params: String::new(),
            read_only: false,
        };
        if driver == Driver::Mongo {
            profile.uri_params = get("uri");
            // Pull database out of a mongodb:// path if present and not set.
            if profile.database.is_empty() {
                if let Some(rest) = profile
                    .uri_params
                    .split_once("://")
                    .and_then(|(_, r)| r.split_once('/'))
                    .map(|(_, p)| p)
                {
                    profile.database = rest.split('?').next().unwrap_or("").to_string();
                }
            }
        }
        self.vault.upsert_profile(profile);
        match self.store.save(&self.master, &self.vault) {
            Ok(()) => self.set_status(StatusKind::Success, format!("saved {id}")),
            Err(e) => self.set_status(StatusKind::Error, format!("save failed: {e}")),
        }
        self.close_form();
    }
}
