package migrations

import (
	"io"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4/source/iofs"
)

func TestEmbeddedMigrationCanBeReadInBothDirections(t *testing.T) {
	source, err := iofs.New(Files, ".")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	version, err := source.First()
	if err != nil || version != 1 {
		t.Fatalf("first migration=%d err=%v", version, err)
	}
	for _, read := range []func(uint) (io.ReadCloser, string, error){source.ReadUp, source.ReadDown} {
		body, _, err := read(version)
		if err != nil {
			t.Fatal(err)
		}
		contents, err := io.ReadAll(body)
		_ = body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(contents), "voucher_order") {
			t.Fatal("missing order table migration")
		}
	}
}

func TestBlogLikeMigrationIsAdditive(t *testing.T) {
	source, err := iofs.New(Files, ".")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	for _, read := range []func(uint) (io.ReadCloser, string, error){source.ReadUp, source.ReadDown} {
		body, _, err := read(2)
		if err != nil {
			t.Fatal(err)
		}
		contents, err := io.ReadAll(body)
		_ = body.Close()
		if err != nil || !strings.Contains(string(contents), "blog_like") || !strings.Contains(string(contents), "idx_blog_created") {
			t.Fatalf("missing blog like table or list index: %v", err)
		}
	}
}
