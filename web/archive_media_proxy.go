package web

import (
	"context"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/mediaarchive"
	"github.com/darron/dbrain/internal/runtimeenv"
)

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
