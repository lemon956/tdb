use std::process::ExitCode;

use tdb::cli::Options;

fn main() -> ExitCode {
    let options = Options::parse(std::env::args().skip(1));

    let runtime = match tokio::runtime::Builder::new_multi_thread().enable_all().build() {
        Ok(rt) => rt,
        Err(e) => {
            eprintln!("tdb: failed to start runtime: {e}");
            return ExitCode::FAILURE;
        }
    };

    match runtime.block_on(tdb::app::run(options)) {
        Ok(()) => ExitCode::SUCCESS,
        Err(e) => {
            eprintln!("tdb: {e:#}");
            ExitCode::FAILURE
        }
    }
}
