package app

import (
	"slices"
	"time"

	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/app/icons"
	"dalibre/internal/app/theme"
	"dalibre/internal/app/widgets"
	"dalibre/internal/weread"
)

func weReadStatusText(status weread.Status) string {
	switch status {
	case weread.StatusReady:
		return locales.WeReadStatusReady()
	case weread.StatusLoginRequired:
		return locales.WeReadStatusLoginRequired()
	case weread.StatusManual:
		return locales.WeReadStatusManual()
	case weread.StatusSending:
		return locales.WeReadStatusSending()
	case weread.StatusImported:
		return locales.WeReadStatusImported()
	case weread.StatusPending:
		return locales.WeReadStatusPending()
	case weread.StatusFailed:
		return locales.WeReadStatusFailed()
	}
	return ""
}

func weReadCheckLabel(status weread.Status) string {
	switch status {
	case "":
		return locales.WeReadSignIn()
	case weread.StatusLoginRequired:
		return locales.WeReadRetryAfterQR()
	case weread.StatusManual, weread.StatusFailed:
		return locales.SettingsCheckAgain()
	}
	return locales.WeReadCheckConnection()
}

func (a *App) weReadSettingsPane(c *ui.Context) {
	state := &a.weRead
	pending := slices.ContainsFunc(state.records, func(record weread.Record) bool {
		return record.Status == weread.StatusPending
	})
	pane(c, func() {
		ui.Column(c.Key("connection")).Gap(theme.Space10).AlignItems(ui.Stretch).Children(func() {
			heading(c, locales.TransferWeRead(), locales.TransferWeReadHelp())
			status := weReadStatusText(state.status)
			if status != "" || state.busy {
				ui.Row(c).Gap(theme.Space8).AlignItems(ui.Center).Children(func() {
					if state.busy {
						ui.Spinner(c).Size(16, 16)
					}
					if status != "" {
						color := pal().TextSecondary
						if state.status == weread.StatusReady || state.status == weread.StatusImported {
							color = pal().Success.FG
						}
						ui.Text(c, status).FontSize(12).TextColor(color)
					}
				})
			}
			if state.status == weread.StatusLoginRequired {
				ui.Text(c, locales.WeReadLoginHelp()).FontSize(12).TextColor(pal().TextSecondary)
			}
			ui.Row(c).Gap(theme.Space8).AlignItems(ui.Center).Wrap().Children(func() {
				if widgets.ButtonSized(c.Key("check"), widgets.Primary, nil, weReadCheckLabel(state.status), theme.ControlS).Disabled(state.busy).Clicked() {
					a.checkWeRead()
				}
				if widgets.ButtonSized(c.Key("shelf"), widgets.Secondary, icons.External, locales.TransferWeReadOpen(), theme.ControlS).Clicked() {
					a.openWeReadShelf()
				}
				if (pending || state.duplicate != nil) && widgets.ButtonSized(c.Key("review"), widgets.Secondary, icons.External, locales.WeReadReviewAction(), theme.ControlS).Disabled(state.busy).Clicked() {
					a.reviewWeReadShelf()
				}
			})
		})
		if state.manual.Title != "" {
			widgets.Hairline(c.Key("manual-divider"))
			ui.Row(c.Key("manual")).Gap(theme.Space12).AlignItems(ui.Center).Children(func() {
				heading(c, state.manual.Title, locales.WeReadStatusManual())
				if widgets.ButtonSized(c, widgets.Secondary, icons.External, locales.WeReadManualAction(), theme.ControlS).Disabled(state.busy).Clicked() {
					a.manualWeReadRequest()
				}
			})
		}
		if record := state.duplicate; record != nil {
			widgets.Hairline(c.Key("duplicate-divider"))
			ui.Column(c.Key("duplicate")).Gap(theme.Space8).AlignItems(ui.Stretch).Children(func() {
				ui.Text(c, locales.WeReadDuplicate()).FontSize(12).TextColor(pal().TextSecondary)
				ui.Row(c).Gap(theme.Space12).AlignItems(ui.Center).Children(func() {
					weReadRecordDetails(c, *record)
					if widgets.ButtonSized(c, widgets.Secondary, nil, locales.WeReadResend(), theme.ControlS).Disabled(state.busy).Clicked() {
						a.resendWeRead()
					}
				})
			})
		}
		widgets.Hairline(c.Key("history-divider"))
		a.weReadHistoryPane(c.Key("history"))
	})
}

func (a *App) weReadHistoryPane(c *ui.Context) {
	state := &a.weRead
	ui.Column(c).Gap(theme.Space8).AlignItems(ui.Stretch).Children(func() {
		heading(c, locales.WeReadHistory(), "")
		if state.historyErr != "" {
			ui.Text(c, locales.WeReadHistoryError()).FontSize(12).TextColor(pal().Danger.FG)
		}
		if len(state.records) == 0 && state.historyErr == "" {
			ui.Text(c, locales.WeReadHistoryEmpty()).FontSize(12).TextColor(pal().TextTertiary)
		}
		for _, record := range state.records {
			ui.Row(c.Key(record.ID)).Padding(theme.Space6, 0).Gap(theme.Space12).AlignItems(ui.Center).Children(func() {
				weReadRecordDetails(c, record)
				if record.Status == weread.StatusPending && widgets.ButtonSized(c, widgets.Secondary, nil, locales.WeReadConfirm(), theme.ControlS).Disabled(state.busy).Clicked() {
					a.confirmWeRead(record.ID)
				}
			})
		}
	})
}

func weReadRecordDetails(c *ui.Context, record weread.Record) {
	status := weReadStatusText(record.Status)
	if record.Status == weread.StatusImported && !record.ConfirmedAt.IsZero() {
		status = locales.WeReadStatusConfirmed()
	}
	ui.Column(c).Grow(1).MinWidth(0).Gap(theme.Space4).Children(func() {
		ui.Text(c, record.Title).FontSize(13).SingleLine()
		ui.Row(c).Gap(theme.Space8).AlignItems(ui.Center).Wrap().Children(func() {
			ui.Text(c, status).FontSize(12).TextColor(pal().TextSecondary)
			if !record.At.IsZero() {
				ui.Text(c, record.At.Local().Format(time.DateTime)).FontSize(12).TextColor(pal().TextTertiary)
			}
		})
	})
}
