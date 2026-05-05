package web

import (
	"errors"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/darron/dbrain/internal/model"
)

func writeArchiveHeaders(header http.Header, asset model.MediaAsset, obj archiveObject) {
	contentType := strings.TrimSpace(obj.ContentType)
	if contentType == "" {
		contentType = strings.TrimSpace(asset.MIMEType)
	}
	if contentType == "" {
		contentType = mime.TypeByExtension(filepath.Ext(asset.ArchiveKey))
	}
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	if obj.ContentLength > 0 {
		header.Set("Content-Length", strconv.FormatInt(obj.ContentLength, 10))
	}
	if strings.TrimSpace(obj.ContentRange) != "" {
		header.Set("Content-Range", obj.ContentRange)
	}
	if strings.TrimSpace(obj.ETag) != "" {
		header.Set("ETag", `"`+obj.ETag+`"`)
	}
	if !obj.LastModified.IsZero() {
		header.Set("Last-Modified", obj.LastModified.UTC().Format(http.TimeFormat))
	}
	header.Set("Accept-Ranges", "bytes")
	header.Set("Cache-Control", "private, max-age=60")
	filename := filepath.Base(firstNonEmptyString(strings.TrimSpace(asset.LocalPath), strings.TrimSpace(asset.ArchiveKey)))
	if filename != "" {
		header.Set("Content-Disposition", `inline; filename="`+sanitizeHeaderFilename(filename)+`"`)
	}
}

func sanitizeHeaderFilename(name string) string {
	name = strings.ReplaceAll(name, `"`, "")
	name = strings.ReplaceAll(name, "\n", "")
	name = strings.ReplaceAll(name, "\r", "")
	return name
}

func (s *server) writeArchiveProxyError(w http.ResponseWriter, err error) {
	switch {
	case isNoSuchKey(err):
		writeMessage(w, http.StatusNotFound, "archived object not found")
	case isInvalidRange(err):
		writeMessage(w, http.StatusRequestedRangeNotSatisfiable, "invalid byte range")
	default:
		writeError(w, http.StatusBadGateway, err)
	}
}

func isNoSuchKey(err error) bool {
	var noSuchKey *s3types.NoSuchKey
	if errors.As(err, &noSuchKey) {
		return true
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NoSuchKey", "NotFound":
			return true
		}
	}
	return false
}

func isInvalidRange(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode() == "InvalidRange"
	}
	return false
}
