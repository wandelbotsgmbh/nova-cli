mod api;
mod env;
mod nova;

use std::sync::Arc;

use axum::extract::Request;
use axum::{Router, ServiceExt};
use tower::Layer;
use tower_http::cors::CorsLayer;
use tower_http::normalize_path::NormalizePathLayer;
use tower_http::trace::TraceLayer;
use tracing_subscriber::EnvFilter;

use crate::api::AppState;
use crate::env::Config;
use crate::nova::NovaClient;

#[tokio::main]
async fn main() {
    // local development settings, written by `nova app update-env`. Variables already set in the
    // environment take precedence; in the container the platform sets them and there is no file.
    let env_file = dotenvy::from_filename(".env.local").ok();

    tracing_subscriber::fmt()
        .with_env_filter(EnvFilter::new(format!(
            "{}={level},tower_http={level}",
            env!("CARGO_CRATE_NAME"),
            level = env::log_level()
        )))
        .init();
    if let Some(path) = env_file {
        tracing::info!("loaded {}", path.display());
    }

    if let Err(err) = run().await {
        tracing::error!("{err}");
        std::process::exit(1);
    }
}

async fn run() -> Result<(), Box<dyn std::error::Error>> {
    rustls::crypto::ring::default_provider()
        .install_default()
        .map_err(|_| "failed to install the rustls crypto provider")?;

    let config = Config::from_env()?;
    let nova = NovaClient::new(
        config.nova_api.clone(),
        config.cell.clone(),
        config.access_token.as_deref(),
    )?;
    let state = Arc::new(AppState {
        base_path: config.base_path.clone(),
        nova,
    });

    let routes = api::router(state);
    let app = if config.base_path.is_empty() {
        routes
    } else {
        Router::new().nest(&config.base_path, routes)
    }
    .layer(CorsLayer::permissive())
    .layer(TraceLayer::new_for_http());

    // strip trailing slashes before routing, so /cell/app/ and /cell/app/position/ match too
    let app = NormalizePathLayer::trim_trailing_slash().layer(app);

    let listener = tokio::net::TcpListener::bind("0.0.0.0:8080").await?;
    tracing::info!(
        base_path = %config.base_path,
        nova_api = %config.nova_api,
        cell = %config.cell,
        "listening on {}",
        listener.local_addr()?
    );
    axum::serve(listener, ServiceExt::<Request>::into_make_service(app))
        .with_graceful_shutdown(shutdown_signal())
        .await?;
    Ok(())
}

/// Resolves on ctrl-c or SIGTERM (sent by kubernetes when the app is stopped or updated).
async fn shutdown_signal() {
    let ctrl_c = async {
        tokio::signal::ctrl_c().await.ok();
    };
    #[cfg(unix)]
    let terminate = async {
        if let Ok(mut sig) =
            tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())
        {
            sig.recv().await;
        }
    };
    #[cfg(not(unix))]
    let terminate = std::future::pending::<()>();

    tokio::select! {
        _ = ctrl_c => {},
        _ = terminate => {},
    }
    tracing::info!("shutting down");
}
