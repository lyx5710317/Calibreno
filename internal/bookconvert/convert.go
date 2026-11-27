// Package bookconvert runs the conversion engines and publishes verified output.
package bookconvert

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/unicode/norm"

	"dalibre/internal/kindleconvert"
	"dalibre/internal/proc"
)

// Request describes a conversion from a file. Select PDF pages to save or
// rerun recognition in Workspace without generating an EPUB.
type Request struct {
	Input        string `json:"input"`
	Output       string `json:"output,omitempty"`
	Workspace    string `json:"workspace,omitempty"`
	Layout       string `json:"layout,omitempty"`
	SourceLayout string `json:"sourceLayout,omitempty"`
	PDFLanguage  string `json:"pdfLanguage,omitempty"`
	Title        string `json:"title,omitempty"`
	Author       string `json:"author,omitempty"`
	Language     string `json:"language,omitempty"`
	OCR          bool   `json:"ocr,omitempty"`
	Pages        []int  `json:"pages,omitempty"`
	sourceFormat string
}

type Result struct {
	Total       int    `json:"total,omitempty"`
	Saved       []int  `json:"saved,omitempty"`
	ReviewPages []int  `json:"reviewPages,omitempty"`
	Warnings    bool   `json:"warnings,omitempty"`
	Error       string `json:"error,omitempty"`
}

type Engine struct {
	// Directory holds the bundled engines. KindleCache stores Epub3-Kindle.
	Directory, KindleCache string
}

func DefaultDirectory() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(exe), "book-conversion")
}

func format(name string) string { return strings.ToUpper(strings.TrimPrefix(filepath.Ext(name), ".")) }

func (r Request) inputFormat() string {
	if r.sourceFormat != "" {
		return r.sourceFormat
	}
	return format(r.Input)
}

// Supported reports whether this build can run the packaged conversion engines.
func Supported() bool { return runtime.GOOS == "windows" && runtime.GOARCH == "amd64" }

// OutputFormats lists the available target formats for a source format.
func OutputFormats(source string) []string {
	switch source {
	case "EPUB":
		return []string{"AZW3", "PDF", "TXT", "EPUB"}
	case "TXT", "FB2", "PDF", "MOBI", "AZW3":
		return []string{"EPUB"}
	default:
		return nil
	}
}

func (r Request) validate() error {
	from, to := r.inputFormat(), format(r.Output)
	if r.Input == "" || (r.Output == "" && len(r.Pages) == 0) {
		return errors.New("choose a source and an output file")
	}
	if r.Layout != "" && (from != "EPUB" || to != "EPUB" || !slices.Contains([]string{"horizontal", "vertical-rtl"}, r.Layout)) {
		return errors.New("output layout requires EPUB → EPUB and must be horizontal or vertical-rtl")
	}
	if from != "PDF" && (r.OCR || r.SourceLayout != "" || r.PDFLanguage != "" || r.Workspace != "" || len(r.Pages) > 0) {
		return errors.New("PDF options require a PDF source")
	}
	if r.SourceLayout != "" && !slices.Contains([]string{"horizontal", "vertical-rtl"}, r.SourceLayout) {
		return errors.New("PDF source layout must be horizontal or vertical-rtl")
	}
	if r.PDFLanguage != "" && !slices.Contains([]string{"auto", "zh-Hant", "ja"}, r.PDFLanguage) {
		return errors.New("PDF language must be auto, zh-Hant, or ja")
	}
	if len(r.Pages) > 0 {
		if from != "PDF" || r.Workspace == "" || r.Output != "" || len(r.Pages) > 20 {
			return errors.New("page recognition requires a PDF, a workspace, and up to 20 pages")
		}
		previous := 0
		for _, page := range r.Pages {
			if page <= previous || page > 3000 {
				return errors.New("pages must be unique, ordered, and in the range 1–3000")
			}
			previous = page
		}
		return nil
	}
	if slices.Contains(OutputFormats(from), to) && (from != to || r.Layout != "") {
		return nil
	}
	return fmt.Errorf("unsupported conversion: %s → %s", from, to)
}

// ParsePages accepts comma-separated PDF page numbers and ranges.
func ParsePages(spec string) ([]int, error) {
	if spec == "" {
		return nil, nil
	}
	if len(spec) > 600 {
		return nil, errors.New("PDF page range is too long")
	}
	spec = strings.NewReplacer("、", ",", "–", "-", "—", "-", "−", "-").Replace(norm.NFKC.String(spec))
	pageNumber := func(s string) (int, bool) {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		return n, err == nil && n > 0 && n <= 3000
	}
	pages := map[int]bool{}
	for _, part := range strings.Split(spec, ",") {
		left, right, span := strings.Cut(part, "-")
		first, ok := pageNumber(left)
		last := first
		if span {
			var endOK bool
			last, endOK = pageNumber(right)
			ok = ok && endOK && last >= first
		}
		if !ok {
			return nil, fmt.Errorf("invalid PDF page range %q", part)
		}
		for n := first; n <= last; n++ {
			pages[n] = true
			if len(pages) > 20 {
				return nil, errors.New("choose up to 20 PDF pages")
			}
		}
	}
	result := make([]int, 0, len(pages))
	for n := range pages {
		result = append(result, n)
	}
	slices.Sort(result)
	return result, nil
}

// Convert writes a new file or processes pages in a PDF checkpoint workspace.
// It never overwrites an existing output path.
func (e Engine) Convert(ctx context.Context, req Request, progress io.Writer) (Result, error) {
	if err := req.validate(); err != nil {
		return Result{}, err
	}
	if !Supported() {
		return Result{}, errors.New("this conversion engine requires Windows x64")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	var err error
	if req.Input, err = filepath.Abs(req.Input); err != nil {
		return Result{}, err
	}
	st, err := os.Stat(req.Input)
	if err != nil {
		return Result{}, err
	}
	if !st.Mode().IsRegular() || st.Size() == 0 || st.Size() > 512<<20 {
		return Result{}, errors.New("the source must be a nonempty file of at most 512 MB")
	}
	output := req.Output
	if output != "" {
		if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
			if err == nil {
				err = fmt.Errorf("output already exists: %s", output)
			}
			return Result{}, err
		}
	}
	work, err := os.MkdirTemp("", "calibreno-convert-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(work)
	if output != "" {
		req.Output = filepath.Join(work, "result"+strings.ToLower(filepath.Ext(output)))
	}
	if req.Title == "" {
		req.Title = strings.TrimSuffix(filepath.Base(req.Input), filepath.Ext(req.Input))
	}
	if req.inputFormat() == "PDF" {
		if req.Workspace == "" {
			req.Workspace = work
		}
		if req.Workspace, err = filepath.Abs(req.Workspace); err != nil {
			return Result{}, err
		}
		unlock, err := lockWorkspace(req.Workspace)
		if err != nil {
			return Result{}, err
		}
		defer unlock()
	}
	var result Result
	if format(req.Output) == "AZW3" {
		convert := kindleconvert.New(e.KindleCache)
		if convert == nil {
			return Result{}, errors.New("Epub3-Kindle cache is unavailable")
		}
		req.Output, result.Warnings, err = convert(ctx, req.Input, work)
	} else {
		// All worker modes read a stable copy, including EPUB routes that
		// may revisit several archive entries while writing the output.
		input := filepath.Join(work, "source."+strings.ToLower(req.inputFormat()))
		if err := copyNew(ctx, req.Input, input); err != nil {
			return Result{}, err
		}
		req.Input = input
		result, err = e.run(ctx, work, req, progress)
	}
	if err != nil || output == "" {
		return result, err
	}
	return result, publishOutput(ctx, req.Output, output)
}

func (e Engine) run(ctx context.Context, work string, req Request, progress io.Writer) (Result, error) {
	if e.Directory == "" {
		e.Directory = DefaultDirectory()
	}
	dir, err := filepath.Abs(e.Directory)
	if err != nil {
		return Result{}, err
	}
	exe := filepath.Join(dir, "electron", "electron.exe")
	engine := filepath.Join(dir, "engine", "main.js")
	for _, file := range []string{exe, engine} {
		if st, err := os.Stat(file); err != nil || !st.Mode().IsRegular() {
			return Result{}, fmt.Errorf("conversion runtime is missing: %s", file)
		}
	}
	file := filepath.Join(work, "request.json")
	request := struct {
		Request
		Format string `json:"format"`
	}{req, format(req.Output)}
	data, err := json.Marshal(request)
	if err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(file, data, 0o600); err != nil {
		return Result{}, err
	}
	args := []string{engine, file}
	printPDF := format(req.Output) == "PDF"
	limit := 31 * time.Minute
	if printPDF {
		args[0] = filepath.Dir(engine)
		limit = 4 * time.Minute
	} else if req.inputFormat() != "PDF" {
		limit = 4 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	proc.Group(cmd)
	cmd.Dir, cmd.Stderr = work, progress
	cmd.WaitDelay = 2 * time.Second
	cmd.Env = slices.DeleteFunc(os.Environ(), func(entry string) bool {
		key, _, _ := strings.Cut(entry, "=")
		return slices.Contains([]string{"ELECTRON_RUN_AS_NODE", "NODE_OPTIONS", "NODE_PATH"}, strings.ToUpper(key))
	})
	if !printPDF {
		cmd.Env = append(cmd.Env, "ELECTRON_RUN_AS_NODE=1")
	}
	var response workerReply
	cmd.Stdout = &response
	runErr := cmd.Run()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if response.overflow {
		return Result{}, errors.New("the conversion process returned too much data")
	}
	var result Result
	decodeErr := json.Unmarshal(response.data.Bytes(), &result)
	if result.Error != "" {
		return Result{}, errors.New(result.Error)
	}
	if runErr != nil {
		return Result{}, fmt.Errorf("conversion process: %w", runErr)
	}
	if decodeErr != nil {
		return Result{}, fmt.Errorf("invalid response from the conversion process: %w", decodeErr)
	}
	return result, nil
}

type workerReply struct {
	data     bytes.Buffer
	overflow bool
}

func (r *workerReply) Write(p []byte) (int, error) {
	const limit = 256 << 10
	room := limit - r.data.Len()
	if len(p) > room {
		r.overflow = true
	}
	_, _ = r.data.Write(p[:min(room, len(p))])
	return len(p), nil
}

func copyNew(ctx context.Context, from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return errors.New("file is empty or unsupported")
	}
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, &contextReader{ctx: ctx, reader: io.LimitReader(in, info.Size()+1)})
	if err == nil {
		err = out.Sync()
	}
	err = errors.Join(err, out.Close(), ctx.Err())
	if err == nil {
		current, statErr := in.Stat()
		switch {
		case statErr != nil:
			err = statErr
		case n != info.Size() || current.Size() != info.Size() || !current.ModTime().Equal(info.ModTime()):
			err = errors.New("file changed while it was being copied")
		}
	}
	if err != nil {
		return errors.Join(err, os.Remove(to))
	}
	return nil
}

// Stage beside the destination so closing/crashing the editor during a
// copy cannot leave a partial file with the final name.
func publishOutput(ctx context.Context, source, target string) error {
	dir, err := os.MkdirTemp(filepath.Dir(target), ".calibreno-convert-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	staged := filepath.Join(dir, "result")
	if err := copyNew(ctx, source, staged); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return commitOutput(staged, target)
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
