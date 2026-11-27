// Package boox sends books directly to the BOOXDrop device on the local network.
package boox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var (
	ErrAddress           = errors.New("enter the private HTTP address shown in BOOXDrop")
	ErrDisconnected      = errors.New("could not connect to BOOXDrop")
	ErrIncompatible      = errors.New("this address did not identify a BOOXDrop device")
	ErrRejected          = errors.New("BOOXDrop rejected the upload")
	ErrUnsupportedFormat = errors.New("BOOXDrop does not support this format")
	ErrBadFileName       = errors.New("invalid BOOXDrop file name")
	ErrFileChanged       = errors.New("the book file is empty, unavailable, or has changed")
)

type Outcome string

const (
	// Sent means the device confirmed a complete upload.
	Sent Outcome = "sent"
	// Unconfirmed means the upload was attempted and its receipt is uncertain.
	Unconfirmed Outcome = "unconfirmed"
)

const (
	probeTimeout  = 5 * time.Second
	transferLimit = 10 * time.Minute
	idleLimit     = 30 * time.Second
	replyLimit    = 32 << 10
)

// NormalizeAddress accepts the literal private IPv4 or IPv6 HTTP origin shown
// by BOOXDrop. The upload and probe paths are fixed by this package.
func NormalizeAddress(address string) (string, error) {
	raw := strings.TrimSpace(address)
	if strings.ContainsAny(raw, "?#%\\") {
		return "", ErrAddress
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, "http") || u.Opaque != "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || strings.HasSuffix(u.Host, ":") {
		return "", ErrAddress
	}
	addr, err := netip.ParseAddr(u.Hostname())
	if err != nil || !addr.IsPrivate() || addr.Is4In6() ||
		(addr.Is6() != strings.HasPrefix(u.Host, "[")) {
		return "", ErrAddress
	}
	host := addr.String()
	if port := u.Port(); port != "" {
		n, err := strconv.ParseUint(port, 10, 16)
		if err != nil || n == 0 || len(port) > 5 {
			return "", ErrAddress
		}
		if n != 80 {
			return "http://" + net.JoinHostPort(host, strconv.FormatUint(n, 10)), nil
		}
	}
	if addr.Is6() {
		host = "[" + host + "]"
	}
	return "http://" + host, nil
}

// Supports reports whether a format can be sent directly to BOOXDrop.
func Supports(format string) bool {
	switch strings.ToUpper(format) {
	case "EPUB", "PDF", "MOBI", "AZW3", "TXT", "DJVU", "FB2":
		return true
	default:
		return false
	}
}

var directClient = newClient((&net.Dialer{Timeout: probeTimeout}).DialContext)

func newClient(dial func(context.Context, string, string) (net.Conn, error)) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			// Dial the numeric address directly so the system proxy never receives a book.
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				conn, err := dial(ctx, network, address)
				if err != nil {
					return nil, err
				}
				return idleConn{conn}, nil
			},
			DisableKeepAlives:      true,
			MaxResponseHeaderBytes: replyLimit,
			ResponseHeaderTimeout:  idleLimit,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

type idleConn struct{ net.Conn }

func (c idleConn) Read(p []byte) (int, error) {
	if err := c.SetReadDeadline(time.Now().Add(idleLimit)); err != nil {
		return 0, err
	}
	return c.Conn.Read(p)
}

func (c idleConn) Write(p []byte) (int, error) {
	if err := c.SetWriteDeadline(time.Now().Add(idleLimit)); err != nil {
		return 0, err
	}
	return c.Conn.Write(p)
}

// Probe checks the configured device and returns its reported name or model.
func Probe(ctx context.Context, address string) (string, error) {
	return probe(ctx, directClient, address)
}

func probe(ctx context.Context, client *http.Client, address string) (string, error) {
	origin, err := NormalizeAddress(address)
	if err != nil {
		return "", err
	}
	check, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(check, http.MethodGet, origin+"/api/device", nil)
	if err != nil {
		return "", err
	}
	_, response, err := deviceReply(client, req)
	if err != nil {
		return "", connectionError(ctx, err)
	}
	for _, field := range []string{"deviceName", "model"} {
		var name string
		if json.Unmarshal(response[field], &name) != nil {
			continue
		}
		name = strings.TrimSpace(strings.Map(stripControl, name))
		if name != "" {
			chars := []rune(name)
			return string(chars[:min(len(chars), 100)]), nil
		}
	}
	return "", ErrIncompatible
}

func stripControl(r rune) rune {
	if r < 32 || r == 127 {
		return -1
	}
	return r
}

// Send probes the device and streams a multipart upload from path. A dropped
// connection after the upload connection is established needs user verification.
func Send(ctx context.Context, address, format, filename, path string) (Outcome, error) {
	return send(ctx, directClient, address, format, filename, path)
}

func send(ctx context.Context, client *http.Client, address, format, filename, path string) (Outcome, error) {
	origin, err := NormalizeAddress(address)
	if err != nil {
		return "", err
	}
	if !Supports(format) {
		return "", ErrUnsupportedFormat
	}
	if strings.TrimSpace(filename) == "" || filename == "." || filename == ".." || strings.ContainsAny(filename, "/\\") ||
		strings.ContainsFunc(filename, func(r rune) bool { return stripControl(r) < 0 }) {
		return "", ErrBadFileName
	}
	if _, err := probe(ctx, client, origin); err != nil {
		return "", err
	}
	file, info, err := openBook(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return upload(ctx, client, origin, filename, file, info)
}

func openBook(path string) (*os.File, os.FileInfo, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrFileChanged, err)
	}
	if !before.Mode().IsRegular() || before.Size() == 0 {
		return nil, nil, ErrFileChanged
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrFileChanged, err)
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		file.Close()
		return nil, nil, ErrFileChanged
	}
	return file, after, nil
}

func upload(ctx context.Context, client *http.Client, origin, filename string, file *os.File, info os.FileInfo) (Outcome, error) {
	transfer, cancel := context.WithTimeout(ctx, transferLimit)
	defer cancel()
	var connected atomic.Bool
	written := make(chan bool, 1)
	transfer = httptrace.WithClientTrace(transfer, &httptrace.ClientTrace{
		GotConn: func(httptrace.GotConnInfo) { connected.Store(true) },
		WroteRequest: func(result httptrace.WroteRequestInfo) {
			select {
			case written <- result.Err == nil:
			default:
			}
		},
	})
	var framing bytes.Buffer
	form := multipart.NewWriter(&framing)
	if _, err := form.CreateFormFile("file", strings.ReplaceAll(filename, `"`, "%22")); err != nil {
		return "", err
	}
	headerLength := framing.Len()
	if err := form.Close(); err != nil {
		return "", err
	}
	if info.Size() > math.MaxInt64-int64(framing.Len()) {
		return "", ErrFileChanged
	}
	// Send a known length for firmware that does not accept chunked uploads.
	prefix, suffix := framing.Bytes()[:headerLength], framing.Bytes()[headerLength:]
	body := io.MultiReader(bytes.NewReader(prefix), io.NewSectionReader(file, 0, info.Size()), bytes.NewReader(suffix))
	req, err := http.NewRequestWithContext(transfer, http.MethodPost, origin+"/api/storage/upload", body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.ContentLength = info.Size() + int64(framing.Len())
	status, response, err := deviceReply(client, req)
	if err != nil {
		if connected.Load() {
			return Unconfirmed, nil
		}
		return "", connectionError(ctx, err)
	}
	var successful *bool
	if err := json.Unmarshal(response["successful"], &successful); err != nil {
		successful = nil
	}
	if successful != nil && !*successful || status >= 400 && status < 500 || status == http.StatusInsufficientStorage {
		return "", ErrRejected
	}
	if successful != nil && *successful {
		select {
		case complete := <-written:
			if after, err := file.Stat(); complete && err == nil && after.Size() == info.Size() && after.ModTime().Equal(info.ModTime()) {
				return Sent, nil
			}
		case <-transfer.Done():
		}
	}
	return Unconfirmed, nil
}

func deviceReply(client *http.Client, req *http.Request) (int, map[string]json.RawMessage, error) {
	res, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	status := res.StatusCode
	if status < 200 || status >= 300 {
		return status, nil, nil
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, replyLimit+1))
	if err != nil {
		return status, nil, err
	}
	var reply map[string]json.RawMessage
	if len(data) > replyLimit || json.Unmarshal(data, &reply) != nil {
		return status, nil, nil
	}
	return status, reply, nil
}

func connectionError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("%w: %w", ErrDisconnected, err)
}
