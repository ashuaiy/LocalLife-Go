package service

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"strings"
	"testing"
)

func TestMediaRoundTripAndValidation(t *testing.T) {
	dir := t.TempDir()
	media := NewMedia(dir)
	ctx := context.Background()
	for _, format := range []string{"png", "jpeg"} {
		var source bytes.Buffer
		img := image.NewRGBA(image.Rect(0, 0, 2, 3))
		if format == "png" {
			if err := png.Encode(&source, img); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := jpeg.Encode(&source, img, nil); err != nil {
				t.Fatal(err)
			}
		}
		source.WriteString("trailing payload")
		path, err := media.Save(ctx, &source)
		if err != nil || !validMediaURL(path) {
			t.Fatalf("save=%q %v", path, err)
		}
		data, kind, err := media.Read(ctx, strings.TrimPrefix(path, "/uploads/"))
		if err != nil || kind != "image/"+format || bytes.Contains(data, []byte("trailing payload")) {
			t.Fatalf("read=%s %v", kind, err)
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width != 2 || cfg.Height != 3 {
			t.Fatalf("image=%+v %v", cfg, err)
		}
	}
	for _, data := range [][]byte{[]byte("<svg>not a photo</svg>"), bytes.Repeat([]byte{'x'}, MaxImageBytes+1)} {
		_, err := media.Save(ctx, bytes.NewReader(data))
		expectStatus(t, err, 400)
	}
	for _, name := range []string{"../secret", "../../.env", "bad.png"} {
		_, _, err := media.Read(ctx, name)
		expectStatus(t, err, 404)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err := media.Save(canceled, strings.NewReader(""))
	expectStatus(t, err, 408)
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 2 {
		t.Fatalf("invalid uploads wrote files: %d %v", len(files), err)
	}
}

func TestBlogImageReferences(t *testing.T) {
	good := []string{"/uploads/0123456789abcdef0123456789abcdef.png", "https://example.com/photo.jpg"}
	if !validBlogImages(good) {
		t.Fatal("valid images rejected")
	}
	for _, bad := range [][]string{{"javascript:alert(1)"}, {"//example.com/a.png"}, {"/uploads/../.env"}, {"https://user:pass@example.com/a"}, make([]string, 10)} {
		if validBlogImages(bad) {
			t.Fatalf("accepted=%v", bad)
		}
	}
}
