//! TDB — full-screen terminal database manager (Rust rewrite).
//!
//! Library crate exposing the backend modules and the TUI application. The
//! `tdb` binary (`src/main.rs`) is a thin entry point over [`run`].

pub mod ai;
pub mod aichat;
pub mod cli;
pub mod config;
pub mod db;
pub mod history;
pub mod result;
pub mod suggest;
pub mod validate;
pub mod terminal;

pub mod app;

pub use cli::Options;
