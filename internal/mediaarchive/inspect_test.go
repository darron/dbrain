package mediaarchive

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

type stubHeadClient struct {
	output *s3.HeadObjectOutput
	err    error
	key    string
}

type stubInventoryClient struct {
	output *s3.ListObjectsV2Output
	input  *s3.ListObjectsV2Input
}

func (s *stubInventoryClient) ListObjectsV2(_ context.Context, input *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	s.input = input
	return s.output, nil
}

func TestS3InventoryListsOneBoundedMetadataPage(t *testing.T) {
	client := &stubInventoryClient{output: &s3.ListObjectsV2Output{
		Contents:    []types.Object{{Key: aws.String("media/a"), Size: aws.Int64(42)}},
		IsTruncated: aws.Bool(true), ContinuationToken: aws.String("token"), NextContinuationToken: aws.String("next"),
	}}
	inventory := newS3Inventory("bucket", client)
	page, err := inventory.ListPage(t.Context(), DefaultPrefix, "token", 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Objects) != 1 || page.Objects[0].Key != "media/a" || page.Objects[0].SizeBytes != 42 || page.Complete || page.NextToken != "next" {
		t.Fatalf("page = %#v", page)
	}
	if aws.ToString(client.input.Bucket) != "bucket" || aws.ToString(client.input.Prefix) != "media/" || aws.ToString(client.input.ContinuationToken) != "token" || aws.ToInt32(client.input.MaxKeys) != 500 {
		t.Fatalf("input = %#v", client.input)
	}
}

func TestS3InventoryRejectsObjectsOutsidePrefixAndInvalidSizes(t *testing.T) {
	for _, object := range []types.Object{
		{Key: aws.String("media2/outside"), Size: aws.Int64(42)},
		{Key: aws.String("media/negative"), Size: aws.Int64(-1)},
		{Key: aws.String("media/missing-size")},
	} {
		client := &stubInventoryClient{output: &s3.ListObjectsV2Output{Contents: []types.Object{object}, IsTruncated: aws.Bool(false)}}
		inventory := newS3Inventory("bucket", client)
		if _, err := inventory.ListPage(t.Context(), DefaultPrefix, "", 500); err == nil {
			t.Fatalf("expected inconsistent object rejection: %#v", object)
		}
	}
}

func TestS3InventoryRejectsMalformedPaginationMetadata(t *testing.T) {
	tests := []struct {
		name  string
		token string
		page  *s3.ListObjectsV2Output
	}{
		{
			name: "missing truncation marker",
			page: &s3.ListObjectsV2Output{},
		},
		{
			name: "complete page with next token",
			page: &s3.ListObjectsV2Output{
				IsTruncated: aws.Bool(false), NextContinuationToken: aws.String("unexpected"),
			},
		},
		{
			name: "truncated page without next token",
			page: &s3.ListObjectsV2Output{IsTruncated: aws.Bool(true)},
		},
		{
			name:  "response token differs from request",
			token: "requested",
			page: &s3.ListObjectsV2Output{
				IsTruncated: aws.Bool(false), ContinuationToken: aws.String("different"),
			},
		},
		{
			name:  "response omits requested token",
			token: "requested",
			page:  &s3.ListObjectsV2Output{IsTruncated: aws.Bool(false)},
		},
		{
			name: "initial response invents continuation token",
			page: &s3.ListObjectsV2Output{
				IsTruncated: aws.Bool(false), ContinuationToken: aws.String("invented"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inventory := newS3Inventory("bucket", &stubInventoryClient{output: tt.page})
			page, err := inventory.ListPage(t.Context(), DefaultPrefix, tt.token, 500)
			if err == nil {
				t.Fatalf("expected malformed pagination rejection, got %#v", page)
			}
			if len(page.Objects) != 0 {
				t.Fatalf("malformed response copied objects: %#v", page.Objects)
			}
		})
	}
}

func TestS3InventoryRejectsOverBudgetContentsBeforeCopy(t *testing.T) {
	objects := make([]types.Object, 501)
	for index := range objects {
		objects[index] = types.Object{Key: aws.String("media/object"), Size: aws.Int64(1)}
	}
	client := &stubInventoryClient{output: &s3.ListObjectsV2Output{
		Contents: objects, IsTruncated: aws.Bool(false),
	}}
	inventory := newS3Inventory("bucket", client)

	page, err := inventory.ListPage(t.Context(), DefaultPrefix, "", 500)
	if err == nil {
		t.Fatalf("expected oversized custom S3 response rejection, got %#v", page)
	}
	if len(page.Objects) != 0 {
		t.Fatalf("oversized response copied objects: got %d", len(page.Objects))
	}
}

func (s *stubHeadClient) HeadObject(_ context.Context, input *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	s.key = aws.ToString(input.Key)
	return s.output, s.err
}

func TestS3InspectorUsesHeadOnlyAndClassifiesNotFound(t *testing.T) {
	client := &stubHeadClient{output: &s3.HeadObjectOutput{ContentLength: aws.Int64(42)}}
	inspector := newS3Inspector("bucket", client)
	got, err := inspector.HeadObject(t.Context(), "media/key")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Exists || got.SizeBytes != 42 || client.key != "media/key" {
		t.Fatalf("metadata=%#v key=%q", got, client.key)
	}
	client.err = &smithy.GenericAPIError{Code: "NotFound", Message: "missing"}
	got, err = inspector.HeadObject(t.Context(), "media/missing")
	if err != nil || got.Exists {
		t.Fatalf("missing metadata=%#v err=%v", got, err)
	}
	client.err = errors.New("transport failed")
	if _, err := inspector.HeadObject(t.Context(), "media/error"); err == nil {
		t.Fatal("expected transport failure")
	}
}

func TestNewS3ClientRejectsEndpointComponents(t *testing.T) {
	for _, endpoint := range []string{
		"https://user:secret@s3.example.com",
		"https://s3.example.com/bucket",
		"https://s3.example.com?query=yes",
		"https://s3.example.com#fragment",
	} {
		t.Run(endpoint, func(t *testing.T) {
			_, err := NewS3Client(Options{Endpoint: endpoint, Region: "auto", AccessKeyID: "id", SecretKey: "secret"})
			if err == nil {
				t.Fatal("expected endpoint rejection")
			}
		})
	}
}
