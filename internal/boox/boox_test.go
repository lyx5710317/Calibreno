package boox

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const address = "http://192.168.1.20:8085"

func TestNormalizeAddress(t *testing.T) {
	for input, want := range map[string]string{
		" " + address + "/ ":          address,
		"HTTP://[FD12:0::1]:8085/":    "http://[fd12::1]:8085",
		"http://172.31.1.23:00080":    "http://172.31.1.23",
		"http://10.0.0.10:65535":      "http://10.0.0.10:65535",
		" http://[fc01::7a]:00080/  ": "http://[fc01::7a]",
	} {
		if value, err := NormalizeAddress(input); err != nil || value != want {
			t.Errorf("NormalizeAddress(%q) = %q, %v; want %q", input, value, err, want)
		}
	}
	for _, input := range []string{
		"boox.local:8085", "http://boox.local:8085", "https://192.168.1.20:8085", "http://127.0.0.1:8085", "http://[::1]:8085",
		"http://1.1.1.1:8085", "http://[::ffff:192.168.1.20]:8085", "http://[fe80::1]:8085", "http://[192.168.1.20]:8085",
		"http://192.168.1.20:0", "http://192.168.1.20:", "http://192.168.1.20:65536", "http://192.168.1.020:8085",
		address + "/#", address + "/?", address + "/%2f", address + "/api/device", address + "//", "http://user@192.168.1.20:8085",
	} {
		if _, err := NormalizeAddress(input); !errors.Is(err, ErrAddress) {
			t.Errorf("NormalizeAddress(%q) did not return ErrAddress: %v", input, err)
		}
	}
}

func TestBOOXDropSend(t *testing.T) {
	content := []byte(strings.Repeat("电子书\n", 20000))
	book := filepath.Join(t.TempDir(), "示例.txt")
	if err := os.WriteFile(book, content, 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		reply    string
		status   int
		outcome  Outcome
		wantErr  error
		location string
	}{
		{name: "confirmed", reply: `{"successful":true}`, outcome: Sent},
		{name: "rejected", reply: `{"successful":false,"message":"raw remote message"}`, wantErr: ErrRejected},
		{name: "no storage", status: http.StatusInsufficientStorage, wantErr: ErrRejected},
		{name: "unknown result", reply: `{"unrelated":true}`, outcome: Unconfirmed},
		{name: "invalid result type", reply: `{"successful":"true"}`, outcome: Unconfirmed},
		{name: "oversized response", reply: strings.Repeat(" ", replyLimit) + `{"successful":true}`, outcome: Unconfirmed},
		{name: "redirect", status: http.StatusTemporaryRedirect, location: "/elsewhere", outcome: Unconfirmed},
		{name: "connection lost", status: -1, outcome: Unconfirmed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var probes, uploads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				switch {
				case req.Method == http.MethodGet && req.URL.Path == "/api/device":
					probes.Add(1)
					_, _ = io.WriteString(w, `{"deviceName":" BOOX\n测试 ","model":"Spare"}`)
					return
				case req.Method == http.MethodPost && req.URL.Path == "/api/storage/upload":
					uploads.Add(1)
					checkMultipart(t, req, content)
				default:
					t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				if tc.status < 0 {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Errorf("disconnect: %v", err)
						return
					}
					_ = conn.Close()
					return
				}
				if tc.location != "" {
					w.Header().Set("Location", tc.location)
				}
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				_, _ = io.WriteString(w, tc.reply)
			}))
			defer server.Close()
			client := loopbackClient(t, server)
			if device, err := probe(t.Context(), client, address); err != nil || device != "BOOX测试" {
				t.Fatalf("probe = %q, %v", device, err)
			}
			outcome, err := send(t.Context(), client, address, "TXT", `示例"书.txt`, book)
			if !errors.Is(err, tc.wantErr) || outcome != tc.outcome {
				t.Errorf("send = %q, %v; want %q, %v", outcome, err, tc.outcome, tc.wantErr)
			}
			if err != nil && strings.Contains(err.Error(), "raw remote message") {
				t.Error("untrusted response message escaped into application errors")
			}
			if probes.Load() != 2 || uploads.Load() != 1 {
				t.Errorf("got %d probes and %d uploads; want 2 probes and 1 upload", probes.Load(), uploads.Load())
			}
		})
	}
}

func TestBOOXDropRequiresCompatibleProbe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/device" {
			t.Errorf("unsupported device received %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"unrelated":true}`)
	}))
	defer server.Close()
	client := loopbackClient(t, server)
	if _, err := send(t.Context(), client, address, "EPUB", "book.epub", "book.epub"); !errors.Is(err, ErrIncompatible) {
		t.Errorf("send = %v; want ErrIncompatible", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := probe(ctx, client, address); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled probe = %v; want context.Canceled", err)
	}
}

func TestBOOXDropValidatesSourceBeforeUpload(t *testing.T) {
	var probes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/device" {
			t.Errorf("invalid source caused request %s", r.URL.Path)
		}
		probes.Add(1)
		_, _ = io.WriteString(w, `{"deviceName":false,"model":"Note Air"}`)
	}))
	defer server.Close()
	client := loopbackClient(t, server)
	missing := filepath.Join(t.TempDir(), "missing.epub")
	if _, err := send(t.Context(), client, address, "KEPUB", "book.kepub", missing); !errors.Is(err, ErrUnsupportedFormat) {
		t.Errorf("unsupported format = %v", err)
	}
	if _, err := send(t.Context(), client, address, "EPUB", "nested/book.epub", missing); !errors.Is(err, ErrBadFileName) {
		t.Errorf("invalid filename = %v", err)
	}
	if _, err := send(t.Context(), client, address, "EPUB", "book.epub", missing); !errors.Is(err, ErrFileChanged) {
		t.Errorf("missing source = %v", err)
	}
	if probes.Load() != 1 {
		t.Errorf("got %d requests; want only the probe for the missing source", probes.Load())
	}
}

func loopbackClient(t *testing.T, server *httptest.Server) *http.Client {
	t.Helper()
	return newClient(func(ctx context.Context, network, host string) (net.Conn, error) {
		if host != "192.168.1.20:8085" {
			t.Errorf("client tried to dial %s", host)
			return nil, errors.New("unexpected test dial")
		}
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	})
}

func checkMultipart(t *testing.T, req *http.Request, want []byte) {
	t.Helper()
	raw, err := io.ReadAll(req.Body)
	if err != nil || int64(len(raw)) != req.ContentLength || len(req.TransferEncoding) != 0 {
		t.Errorf("stream: got %d bytes, length %d, encoding %v, error %v", len(raw), req.ContentLength, req.TransferEncoding, err)
		return
	}
	mediaType, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
		t.Errorf("upload Content-Type %q, %v", req.Header.Get("Content-Type"), err)
		return
	}
	parts := multipart.NewReader(bytes.NewReader(raw), params["boundary"])
	part, err := parts.NextPart()
	if err != nil {
		t.Errorf("multipart part: %v", err)
		return
	}
	if part.FormName() != "file" || part.FileName() != "示例%22书.txt" || part.Header.Get("Content-Type") != "application/octet-stream" {
		t.Errorf("multipart header: %v", part.Header)
	}
	if data, err := io.ReadAll(part); err != nil || !bytes.Equal(data, want) {
		t.Errorf("multipart file payload length %d, error %v", len(data), err)
	}
	if _, err := parts.NextPart(); !errors.Is(err, io.EOF) {
		t.Errorf("extra multipart part or invalid terminator: %v", err)
	}
}
