package sqlitearchive

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type stubListClient struct {
	pages []*s3.ListObjectsV2Output
	calls int
}

func (s *stubListClient) ListObjectsV2(_ context.Context, _ *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	page := s.pages[s.calls]
	s.calls++
	return page, nil
}

func TestS3InspectorListsMetadataOnlyWithExplicitBudget(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	client := &stubListClient{pages: []*s3.ListObjectsV2Output{
		{Contents: []types.Object{{Key: aws.String("archive/db/a"), Size: aws.Int64(10), LastModified: aws.Time(now)}}, IsTruncated: aws.Bool(true), NextContinuationToken: aws.String("next")},
		{Contents: []types.Object{{Key: aws.String("archive/db/b"), Size: aws.Int64(20), LastModified: aws.Time(now.Add(time.Hour))}}, IsTruncated: aws.Bool(false)},
	}}
	inspector := newS3Inspector("bucket", client)
	listing, err := inspector.ListObjects(t.Context(), "archive/db", 10)
	if err != nil {
		t.Fatal(err)
	}
	if !listing.Complete || len(listing.Objects) != 2 || client.calls != 2 {
		t.Fatalf("listing=%#v calls=%d", listing, client.calls)
	}

	client = &stubListClient{pages: []*s3.ListObjectsV2Output{{
		Contents:    []types.Object{{Key: aws.String("archive/db/a")}, {Key: aws.String("archive/db/b")}},
		IsTruncated: aws.Bool(true), NextContinuationToken: aws.String("next"),
	}}}
	listing, err = newS3Inspector("bucket", client).ListObjects(t.Context(), "archive/db", 1)
	if err != nil {
		t.Fatal(err)
	}
	if listing.Complete || len(listing.Objects) != 1 || client.calls != 1 {
		t.Fatalf("bounded listing=%#v calls=%d", listing, client.calls)
	}
}
