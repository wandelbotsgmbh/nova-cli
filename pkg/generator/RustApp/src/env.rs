use std::env;

use reqwest::Url;

/// Runtime configuration, read from the environment variables NOVA sets for the app.
#[derive(Debug, Clone)]
pub struct Config {
    /// Base path the app is served from (e.g. /cell/myappname). Empty if served from the root.
    pub base_path: String,
    /// The URL of the NOVA API (e.g. https://xdxqzvpu.instance.wandelbots.io/api/v2/)
    pub nova_api: Url,
    /// The cell this app is running in.
    pub cell: String,
    /// Optional access token, needed when talking to a cloud instance from your machine.
    pub access_token: Option<String>,
}

impl Config {
    pub fn from_env() -> Result<Self, String> {
        Ok(Self {
            base_path: base_path(&var("BASE_PATH")),
            nova_api: nova_api(&var("NOVA_API"))?,
            cell: Some(var("CELL_NAME"))
                .filter(|c| !c.is_empty())
                .unwrap_or_else(|| "cell".to_string()),
            access_token: Some(var("ACCESS_TOKEN")).filter(|t| !t.is_empty()),
        })
    }
}

/// Log filter from LOG_LEVEL (debug, info, warn, error). Defaults to info.
pub fn log_level() -> String {
    match var("LOG_LEVEL").to_lowercase().as_str() {
        level @ ("trace" | "debug" | "warn" | "error") => level.to_string(),
        _ => "info".to_string(),
    }
}

fn var(name: &str) -> String {
    env::var(name).unwrap_or_default().trim().to_string()
}

fn base_path(raw: &str) -> String {
    let trimmed = raw.trim_matches('/');
    if trimmed.is_empty() {
        String::new()
    } else {
        format!("/{trimmed}")
    }
}

fn nova_api(raw: &str) -> Result<Url, String> {
    if raw.is_empty() {
        return Err("NOVA_API environment variable is not set".to_string());
    }
    let host = raw.trim_end_matches('/');
    let url = if host.contains("://") {
        format!("{host}/api/v2/")
    } else {
        format!("http://{host}/api/v2/")
    };
    Url::parse(&url).map_err(|e| format!("invalid NOVA_API {raw:?}: {e}"))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn normalizes_base_path() {
        assert_eq!(base_path(""), "");
        assert_eq!(base_path("/"), "");
        assert_eq!(base_path("cell/app/"), "/cell/app");
        assert_eq!(base_path("/cell/app"), "/cell/app");
    }

    #[test]
    fn normalizes_nova_api() {
        assert_eq!(
            nova_api("api-gateway:8080").unwrap().as_str(),
            "http://api-gateway:8080/api/v2/"
        );
        assert_eq!(
            nova_api("https://foo.instance.wandelbots.io/")
                .unwrap()
                .as_str(),
            "https://foo.instance.wandelbots.io/api/v2/"
        );
        assert!(nova_api("").is_err());
    }
}
