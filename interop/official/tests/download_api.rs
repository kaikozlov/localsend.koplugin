mod common;

use common::{assert_status, official_client, start_reverse_sender, HOST, PORT};
use localsend::model::discovery::ProtocolType;

async fn create_source_file(directory: &tempfile::TempDir) -> (std::path::PathBuf, Vec<u8>) {
    let path = directory.path().join("official-source.bin");
    let bytes: Vec<u8> = (0..65_537u32).map(|value| (value % 251) as u8).collect();
    tokio::fs::write(&path, &bytes).await.unwrap();
    (path, bytes)
}

// Ported from LocalSend packages/core/tests/v2_web_send.rs at OFFICIAL_LOCALSEND_REF.
#[tokio::test]
async fn official_client_downloads_from_go_reverse_sender() {
    let directory = tempfile::tempdir().unwrap();
    let (source, expected) = create_source_file(&directory).await;
    let process = start_reverse_sender(&source, None).await;
    let client = official_client();

    let response = client
        .prepare_download(ProtocolType::Http, HOST, PORT, None, None)
        .await
        .unwrap();
    assert_eq!(response.info.alias, "GoReverseSender");
    assert!(response.info.download);
    assert_eq!(response.files.len(), 1);
    let (file_id, file) = response.files.iter().next().unwrap();
    assert_eq!(file.file_name, "official-source.bin");

    let download = client
        .download(
            ProtocolType::Http,
            HOST,
            PORT,
            &response.session_id,
            file_id,
        )
        .await
        .unwrap();
    assert_eq!(
        download.bytes().await.unwrap().as_ref(),
        expected.as_slice()
    );

    let reused = client
        .prepare_download(
            ProtocolType::Http,
            HOST,
            PORT,
            Some(&response.session_id),
            None,
        )
        .await
        .unwrap();
    assert_eq!(reused.session_id, response.session_id);

    process.stop().await;
}

#[tokio::test]
async fn go_reverse_sender_exposes_official_info_endpoint() {
    let directory = tempfile::tempdir().unwrap();
    let (source, _) = create_source_file(&directory).await;
    let process = start_reverse_sender(&source, None).await;
    let client = official_client();

    let info = client.info(ProtocolType::Http, HOST, PORT).await.unwrap();
    assert_eq!(info.alias, "GoReverseSender");
    assert!(info.download);

    process.stop().await;
}

#[tokio::test]
async fn go_reverse_sender_exposes_same_download_identity_on_legacy_v1_info() {
    let directory = tempfile::tempdir().unwrap();
    let (source, _) = create_source_file(&directory).await;
    let process = start_reverse_sender(&source, None).await;
    let _client = official_client();

    let legacy: localsend::serde_json::Value =
        localsend::reqwest::get(format!("http://{HOST}:{PORT}/api/localsend/v1/info"))
            .await
            .unwrap()
            .error_for_status()
            .unwrap()
            .json()
            .await
            .unwrap();
    let current: localsend::serde_json::Value =
        localsend::reqwest::get(format!("http://{HOST}:{PORT}/api/localsend/v2/info"))
            .await
            .unwrap()
            .error_for_status()
            .unwrap()
            .json()
            .await
            .unwrap();

    assert_eq!(legacy, current);
    assert_eq!(legacy["alias"], "GoReverseSender");
    assert_eq!(legacy["download"], true);

    process.stop().await;
}

#[tokio::test]
async fn go_reverse_sender_rejects_unknown_session_and_file_as_forbidden() {
    let directory = tempfile::tempdir().unwrap();
    let (source, _) = create_source_file(&directory).await;
    let process = start_reverse_sender(&source, None).await;
    let client = official_client();
    let response = client
        .prepare_download(ProtocolType::Http, HOST, PORT, None, None)
        .await
        .unwrap();
    let file_id = response.files.keys().next().unwrap();

    assert_status(
        client
            .download(ProtocolType::Http, HOST, PORT, "unknown-session", file_id)
            .await,
        403,
    );
    assert_status(
        client
            .download(
                ProtocolType::Http,
                HOST,
                PORT,
                &response.session_id,
                "unknown-file",
            )
            .await,
        403,
    );

    process.stop().await;
}

#[tokio::test]
async fn go_reverse_sender_blocks_after_three_incorrect_pins() {
    let directory = tempfile::tempdir().unwrap();
    let (source, _) = create_source_file(&directory).await;
    let process = start_reverse_sender(&source, Some("123456")).await;
    let client = official_client();

    for _ in 0..3 {
        assert_status(
            client
                .prepare_download(ProtocolType::Http, HOST, PORT, None, Some("000000"))
                .await,
            401,
        );
    }
    assert_status(
        client
            .prepare_download(ProtocolType::Http, HOST, PORT, None, Some("123456"))
            .await,
        429,
    );

    process.stop().await;
}

#[tokio::test]
async fn go_reverse_sender_does_not_count_missing_pin_as_failed_attempt() {
    let directory = tempfile::tempdir().unwrap();
    let (source, _) = create_source_file(&directory).await;
    let process = start_reverse_sender(&source, Some("123456")).await;
    let client = official_client();

    for _ in 0..3 {
        assert_status(
            client
                .prepare_download(ProtocolType::Http, HOST, PORT, None, None)
                .await,
            401,
        );
    }
    client
        .prepare_download(ProtocolType::Http, HOST, PORT, None, Some("123456"))
        .await
        .unwrap();

    process.stop().await;
}

#[tokio::test]
async fn official_client_downloads_with_pin_authenticated_session() {
    let directory = tempfile::tempdir().unwrap();
    let (source, expected) = create_source_file(&directory).await;
    let process = start_reverse_sender(&source, Some("123456")).await;
    let client = official_client();

    let response = client
        .prepare_download(ProtocolType::Http, HOST, PORT, None, Some("123456"))
        .await
        .unwrap();
    let refreshed = client
        .prepare_download(
            ProtocolType::Http,
            HOST,
            PORT,
            Some(&response.session_id),
            None,
        )
        .await
        .unwrap();
    assert_eq!(refreshed.session_id, response.session_id);
    let file_id = response.files.keys().next().unwrap();
    let download = client
        .download(
            ProtocolType::Http,
            HOST,
            PORT,
            &response.session_id,
            file_id,
        )
        .await
        .unwrap();
    assert_eq!(
        download.bytes().await.unwrap().as_ref(),
        expected.as_slice()
    );

    process.stop().await;
}

#[tokio::test]
async fn go_reverse_sender_binds_download_session_to_client_ip() {
    let directory = tempfile::tempdir().unwrap();
    let (source, _) = create_source_file(&directory).await;
    let process = start_reverse_sender(&source, None).await;
    let client = official_client();

    let response = client
        .prepare_download(ProtocolType::Http, HOST, PORT, None, None)
        .await
        .unwrap();
    let file_id = response.files.keys().next().unwrap();
    assert_status(
        client
            .download(
                ProtocolType::Http,
                "::1",
                PORT,
                &response.session_id,
                file_id,
            )
            .await,
        403,
    );

    process.stop().await;
}
