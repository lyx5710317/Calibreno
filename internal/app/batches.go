package app

import (
	"context"
	"errors"
	"slices"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/batch"
	"dalibre/internal/calibre"
	"dalibre/internal/localhandoff"
)

type batchPage int

const (
	batchOverview batchPage = iota
	batchCompare
	batchMetadata
	batchCommon
	batchPreview
	batchResult
)

type batchView struct {
	active        *batch.Batch
	history       []batch.Summary
	mode          batchPage
	work, loading bool

	compare        string
	choice         int
	comparison     *batchComparison
	confirmReplace bool

	selected                  map[string]bool
	filter                    string
	language, publisher, tags string
	source                    string
	lookup                    map[string]*batchLookup
	preview                   []batchMetadataEdit
}

type batchComparison struct {
	book        *calibre.Book
	details     *calibre.Details
	translators []string
	hasFormat   bool
	digest      string
	err         error
}

func (a *App) beginBatchWork(label string) bool {
	if a.lib == nil || a.loading || a.batch.work || a.batch.loading || a.busy != "" {
		return false
	}
	a.batch.work = true
	a.busy, a.busyDone, a.busyTotal = label, 0, 0
	return true
}

func (a *App) endBatchWork() {
	a.batch.work = false
	a.busy, a.busyDone, a.busyTotal = "", 0, 0
}

func (a *App) showBatchWork(lib *calibre.Library, b *batch.Batch, err error) {
	a.endBatchWork()
	if a.lib == lib && b != nil {
		a.batch.active = b
		a.refreshBatchHistory()
		a.reload(nil)
	}
	if err != nil {
		a.toastError(err)
	}
}

func (a *App) refreshBatchHistory() {
	key := a.batchKey
	if key == "" {
		return
	}
	a.run(func() {
		list, err := a.batchStore.List(key)
		a.post(func() {
			if a.batchKey != key {
				return
			}
			if err != nil {
				a.toastError(err)
				return
			}
			a.batch.history = list
		})
	})
}

func (a *App) openBatches() {
	if a.lib == nil || a.loading {
		return
	}
	a.page = pageBatch
	a.batch.mode = batchOverview
	a.closeResults()
	if a.batch.active != nil || a.batch.loading || a.batch.work {
		return
	}
	key := a.batchKey
	a.batch.loading = true
	a.run(func() {
		list, err := a.batchStore.List(key)
		a.post(func() {
			if a.batchKey != key {
				return
			}
			a.batch.loading = false
			if err != nil {
				a.toastError(err)
				return
			}
			a.batch.history = list
			if len(list) > 0 {
				index := slices.IndexFunc(list, func(s batch.Summary) bool { return s.Pending() > 0 })
				a.selectBatch(list[max(0, index)].ID)
			}
			a.drainImports()
		})
	})
}

func (a *App) selectBatch(id string) {
	if a.lib == nil || a.batch.work || a.batch.loading {
		return
	}
	key, lib := a.batchKey, a.lib
	a.batch.loading = true
	a.run(func() {
		b, err := a.batchStore.Load(key, id)
		if err == nil {
			err = a.withHandoff(b, lib, func() error { return a.batchStore.Recover(b, lib) })
			// Previously processed batches remain visible if their inbox is unavailable
			// or another receiver owns a legacy delivery.
			if err != nil && b.Handoff != nil && len(b.Items) == 1 && b.Items[0].Intent == nil {
				if !errors.Is(err, localhandoff.ErrClaimed) && !errors.Is(err, localhandoff.ErrBusy) {
					receiptErr := err
					a.post(func() { a.toastError(receiptErr) })
				}
				err = nil
			}
		}
		if err == nil {
			err = a.recoverBatchMetadata(b, lib)
		}
		a.post(func() {
			if a.lib != lib {
				return
			}
			a.batch.loading = false
			if err != nil {
				a.toastError(err)
			} else {
				history := a.batch.history
				a.batch = batchView{active: b, history: history}
				a.refreshBatchHistory()
				a.reload(nil)
			}
			a.drainImports()
		})
	})
}

func (a *App) firstReview() {
	if a.batch.active == nil {
		return
	}
	for _, i := range a.batch.active.Items {
		if i.State == batch.Review {
			a.openBatchCompare(i.ID)
			return
		}
	}
	a.batch.mode = batchOverview
}

func (a *App) openBatchCompare(id string) {
	b := a.batch.active
	if b == nil || a.batch.work || a.batch.loading {
		return
	}
	item := b.Item(id)
	if item == nil || item.State != batch.Review {
		return
	}
	a.batch.mode, a.batch.compare = batchCompare, id
	a.batch.confirmReplace = false
	if len(item.Candidates) == 1 {
		a.loadBatchComparison(0)
	} else {
		a.loadBatchComparison(-1)
	}
}

func (a *App) loadBatchComparison(choice int) {
	b := a.batch.active
	if b == nil {
		return
	}
	item := b.Item(a.batch.compare)
	if item == nil || choice >= len(item.Candidates) || a.batch.loading {
		return
	}
	a.batch.choice, a.batch.comparison, a.batch.confirmReplace = choice, nil, false
	if choice < 0 {
		return
	}
	lib, selected := a.lib, item.Candidates[choice]
	a.batch.loading = true
	a.run(func() {
		data := &batchComparison{}
		data.book, data.err = selected.Book.Book(lib)
		if data.err == nil {
			data.details, data.err = lib.Details(data.book)
		}
		if data.err == nil {
			for _, file := range data.details.Files {
				if file.Format == "EPUB" && len(data.translators) == 0 {
					data.translators = calibre.Prepare(file.Path, calibre.Metadata{}, nil).Translators
				}
				if file.Format == item.Format {
					data.hasFormat = true
					data.digest, _, data.err = calibre.FileFingerprint(file.Path)
				}
			}
		}
		a.post(func() {
			if a.lib == lib && a.batch.active == b {
				a.batch.loading, a.batch.comparison = false, data
				a.drainImports()
			}
		})
	})
}

func (a *App) recheckBatchItem() {
	old, id, lib := a.batch.active, a.batch.compare, a.lib
	if old == nil || !a.beginBatchWork(locales.CommonLoading()) {
		return
	}
	a.run(func() {
		b, err := a.batchStore.Load(old.Library, old.ID)
		if err == nil {
			err = a.withHandoff(b, lib, func() error { return a.batchStore.Recheck(b, b.Item(id), lib) })
		}
		a.post(func() {
			a.showBatchWork(lib, b, err)
			if err == nil && a.page == pageBatch && a.batch.mode == batchCompare {
				if item := b.Item(id); item != nil && item.State == batch.Review {
					a.openBatchCompare(id)
				} else {
					a.firstReview()
				}
			}
			a.drainImports()
		})
	})
}

func (a *App) applyBatchDecision(act batch.Action) {
	old, id, comparison, lib := a.batch.active, a.batch.compare, a.batch.comparison, a.lib
	if old == nil || !a.beginBatchWork(locales.BatchApplying()) {
		return
	}
	var target *calibre.Book
	var before string
	if comparison != nil && comparison.err == nil {
		target = comparison.book
		if act == batch.Replace {
			before = comparison.digest
		}
	}
	after := a.hookPlugins()
	a.run(func() {
		b, err := a.batchStore.Load(old.Library, old.ID)
		if err == nil {
			err = a.withHandoff(b, lib, func() error {
				item := b.Item(id)
				if item == nil {
					return errors.New("missing batch item")
				}
				result, err := a.batchStore.Apply(b, item, lib, act, target, before)
				if err == nil && result != nil {
					afterAdd(context.Background(), lib, after, result.ID, item.Format, act == batch.AddBook)
				}
				return err
			})
		}
		a.post(func() {
			a.showBatchWork(lib, b, err)
			if a.lib == lib && a.page == pageBatch && a.batch.mode == batchCompare && a.batch.compare == id {
				if err == nil {
					a.firstReview()
				} else {
					a.loadBatchComparison(a.batch.choice)
				}
			}
			a.drainImports()
		})
	})
}
