use opennavo_desktop_lib::{appstore::*, model::Settings};
use serde_json::json;

#[test]
fn appstore_commands_are_registered_and_only_main_can_invoke_them() {
    let registration = include_str!("../src/lib.rs");
    let acl = include_str!("../permissions/native.toml");
    let main = acl
        .split("[[permission]]")
        .find(|s| s.contains("identifier = \"native-main\""))
        .unwrap();
    for command in [
        "mas_status",
        "mas_search",
        "mas_lookup",
        "mas_lookup_bundles",
        "mas_installed",
        "mas_updates",
        "mas_open_store",
    ] {
        assert!(registration.contains(&format!("commands::appstore::{command},")));
        assert!(main.contains(&format!("\"{command}\"")));
        for other in acl
            .split("[[permission]]")
            .filter(|s| !s.contains("identifier = \"native-main\""))
        {
            assert!(!other.contains(&format!("\"{command}\"")));
        }
    }
    assert!(registration.contains("events::AppStoreEvent"));
    assert!(include_str!("../src/events.rs").contains("event_name = \"appstore:changed\""));
}

#[test]
fn nullable_fields_and_settings_match_the_frontend_contract() {
    for price in [f64::NAN, f64::INFINITY, -1.0] {
        assert!(MasPrice::try_from(price).is_err());
    }
    let value = json!({"adamId":42,"bundleId":"test.app","name":"App","seller":"Seller","version":"1.0",
        "releaseNotes":null,"releasedAt":null,"minimumOs":null,"sizeBytes":null,"price":0.0,"formattedPrice":"Free",
        "currency":"USD","rating":null,"ratingCount":null,"iconUrl":null,"screenshots":[],"description":"",
        "genres":[],"storeUrl":"https://apps.apple.com/us/app/id42?mt=12","country":"us"});
    let app: MasApp = serde_json::from_value(value.clone()).unwrap();
    assert_eq!(serde_json::to_value(app).unwrap(), value);
    assert_eq!(
        serde_json::to_value(MasBundleLookup::default()).unwrap(),
        json!({})
    );
    let settings: Settings = serde_json::from_value(
        json!({"appStoreRegion":"jp","appStoreSearch":false,"appStoreUpdates":false}),
    )
    .unwrap();
    assert_eq!(settings.app_store_region, "jp");
    assert!(!settings.app_store_search && !settings.app_store_updates);
    let settings = serde_json::to_value(settings).unwrap();
    assert_eq!(settings["appStoreRegion"], "jp");
    assert_eq!(settings["appStoreSearch"], false);
    assert_eq!(settings["appStoreUpdates"], false);
}
