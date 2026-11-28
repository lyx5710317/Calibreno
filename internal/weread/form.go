package weread

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

const documentFunctions = `
const uploadPage = /^\/web\/upload\/?$/.test(location.pathname);
const bodyText = () => document.body?.innerText || '';
const loginRequired = () => /使用微信扫一扫登录|微信扫码登录/.test(bodyText().replace(/\s/g, '')) ||
  [...document.querySelectorAll('iframe')].some(frame =>
    /^https:\/\/open\.weixin\.qq\.com\/connect\/qrconnect(?:[?#]|$)/.test(frame.src) && frame.getClientRects().length);
const buttonsNamed = name => [...document.querySelectorAll('button, [role="button"]')].filter(button =>
  [button.innerText, button.getAttribute('aria-label')].some(text => text?.trim() === name) &&
  !button.matches(':disabled') && button.getClientRects().length);
const uploadInput = () => {
  const inputs = document.querySelectorAll('input[type="file"]');
  if (inputs.length !== 1 || inputs[0].matches(':disabled')) return null;
  const input = inputs[0];
  const accept = (input.getAttribute('accept') || '').toLowerCase().split(',').map(x => x.trim()).filter(Boolean);
  return !accept.length || accept.some(x => ['.epub', 'application/epub+zip', 'application/*', '*/*'].includes(x)) ? input : null;
};
`

func (c *Client) pageCode(notOfficial, body string) string {
	origin, _ := json.Marshal(c.origin)
	return "(() => { if (location.origin !== " + string(origin) + ") return " + notOfficial + ";\n" + documentFunctions + body + "\n})()"
}

func evaluatePage(ctx context.Context, target *chromedp.Target, expression string, byValue bool) (*runtime.RemoteObject, error) {
	frame, err := cdp.Call(ctx, target, page.GetFrameTree, cdp.Empty{})
	if err != nil {
		return nil, err
	}
	if frame.FrameTree == nil || frame.FrameTree.Frame == nil {
		return nil, ErrFormUnavailable
	}
	world, err := cdp.Call(ctx, target, page.CreateIsolatedWorld, page.CreateIsolatedWorldParams{
		FrameID: frame.FrameTree.Frame.ID, WorldName: "Calibreno WeRead",
	})
	if err != nil {
		return nil, err
	}
	value, err := cdp.Call(ctx, target, runtime.Evaluate, runtime.EvaluateParams{
		Expression: expression, ContextID: world.ExecutionContextID, ReturnByValue: &byValue,
	})
	if err != nil {
		return nil, err
	}
	if value.ExceptionDetails != nil || value.Result == nil {
		return nil, ErrFormUnavailable
	}
	return value.Result, nil
}

func readPage[T any](ctx context.Context, expression string) (T, error) {
	return chromedp.Run(ctx, func(ctx context.Context, target *chromedp.Target) (T, error) {
		var result T
		value, err := evaluatePage(ctx, target, expression, true)
		if err != nil {
			return result, err
		}
		err = json.Unmarshal(value.Value, &result)
		return result, err
	})
}

type formState struct {
	Official bool
	Upload   bool
	Ready    bool
	Login    bool
	SignIn   bool
}

func (c *Client) scanForm(ctx context.Context) (formState, error) {
	return readPage[formState](ctx, c.pageCode("{}", `
const shelf = location.pathname === '/web/shelf' || location.pathname === '/';
return { Official: true, Upload: uploadPage, Ready: uploadPage && !!uploadInput(), Login: loginRequired(),
  SignIn: shelf && buttonsNamed('登录').length === 1 };
`))
}

func (c *Client) prepare(ctx context.Context) (Status, error) {
	if c.upload.finished {
		c.upload.cancel()
		c.upload = browserTab{}
	}
	if err := c.showTab(ctx, &c.upload); err != nil {
		return StatusManual, err
	}
	op, cancel := c.operation(ctx, c.upload.ctx, prepareTimeout)
	defer cancel()
	state, err := c.scanForm(op)
	if err != nil {
		return StatusManual, err
	}
	if state.Login {
		return StatusLoginRequired, nil
	}
	if !state.Upload {
		if err := chromedp.Do(op, chromedp.Navigate(c.origin+"/web/upload")); err != nil {
			return StatusManual, err
		}
		state, err = c.scanForm(op)
		if err != nil || !state.Official {
			return StatusManual, formError(err)
		}
		if !state.Upload && !state.Login {
			if err := c.openLogin(op); err != nil {
				return StatusManual, err
			}
		}
	}
	wait, cancelWait := context.WithTimeout(op, actionTimeout)
	defer cancelWait()
	signIn := state.SignIn
	for {
		state, err = c.scanForm(wait)
		switch {
		case err == nil && state.Login:
			return StatusLoginRequired, nil
		case err == nil && state.Ready:
			return StatusReady, nil
		case err == nil && !state.Official:
			return StatusManual, ErrFormUnavailable
		}
		if err == nil {
			signIn = state.SignIn
		}
		if !pollNext(wait) {
			if signIn {
				return StatusLoginRequired, nil
			}
			return StatusManual, ErrFormUnavailable
		}
	}
}

func formError(err error) error {
	if err != nil {
		return err
	}
	return ErrFormUnavailable
}

func (c *Client) openLogin(ctx context.Context) error {
	if err := chromedp.Do(ctx, chromedp.Navigate(c.origin+"/web/shelf")); err != nil {
		return err
	}
	wait, cancel := context.WithTimeout(ctx, actionTimeout)
	defer cancel()
	expression := c.pageCode("{State:'manual'}", `
if (loginRequired() || uploadPage) return {State:'ready'};
if (location.pathname !== '/web/shelf' && location.pathname !== '/') return {State:'manual'};
const entries = buttonsNamed('传书到手机');
if (entries.length !== 1) return {};
const button = entries[0];
button.scrollIntoView({block:'nearest', inline:'nearest'});
const bounds = button.getBoundingClientRect();
const x = bounds.x + bounds.width / 2, y = bounds.y + bounds.height / 2;
return button.contains(document.elementFromPoint(x, y)) ? {State:'click', X:x, Y:y} : {};
`)
	type loginEntry struct {
		State string
		X, Y  float64
	}
	for {
		entry, err := readPage[loginEntry](wait, expression)
		if err == nil {
			switch entry.State {
			case "ready":
				return nil
			case "click":
				return chromedp.Do(wait, chromedp.MouseClickXY(entry.X, entry.Y))
			case "manual":
				return ErrFormUnavailable
			}
		}
		if !pollNext(wait) {
			return ErrFormUnavailable
		}
	}
}

func pollNext(ctx context.Context) bool {
	delay := time.NewTimer(200 * time.Millisecond)
	defer delay.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-delay.C:
		return true
	}
}

type receipts struct{ imports, failures int }

var (
	importedText = regexp.MustCompile(`^(?:导入成功|(?:书籍|文件)导入成功|导入完成|已成功导入(?:书架)?|(?:已|成功)加入书架)[。！!]?$`)
	failedText   = regexp.MustCompile(`^(?:(?:书籍|文件)?(?:上传|导入)失败|(?:文件)?格式不支持|不支持(?:该|此)文件格式|(?:已)?超出(?:上传|导入)限额)(?:[，。：:！!].*)?$`)
)

func countReceipts(text string) (counts receipts) {
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case importedText.MatchString(line):
			counts.imports++
		case failedText.MatchString(line):
			counts.failures++
		}
	}
	return counts
}

func (c *Client) attach(ctx context.Context, path string) (receipts, bool, error) {
	var previous receipts
	var submitted bool
	err := chromedp.Do(ctx, chromedp.Func(func(ctx context.Context, target *chromedp.Target) error {
		expression := c.pageCode("null", `return uploadPage && !loginRequired() ? uploadInput() : null;`)
		input, err := evaluatePage(ctx, target, expression, false)
		if err != nil {
			return err
		}
		if input.ObjectID == "" {
			return ErrFormUnavailable
		}
		// Verify the same node is still unique on the official upload document,
		// then use its object handle so a navigation cannot redirect the file.
		byValue := true
		state, err := cdp.Call(ctx, target, runtime.CallFunctionOn, runtime.CallFunctionOnParams{
			ObjectID: input.ObjectID, ReturnByValue: &byValue,
			FunctionDeclaration: "function () { return " + c.pageCode("null", `
return uploadPage && !loginRequired() && this === uploadInput() && !this.files?.length ? bodyText() : null;
`) + "; }",
		})
		if err != nil {
			return err
		}
		if state.ExceptionDetails != nil || state.Result == nil || state.Result.Type != "string" {
			return ErrFormUnavailable
		}
		var before string
		if err := json.Unmarshal(state.Result.Value, &before); err != nil {
			return err
		}
		previous = countReceipts(before)
		submitted = true
		_, err = cdp.Call(ctx, target, dom.SetFileInputFiles, dom.SetFileInputFilesParams{Files: []string{path}, ObjectID: input.ObjectID})
		return err
	}))
	if err != nil {
		err = fmt.Errorf("attach EPUB on WeRead: %w", err)
	}
	return previous, submitted, err
}

func (c *Client) waitReceipt(ctx context.Context, previous receipts) Status {
	expression := c.pageCode("null", `return uploadPage && !loginRequired() ? bodyText() : null;`)
	for {
		text, err := readPage[*string](ctx, expression)
		if err == nil {
			if text == nil {
				return StatusPending
			}
			current := countReceipts(*text)
			switch {
			case current.failures > previous.failures:
				return StatusFailed
			case current.imports > previous.imports:
				return StatusImported
			}
		}
		if !pollNext(ctx) {
			return StatusPending
		}
	}
}
