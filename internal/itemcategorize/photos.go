package itemcategorize

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/mediaarchive"
	"github.com/darron/dbrain/internal/model"
)

// loadPhotoBytes collects raw bytes for each photo associated with an item.
// For each photo ref it tries, in order:
//  1. Local file on disk (if present and not pruned)
//  2. R2/S3 archive (if the item was pruned locally but is archived)
//
// Refs that are neither locally available nor archived are silently skipped.
func loadPhotoBytes(ctx context.Context, cfg config.Config, refs []model.ItemMediaRef, s3client *s3.Client, include bool) [][]byte {
	if !include {
		return nil
	}
	var out [][]byte
	for _, ref := range refs {
		if ref.MediaType != "photo" {
			continue
		}
		// 1. Try local file first.
		if strings.TrimSpace(ref.LocalPath) != "" && ref.LocalPrunedAt.IsZero() {
			absPath := filepath.Join(cfg.VaultDir, filepath.FromSlash(ref.LocalPath))
			if data, err := os.ReadFile(absPath); err == nil {
				out = append(out, data)
				continue
			}
		}
		// 2. Fall back to R2/S3 if archived.
		if s3client != nil && ref.ArchiveStatus == "archived" &&
			strings.TrimSpace(ref.ArchiveBucket) != "" && strings.TrimSpace(ref.ArchiveKey) != "" {
			if data, err := fetchFromS3(ctx, s3client, ref.ArchiveBucket, ref.ArchiveKey); err == nil {
				out = append(out, data)
			}
		}
	}
	return out
}

func fetchFromS3(ctx context.Context, client *s3.Client, bucket, key string) ([]byte, error) {
	output, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = output.Body.Close() }()
	return io.ReadAll(output.Body)
}

func buildS3Client(opts Options) *s3.Client {
	if opts.S3Endpoint == "" || opts.S3AccessKey == "" || opts.S3SecretKey == "" {
		return nil
	}
	client, err := mediaarchive.NewS3Client(mediaarchive.Options{
		Endpoint:    opts.S3Endpoint,
		Region:      firstNonEmpty(opts.S3Region, "auto"),
		AccessKeyID: opts.S3AccessKey,
		SecretKey:   opts.S3SecretKey,
		PathStyle:   true,
	})
	if err != nil {
		return nil
	}
	return client
}
