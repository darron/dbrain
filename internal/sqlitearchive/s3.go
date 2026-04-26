package sqlitearchive

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"dbrain/internal/mediaarchive"
)

type S3Options struct {
	Bucket       string
	Endpoint     string
	Region       string
	AccessKeyID  string
	SecretKey    string
	SessionToken string
	PathStyle    bool
}

type S3Store struct {
	bucket string
	client *s3.Client
}

func NewS3Store(opts S3Options) (*S3Store, error) {
	bucket := strings.TrimSpace(opts.Bucket)
	if bucket == "" {
		return nil, fmt.Errorf("archive bucket is required")
	}
	if strings.TrimSpace(opts.Endpoint) == "" {
		return nil, fmt.Errorf("archive endpoint is required")
	}
	if strings.TrimSpace(opts.AccessKeyID) == "" || strings.TrimSpace(opts.SecretKey) == "" {
		return nil, fmt.Errorf("archive credentials are required")
	}
	region := strings.TrimSpace(opts.Region)
	if region == "" {
		region = "auto"
	}
	client, err := mediaarchive.NewS3Client(mediaarchive.Options{
		Endpoint:     opts.Endpoint,
		Region:       region,
		AccessKeyID:  opts.AccessKeyID,
		SecretKey:    opts.SecretKey,
		SessionToken: opts.SessionToken,
		PathStyle:    opts.PathStyle,
	})
	if err != nil {
		return nil, err
	}
	return &S3Store{bucket: bucket, client: client}, nil
}

func (s *S3Store) PutObject(ctx context.Context, key string, body io.Reader, contentType string, contentLength int64) (string, error) {
	input := &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(strings.TrimSpace(key)),
		Body:          body,
		ContentLength: aws.Int64(contentLength),
	}
	if strings.TrimSpace(contentType) != "" {
		input.ContentType = aws.String(strings.TrimSpace(contentType))
	}
	output, err := s.client.PutObject(ctx, input)
	if err != nil {
		return "", err
	}
	return aws.ToString(output.ETag), nil
}

func (s *S3Store) ListObjects(ctx context.Context, prefix string) ([]Object, error) {
	var objects []Object
	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(strings.TrimSpace(prefix)),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, obj := range page.Contents {
			var modified time.Time
			if obj.LastModified != nil {
				modified = *obj.LastModified
			}
			objects = append(objects, Object{
				Key:          aws.ToString(obj.Key),
				LastModified: modified,
				Size:         aws.ToInt64(obj.Size),
			})
		}
	}
	return objects, nil
}

func (s *S3Store) GetObject(ctx context.Context, key string) (io.ReadCloser, error) {
	output, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(strings.TrimSpace(key)),
	})
	if err != nil {
		return nil, err
	}
	return output.Body, nil
}
