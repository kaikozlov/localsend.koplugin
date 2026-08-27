use localsend::http::server::common::save::FileUploadTarget;
use localsend::http::server::v2::{PrepareUploadDecisionV2, ServerEventV2};
use localsend::http::server::{start_with_port, ServerConfigV2};
use localsend::http::state::ClientInfo;
use std::time::Duration;
use tokio::process::Command;
use tokio::sync::{mpsc, oneshot};

const PORT: u16 = 53317;

async fn wait_until_reachable() {
    for _ in 0..100 {
        if tokio::net::TcpStream::connect(("127.0.0.1", PORT))
            .await
            .is_ok()
        {
            return;
        }
        tokio::time::sleep(Duration::from_millis(20)).await;
    }
    panic!("official LocalSend server did not become reachable");
}

// Ported from LocalSend packages/core/tests/v2_server.rs at OFFICIAL_LOCALSEND_REF,
// with the implementations reversed: the official server receives from our Go sender.
#[tokio::test]
async fn go_sender_uploads_file_to_official_rust_server() {
    let directory = tempfile::tempdir().unwrap();
    let source = directory.path().join("go-sender-source.bin");
    let expected: Vec<u8> = (0..65_537u32).map(|value| (value % 239) as u8).collect();
    tokio::fs::write(&source, &expected).await.unwrap();
    let receive_dir = directory.path().join("received");
    tokio::fs::create_dir(&receive_dir).await.unwrap();

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
        None,
        ClientInfo {
            alias: "Official Rust Test Server".to_string(),
            version: "2.1".to_string(),
            device_model: Some("Rust".to_string()),
            device_type: None,
            token: "official-server-fingerprint".to_string(),
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
    wait_until_reachable().await;

    let binary = std::env::var("LOCALSEND_BIN").expect("LOCALSEND_BIN must point to Go binary");
    let output = Command::new(binary)
        .arg("send")
        .arg("--ip")
        .arg("127.0.0.1")
        .arg("--https=false")
        .arg("--devname")
        .arg("GoInteropSender")
        .arg(&source)
        .output()
        .await
        .unwrap();
    assert!(
        output.status.success(),
        "Go sender failed\nstdout: {}\nstderr: {}",
        String::from_utf8_lossy(&output.stdout),
        String::from_utf8_lossy(&output.stderr)
    );

    let uploaded_path = tokio::time::timeout(Duration::from_secs(5), uploaded_rx)
        .await
        .expect("timed out waiting for upload")
        .expect("upload completion channel closed");
    assert_eq!(tokio::fs::read(uploaded_path).await.unwrap(), expected);
    let _ = stop_tx.send(());
}
