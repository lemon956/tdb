.PHONY: build test run fmt clippy

build:
	cargo build --release

test:
	cargo test

run:
	cargo run -- --config $${HOME}/.config/tdb/tdb.enc

fmt:
	cargo fmt

clippy:
	cargo clippy --all-targets -- -D warnings
