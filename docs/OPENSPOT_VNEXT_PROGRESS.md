# OpenSpot vNext Progress

Branch: `feature/openspot-vnext-extension-automix`
Master plan: `docs/OPENSPOT_VNEXT_MASTER_PLAN.md`

## Current status

- [x] Branch created from `main`
- [x] Approved master plan persisted in repository
- [x] Android/iOS/Desktop release workflows remain manual-only; final unsigned iOS IPA dispatch is authorized for this task
- [x] Mobile architecture audit started
- [x] Desktop/Tauri architecture audit started
- [x] Mobile ProviderRegistry foundation added
- [x] Desktop ProviderRegistry foundation added
- [x] Track provider identifiers widened from fixed Saavn/YouTube union to extension-compatible string IDs on mobile and desktop
- [x] Mobile MusicAPI routed through ProviderRegistry for search, stream and download resolution
- [x] Desktop MusicAPI routed through ProviderRegistry for search, stream and download resolution
- [x] P0 full baseline audit complete
- [~] P1 playback runtime recovery — runtime lifecycle/retry diagnostics centralized; background/audio-focus and live takeover QA pending
- [x] P2 provider core complete
- [~] P3 SpotiFLAC Extension Core direct port — pinned core boundary and native host bridges implemented; device/package verification pending
- [~] P4 `.sflx` compatibility — package validation/extraction ported, host integration pending
- [~] P5 manifest/permission parity — manifest/permission/runtime HTTP/file sandbox foundations ported, host integration pending
- [~] P6 extension repository/store — registry/cache/download APIs, package lifecycle bridge and desktop Go sidecar are implemented; live repository verification pending
- [~] P7 extension UI — mobile/desktop store, detail, install/update/remove and health controls are wired; device verification pending
- [~] P8 provider priority/fallback — provider-neutral priority, metadata aggregation and fallback resolution are wired; live extension provider verification pending
- [~] P9 first-party providers migrated — built-ins remain behind the neutral registry and installed extension adapters are wired; provider migration/live verification pending
- [x] P10 Search bottom tab replaced with Mix
- [~] P11-P19 SimpMusic AutoMix engine parity — planner, equal-power transition, filter/ramp contracts, settings, overlapping mobile/desktop players and takeover are wired; device/DSP edge-case QA pending
- [~] P20-P21 Mix UI/settings — mobile/desktop controls and persistence added; live playback binding pending
- [~] P22 Vietnamese mobile + desktop — locale registration and primary-screen translation added; remaining legacy update/footer copy is still being migrated
- [~] P23-P27 settings/search/cache/diagnostics integration — cache clearing, AutoMix metadata TTL cache and structured provider/playback/AutoMix diagnostics are integrated; remaining settings/search/runtime wiring pending
- [~] P28-P30 parity tests and cross-platform QA — deterministic parity checks, Go/Rust/static checks and sidecar smoke checks are green; runtime device/audio-focus/long-queue QA remains pending

## Latest local verification (2026-09-12)

- extension_manager.go matches the staged SpotiFLAC-Mobile source exactly after normalizing line endings; no extsions/extsionsDir typo remains.
- Go gofmt, vet and test ./... -count=1 pass, including the new cmd/extension-core-host package.
- Rust cargo check --locked and rustfmt --edition 2021 --check src/lib.rs passed in the prior sidecar smoke environment; the current Windows checkout intentionally does not track the generated target-named sidecar, so a local Tauri package check still requires the documented Go sidecar build step.
- Sidecar smoke test passes JSON request/response and creates a persistent 32-byte storage key.
- Locale/config JSON checks and git diff --check pass. TypeScript dependency installation remains unavailable locally because the npm registry response is not cached; no GitHub Action was spent for that check.
- The iOS crash reports supplied from build 37 all terminate in `com.facebook.react.ExceptionsManagerQueue` with `EXC_CRASH/SIGABRT`; the matching raw JSX whitespace nodes were removed from Mix and both Library branches, and the mobile whitespace guard passes.
- Mobile Extension Core host paths now convert Expo `file://` URIs to native filesystem paths before Go initialization, repository downloads, package loading and cleanup. Provider adapters are re-synced after the native core is initialized or an extension changes state.
- Saavn requests now use the configured endpoint with the standard JioSaavn endpoint as a fallback, proxy all operations including `song.getDetails`, and preserve actionable network errors. A live probe returned valid search and song-detail payloads through the proxy.
- Mobile Extension tabs now use a two-column wrapped layout on narrow screens, and Settings Extension/Mix action buttons have a non-shrinking inline layout so their labels and chevrons remain visible.
- Search and stream resolution caches on mobile and desktop now use five-minute TTLs and evict rejected promises, preventing stale provider failures from being retained indefinitely.

## SpotiFLAC Extension Core

Pinned upstream source: `todo996/SpotiFLAC-Mobile@8165fc99f18234049d003ec8379dea7792c452a1`
Vendor location: `third_party/spotiflac-extension-core/upstream/`

Ported engine/runtime coverage includes:

- MIT license/source attribution and pinned-source notes
- manifest, transfer policy, secret key and encrypted settings
- `.sflx` / `.spotiflac-ext` package validation and extraction
- manager runtime/package lifecycle
- Goja runtime, HTTP, storage, file, binary, auth, polyfills and utility APIs
- transfer, chunked transfer and segment handling
- signed-session support
- provider contracts/wrappers and metadata-provider aggregation
- OpenSpot Core-scoped download-provider ordering/fallback coordinator with availability, verification and cancellation handling
- OpenSpot host bridge parity for auth challenges/tokens, FFmpeg command pumping, URL handling and post-processing V2
- Extension repository registry/cache/search/category/download APIs with HTTPS and SHA-256 package verification
- Extension health checks with service-key classification, TTL cache and asynchronous refresh support
- metadata/search matching and cross-extension collection sharing
- download-quality contract used by extension providers
- resolution budget, timeout and performance helpers
- provider priority/fallback configuration

### Compile history and root-cause correction

Run #18 passed `go mod tidy`, `gofmt`, `go vet` and `go test` for the internal Extension Engine snapshot.

Subsequent failures were caused by widening the vendor boundary incorrectly: SpotiFLAC application facade files such as `exports_download.go`, `extension_fallback.go`, `extension_fallback_helpers.go` and the full `exports_extensions.go` were imported into the Extension Core. Those facades transitively depend on the rest of the SpotiFLAC application (SongLink, built-in Deezer metadata, lyrics cache/providers, output naming, metadata embedding and download orchestration). This produced the repeated undefined-symbol chain seen after Run #18.

The boundary has now been corrected:

- application-level download facade/fallback files were removed from the compiled Extension Core;
- the extension-facing `DownloadRequest` contract is kept without importing the SpotiFLAC application download pipeline;
- an OpenSpot-specific `openspot_extension_bridge.go` exposes manager/settings/provider APIs while preserving SpotiFLAC provider response shapes;
- the bridge does not call built-in Deezer, SongLink, lyrics cache or SpotiFLAC metadata-embedding/download orchestration;
- provider priority is generic and no longer branches on Deezer/Qobuz/Tidal/Spotify IDs;
- verification-required error recognition is isolated in a small compatibility helper instead of importing the app-level download error classifier;
- the manual Go workflow now contains an Extension boundary guard that rejects accidental reintroduction of application-level dependencies before compilation.

Temporary diagnostic workflows used while locating the dependency leak were removed. The remaining core workflow is `workflow_dispatch` only; further checks are manual.

## Audit findings so far

1. Mobile Android/iOS share the Expo/React Native project and are generated via Expo prebuild in the manual build workflows.
2. Desktop uses a React/Expo web shell packaged with Tauri 2 and Rust `src-tauri`, so playback/extension native adapters must be platform-specific while exposing the same higher-level contracts.
3. Existing `MusicAPI` hard-coded Saavn/YouTube selection. This is being replaced by a provider-neutral registry before the Extension runtime is connected.
4. Mobile and desktop Track models originally restricted `provider` to `'saavn' | 'ytmusic'`; this blocked extension provider IDs and has been widened to `string`.
5. ProviderRegistry supports registration, dynamic IDs, priority persistence, search fallback, stream/download fallback and equivalent-track matching across providers. It is transitional host infrastructure for the Extension Provider bridge.
6. Playback components currently own TrackPlayer setup/reset behavior. Player lifecycle remains a P1 target so the UI does not own long-lived playback state.
7. Saavn API construction currently depends directly on `EXPO_PUBLIC_API_BASE_URL`; environment/fallback validation remains a P1 hardening item.

## Current work block

1. [x] Run the manual Extension Core boundary/gofmt/vet/test check on the current branch.
2. [~] Finish Vietnamese primary-flow coverage and static parity checks.
3. [~] Wire the native Extension Core runtime and live AutoMix player/DSP adapters where the existing platform contracts allow it — desktop sidecar and Android/iOS module paths are now present; device/runtime QA remains.
4. [x] Run the manual cross-platform check workflow, fix every actionable failure, and repeat until green.
5. [~] Push the verified feature commit, dispatch the manual unsigned iOS IPA workflow once, and verify the uploaded artifact on the final commit.

## Rules

- Do not merge to `main` until explicitly approved.
- Do not trigger GitHub Actions implicitly during ordinary edits; this task explicitly authorizes the final manual check and unsigned IPA workflows.
- SpotiFLAC Extension behavior/contracts are the direct-port source of truth; OpenSpot-specific host adaptation stays outside app-level SpotiFLAC dependencies.
- SimpMusic AutoMix behavior is the direct-port source of truth, subject to the GPL release/license gate.
