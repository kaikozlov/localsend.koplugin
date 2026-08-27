mod common;

use common::start_receiver;
use localsend::crypto::cert;
use localsend::discovery::{self, DeviceIdentity, DiscoveryConfig};
use localsend::http::server::v2::ServerEventV2;
use localsend::http::server::{start_with_port, ServerConfigV2};
use localsend::http::state::ClientInfo;
use localsend::model::discovery::{DeviceType, ProtocolType, PROTOCOL_VERSION_V2};
use localsend::multicast::{MulticastDevice, DEFAULT_MULTICAST_GROUP, DEFAULT_PORT};
use localsend::util::interface::InterfaceFilter;
use std::time::Duration;
use tokio::sync::{mpsc, oneshot};

const GO_ALIAS: &str = "OfficialInteropReceiver";
const OFFICIAL_ALIAS: &str = "OfficialRustDiscovery";
const OFFICIAL_FINGERPRINT: &str = "official-rust-discovery-fingerprint";

async fn wait_for_official_to_discover_go(handle: &discovery::DiscoveryHandle) {
    tokio::time::timeout(Duration::from_secs(10), async {
        loop {
            if let Some(device) = handle
                .devices()
                .into_iter()
                .find(|device| device.device.alias == GO_ALIAS)
            {
                assert_eq!(device.device.version, PROTOCOL_VERSION_V2);
                assert_eq!(device.device.device_type, Some(DeviceType::Headless));
                assert_eq!(device.device.http().unwrap().port, DEFAULT_PORT);
                return;
            }
            tokio::time::sleep(Duration::from_millis(50)).await;
        }
    })
    .await
    .expect("official Rust discovery never confirmed the Go receiver");
}

async fn wait_for_go_to_register_with_official(event_rx: &mut mpsc::Receiver<ServerEventV2>) {
    tokio::time::timeout(Duration::from_secs(10), async {
        loop {
            match event_rx.recv().await {
                Some(ServerEventV2::Register { info, .. }) if info.alias == GO_ALIAS => {
                    assert_eq!(info.version, PROTOCOL_VERSION_V2);
                    assert_eq!(info.device_type, Some(DeviceType::Headless));
                    assert_eq!(info.port, DEFAULT_PORT);
                    assert_eq!(info.protocol, ProtocolType::Http);
                    return;
                }
                Some(_) => {}
                None => panic!("official server event channel closed before Go registered"),
            }
        }
    })
    .await
    .expect("Go receiver never registered in response to official multicast");
}

// Black-box discovery coverage: both implementations run their production
// multicast + HTTP registration paths in the same network namespace. This
// catches wire-format, socket, announcement, and register-response drift that
// the ordinary HTTP-only interoperability tests cannot see.
#[tokio::test]
async fn go_and_official_rust_discover_each_other_over_multicast() {
    let directory = tempfile::tempdir().unwrap();

    // The Go receiver owns TCP/UDP 53317 just like it does on a real device.
    let go_receiver = start_receiver(directory.path(), None).await;

    // The official HTTP server uses an ephemeral TCP port because its multicast
    // listener still shares UDP 53317 with the Go receiver. Its advertised port
    // is carried in the official multicast message and must be honored by Go.
    let (server_event_tx, mut server_event_rx) = mpsc::channel::<ServerEventV2>(32);
    let (server_stop_tx, server_stop_rx) = oneshot::channel::<()>();
    let official_server = start_with_port(
        0,
        None,
        ClientInfo {
            alias: OFFICIAL_ALIAS.to_string(),
            version: PROTOCOL_VERSION_V2.to_string(),
            device_model: Some("Rust".to_string()),
            device_type: Some(DeviceType::Desktop),
            token: OFFICIAL_FINGERPRINT.to_string(),
        },
        None,
        Some(ServerConfigV2 {
            pin: None,
            verify_checksums: true,
            event_tx: server_event_tx,
        }),
        None,
        server_stop_rx,
    )
    .await
    .unwrap();

    let identity = cert::generate_self_signed().unwrap();
    let (discovery_stop_tx, discovery_stop_rx) = oneshot::channel::<()>();
    let official_discovery = discovery::start(
        DiscoveryConfig {
            group: DEFAULT_MULTICAST_GROUP,
            group_v6: None,
            port: DEFAULT_PORT,
            interface_filter: InterfaceFilter::default(),
            device: MulticastDevice {
                alias: OFFICIAL_ALIAS.to_string(),
                version: PROTOCOL_VERSION_V2.to_string(),
                device_model: Some("Rust".to_string()),
                device_type: Some(DeviceType::Desktop),
                fingerprint: OFFICIAL_FINGERPRINT.to_string(),
                port: official_server.port(),
                protocol: ProtocolType::Http,
                download: false,
            },
            identity: DeviceIdentity {
                cert_pem: identity.certificate_pem,
                private_key_pem: identity.private_key_pem,
            },
            timeout: Duration::from_secs(1),
            event_tx: None,
        },
        discovery_stop_rx,
    )
    .await;

    assert!(
        official_discovery.multicast_error().is_none(),
        "official Rust multicast could not bind alongside Go: {:?}",
        official_discovery.multicast_error()
    );

    // Official -> Go: the production Rust announcement must be parsed by Go,
    // which then POSTs its real registration to the advertised official port.
    official_discovery.announce().await;
    wait_for_go_to_register_with_official(&mut server_event_rx).await;

    // Go -> Official: the receiver advertises immediately and every 3 seconds.
    // The production Rust discoverer must parse it, register with Go, and only
    // then expose the confirmed Go device in its store.
    wait_for_official_to_discover_go(&official_discovery).await;

    let _ = discovery_stop_tx.send(());
    official_discovery.wait_stopped().await;
    let _ = server_stop_tx.send(());
    official_server.wait_stopped().await;
    go_receiver.stop().await;
}
