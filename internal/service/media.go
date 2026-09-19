package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const MaxImageBytes = 2 * 1024 * 1024

var mediaName = regexp.MustCompile(`^[0-9a-f]{32}\.(jpg|png)$`)

func validMediaURL(value string) bool {
	return strings.HasPrefix(value, "/uploads/") && mediaName.MatchString(strings.TrimPrefix(value, "/uploads/"))
}

type Media struct{ dir string }

func NewMedia(dir string) *Media { return &Media{dir: dir} }
func (m *Media) Save(ctx context.Context, reader io.Reader) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	data, err := io.ReadAll(io.LimitReader(reader, MaxImageBytes+1))
	if err != nil {
		return "", apperror.New(apperror.Validation, err)
	}
	if len(data) > MaxImageBytes {
		return "", apperror.New(apperror.Validation, nil)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "jpeg" && format != "png") || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 16000000 {
		return "", apperror.New(apperror.Validation, err)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", apperror.New(apperror.Validation, err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var encoded bytes.Buffer
	ext := "png"
	if format == "jpeg" {
		ext = "jpg"
		err = jpeg.Encode(&encoded, img, &jpeg.Options{Quality: 90})
	} else {
		err = png.Encode(&encoded, img)
	}
	if err != nil {
		return "", apperror.New(apperror.Internal, err)
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", apperror.New(apperror.Internal, err)
	}
	name := hex.EncodeToString(random) + "." + ext
	if err := os.MkdirAll(m.dir, 0755); err != nil {
		return "", apperror.New(apperror.Dependency, err)
	}
	path := filepath.Join(m.dir, name)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return "", apperror.New(apperror.Dependency, err)
	}
	_, writeErr := file.Write(encoded.Bytes())
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(path)
		return "", apperror.New(apperror.Dependency, nil)
	}
	return "/uploads/" + name, nil
}
func (m *Media) Read(ctx context.Context, name string) ([]byte, string, error) {
	if !mediaName.MatchString(name) {
		return nil, "", apperror.New(apperror.NotFound, nil)
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	data, err := os.ReadFile(filepath.Join(m.dir, name))
	if os.IsNotExist(err) {
		return nil, "", apperror.New(apperror.NotFound, err)
	}
	if err != nil {
		return nil, "", apperror.New(apperror.Dependency, err)
	}
	kind := "image/png"
	if strings.HasSuffix(name, ".jpg") {
		kind = "image/jpeg"
	}
	return data, kind, nil
}
