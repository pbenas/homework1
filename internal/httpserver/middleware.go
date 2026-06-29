package httpserver

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
	"unicode/utf8"
)

const maxIdentifierBytes = 180

type responseRecorder struct {
	http.ResponseWriter
	status int
}

// Unwrap lets http.ResponseController access optional features of the original
// writer without the logging middleware hiding them.
func (recorder *responseRecorder) Unwrap() http.ResponseWriter {
	return recorder.ResponseWriter
}

func (recorder *responseRecorder) Flush() {
	_ = http.NewResponseController(recorder.ResponseWriter).Flush()
}

func (recorder *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(recorder.ResponseWriter).Hijack()
}

func (recorder *responseRecorder) Push(target string, options *http.PushOptions) error {
	if pusher, ok := recorder.ResponseWriter.(http.Pusher); ok {
		return pusher.Push(target, options)
	}
	return http.ErrNotSupported
}

func (recorder *responseRecorder) ReadFrom(reader io.Reader) (int64, error) {
	if recorder.status == 0 {
		recorder.WriteHeader(http.StatusOK)
	}
	if readerFrom, ok := recorder.ResponseWriter.(io.ReaderFrom); ok {
		return readerFrom.ReadFrom(reader)
	}
	return io.Copy(struct{ io.Writer }{recorder.ResponseWriter}, reader)
}

func (recorder *responseRecorder) WriteHeader(status int) {
	if recorder.status != 0 {
		return
	}
	recorder.status = status
	recorder.ResponseWriter.WriteHeader(status)
}

func (recorder *responseRecorder) Write(body []byte) (int, error) {
	if recorder.status == 0 {
		recorder.WriteHeader(http.StatusOK)
	}
	return recorder.ResponseWriter.Write(body)
}

func requestLoggingMiddleware(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		recorder := &responseRecorder{ResponseWriter: w}
		started := time.Now()
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Error(
					"request panic",
					"method", request.Method,
					"path", request.URL.Path,
					"value", recovered,
					"stack", string(debug.Stack()),
				)
				if recorder.status == 0 {
					http.Error(recorder, "internal server error", http.StatusInternalServerError)
				}
			}
			status := recorder.status
			if status == 0 {
				status = http.StatusOK
			}
			bucket, objectID := requestIdentifiers(request)
			logger.Info(
				"request",
				"method", request.Method,
				"bucket", bucket,
				"object", objectID,
				"status", status,
				"duration", time.Since(started).Round(time.Microsecond),
			)
		}()
		next.ServeHTTP(recorder, request)
	})
}

func requestIdentifiers(request *http.Request) (string, string) {
	bucket, objectID := request.PathValue("bucket"), request.PathValue("objectID")
	if bucket == "" {
		bucket = "-"
	}
	if objectID == "" {
		objectID = "-"
	}
	return bucket, objectID
}

func requestValidationMiddleware(next http.Handler, maxObjectSize int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		bucket, objectID := request.PathValue("bucket"), request.PathValue("objectID")
		if bucket != "" || objectID != "" {
			if !validIdentifier(bucket) || !validIdentifier(objectID) {
				http.Error(w, "invalid bucket or object ID", http.StatusBadRequest)
				return
			}
		}
		if request.Method == http.MethodPut && bucket != "" {
			mediaType, params, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
			if err != nil || !strings.EqualFold(mediaType, "text/plain") ||
				(params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) {
				http.Error(w, "Content-Type must be text/plain with UTF-8 charset", http.StatusUnsupportedMediaType)
				return
			}
			limitedBody := http.MaxBytesReader(w, request.Body, maxObjectSize)
			data, err := io.ReadAll(limitedBody)
			if err != nil {
				var tooLarge *http.MaxBytesError
				if errors.As(err, &tooLarge) {
					http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
					return
				}
				http.Error(w, "invalid request body", http.StatusBadRequest)
				return
			}
			if !utf8.Valid(data) {
				http.Error(w, "request body must be valid UTF-8", http.StatusBadRequest)
				return
			}
			request.Body = io.NopCloser(bytes.NewReader(data))
		}
		next.ServeHTTP(w, request)
	})
}

func validIdentifier(value string) bool {
	return value != "" &&
		utf8.ValidString(value) &&
		len(value) <= maxIdentifierBytes &&
		!strings.Contains(value, "/")
}
