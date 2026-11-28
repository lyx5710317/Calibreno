package app

import (
	"context"
	"path/filepath"
	"slices"
	"time"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/calibre"
	"dalibre/internal/weread"
)

type weReadClient interface {
	Check(context.Context) (weread.Status, error)
	Send(context.Context, string) (weread.Status, error)
	OpenShelf(context.Context) error
	Close()
}

type weReadRequest struct {
	Title string
	lib   *calibre.Library
	id    int64
	uuid  string
}

type weReadState struct {
	client     weReadClient
	history    *weread.History
	records    []weread.Record
	historyErr string
	status     weread.Status
	busy       bool
	cancel     context.CancelFunc
	done       <-chan struct{}

	duplicate *weread.Record
	resend    weReadRequest
	manual    weReadRequest
}

func newWeReadState(settingsPath string) weReadState {
	var state weReadState
	var historyPath string
	if settingsPath != "" {
		dir := filepath.Join(filepath.Dir(settingsPath), "weread")
		state.client = weread.NewClient(filepath.Join(dir, "browser"))
		historyPath = filepath.Join(dir, "transfers.json")
	}
	history, err := weread.OpenHistory(historyPath)
	if err != nil {
		state.historyErr = locales.WeReadHistoryError()
		return state
	}
	state.history, state.records = history, history.Recent()
	return state
}

func (a *App) weReadBook(req weReadRequest) *calibre.Book {
	if req.lib == nil || a.lib != req.lib || a.loading {
		return nil
	}
	if b := a.byID[req.id]; b != nil && b.UUID == req.uuid {
		return b
	}
	return nil
}

// runWeRead serializes the browser and record actions initiated by the desktop.
func (a *App) runWeRead(timeout time.Duration, work func(context.Context) func()) {
	state := &a.weRead
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	done := make(chan struct{})
	state.busy, state.cancel, state.done = true, cancel, done
	a.run(func() {
		apply := work(ctx)
		cancel()
		close(done)
		a.post(func() {
			state.busy, state.cancel, state.done = false, nil, nil
			apply()
		})
	})
}

// ShutdownWeRead lets an in-flight upload record its outcome before closing Chrome or Edge.
func (a *App) ShutdownWeRead() {
	state := &a.weRead
	if state.cancel != nil {
		state.cancel()
		select {
		case <-state.done:
		case <-time.After(2 * time.Second):
		}
	}
	if state.client != nil {
		state.client.Close()
	}
}

func (a *App) checkWeRead() {
	state := &a.weRead
	if state.busy {
		return
	}
	client := state.client
	if client == nil {
		state.status = weread.StatusManual
		return
	}
	a.runWeRead(40*time.Second, func(ctx context.Context) func() {
		status, err := client.Check(ctx)
		if status == "" || err != nil && status == weread.StatusReady {
			status = weread.StatusManual
		}
		return func() {
			state.status = status
			if err != nil {
				a.toastErrorSummary(locales.TransferWeReadOpenFailed(), err)
			}
		}
	})
}

func (a *App) prepareWeRead(b *calibre.Book) {
	if a.lib == nil || b == nil || a.busy != "" || a.loading || a.weRead.busy {
		return
	}
	request := weReadRequest{Title: b.Title, lib: a.lib, id: b.ID, uuid: b.UUID}
	if slices.Contains(b.Formats, "EPUB") {
		a.startWeReadSend(request, "")
		return
	}
	if source := sourceForWeRead(b.Formats); source != "" && a.conversionEnabled() {
		a.openBookConversion(b, source)
		if s := a.conversion; s != nil && s.form.open && s.lib == request.lib && s.book == request.id {
			s.onEPUB = func() { a.startWeReadSend(request, "") }
		}
	}
	a.toast("%s", locales.WeReadNeedsEPUB())
}

func (a *App) startWeReadSend(request weReadRequest, approvedDigest string) {
	state := &a.weRead
	b := a.weReadBook(request)
	if b == nil || state.busy || a.busy != "" {
		return
	}
	request.Title = b.Title
	state.duplicate, state.resend, state.manual = nil, weReadRequest{}, weReadRequest{}
	if state.client == nil || state.history == nil {
		state.status, state.manual = weread.StatusManual, request
		a.weReadOutcomeToast(request, state.status, nil)
		return
	}
	state.status = weread.StatusSending
	a.busy = locales.WeReadSending()
	client, history := state.client, state.history
	a.runWeRead(85*time.Second, func(ctx context.Context) func() {
		result := sendWeRead(ctx, client, history, request.lib, b, approvedDigest)
		return func() {
			a.busy = ""
			state.records = history.Recent()
			if result.historyErr != nil {
				state.historyErr = locales.WeReadHistoryError()
				a.toastErrorSummary(state.historyErr, result.historyErr)
			} else if result.recorded {
				state.historyErr = ""
			}
			state.status = result.status
			if result.duplicate != nil {
				state.duplicate, state.resend = result.duplicate, request
				a.toasts = append(a.toasts, toastMsg{
					text: locales.WeReadDuplicate(), action: locales.SettingsSectionTransfer(),
					onAction: func() { a.openTransferSettings(transferWeRead) },
				})
				return
			}
			if result.status == weread.StatusManual {
				state.manual = request
			}
			a.weReadOutcomeToast(request, result.status, result.err)
		}
	})
}

type weReadResult struct {
	status     weread.Status
	duplicate  *weread.Record
	err        error
	historyErr error
	recorded   bool
}

func sendWeRead(ctx context.Context, client weReadClient, history *weread.History, lib *calibre.Library, b *calibre.Book, approvedDigest string) weReadResult {
	_, file, err := libraryFormatPaths(lib, b, "EPUB")
	if err != nil {
		return weReadResult{status: weread.StatusFailed, err: err}
	}
	digest, err := weread.FileDigest(file)
	if err != nil {
		return weReadResult{status: weread.StatusFailed, err: err}
	}
	if previous, ok := history.Duplicate(digest); ok && digest != approvedDigest {
		return weReadResult{status: previous.Status, duplicate: &previous}
	}
	if err := ctx.Err(); err != nil {
		return weReadResult{status: weread.StatusFailed, err: err}
	}
	record, err := history.Begin(b.Title, digest)
	if err != nil {
		return weReadResult{status: weread.StatusManual, historyErr: err}
	}
	status, err := client.Send(ctx, file)
	switch status {
	case weread.StatusImported, weread.StatusPending, weread.StatusFailed, weread.StatusManual, weread.StatusLoginRequired:
	default:
		status = weread.StatusPending
	}
	historyErr := history.Finish(record.ID, status)
	return weReadResult{status: status, err: err, historyErr: historyErr, recorded: historyErr == nil}
}

func (a *App) weReadOutcomeToast(request weReadRequest, status weread.Status, err error) {
	message := weReadStatusText(status)
	toast := toastMsg{text: message}
	switch status {
	case weread.StatusFailed:
		if err != nil {
			a.toastErrorSummary(message, err)
			return
		}
		toast.error = true
	case weread.StatusPending:
		toast.action, toast.onAction = locales.WeReadReviewAction(), a.reviewWeReadShelf
	case weread.StatusManual:
		toast.action = locales.WeReadManualAction()
		toast.onAction = func() { a.manualWeReadFor(request) }
	}
	a.toasts = append(a.toasts, toast)
}

func (a *App) resendWeRead() {
	if duplicate := a.weRead.duplicate; duplicate != nil {
		a.startWeReadSend(a.weRead.resend, duplicate.Digest)
	}
}

func (a *App) confirmWeRead(id string) {
	state := &a.weRead
	history := state.history
	if state.busy || history == nil {
		return
	}
	a.runWeRead(5*time.Second, func(context.Context) func() {
		err := history.Confirm(id)
		return func() {
			if err != nil {
				state.historyErr = locales.WeReadHistoryError()
				a.toastErrorSummary(state.historyErr, err)
				return
			}
			state.records, state.historyErr = history.Recent(), ""
			lastResult := len(state.records) > 0 && state.records[0].ID == id
			if state.duplicate != nil && state.duplicate.ID == id {
				if index := slices.IndexFunc(state.records, func(r weread.Record) bool { return r.ID == id }); index >= 0 {
					state.duplicate = &state.records[index]
				}
				lastResult = true
			}
			if lastResult && state.status == weread.StatusPending {
				state.status = weread.StatusImported
			}
		}
	})
}

func (a *App) reviewWeReadShelf() {
	state := &a.weRead
	if state.busy || state.client == nil {
		return
	}
	client := state.client
	a.runWeRead(35*time.Second, func(ctx context.Context) func() {
		err := client.OpenShelf(ctx)
		return func() {
			if err != nil {
				a.toastErrorSummary(locales.TransferWeReadOpenFailed(), err)
			}
		}
	})
}

func (a *App) openWeReadShelf() {
	if err := a.readingTransfers.openURL(weread.UploadPage); err != nil {
		a.toastErrorSummary(locales.TransferWeReadOpenFailed(), err)
	}
}

func (a *App) manualWeRead(b *calibre.Book) {
	if b != nil {
		a.manualWeReadFor(weReadRequest{Title: b.Title, lib: a.lib, id: b.ID, uuid: b.UUID})
	}
}

func (a *App) manualWeReadRequest() { a.manualWeReadFor(a.weRead.manual) }

func (a *App) manualWeReadFor(request weReadRequest) {
	b := a.weReadBook(request)
	if b == nil || a.weRead.busy || a.busy != "" {
		return
	}
	if !slices.Contains(b.Formats, "EPUB") {
		a.toast("%s", locales.WeReadNeedsEPUB())
		return
	}
	open, reveal, copyPath := a.readingTransfers.openURL, a.readingTransfers.reveal, a.readingTransfers.copyPath
	a.runWeRead(15*time.Second, func(context.Context) func() {
		file, _, err := libraryFormatPaths(request.lib, b, "EPUB")
		if err == nil {
			err = weread.Handoff(file, open, reveal)
		}
		return func() {
			if err != nil {
				a.toastErrorSummary(locales.WeReadOpenFailed(), err)
				return
			}
			a.toasts = append(a.toasts, toastMsg{
				text: locales.WeReadOpened(), action: locales.WeReadCopyPath(),
				onAction: func() {
					if err := copyPath(file); err != nil {
						a.toastErrorSummary(locales.WeReadCopyPath(), err)
						return
					}
					a.toast("%s", locales.WeReadCopiedPath())
				},
			})
		}
	})
}
