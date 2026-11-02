use std::sync::Arc;

use axum::extract::State;
use axum::http::{StatusCode, header};
use axum::response::{Html, IntoResponse, Response};
use axum::routing::get;
use axum::{Json, Router};
use serde::Serialize;
use utoipa::openapi::server::Server;
use utoipa::{OpenApi, ToSchema};

use crate::nova::NovaClient;

const STOPLIGHT_HTML: &str = include_str!("stoplight.html");
const APP_ICON: &[u8] = include_bytes!("../static/app_icon.png");

pub struct AppState {
    /// Base path where this app is served from (e.g. /cell/myappname)
    pub base_path: String,
    pub nova: NovaClient,
}

/// The OpenAPI spec of this app, generated from the handler annotations below.
#[derive(OpenApi)]
#[openapi(
    info(title = "Nova App", description = "Nova App", version = "0.0.1"),
    paths(index, openapi_json, app_icon, position)
)]
struct ApiDoc;

/// Routes relative to the base path.
pub fn router(state: Arc<AppState>) -> Router {
    Router::new()
        .route("/", get(index))
        .route("/openapi.json", get(openapi_json))
        .route("/app_icon.png", get(app_icon))
        .route("/position", get(position))
        .with_state(state)
}

/// Gets the stoplight ui.
#[utoipa::path(get, path = "/", responses((status = 200, description = "The stoplight ui.", content_type = "text/html", body = String)))]
async fn index(State(state): State<Arc<AppState>>) -> Html<String> {
    let openapi_path = format!("{}/openapi.json", state.base_path);
    Html(STOPLIGHT_HTML.replace("__OPENAPI_PATH__", &openapi_path))
}

/// Gets the openapi spec.
#[utoipa::path(get, path = "/openapi.json", responses((status = 200, description = "The openapi spec.", content_type = "application/json", body = String)))]
async fn openapi_json(State(state): State<Arc<AppState>>) -> Json<utoipa::openapi::OpenApi> {
    let mut spec = ApiDoc::openapi();
    // the paths are relative to the base path the app is served from
    let server = if state.base_path.is_empty() {
        "/"
    } else {
        &state.base_path
    };
    spec.servers = Some(vec![Server::new(server)]);
    Json(spec)
}

/// Gets the app icon for the Nova Homescreen.
#[utoipa::path(get, path = "/app_icon.png", responses((status = 200, description = "The app icon for the Nova Homescreen.", content_type = "image/png", body = Vec<u8>)))]
async fn app_icon() -> impl IntoResponse {
    ([(header::CONTENT_TYPE, "image/png")], APP_ICON)
}

#[derive(Serialize, ToSchema)]
struct Pose {
    position: [f64; 3],
    orientation: [f64; 3],
}

#[derive(Serialize, ToSchema)]
struct Position {
    pose: Pose,
    joints: Vec<f64>,
}

/// Gets the position of the first motion group which was found.
#[utoipa::path(get, path = "/position", responses(
    (status = 200, description = "The current position.", body = Position),
    (status = 404, description = "No controller with a motion group found.", body = String),
))]
async fn position(State(state): State<Arc<AppState>>) -> Result<Json<Position>, AppError> {
    // the first motion group of the first controller that has one
    for controller in state.nova.list_controllers().await? {
        let controller_state = state.nova.controller_state(&controller).await?;
        let Some(motion_group) = controller_state.motion_groups.into_iter().next() else {
            continue;
        };
        tracing::debug!(
            controller,
            motion_group = motion_group.motion_group,
            "found motion group"
        );

        // the tcp pose in world coordinates
        let tcp_pose = motion_group
            .tcp_pose
            .ok_or(AppError::NotFound("No tcp pose found for motion group"))?;
        return Ok(Json(Position {
            pose: Pose {
                position: tcp_pose.position,
                orientation: tcp_pose.orientation,
            },
            joints: motion_group.joint_position,
        }));
    }
    Err(AppError::NotFound(
        "No controller with a motion group found",
    ))
}

enum AppError {
    NotFound(&'static str),
    Nova(reqwest::Error),
}

impl From<reqwest::Error> for AppError {
    fn from(err: reqwest::Error) -> Self {
        AppError::Nova(err)
    }
}

impl IntoResponse for AppError {
    fn into_response(self) -> Response {
        match self {
            AppError::NotFound(msg) => (StatusCode::NOT_FOUND, msg).into_response(),
            AppError::Nova(err) => {
                tracing::error!(error = %err, "nova api request failed");
                (
                    StatusCode::BAD_GATEWAY,
                    format!("NOVA API request failed: {err}"),
                )
                    .into_response()
            }
        }
    }
}
