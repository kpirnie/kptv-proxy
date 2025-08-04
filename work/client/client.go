package client

import (
	"net/http"
	"time"

	"kptv-proxy/work/config"
)

// HeaderSettingClient wraps http.Client to automatically set headers
type HeaderSettingClient struct {
	Client *http.Client
	config *config.Config
}

// CustomResponseWriter wraps http.ResponseWriter to track headers and implement Flusher
type CustomResponseWriter struct {
	http.ResponseWriter
	WroteHeader bool
	statusCode  int
}

// HeaderSettingClient implementation
func NewHeaderSettingClient(config *config.Config) *HeaderSettingClient {
	client := &http.Client{
		Timeout: 0, // No overall timeout for streaming
		Transport: &http.Transport{
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   10,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			DisableKeepAlives:     false,
			ResponseHeaderTimeout: 30 * time.Second, // Only timeout for headers
		},
	}

	return &HeaderSettingClient{
		Client: client,
		config: config,
	}
}

func (hsc *HeaderSettingClient) Do(req *http.Request) (*http.Response, error) {
	hsc.setHeaders(req)
	return hsc.Client.Do(req)
}

func (hsc *HeaderSettingClient) setHeaders(req *http.Request) {
	req.Header.Set("User-Agent", hsc.config.UserAgent)
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Accept", "*/*")

	if hsc.config.ReqOrigin != "" {
		req.Header.Set("Origin", hsc.config.ReqOrigin)
	}
	if hsc.config.ReqReferrer != "" {
		req.Header.Set("Referer", hsc.config.ReqReferrer)
	}
}

// CustomResponseWriter implementation
func NewCustomResponseWriter(w http.ResponseWriter) *CustomResponseWriter {
	return &CustomResponseWriter{
		ResponseWriter: w,
		WroteHeader:    false,
		statusCode:     0,
	}
}

func (crw *CustomResponseWriter) WriteHeader(statusCode int) {
	if crw.WroteHeader {
		return
	}

	// Set default headers
	crw.Header().Set("Connection", "keep-alive")
	crw.Header().Set("Accept", "*/*")
	crw.Header().Set("Cache-Control", "no-cache")

	crw.statusCode = statusCode
	crw.ResponseWriter.WriteHeader(statusCode)
	crw.WroteHeader = true
}

func (crw *CustomResponseWriter) Write(b []byte) (int, error) {
	if !crw.WroteHeader {
		crw.WriteHeader(http.StatusOK)
	}
	return crw.ResponseWriter.Write(b)
}

// Implement http.Flusher interface
func (crw *CustomResponseWriter) Flush() {
	if flusher, ok := crw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}
