# SpotiFLAC Extension Core — upstream snapshot

This directory vendors the SpotiFLAC Mobile extension subsystem as the source-of-truth baseline for the OpenSpot vNext extension port.

## Upstream

- Repository: `todo996/SpotiFLAC-Mobile`
- Upstream branch: `main`
- Upstream commit: `8165fc99f18234049d003ec8379dea7792c452a1`
- License: MIT (copyright 2026 zarzet)
- Original package: `gobackend`

## Porting rule

Files under `upstream/` are intended to stay as close to the upstream source as possible. OpenSpot-specific bridges and adaptations must live outside `upstream/` so behavior can be checked against the original implementation.

The OpenSpot port must preserve the SpotiFLAC contracts for `.sflx` / `.spotiflac-ext`, manifest validation, permissions, provider priority/fallback, extension settings, repository integrity, health checks, runtime isolation and package lifecycle.

Android/iOS will expose this core through a native Expo/React Native bridge. Desktop will expose the same core through a Tauri/native bridge. The UI remains OpenSpot-styled while preserving the source feature structure and identity.
