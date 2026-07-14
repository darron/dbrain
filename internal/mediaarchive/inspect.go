package mediaarchive

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type InventoryObject struct {
	Key       string
	SizeBytes int64
}

type InventoryPage struct {
	Objects   []InventoryObject
	NextToken string
	Complete  bool
}

type listInventoryClient interface {
	ListObjectsV2(context.Context, *s3.ListObjectsV2Input, ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
}

// S3Inventory is a list-only deep-audit capability. It cannot get, put, or
// delete object data.
type S3Inventory struct {
	bucket string
	client listInventoryClient
}

func NewS3Inventory(opts Options) (*S3Inventory, error) {
	if strings.TrimSpace(opts.Bucket) == "" {
		return nil, fmt.Errorf("archive bucket is required")
	}
	client, err := NewS3Client(opts)
	if err != nil {
		return nil, err
	}
	return newS3Inventory(opts.Bucket, client), nil
}

func newS3Inventory(bucket string, client listInventoryClient) *S3Inventory {
	return &S3Inventory{bucket: strings.TrimSpace(bucket), client: client}
}

func (i *S3Inventory) ListPage(ctx context.Context, prefix, token string, maxKeys int) (InventoryPage, error) {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return InventoryPage{Objects: []InventoryObject{}}, fmt.Errorf("list media inventory page: prefix is required")
	}
	if maxKeys <= 0 || maxKeys > 1000 {
		maxKeys = 1000
	}
	input := &s3.ListObjectsV2Input{
		Bucket: aws.String(i.bucket), Prefix: aws.String(prefix), MaxKeys: aws.Int32(int32(maxKeys)),
	}
	if token = strings.TrimSpace(token); token != "" {
		input.ContinuationToken = aws.String(token)
	}
	output, err := i.client.ListObjectsV2(ctx, input)
	if err != nil {
		return InventoryPage{Objects: []InventoryObject{}}, fmt.Errorf("list media inventory page: %w", err)
	}
	page := InventoryPage{Objects: make([]InventoryObject, 0, len(output.Contents)), Complete: !aws.ToBool(output.IsTruncated)}
	for _, object := range output.Contents {
		key := strings.TrimSpace(aws.ToString(object.Key))
		size := aws.ToInt64(object.Size)
		if key == "" || !strings.HasPrefix(key, prefix) || object.Size == nil || size < 0 {
			return InventoryPage{Objects: []InventoryObject{}}, fmt.Errorf("list media inventory page: inconsistent object metadata")
		}
		page.Objects = append(page.Objects, InventoryObject{Key: key, SizeBytes: size})
	}
	if !page.Complete {
		page.NextToken = strings.TrimSpace(aws.ToString(output.NextContinuationToken))
	}
	return page, nil
}
