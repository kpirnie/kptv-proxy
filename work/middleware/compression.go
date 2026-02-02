package middleware

import (
	"io"
	"kptv-proxy/work/logger"
	"net/http"
	"strings"
	"sync"

	"github.com/klauspost/compress/gzip"
)

// gzipWriterPool maintains a reusable pool of gzip writers to avoid repeated allocation
// overhead on every compressed response. Writers are initialized at BestSpeed compression
// level, prioritizing throughput over compression ratio for real-time HTTP responses.
var gzipWriterPool = sync.Pool{
	New: func() interface{} {
		w, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
		return w
	},
}

// gzipResponseWriter wraps an http.ResponseWriter with a gzip-compressing io.Writer,
// intercepting Write calls to transparently compress response bodies before they are
// sent to the client. It tracks header write state to ensure proper status code handling.
type gzipResponseWriter struct {
	io.Writer                // Embedded gzip writer for compressed output
	http.ResponseWriter      // Embedded original response writer for header access
	wroteHeader         bool // Tracks whether WriteHeader has been called
}

// WriteHeader records the HTTP status code on the underlying ResponseWriter and marks
// the header as written to prevent duplicate header writes on subsequent Write calls.
func (w *gzipResponseWriter) WriteHeader(status int) {
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

// Write compresses and writes the byte slice to the underlying gzip writer. If no
// explicit status code has been set via WriteHeader, it defaults to 200 OK before
// writing the first chunk of response data to maintain proper HTTP semantics.
func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.Writer.Write(b)
}

// Flush ensures both the gzip compression buffer and the underlying HTTP response
// writer are flushed to the client. This enables streaming responses where data
// needs to be delivered incrementally rather than buffered until connection close.
func (w *gzipResponseWriter) Flush() {
	// flush the gzip writer's internal buffer first
	if gzw, ok := w.Writer.(*gzip.Writer); ok {
		gzw.Flush()
	}

	// then flush the underlying response writer if it supports it
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// GzipMiddleware wraps an http.HandlerFunc with transparent gzip response compression.
// It checks the incoming request's Accept-Encoding header for gzip support and, when
// present, acquires a pooled gzip writer to compress the response body. Requests from
// clients that do not advertise gzip support are passed through unmodified.
//
// The middleware manages the full lifecycle of the pooled writer: acquisition, reset
// to the current response writer, deferred close and return to the pool, ensuring
// no writer leaks occur even if the downstream handler panics.
func GzipMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {

		// pass through if the client doesn't accept gzip encoding
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next(w, r)
			return
		}

		// set the appropriate encoding header and remove content-length
		// since compressed size is unknown until the response is fully written
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Del("Content-Length")

		// acquire a gzip writer from the pool and reset it for this response
		gz := gzipWriterPool.Get().(*gzip.Writer)
		gz.Reset(w)
		defer func() {
			if err := gz.Close(); err != nil {
				logger.Error("{compression - GzipMiddleware} failed to close gzip writer for: %s %s - %v", r.Method, r.URL.Path, err)
			}
			gzipWriterPool.Put(gz)
		}()

		// wrap the response writer with our gzip-compressing writer
		gzw := &gzipResponseWriter{
			Writer:         gz,
			ResponseWriter: w,
		}

		// hand off to the next handler with the compressed writer
		next(gzw, r)
	}
}
