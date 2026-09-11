module github.com/zarz/spotiflac_android/go_backend

// Needs >= 1.26.3: fixes the cgo "bulkBarrierPreWrite" crash (golang/go#46893)
// without the 1.26.0-1.26.2 arm32 SIGSYS regression (golang/go#78936).
// Full patch version here because gomobile's bind module inherits it.
go 1.26.6

require (
	github.com/dop251/goja v0.0.0-20260826204918-8f1c0696a37b
	github.com/go-flac/flacpicture/v2 v2.0.2
	github.com/go-flac/flacvorbis/v2 v2.0.2
	github.com/go-flac/go-flac/v2 v2.0.4
	github.com/refraction-networking/utls v1.8.2
	golang.org/x/crypto v0.55.0
	golang.org/x/image v0.45.0
	golang.org/x/mobile v0.0.0-20260821190718-4776eadac327
	golang.org/x/net v0.58.0
	golang.org/x/sync v0.22.0
	golang.org/x/text v0.41.0
)

require (
	github.com/andybalholm/brotli v1.2.3 // indirect
	github.com/dlclark/regexp2/v2 v2.7.1 // indirect
	github.com/go-sourcemap/sourcemap v2.1.4+incompatible // indirect
	github.com/google/pprof v0.0.0-20260825171938-4d453200e7d9 // indirect
	github.com/klauspost/compress v1.19.2 // indirect
	golang.org/x/mod v0.40.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/tools v0.49.0 // indirect
)
