package sqlitearchive

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/darron/dbrain/internal/mediaarchive"
)

type S3Options struct {
	Bucket                string
	Endpoint              string
	Region                string
	AccessKeyID           string
	SecretKey             string
	SessionToken          string
	PathStyle             bool
	ConnectTimeout        time.Duration
	TLSHandshakeTimeout   time.Duration
	ResponseHeaderTimeout time.Duration
}

type S3Store struct {
	bucket string
	client *s3.Client
}

type getObjectClient interface {
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

// S3Reader is deliberately get-only. It cannot list, write, delete, or
// restore archive objects.
type S3Reader struct {
	bucket string
	client getObjectClient
}

func NewS3Reader(opts S3Options) (*S3Reader, error) {
	bucket := strings.TrimSpace(opts.Bucket)
	if bucket == "" {
		return nil, fmt.Errorf("archive bucket is required")
	}
	if strings.TrimSpace(opts.AccessKeyID) == "" || strings.TrimSpace(opts.SecretKey) == "" {
		return nil, fmt.Errorf("archive credentials are required")
	}
	region := strings.TrimSpace(opts.Region)
	if region == "" {
		region = "auto"
	}
	client, err := mediaarchive.NewS3Client(mediaarchive.Options{
		Endpoint: opts.Endpoint, Region: region, AccessKeyID: opts.AccessKeyID,
		SecretKey: opts.SecretKey, SessionToken: opts.SessionToken, PathStyle: opts.PathStyle,
		ConnectTimeout: opts.ConnectTimeout, TLSHandshakeTimeout: opts.TLSHandshakeTimeout,
		ResponseHeaderTimeout: opts.ResponseHeaderTimeout,
	})
	if err != nil {
		return nil, err
	}
	return newS3Reader(bucket, client), nil
}

func newS3Reader(bucket string, client getObjectClient) *S3Reader {
	return &S3Reader{bucket: strings.TrimSpace(bucket), client: client}
}

func (r *S3Reader) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	output, err := r.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(r.bucket), Key: aws.String(strings.TrimSpace(key))})
	if err != nil {
		return nil, fmt.Errorf("get sqlite archive candidate: %w", err)
	}
	return output.Body, nil
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
