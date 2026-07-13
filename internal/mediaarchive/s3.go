package mediaarchive

import (
	"context"
	"fmt"
	"mime"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/vaultfs"
)

type S3Uploader struct {
	client *s3.Client
}

func NewS3Uploader(opts Options) (*S3Uploader, error) {
	client, err := NewS3Client(opts)
	if err != nil {
		return nil, err
	}
	return &S3Uploader{client: client}, nil
}

func NewS3Client(opts Options) (*s3.Client, error) {
	cfg := aws.Config{
		Region: strings.TrimSpace(opts.Region),
		Credentials: aws.NewCredentialsCache(
			credentials.NewStaticCredentialsProvider(
				strings.TrimSpace(opts.AccessKeyID),
				strings.TrimSpace(opts.SecretKey),
				strings.TrimSpace(opts.SessionToken),
			),
		),
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = opts.PathStyle
		o.BaseEndpoint = aws.String(strings.TrimSpace(opts.Endpoint))
	})
	return client, nil
}

func (u *S3Uploader) Upload(ctx context.Context, cfg config.Config, asset model.MediaAsset, opts Options) (model.MediaArchiveResult, bool, error) {
	result := archiveResultForAsset(asset, opts)
	if strings.TrimSpace(asset.ArchiveStatus) == model.MediaArchiveStatusArchived &&
		strings.TrimSpace(asset.ArchiveBucket) == strings.TrimSpace(opts.Bucket) &&
		strings.TrimSpace(asset.ArchiveKey) == result.Key {
		result.ETag = strings.TrimSpace(asset.ArchiveETag)
		return result, false, nil
	}

	root, err := vaultfs.Open(cfg.VaultDir)
	if err != nil {
		return model.MediaArchiveResult{}, false, err
	}
	defer func() { _ = root.Close() }()
	file, err := root.Open(asset.LocalPath)
	if err != nil {
		return model.MediaArchiveResult{}, false, fmt.Errorf("open local media %q: %w", asset.LocalPath, err)
	}
	defer func() {
		_ = file.Close()
	}()

	info, err := file.Stat()
	if err != nil {
		return model.MediaArchiveResult{}, false, fmt.Errorf("stat local media %q: %w", asset.LocalPath, err)
	}

	contentType := strings.TrimSpace(asset.MIMEType)
	if contentType == "" {
		contentType = mime.TypeByExtension(filepath.Ext(asset.LocalPath))
	}

	input := &s3.PutObjectInput{
		Bucket:        aws.String(strings.TrimSpace(opts.Bucket)),
		Key:           aws.String(result.Key),
		Body:          file,
		ContentLength: aws.Int64(info.Size()),
	}
	if contentType != "" {
		input.ContentType = aws.String(contentType)
	}

	output, err := u.client.PutObject(ctx, input)
	if err != nil {
		return model.MediaArchiveResult{}, false, fmt.Errorf("put archive object %s/%s: %w", strings.TrimSpace(opts.Bucket), result.Key, err)
	}
	if output.ETag != nil {
		result.ETag = strings.TrimSpace(*output.ETag)
	}
	return result, true, nil
}
