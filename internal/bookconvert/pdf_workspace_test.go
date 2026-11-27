package bookconvert

import (
	"context"
	"errors"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestPDFWorkspaceReview(t *testing.T) {
	workspace := t.TempDir()
	dir := filepath.Join(workspace, "pdf-pages", "document")
	previews := filepath.Join(dir, "previews")
	if err := os.MkdirAll(previews, 0o700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"document.json":       `{"version":1,"total":4,"sourceDigest":"` + strings.Repeat("a", 64) + `"}`,
		"page-1.json":         `{"page":1,"total":4,"text":"第一頁","rawText":"第一頁","ocr":false,"forceOcr":false,"layout":"horizontal","language":"auto","lowConfidence":[]}`,
		"page-2.json":         `{"page":2,"total":4,"text":"二ページ","rawText":"二ページ","ocr":true,"forceOcr":true,"layout":"vertical-rtl","language":"ja","lowConfidence":["ページ"]}`,
		"page-4.json":         `{"page":3,"total":4,"text":"wrong physical page","rawText":"wrong physical page","ocr":false,"forceOcr":false,"layout":"horizontal","language":"auto"}`,
		"previews/page-2.jpg": "invalid image data",
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	file, err := os.Create(filepath.Join(previews, "page-1.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(jpeg.Encode(file, image.NewRGBA(image.Rect(0, 0, 25, 35)), nil), file.Close()); err != nil {
		t.Fatal(err)
	}

	summary, err := ReadPDFWorkspace(t.Context(), workspace)
	if err != nil || summary.Total != 4 || !slices.Equal(summary.Saved, []int{1, 2}) || !slices.Equal(summary.ReviewPages, []int{2, 3, 4}) {
		t.Fatalf("summary = %+v, err = %v", summary, err)
	}
	first, err := ReadPDFPage(workspace, 1, summary.Total)
	if err != nil || !first.Available || first.Text != "第一頁" || first.Preview == nil || first.Preview.Bounds().Dy() != 35 {
		t.Fatalf("page 1 = %+v, err = %v", first, err)
	}
	second, err := ReadPDFPage(workspace, 2, summary.Total)
	if err != nil || !second.Available || second.Text != "二ページ" || second.Preview != nil || second.PreviewError == nil {
		t.Fatalf("page 2 = %+v, err = %v", second, err)
	}
	third, err := ReadPDFPage(workspace, 3, summary.Total)
	if err != nil || third.Number != 3 || third.Available || third.PreviewError != nil {
		t.Fatalf("page 3 = %+v, err = %v", third, err)
	}
	if _, err := ReadPDFPage(workspace, 4, summary.Total); err == nil {
		t.Fatal("accepted mismatched physical page number")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ReadPDFWorkspace(ctx, workspace); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled summary error = %v", err)
	}
}
