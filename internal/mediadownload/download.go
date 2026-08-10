package mediadownload

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"mime"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "golang.org/x/image/webp"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/safehttp"
)

type progressOptions struct {
	Logger   *slog.Logger
	Interval time.Duration
	Bytes    int64
	MaxBytes int64
}

func downloadRef(ctx context.Context, client *http.Client, cfg config.Config, ref model.ItemMediaRef, namespace string, progress progressOptions) (model.MediaDownloadResult, error) {
	if progress.MaxBytes <= 0 {
		progress.MaxBytes = DefaultMaxBytes
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ref.RemoteURL, nil)
	if err != nil {
		return model.MediaDownloadResult{}, fmt.Errorf("create media request %q: %w", ref.RemoteURL, err)
	}
	req.Header.Set("user-agent", "dbrain/1.0")

	resp, err := client.Do(req)
	if err != nil {
		status := model.MediaDownloadStatusError
		if safehttp.IsPolicyError(err) {
			status = model.MediaDownloadStatusBlocked
		}
		return model.MediaDownloadResult{
			Status: status,
			Error:  err.Error(),
		}, nil
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		return model.MediaDownloadResult{
			Status: model.MediaDownloadStatusGone,
			Error:  fmt.Sprintf("media returned status=%d", resp.StatusCode),
		}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		status := model.MediaDownloadStatusError
		if resp.StatusCode != http.StatusTooManyRequests && !retryableMediaHTTPStatus(resp.StatusCode) {
			status = model.MediaDownloadStatusBlocked
		}
		return model.MediaDownloadResult{
			Status: status,
			Error:  fmt.Sprintf("media returned status=%d", resp.StatusCode),
		}, nil
	}

	contentLength := resp.ContentLength
	if contentLength > progress.MaxBytes {
		return model.MediaDownloadResult{Status: model.MediaDownloadStatusBlocked, Error: fmt.Sprintf("media response exceeds %d bytes", progress.MaxBytes)}, nil
	}
	contentType := resp.Header.Get("content-type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = strings.TrimSpace(contentType)
	}
	if strings.HasPrefix(mediaType, "text/html") {
		return model.MediaDownloadResult{
			Status: model.MediaDownloadStatusBlocked,
			Error:  "media request returned HTML instead of media bytes",
		}, nil
	}
	bufferedBody := bufio.NewReader(resp.Body)
	playlistHeader, _ := bufferedBody.Peek(512)
	if isHLSPlaylist(mediaType, ref.RemoteURL, playlistHeader) {
		return model.MediaDownloadResult{
			Status: model.MediaDownloadStatusBlocked,
			Error:  "media request returned an HLS playlist instead of media bytes",
		}, nil
	}
	if mediaType == "" || mediaType == "application/octet-stream" {
		detected := sniffMediaMIME(ref.MediaType, playlistHeader)
		if detected != "application/octet-stream" {
			mediaType = detected
		} else if ref.MediaType == "video" {
			// Bluesky getBlob URLs have no extension and some PDSes omit the
			// content type. Bluesky video blobs are MP4, so retain a truthful
			// extension for the existing ffprobe/transcription path.
			mediaType = "video/mp4"
		}
	}
	tmpDir := filepath.Join(cfg.MediaDir, ".tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return model.MediaDownloadResult{}, fmt.Errorf("create media temp dir: %w", err)
	}

	tmpFile, err := os.CreateTemp(tmpDir, "download-*")
	if err != nil {
		return model.MediaDownloadResult{}, fmt.Errorf("create media temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
	}()

	hasher := sha256.New()
	writer := io.MultiWriter(tmpFile, hasher)
	tracker := newDownloadProgressWriter(writer, progress, ref, contentLength)
	if tracker != nil {
		writer = tracker
	}
	written, copyErr := io.Copy(writer, io.LimitReader(bufferedBody, progress.MaxBytes+1))
	if tracker != nil {
		tracker.finish()
	}
	if copyErr != nil {
		return model.MediaDownloadResult{
			Status: model.MediaDownloadStatusError,
			Error:  copyErr.Error(),
		}, nil
	}
	if written > progress.MaxBytes {
		return model.MediaDownloadResult{Status: model.MediaDownloadStatusBlocked, Error: fmt.Sprintf("media response exceeds %d bytes", progress.MaxBytes)}, nil
	}
	if err := validateMediaFile(ref.MediaType, mediaType, playlistHeader, tmpFile, written); err != nil {
		return model.MediaDownloadResult{Status: model.MediaDownloadStatusBlocked, Error: err.Error()}, nil
	}
	if err := tmpFile.Close(); err != nil {
		return model.MediaDownloadResult{
			Status: model.MediaDownloadStatusError,
			Error:  err.Error(),
		}, nil
	}

	sum := hex.EncodeToString(hasher.Sum(nil))
	ext := mediaExtension(mediaType, ref.RemoteURL)
	relPath := filepath.ToSlash(filepath.Join("media", namespace, normalizedMediaType(ref.MediaType), sum[:2], sum+ext))
	fullPath := filepath.Join(cfg.VaultDir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return model.MediaDownloadResult{}, fmt.Errorf("create media target dir: %w", err)
	}

	if _, err := os.Stat(fullPath); err == nil {
		return model.MediaDownloadResult{
			MIMEType:     mediaType,
			ByteSize:     written,
			ContentHash:  "sha256:" + sum,
			LocalPath:    relPath,
			Status:       model.MediaDownloadStatusDownloaded,
			DownloadedAt: time.Now().UTC(),
		}, nil
	}
	if err := os.Rename(tmpPath, fullPath); err != nil {
		return model.MediaDownloadResult{}, fmt.Errorf("move media into vault: %w", err)
	}

	return model.MediaDownloadResult{
		MIMEType:     mediaType,
		ByteSize:     written,
		ContentHash:  "sha256:" + sum,
		LocalPath:    relPath,
		Status:       model.MediaDownloadStatusDownloaded,
		DownloadedAt: time.Now().UTC(),
	}, nil
}

func retryableMediaHTTPStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func validateMediaBytes(expected, declared string, prefix []byte) error {
	if len(prefix) == 0 {
		return errors.New("media response was empty")
	}
	detected := sniffMediaMIME(expected, prefix)
	if unsafeMediaMIME(detected) {
		return fmt.Errorf("media response content sniffed as %s", detected)
	}
	declared = strings.ToLower(strings.TrimSpace(declared))
	switch normalizedMediaType(expected) {
	case "photo":
		if !strings.HasPrefix(declared, "image/") || declared == "image/svg+xml" {
			return fmt.Errorf("media response MIME %q is not a photo", declared)
		}
		if !strings.HasPrefix(detected, "image/") || detected == "image/svg+xml" {
			return fmt.Errorf("media response content sniffed as %s, not an image", detected)
		}
		if detected != declared {
			return fmt.Errorf("media response MIME %q disagrees with content %q", declared, detected)
		}
	case "animated_gif":
		if strings.HasPrefix(declared, "video/") {
			if !strings.HasPrefix(detected, "video/") {
				return fmt.Errorf("media response content sniffed as %s, not a video-backed animated image", detected)
			}
			break
		}
		if declared != "image/gif" {
			return fmt.Errorf("media response MIME %q is not an animated image", declared)
		}
		if detected != "image/gif" {
			return fmt.Errorf("media response content sniffed as %s, not an animated image", detected)
		}
	case "video":
		if !strings.HasPrefix(declared, "video/") {
			return fmt.Errorf("media response MIME %q is not a video", declared)
		}
		if strings.HasPrefix(detected, "image/") || strings.HasPrefix(detected, "audio/") {
			return fmt.Errorf("media response content sniffed as %s, not a video", detected)
		}
		if !isVideoBytes(prefix) {
			return fmt.Errorf("media response content is not a recognized video format")
		}
	case "audio":
		if !strings.HasPrefix(declared, "audio/") {
			return fmt.Errorf("media response MIME %q is not audio", declared)
		}
		if strings.HasPrefix(detected, "image/") || strings.HasPrefix(detected, "video/") {
			return fmt.Errorf("media response content sniffed as %s, not audio", detected)
		}
		if !isAudioBytes(prefix) {
			return fmt.Errorf("media response content is not a recognized audio format")
		}
	}
	return nil
}

func validateMediaFile(expected, declared string, prefix []byte, content io.ReaderAt, size int64) error {
	if err := validateMediaBytes(expected, declared, prefix); err != nil {
		return err
	}
	normalizedExpected := normalizedMediaType(expected)
	validationType := normalizedExpected
	if normalizedExpected == "animated_gif" && strings.HasPrefix(strings.ToLower(strings.TrimSpace(declared)), "video/") {
		validationType = "video"
	}
	switch validationType {
	case "photo", "animated_gif":
		if !validImageFile(content, size) {
			return fmt.Errorf("media response content is not a complete recognized image format")
		}
	case "video", "audio":
		if !validMediaContainer(validationType, content, size) {
			return fmt.Errorf("media response content is not a complete recognized %s format", validationType)
		}
	}
	return nil
}

func validImageFile(content io.ReaderAt, size int64) bool {
	if size <= 0 {
		return false
	}
	section := io.NewSectionReader(content, 0, size)
	config, format, err := image.DecodeConfig(section)
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 64*1024*1024 {
		return false
	}
	section = io.NewSectionReader(content, 0, size)
	if format == "gif" {
		_, err = gif.DecodeAll(section)
		return err == nil
	}
	_, _, err = image.Decode(section)
	return err == nil
}

func validMediaContainer(expected string, content io.ReaderAt, size int64) bool {
	if validISOBaseMediaFile(expected, content, size) || validEBMLFile(expected, content, size) || validOggFile(expected, content, size) {
		return true
	}
	if expected == "video" {
		return validMPEGTransportStreamFile(expected, content, size)
	}
	return validWAVFile(content, size) || validFLACFile(content, size) || validMPEGFile(content, size)
}

func readAtExact(content io.ReaderAt, offset int64, size int) ([]byte, bool) {
	if offset < 0 || size < 0 {
		return nil, false
	}
	data := make([]byte, size)
	read, err := content.ReadAt(data, offset)
	return data, read == size && (err == nil || err == io.EOF)
}

func validISOBaseMediaFile(expected string, content io.ReaderAt, total int64) bool {
	if total < 16 {
		return false
	}
	boxType, payloadOffset, payloadSize, next, ok := readISOBoxHeader(content, total, 0)
	if !ok || boxType != "ftyp" || payloadSize < 8 {
		return false
	}
	brand, ok := readAtExact(content, payloadOffset, 4)
	if !ok || !isASCIIContainerBrand(brand) {
		return false
	}
	if next > total {
		return false
	}
	mediaBoxes := make([]struct{ offset, size int64 }, 0, 2)
	sampleEntry := ""
	for offset := next; offset < total; {
		boxType, boxPayloadOffset, boxPayloadSize, boxEnd, ok := readISOBoxHeader(content, total, offset)
		if !ok {
			return false
		}
		if boxPayloadSize > 0 {
			switch boxType {
			case "mdat":
				mediaBoxes = append(mediaBoxes, struct{ offset, size int64 }{boxPayloadOffset, boxPayloadSize})
			case "moov", "moof":
				if candidate, found := isoExpectedSampleEntry(expected, content, boxPayloadOffset, boxPayloadSize); found && sampleEntry == "" {
					sampleEntry = candidate
				}
			}
		}
		offset = boxEnd
	}
	if sampleEntry == "" {
		return false
	}
	for _, media := range mediaBoxes {
		if isoMediaPayloadLooksValid(expected, sampleEntry, content, media.offset, media.size) {
			return true
		}
	}
	return false
}

const maxISODescriptionBytes = int64(8 << 20)

func isoExpectedSampleEntry(expected string, content io.ReaderAt, offset, size int64) (string, bool) {
	if size <= 0 || size > maxISODescriptionBytes {
		return "", false
	}
	data, ok := readAtExact(content, offset, int(size))
	if !ok {
		return "", false
	}
	return isoFindSampleEntry(data, expected, 0)
}

const isoTrackPathMask = 1 | 2 | 4 | 8

func isoFindSampleEntry(data []byte, expected string, trackPath uint8) (string, bool) {
	for offset := 0; offset+8 <= len(data); {
		boxSize := uint64(binary.BigEndian.Uint32(data[offset : offset+4]))
		headerSize := 8
		switch boxSize {
		case 1:
			if offset+16 > len(data) {
				return "", false
			}
			boxSize = binary.BigEndian.Uint64(data[offset+8 : offset+16])
			headerSize = 16
		case 0:
			boxSize = uint64(len(data) - offset)
		}
		if boxSize < uint64(headerSize) || boxSize > uint64(len(data)-offset) {
			return "", false
		}
		end := offset + int(boxSize)
		boxType := string(data[offset+4 : offset+8])
		path := trackPath
		switch boxType {
		case "trak":
			path |= 1
		case "mdia":
			path |= 2
		case "minf":
			path |= 4
		case "stbl":
			path |= 8
		}
		payload := data[offset+headerSize : end]
		if boxType == "stsd" && path == isoTrackPathMask && len(payload) >= 8 {
			entryCount := int(binary.BigEndian.Uint32(payload[4:8]))
			cursor := 8
			for entry := 0; entry < entryCount && cursor+8 <= len(payload); entry++ {
				entrySize := int(binary.BigEndian.Uint32(payload[cursor : cursor+4]))
				if entrySize < 8 || cursor+entrySize > len(payload) {
					break
				}
				entryType := string(payload[cursor+4 : cursor+8])
				entryPayload := payload[cursor : cursor+entrySize]
				if isoSampleEntryMatches(expected, entryType) && isoSampleEntryHasCodecConfig(expected, entryType, entryPayload) {
					return entryType, true
				}
				cursor += entrySize
			}
		}
		if boxType == "moov" || boxType == "trak" || boxType == "mdia" || boxType == "minf" || boxType == "stbl" {
			if candidate, found := isoFindSampleEntry(payload, expected, path); found {
				return candidate, true
			}
		}
		offset = end
	}
	return "", false
}

func isoSampleEntryHasCodecConfig(expected, entryType string, entry []byte) bool {
	if expected == "video" {
		// Visual sample entries contain a fixed 78-byte visual sample
		// description after the eight-byte size/type header. A codec
		// configuration box after that description distinguishes a real
		// track from a superficial stsd plus arbitrary NAL-shaped bytes.
		if len(entry) < 86 {
			return false
		}
		target := ""
		switch entryType {
		case "avc1", "avc3":
			target = "avcC"
		case "hvc1", "hev1":
			target = "hvcC"
		case "av01":
			target = "av1C"
		case "vp09":
			target = "vpcC"
		case "mp4v":
			target = "esds"
		default:
			return false
		}
		config, ok := isoChildBoxPayload(entry[86:], target)
		if !ok || len(config) < 4 {
			return false
		}
		if entryType == "avc1" || entryType == "avc3" {
			return validAVCConfiguration(config)
		}
		return true
	}
	if len(entry) < 36 {
		return false
	}
	target := ""
	switch entryType {
	case "mp4a":
		target = "esds"
	case "Opus":
		target = "dOps"
	case "ac-3":
		target = "dac3"
	case "ec-3":
		target = "dec3"
	case "fLaC":
		target = "dfLa"
	default:
		return true
	}
	config, ok := isoChildBoxPayload(entry[36:], target)
	return ok && len(config) >= 4
}

func validAVCConfiguration(config []byte) bool {
	if len(config) < 7 || config[0] != 1 {
		return false
	}
	sps := int(config[5] & 0x1f)
	if sps == 0 {
		return false
	}
	cursor := 6
	for index := 0; index < sps; index++ {
		if cursor+2 > len(config) {
			return false
		}
		length := int(binary.BigEndian.Uint16(config[cursor : cursor+2]))
		cursor += 2
		if length < 4 || cursor+length > len(config) {
			return false
		}
		cursor += length
	}
	if cursor >= len(config) {
		return false
	}
	pps := int(config[cursor])
	if pps == 0 {
		return false
	}
	cursor++
	for index := 0; index < pps; index++ {
		if cursor+2 > len(config) {
			return false
		}
		length := int(binary.BigEndian.Uint16(config[cursor : cursor+2]))
		cursor += 2
		if length == 0 || cursor+length > len(config) {
			return false
		}
		cursor += length
	}
	return true
}

func isoChildBoxPayload(data []byte, target string) ([]byte, bool) {
	for offset := 0; offset+8 <= len(data); {
		boxSize := uint64(binary.BigEndian.Uint32(data[offset : offset+4]))
		headerSize := 8
		switch boxSize {
		case 1:
			if offset+16 > len(data) {
				return nil, false
			}
			boxSize = binary.BigEndian.Uint64(data[offset+8 : offset+16])
			headerSize = 16
		case 0:
			boxSize = uint64(len(data) - offset)
		}
		if boxSize < uint64(headerSize) || boxSize > uint64(len(data)-offset) {
			return nil, false
		}
		end := offset + int(boxSize)
		if string(data[offset+4:offset+8]) == target {
			return data[offset+headerSize : end], true
		}
		offset = end
	}
	return nil, false
}

func isoSampleEntryMatches(expected, entryType string) bool {
	if expected == "video" {
		switch entryType {
		case "avc1", "avc3", "av01", "hvc1", "hev1", "vp09", "mp4v":
			return true
		default:
			return false
		}
	}
	switch entryType {
	case "mp4a", "Opus", "ac-3", "ec-3", "fLaC", "alaw", "ulaw", "sowt", "twos", "lpcm":
		return true
	default:
		return false
	}
}

func isoMediaPayloadLooksValid(expected, sampleEntry string, content io.ReaderAt, offset, size int64) bool {
	if size < 8 {
		return false
	}
	readSize := size
	if readSize > 256*1024 {
		readSize = 256 * 1024
	}
	payload, ok := readAtExact(content, offset, int(readSize))
	if !ok || len(payload) < 8 {
		return false
	}
	if expected == "video" {
		switch sampleEntry {
		case "avc1", "avc3":
			return containsH264NAL(payload)
		case "hvc1", "hev1":
			return containsHEVCNAL(payload)
		case "av01":
			return containsAV1OBU(payload)
		case "vp09":
			return containsVP9Frame(payload)
		case "mp4v":
			return containsMPEGVideoStartCode(payload)
		default:
			return false
		}
	}
	if expected == "audio" && containsNonUniformPayload(payload) {
		return true
	}
	return false
}

func containsH264NAL(data []byte) bool {
	for index := 0; index+5 <= len(data); index++ {
		if data[index] == 0 && data[index+1] == 0 && ((data[index+2] == 1 && index+4 < len(data)) || (data[index+2] == 0 && data[index+3] == 1 && index+5 < len(data))) {
			nalIndex := index + 3
			if data[index+2] == 0 {
				nalIndex++
			}
			nalType := data[nalIndex] & 0x1f
			nalEnd := len(data)
			for cursor := nalIndex + 1; cursor+3 < len(data); cursor++ {
				if data[cursor] == 0 && data[cursor+1] == 0 && (data[cursor+2] == 1 || (data[cursor+2] == 0 && data[cursor+3] == 1)) {
					nalEnd = cursor
					break
				}
			}
			if nalType >= 1 && nalType <= 8 && nalEnd-nalIndex >= 8 {
				return true
			}
		}
		if index+5 <= len(data) {
			length := int(binary.BigEndian.Uint32(data[index : index+4]))
			if length >= 8 && index+4+length <= len(data) && data[index+4]&0x1f >= 1 && data[index+4]&0x1f <= 8 {
				return true
			}
		}
	}
	return false
}

func containsHEVCNAL(data []byte) bool {
	for index := 0; index+5 <= len(data); index++ {
		nalIndex := -1
		if data[index] == 0 && data[index+1] == 0 && data[index+2] == 1 {
			nalIndex = index + 3
		} else if index+4 < len(data) && data[index] == 0 && data[index+1] == 0 && data[index+2] == 0 && data[index+3] == 1 {
			nalIndex = index + 4
		} else if index+5 <= len(data) {
			length := int(binary.BigEndian.Uint32(data[index : index+4]))
			if length > 0 && index+4+length <= len(data) {
				nalIndex = index + 4
			}
		}
		if nalIndex >= 0 && nalIndex+2 < len(data) {
			nalType := (data[nalIndex] >> 1) & 0x3f
			if nalType >= 1 && nalType <= 40 {
				return true
			}
		}
	}
	return false
}

func containsAV1OBU(data []byte) bool {
	for index, value := range data {
		if value&0x80 != 0 || value&0x01 != 0 || value&0x02 == 0 {
			continue
		}
		obuType := (value >> 3) & 0x0f
		if obuType < 1 || obuType > 8 {
			continue
		}
		cursor := index + 1
		if value&0x04 != 0 {
			cursor++
		}
		if cursor >= len(data) {
			continue
		}
		var payloadSize uint64
		for shift := uint(0); cursor < len(data) && shift < 56; shift += 7 {
			part := data[cursor]
			cursor++
			payloadSize |= uint64(part&0x7f) << shift
			if part&0x80 == 0 {
				if payloadSize > 0 && payloadSize <= uint64(len(data)-cursor) {
					return true
				}
				break
			}
		}
	}
	return false
}

func containsVP9Frame(data []byte) bool {
	return len(data) >= 2 && data[0]&0xc0 == 0x80 && containsNonUniformPayload(data)
}

func containsNonUniformPayload(data []byte) bool {
	if len(data) < 16 {
		return false
	}
	first := data[0]
	seenDifferent := false
	seenNonZero := first != 0
	for _, value := range data[1:] {
		seenDifferent = seenDifferent || value != first
		seenNonZero = seenNonZero || value != 0
		if seenDifferent && seenNonZero {
			return true
		}
	}
	return false
}

func readISOBoxHeader(content io.ReaderAt, total, offset int64) (string, int64, int64, int64, bool) {
	header, ok := readAtExact(content, offset, 8)
	if !ok || total-offset < 8 {
		return "", 0, 0, 0, false
	}
	boxSize := uint64(binary.BigEndian.Uint32(header[:4]))
	headerSize := int64(8)
	switch boxSize {
	case 1:
		extended, ok := readAtExact(content, offset+8, 8)
		if !ok {
			return "", 0, 0, 0, false
		}
		boxSize = binary.BigEndian.Uint64(extended)
		headerSize = 16
	case 0:
		boxSize = uint64(total - offset)
	}
	if boxSize < uint64(headerSize) || boxSize > uint64(total-offset) {
		return "", 0, 0, 0, false
	}
	boxEnd := offset + int64(boxSize)
	return string(header[4:8]), offset + headerSize, int64(boxSize) - headerSize, boxEnd, true
}

func validEBMLFile(expected string, content io.ReaderAt, total int64) bool {
	if total < 16 {
		return false
	}
	id, _, headerPayloadOffset, headerPayloadSize, headerEnd, ok := readEBMLElement(content, total, 0)
	if !ok || id != 0x1a45dfa3 {
		return false
	}
	if !validEBMLHeaderDocType(content, headerPayloadOffset, headerPayloadSize) {
		return false
	}
	offset := headerEnd
	for offset < total {
		id, _, payloadOffset, payloadSize, elementEnd, ok := readEBMLElement(content, total, offset)
		if !ok {
			return false
		}
		if id == 0x18538067 {
			segmentEnd := elementEnd
			if payloadSize == ebmlUnknownSize {
				segmentEnd = total
			}
			segmentTracks := ebmlTrackNumbers(content, payloadOffset, segmentEnd-payloadOffset, expected)
			if len(segmentTracks) == 0 {
				return false
			}
			clusterOffset := payloadOffset
			for clusterOffset < segmentEnd {
				childID, _, childPayloadOffset, childPayloadSize, childEnd, childOK := readEBMLElement(content, segmentEnd, clusterOffset)
				if !childOK {
					return false
				}
				if childID == 0x1f43b675 && validEBMLCluster(content, childPayloadOffset, childPayloadSize, childEnd, segmentEnd, segmentTracks) {
					return true
				}
				if childPayloadSize == ebmlUnknownSize {
					return false
				}
				clusterOffset = childEnd
			}
			return false
		}
		if payloadSize == ebmlUnknownSize {
			return false
		}
		offset = elementEnd
	}
	return false
}

const ebmlUnknownSize int64 = -1

func validEBMLHeaderDocType(content io.ReaderAt, offset int64, size int64) bool {
	end := offset + size
	for offset < end {
		id, _, payloadOffset, payloadSize, elementEnd, ok := readEBMLElement(content, end, offset)
		if !ok || payloadSize == ebmlUnknownSize || payloadSize > end-payloadOffset {
			return false
		}
		if id == 0x4282 && payloadSize > 0 && payloadSize <= 32 {
			docType, ok := readAtExact(content, payloadOffset, int(payloadSize))
			return ok && (strings.EqualFold(string(docType), "webm") || strings.EqualFold(string(docType), "matroska"))
		}
		offset = elementEnd
	}
	return false
}

func ebmlTrackNumbers(content io.ReaderAt, offset, size int64, expected string) map[uint64]string {
	tracks := make(map[uint64]string)
	if size <= 0 {
		return tracks
	}
	end := offset + size
	for offset < end {
		id, _, payloadOffset, payloadSize, childEnd, ok := readEBMLElement(content, end, offset)
		if !ok {
			return map[uint64]string{}
		}
		if id == 0x1654ae6b && payloadSize != ebmlUnknownSize {
			trackOffset := payloadOffset
			trackEnd := payloadOffset + payloadSize
			for trackOffset < trackEnd {
				trackID, _, trackPayloadOffset, trackPayloadSize, trackChildEnd, trackOK := readEBMLElement(content, trackEnd, trackOffset)
				if !trackOK {
					return map[uint64]string{}
				}
				if trackID == 0xae && trackPayloadSize != ebmlUnknownSize {
					number, trackType, codec, configured, valid := parseEBMLTrackEntry(content, trackPayloadOffset, trackPayloadSize)
					if valid && configured && ebmlTrackMatches(expected, trackType, codec) {
						tracks[number] = codec
					}
				}
				if trackPayloadSize == ebmlUnknownSize {
					return map[uint64]string{}
				}
				trackOffset = trackChildEnd
			}
		}
		if payloadSize == ebmlUnknownSize {
			return map[uint64]string{}
		}
		offset = childEnd
	}
	return tracks
}

func parseEBMLTrackEntry(content io.ReaderAt, offset, size int64) (uint64, uint64, string, bool, bool) {
	end := offset + size
	var number, trackType uint64
	var codec string
	configured := false
	for offset < end {
		id, _, payloadOffset, payloadSize, childEnd, ok := readEBMLElement(content, end, offset)
		if !ok || payloadSize == ebmlUnknownSize {
			return 0, 0, "", false, false
		}
		switch id {
		case 0xd7, 0x83:
			value, valueOK := readEBMLUint(content, payloadOffset, payloadSize)
			if !valueOK {
				return 0, 0, "", false, false
			}
			if id == 0xd7 {
				number = value
			} else {
				trackType = value
			}
		case 0x86:
			value, valueOK := readAtExact(content, payloadOffset, int(payloadSize))
			if !valueOK || payloadSize == 0 || payloadSize > 128 {
				return 0, 0, "", false, false
			}
			codec = string(value)
		case 0xe0:
			configured = ebmlTrackHasConfiguration(content, payloadOffset, payloadSize, "video")
		case 0xe1:
			configured = ebmlTrackHasConfiguration(content, payloadOffset, payloadSize, "audio")
		}
		offset = childEnd
	}
	return number, trackType, codec, configured, number != 0 && trackType != 0 && codec != ""
}

func ebmlTrackHasConfiguration(content io.ReaderAt, offset, size int64, expected string) bool {
	end := offset + size
	hasWidth, hasHeight, hasRate, hasChannels := false, false, false, false
	for offset < end {
		id, _, payloadOffset, payloadSize, childEnd, ok := readEBMLElement(content, end, offset)
		if !ok || payloadSize == ebmlUnknownSize {
			return false
		}
		switch id {
		case 0xb0:
			value, valueOK := readEBMLUint(content, payloadOffset, payloadSize)
			hasWidth = valueOK && value > 0
		case 0xba:
			value, valueOK := readEBMLUint(content, payloadOffset, payloadSize)
			hasHeight = valueOK && value > 0
		case 0xb5:
			data, dataOK := readAtExact(content, payloadOffset, int(payloadSize))
			if dataOK && (len(data) == 4 || len(data) == 8) {
				for _, value := range data {
					hasRate = hasRate || value != 0
				}
			}
		case 0x9f:
			value, valueOK := readEBMLUint(content, payloadOffset, payloadSize)
			hasChannels = valueOK && value > 0
		}
		offset = childEnd
	}
	if expected == "video" {
		return hasWidth && hasHeight
	}
	return hasRate && hasChannels
}

func readEBMLUint(content io.ReaderAt, offset, size int64) (uint64, bool) {
	if size <= 0 || size > 8 {
		return 0, false
	}
	data, ok := readAtExact(content, offset, int(size))
	if !ok {
		return 0, false
	}
	var value uint64
	for _, part := range data {
		value = value<<8 | uint64(part)
	}
	return value, true
}

func ebmlTrackMatches(expected string, trackType uint64, codec string) bool {
	codec = strings.ToUpper(strings.TrimSpace(codec))
	if expected == "video" && trackType == 1 {
		switch codec {
		case "V_VP8", "V_VP9", "V_AV1", "V_MPEG4/ISO/AVC", "V_MPEGH/ISO/HEVC", "V_MPEG2":
			return true
		}
	}
	if expected == "audio" && trackType == 2 {
		switch codec {
		case "A_OPUS", "A_VORBIS", "A_AAC", "A_MPEG/L3", "A_FLAC", "A_AC3", "A_EAC3":
			return true
		}
	}
	return false
}

func validEBMLCluster(content io.ReaderAt, offset, size, elementEnd, total int64, tracks map[uint64]string) bool {
	end := elementEnd
	if size == ebmlUnknownSize {
		end = total
	}
	for offset < end {
		id, _, payloadOffset, payloadSize, childEnd, ok := readEBMLElement(content, end, offset)
		if !ok {
			return false
		}
		if id == 0xa3 && payloadSize >= 5 && validEBMLBlockPayload(content, payloadOffset, payloadSize, tracks) {
			return true
		}
		if id == 0xa0 && payloadSize != ebmlUnknownSize {
			groupOffset := payloadOffset
			groupEnd := payloadOffset + payloadSize
			for groupOffset < groupEnd {
				groupID, _, groupPayloadOffset, groupPayloadSize, groupEndOffset, groupOK := readEBMLElement(content, groupEnd, groupOffset)
				if !groupOK {
					return false
				}
				if groupID == 0xa1 && groupPayloadSize >= 5 && validEBMLBlockPayload(content, groupPayloadOffset, groupPayloadSize, tracks) {
					return true
				}
				if groupPayloadSize == ebmlUnknownSize {
					return false
				}
				groupOffset = groupEndOffset
			}
		}
		if payloadSize == ebmlUnknownSize {
			return false
		}
		offset = childEnd
	}
	return false
}

func validEBMLBlockPayload(content io.ReaderAt, offset, size int64, tracks map[uint64]string) bool {
	trackNumber, trackSize, unknown, ok := readEBMLSize(content, offset, offset+size)
	if !ok || unknown || trackNumber == 0 || trackSize <= 0 {
		return false
	}
	codec, ok := tracks[trackNumber]
	if !ok || size < int64(trackSize)+4 {
		return false
	}
	frameOffset := offset + int64(trackSize) + 3
	frameSize := size - int64(trackSize) - 3
	if frameSize <= 1 {
		return false
	}
	frame, ok := readAtExact(content, frameOffset, int(frameSize))
	return ok && validEBMLFramePayload(codec, frame)
}

func validEBMLFramePayload(codec string, frame []byte) bool {
	if len(frame) < 4 || !containsNonUniformPayload(frame) {
		return false
	}
	switch strings.ToUpper(codec) {
	case "V_VP8":
		return frame[0]&0x01 == 0 && len(frame) >= 10 && bytes.Equal(frame[3:6], []byte{0x9d, 0x01, 0x2a})
	case "V_VP9":
		return frame[0]&0xc0 == 0x80
	case "V_AV1":
		return containsAV1OBU(frame)
	case "V_MPEG4/ISO/AVC":
		return containsH264NAL(frame)
	case "V_MPEGH/ISO/HEVC":
		return containsHEVCNAL(frame)
	case "V_MPEG2":
		return containsMPEGVideoStartCode(frame)
	case "A_OPUS":
		return frame[0]&0x03 != 0x03
	case "A_VORBIS", "A_AAC":
		return true
	case "A_MPEG/L3":
		return containsMPEGAudioFrame(frame)
	case "A_FLAC":
		return frame[0] == 0xff && frame[1]&0xfc == 0xf8
	case "A_AC3", "A_EAC3":
		return containsAC3SyncFrame(frame)
	default:
		return false
	}
}

func readEBMLElement(content io.ReaderAt, total, offset int64) (uint64, int, int64, int64, int64, bool) {
	id, idSize, ok := readEBMLID(content, offset, total)
	if !ok {
		return 0, 0, 0, 0, 0, false
	}
	size, sizeSize, unknown, ok := readEBMLSize(content, offset+int64(idSize), total)
	if !ok {
		return 0, 0, 0, 0, 0, false
	}
	payloadOffset := offset + int64(idSize+sizeSize)
	if unknown {
		return id, idSize + sizeSize, payloadOffset, ebmlUnknownSize, total, payloadOffset <= total
	}
	if size > uint64(total-payloadOffset) {
		return 0, 0, 0, 0, 0, false
	}
	return id, idSize + sizeSize, payloadOffset, int64(size), payloadOffset + int64(size), true
}

func readEBMLID(content io.ReaderAt, offset, total int64) (uint64, int, bool) {
	first, ok := readAtExact(content, offset, 1)
	if !ok {
		return 0, 0, false
	}
	width := 1
	mask := byte(0x80)
	for first[0]&mask == 0 && width <= 4 {
		width++
		mask >>= 1
	}
	if width > 4 || offset+int64(width) > total {
		return 0, 0, false
	}
	data, ok := readAtExact(content, offset, width)
	if !ok {
		return 0, 0, false
	}
	var value uint64
	for _, part := range data {
		value = value<<8 | uint64(part)
	}
	return value, width, true
}

func readEBMLSize(content io.ReaderAt, offset, total int64) (uint64, int, bool, bool) {
	first, ok := readAtExact(content, offset, 1)
	if !ok {
		return 0, 0, false, false
	}
	width := 1
	mask := byte(0x80)
	for first[0]&mask == 0 && width <= 8 {
		width++
		mask >>= 1
	}
	if width > 8 || offset+int64(width) > total {
		return 0, 0, false, false
	}
	data, ok := readAtExact(content, offset, width)
	if !ok {
		return 0, 0, false, false
	}
	value := uint64(data[0] & (mask - 1))
	for _, part := range data[1:] {
		value = value<<8 | uint64(part)
	}
	unknown := value == (uint64(1)<<(7*width))-1
	return value, width, unknown, true
}

func validOggFile(expected string, content io.ReaderAt, total int64) bool {
	if total < 27 {
		return false
	}
	offset := int64(0)
	var serial uint32
	var sequence uint32
	packet := make([]byte, 0, 4096)
	codec := ""
	requiredHeaders := 0
	headerPackets := 0
	dataPackets := 0
	pages := 0
	for offset < total {
		header, ok := readAtExact(content, offset, 27)
		if !ok || !bytes.Equal(header[:4], []byte("OggS")) || header[4] != 0 {
			return false
		}
		segmentCount := int(header[26])
		segments, ok := readAtExact(content, offset+27, segmentCount)
		if !ok {
			return false
		}
		payloadSize := 0
		for _, segment := range segments {
			payloadSize += int(segment)
		}
		pageSize := int64(27 + segmentCount + payloadSize)
		if pageSize > total-offset {
			return false
		}
		page, ok := readAtExact(content, offset, int(pageSize))
		if !ok || !validOggPageChecksum(page) {
			return false
		}
		pageSerial := binary.LittleEndian.Uint32(header[14:18])
		pageSequence := binary.LittleEndian.Uint32(header[18:22])
		if pages == 0 {
			serial = pageSerial
		} else if pageSerial != serial || pageSequence != sequence+1 {
			return false
		}
		if header[5]&0x01 != 0 && len(packet) == 0 {
			return false
		}
		bodyOffset := 27 + segmentCount
		for _, segment := range segments {
			segmentSize := int(segment)
			packet = append(packet, page[bodyOffset:bodyOffset+segmentSize]...)
			bodyOffset += segmentSize
			if segment == 255 {
				if len(packet) > 1<<20 {
					return false
				}
				continue
			}
			if len(packet) == 0 {
				return false
			}
			if codec == "" {
				codec, requiredHeaders = oggCodec(expected, packet)
				if codec == "" {
					return false
				}
				headerPackets = 1
			} else if headerPackets < requiredHeaders {
				if !validOggCodecHeader(codec, headerPackets, packet) {
					return false
				}
				headerPackets++
			} else {
				if !validOggDataPacket(codec, packet) {
					return false
				}
				dataPackets++
			}
			packet = packet[:0]
		}
		offset += pageSize
		pages++
		sequence = pageSequence
	}
	return pages > 0 && offset == total && len(packet) == 0 && dataPackets > 0
}

func oggCodec(expected string, packet []byte) (string, int) {
	if expected == "audio" {
		switch {
		case bytes.HasPrefix(packet, []byte("OpusHead")):
			return "opus", 2
		case len(packet) >= 7 && packet[0] == 1 && bytes.Equal(packet[1:7], []byte("vorbis")):
			return "vorbis", 3
		case bytes.HasPrefix(packet, []byte("Speex   ")):
			return "speex", 2
		case len(packet) >= 5 && packet[0] == 0x7f && bytes.Equal(packet[1:5], []byte("FLAC")):
			return "flac", 2
		}
		return "", 0
	}
	if len(packet) >= 42 && packet[0] == 0x80 && bytes.Equal(packet[1:7], []byte("theora")) && (packet[10] != 0 || packet[11] != 0 || packet[12] != 0 || packet[13] != 0) {
		return "theora", 3
	}
	if bytes.HasPrefix(packet, []byte("OVP8")) {
		return "vp8", 2
	}
	return "", 0
}

func validOggCodecHeader(codec string, headerIndex int, packet []byte) bool {
	if headerIndex <= 0 || len(packet) == 0 {
		return false
	}
	switch codec {
	case "opus":
		return headerIndex == 1 && len(packet) > len("OpusTags") && bytes.HasPrefix(packet, []byte("OpusTags"))
	case "vp8":
		return headerIndex == 1 && len(packet) >= 6 && bytes.HasPrefix(packet, []byte("OVP8")) && packet[4] == 0x30 && packet[5] == 0x02
	case "vorbis":
		if headerIndex > 2 || len(packet) < 7 || !bytes.Equal(packet[1:7], []byte("vorbis")) {
			return false
		}
		return packet[0] == map[int]byte{1: 3, 2: 5}[headerIndex]
	case "speex":
		return headerIndex == 1 && len(packet) >= 2
	case "flac":
		return headerIndex == 1 && len(packet) >= 4
	case "theora":
		return headerIndex <= 2 && len(packet) >= 7 && packet[0] == byte(0x80+headerIndex) && bytes.Equal(packet[1:7], []byte("theora"))
	default:
		return false
	}
}

func validOggDataPacket(codec string, packet []byte) bool {
	if len(packet) < 2 || !containsNonUniformPayload(packet) {
		return false
	}
	switch codec {
	case "opus":
		return packet[0]&0x03 != 0x03
	case "vp8":
		return bytes.Contains(packet, []byte{0x9d, 0x01, 0x2a})
	case "vorbis":
		return packet[0]&0x01 == 0
	case "theora":
		return packet[0]&0xc0 == 0
	default:
		return true
	}
}

func validOggPageChecksum(page []byte) bool {
	if len(page) < 27 {
		return false
	}
	want := binary.LittleEndian.Uint32(page[22:26])
	page[22], page[23], page[24], page[25] = 0, 0, 0, 0
	return oggCRC(page) == want
}

func oggCRC(data []byte) uint32 {
	var crc uint32
	for _, value := range data {
		crc ^= uint32(value) << 24
		for bit := 0; bit < 8; bit++ {
			if crc&0x80000000 != 0 {
				crc = crc<<1 ^ 0x04c11db7
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

func validWAVFile(content io.ReaderAt, total int64) bool {
	header, ok := readAtExact(content, 0, 12)
	if !ok || !bytes.Equal(header[:4], []byte("RIFF")) || !bytes.Equal(header[8:12], []byte("WAVE")) {
		return false
	}
	declaredSize := uint64(binary.LittleEndian.Uint32(header[4:8]))
	if declaredSize+8 != uint64(total) {
		return false
	}
	offset := int64(12)
	fmtFound := false
	dataFound := false
	for offset < total {
		chunk, ok := readAtExact(content, offset, 8)
		if !ok {
			return false
		}
		chunkSize := uint64(binary.LittleEndian.Uint32(chunk[4:8]))
		end := offset + 8 + int64(chunkSize)
		if end > total {
			return false
		}
		switch string(chunk[:4]) {
		case "fmt ":
			if chunkSize < 16 {
				return false
			}
			format, ok := readAtExact(content, offset+8, 16)
			if !ok || binary.LittleEndian.Uint16(format[:2]) == 0 || binary.LittleEndian.Uint16(format[2:4]) == 0 || binary.LittleEndian.Uint32(format[4:8]) == 0 {
				return false
			}
			fmtFound = true
		case "data":
			if chunkSize > 0 {
				dataFound = true
			}
		}
		offset = end
		if offset%2 != 0 {
			offset++
		}
	}
	return offset == total && fmtFound && dataFound
}

func validFLACFile(content io.ReaderAt, total int64) bool {
	header, ok := readAtExact(content, 0, 4)
	if !ok || !bytes.Equal(header, []byte("fLaC")) {
		return false
	}
	offset := int64(4)
	streamInfo := false
	for offset+4 <= total {
		block, ok := readAtExact(content, offset, 4)
		if !ok {
			return false
		}
		last := block[0]&0x80 != 0
		blockType := block[0] & 0x7f
		blockSize := int64(uint32(block[1])<<16 | uint32(block[2])<<8 | uint32(block[3]))
		offset += 4
		if blockSize > total-offset {
			return false
		}
		if blockType == 0 {
			if blockSize != 34 {
				return false
			}
			streamInfo = true
		}
		offset += blockSize
		if last {
			frame, ok := readAtExact(content, offset, 4)
			return streamInfo && ok && frame[0] == 0xff && frame[1]&0xfc == 0xf8
		}
	}
	return false
}

func validMPEGFile(content io.ReaderAt, total int64) bool {
	if total >= 10 {
		header, ok := readAtExact(content, 0, 10)
		if ok && bytes.Equal(header[:3], []byte("ID3")) {
			tagSize := int64(header[6]&0x7f)<<21 | int64(header[7]&0x7f)<<14 | int64(header[8]&0x7f)<<7 | int64(header[9]&0x7f)
			offset := int64(10) + tagSize
			if header[5]&0x10 != 0 {
				offset += 10
			}
			return validMPEGFrameInRange(content, total, offset, minInt64(total, offset+4096))
		}
	}
	return validMPEGFrameInRange(content, total, 0, minInt64(total, 4096))
}

func validMPEGFrameInRange(content io.ReaderAt, total, start, end int64) bool {
	if start < 0 || start >= total {
		return false
	}
	if end > total {
		end = total
	}
	for offset := start; offset+4 <= end; offset++ {
		header, ok := readAtExact(content, offset, 4)
		if !ok || !isMPEGAudioFrame(header) {
			continue
		}
		frameLength, ok := mpegFrameLength(header)
		if ok && frameLength >= 4 && offset+int64(frameLength) <= total {
			return validMPEGFrameStream(content, total, offset)
		}
	}
	return false
}

func validMPEGFrameStream(content io.ReaderAt, total, offset int64) bool {
	frames := 0
	for offset < total {
		header, ok := readAtExact(content, offset, 4)
		if !ok || !isMPEGAudioFrame(header) {
			return false
		}
		frameLength, ok := mpegFrameLength(header)
		if !ok || frameLength < 4 || offset+int64(frameLength) > total {
			return false
		}
		offset += int64(frameLength)
		frames++
	}
	return frames > 0 && offset == total
}

func mpegFrameLength(header []byte) (int, bool) {
	if len(header) < 4 || !isMPEGAudioFrame(header) {
		return 0, false
	}
	value := binary.BigEndian.Uint32(header[:4])
	version := (value >> 19) & 0x3
	layer := (value >> 17) & 0x3
	bitrateIndex := (value >> 12) & 0xf
	sampleRateIndex := (value >> 10) & 0x3
	padding := (value >> 9) & 0x1
	bitratesMPEG1 := [...]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0}
	bitratesMPEG2 := [...]int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0}
	if bitrateIndex == 0 || bitrateIndex == 15 || sampleRateIndex == 3 {
		return 0, false
	}
	bitrate := bitratesMPEG2[bitrateIndex]
	if version == 3 {
		bitrate = bitratesMPEG1[bitrateIndex]
	}
	if bitrate == 0 {
		return 0, false
	}
	sampleRates := [...]int{44100, 48000, 32000}
	sampleRate := sampleRates[sampleRateIndex]
	switch version {
	case 2:
		sampleRate /= 2
	case 0:
		sampleRate /= 4
	}
	if sampleRate == 0 {
		return 0, false
	}
	if layer == 3 {
		return (12*bitrate*1000/sampleRate + int(padding)) * 4, true
	}
	if layer == 1 && version != 3 {
		return 72 * bitrate * 1000 / sampleRate, true
	}
	coefficient := 144
	if version != 3 {
		coefficient = 72
	}
	return coefficient*bitrate*1000/sampleRate + int(padding), true
}

func validMPEGTransportStreamFile(expected string, content io.ReaderAt, total int64) bool {
	if total < 188*4 || total%188 != 0 {
		return false
	}
	packetCount := total / 188
	if packetCount > 8192 {
		packetCount = 8192
	}
	var pmtPID uint16
	streamPIDs := make(map[uint16]struct{})
	streamTypes := make(map[uint16]byte)
	hasPES := false
	for index := int64(0); index < packetCount; index++ {
		packet, ok := readAtExact(content, index*188, 188)
		if !ok {
			return false
		}
		pid, payload, start, ok := mpegTSPayload(packet)
		if !ok {
			return false
		}
		if pid == 0 && start {
			if candidate, parsed := parseMPEGTSProgramMapPID(payload); parsed {
				pmtPID = candidate
			}
		} else if pmtPID != 0 && pid == pmtPID && start {
			for streamPID, streamType := range parseMPEGTSPMT(payload) {
				if mpegTSStreamMatches(expected, streamType) {
					streamPIDs[streamPID] = struct{}{}
					streamTypes[streamPID] = streamType
				}
			}
		} else if streamType, ok := streamTypes[pid]; ok && start {
			if validMPEGTSPESPayload(expected, streamType, payload) {
				hasPES = true
			}
		}
	}
	return pmtPID != 0 && len(streamTypes) > 0 && hasPES
}

func validMPEGTSPESPayload(expected string, streamType byte, payload []byte) bool {
	if len(payload) < 9 || !bytes.Equal(payload[:3], []byte{0, 0, 1}) {
		return false
	}
	mediaOffset := 9 + int(payload[8])
	if mediaOffset >= len(payload) {
		return false
	}
	media := payload[mediaOffset:]
	if !containsNonUniformPayload(media) {
		return false
	}
	if expected == "video" {
		switch streamType {
		case 0x01, 0x02:
			return containsMPEGVideoStartCode(media)
		case 0x1b:
			return containsH264NAL(media)
		case 0x24:
			return containsHEVCNAL(media)
		default:
			return false
		}
	}
	switch streamType {
	case 0x03, 0x04:
		return containsMPEGAudioFrame(media)
	case 0x0f:
		return containsAACADTSFrame(media)
	case 0x11:
		return containsAACLOASFrame(media)
	case 0x81, 0x87:
		return containsAC3SyncFrame(media)
	default:
		return false
	}
}

func containsMPEGVideoStartCode(data []byte) bool {
	for offset := 0; offset+4 <= len(data); offset++ {
		if data[offset] != 0 || data[offset+1] != 0 || data[offset+2] != 1 {
			continue
		}
		switch data[offset+3] {
		case 0x00, 0xb3, 0xb8:
			return true
		}
	}
	return false
}

func containsMPEGAudioFrame(data []byte) bool {
	for offset := 0; offset+4 <= len(data); offset++ {
		if !isMPEGAudioFrame(data[offset : offset+4]) {
			continue
		}
		frameLength, ok := mpegFrameLength(data[offset : offset+4])
		if ok && frameLength >= 4 && offset+frameLength <= len(data) {
			return true
		}
	}
	return false
}

func containsAACADTSFrame(data []byte) bool {
	for offset := 0; offset+7 <= len(data); offset++ {
		if data[offset] != 0xff || data[offset+1]&0xf6 != 0xf0 {
			continue
		}
		headerSize := 7
		if data[offset+1]&0x01 == 0 {
			headerSize = 9
		}
		frameLength := int(data[offset+3]&0x03)<<11 | int(data[offset+4])<<3 | int(data[offset+5]>>5)
		if frameLength >= headerSize+2 && offset+frameLength <= len(data) {
			return true
		}
	}
	return false
}

func containsAACLOASFrame(data []byte) bool {
	for offset := 0; offset+3 <= len(data); offset++ {
		if data[offset] == 0x56 && data[offset+1]&0xe0 == 0xe0 {
			frameLength := int(data[offset+1]&0x1f)<<8 | int(data[offset+2])
			if frameLength >= 3 && offset+3+frameLength <= len(data) {
				return true
			}
		}
	}
	return false
}

func containsAC3SyncFrame(data []byte) bool {
	for offset := 0; offset+8 <= len(data); offset++ {
		if data[offset] == 0x0b && data[offset+1] == 0x77 {
			return true
		}
	}
	return false
}

func mpegTSPayload(packet []byte) (uint16, []byte, bool, bool) {
	if len(packet) != 188 || packet[0] != 0x47 || packet[1]&0x80 != 0 {
		return 0, nil, false, false
	}
	pid := uint16(packet[1]&0x1f)<<8 | uint16(packet[2])
	adaptationControl := (packet[3] >> 4) & 0x3
	if adaptationControl == 0 {
		return 0, nil, false, false
	}
	offset := 4
	if adaptationControl == 2 || adaptationControl == 3 {
		adaptationLength := int(packet[4])
		if adaptationLength > 183 || 5+adaptationLength > len(packet) {
			return 0, nil, false, false
		}
		offset += 1 + adaptationLength
	}
	if offset > len(packet) {
		return 0, nil, false, false
	}
	return pid, packet[offset:], packet[1]&0x40 != 0, true
}

func parseMPEGTSProgramMapPID(payload []byte) (uint16, bool) {
	section, ok := mpegTSSection(payload, 0)
	if !ok || len(section) < 12 || section[0] != 0 {
		return 0, false
	}
	for offset := 8; offset+4 <= len(section)-4; offset += 4 {
		program := binary.BigEndian.Uint16(section[offset : offset+2])
		if program == 0 {
			continue
		}
		return binary.BigEndian.Uint16(section[offset+2:offset+4]) & 0x1fff, true
	}
	return 0, false
}

func parseMPEGTSPMT(payload []byte) map[uint16]byte {
	streams := make(map[uint16]byte)
	section, ok := mpegTSSection(payload, 2)
	if !ok || len(section) < 12 {
		return streams
	}
	programInfoLength := int(binary.BigEndian.Uint16(section[10:12]) & 0x0fff)
	offset := 12 + programInfoLength
	for offset+5 <= len(section)-4 {
		streamType := section[offset]
		pid := binary.BigEndian.Uint16(section[offset+1:offset+3]) & 0x1fff
		esInfoLength := int(binary.BigEndian.Uint16(section[offset+3:offset+5]) & 0x0fff)
		if offset+5+esInfoLength > len(section)-4 {
			return make(map[uint16]byte)
		}
		streams[pid] = streamType
		offset += 5 + esInfoLength
	}
	return streams
}

func mpegTSSection(payload []byte, tableID byte) ([]byte, bool) {
	if len(payload) == 0 {
		return nil, false
	}
	pointer := int(payload[0])
	if pointer+3 > len(payload) {
		return nil, false
	}
	section := payload[1+pointer:]
	if len(section) < 3 || section[0] != tableID {
		return nil, false
	}
	sectionLength := int(binary.BigEndian.Uint16(section[1:3]) & 0x0fff)
	if sectionLength < 4 || 3+sectionLength > len(section) {
		return nil, false
	}
	return section[:3+sectionLength], true
}

func mpegTSStreamMatches(expected string, streamType byte) bool {
	if expected == "video" {
		switch streamType {
		case 0x01, 0x02, 0x1b, 0x24:
			return true
		}
		return false
	}
	switch streamType {
	case 0x03, 0x04, 0x0f, 0x11, 0x81, 0x87:
		return true
	default:
		return false
	}
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func sniffMediaMIME(expected string, prefix []byte) string {
	detected := strings.ToLower(strings.TrimSpace(strings.SplitN(http.DetectContentType(prefix), ";", 2)[0]))
	if normalizedMediaType(expected) == "audio" {
		switch {
		case detected == "video/mp4" && isISOBaseMedia(prefix):
			detected = "audio/mp4"
		case detected == "video/webm" && isEBML(prefix):
			detected = "audio/webm"
		case detected == "video/ogg" && bytes.HasPrefix(prefix, []byte("OggS")):
			detected = "audio/ogg"
		}
	}
	if detected != "application/octet-stream" {
		return detected
	}
	if isISOBaseMedia(prefix) {
		if normalizedMediaType(expected) == "audio" {
			return "audio/mp4"
		}
		return "video/mp4"
	}
	if isEBML(prefix) {
		if normalizedMediaType(expected) == "audio" {
			return "audio/webm"
		}
		return "video/webm"
	}
	if bytes.HasPrefix(prefix, []byte("OggS")) {
		if normalizedMediaType(expected) == "audio" {
			return "audio/ogg"
		}
		return "video/ogg"
	}
	if isWAV(prefix) {
		return "audio/wav"
	}
	if bytes.HasPrefix(prefix, []byte("fLaC")) {
		return "audio/flac"
	}
	if bytes.HasPrefix(prefix, []byte("ID3")) || isMPEGAudioFrame(prefix) {
		return "audio/mpeg"
	}
	if isMPEGTransportStream(prefix) {
		return "video/mp2t"
	}
	return detected
}

func isVideoBytes(prefix []byte) bool {
	return isISOBaseMedia(prefix) || isEBML(prefix) || bytes.HasPrefix(prefix, []byte("OggS")) || isMPEGTransportStream(prefix)
}

func isAudioBytes(prefix []byte) bool {
	return isISOBaseMedia(prefix) || isEBML(prefix) || bytes.HasPrefix(prefix, []byte("OggS")) || isWAV(prefix) || bytes.HasPrefix(prefix, []byte("fLaC")) || bytes.HasPrefix(prefix, []byte("ID3")) || isMPEGAudioFrame(prefix)
}

func isISOBaseMedia(prefix []byte) bool {
	if len(prefix) < 16 || !bytes.Equal(prefix[4:8], []byte("ftyp")) {
		return false
	}
	boxSize := uint64(binary.BigEndian.Uint32(prefix[:4]))
	brandOffset := 8
	if boxSize == 1 {
		if len(prefix) < 24 {
			return false
		}
		boxSize = binary.BigEndian.Uint64(prefix[8:16])
		brandOffset = 16
	}
	if boxSize < uint64(brandOffset+8) || !isASCIIContainerBrand(prefix[brandOffset:brandOffset+4]) {
		return false
	}
	return true
}

func isEBML(prefix []byte) bool {
	return len(prefix) >= 8 && bytes.Equal(prefix[:4], []byte{0x1a, 0x45, 0xdf, 0xa3}) &&
		(bytes.Contains(prefix, []byte("webm")) || bytes.Contains(prefix, []byte("matroska")))
}

func isWAV(prefix []byte) bool {
	if len(prefix) < 20 || !bytes.Equal(prefix[:4], []byte("RIFF")) || !bytes.Equal(prefix[8:12], []byte("WAVE")) {
		return false
	}
	for offset := 12; offset+8 <= len(prefix) && offset < 512; {
		chunkSize := int(binary.LittleEndian.Uint32(prefix[offset+4 : offset+8]))
		if bytes.Equal(prefix[offset:offset+4], []byte("fmt ")) {
			return chunkSize >= 16 && offset+8+16 <= len(prefix)
		}
		if chunkSize < 0 || offset+8+chunkSize > len(prefix) {
			return false
		}
		offset += 8 + chunkSize
		if offset%2 != 0 {
			offset++
		}
	}
	return false
}

func isMPEGAudioFrame(prefix []byte) bool {
	if len(prefix) < 4 || prefix[0] != 0xff || prefix[1]&0xe0 != 0xe0 {
		return false
	}
	header := binary.BigEndian.Uint32(prefix[:4])
	version := (header >> 19) & 0x3
	layer := (header >> 17) & 0x3
	bitrate := (header >> 12) & 0xf
	sampleRate := (header >> 10) & 0x3
	return version != 1 && layer != 0 && bitrate != 0 && bitrate != 15 && sampleRate != 3
}

func isASCIIContainerBrand(value []byte) bool {
	if len(value) != 4 {
		return false
	}
	for _, character := range value {
		if character != ' ' &&
			(character < '0' || character > '9') &&
			(character < 'A' || character > 'Z') &&
			(character < 'a' || character > 'z') {
			return false
		}
	}
	return true
}

func isMPEGTransportStream(prefix []byte) bool {
	if len(prefix) < 188*2+1 {
		return false
	}
	return prefix[0] == 0x47 && prefix[188] == 0x47 && prefix[376] == 0x47
}

func unsafeMediaMIME(value string) bool {
	return strings.HasPrefix(value, "text/") || value == "application/json" || value == "application/xml" || value == "application/xhtml+xml" || value == "application/javascript" || value == "application/x-javascript" || value == "image/svg+xml"
}

func normalizedMediaNamespace(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "bsky":
		return "bsky"
	case "mastodon":
		return "mastodon"
	}
	return "x"
}

// MediaNamespaceForSourceType keeps all Bluesky source types in the same
// vault namespace, including child sources introduced by later importers.
func MediaNamespaceForSourceType(sourceType string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(sourceType)), "bsky_") {
		return "bsky"
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(sourceType)), "mastodon_") {
		return "mastodon"
	}
	return "x"
}

func isHLSPlaylist(mediaType, rawURL string, prefix []byte) bool {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if mediaType == "application/vnd.apple.mpegurl" || mediaType == "application/x-mpegurl" || mediaType == "audio/mpegurl" {
		return true
	}
	if parsed, err := neturl.Parse(rawURL); err == nil && strings.EqualFold(filepath.Ext(parsed.Path), ".m3u8") {
		return true
	}
	return bytes.HasPrefix(bytes.TrimSpace(prefix), []byte("#EXTM3U"))
}

func normalizedMediaType(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	switch value {
	case "photo", "video", "animated_gif", "audio":
		return value
	default:
		return "unknown"
	}
}

func mediaExtension(mediaType, rawURL string) string {
	switch mediaType {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "video/mp4":
		return ".mp4"
	case "video/quicktime":
		return ".mov"
	case "audio/mpeg":
		return ".mp3"
	case "audio/ogg":
		return ".ogg"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	}

	if exts, err := mime.ExtensionsByType(mediaType); err == nil && len(exts) > 0 {
		return exts[0]
	}

	parsed, err := neturl.Parse(rawURL)
	if err == nil {
		ext := strings.ToLower(filepath.Ext(parsed.Path))
		if ext != "" {
			return ext
		}
	}
	return ".bin"
}
