package mediaarchive

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

type stubHeadClient struct {
	output *s3.HeadObjectOutput
	err    error
	key    string
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
