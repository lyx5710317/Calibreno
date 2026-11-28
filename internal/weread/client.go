package weread

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

const officialOrigin = "https://weread.qq.com"

const (
	startupTimeout = 15 * time.Second
	prepareTimeout = 30 * time.Second
	actionTimeout  = 6 * time.Second
	receiptTimeout = 30 * time.Second
)

var (
	ErrBrowserUnavailable = errors.New("Chrome or Edge was not found")
	ErrFormUnavailable    = errors.New("the official WeRead upload form is unavailable")
)

type browserTab struct {
	ctx      context.Context
	cancel   context.CancelFunc
	finished bool
}

// Client uses a visible local browser with its own persistent WeRead profile.
// Requests are serialized so each result can be attributed to one EPUB.
type Client struct {
	profileDir string
	origin     string
	executable string
	headless   bool
	waitResult time.Duration
	gate       chan struct{}
	life       context.Context
	stop       context.CancelFunc

	browser     browserTab
	cancelAlloc context.CancelFunc
	upload      browserTab
	shelf       browserTab
}

func NewClient(profileDir string) *Client {
	life, stop := context.WithCancel(context.Background())
	return &Client{profileDir: profileDir, origin: officialOrigin, waitResult: receiptTimeout,
		gate: make(chan struct{}, 1), life: life, stop: stop}
}

func (c *Client) lock(ctx context.Context) error {
	select {
	case <-c.life.Done():
		return c.life.Err()
	case <-ctx.Done():
		return ctx.Err()
	case c.gate <- struct{}{}:
		if err := c.life.Err(); err != nil {
			c.unlock()
			return err
		}
		if err := ctx.Err(); err != nil {
			c.unlock()
			return err
		}
		return nil
	}
}

func (c *Client) unlock() { <-c.gate }

// Close disconnects the dedicated browser and releases its profile.
func (c *Client) Close() {
	c.stop()
	c.gate <- struct{}{}
	defer c.unlock()
	c.clearBrowser()
}

func (c *Client) clearBrowser() {
	if c.browser.ctx != nil {
		closeCtx, cancel := context.WithTimeout(c.browser.ctx, 3*time.Second)
		_ = chromedp.Cancel(closeCtx)
		cancel()
		c.browser.cancel()
		c.cancelAlloc()
	}
	c.browser, c.upload, c.shelf = browserTab{}, browserTab{}, browserTab{}
	c.cancelAlloc = nil
}

func (c *Client) start(ctx context.Context) error {
	if c.browser.ctx != nil && c.browser.ctx.Err() == nil {
		return nil
	}
	c.clearBrowser()
	exe := c.executable
	if exe == "" {
		exe = installedBrowser()
		if exe == "" {
			return ErrBrowserUnavailable
		}
	}
	if c.profileDir == "" {
		return errors.New("the WeRead browser profile path is missing")
	}
	family := "chrome"
	if strings.Contains(strings.ToLower(filepath.Base(exe)), "edge") {
		family = "edge"
	}
	profileDir, err := filepath.Abs(filepath.Join(c.profileDir, family))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		return fmt.Errorf("prepare WeRead browser profile: %w", err)
	}
	allocator, cancelAllocator := chromedp.NewExecAllocator(context.Background(),
		chromedp.ExecPath(exe), chromedp.UserDataDir(profileDir), chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck, chromedp.Flag("lang", "zh-CN"),
		chromedp.Flag("headless", c.headless))
	browser, cancel := chromedp.NewContext(allocator)
	startup, cancelStartup := context.WithTimeout(ctx, startupTimeout)
	defer cancelStartup()
	// The first CDP operation owns the browser process. Stop it on a startup
	// deadline without tying its lifetime to later requests.
	stopStartup := context.AfterFunc(startup, cancel)
	stopClosing := context.AfterFunc(c.life, cancel)
	err = chromedp.Do(browser)
	stopClosing()
	stopStartup()
	if err == nil {
		err = startup.Err()
	}
	if err != nil {
		cancel()
		cancelAllocator()
		return fmt.Errorf("open the WeRead browser: %w", err)
	}
	c.browser = browserTab{ctx: browser, cancel: cancel}
	c.cancelAlloc = cancelAllocator
	return nil
}

func installedBrowser() string {
	var candidates []string
	switch runtime.GOOS {
	case "windows":
		for _, rel := range []string{`Google\Chrome\Application\chrome.exe`, `Microsoft\Edge\Application\msedge.exe`} {
			for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LOCALAPPDATA")} {
				if root != "" {
					candidates = append(candidates, filepath.Join(root, rel))
				}
			}
		}
	case "darwin":
		candidates = []string{"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge"}
	default:
		candidates = []string{"/usr/bin/google-chrome", "/usr/bin/google-chrome-stable", "/usr/bin/microsoft-edge", "/usr/bin/microsoft-edge-stable"}
	}
	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path
		}
	}
	return ""
}

func (c *Client) operation(ctx, tab context.Context, limit time.Duration) (context.Context, context.CancelFunc) {
	op, cancel := context.WithTimeout(tab, limit)
	stopRequest := context.AfterFunc(ctx, cancel)
	stopClose := context.AfterFunc(c.life, cancel)
	return op, func() { stopRequest(); stopClose(); cancel() }
}

func (c *Client) showTab(ctx context.Context, tab *browserTab) error {
	if err := c.start(ctx); err != nil {
		return err
	}
	bringToFront := func(ctx context.Context) error {
		op, cancel := c.operation(ctx, tab.ctx, actionTimeout)
		defer cancel()
		return chromedp.Do(op, chromedp.Func(func(ctx context.Context, target *chromedp.Target) error {
			_, err := cdp.Call(ctx, target, page.BringToFront, cdp.Empty{})
			return err
		}))
	}
	if tab.ctx != nil {
		if err := bringToFront(ctx); err == nil {
			return nil
		}
		tab.cancel()
	}
	tab.ctx, tab.cancel = chromedp.NewContext(c.browser.ctx)
	startup, cancelStartup := context.WithTimeout(ctx, actionTimeout)
	defer cancelStartup()
	stopStartup := context.AfterFunc(startup, tab.cancel)
	stopClose := context.AfterFunc(c.life, tab.cancel)
	err := chromedp.Do(tab.ctx)
	stopStartup()
	stopClose()
	if err != nil {
		tab.cancel()
		*tab = browserTab{}
		return err
	}
	return bringToFront(ctx)
}

// OpenShelf shows the official shelf using the same login as the upload tab.
// An upload awaiting confirmation stays open in its own tab.
func (c *Client) OpenShelf(ctx context.Context) error {
	if err := c.lock(ctx); err != nil {
		return err
	}
	defer c.unlock()
	if err := c.showTab(ctx, &c.shelf); err != nil {
		return err
	}
	op, cancel := c.operation(ctx, c.shelf.ctx, prepareTimeout)
	defer cancel()
	return chromedp.Do(op, chromedp.Navigate(c.origin+"/web/shelf"))
}

// Check opens the official upload workflow, prompting the user when WeRead
// requires a QR login in the dedicated browser.
func (c *Client) Check(ctx context.Context) (Status, error) {
	if err := c.lock(ctx); err != nil {
		return StatusManual, err
	}
	defer c.unlock()
	return c.prepare(ctx)
}

// Send attaches one EPUB to the official form and waits for a new receipt.
func (c *Client) Send(ctx context.Context, path string) (Status, error) {
	if !strings.EqualFold(filepath.Ext(path), ".epub") {
		return StatusFailed, ErrInvalidEPUB
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return StatusFailed, err
	}
	if err := checkEPUB(abs); err != nil {
		return StatusFailed, err
	}
	if err := c.lock(ctx); err != nil {
		return StatusManual, err
	}
	defer c.unlock()
	if status, err := c.prepare(ctx); status != StatusReady || err != nil {
		return status, err
	}

	tab := c.upload
	op, cancel := c.operation(ctx, tab.ctx, actionTimeout)
	previous, submitted, err := c.attach(op, abs)
	cancel()
	if !submitted {
		return StatusManual, err
	}
	// Once attachment was attempted, preserve this page and never resend based
	// on a timeout, navigation, or disconnect. The website may have the file.
	c.upload = browserTab{}
	if err != nil {
		return StatusPending, err
	}
	op, cancel = c.operation(ctx, tab.ctx, c.waitResult)
	defer cancel()
	status := c.waitReceipt(op, previous)
	if status != StatusPending {
		c.upload = tab
		c.upload.finished = true
	}
	return status, nil
}
