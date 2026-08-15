package s3_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tamaco489/folio/backend/internal/awsx/s3"
	"github.com/tamaco489/folio/backend/internal/awsx/s3/s3test"
)

const testBucket = "dev-folio-artifacts"

func newClient(t *testing.T) (*s3.Client, *s3test.Fake) {
	t.Helper()

	fake := s3test.NewFake()
	return s3.New(fake, testBucket), fake
}

func TestClientPutBytesAndGetBytes(t *testing.T) {
	t.Parallel()

	client, fake := newClient(t)
	ctx := context.Background()
	key := s3.TextLayerKey("job-1")

	if err := client.PutBytes(ctx, key, []byte("layer"), s3.WithContentType("text/plain")); err != nil {
		t.Fatalf("PutBytes: %v", err)
	}

	obj, ok := fake.Object(testBucket, key)
	if !ok {
		t.Fatalf("object %q was not stored", key)
	}
	if obj.ContentType != "text/plain" {
		t.Errorf("content type: got %q, want %q", obj.ContentType, "text/plain")
	}

	got, err := client.GetBytes(ctx, key)
	if err != nil {
		t.Fatalf("GetBytes: %v", err)
	}
	if string(got) != "layer" {
		t.Errorf("body: got %q, want %q", got, "layer")
	}
}

func TestClientPutStreamsWithoutBuffering(t *testing.T) {
	t.Parallel()

	client, fake := newClient(t)
	ctx := context.Background()
	key := s3.OriginalPDFKey("job-1")
	body := strings.Repeat("pdf", 1024)

	err := client.Put(ctx, key, strings.NewReader(body),
		s3.WithContentType("application/pdf"),
		s3.WithContentLength(int64(len(body))),
		s3.WithMetadata(map[string]string{"job-id": "job-1"}),
	)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	obj, ok := fake.Object(testBucket, key)
	if !ok {
		t.Fatalf("object %q was not stored", key)
	}
	if string(obj.Body) != body {
		t.Error("stored body differs from the source stream")
	}
	if obj.Metadata["job-id"] != "job-1" {
		t.Errorf("metadata: got %v", obj.Metadata)
	}
}

func TestClientGetReturnsStream(t *testing.T) {
	t.Parallel()

	client, fake := newClient(t)
	ctx := context.Background()
	key := s3.OriginalPDFKey("job-1")
	fake.Seed(testBucket, key, s3test.Object{Body: []byte("pdf-bytes")})

	body, err := client.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer body.Close()

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(body); err != nil {
		t.Fatalf("read: %v", err)
	}
	if buf.String() != "pdf-bytes" {
		t.Errorf("body: got %q", buf.String())
	}
}

func TestClientDownload(t *testing.T) {
	t.Parallel()

	client, fake := newClient(t)
	ctx := context.Background()
	key := s3.PageImageKey("job-1", 12)
	fake.Seed(testBucket, key, s3test.Object{Body: []byte("png-bytes")})

	var dst bytes.Buffer
	n, err := client.Download(ctx, key, &dst)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if n != int64(len("png-bytes")) {
		t.Errorf("written: got %d, want %d", n, len("png-bytes"))
	}
	if dst.String() != "png-bytes" {
		t.Errorf("body: got %q", dst.String())
	}
}

func TestClientJSONRoundTrip(t *testing.T) {
	t.Parallel()

	type payload struct {
		JobID string `json:"jobId"`
		Pages int    `json:"pages"`
	}

	client, fake := newClient(t)
	ctx := context.Background()
	key := s3.ResultTextractKey("job-1")
	want := payload{JobID: "job-1", Pages: 12}

	if err := client.PutJSON(ctx, key, want); err != nil {
		t.Fatalf("PutJSON: %v", err)
	}

	obj, ok := fake.Object(testBucket, key)
	if !ok {
		t.Fatalf("object %q was not stored", key)
	}
	if obj.ContentType != s3.ContentTypeJSON {
		t.Errorf("content type: got %q, want %q", obj.ContentType, s3.ContentTypeJSON)
	}

	var got payload
	if err := client.GetJSON(ctx, key, &got); err != nil {
		t.Fatalf("GetJSON: %v", err)
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestClientGetJSONInvalidPayload(t *testing.T) {
	t.Parallel()

	client, fake := newClient(t)
	ctx := context.Background()
	key := s3.ComparisonKey("job-1")
	fake.Seed(testBucket, key, s3test.Object{Body: []byte("{")})

	var got map[string]any
	if err := client.GetJSON(ctx, key, &got); err == nil {
		t.Fatal("want decode error, got nil")
	}
}

func TestClientHead(t *testing.T) {
	t.Parallel()

	client, fake := newClient(t)
	ctx := context.Background()
	key := s3.OriginalPDFKey("job-1")
	fake.Seed(testBucket, key, s3test.Object{
		Body:        []byte("pdf-bytes"),
		ContentType: "application/pdf",
		Metadata:    map[string]string{"job-id": "job-1"},
	})

	info, err := client.Head(ctx, key)
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if info.Key != key {
		t.Errorf("key: got %q, want %q", info.Key, key)
	}
	if info.Size != int64(len("pdf-bytes")) {
		t.Errorf("size: got %d, want %d", info.Size, len("pdf-bytes"))
	}
	if info.ContentType != "application/pdf" {
		t.Errorf("content type: got %q", info.ContentType)
	}
	if info.Metadata["job-id"] != "job-1" {
		t.Errorf("metadata: got %v", info.Metadata)
	}
}

func TestClientExists(t *testing.T) {
	t.Parallel()

	client, fake := newClient(t)
	ctx := context.Background()
	key := s3.OriginalPDFKey("job-1")

	exists, err := client.Exists(ctx, key)
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if exists {
		t.Error("want false for a missing object")
	}

	fake.Seed(testBucket, key, s3test.Object{Body: []byte("pdf-bytes")})

	exists, err = client.Exists(ctx, key)
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if !exists {
		t.Error("want true for a stored object")
	}
}

func TestClientNotFoundIsUnified(t *testing.T) {
	t.Parallel()

	client, _ := newClient(t)
	ctx := context.Background()
	key := s3.OriginalPDFKey("missing")

	if _, err := client.Get(ctx, key); !errors.Is(err, s3.ErrNotFound) {
		t.Errorf("Get: want ErrNotFound, got %v", err)
	}
	if _, err := client.GetBytes(ctx, key); !errors.Is(err, s3.ErrNotFound) {
		t.Errorf("GetBytes: want ErrNotFound, got %v", err)
	}
	if _, err := client.Head(ctx, key); !errors.Is(err, s3.ErrNotFound) {
		t.Errorf("Head: want ErrNotFound, got %v", err)
	}
}

func TestClientPropagatesAPIError(t *testing.T) {
	t.Parallel()

	client, fake := newClient(t)
	ctx := context.Background()
	wantErr := errors.New("access denied")
	fake.GetErr = wantErr
	fake.PutErr = wantErr
	fake.HeadErr = wantErr

	key := s3.OriginalPDFKey("job-1")

	if _, err := client.GetBytes(ctx, key); !errors.Is(err, wantErr) {
		t.Errorf("GetBytes: got %v", err)
	}
	if err := client.PutBytes(ctx, key, []byte("x")); !errors.Is(err, wantErr) {
		t.Errorf("PutBytes: got %v", err)
	}
	if _, err := client.Exists(ctx, key); !errors.Is(err, wantErr) {
		t.Errorf("Exists: got %v", err)
	}
}

func TestClientBucket(t *testing.T) {
	t.Parallel()

	client, _ := newClient(t)
	if client.Bucket() != testBucket {
		t.Errorf("got %q, want %q", client.Bucket(), testBucket)
	}
}
