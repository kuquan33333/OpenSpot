# OpenSpot vNext Master Plan

Status: APPROVED SOURCE OF TRUTH
Branch: `feature/openspot-vnext-extension-automix`
Platforms: Android, iOS, Desktop/PC

## Product goal
OpenSpot remains the primary product shell/UI. Two major subsystems are ported from their source projects and adapted to OpenSpot architecture:

1. **SpotiFLAC Extension System** -> direct port of runtime logic, manifest contract, `.sflx` package format, repository/store, provider priority/fallback, permissions, health, settings, install/update/remove and extension detail behavior.
2. **SimpMusic AutoMix** -> direct port of AutoMix/DJ transition behavior and algorithms: multi-player/precache, equal-power crossfade, DJ filters, BPM matching, Camelot harmonic matching, automatic transition duration, beat quantization, speed/pitch ramps, retry/cancel/takeover behavior and same-album handling.

OpenSpot UI remains visually consistent with OpenSpot, while the Extension and AutoMix features preserve the source features' recognizable iconography, settings structure, controls and functional layout.

## Final navigation
`Home | Mix | Library | Downloads | Settings`

Search remains available from the header/search action; it is removed only from the bottom tab bar.

## Non-negotiable implementation rules
- Do not build a simplified "similar" extension framework. Port SpotiFLAC behavior and contracts.
- Do not build AutoMix as a single-player volume fade. Port the multi-player/precache transition model.
- Do not hard-code provider IDs into playback logic when capability/priority/fallback can drive behavior.
- Keep `.sflx` compatibility and legacy `.spotiflac-ext` support.
- Preserve extension permission boundaries, repository package integrity checks and health/fallback behavior.
- Preserve SimpMusic AutoMix formulas/constants/transition behavior with parity tests.
- UI may be restyled to OpenSpot, but the Extension and AutoMix settings/function organization and feature identity must remain recognizable from the source apps.
- Apply the upgrade across Android, iOS and Desktop/PC; platform engines/adapters may differ but behavior should be aligned.

## Phases

### P0 — Baseline and repository audit
- Map mobile navigation, player lifecycle, queue, provider resolution, downloads, settings, i18n and persistence.
- Map desktop architecture/player/settings/provider flow.
- Identify Android/iOS native build surfaces and Expo prebuild requirements.
- Record current build/lint/test commands and known failures.

### P1 — Playback recovery/foundation
- Move player lifecycle out of screen/component ownership.
- Centralize `PlaybackController` / player service.
- Handle URL expiry, 401/403/404, timeout, decoder/network errors, background/foreground and audio focus.
- Add bounded retry and diagnostic events.
- Require real single-track playback, seek, pause/resume, next/previous and queue continuation before AutoMix work.

### P2 — Provider Core
- Introduce provider-neutral registry/resolver interfaces.
- Remove direct player dependence on hard-coded Saavn/YouTube settings.
- Normalize search/playback/download metadata into shared OpenSpot track/provider contracts.

### P3 — SpotiFLAC Extension Core port
- Port Go extension runtime/backend modules required for manager, manifest, runtime, HTTP, storage/file access, repo, priority, fallback, health, providers and settings.
- Build a React Native bridge for Android/iOS rather than rewriting runtime semantics in TypeScript.
- For Desktop, expose equivalent host bindings around the same contract/runtime where feasible.

### P4 — `.sflx` package compatibility
- Preserve root `manifest.json` + `index.js` contract.
- Accept `.sflx` and `.spotiflac-ext`.
- Preserve validation against traversal, symlinks, duplicates, oversized manifests/extractions and invalid packages.

### P5 — Manifest / permissions parity
- Preserve `name`, `displayName`, `version`, `description`, `type`, `permissions`, `settings`, `qualityOptions`, `searchBehavior`, `urlHandler`, `trackMatching`, `postProcessing`, `serviceHealth`, `signedSession`, `requiredRuntimeFeatures`, `capabilities`, etc.
- Preserve network allowlist, storage/file permissions and HTTPS/allowHttp rules.
- Add streaming capability only in a backward-compatible way.

### P6 — Extension repository/store
- Port repository URL onboarding, registry refresh, search, category filters, extension detail, install/update and SHA-256 verification behavior.
- OpenSpot location: `Settings > Extensions > Extension Store`.

### P7 — Extension management UI
- Add a dedicated Extensions row in Settings using recognizable extension iconography from SpotiFLAC, adapted to OpenSpot theme.
- Pages/sections: Store, Provider Priority, Fallback, Search Provider, Home Provider, Lyrics, Downloads, Installed Extensions.
- Installed rows show icon, display name, version, health and enable switch.
- Detail page keeps settings, permissions, capabilities, health, quality options, URL handler, update/remove.

### P8 — Provider priority/fallback integration
- Playback resolves through provider priority and fallback instead of provider-specific branches.
- A failing source must fall through to the next eligible source without crashing playback.

### P9 — First-party providers
- Integrate YouTube/YouTube Music, SoundCloud and Saavn behind the provider registry.
- Keep built-ins during migration where necessary, then allow extension-based operation.

### P10 — Bottom navigation change
- Replace bottom Search tab with Mix.
- Keep Search route and expose it from header/search actions.
- Keep `Home | Mix | Library | Downloads | Settings` on mobile; desktop navigation should expose equivalent Mix and Extension destinations.

### P11 — SimpMusic AutoMix engine architecture
- Port the internal state model (`IDLE/PREPARING/READY/PLAYING/PAUSED/ENDED/ERROR`).
- Use current + secondary player and precached next tracks.
- Maintain a stable external playback/session interface while internal player instances swap.

### P12 — Equal-power crossfade parity
- Use `angle = progress * PI/2`.
- Outgoing gain = `cos(angle)`; incoming gain = `sin(angle)`.
- Preserve smooth stepped transition behavior and cancellation/takeover semantics.

### P13 — DJ filter parity
- Outgoing low-pass sweep: 20,000 Hz -> 200 Hz.
- Incoming high-pass sweep: 2,000 Hz -> 20 Hz.
- Preserve sigmoid/exponential interpolation behavior.

### P14 — Auto duration parity
- Fallback 30,000 ms when BPM metadata is missing.
- Clamp automatic transition to 20,000–45,000 ms.
- Beat candidates: 8,16,24,32,40,48,64,80,96; default 32.
- Base duration and BPM/key gap factors follow source behavior and are beat-quantized.

### P15 — BPM matching parity
- Normalize half-time/double-time ratios.
- Apply tempo matching only within 0.75–1.25 safe ratio.
- Preserve quantization/stepping behavior used to reduce processor artifacts.

### P16 — Camelot/harmonic parity
- Convert musical keys to Camelot representation.
- Preserve key-distance duration factors.
- Try +/-1 then +/-2 semitone pitch adjustments and choose the smallest shift that reaches harmonic compatibility; otherwise no shift.

### P17 — Speed/pitch ramp parity
- Preserve smoothstep-style ramping during transition rather than instantaneous changes.
- Return tracks to natural speed/pitch after the mix.

### P18 — AutoMix metadata pipeline
- Unified `SongAudioMeta` equivalent with BPM/key/keyScale.
- Provider metadata -> persistent/in-memory cache -> local analysis fallback.
- Missing metadata must fall back to safe Auto crossfade rather than fail playback.

### P19 — Transition edge cases
- Handle next/previous, seek, pause, queue reorder/removal, repeat, shuffle, source expiry, not-ready next player, connectivity changes, Bluetooth/headphones, audio focus, calls, background and short tracks.
- Never create avoidable silence if the next track is not ready.

### P20 — Mix screen
- Preserve recognizable AutoMix icon/function identity from SimpMusic while adapting visual styling to OpenSpot.
- Show current track -> next track, artwork, BPM, Camelot/key, transition duration/beats, preload/mix state and queue.
- Controls: AutoMix, DJ mode, BPM match, harmonic match.

### P21 — Mix settings
- Modes: Off / Crossfade / AutoMix.
- Duration: Auto / 5 / 10 / 15 / 20 / 30 / 45 sec.
- DJ Filter, BPM Matching, Harmonic Matching, Skip Same Album, BPM/Key display.
- Preserve source feature organization, adapted to OpenSpot Settings design.

### P22 — Vietnamese localization
- Add complete `vi` locale across mobile and desktop.
- Remove user-visible English hard-coding in primary flows.
- Use natural Vietnamese; keep technical names such as AutoMix/BPM/Camelot where appropriate.

### P23 — Settings refactor
- Split monolithic settings into Appearance, Language, Playback, Mix, Extensions, Downloads, Cache, Diagnostics and About.
- Remove legacy hard-coded Music Provider selection after provider registry migration.

### P24 — Multi-source Search
- Route search through the active search provider / Provider Registry.
- Normalize provider results into shared track entities.

### P25 — Extension + AutoMix integration
- Pipeline: ProviderRegistry -> UnifiedTrack -> AudioMetaRepository -> AutoMixPlanner -> platform playback engine.
- AutoMix must be provider-agnostic and work across different sources.

### P26 — Cache/reliability
- Expiring stream URL cache, audio metadata cache, artwork cache, search cache and extension health cache.
- Invalidate expired stream URLs and re-resolve automatically.

### P27 — Diagnostics
- Add provider, stream, buffer, preload, AutoMix state, BPM/key, transition and extension health diagnostics.
- Structured events include provider resolve/fallback, stream ready, precache ready, AutoMix planned/started/finished/fallback and player errors.

### P28 — SpotiFLAC parity tests
- Install both package extensions, invalid archives, traversal, duplicate paths, permissions, network allowlists, enable/disable, settings, health, priority, fallback, update/remove and SHA-256 mismatch.

### P29 — SimpMusic AutoMix parity tests
- Golden/reference tests for BPM normalization, Camelot, automatic duration, beat quantization, equal-power gains, DJ filter bounds, pitch shifts, fallback and transition cancellation.

### P30 — Cross-platform QA/release
- Android, iOS and Desktop/PC.
- Long queues, repeated AutoMix transitions, provider fallback, background/screen-off, connectivity changes, Bluetooth/headphones, calls/audio focus and low-memory/reopen scenarios.
- Verify no per-transition player/resource leak.

## Licensing gate
- SpotiFLAC and OpenSpot are MIT, so directly reused MIT code must retain required copyright/license notices.
- SimpMusic/core is GPL-3.0. Direct copying/integration of GPL implementation into a distributed OpenSpot build requires GPL-compatible distribution obligations. The AutoMix merge/release must not silently remain MIT if GPL-covered code is directly incorporated.

## Definition of Done
- Playback works with real providers and fallback.
- SpotiFLAC-compatible extension packages install, configure, enable/disable, update/remove and participate in priority/fallback.
- AutoMix uses actual overlapping player instances and source-parity algorithms/DSP behavior.
- Mobile bottom navigation is `Home | Mix | Library | Downloads | Settings` and Search remains accessible elsewhere.
- Vietnamese covers primary flows.
- Android, iOS and Desktop/PC reach behavior parity for the supported feature set.
- Parity and QA checks pass before merge to `main`.
