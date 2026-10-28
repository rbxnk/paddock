package blob

import (
	"context"
	"errors"
	"io/fs"
	"net/http/httptest"
	"os"
	"regexp"
	"testing"

	"github.com/minio/minio-go/v7"
)

func TestName(t *testing.T) {
	for in, want := range map[string]string{
		"Spa Map.SVG":    `^spa_map_[a-z0-9]{10}\.svg$`,
		"../../etc.png":  `^etc_[a-z0-9]{10}\.png$`,
		"ünï.jpeg":       `^n_[a-z0-9]{10}\.jpeg$`,
		".png":           `^file_[a-z0-9]{10}\.png$`,
		"noext":          `^noext_[a-z0-9]{10}$`,
		"x.p/ng":         `^x_p_ng_[a-z0-9]{10}$`,
		"a.verylongext1": `^a_[a-z0-9]{10}$`,
	} {
		if got := Name(in); !regexp.MustCompile(want).MatchString(got) {
			t.Errorf("Name(%q) = %q, want %s", in, got, want)
		}
	}
}

func TestDir(t *testing.T) {
	ctx, d := context.Background(), Dir(t.TempDir())
	if err := d.Put(ctx, "../x", []byte("no"), ""); err == nil {
		t.Fatal("a key outside the directory was accepted")
	}
	if err := d.Put(ctx, "c/r/a.svg", []byte("<svg/>"), "image/svg+xml"); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	if err := Serve(w, httptest.NewRequest("GET", "/", nil), d, "c/r/a.svg", "a.svg"); err != nil {
		t.Fatal(err)
	}
	h := w.Header()
	if w.Body.String() != "<svg/>" || h.Get("Content-Type") != "image/svg+xml" || h.Get("Content-Disposition") != `attachment; filename="a.svg"` ||
		!regexp.MustCompile(`sandbox`).MatchString(h.Get("Content-Security-Policy")) {
		t.Fatalf("served %q with %v", w.Body, h)
	}
	if err := d.Delete(ctx, "c/r/a.svg"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.Open(ctx, "c/r/a.svg"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("deleted file: %v", err)
	}
	if err := d.Delete(ctx, "c/r/a.svg"); err != nil {
		t.Fatalf("deleting a missing file: %v", err)
	}
}

// TestBucket runs against an S3 server named by BLOB_TEST_ENDPOINT (e.g. a local MinIO:
// `minio server <dir>`, then BLOB_TEST_ENDPOINT=http://127.0.0.1:9000 with
// AWS_ACCESS_KEY_ID=minioadmin AWS_SECRET_ACCESS_KEY=minioadmin).
func TestBucket(t *testing.T) {
	endpoint := os.Getenv("BLOB_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("BLOB_TEST_ENDPOINT isn't set")
	}
	t.Setenv("AWS_ENDPOINT_URL_S3", endpoint)
	t.Setenv("BUCKET_NAME", "blob-test")
	ctx := context.Background()
	s, err := FromEnv("")
	if err != nil {
		t.Fatal(err)
	}
	b := s.(Bucket)
	if err := b.c.MakeBucket(ctx, b.bucket, minio.MakeBucketOptions{}); err != nil && minio.ToErrorResponse(err).Code != "BucketAlreadyOwnedByYou" {
		t.Fatal(err)
	}
	if _, _, err := b.Open(ctx, "c/r/missing.png"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing key: %v", err)
	}
	if err := b.Put(ctx, "c/r/a.png", []byte("png bytes"), "image/png"); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Range", "bytes=4-")
	if err := Serve(w, req, b, "c/r/a.png", "a.png"); err != nil || w.Body.String() != "bytes" || w.Header().Get("Content-Disposition") != `inline; filename="a.png"` {
		t.Fatalf("served %q %v: %v", w.Body, w.Header(), err)
	}
	if err := b.Delete(ctx, "c/r/a.png"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Open(ctx, "c/r/a.png"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("deleted key: %v", err)
	}
}
