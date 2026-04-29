package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/mediaarchive"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/runtimeenv"
)

const defaultSignedURLTTL = 5 * time.Minute

type archiveProxy interface {
	GetObject(ctx context.Context, bucket, key, rangeHeader string) (archiveObject, error)
	HeadObject(ctx context.Context, bucket, key string) (archiveObject, error)
	PresignGetObject(ctx context.Context, bucket, key string, ttl time.Duration) (string, time.Time, error)
}

type archiveObject struct {
	Body          io.ReadCloser
	ContentType   string
	ContentLength int64
	ContentRange  string
	ETag          string
	LastModified  time.Time
}

type s3ArchiveProxy struct {
	client  *s3.Client
	presign *s3.PresignClient
}

type signedURLResponse struct {
	AssetID   int64  `json:"asset_id"`
	URL       string `json:"url"`
	ExpiresAt string `json:"expires_at"`
	Bucket    string `json:"bucket"`
	Key       string `json:"key"`
	ProxyURL  string `json:"proxy_url"`
	MediaType string `json:"media_type"`
	Source    string `json:"source"`
}

func newArchiveProxy(cfg config.Config) (archiveProxy, error) {
	endpoint := strings.TrimSpace(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_R2_ENDPOINT", "DBRAIN_S3_ENDPOINT"))
	accessKeyID := strings.TrimSpace(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_R2_ACCESS_KEY_ID", "DBRAIN_S3_ACCESS_KEY_ID", "AWS_ACCESS_KEY_ID"))
	secretKey := strings.TrimSpace(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_R2_SECRET_ACCESS_KEY", "DBRAIN_S3_SECRET_ACCESS_KEY", "AWS_SECRET_ACCESS_KEY"))
	if endpoint == "" || accessKeyID == "" || secretKey == "" {
		return nil, nil
	}

	client, err := mediaarchive.NewS3Client(mediaarchive.Options{
		Endpoint:     endpoint,
		Region:       firstNonEmptyString(strings.TrimSpace(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_R2_REGION", "DBRAIN_S3_REGION")), "auto"),
		AccessKeyID:  accessKeyID,
		SecretKey:    secretKey,
		SessionToken: strings.TrimSpace(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_R2_SESSION_TOKEN", "DBRAIN_S3_SESSION_TOKEN", "AWS_SESSION_TOKEN")),
		PathStyle:    true,
	})
	if err != nil {
		return nil, err
	}

	return &s3ArchiveProxy{
		client:  client,
		presign: s3.NewPresignClient(client),
	}, nil
}

func (p *s3ArchiveProxy) GetObject(ctx context.Context, bucket, key, rangeHeader string) (archiveObject, error) {
	input := &s3.GetObjectInput{
		Bucket: aws.String(strings.TrimSpace(bucket)),
		Key:    aws.String(strings.TrimSpace(key)),
	}
	if strings.TrimSpace(rangeHeader) != "" {
		input.Range = aws.String(strings.TrimSpace(rangeHeader))
	}
	output, err := p.client.GetObject(ctx, input)
	if err != nil {
		return archiveObject{}, err
	}
	return archiveObject{
		Body:          output.Body,
		ContentType:   strings.TrimSpace(aws.ToString(output.ContentType)),
		ContentLength: aws.ToInt64(output.ContentLength),
		ContentRange:  strings.TrimSpace(aws.ToString(output.ContentRange)),
		ETag:          strings.Trim(strings.TrimSpace(aws.ToString(output.ETag)), `"`),
		LastModified:  aws.ToTime(output.LastModified),
	}, nil
}

func (p *s3ArchiveProxy) HeadObject(ctx context.Context, bucket, key string) (archiveObject, error) {
	output, err := p.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(strings.TrimSpace(bucket)),
		Key:    aws.String(strings.TrimSpace(key)),
	})
	if err != nil {
		return archiveObject{}, err
	}
	return archiveObject{
		ContentType:   strings.TrimSpace(aws.ToString(output.ContentType)),
		ContentLength: aws.ToInt64(output.ContentLength),
		ETag:          strings.Trim(strings.TrimSpace(aws.ToString(output.ETag)), `"`),
		LastModified:  aws.ToTime(output.LastModified),
	}, nil
}

func (p *s3ArchiveProxy) PresignGetObject(ctx context.Context, bucket, key string, ttl time.Duration) (string, time.Time, error) {
	if ttl <= 0 {
		ttl = defaultSignedURLTTL
	}
	output, err := p.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(strings.TrimSpace(bucket)),
		Key:    aws.String(strings.TrimSpace(key)),
	}, func(opts *s3.PresignOptions) {
		opts.Expires = ttl
	})
	if err != nil {
		return "", time.Time{}, err
	}
	return output.URL, time.Now().UTC().Add(ttl), nil
}

func (s *server) handleMediaAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeMethodNotAllowed(w, http.MethodGet, http.MethodHead)
		return
	}
	asset, ok := s.loadArchivedMediaAsset(w, r)
	if !ok {
		return
	}
	if s.archive == nil {
		writeMessage(w, http.StatusServiceUnavailable, "archive media proxy is not configured")
		return
	}

	var (
		obj archiveObject
		err error
	)
	if r.Method == http.MethodHead {
		obj, err = s.archive.HeadObject(r.Context(), asset.ArchiveBucket, asset.ArchiveKey)
	} else {
		obj, err = s.archive.GetObject(r.Context(), asset.ArchiveBucket, asset.ArchiveKey, r.Header.Get("Range"))
	}
	if err != nil {
		s.writeArchiveProxyError(w, err)
		return
	}
	if obj.Body != nil {
		defer func() {
			_ = obj.Body.Close()
		}()
	}

	writeArchiveHeaders(w.Header(), asset, obj)
	status := http.StatusOK
	if strings.TrimSpace(r.Header.Get("Range")) != "" && strings.TrimSpace(obj.ContentRange) != "" {
		status = http.StatusPartialContent
	}
	w.WriteHeader(status)
	if r.Method == http.MethodHead || obj.Body == nil {
		return
	}
	if _, err := io.Copy(w, obj.Body); err != nil {
		return
	}
}

func (s *server) handleMediaSignedURL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	asset, ok := s.loadArchivedMediaAsset(w, r)
	if !ok {
		return
	}
	if s.archive == nil {
		writeMessage(w, http.StatusServiceUnavailable, "archive media proxy is not configured")
		return
	}

	ttl := parseSignedURLTTL(r.URL.Query().Get("ttl"))
	signedURL, expiresAt, err := s.archive.PresignGetObject(r.Context(), asset.ArchiveBucket, asset.ArchiveKey, ttl)
	if err != nil {
		s.writeArchiveProxyError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, signedURLResponse{
		AssetID:   asset.ID,
		URL:       signedURL,
		ExpiresAt: expiresAt.Format(time.RFC3339),
		Bucket:    asset.ArchiveBucket,
		Key:       asset.ArchiveKey,
		ProxyURL:  s.mediaProxyURL(asset.ID),
		MediaType: asset.MediaType,
		Source:    asset.LocalPath,
	})
}

func (s *server) loadArchivedMediaAsset(w http.ResponseWriter, r *http.Request) (model.MediaAsset, bool) {
	assetID, err := parseMediaAssetID(r)
	if err != nil {
		writeMessage(w, http.StatusBadRequest, err.Error())
		return model.MediaAsset{}, false
	}
	asset, err := s.store.GetMediaAsset(r.Context(), assetID)
	if err != nil {
		writeMessage(w, http.StatusNotFound, fmt.Sprintf("media asset not found: %d", assetID))
		return model.MediaAsset{}, false
	}
	if strings.TrimSpace(asset.ArchiveStatus) != "archived" || strings.TrimSpace(asset.ArchiveBucket) == "" || strings.TrimSpace(asset.ArchiveKey) == "" {
		writeMessage(w, http.StatusConflict, fmt.Sprintf("media asset %d is not archived", assetID))
		return model.MediaAsset{}, false
	}
	return asset, true
}

func parseMediaAssetID(r *http.Request) (int64, error) {
	raw := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/media/asset/"))
	if raw == "" || raw == r.URL.Path {
		raw = strings.TrimSpace(r.URL.Query().Get("id"))
	}
	raw = strings.Trim(raw, "/")
	if raw == "" {
		return 0, fmt.Errorf("media asset id is required")
	}
	assetID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || assetID <= 0 {
		return 0, fmt.Errorf("invalid media asset id: %q", raw)
	}
	return assetID, nil
}

func parseSignedURLTTL(raw string) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultSignedURLTTL
	}
	ttl, err := time.ParseDuration(raw)
	if err != nil {
		return defaultSignedURLTTL
	}
	if ttl < time.Minute {
		return time.Minute
	}
	if ttl > time.Hour {
		return time.Hour
	}
	return ttl
}

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

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func (s *server) mediaProxyURL(assetID int64) string {
	baseURL := strings.TrimSpace(s.proxyBase)
	if baseURL == "" {
		baseURL = mediaProxyBaseURL(s.cfg)
	}
	return strings.TrimRight(baseURL, "/") + "/media/asset/" + url.PathEscape(strconv.FormatInt(assetID, 10))
}

func mediaProxyBaseURL(cfg config.Config) string {
	baseURL := strings.TrimSpace(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_MEDIA_PROXY_BASE_URL", "DBRAIN_WEB_BASE_URL"))
	switch strings.ToLower(baseURL) {
	case "off", "none", "disabled":
		return ""
	}
	if baseURL == "" {
		return "http://" + defaultAddr
	}
	return baseURL
}
