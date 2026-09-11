package gobackend

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
)

// AudioQuality is the scoped copy used by the extension runtime. The parsing
// behavior below is adapted directly from SpotiFLAC metadata.go and
// metadata_m4a.go so ffmpeg.getInfo and gobackend.getAudioQuality preserve the
// same FLAC/M4A quality semantics without importing the full metadata/tag stack.
type AudioQuality struct {
	BitDepth     int    `json:"bit_depth"`
	SampleRate   int    `json:"sample_rate"`
	TotalSamples int64  `json:"total_samples"`
	Duration     int    `json:"duration"`
	Bitrate      int    `json:"bitrate,omitempty"`
	Codec        string `json:"codec,omitempty"`
}

func flacAudioQualityFromStreamInfo(streamInfo []byte) AudioQuality {
	bitDepth, sampleRate, totalSamples := parseFLACStreamInfoQuality(streamInfo)
	duration := 0
	if sampleRate > 0 && totalSamples > 0 {
		duration = int(totalSamples / int64(sampleRate))
	}
	return AudioQuality{
		BitDepth:     bitDepth,
		SampleRate:   sampleRate,
		TotalSamples: totalSamples,
		Duration:     duration,
		Codec:        "flac",
	}
}

func GetAudioQuality(filePath string) (AudioQuality, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return AudioQuality{}, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	marker := make([]byte, 4)
	if _, err := file.Read(marker); err != nil {
		return AudioQuality{}, fmt.Errorf("failed to read marker: %w", err)
	}

	if string(marker) == "fLaC" {
		header := make([]byte, 4)
		if _, err := file.Read(header); err != nil {
			return AudioQuality{}, fmt.Errorf("failed to read header: %w", err)
		}
		if header[0]&0x7F != 0 {
			return AudioQuality{}, fmt.Errorf("first block is not STREAMINFO")
		}
		streamInfo := make([]byte, 34)
		if _, err := file.Read(streamInfo); err != nil {
			return AudioQuality{}, fmt.Errorf("failed to read STREAMINFO: %w", err)
		}
		return flacAudioQualityFromStreamInfo(streamInfo), nil
	}

	if _, err := file.Seek(0, 0); err != nil {
		return AudioQuality{}, fmt.Errorf("failed to seek file: %w", err)
	}
	header8 := make([]byte, 8)
	if _, err := file.Read(header8); err != nil {
		return AudioQuality{}, fmt.Errorf("failed to read header: %w", err)
	}
	if string(header8[4:8]) == "ftyp" {
		return GetM4AQuality(filePath)
	}
	return AudioQuality{}, fmt.Errorf("unsupported file format (not FLAC or M4A)")
}

func GetM4AQuality(filePath string) (AudioQuality, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return AudioQuality{}, fmt.Errorf("failed to open M4A file: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return AudioQuality{}, fmt.Errorf("failed to stat M4A file: %w", err)
	}
	return m4aQualityFromFile(f, info.Size())
}

func m4aQualityFromFile(f *os.File, fileSize int64) (AudioQuality, error) {
	moovHeader, moovFound, err := findAtomInRange(f, 0, fileSize, "moov", fileSize)
	if err != nil {
		return AudioQuality{}, fmt.Errorf("failed to find moov atom: %w", err)
	}
	if !moovFound {
		return AudioQuality{}, fmt.Errorf("moov atom not found")
	}

	moovStart := moovHeader.offset
	moovEnd := moovHeader.offset + moovHeader.size
	duration := readM4ADurationSeconds(f, moovHeader, fileSize)
	sampleOffset, atomType, err := findAudioSampleEntry(f, moovStart, moovEnd, fileSize)
	if err != nil {
		return AudioQuality{}, err
	}

	buf := make([]byte, 32)
	if _, err := f.ReadAt(buf, sampleOffset); err != nil {
		return AudioQuality{}, fmt.Errorf("failed to read audio sample entry: %w", err)
	}

	sampleRate := int(buf[28])<<8 | int(buf[29])
	bitDepth := 0
	codec := normalizeM4AAudioCodec(atomType)

	switch atomType {
	case "alac":
		bitDepth = int(buf[22])<<8 | int(buf[23])
		if alacBitDepth, alacSampleRate, ok := readALACSpecificConfig(f, sampleOffset, fileSize); ok {
			if alacBitDepth > 0 {
				bitDepth = alacBitDepth
			}
			if alacSampleRate > 0 {
				sampleRate = alacSampleRate
			}
		}
	case "fLaC":
		bitDepth = int(buf[22])<<8 | int(buf[23])
		if flacBitDepth, flacSampleRate, flacTotalSamples, ok := readMP4FLACSpecificConfig(f, sampleOffset, fileSize); ok {
			if flacBitDepth > 0 {
				bitDepth = flacBitDepth
			}
			if flacSampleRate > 0 {
				sampleRate = flacSampleRate
			}
			if flacTotalSamples > 0 && sampleRate > 0 && duration <= 0 {
				duration = int(flacTotalSamples / int64(sampleRate))
			}
		}
	}

	bitrate := estimateAudioBitrateKbps(fileSize, duration)
	if bitrate > 0 && bitrate < 16 {
		bitrate = 0
	}
	return AudioQuality{
		BitDepth:   bitDepth,
		SampleRate: sampleRate,
		Duration:   duration,
		Bitrate:    bitrate,
		Codec:      codec,
	}, nil
}

func normalizeM4AAudioCodec(atomType string) string {
	switch atomType {
	case "mp4a":
		return "aac"
	case "alac":
		return "alac"
	case "fLaC":
		return "flac"
	case "ec-3":
		return "eac3"
	case "ac-3":
		return "ac3"
	case "ac-4":
		return "ac4"
	case "Opus":
		return "opus"
	default:
		return strings.TrimSpace(atomType)
	}
}

func estimateAudioBitrateKbps(fileSize int64, durationSeconds int) int {
	if fileSize <= 0 || durationSeconds <= 0 {
		return 0
	}
	return int(math.Round(float64(fileSize*8) / float64(durationSeconds) / 1000.0))
}

func readM4ADurationSeconds(f *os.File, moovHeader atomHeader, fileSize int64) int {
	childStart := moovHeader.offset + moovHeader.headerSize
	childSize := moovHeader.size - moovHeader.headerSize
	mvhdHeader, found, err := findAtomInRange(f, childStart, childSize, "mvhd", fileSize)
	if err == nil && found {
		if duration := readMP4DurationAtomSeconds(f, mvhdHeader, fileSize); duration > 0 {
			return duration
		}
	}
	return readM4ATrackDurationSeconds(f, moovHeader, fileSize)
}

func readMP4DurationAtomSeconds(f *os.File, header atomHeader, _ int64) int {
	payloadOffset := header.offset + header.headerSize
	versionBuf := make([]byte, 1)
	if _, err := f.ReadAt(versionBuf, payloadOffset); err != nil {
		return 0
	}
	if versionBuf[0] == 1 {
		buf := make([]byte, 32)
		if _, err := f.ReadAt(buf, payloadOffset); err != nil {
			return 0
		}
		timescale := binary.BigEndian.Uint32(buf[20:24])
		duration := binary.BigEndian.Uint64(buf[24:32])
		if timescale == 0 || duration == 0 {
			return 0
		}
		return int(math.Round(float64(duration) / float64(timescale)))
	}
	buf := make([]byte, 20)
	if _, err := f.ReadAt(buf, payloadOffset); err != nil {
		return 0
	}
	timescale := binary.BigEndian.Uint32(buf[12:16])
	duration := binary.BigEndian.Uint32(buf[16:20])
	if timescale == 0 || duration == 0 {
		return 0
	}
	return int(math.Round(float64(duration) / float64(timescale)))
}

func readM4ATrackDurationSeconds(f *os.File, moovHeader atomHeader, fileSize int64) int {
	childStart := moovHeader.offset + moovHeader.headerSize
	childSize := moovHeader.size - moovHeader.headerSize
	bestDuration := 0
	_ = walkMP4AtomsInRange(f, childStart, childSize, fileSize, func(header atomHeader) bool {
		if header.typ == "mdhd" {
			if duration := readMP4DurationAtomSeconds(f, header, fileSize); duration > bestDuration {
				bestDuration = duration
			}
			return false
		}
		return header.typ == "trak" || header.typ == "mdia"
	})
	return bestDuration
}

func walkMP4AtomsInRange(f *os.File, start, size, fileSize int64, visit func(atomHeader) bool) error {
	if size <= 0 {
		return nil
	}
	end := start + size
	for pos := start; pos+8 <= end; {
		header, err := readAtomHeaderAt(f, pos, fileSize)
		if err != nil {
			return err
		}
		atomSize := header.size
		if atomSize == 0 {
			atomSize = end - pos
		}
		if atomSize < header.headerSize {
			return fmt.Errorf("invalid atom size for %s", header.typ)
		}
		header.size = atomSize
		if visit(header) {
			childStart := header.offset + header.headerSize
			childSize := header.size - header.headerSize
			if err := walkMP4AtomsInRange(f, childStart, childSize, fileSize, visit); err != nil {
				return err
			}
		}
		pos += atomSize
	}
	return nil
}

func readALACSpecificConfig(f *os.File, sampleOffset, fileSize int64) (int, int, bool) {
	if sampleOffset < 4 {
		return 0, 0, false
	}
	sampleEntryHeader, err := readAtomHeaderAt(f, sampleOffset-4, fileSize)
	if err != nil {
		return 0, 0, false
	}
	childStart := sampleOffset + 32
	childEnd := sampleEntryHeader.offset + sampleEntryHeader.size
	if childStart >= childEnd {
		return 0, 0, false
	}
	configHeader, found, err := findAtomInRange(f, childStart, childEnd-childStart, "alac", fileSize)
	if err != nil || !found {
		return 0, 0, false
	}
	payloadSize := configHeader.size - configHeader.headerSize
	if payloadSize <= 0 {
		return 0, 0, false
	}
	payload := make([]byte, payloadSize)
	if _, err := f.ReadAt(payload, configHeader.offset+configHeader.headerSize); err != nil {
		return 0, 0, false
	}
	return parseALACSpecificConfig(payload)
}

func readMP4FLACSpecificConfig(f *os.File, sampleOffset, fileSize int64) (int, int, int64, bool) {
	if sampleOffset < 4 {
		return 0, 0, 0, false
	}
	sampleEntryHeader, err := readAtomHeaderAt(f, sampleOffset-4, fileSize)
	if err != nil {
		return 0, 0, 0, false
	}
	childStart := sampleOffset + 32
	childEnd := sampleEntryHeader.offset + sampleEntryHeader.size
	if childStart >= childEnd {
		return 0, 0, 0, false
	}
	configHeader, found, err := findAtomInRange(f, childStart, childEnd-childStart, "dfLa", fileSize)
	if err != nil || !found {
		return 0, 0, 0, false
	}
	payloadSize := configHeader.size - configHeader.headerSize
	if payloadSize <= 0 {
		return 0, 0, 0, false
	}
	payload := make([]byte, payloadSize)
	if _, err := f.ReadAt(payload, configHeader.offset+configHeader.headerSize); err != nil {
		return 0, 0, 0, false
	}
	return parseMP4FLACSpecificConfig(payload)
}

func parseMP4FLACSpecificConfig(payload []byte) (int, int, int64, bool) {
	if len(payload) >= 4 && string(payload[:4]) == "fLaC" {
		payload = payload[4:]
	} else if len(payload) >= 4 {
		payload = payload[4:]
	}
	for len(payload) >= 4 {
		blockType := payload[0] & 0x7F
		blockLen := int(payload[1])<<16 | int(payload[2])<<8 | int(payload[3])
		if len(payload) < 4+blockLen {
			return 0, 0, 0, false
		}
		block := payload[4 : 4+blockLen]
		if blockType == 0 && len(block) >= 34 {
			bitDepth, sampleRate, totalSamples := parseFLACStreamInfoQuality(block[:34])
			return bitDepth, sampleRate, totalSamples, bitDepth > 0 || sampleRate > 0
		}
		payload = payload[4+blockLen:]
	}
	return 0, 0, 0, false
}

func parseFLACStreamInfoQuality(streamInfo []byte) (int, int, int64) {
	if len(streamInfo) < 18 {
		return 0, 0, 0
	}
	sampleRate := (int(streamInfo[10]) << 12) | (int(streamInfo[11]) << 4) | (int(streamInfo[12]) >> 4)
	bitsPerSample := (((int(streamInfo[12]) & 0x01) << 4) | (int(streamInfo[13]) >> 4)) + 1
	totalSamples := int64(streamInfo[13]&0x0F)<<32 |
		int64(streamInfo[14])<<24 |
		int64(streamInfo[15])<<16 |
		int64(streamInfo[16])<<8 |
		int64(streamInfo[17])
	return bitsPerSample, sampleRate, totalSamples
}

func parseALACSpecificConfig(payload []byte) (int, int, bool) {
	if len(payload) < 24 {
		return 0, 0, false
	}
	bitDepth := int(payload[5])
	sampleRate := int(binary.BigEndian.Uint32(payload[20:24]))
	if bitDepth > 0 && sampleRate > 0 {
		return bitDepth, sampleRate, true
	}
	if len(payload) >= 28 {
		bitDepth = int(payload[9])
		sampleRate = int(binary.BigEndian.Uint32(payload[24:28]))
		if bitDepth > 0 && sampleRate > 0 {
			return bitDepth, sampleRate, true
		}
	}
	return 0, 0, false
}

type atomHeader struct {
	offset     int64
	size       int64
	headerSize int64
	typ        string
}

func readAtomHeaderAt(f *os.File, offset, fileSize int64) (atomHeader, error) {
	if offset+8 > fileSize {
		return atomHeader{}, io.ErrUnexpectedEOF
	}
	headerBuf := make([]byte, 8)
	if _, err := f.ReadAt(headerBuf, offset); err != nil {
		return atomHeader{}, err
	}
	size32 := binary.BigEndian.Uint32(headerBuf[0:4])
	typ := string(headerBuf[4:8])
	if size32 == 1 {
		if offset+16 > fileSize {
			return atomHeader{}, io.ErrUnexpectedEOF
		}
		extBuf := make([]byte, 8)
		if _, err := f.ReadAt(extBuf, offset+8); err != nil {
			return atomHeader{}, err
		}
		return atomHeader{offset: offset, size: int64(binary.BigEndian.Uint64(extBuf)), headerSize: 16, typ: typ}, nil
	}
	return atomHeader{offset: offset, size: int64(size32), headerSize: 8, typ: typ}, nil
}

func findAtomInRange(f *os.File, start, size int64, target string, fileSize int64) (atomHeader, bool, error) {
	if size <= 0 {
		return atomHeader{}, false, nil
	}
	end := start + size
	for pos := start; pos+8 <= end; {
		header, err := readAtomHeaderAt(f, pos, fileSize)
		if err != nil {
			return atomHeader{}, false, err
		}
		atomSize := header.size
		if atomSize == 0 {
			atomSize = end - pos
		}
		if atomSize < header.headerSize {
			return atomHeader{}, false, fmt.Errorf("invalid atom size for %s", header.typ)
		}
		header.size = atomSize
		if header.typ == target {
			return header, true, nil
		}
		pos += atomSize
	}
	return atomHeader{}, false, nil
}

func findAudioSampleEntry(f *os.File, start, end, fileSize int64) (int64, string, error) {
	const chunkSize = 64 * 1024
	patterns := [][]byte{
		[]byte("mp4a"), []byte("alac"), []byte("fLaC"), []byte("ec-3"),
		[]byte("ac-3"), []byte("ac-4"), []byte("Opus"),
	}
	var tail []byte
	readPos := start
	for readPos < end {
		toRead := end - readPos
		if toRead > chunkSize {
			toRead = chunkSize
		}
		buf := make([]byte, toRead)
		n, err := f.ReadAt(buf, readPos)
		if err != nil && err != io.EOF {
			return 0, "", fmt.Errorf("failed to read M4A atom data: %w", err)
		}
		if n == 0 {
			break
		}
		data := append(tail, buf[:n]...)
		bestIdx := -1
		bestType := ""
		for _, pattern := range patterns {
			idx := bytes.Index(data, pattern)
			if idx >= 0 && (bestIdx < 0 || idx < bestIdx) {
				bestIdx = idx
				bestType = string(pattern)
			}
		}
		if bestIdx >= 0 {
			absolute := readPos - int64(len(tail)) + int64(bestIdx)
			if absolute+32 > fileSize {
				return 0, "", fmt.Errorf("audio info not found in M4A file")
			}
			return absolute, bestType, nil
		}
		if len(data) >= 3 {
			tail = append([]byte{}, data[len(data)-3:]...)
		} else {
			tail = append([]byte{}, data...)
		}
		readPos += int64(n)
	}
	return 0, "", fmt.Errorf("audio info not found in M4A file")
}
