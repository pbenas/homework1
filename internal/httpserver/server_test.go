package httpserver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pbenas/homework1/internal/store"
)

func testLogger(output io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(output, &slog.HandlerOptions{ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
		if attr.Key == slog.TimeKey {
			return slog.Attr{}
		}
		return attr
	}}))
}

func TestRequestLoggingMiddleware(t *testing.T) {
	var output bytes.Buffer
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /objects/{bucket}/{objectID}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	handler := requestLoggingMiddleware(mux, testLogger(&output))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/objects/documents/welcome", nil))

	for _, expected := range []string{
		"method=PUT",
		"bucket=documents",
		"object=welcome",
		"status=201",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("log output %q does not contain %q", output.String(), expected)
		}
	}
}

func TestRequestLoggingMiddlewareRecoversPanic(t *testing.T) {
	var output bytes.Buffer
	handler := requestLoggingMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("broken handler")
	}), testLogger(&output))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/panic", nil))

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", response.Code)
	}
	for _, expected := range []string{"request panic", "broken handler", "status=500"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("log output %q does not contain %q", output.String(), expected)
		}
	}
}

func TestResponseRecorder(t *testing.T) {
	underlying := httptest.NewRecorder()
	recorder := &responseRecorder{ResponseWriter: underlying}
	if _, err := recorder.Write([]byte("body")); err != nil {
		t.Fatal(err)
	}
	recorder.WriteHeader(http.StatusCreated)
	if recorder.status != http.StatusOK || underlying.Body.String() != "body" {
		t.Fatalf("status=%d body=%q", recorder.status, underlying.Body.String())
	}
	if err := http.NewResponseController(recorder).Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if recorder.Unwrap() != underlying {
		t.Fatal("Unwrap() did not return the underlying writer")
	}

	streamed := httptest.NewRecorder()
	streamRecorder := &responseRecorder{ResponseWriter: streamed}
	if _, err := streamRecorder.ReadFrom(strings.NewReader("streamed")); err != nil {
		t.Fatalf("ReadFrom() error = %v", err)
	}
	if streamed.Body.String() != "streamed" {
		t.Fatalf("streamed body = %q", streamed.Body.String())
	}
	if err := streamRecorder.Push("/asset", nil); !errors.Is(err, http.ErrNotSupported) {
		t.Fatalf("Push() error = %v", err)
	}
	if _, _, err := streamRecorder.Hijack(); err == nil {
		t.Fatal("Hijack() error = nil")
	}
}

func TestGeneratedRequestErrorHandler(t *testing.T) {
	request := httptest.NewRequest(http.MethodPut, "/", nil)
	for _, test := range []struct {
		err  error
		want int
	}{
		{errors.New("invalid"), http.StatusBadRequest},
		{&http.MaxBytesError{Limit: 1}, http.StatusRequestEntityTooLarge},
	} {
		response := httptest.NewRecorder()
		requestErrorHandler(response, request, test.err)
		if response.Code != test.want {
			t.Errorf("status = %d, want %d", response.Code, test.want)
		}
	}
}

func TestRequestValidationMiddleware(t *testing.T) {
	handler := New(
		Config{Address: "127.0.0.1:0", MaxObjectSize: 4},
		store.NewMemory(),
		testLogger(io.Discard),
	).http.Handler
	tests := []struct {
		name        string
		path        string
		contentType string
		body        []byte
		want        int
	}{
		{name: "valid", path: "/objects/bucket/id", contentType: "text/plain; charset=utf-8", body: []byte("text"), want: http.StatusCreated},
		{name: "missing content type", path: "/objects/bucket/no-content-type", body: []byte("text"), want: http.StatusUnsupportedMediaType},
		{name: "wrong content type", path: "/objects/bucket/json", contentType: "application/json", body: []byte("text"), want: http.StatusUnsupportedMediaType},
		{name: "wrong charset", path: "/objects/bucket/latin", contentType: "text/plain; charset=iso-8859-1", body: []byte("text"), want: http.StatusUnsupportedMediaType},
		{name: "too large", path: "/objects/bucket/large", contentType: "text/plain", body: []byte("large"), want: http.StatusRequestEntityTooLarge},
		{name: "invalid UTF-8", path: "/objects/bucket/invalid", contentType: "text/plain", body: []byte{0xff}, want: http.StatusBadRequest},
		{name: "long identifier", path: "/objects/bucket/" + strings.Repeat("a", maxIdentifierBytes+1), contentType: "text/plain", body: []byte("text"), want: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPut, test.path, bytes.NewReader(test.body))
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d; body = %q", response.Code, test.want, response.Body.String())
			}
		})
	}
}

type failingStore struct{}

func (failingStore) Create(context.Context, string, string, []byte) error {
	return errors.New("secret /storage/path")
}
func (failingStore) Get(context.Context, string, string) ([]byte, error) {
	return nil, errors.New("secret /storage/path")
}
func (failingStore) Delete(context.Context, string, string) error {
	return errors.New("secret /storage/path")
}

func TestHandlerHidesInternalErrors(t *testing.T) {
	var output bytes.Buffer
	handler := New(
		Config{Address: "127.0.0.1:0", MaxObjectSize: 10},
		failingStore{},
		testLogger(&output),
	).http.Handler
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/objects/bucket/id", nil))

	if response.Code != http.StatusInternalServerError ||
		strings.Contains(response.Body.String(), "secret") ||
		!strings.Contains(output.String(), "secret /storage/path") {
		t.Fatalf("status=%d response=%q log=%q", response.Code, response.Body.String(), output.String())
	}
}

func TestRunGracefulShutdownAndBindError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var output bytes.Buffer
	server := New(
		Config{Address: "127.0.0.1:0", MaxObjectSize: 10},
		store.NewMemory(),
		testLogger(&output),
	)
	if err := server.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(output.String(), "server stopped") {
		t.Fatalf("log output = %q", output.String())
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server = New(
		Config{Address: listener.Addr().String(), MaxObjectSize: 10},
		store.NewMemory(),
		testLogger(io.Discard),
	)
	if err := server.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "serve HTTP") {
		t.Fatalf("Run() error = %v", err)
	}
}
