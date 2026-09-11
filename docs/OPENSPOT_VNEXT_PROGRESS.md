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
- [ ] P3 SpotiFLAC Extension Core direct port
- [ ] P4 `.sflx` compatibility
- [ ] P5 manifest/permission parity
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

## Audit findings so far

1. Mobile Android/iOS share the Expo/React Native project and are generated via Expo prebuild in the manual build workflows.
2. Desktop uses a React/Expo web shell packaged with Tauri 2 and Rust `src-tauri`, so playback/extension native adapters must be platform-specific while exposing the same higher-level contracts.
3. Existing `MusicAPI` hard-coded Saavn/YouTube selection. This is being replaced by a provider-neutral registry before the SpotiFLAC runtime is connected.
4. Mobile and desktop Track models originally restricted `provider` to `'saavn' | 'ytmusic'`; this blocked extension provider IDs and has been widened to `string`.
5. ProviderRegistry now supports registration, dynamic IDs, priority persistence, search fallback, stream/download fallback and equivalent-track matching across providers. This is transitional infrastructure that the SpotiFLAC Extension Provider host will plug into.
6. Playback components currently own TrackPlayer setup/reset behavior. Player lifecycle is the next P1 target so the UI does not own long-lived playback state.
7. Saavn API construction currently depends directly on `EXPO_PUBLIC_API_BASE_URL`; environment/fallback validation will be hardened during P1.

## Next work block

1. Centralize mobile TrackPlayer runtime lifecycle and diagnostics.
2. Remove UI-layer provider-disable hard stops that prevent registry fallback.
3. Apply equivalent playback lifecycle changes to Desktop/Tauri adapter.
4. Finish P0/P1 checks before importing the SpotiFLAC Go extension runtime.
