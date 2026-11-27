package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"

	"dalibre/internal/bookconvert"
	"dalibre/internal/calibre"
	"dalibre/internal/settings"
)

func convertBook(args []string, stdout, stderr io.Writer) error {
	var req bookconvert.Request
	var pages, library, from, to string
	var book int64
	engine := bookconvert.Engine{KindleCache: filepath.Join(filepath.Dir(settings.DefaultPath()), "conversion-tools")}
	flags := flag.NewFlagSet("convert", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: Calibreno convert [options] INPUT OUTPUT")
		fmt.Fprintln(stderr, "       Calibreno convert -workspace DIR -pages 1,3-5 [options] INPUT.pdf")
		fmt.Fprintln(stderr, "       Calibreno convert -library DIR -book ID -from FORMAT -to FORMAT [options]")
		flags.PrintDefaults()
	}
	flags.StringVar(&engine.Directory, "runtime", "", "location of the conversion runtime")
	flags.StringVar(&library, "library", "", "Calibre library for converting a book by ID")
	flags.Int64Var(&book, "book", 0, "book ID in the selected library")
	flags.StringVar(&from, "from", "", "source format of the selected library book")
	flags.StringVar(&to, "to", "", "new format to add to the selected library book")
	flags.StringVar(&req.Layout, "layout", "", "EPUB output orientation (horizontal or vertical-rtl)")
	flags.StringVar(&req.SourceLayout, "pdf-layout", "", "PDF source orientation (horizontal or vertical-rtl)")
	flags.StringVar(&req.PDFLanguage, "pdf-language", "", "PDF recognition language (auto, zh-Hant, or ja)")
	flags.BoolVar(&req.OCR, "ocr", false, "use OCR on all PDF pages")
	flags.StringVar(&req.Workspace, "workspace", "", "directory for resumable PDF recognition")
	flags.StringVar(&pages, "pages", "", "PDF pages to recognize and preview without generating EPUB")
	flags.StringVar(&req.Title, "title", "", "book title for text and PDF conversions")
	flags.StringVar(&req.Author, "author", "", "book author for text and PDF conversions")
	flags.StringVar(&req.Language, "language", "", "book language for text and PDF conversions")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	var err error
	if req.Pages, err = bookconvert.ParsePages(pages); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	var result bookconvert.Result
	if library != "" {
		if book <= 0 || from == "" || flags.NArg() > 1 ||
			(to == "") != (flags.NArg() == 1 || len(req.Pages) > 0) {
			return errors.New("choose a library book and source format, then provide an output path or -to")
		}
		lib, openErr := calibre.Open(library)
		if openErr != nil {
			return openErr
		}
		defer lib.Close()
		req.Output = flags.Arg(0)
		result, err = engine.ConvertBook(ctx, lib, book, from, to, req, stderr)
	} else {
		if book != 0 || from != "" || to != "" ||
			flags.NArg() != 2 && !(flags.NArg() == 1 && len(req.Pages) > 0) {
			flags.Usage()
			return errors.New("invalid input or output arguments")
		}
		req.Input, req.Output = flags.Arg(0), flags.Arg(1)
		result, err = engine.Convert(ctx, req, stderr)
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(result)
}
