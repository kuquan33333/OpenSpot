package gobackend

// DownloadRequest is the extension-facing request contract extracted from
// SpotiFLAC-Mobile go_backend/exports_download.go at pinned upstream commit
// 8165fc99f18234049d003ec8379dea7792c452a1. The SpotiFLAC application-level
// download pipeline is intentionally not part of the OpenSpot Extension Core.
type DownloadRequest struct {
	ContractVersion             int    `json:"contract_version,omitempty"`
	ISRC                        string `json:"isrc"`
	Service                     string `json:"service"`
	DownloadProvider            string `json:"download_provider,omitempty"`
	ProviderTrackID             string `json:"provider_track_id,omitempty"`
	SpotifyID                   string `json:"spotify_id"`
	TrackName                   string `json:"track_name"`
	ArtistName                  string `json:"artist_name"`
	AlbumName                   string `json:"album_name"`
	AlbumArtist                 string `json:"album_artist"`
	CoverURL                    string `json:"cover_url"`
	CoverMaxDimension           int    `json:"cover_max_dimension,omitempty"`
	OutputDir                   string `json:"output_dir"`
	AlbumFolderTemplate         string `json:"album_folder_template,omitempty"`
	OutputPath                  string `json:"output_path,omitempty"`
	OutputFD                    int    `json:"output_fd,omitempty"`
	OutputExt                   string `json:"output_ext,omitempty"`
	FilenameFormat              string `json:"filename_format"`
	Quality                     string `json:"quality"`
	EmbedMetadata               bool   `json:"embed_metadata"`
	ArtistTagMode               string `json:"artist_tag_mode,omitempty"`
	EmbedLyrics                 bool   `json:"embed_lyrics"`
	EmbedReplayGain             bool   `json:"embed_replaygain,omitempty"`
	PostProcessingEnabled       bool   `json:"post_processing_enabled,omitempty"`
	TrackNumber                 int    `json:"track_number"`
	PlaylistPosition            int    `json:"playlist_position,omitempty"`
	DiscNumber                  int    `json:"disc_number"`
	TotalTracks                 int    `json:"total_tracks"`
	TotalDiscs                  int    `json:"total_discs,omitempty"`
	ReleaseDate                 string `json:"release_date"`
	ItemID                      string `json:"item_id"`
	DurationMS                  int    `json:"duration_ms"`
	Source                      string `json:"source"`
	Genre                       string `json:"genre,omitempty"`
	Label                       string `json:"label,omitempty"`
	Copyright                   string `json:"copyright,omitempty"`
	Composer                    string `json:"composer,omitempty"`
	Comment                     string `json:"comment,omitempty"`
	Explicit                    bool   `json:"explicit,omitempty"`
	AlbumType                   string `json:"album_type,omitempty"`
	UPC                         string `json:"upc,omitempty"`
	TidalID                     string `json:"tidal_id,omitempty"`
	QobuzID                     string `json:"qobuz_id,omitempty"`
	DeezerID                    string `json:"deezer_id,omitempty"`
	LyricsMode                  string `json:"lyrics_mode,omitempty"`
	UseExtensions               bool   `json:"use_extensions,omitempty"`
	UseFallback                 bool   `json:"use_fallback,omitempty"`
	RequiresContainerConversion bool   `json:"requires_container_conversion,omitempty"`
	AllowQualityVariant         bool   `json:"allow_quality_variant,omitempty"`
	QualityVariant              string `json:"quality_variant,omitempty"`
	SongLinkRegion              string `json:"songlink_region,omitempty"`
	NetworkConcurrencyLimit     int    `json:"network_concurrency_limit,omitempty"`
}
