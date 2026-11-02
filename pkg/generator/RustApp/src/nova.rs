//! Minimal client for the parts of the NOVA API this app uses.
//! Extend it with the endpoints you need: https://docs.wandelbots.io

use reqwest::Url;
use reqwest::header::{AUTHORIZATION, HeaderMap, HeaderValue};
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
    pub async fn list_controllers(&self) -> Result<Vec<String>, reqwest::Error> {
        self.get(&["controllers"]).await
    }

    /// GET /cells/{cell}/controllers/{controller}/state
    pub async fn controller_state(
        &self,
        controller: &str,
    ) -> Result<RobotControllerState, reqwest::Error> {
        self.get(&["controllers", controller, "state"]).await
    }

    async fn get<T: DeserializeOwned>(&self, segments: &[&str]) -> Result<T, reqwest::Error> {
        let mut url = self.base_url.clone();
        url.path_segments_mut()
            .expect("NOVA API url is a base url")
            .pop_if_empty()
            .extend(["cells", &self.cell])
            .extend(segments);
        self.http
            .get(url)
            .send()
            .await?
            .error_for_status()?
            .json()
            .await
    }
}

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
