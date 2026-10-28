// Package blob stores uploaded files under string keys: in an S3 bucket (Tigris) when the
// environment names one, otherwise in a local directory (dev, tests).
package blob

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Store keeps files by key ("a/b/name.png").
type Store interface {
	Put(ctx context.Context, key string, data []byte, contentType string) error
	// Open fails with fs.ErrNotExist for a missing key.
	Open(ctx context.Context, key string) (io.ReadSeekCloser, time.Time, error)
	Delete(ctx context.Context, key string) error
}

// FromEnv is the bucket Fly's `fly storage create` secrets name (BUCKET_NAME,
// AWS_ENDPOINT_URL_S3, AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY), or dir without them.
func FromEnv(dir string) (Store, error) {
	bucket := os.Getenv("BUCKET_NAME")
	if bucket == "" {
		return Dir(dir), nil
	}
	u, err := url.Parse(os.Getenv("AWS_ENDPOINT_URL_S3"))
	if err != nil || u.Host == "" {
		return nil, errors.New("blob: AWS_ENDPOINT_URL_S3 isn't a URL")
	}
	c, err := minio.New(u.Host, &minio.Options{
		Creds:  credentials.NewStaticV4(os.Getenv("AWS_ACCESS_KEY_ID"), os.Getenv("AWS_SECRET_ACCESS_KEY"), ""),
		Secure: u.Scheme != "http",
		Region: "auto",
	})
	if err != nil {
		return nil, err
	}
	return Bucket{c, bucket}, nil
}

// Bucket keeps files in an S3 bucket.
type Bucket struct {
	c      *minio.Client
	bucket string
}

func (b Bucket) Put(ctx context.Context, key string, data []byte, contentType string) error {
	_, err := b.c.PutObject(ctx, b.bucket, key, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{ContentType: contentType})
	return err
}

func (b Bucket) Open(ctx context.Context, key string) (io.ReadSeekCloser, time.Time, error) {
	o, err := b.c.GetObject(ctx, b.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, time.Time{}, err
	}
	st, err := o.Stat() // GetObject is lazy: this is the request
	if err != nil {
		o.Close()
		if minio.ToErrorResponse(err).StatusCode == http.StatusNotFound {
			err = fs.ErrNotExist
		}
		return nil, time.Time{}, err
	}
	return o, st.LastModified, nil
}

func (b Bucket) Delete(ctx context.Context, key string) error {
	return b.c.RemoveObject(ctx, b.bucket, key, minio.RemoveObjectOptions{})
}

// Dir keeps files in a directory, one file per key.
type Dir string

func (d Dir) path(key string) (string, error) {
	if !fs.ValidPath(key) {
		return "", errors.New("blob: bad key " + strconv.Quote(key))
	}
	return filepath.Join(string(d), filepath.FromSlash(key)), nil
}

func (d Dir) Put(_ context.Context, key string, data []byte, _ string) error {
	p, err := d.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

func (d Dir) Open(_ context.Context, key string) (io.ReadSeekCloser, time.Time, error) {
	p, err := d.path(key)
	if err != nil {
		return nil, time.Time{}, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, time.Time{}, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, time.Time{}, err
	}
	return f, st.ModTime(), nil
}

func (d Dir) Delete(_ context.Context, key string) error {
	p, err := d.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// ContentType is the type of the image files the app accepts, by extension.
func ContentType(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	}
	return "application/octet-stream"
}

// Serve sends the file at key as name. Uploaded files are untrusted, so they're sandboxed,
// and only raster images show inline; an SVG still works in an <img>.
func Serve(w http.ResponseWriter, r *http.Request, s Store, key, name string) error {
	f, mod, err := s.Open(r.Context(), key)
	if err != nil {
		return err
	}
	defer f.Close()
	ct := ContentType(name)
	disposition := "inline"
	if ct == "image/svg+xml" || ct == "application/octet-stream" {
		disposition = "attachment"
	}
	h := w.Header()
	h.Set("Content-Type", ct)
	h.Set("Content-Disposition", disposition+"; filename="+strconv.Quote(name))
	h.Set("Content-Security-Policy", "default-src 'none'; media-src 'self'; style-src 'unsafe-inline'; sandbox")
	h.Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, name, mod, f)
	return nil
}

var (
	nameUnsafe = regexp.MustCompile(`[^a-z0-9]+`)
	extOK      = regexp.MustCompile(`^\.[a-z0-9]{1,10}$`)
)

// Name is a stored file name for an upload called original: its name made safe, plus a random
// part, so every upload gets a new name (and URL) and can be cached forever.
func Name(original string) string {
	ext := strings.ToLower(path.Ext(original))
	if !extOK.MatchString(ext) {
		ext = ""
	}
	base := strings.Trim(nameUnsafe.ReplaceAllString(strings.ToLower(strings.TrimSuffix(original, path.Ext(original))), "_"), "_")
	if len(base) > 100 {
		base = base[:100]
	}
	if base == "" {
		base = "file"
	}
	return base + "_" + strings.ToLower(rand.Text()[:10]) + ext
}
