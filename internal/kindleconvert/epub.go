package kindleconvert

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"strings"
	"unicode"

	"dalibre/internal/epub"
)

func stageEPUB(ctx context.Context, source, staged string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return epub.ErrNotEPUB
	}
	if info.Size() > maxBookSize {
		return fmt.Errorf("EPUB is over %d MB", maxBookSize>>20)
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	opened, err := in.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(info, opened) || info.Size() != opened.Size() || !info.ModTime().Equal(opened.ModTime()) {
		return errors.New("the EPUB changed before it could be staged")
	}
	out, err := os.OpenFile(staged, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, io.LimitReader(contextReader{ctx, in}, maxBookSize+1))
	if err := errors.Join(err, out.Close()); err != nil {
		return err
	}
	if n > maxBookSize {
		return fmt.Errorf("EPUB is over %d MB", maxBookSize>>20)
	}
	current, err := in.Stat()
	if err != nil {
		return err
	}
	if n != opened.Size() || current.Size() != opened.Size() || !current.ModTime().Equal(opened.ModTime()) {
		return errors.New("the EPUB changed while it was being staged")
	}
	return checkEPUB(ctx, staged)
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func checkEPUB(ctx context.Context, source string) error {
	z, err := zip.OpenReader(source)
	if err != nil {
		return fmt.Errorf("%w: %v", epub.ErrNotEPUB, err)
	}
	defer z.Close()
	if len(z.File) > 25000 {
		return errors.New("EPUB has too many ZIP entries")
	}
	entries := make(map[string]*zip.File, len(z.File))
	remaining := uint64(512 << 20)
	for _, f := range z.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := strings.TrimSuffix(f.Name, "/")
		if !fs.ValidPath(name) || name == "." || strings.ContainsAny(name, `\:`) ||
			strings.ContainsFunc(name, unicode.IsControl) || entries[name] != nil ||
			!(f.Mode().IsRegular() || f.Mode().IsDir()) {
			return errors.New("EPUB has an invalid or duplicate ZIP entry")
		}
		if f.Flags&1 != 0 || name == "META-INF/rights.xml" || name == "META-INF/signatures.xml" {
			return epub.ErrDRM
		}
		if f.Method != zip.Store && f.Method != zip.Deflate {
			return errors.New("EPUB uses an unsupported ZIP compression method")
		}
		if f.UncompressedSize64 > remaining {
			return errors.New("EPUB exceeds 512 MB when unpacked")
		}
		remaining -= f.UncompressedSize64
		entries[name] = f
	}
	mime, err := readEntry(entries["mimetype"], 64)
	if err != nil || string(mime) != "application/epub+zip" {
		return epub.ErrNotEPUB
	}
	if f := entries["META-INF/encryption.xml"]; f != nil && !onlyObfuscatedFonts(f, entries) {
		return epub.ErrDRM
	}
	return nil
}

func readEntry(f *zip.File, limit int64) ([]byte, error) {
	if f == nil || f.UncompressedSize64 > uint64(limit) {
		return nil, epub.ErrNotEPUB
	}
	r, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if int64(len(data)) > limit {
		return nil, epub.ErrNotEPUB
	}
	return data, err
}

// EPUB permits two font-obfuscation algorithms. Other encrypted resources
// and signed books must stay outside the converter.
func onlyObfuscatedFonts(f *zip.File, entries map[string]*zip.File) bool {
	data, err := readEntry(f, 2<<20)
	if err != nil {
		return false
	}
	var enc struct {
		Blocks []struct {
			Refs []struct {
				URI string `xml:"URI,attr"`
			} `xml:"CipherData>CipherReference"`
			Method struct {
				Algorithm string `xml:"Algorithm,attr"`
			} `xml:"EncryptionMethod"`
		} `xml:"http://www.w3.org/2001/04/xmlenc# EncryptedData"`
	}
	if xml.Unmarshal(data, &enc) != nil || len(enc.Blocks) == 0 {
		return false
	}
	for _, block := range enc.Blocks {
		if len(block.Refs) != 1 || (block.Method.Algorithm != "http://www.idpf.org/2008/embedding" &&
			block.Method.Algorithm != "http://ns.adobe.com/pdf/enc#RC") {
			return false
		}
		ref, err := url.Parse(block.Refs[0].URI)
		if err != nil || ref.Scheme != "" || ref.Host != "" || ref.RawQuery != "" || ref.Fragment != "" ||
			entries[ref.Path] == nil || !entries[ref.Path].Mode().IsRegular() {
			return false
		}
		switch strings.ToLower(path.Ext(ref.Path)) {
		case ".otf", ".ttf", ".ttc", ".woff", ".woff2":
		default:
			return false
		}
	}
	return true
}
