//! Minimal client for the parts of the NOVA API this app uses.
//! Extend it with the endpoints you need: https://docs.wandelbots.io

use reqwest::Url;
use std::fmt;

use reqwest::header::{AUTHORIZATION, CONTENT_TYPE, HeaderMap, HeaderValue};
use serde::Deserialize;
use serde::de::DeserializeOwned;

#[derive(Clone)]
pub struct NovaClient {
    http: reqwest::Client,
    base_url: Url,
    cell: String,
}

impl NovaClient {
    pub fn new(
        base_url: Url,
        cell: String,
        access_token: Option<&str>,
    ) -> Result<Self, Box<dyn std::error::Error>> {
        let mut headers = HeaderMap::new();
        if let Some(token) = access_token {
            let mut value = HeaderValue::from_str(&format!("Bearer {token}"))?;
            value.set_sensitive(true);
            headers.insert(AUTHORIZATION, value);
        }
        let http = reqwest::Client::builder()
            .default_headers(headers)
            .build()?;
        Ok(Self {
            http,
            base_url,
            cell,
        })
    }

    /// GET /cells/{cell}/controllers
    pub async fn list_controllers(&self) -> Result<Vec<String>, NovaError> {
        self.get(&["controllers"]).await
    }

    /// GET /cells/{cell}/controllers/{controller}/state
    pub async fn controller_state(
        &self,
        controller: &str,
    ) -> Result<RobotControllerState, NovaError> {
        self.get(&["controllers", controller, "state"]).await
    }

    async fn get<T: DeserializeOwned>(&self, segments: &[&str]) -> Result<T, NovaError> {
        let mut url = self.base_url.clone();
        url.path_segments_mut()
            .expect("NOVA API url is a base url")
            .pop_if_empty()
            .extend(["cells", &self.cell])
            .extend(segments);
        let resp = self.http.get(url).send().await?.error_for_status()?;

        // The ingress of an instance may answer errors (e.g. 401 for a missing or expired
        // ACCESS_TOKEN) with a plain text error page and status 200, so check the content type.
        let is_json = resp
            .headers()
            .get(CONTENT_TYPE)
            .and_then(|v| v.to_str().ok())
            .is_some_and(|v| v.starts_with("application/json"));
        if !is_json {
            let url = resp.url().clone();
            let body = resp.text().await.unwrap_or_default();
            return Err(NovaError::NotJson {
                url,
                message: body.lines().next().unwrap_or_default().trim().to_string(),
            });
        }
        Ok(resp.json().await?)
    }
}

#[derive(Debug)]
pub enum NovaError {
    Request(reqwest::Error),
    /// The response was not JSON, most likely an error page of the ingress.
    NotJson {
        url: Url,
        message: String,
    },
}

impl From<reqwest::Error> for NovaError {
    fn from(err: reqwest::Error) -> Self {
        NovaError::Request(err)
    }
}

impl fmt::Display for NovaError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            NovaError::Request(err) => write!(f, "{err}"),
            NovaError::NotJson { url, message } => write!(
                f,
                "unexpected non-JSON response for url ({url}): {message:?}. Check NOVA_API and ACCESS_TOKEN (refresh it with `nova app env`)."
            ),
        }
    }
}

impl std::error::Error for NovaError {}

#[derive(Debug, Deserialize)]
pub struct RobotControllerState {
    #[serde(default)]
    pub motion_groups: Vec<MotionGroupState>,
}

#[derive(Debug, Deserialize)]
pub struct MotionGroupState {
    pub motion_group: String,
    pub joint_position: Vec<f64>,
    /// Missing if the motion group has no tcp configured.
    pub tcp_pose: Option<Pose>,
}

#[derive(Debug, Deserialize)]
pub struct Pose {
    pub position: [f64; 3],
    /// Rotation vector
    pub orientation: [f64; 3],
}
