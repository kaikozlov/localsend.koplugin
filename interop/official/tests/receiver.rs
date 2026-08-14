mod common;

use common::{
    assert_status, file_dto, official_client, prepare_upload_request, sender_info, start_receiver,
    upload_bytes, HOST, PORT,
};
use localsend::model::discovery::ProtocolType;
use tokio_util::sync::CancellationToken;

// Ported from LocalSend packages/core/tests/v2_server.rs at OFFICIAL_LOCALSEND_REF.
#[tokio::test]
async fn official_client_registers_and_reads_go_receiver_info() {
    let receive_dir = tempfile::tempdir().unwrap();
    let process = start_receiver(receive_dir.path(), None).await;
    let client = official_client();

    let registered = client
        .register(ProtocolType::Http, HOST, PORT, sender_info())
        .await
        .unwrap();
    assert_eq!(registered.body.alias, "OfficialInteropReceiver");
    assert_eq!(registered.body.version, "2.2");

    let info = client.info(ProtocolType::Http, HOST, PORT).await.unwrap();
    assert_eq!(info.alias, "OfficialInteropReceiver");
    assert_eq!(info.version, "2.2");
    assert!(!info.download);

    process.stop().await;
}

#[tokio::test]
async fn official_client_registers_with_go_receiver_over_ipv6() {
    let receive_dir = tempfile::tempdir().unwrap();
    let process = start_receiver(receive_dir.path(), None).await;
    let client = official_client();

    let registered = client
        .register(ProtocolType::Http, "::1", PORT, sender_info())
        .await
        .unwrap();
    assert_eq!(registered.body.alias, "OfficialInteropReceiver");

    process.stop().await;
}

#[tokio::test]
async fn official_client_uploads_files_to_go_receiver() {
    let receive_dir = tempfile::tempdir().unwrap();
    let process = start_receiver(receive_dir.path(), None).await;
    let client = official_client();
    let bytes_a: Vec<u8> = (0..100_000u32).map(|value| value as u8).collect();
    let bytes_b = b"official LocalSend client payload".to_vec();
    let files = [
        file_dto("file-a", "a.bin", bytes_a.len() as u64),
        file_dto("file-b", "b.bin", bytes_b.len() as u64),
    ];

    let response = client
        .prepare_upload(
            ProtocolType::Http,
            HOST,
            PORT,
            None,
            prepare_upload_request(&files),
            None,
            CancellationToken::new(),
        )
        .await
        .unwrap()
        .response
        .unwrap();
    assert_eq!(response.files.len(), 2);

    upload_bytes(
        &client,
        &response.session_id,
        "file-a",
        &response.files["file-a"],
        bytes_a.clone(),
    )
    .await
    .unwrap();
    upload_bytes(
        &client,
        &response.session_id,
        "file-b",
        &response.files["file-b"],
        bytes_b.clone(),
    )
    .await
    .unwrap();

    assert_eq!(
        tokio::fs::read(receive_dir.path().join("a.bin"))
            .await
            .unwrap(),
        bytes_a
    );
    assert_eq!(
        tokio::fs::read(receive_dir.path().join("b.bin"))
            .await
            .unwrap(),
        bytes_b
    );
    assert_status(
        upload_bytes(
            &client,
            &response.session_id,
            "file-a",
            &response.files["file-a"],
            b"token replay".to_vec(),
        )
        .await,
        403,
    );

    process.stop().await;
}

#[tokio::test]
async fn official_client_recovers_after_invalid_upload_token() {
    let receive_dir = tempfile::tempdir().unwrap();
    let process = start_receiver(receive_dir.path(), None).await;
    let client = official_client();
    let file = file_dto("invalid-token-file", "invalid.bin", 5);

    let response = client
        .prepare_upload(
            ProtocolType::Http,
            HOST,
            PORT,
            None,
            prepare_upload_request(&[file]),
            None,
            CancellationToken::new(),
        )
        .await
        .unwrap()
        .response
        .unwrap();
    assert_status(
        upload_bytes(
            &client,
            &response.session_id,
            "invalid-token-file",
            "wrong-token",
            b"hello".to_vec(),
        )
        .await,
        403,
    );
    upload_bytes(
        &client,
        &response.session_id,
        "invalid-token-file",
        &response.files["invalid-token-file"],
        b"hello".to_vec(),
    )
    .await
    .unwrap();
    assert_eq!(
        tokio::fs::read(receive_dir.path().join("invalid.bin"))
            .await
            .unwrap(),
        b"hello"
    );

    process.stop().await;
}

#[tokio::test]
async fn official_client_cancels_go_receiver_session() {
    let receive_dir = tempfile::tempdir().unwrap();
    let process = start_receiver(receive_dir.path(), None).await;
    let client = official_client();
    let file = file_dto("active-file", "active.bin", 5);

    let active = client
        .prepare_upload(
            ProtocolType::Http,
            HOST,
            PORT,
            None,
            prepare_upload_request(std::slice::from_ref(&file)),
            None,
            CancellationToken::new(),
        )
        .await
        .unwrap()
        .response
        .unwrap();
    assert_status(
        client
            .prepare_upload(
                ProtocolType::Http,
                HOST,
                PORT,
                None,
                prepare_upload_request(std::slice::from_ref(&file)),
                None,
                CancellationToken::new(),
            )
            .await,
        409,
    );
    client
        .cancel(ProtocolType::Http, HOST, PORT, &active.session_id)
        .await
        .unwrap();
    let after_cancel = client
        .prepare_upload(
            ProtocolType::Http,
            HOST,
            PORT,
            None,
            prepare_upload_request(&[file]),
            None,
            CancellationToken::new(),
        )
        .await
        .unwrap()
        .response
        .unwrap();
    client
        .cancel(ProtocolType::Http, HOST, PORT, &after_cancel.session_id)
        .await
        .unwrap();

    process.stop().await;
}

#[tokio::test]
async fn go_receiver_rejects_upload_with_missing_parameters() {
    let receive_dir = tempfile::tempdir().unwrap();
    let process = start_receiver(receive_dir.path(), None).await;
    // Initializing the official client installs the rustls crypto provider used
    // by reqwest, even though this malformed request is sent manually.
    let _client = official_client();

    let response = localsend::reqwest::Client::new()
        .post(format!(
            "http://{HOST}:{PORT}/api/localsend/v2/upload?sessionId=missing"
        ))
        .body("data")
        .send()
        .await
        .unwrap();
    assert_eq!(response.status().as_u16(), 400);

    process.stop().await;
}

#[tokio::test]
async fn official_client_authenticates_to_go_receiver_with_pin() {
    let receive_dir = tempfile::tempdir().unwrap();
    let process = start_receiver(receive_dir.path(), Some("123456")).await;
    let client = official_client();
    let file = file_dto("pin-file", "pin.bin", 5);

    assert_status(
        client
            .prepare_upload(
                ProtocolType::Http,
                HOST,
                PORT,
                None,
                prepare_upload_request(std::slice::from_ref(&file)),
                Some("000000"),
                CancellationToken::new(),
            )
            .await,
        401,
    );
    let response = client
        .prepare_upload(
            ProtocolType::Http,
            HOST,
            PORT,
            None,
            prepare_upload_request(&[file]),
            Some("123456"),
            CancellationToken::new(),
        )
        .await
        .unwrap()
        .response
        .unwrap();
    client
        .cancel(ProtocolType::Http, HOST, PORT, &response.session_id)
        .await
        .unwrap();

    process.stop().await;
}

#[tokio::test]
async fn go_receiver_blocks_after_three_incorrect_pins() {
    let receive_dir = tempfile::tempdir().unwrap();
    let process = start_receiver(receive_dir.path(), Some("123456")).await;
    let client = official_client();
    let file = file_dto("pin-file", "pin.bin", 5);

    for _ in 0..3 {
        assert_status(
            client
                .prepare_upload(
                    ProtocolType::Http,
                    HOST,
                    PORT,
                    None,
                    prepare_upload_request(std::slice::from_ref(&file)),
                    Some("000000"),
                    CancellationToken::new(),
                )
                .await,
            401,
        );
    }
    assert_status(
        client
            .prepare_upload(
                ProtocolType::Http,
                HOST,
                PORT,
                None,
                prepare_upload_request(&[file]),
                Some("123456"),
                CancellationToken::new(),
            )
            .await,
        429,
    );

    process.stop().await;
}

#[tokio::test]
async fn go_receiver_does_not_count_missing_pin_as_failed_attempt() {
    let receive_dir = tempfile::tempdir().unwrap();
    let process = start_receiver(receive_dir.path(), Some("123456")).await;
    let client = official_client();
    let file = file_dto("pin-file", "pin.bin", 5);

    for _ in 0..3 {
        assert_status(
            client
                .prepare_upload(
                    ProtocolType::Http,
                    HOST,
                    PORT,
                    None,
                    prepare_upload_request(std::slice::from_ref(&file)),
                    None,
                    CancellationToken::new(),
                )
                .await,
            401,
        );
    }
    client
        .prepare_upload(
            ProtocolType::Http,
            HOST,
            PORT,
            None,
            prepare_upload_request(&[file]),
            Some("123456"),
            CancellationToken::new(),
        )
        .await
        .unwrap();

    process.stop().await;
}
