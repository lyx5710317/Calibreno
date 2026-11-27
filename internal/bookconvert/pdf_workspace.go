package bookconvert

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"dalibre/internal/proc"
)

const (
	pdfMaxPages      = 3000
	pdfMaxPageRecord = 12 << 20
	pdfMaxPreview    = 8 << 20
	pdfMaxReviewRead = 256 << 20
)

// PDFWorkspaceSummary lists recognized pages and pages that need review in
// physical document order. Missing pages are included in ReviewPages.
type PDFWorkspaceSummary struct {
	Total       int
	Saved       []int
	ReviewPages []int
}

// PDFPage is a page's saved text and its optional cached original image.
// An invalid image does not prevent access to the recognized text.
type PDFPage struct {
	Number        int
	Available     bool
	Text          string
	OCR           bool
	Warning       string
	LowConfidence []string
	Preview       image.Image
	PreviewError  error
}

func pdfDocumentRoot(workspace string) (*os.Root, error) {
	if workspace == "" {
		return nil, errors.New("PDF workspace is missing")
	}
	return os.OpenRoot(filepath.Join(workspace, "pdf-pages", "document"))
}

// ReadPDFWorkspace reads the same checkpoints that PDF conversion uses to
// resume. It treats missing and unreadable page records as needing review.
func ReadPDFWorkspace(ctx context.Context, workspace string) (PDFWorkspaceSummary, error) {
	root, err := pdfDocumentRoot(workspace)
	if err != nil {
		return PDFWorkspaceSummary{}, err
	}
	defer root.Close()
	data, err := readPDFWorkspaceFile(root, "document.json", 4096)
	if err != nil {
		return PDFWorkspaceSummary{}, err
	}
	var index struct {
		Version      int    `json:"version"`
		Total        int    `json:"total"`
		SourceDigest string `json:"sourceDigest"`
	}
	if err := json.Unmarshal(data, &index); err != nil {
		return PDFWorkspaceSummary{}, fmt.Errorf("invalid PDF workspace index: %w", err)
	}
	digest, err := hex.DecodeString(index.SourceDigest)
	if err != nil || len(digest) != 32 || index.Version != 1 || index.Total < 1 || index.Total > pdfMaxPages {
		return PDFWorkspaceSummary{}, errors.New("invalid PDF workspace index")
	}
	summary := PDFWorkspaceSummary{Total: index.Total}
	var bytesRead int
	for page := 1; page <= index.Total; page++ {
		if err := ctx.Err(); err != nil {
			return PDFWorkspaceSummary{}, err
		}
		record, size, err := readPDFPageRecord(root, page, index.Total)
		bytesRead += size
		if bytesRead > pdfMaxReviewRead {
			return PDFWorkspaceSummary{}, errors.New("PDF workspace records exceed the review size limit")
		}
		if err == nil {
			summary.Saved = append(summary.Saved, page)
		}
		if err != nil || record.needsReview() {
			summary.ReviewPages = append(summary.ReviewPages, page)
		}
	}
	return summary, nil
}

// ReadPDFPage reads both sides of the review. When the text record is
// invalid, the partially populated result can still hold the original image.
func ReadPDFPage(workspace string, page, total int) (PDFPage, error) {
	result := PDFPage{Number: page}
	if total < 1 || total > pdfMaxPages || page < 1 || page > total {
		return result, errors.New("PDF page is outside the document")
	}
	root, err := pdfDocumentRoot(workspace)
	if err != nil {
		return result, err
	}
	defer root.Close()
	result.Preview, result.PreviewError = readPDFPreview(root, filepath.Join("previews", fmt.Sprintf("page-%d.jpg", page)))
	if errors.Is(result.PreviewError, os.ErrNotExist) {
		result.PreviewError = nil
	}
	record, _, err := readPDFPageRecord(root, page, total)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	result.Available = true
	result.Text, result.OCR, result.Warning, result.LowConfidence = *record.Text, *record.OCR, record.Warning, record.LowConfidence
	return result, nil
}

type pdfPageRecord struct {
	Page          int      `json:"page"`
	Total         int      `json:"total"`
	Text          *string  `json:"text"`
	RawText       *string  `json:"rawText"`
	OCR           *bool    `json:"ocr"`
	ForceOCR      *bool    `json:"forceOcr"`
	Layout        string   `json:"layout"`
	Language      string   `json:"language"`
	Warning       string   `json:"warning"`
	LowConfidence []string `json:"lowConfidence"`
	AI            string   `json:"ai"`
}

func (record pdfPageRecord) needsReview() bool {
	return record.Warning == "PAGE_READ" || len(record.LowConfidence) > 0 || record.AI == "fallback"
}

func readPDFPageRecord(root *os.Root, page, total int) (pdfPageRecord, int, error) {
	data, err := readPDFWorkspaceFile(root, fmt.Sprintf("page-%d.json", page), pdfMaxPageRecord)
	if err != nil {
		return pdfPageRecord{}, 0, err
	}
	var record pdfPageRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return pdfPageRecord{}, len(data), fmt.Errorf("invalid PDF page %d record: %w", page, err)
	}
	if record.Page != page || record.Total != total || record.Text == nil || record.RawText == nil || record.OCR == nil || record.ForceOCR == nil ||
		(record.Layout != "horizontal" && record.Layout != "vertical-rtl") ||
		(record.Language != "auto" && record.Language != "zh-Hant" && record.Language != "ja") ||
		len(record.LowConfidence) > 25 || (record.Warning != "" && record.Warning != "EMPTY" && record.Warning != "PAGE_READ") {
		return pdfPageRecord{}, len(data), fmt.Errorf("invalid PDF page %d record", page)
	}
	for _, fragment := range record.LowConfidence {
		if len(fragment) > 800 {
			return pdfPageRecord{}, len(data), fmt.Errorf("invalid PDF page %d review text", page)
		}
	}
	return record, len(data), nil
}

func readPDFWorkspaceFile(root *os.Root, name string, limit int64) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("invalid PDF workspace file size or type")
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if int64(len(data)) > limit {
		return nil, errors.New("PDF workspace file exceeds its size limit")
	}
	return data, err
}

func readPDFPreview(root *os.Root, name string) (image.Image, error) {
	data, err := readPDFWorkspaceFile(root, name, pdfMaxPreview)
	if err != nil {
		return nil, err
	}
	config, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 2048 || config.Height > 2048 {
		return nil, errors.New("invalid image for PDF preview")
	}
	return jpeg.Decode(bytes.NewReader(data))
}

// RenderPDFPreview renders one physical page in a temporary directory and
// returns the decoded image.
func (e Engine) RenderPDFPreview(ctx context.Context, input string, page int) (image.Image, error) {
	if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
		return nil, errors.New("PDF preview requires Windows x64")
	}
	if input == "" || page < 1 || page > pdfMaxPages {
		return nil, errors.New("choose a source PDF and a page number")
	}
	input, err := filepath.Abs(input)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(input)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > 512<<20 {
		return nil, errors.New("the source must be a nonempty file of at most 512 MB")
	}
	dir := e.Directory
	if dir == "" {
		dir = DefaultDirectory()
	}
	worker, err := filepath.Abs(filepath.Join(dir, "pdf-ocr", "pdf-ocr.exe"))
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(worker); err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("conversion runtime is missing: %s", worker)
	}
	work, err := os.MkdirTemp("", "calibreno-pdf-preview-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, worker, input, "auto", "--pages", strconv.Itoa(page), "--preview-dir", work, "--image-only")
	cmd.Dir = filepath.Dir(worker)
	cmd.WaitDelay = 2 * time.Second
	proc.Group(cmd)
	err = cmd.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("render PDF page %d: %w", page, err)
	}
	root, err := os.OpenRoot(work)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return readPDFPreview(root, fmt.Sprintf("page-%d.jpg", page))
}
