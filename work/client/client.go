package client

import (
	"net/http"
	"time"
)

// HeaderSettingClient wraps http.Client to automatically set common HTTP headers
// for requests. It provides convenient methods for making requests with
// pre-configured headers like User-Agent, Origin, and Referer.
type HeaderSettingClient struct {
	Client *http.Client // Underlying HTTP client with custom transport settings
}

// CustomResponseWriter wraps http.ResponseWriter to track header status and
// automatically set common response headers. It also implements http.Flusher
// to support streaming responses.
type CustomResponseWriter struct {
	http.ResponseWriter      // Embedded ResponseWriter for default functionality
	WroteHeader         bool // Tracks whether headers have been written
	statusCode          int  // Stores the HTTP status code that was set
}

// NewHeaderSettingClient creates and returns a new HeaderSettingClient instance
// with optimized transport settings for streaming scenarios. The client has:
// - No overall request timeout (suitable for long-lived connections)
// - Connection pooling with keep-alives enabled
// - Reasonable timeouts for various connection phases
func NewHeaderSettingClient() *HeaderSettingClient {
	client := &http.Client{
		Timeout: 0, // No overall timeout for streaming scenarios
		Transport: &http.Transport{
			MaxIdleConns:          100,              // Maximum total idle connections
			MaxIdleConnsPerHost:   20,               // Increased from 10 for better reuse
			MaxConnsPerHost:       50,               // Limit concurrent connections per host
			IdleConnTimeout:       90 * time.Second, // Keep connections alive longer
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			DisableKeepAlives:     false,
			ResponseHeaderTimeout: 30 * time.Second,
			ForceAttemptHTTP2:     true,             // Enable HTTP/2 where available
			DisableCompression:    false,            // Allow compression
			WriteBufferSize:       32 * 1024,        // 32KB write buffer
			ReadBufferSize:        32 * 1024,        // 32KB read buffer
		},
	}

	return &HeaderSettingClient{
		Client: client,
	}
}

// Do executes an HTTP request using the underlying client without modifying headers.
// This method provides direct access to the standard http.Client Do method.
//
// Parameters:
//   - req: the HTTP request to execute
//
// Returns:
//   - *http.Response: the HTTP response
//   - error: any error encountered while making the request
func (hsc *HeaderSettingClient) Do(req *http.Request) (*http.Response, error) {
	return hsc.Client.Do(req)
}

// DoWithHeaders performs an HTTP request with custom headers automatically set.
// The method sets User-Agent, Connection, Accept, Origin, and Referer headers
// based on the provided parameters before executing the request.
//
// Parameters:
//   - req: the HTTP request to execute
//   - userAgent: custom User-Agent header value (optional)
//   - origin: custom Origin header value (optional)
//   - referrer: custom Referer header value (optional)
//
// Returns:
//   - *http.Response: the HTTP response
//   - error: any error encountered while making the request
func (hsc *HeaderSettingClient) DoWithHeaders(req *http.Request, userAgent, origin, referrer string) (*http.Response, error) {

	// Set common headers before executing
	hsc.setHeaders(req, userAgent, origin, referrer)
	return hsc.Client.Do(req)
}

// setHeaders configures common HTTP headers on the request for streaming scenarios.
// This includes User-Agent, Connection, Accept, Origin, and Referer headers.
// Empty string parameters are ignored (not set on the request).
//
// Parameters:
//   - req: the HTTP request to configure
//   - userAgent: optional User-Agent value
//   - origin: optional Origin value
//   - referrer: optional Referer value
func (hsc *HeaderSettingClient) setHeaders(req *http.Request, userAgent, origin, referrer string) {

	// Apply User-Agent if provided
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}

	// Always enforce persistent connections for streaming
	req.Header.Set("Connection", "keep-alive")

	// Accept any MIME type since stream content varies
	req.Header.Set("Accept", "*/*")

	// Set optional headers if provided
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if referrer != "" {
		req.Header.Set("Referer", referrer)
	}
}

// NewCustomResponseWriter creates and returns a new CustomResponseWriter that
// wraps the provided http.ResponseWriter. The wrapper tracks header writing
// status and automatically sets common response headers.
//
// Parameters:
//   - w: the underlying http.ResponseWriter to wrap
//
// Returns:
//   - *CustomResponseWriter: wrapped response writer with tracking
func NewCustomResponseWriter(w http.ResponseWriter) *CustomResponseWriter {
	return &CustomResponseWriter{
		ResponseWriter: w,
		WroteHeader:    false, // Headers not written yet
		statusCode:     0,     // Status code not set yet
	}
}

// WriteHeader sends HTTP response headers with the provided status code.
// This method ensures headers are only written once and automatically sets
// common headers: Connection, Accept, and Cache-Control.
//
// Parameters:
//   - statusCode: HTTP status code to send in the response
func (crw *CustomResponseWriter) WriteHeader(statusCode int) {

	// Prevent multiple calls to WriteHeader
	if crw.WroteHeader {
		return
	}

	// Set default headers for streaming responses
	crw.Header().Set("Connection", "keep-alive")  // Maintain persistent connection
	crw.Header().Set("Accept", "*/*")             // Allow any response type
	crw.Header().Set("Cache-Control", "no-cache") // Prevent browser/proxy caching

	// Record status code and forward call to underlying writer
	crw.statusCode = statusCode
	crw.ResponseWriter.WriteHeader(statusCode)
	crw.WroteHeader = true // Mark headers as written
}

// Write writes the data to the connection as part of an HTTP response.
// If headers haven't been written yet, it automatically calls WriteHeader(http.StatusOK)
// to send headers with a 200 status code before writing the data.
//
// Parameters:
//   - b: byte slice of response body data
//
// Returns:
//   - int: number of bytes written
//   - error: any error encountered while writing
func (crw *CustomResponseWriter) Write(b []byte) (int, error) {

	// Ensure headers are sent before writing body
	if !crw.WroteHeader {
		crw.WriteHeader(http.StatusOK)
	}
	return crw.ResponseWriter.Write(b)
}

// Flush implements the http.Flusher interface to support streaming responses.
// If the underlying ResponseWriter supports flushing, this method flushes
// any buffered data to the client immediately.
func (crw *CustomResponseWriter) Flush() {

	// Check if underlying writer supports Flusher
	if flusher, ok := crw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}
