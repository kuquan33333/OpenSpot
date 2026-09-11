# OpenSpot vNext Progress

Branch: `feature/openspot-vnext-extension-automix`
Master plan: `docs/OPENSPOT_VNEXT_MASTER_PLAN.md`

## Current status

- [x] Branch created from `main`
- [x] Approved master plan persisted in repository
- [x] Workflow audit: Android/iOS/Desktop build workflows are manual-dispatch oriented; no workflow has been triggered by this work
- [x] Mobile architecture audit started
- [x] Desktop/Tauri architecture audit started
- [x] Mobile ProviderRegistry foundation added
- [x] Desktop ProviderRegistry foundation added
- [x] Track provider identifiers widened from fixed Saavn/YouTube union to extension-compatible string IDs on mobile and desktop
- [x] Mobile MusicAPI routed through ProviderRegistry for search, stream and download resolution
- [x] Desktop MusicAPI routed through ProviderRegistry for search, stream and download resolution
- [ ] P0 full baseline audit complete
- [ ] P1 playback runtime recovery complete
- [ ] P2 provider core complete
- [~] P3 SpotiFLAC Extension Core direct port — active
- [~] P4 `.sflx` compatibility — package validation/extraction direct-ported, host integration pending
- [~] P5 manifest/permission parity — manifest/permission/runtime HTTP/file sandbox foundations direct-ported, compile/integration pending
- [ ] P6 extension repository/store
- [ ] P7 extension UI
- [ ] P8 provider priority/fallback full integration
- [ ] P9 first-party providers migrated
- [ ] P10 Search bottom tab replaced with Mix
- [ ] P11-P19 SimpMusic AutoMix engine parity
- [ ] P20-P21 Mix UI/settings
- [ ] P22 Vietnamese mobile + desktop
- [ ] P23-P27 settings/search/cache/diagnostics integration
- [ ] P28-P30 parity tests and cross-platform QA

## SpotiFLAC Extension Core direct-port progress

Pinned upstream source: `todo996/SpotiFLAC-Mobile@8165fc99f18234049d003ec8379dea7792c452a1`
Vendor location: `third_party/spotiflac-extension-core/upstream/`

Direct-ported so far:

- MIT license/source attribution and pinned-source notes
- `extension_manifest.go`
- `extension_transfer_policy.go`
- `extension_secret_key.go`
- `extension_settings.go`
- `extension_priority.go`
- `extension_manager_package.go`
- `extension_download_quality.go`
- `extension_resolution_budget.go`
- `extension_timeout.go`
- `extension_perf.go`
- `extension_manager_runtime.go`
- `extension_runtime.go`
- `extension_runtime_http.go`
- `extension_runtime_matching.go`
- `extension_runtime_storage.go`
- `extension_runtime_polyfills.go`
- `extension_runtime_utils.go`
- `extension_runtime_auth.go`
- `extension_runtime_file.go`
- `extension_runtime_binary.go`

Important: the vendored snapshot is intentionally not marked complete yet. It still needs the main manager, transfer/chunk/segment helpers, provider/fallback wrappers, repository/health, signed-session and shared host dependencies before the Go core can be compiled and bridged into OpenSpot.

## Audit findings so far

1. Mobile Android/iOS share the Expo/React Native project and are generated via Expo prebuild in the manual build workflows.
2. Desktop uses a React/Expo web shell packaged with Tauri 2 and Rust `src-tauri`, so playback/extension native adapters must be platform-specific while exposing the same higher-level contracts.
3. Existing `MusicAPI` hard-coded Saavn/YouTube selection. This is being replaced by a provider-neutral registry before the SpotiFLAC runtime is connected.
4. Mobile and desktop Track models originally restricted `provider` to `'saavn' | 'ytmusic'`; this blocked extension provider IDs and has been widened to `string`.
5. ProviderRegistry now supports registration, dynamic IDs, priority persistence, search fallback, stream/download fallback and equivalent-track matching across providers. This is transitional infrastructure that the SpotiFLAC Extension Provider host will plug into.
6. Playback components currently own TrackPlayer setup/reset behavior. Player lifecycle remains a P1 target so the UI does not own long-lived playback state.
7. Saavn API construction currently depends directly on `EXPO_PUBLIC_API_BASE_URL`; environment/fallback validation remains a P1 hardening item.

## Current work block

1. Complete SpotiFLAC transfer/chunk/segment runtime dependency closure.
2. Direct-port the main Extension Manager and provider/fallback wrapper layer.
3. Direct-port repository/health/signed-session pieces.
4. Establish a compilable Go module boundary and replace SpotiFLAC app-specific shared helpers with OpenSpot host adapters only where required.
5. Build the Android/iOS Expo native bridge and Desktop/Tauri bridge against the same Go core.
6. Recreate the SpotiFLAC Extensions settings/store/detail UI in OpenSpot styling without changing feature behavior.
7. Then continue the SimpMusic AutoMix direct-port work.

## Rules

- Do not merge to `main` until explicitly approved.
- Do not trigger GitHub Actions automatically.
- SpotiFLAC extension behavior/contracts are the direct-port source of truth.
- SimpMusic AutoMix behavior is the direct-port source of truth, subject to the GPL release/license gate.
