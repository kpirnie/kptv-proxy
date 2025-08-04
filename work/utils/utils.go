package utils

import (
	"kptv-proxy/work/config"
	"net/url"
	"strings"
)

// LogURL returns either the original URL or an obfuscated version for logging
func LogURL(cfg *config.Config, url string) string {
	if cfg.ObfuscateUrls {
		return ObfuscateURL(url)
	}
	return url
}

// Or if you prefer to pass just the flag:
func LogURLWithFlag(obfuscate bool, url string) string {
	if obfuscate {
		return ObfuscateURL(url)
	}
	return url
}

func SanitizeChannelName(name string) string {
	sanitized := name
	replacements := map[string]string{
		" ":  "_",
		",":  "_",
		"\"": "",
		"'":  "",
		"/":  "_",
		"\\": "_",
		"?":  "_",
		"&":  "_",
		"=":  "_",
		":":  "_",
		";":  "_",
		"|":  "_",
		"*":  "_",
		"<":  "_",
		">":  "_",
	}

	for old, new := range replacements {
		sanitized = strings.ReplaceAll(sanitized, old, new)
	}

	// Remove consecutive underscores
	for strings.Contains(sanitized, "__") {
		sanitized = strings.ReplaceAll(sanitized, "__", "_")
	}

	return strings.Trim(sanitized, "_")
}

func ObfuscateURL(urlStr string) string {
	if urlStr == "" {
		return ""
	}

	// Parse the URL
	u, err := url.Parse(urlStr)
	if err != nil {
		// If parsing fails, just obfuscate the whole thing
		return "***OBFUSCATED***"
	}

	// Keep scheme and host, obfuscate path and query
	result := u.Scheme + "://" + u.Host
	if u.Path != "" && u.Path != "/" {
		result += "/***"
	}
	if u.RawQuery != "" {
		result += "?***"
	}
	if u.Fragment != "" {
		result += "#***"
	}

	return result
}
