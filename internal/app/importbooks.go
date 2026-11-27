package app

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/batch"
	"dalibre/internal/calibre"
	"dalibre/internal/plugin"
)

type importRequest struct {
	key   string
	paths []string
}

func (a *App) importFiles(paths []string) {
	if a.lib == nil || a.loading || len(paths) == 0 {
		return
	}
	a.imports = append(a.imports, importRequest{a.batchKey, paths})
	a.drainImports()
}

func (a *App) drainImports() {
	if a.loading || a.busy != "" || a.batch.loading || len(a.imports) == 0 || a.lib == nil {
		return
	}
	request := a.imports[0]
	a.imports = a.imports[1:]
	lib, store := a.lib, a.batchStore
	if a.batchKey != request.key {
		return
	}
	hooks, after := a.importPlugins(), a.hookPlugins()
	a.busy, a.busyDone, a.busyTotal = locales.BatchImporting(), 0, 0
	a.batch.work = true
	a.run(func() {
		b := batch.New(request.key, filepath.Base(request.paths[0]))
		if len(request.paths) > 1 {
			b.Source = ""
		}
		b.Items = scanImport(request.paths)
		err := store.Save(b)
		if err == nil {
			err = a.runImport(b, lib, hooks, after, "")
		}
		a.post(func() {
			a.endBatchWork()
			if err != nil {
				a.toastError(err)
			}
			if a.lib == lib {
				a.page = pageBatch
				a.batch = batchView{active: b, history: a.batch.history}
				a.refreshBatchHistory()
				a.reload(nil)
			}
			a.drainImports()
		})
	})
}

func scanImport(paths []string) []batch.Item {
	seen := map[string]bool{}
	var items []batch.Item
	add := func(path string, state batch.State, err error) {
		abs, absoluteErr := filepath.Abs(path)
		if absoluteErr == nil {
			path = abs
		}
		key := path
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if seen[key] {
			return
		}
		seen[key] = true
		item := batch.NewItem(path, state)
		if err != nil {
			item.Problem, item.Detail = "read", err.Error()
		}
		items = append(items, item)
	}
	for _, root := range paths {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			switch {
			case err != nil:
				add(path, batch.Failed, err)
			case d.IsDir():
			case isEbook(path):
				add(path, batch.Waiting, nil)
			case path == root:
				add(path, batch.Unsupported, nil)
			}
			return nil
		})
	}
	return items
}

func (a *App) runImport(b *batch.Batch, lib *calibre.Library, hooks, after []*plugin.Plugin, only string) error {
	store := a.batchStore
	if err := store.Recover(b, lib); err != nil {
		return err
	}
	index, err := lib.Duplicates()
	if err != nil {
		return err
	}
	ctx := context.Background()
	total := b.Summary().Files
	a.post(func() { a.busyTotal = total })
	doneCount := 0
	for n := range b.Items {
		item := &b.Items[n]
		if (only == "" && item.State != batch.Waiting) || (only != "" && item.ID != only) {
			continue
		}
		if item.State == batch.Review || item.State == batch.Added || item.State == batch.Replaced || item.State == batch.Supplement {
			continue
		}
		path := item.Path
		if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
			if err == nil {
				err = fs.ErrInvalid
			}
			if storeErr := store.RecordError(b, item, batch.Failed, "read", err); storeErr != nil {
				return storeErr
			}
			continue
		}
		item.State = batch.Waiting
		file, clean, warnings := processImport(ctx, hooks, path)
		prepared := calibre.Prepare(file, calibre.Metadata{}, nil)
		err := store.Prepare(b, item, prepared, index)
		clean()
		for _, warning := range warnings {
			a.post(func() { a.toastError(warning) })
		}
		if err != nil {
			return err
		}
		if item.State == batch.Waiting {
			book, writeErr := store.Apply(b, item, lib, batch.AddBook, nil, "")
			if writeErr != nil && (item.State != batch.Failed || errors.Is(writeErr, batch.ErrStorage)) {
				return writeErr
			}
			if book != nil {
				index.Added(book.ID, item.Metadata)
				afterAdd(ctx, lib, after, book.ID, item.Format, true)
			}
		}
		doneCount++
		done := doneCount
		a.post(func() { a.busyDone = done })
	}
	return nil
}

func (a *App) retryImport(id string) {
	lib, b := a.lib, a.batch.active
	if b == nil || !a.beginBatchWork(locales.BatchImporting()) {
		return
	}
	hooks, after := a.importPlugins(), a.hookPlugins()
	a.run(func() {
		work, err := a.batchStore.Load(b.Library, b.ID)
		if err == nil && id != "" && work.Item(id) == nil {
			err = errors.New("missing batch item")
		}
		if err == nil {
			err = a.runImport(work, lib, hooks, after, id)
		}
		a.post(func() {
			a.showBatchWork(lib, work, err)
			a.drainImports()
		})
	})
}

const supportedFormats = "EPUB, PDF, MOBI, AZW3, AZW, FB2, CBZ, DJVU, DOCX, RTF, PRC, TXT"

func isEbook(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".epub", ".pdf", ".mobi", ".azw3", ".azw", ".fb2", ".txt", ".cbz", ".djvu", ".docx", ".rtf", ".prc":
		return true
	}
	return false
}
