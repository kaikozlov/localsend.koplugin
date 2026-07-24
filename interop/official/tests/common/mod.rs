#![allow(dead_code)]

use localsend::http::client::{ClientError, LsHttpClientV2};
use localsend::http::dto::ProtocolType;
use localsend::http::dto_v2::{PrepareUploadRequestDtoV2, ProtocolTypeV2, RegisterDtoV2};
use localsend::model::transfer::FileDto;
use std::process::Stdio;
use std::time::Duration;
use tokio::process::{Child, Command};
use tokio_util::sync::CancellationToken;

pub const HOST: &str = "127.0.0.1";
pub const PORT: u16 = 53317;

pub struct TestProcess {
    child: Child,
}

impl TestProcess {
    pub async fn stop(mut self) {
        let _ = self.child.start_kill();
        let _ = tokio::time::timeout(Duration::from_secs(5), self.child.wait()).await;
    }
}

impl Drop for TestProcess {
    fn drop(&mut self) {
        let _ = self.child.start_kill();
    }
}

fn localsend_command() -> Command {
    let binary = std::env::var("LOCALSEND_BIN").expect("LOCALSEND_BIN must point to the Go binary");
    let mut command = Command::new(binary);
    command
        .kill_on_drop(true)
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null());
    command
}

async fn wait_until_reachable(child: &mut Child) {
    for _ in 0..100 {
        if let Some(status) = child
            .try_wait()
            .expect("failed to inspect LocalSend process")
        {
            panic!("LocalSend process exited before listening: {status}");
        }
        if tokio::net::TcpStream::connect((HOST, PORT)).await.is_ok() {
            return;
        }
        tokio::time::sleep(Duration::from_millis(20)).await;
    }
    panic!("LocalSend process did not listen on {HOST}:{PORT}");
}

pub async fn start_receiver(save_dir: &std::path::Path, pin: Option<&str>) -> TestProcess {
    let mut command = localsend_command();
    command.args([
        "recv",
        "--https=false",
        "--webrtc=false",
        "--devname",
        "OfficialInteropReceiver",
        "--dir",
    ]);
    command.arg(save_dir);
    if let Some(pin) = pin {
        command.args(["--pin", pin]);
    }
    let mut child = command.spawn().expect("failed to start Go receiver");
    wait_until_reachable(&mut child).await;
    TestProcess { child }
}

pub async fn start_reverse_sender(file: &std::path::Path, pin: Option<&str>) -> TestProcess {
    let mut command = localsend_command();
    command.args([
        "send",
        "--dapi",
        "--https=false",
        "--devname",
        "GoReverseSender",
    ]);
    if let Some(pin) = pin {
        command.args(["--pin", pin]);
    }
    command.arg(file);
    let mut child = command.spawn().expect("failed to start Go reverse sender");
    wait_until_reachable(&mut child).await;
    TestProcess { child }
}

pub fn official_client() -> LsHttpClientV2 {
    LsHttpClientV2::try_new_without_cert().expect("failed to create official LocalSend client")
}

pub fn sender_info() -> RegisterDtoV2 {
    RegisterDtoV2 {
        alias: "Official Rust Test Sender".to_string(),
        version: "2.1".to_string(),
        device_model: Some("Rust".to_string()),
        device_type: None,
        fingerprint: "official-test-fingerprint".to_string(),
        port: PORT,
        protocol: ProtocolTypeV2::Http,
        download: false,
    }
}

pub fn file_dto(id: &str, name: &str, size: u64) -> FileDto {
    FileDto {
        id: id.to_string(),
        file_name: name.to_string(),
        size,
        file_type: "application/octet-stream".to_string(),
        sha256: None,
        preview: None,
        metadata: None,
    }
}

pub fn prepare_upload_request(files: &[FileDto]) -> PrepareUploadRequestDtoV2 {
    PrepareUploadRequestDtoV2 {
        info: sender_info(),
        files: files
            .iter()
            .map(|file| (file.id.clone(), file.clone()))
            .collect(),
    }
}

pub async fn upload_bytes(
    client: &LsHttpClientV2,
    session_id: &str,
    file_id: &str,
    token: &str,
    bytes: Vec<u8>,
) -> Result<(), ClientError> {
    client
        .upload(
            ProtocolType::Http,
            HOST,
            PORT,
            None,
            session_id,
            file_id,
            token,
            localsend::reqwest::Body::from(bytes),
            CancellationToken::new(),
        )
        .await
}

pub fn assert_status<T>(result: Result<T, ClientError>, expected: u16) {
    match result {
        Err(ClientError::StatusCode(error)) => assert_eq!(error.status, expected),
        Err(error) => panic!("expected status {expected}, got {error:?}"),
        Ok(_) => panic!("expected status {expected}, got success"),
    }
}
