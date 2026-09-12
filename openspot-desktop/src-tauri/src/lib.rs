use serde::Deserialize;
use serde_json::{json, Value};
use std::collections::HashMap;
use std::io::{BufRead, BufReader, Write};
use std::path::PathBuf;
use std::process::{Child, ChildStdin, ChildStdout, Command, Stdio};
use std::sync::Mutex;
use tauri::Manager;

fn offline_dir(app: &tauri::AppHandle) -> Result<PathBuf, String> {
    let dir = app
        .path()
        .app_data_dir()
        .map_err(|error| error.to_string())?
        .join("offline");
    std::fs::create_dir_all(&dir).map_err(|error| error.to_string())?;
    Ok(dir)
}

fn sanitize_file_name(file_name: &str) -> String {
    let sanitized: String = file_name
        .chars()
        .filter(|ch| ch.is_ascii_alphanumeric() || matches!(ch, '.' | '-' | '_'))
        .collect();

    if sanitized.is_empty() {
        "offline_file".to_string()
    } else {
        sanitized
    }
}

#[tauri::command]
fn save_offline_file(
    app: tauri::AppHandle,
    file_name: String,
    bytes: Vec<u8>,
) -> Result<String, String> {
    let path = offline_dir(&app)?.join(sanitize_file_name(&file_name));
    std::fs::write(&path, bytes).map_err(|error| error.to_string())?;
    Ok(path.to_string_lossy().to_string())
}

#[tauri::command]
fn delete_offline_file(app: tauri::AppHandle, path: String) -> Result<(), String> {
    let offline = offline_dir(&app)?;
    let target = PathBuf::from(path);

    if !target.exists() {
        return Ok(());
    }

    let offline = offline.canonicalize().map_err(|error| error.to_string())?;
    let target = target.canonicalize().map_err(|error| error.to_string())?;
    if !target.starts_with(offline) {
        return Err("Refusing to delete a file outside offline storage".to_string());
    }

    std::fs::remove_file(target).map_err(|error| error.to_string())
}

fn validate_offline_file(app: &tauri::AppHandle, path: String) -> Result<PathBuf, String> {
    let offline = offline_dir(app)?;
    let target = PathBuf::from(path);

    if !target.exists() {
        return Err("Offline file does not exist".to_string());
    }

    let offline = offline.canonicalize().map_err(|error| error.to_string())?;
    let target = target.canonicalize().map_err(|error| error.to_string())?;
    if !target.starts_with(offline) {
        return Err("Refusing to access a file outside offline storage".to_string());
    }

    Ok(target)
}

#[tauri::command]
fn read_offline_file(app: tauri::AppHandle, path: String) -> Result<Vec<u8>, String> {
    let target = validate_offline_file(&app, path)?;
    std::fs::read(target).map_err(|error| error.to_string())
}

#[tauri::command]
fn offline_file_exists(app: tauri::AppHandle, path: String) -> Result<bool, String> {
    let Ok(target) = validate_offline_file(&app, path) else {
        return Ok(false);
    };

    Ok(target
        .metadata()
        .map(|metadata| metadata.len() > 0)
        .unwrap_or(false))
}

#[tauri::command]
async fn proxy_http(
    url: String,
    method: String,
    headers: HashMap<String, String>,
    body: Option<String>,
) -> Result<serde_json::Value, String> {
    let client = reqwest::Client::builder()
        .danger_accept_invalid_certs(false)
        .build()
        .map_err(|e| e.to_string())?;

    let mut req = match method.to_uppercase().as_str() {
        "POST" => client.post(&url),
        "GET" => client.get(&url),
        "PUT" => client.put(&url),
        "DELETE" => client.delete(&url),
        _ => return Err(format!("Unsupported method: {}", method)),
    };

    for (key, value) in &headers {
        req = req.header(key.as_str(), value.as_str());
    }

    if let Some(b) = body {
        req = req.body(b);
    }

    let res = req.send().await.map_err(|e| e.to_string())?;
    let status = res.status().as_u16();
    let response_body: serde_json::Value = res.json().await.map_err(|e| e.to_string())?;

    Ok(serde_json::json!({
      "status": status,
      "data": response_body,
    }))
}

// Stable command boundary for the shared OpenSpot Extension Core contract.
// Desktop uses a long-lived Go sidecar so the same pinned Extension Core
// implementation is used by the Tauri UI and the native mobile bridge.
struct ExtensionCoreProcess {
    child: Child,
    stdin: ChildStdin,
    stdout: BufReader<ChildStdout>,
}

struct ExtensionCoreHost {
    process: Mutex<Option<ExtensionCoreProcess>>,
}

impl Default for ExtensionCoreHost {
    fn default() -> Self {
        Self {
            process: Mutex::new(None),
        }
    }
}

#[derive(Deserialize)]
struct ExtensionCoreHostResponse {
    result: Option<Value>,
    error: Option<String>,
}

fn add_extension_host_candidates(candidates: &mut Vec<PathBuf>, directory: PathBuf) {
    for name in [
        "openspot-extension-core-host",
        "openspot-extension-core-host.exe",
    ] {
        candidates.push(directory.join(name));
    }

    if let Ok(entries) = std::fs::read_dir(directory) {
        for entry in entries.flatten() {
            let path = entry.path();
            if path.is_file()
                && path
                    .file_name()
                    .and_then(|name| name.to_str())
                    .is_some_and(|name| name.starts_with("openspot-extension-core-host"))
            {
                candidates.push(path);
            }
        }
    }
}

fn find_extension_core_host(app: &tauri::AppHandle) -> Result<PathBuf, String> {
    let mut candidates = Vec::new();
    if let Ok(override_path) = std::env::var("OPENSPOT_EXTENSION_CORE_HOST") {
        let path = PathBuf::from(override_path);
        if path.is_file() {
            return Ok(path);
        }
    }

    if let Ok(directory) = app.path().resource_dir() {
        add_extension_host_candidates(&mut candidates, directory.clone());
        add_extension_host_candidates(&mut candidates, directory.join("binaries"));
    }
    if let Ok(directory) = app.path().executable_dir() {
        add_extension_host_candidates(&mut candidates, directory);
    }
    if let Ok(executable) = std::env::current_exe() {
        if let Some(directory) = executable.parent() {
            add_extension_host_candidates(&mut candidates, directory.to_path_buf());
            add_extension_host_candidates(&mut candidates, directory.join("binaries"));
        }
    }

    candidates
        .into_iter()
        .find(|candidate| candidate.is_file())
        .ok_or_else(|| {
            "Extension Core sidecar is not bundled; rebuild the desktop package with the Go host"
                .to_string()
        })
}

impl ExtensionCoreHost {
    fn start_process(&self, app: &tauri::AppHandle) -> Result<ExtensionCoreProcess, String> {
        let executable = find_extension_core_host(app)?;
        let key_file = app
            .path()
            .app_data_dir()
            .map_err(|error| error.to_string())?
            .join("extension-core")
            .join("storage-master-key");
        if let Some(parent) = key_file.parent() {
            std::fs::create_dir_all(parent).map_err(|error| error.to_string())?;
        }

        let mut child = Command::new(executable)
            .env("OPENSPOT_EXTENSION_CORE_KEY_FILE", key_file)
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::null())
            .spawn()
            .map_err(|error| format!("start Extension Core sidecar: {error}"))?;
        let stdin = child
            .stdin
            .take()
            .ok_or_else(|| "Extension Core sidecar stdin is unavailable".to_string())?;
        let stdout = child
            .stdout
            .take()
            .ok_or_else(|| "Extension Core sidecar stdout is unavailable".to_string())?;
        Ok(ExtensionCoreProcess {
            child,
            stdin,
            stdout: BufReader::new(stdout),
        })
    }

    fn call_raw(
        &self,
        app: &tauri::AppHandle,
        operation: &str,
        payload: Value,
    ) -> Result<String, String> {
        let mut process_guard = self
            .process
            .lock()
            .map_err(|_| "Extension Core process lock is poisoned".to_string())?;
        if process_guard.is_none() {
            *process_guard = Some(self.start_process(app)?);
        }

        let process = process_guard.as_mut().expect("process initialized");
        let request = json!({
          "operation": operation,
          "payload": payload,
        });
        let request_line = serde_json::to_string(&request).map_err(|error| error.to_string())?;
        if let Err(error) =
            writeln!(process.stdin, "{request_line}").and_then(|_| process.stdin.flush())
        {
            let _ = process.child.kill();
            *process_guard = None;
            return Err(format!("send Extension Core request: {error}"));
        }

        let mut line = String::new();
        match process.stdout.read_line(&mut line) {
            Ok(0) => {
                let _ = process.child.kill();
                *process_guard = None;
                Err("Extension Core sidecar exited unexpectedly".to_string())
            }
            Ok(_) => {
                let response: ExtensionCoreHostResponse = serde_json::from_str(&line)
                    .map_err(|error| format!("decode Extension Core response: {error}"))?;
                if let Some(error) = response.error {
                    return Err(error);
                }
                Ok(response.result.unwrap_or(Value::Null).to_string())
            }
            Err(error) => {
                let _ = process.child.kill();
                *process_guard = None;
                Err(format!("read Extension Core response: {error}"))
            }
        }
    }

    fn call(
        &self,
        app: &tauri::AppHandle,
        operation: String,
        payload: Option<Value>,
    ) -> Result<String, String> {
        if operation == "status" {
            return match find_extension_core_host(app) {
                Ok(path) => Ok(json!({
                  "available": true,
                  "path": path.to_string_lossy(),
                })
                .to_string()),
                Err(reason) => Ok(json!({
                  "available": false,
                  "reason": reason,
                })
                .to_string()),
            };
        }

        let payload = payload.unwrap_or_else(|| json!([]));
        if operation == "init" {
            let data_root = app
                .path()
                .app_data_dir()
                .map_err(|error| error.to_string())?;
            let extensions_dir = data_root.join("extensions");
            let extension_data_dir = data_root.join("extension-data");
            std::fs::create_dir_all(&extensions_dir).map_err(|error| error.to_string())?;
            std::fs::create_dir_all(&extension_data_dir).map_err(|error| error.to_string())?;
            self.call_raw(
                app,
                "InitExtensionSystem",
                json!([
                    extensions_dir.to_string_lossy(),
                    extension_data_dir.to_string_lossy(),
                ]),
            )?;
            let cache_dir = app
                .path()
                .app_cache_dir()
                .map_err(|error| error.to_string())?
                .join("extension-repository");
            std::fs::create_dir_all(&cache_dir).map_err(|error| error.to_string())?;
            self.call_raw(
                app,
                "InitExtensionRepoJSON",
                json!([cache_dir.to_string_lossy()]),
            )
        } else if operation == "InitExtensionRepoJSON" {
            let cache_dir = app
                .path()
                .app_cache_dir()
                .map_err(|error| error.to_string())?
                .join("extension-repository");
            let requested = payload
                .as_array()
                .and_then(|args| args.first())
                .and_then(Value::as_str)
                .filter(|value| !value.trim().is_empty())
                .map(PathBuf::from)
                .unwrap_or(cache_dir);
            std::fs::create_dir_all(&requested).map_err(|error| error.to_string())?;
            self.call_raw(
                app,
                "InitExtensionRepoJSON",
                json!([requested.to_string_lossy()]),
            )
        } else if operation == "DownloadRepoExtensionJSON" {
            let mut args = payload
                .as_array()
                .cloned()
                .ok_or_else(|| "DownloadRepoExtensionJSON payload must be an array".to_string())?;
            if args.len() < 2 {
                args.push(Value::String(String::new()));
            }
            if args[1]
                .as_str()
                .is_some_and(|value| value.trim().is_empty())
            {
                let destination = app
                    .path()
                    .app_cache_dir()
                    .map_err(|error| error.to_string())?
                    .join("extension-packages");
                std::fs::create_dir_all(&destination).map_err(|error| error.to_string())?;
                args[1] = Value::String(destination.to_string_lossy().to_string());
            }
            self.call_raw(app, &operation, Value::Array(args))
        } else {
            self.call_raw(app, &operation, payload)
        }
    }
}

#[tauri::command]
fn extension_core_call(
    app: tauri::AppHandle,
    host: tauri::State<ExtensionCoreHost>,
    operation: String,
    payload: Option<serde_json::Value>,
) -> Result<String, String> {
    host.call(&app, operation, payload)
}

#[tauri::command]
fn delete_extension_package(app: tauri::AppHandle, path: String) -> Result<(), String> {
    let cache_root = app
        .path()
        .app_cache_dir()
        .map_err(|error| error.to_string())?;
    let target = PathBuf::from(path);
    if !target.exists() {
        return Ok(());
    }
    let cache_root = cache_root
        .canonicalize()
        .map_err(|error| error.to_string())?;
    let target = target.canonicalize().map_err(|error| error.to_string())?;
    if !target.starts_with(&cache_root) {
        return Err("Refusing to delete a package outside Extension Core cache".to_string());
    }
    std::fs::remove_file(target).map_err(|error| error.to_string())
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .manage(ExtensionCoreHost::default())
        .plugin(tauri_plugin_http::init())
        .plugin(tauri_plugin_shell::init())
        .invoke_handler(tauri::generate_handler![
            save_offline_file,
            delete_offline_file,
            read_offline_file,
            offline_file_exists,
            proxy_http,
            extension_core_call,
            delete_extension_package
        ])
        .setup(|app| {
            if cfg!(debug_assertions) {
                app.handle().plugin(
                    tauri_plugin_log::Builder::default()
                        .level(log::LevelFilter::Info)
                        .build(),
                )?;
            }
            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
