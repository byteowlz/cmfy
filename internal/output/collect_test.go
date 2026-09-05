package output_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cmfy/internal/output"
)

type memorySource struct {
	data      []byte
	partial   bool
	interrupt bool
}

func (s memorySource) Fetch(_ context.Context, _ output.Descriptor, offset int64) (io.ReadCloser, output.FetchInfo, error) {
	if offset > int64(len(s.data)) {
		return nil, output.FetchInfo{}, errors.New("invalid offset")
	}
	start := int64(0)
	if s.partial {
		start = offset
	}
	reader := io.Reader(bytes.NewReader(s.data[start:]))
	if s.interrupt {
		reader = io.MultiReader(io.LimitReader(reader, 2), errReader{})
	}
	return io.NopCloser(reader), output.FetchInfo{Partial: offset > 0 && s.partial}, nil
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("connection interrupted") }

func TestCollectRejectsEscapesAndSymlinkDestinations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	for _, descriptor := range []output.Descriptor{
		{Filename: "../escape.png"},
		{Filename: filepath.Join(root, "absolute.png")},
		{Filename: "escape.png", Subfolder: "../outside"},
		{Filename: "escape.png", Subfolder: "linked"},
	} {
		_, err := output.Collect(ctx, memorySource{data: []byte("image")}, root, []output.Descriptor{descriptor}, output.Limits{MaxFileBytes: 1024, MaxTotalBytes: 2048})
		if err == nil {
			t.Fatalf("expected descriptor %#v to fail", descriptor)
		}
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("escape wrote outside root: %#v", entries)
	}
}

func TestCollectBoundsAndAtomicallyMaterializesOutputs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	descriptor := output.Descriptor{Filename: "result.png", Subfolder: "images", Type: "output", MediaType: "image/png"}

	_, err := output.Collect(ctx, memorySource{data: []byte("oversized")}, root, []output.Descriptor{descriptor}, output.Limits{MaxFileBytes: 4, MaxTotalBytes: 4})
	if err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("expected byte-limit error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "images", "result.png")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oversized final file exists: %v", err)
	}

	_, err = output.Collect(ctx, memorySource{data: []byte("content"), interrupt: true}, root, []output.Descriptor{descriptor}, output.Limits{MaxFileBytes: 1024, MaxTotalBytes: 1024})
	if err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("expected interrupted download, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "images", "result.png")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("interrupted final file exists: %v", err)
	}

	assets, err := output.Collect(ctx, memorySource{data: []byte("content")}, root, []output.Descriptor{descriptor}, output.Limits{MaxFileBytes: 1024, MaxTotalBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 1 || assets[0].Path != filepath.Join("images", "result.png") || assets[0].Size != 7 {
		t.Fatalf("unexpected assets: %#v", assets)
	}
	body, err := os.ReadFile(filepath.Join(root, assets[0].Path))
	if err != nil || string(body) != "content" {
		t.Fatalf("unexpected materialized body %q err=%v", body, err)
	}
}

func TestCollectUsesDeterministicCollisionNames(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "result.png"), []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	descriptor := output.Descriptor{Filename: "result.png", MediaType: "image/png"}
	first, err := output.Collect(ctx, memorySource{data: []byte("new")}, root, []output.Descriptor{descriptor}, output.Limits{MaxFileBytes: 1024, MaxTotalBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	second, err := output.Collect(ctx, memorySource{data: []byte("new")}, root, []output.Descriptor{descriptor}, output.Limits{MaxFileBytes: 1024, MaxTotalBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if first[0].Path != second[0].Path || first[0].Path == "result.png" {
		t.Fatalf("collision name is not deterministic: first=%#v second=%#v", first, second)
	}
}
