mod common;

use common::HOST;
use localsend::crypto::cert;
use localsend::http::client::LsHttpClientV2;
use localsend::http::server::common::save::FileUploadTarget;
use localsend::http::server::v2::{PrepareUploadDecisionV2, ServerEventV2};
use localsend::http::server::{start_with_port, ServerConfigV2, TlsConfig};
use localsend::http::state::ClientInfo;
use localsend::model::discovery::ProtocolType;
use std::time::Duration;
use tokio::process::Command;
use tokio::sync::{mpsc, oneshot};

const PORT: u16 = 53317;

async fn wait_until_reachable_tls() {
    for _ in 0..100 {
        if tokio::net::TcpStream::connect((HOST, PORT)).await.is_ok() {
            return;
        }
        tokio::time::sleep(Duration::from_millis(20)).await;
    }
    panic!("official LocalSend TLS server did not become reachable");
}

// The Go sender presents its own certificate and pins the server certificate
// learned from /info; the official non-web server requires a client
// certificate. Verifies mTLS interoperability Go-sender -> official receiver.
#[tokio::test]
async fn go_sender_uploads_over_mtls_to_official_tls_server() {
    let directory = tempfile::tempdir().unwrap();
    let source = directory.path().join("mtls-source.bin");
    let expected: Vec<u8> = (0..4096u32).map(|v| (v % 251) as u8).collect();
    tokio::fs::write(&source, &expected).await.unwrap();
    let receive_dir = directory.path().join("received");
    tokio::fs::create_dir(&receive_dir).await.unwrap();

    let identity = cert::generate_self_signed().unwrap();

    let (event_tx, mut event_rx) = mpsc::channel::<ServerEventV2>(16);
    let (uploaded_tx, uploaded_rx) = oneshot::channel::<std::path::PathBuf>();
    let mut uploaded_tx = Some(uploaded_tx);
    let handler_receive_dir = receive_dir.clone();

    tokio::spawn(async move {
        while let Some(event) = event_rx.recv().await {
            match event {
                ServerEventV2::Register { .. } => {}
                ServerEventV2::PrepareUpload {
                    files, decision_tx, ..
                } => {
                    let _ = decision_tx.send(PrepareUploadDecisionV2::Accept(
                        files.keys().cloned().collect(),
                    ));
                }
                ServerEventV2::FileUpload {
                    file_id, target_tx, ..
                } => {
                    let path = handler_receive_dir.join(&file_id);
                    let (result_tx, result_rx) = oneshot::channel();
                    let _ = target_tx.send(FileUploadTarget::Path {
                        path: path.clone(),
                        result_tx,
                        progress_tx: None,
                    });
                    if let Some(done) = uploaded_tx.take() {
                        tokio::spawn(async move {
                            match result_rx.await {
                                Ok(Ok(())) => {
                                    let _ = done.send(path);
                                }
                                result => panic!("official server upload failed: {result:?}"),
                            }
                        });
                    }
                }
                ServerEventV2::SessionEnd { .. }
                | ServerEventV2::PrepareUploadAborted { .. }
                | ServerEventV2::CancelReceived { .. }
                | ServerEventV2::ListenerFailed { .. } => {}
            }
        }
    });

    let (stop_tx, stop_rx) = oneshot::channel::<()>();
    start_with_port(
        PORT,
        Some(TlsConfig {
            cert: identity.certificate_pem.clone(),
            private_key: identity.private_key_pem.clone(),
        }),
        ClientInfo {
            alias: "Official TLS Test Server".to_string(),
            version: "2.2".to_string(),
            device_model: Some("Rust".to_string()),
            device_type: None,
            token: "official-tls-server".to_string(),
        },
        None,
        Some(ServerConfigV2 {
            pin: None,
            verify_checksums: true,
            event_tx,
        }),
        None,
        stop_rx,
    )
    .await
    .unwrap();
    wait_until_reachable_tls().await;

    let binary = std::env::var("LOCALSEND_BIN").expect("LOCALSEND_BIN must point to Go binary");
    let output = Command::new(binary)
        .arg("send")
        .arg("--ip")
        .arg(HOST)
        .arg("--https=true")
        .arg("--devname")
        .arg("GoMtlsSender")
        .arg(&source)
        .output()
        .await
        .unwrap();
    assert!(
        output.status.success(),
        "Go sender failed over mTLS\nstdout: {}\nstderr: {}",
        String::from_utf8_lossy(&output.stdout),
        String::from_utf8_lossy(&output.stderr)
    );

    let uploaded_path = tokio::time::timeout(Duration::from_secs(5), uploaded_rx)
        .await
        .expect("timed out waiting for mTLS upload")
        .expect("upload completion channel closed");
    assert_eq!(tokio::fs::read(uploaded_path).await.unwrap(), expected);
    let _ = stop_tx.send(());
}

// The official Rust client registers with the Go HTTPS receiver using its own
// certificate. The Go receiver pins the announced fingerprint during the TLS
// handshake; a mismatching announced fingerprint must fail the handshake
// before any request bytes are sent.
#[tokio::test]
async fn official_client_registers_over_mtls_with_go_https_receiver() {
    let receive_dir = tempfile::tempdir().unwrap();

    let expected_fingerprint =
        std::env::var("GO_RECEIVER_FINGERPRINT").expect("GO_RECEIVER_FINGERPRINT must be set");
    assert!(
        !expected_fingerprint.is_empty(),
        "GO_RECEIVER_FINGERPRINT must not be empty"
    );

    let binary = std::env::var("LOCALSEND_BIN").expect("LOCALSEND_BIN must point to Go binary");
    let mut command = Command::new(&binary);
    command.args([
        "recv",
        "--https=true",
        "--webrtc=false",
        "--devname",
        "GoHttpsInteropReceiver",
        "--dir",
    ]);
    command.arg(receive_dir.path());
    command.kill_on_drop(true);
    let mut child = command.spawn().expect("failed to start Go receiver");

    for _ in 0..100 {
        if let Some(status) = child.try_wait().unwrap() {
            let _ = child.start_kill();
            panic!("Go HTTPS receiver exited before listening: {status}");
        }
        if tokio::net::TcpStream::connect((HOST, PORT)).await.is_ok() {
            break;
        }
        tokio::time::sleep(Duration::from_millis(20)).await;
    }

    // Official sender identity (client certificate presented as mTLS client).
    let sender = cert::generate_self_signed().unwrap();
    let client = LsHttpClientV2::try_new(
        &sender.private_key_pem,
        &sender.certificate_pem,
        Some(expected_fingerprint.clone()),
        None,
    )
    .unwrap();

    let mut payload = crate::common::sender_info();
    payload.protocol = ProtocolType::Https;
    let registered = client
        .register(ProtocolType::Https, HOST, PORT, payload)
        .await
        .expect("official client failed to register with Go HTTPS receiver");
    assert_eq!(
        registered.cert_fingerprint.as_deref(),
        Some(expected_fingerprint.as_str()),
        "register must expose the pinned certificate fingerprint"
    );
    assert!(!registered.body.alias.is_empty());

    // A wrong announced fingerprint must fail during the handshake.
    let mismatch_client = LsHttpClientV2::try_new(
        &sender.private_key_pem,
        &sender.certificate_pem,
        Some("00".repeat(32).to_uppercase()),
        None,
    )
    .unwrap();
    let mut bad_payload = crate::common::sender_info();
    bad_payload.protocol = ProtocolType::Https;
    let result = mismatch_client
        .register(ProtocolType::Https, HOST, PORT, bad_payload)
        .await;
    assert!(
        result.is_err(),
        "register with a wrong pinned fingerprint must fail"
    );

    let _ = child.start_kill();
    let _ = tokio::time::timeout(Duration::from_secs(5), child.wait()).await;
}
