package sqlitearchive

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/darron/dbrain/internal/mediaarchive"
)

const defaultInspectObjectLimit = 10_000

type Listing struct {
	Objects  []Object
	Complete bool
}

type listObjectsClient interface {
	ListObjectsV2(context.Context, *s3.ListObjectsV2Input, ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
}

// S3Inspector is deliberately list-only. It cannot write or restore objects.
type S3Inspector struct {
	bucket string
	client listObjectsClient
}

func NewS3Inspector(opts S3Options) (*S3Inspector, error) {
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
	})
	if err != nil {
		return nil, err
	}
	return newS3Inspector(bucket, client), nil
}

func newS3Inspector(bucket string, client listObjectsClient) *S3Inspector {
	return &S3Inspector{bucket: strings.TrimSpace(bucket), client: client}
}

func (i *S3Inspector) ListObjects(ctx context.Context, prefix string, maxObjects int) (Listing, error) {
	if maxObjects <= 0 {
		maxObjects = defaultInspectObjectLimit
	}
	listing := Listing{Objects: []Object{}, Complete: false}
	var token *string
	for len(listing.Objects) < maxObjects {
		remaining := maxObjects - len(listing.Objects)
		if remaining > 1000 {
			remaining = 1000
		}
		page, err := i.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket: aws.String(i.bucket), Prefix: aws.String(strings.TrimSpace(prefix)),
			ContinuationToken: token, MaxKeys: aws.Int32(int32(remaining)),
		})
		if err != nil {
			return Listing{}, fmt.Errorf("list archive metadata: %w", err)
		}
		for _, object := range page.Contents {
			if len(listing.Objects) >= maxObjects {
				return listing, nil
			}
			listing.Objects = append(listing.Objects, Object{
				Key: aws.ToString(object.Key), Size: aws.ToInt64(object.Size),
				LastModified: aws.ToTime(object.LastModified),
			})
		}
		if !aws.ToBool(page.IsTruncated) {
			listing.Complete = true
			return listing, nil
		}
		if page.NextContinuationToken == nil || strings.TrimSpace(aws.ToString(page.NextContinuationToken)) == "" {
			return Listing{}, fmt.Errorf("list archive metadata: truncated page missing continuation token")
		}
		token = page.NextContinuationToken
	}
	return listing, nil
}
