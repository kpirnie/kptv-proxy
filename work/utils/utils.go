package utils

import (
	"fmt"
	"kptv-proxy/work/config"
	"net/url"
	"strings"
)

// LogURL provides intelligent URL formatting for logging output based on application
// configuration settings, enabling secure logging by obfuscating sensitive URL components
// when privacy protection is enabled. The function serves as the primary interface for
// URL logging throughout the application, ensuring consistent handling of potentially
// sensitive stream URLs, authentication tokens, and private server information.
//
// The function implements a simple but effective privacy protection mechanism that
// preserves essential debugging information (protocol, hostname) while hiding
// potentially sensitive path components, query parameters, and authentication details
// that might appear in application logs or monitoring systems.
//
// Parameters:
//   - cfg: application configuration containing URL obfuscation preference settings
//   - url: complete URL string that may contain sensitive authentication or path information
//
// Returns:
//   - string: original URL if obfuscation disabled, or privacy-protected version with sensitive parts masked
func LogURL(cfg *config.Config, url string) string {

	// Check configuration setting to determine if URL obfuscation is enabled
	if cfg.ObfuscateUrls {
		return ObfuscateURL(url)
	}
	return url
}

// LogURLWithFlag provides direct URL formatting control without requiring configuration
// object access, offering a lightweight alternative for logging contexts where only
// the obfuscation preference is available. This function is particularly useful in
// utility contexts or when passing configuration objects would create unnecessary
// coupling between components.
//
// The function implements identical obfuscation logic to LogURL but accepts the
// obfuscation preference as a direct boolean parameter rather than extracting it
// from a configuration structure, providing greater flexibility for diverse usage patterns.
//
// Parameters:
//   - obfuscate: boolean flag indicating whether URL obfuscation should be applied
//   - url: complete URL string potentially containing sensitive information
//
// Returns:
//   - string: original URL if obfuscation disabled, or privacy-protected version with sensitive components masked
func LogURLWithFlag(obfuscate bool, url string) string {
	if obfuscate {
		return ObfuscateURL(url)
	}
	return url
}

// SanitizeChannelName transforms channel names into URL-safe identifiers by replacing
// or removing characters that would cause issues in HTTP URLs, file paths, or web
// interfaces. The sanitization process implements a comprehensive character replacement
// strategy that maintains readability while ensuring compatibility with web standards
// and various client applications that may have restrictive character support.
//
// The function performs multiple sanitization passes:
//  1. Character replacement using a predefined mapping of problematic characters
//  2. Consecutive underscore consolidation to improve readability
//  3. Leading/trailing underscore removal for clean formatting
//
// This sanitization is essential for generating proxy URLs that work reliably across
// diverse client applications, operating systems, and network infrastructure while
// maintaining a reasonable resemblance to the original channel names for user recognition.
//
// Parameters:
//   - name: original channel name potentially containing special characters, spaces, or symbols
//
// Returns:
//   - string: URL-safe channel identifier suitable for use in streaming endpoints and file paths
func SanitizeChannelName(name string) string {
	sanitized := name

	// Define comprehensive character replacement mapping for URL safety
	replacements := map[string]string{
		" ":  "_", // Replace spaces with underscores for URL compatibility
		",":  "_", // Replace commas which can interfere with parameter parsing
		"\"": "",  // Remove double quotes which can break HTML attributes
		"'":  "",  // Remove single quotes which can break JavaScript strings
		"/":  "_", // Replace forward slashes which indicate path separators
		"\\": "_", // Replace backslashes which indicate path separators on Windows
		"?":  "_", // Replace question marks which indicate query string start
		"&":  "_", // Replace ampersands which separate query parameters
		"=":  "_", // Replace equals signs which separate parameter names/values
		":":  "_", // Replace colons which can interfere with port specifications
		";":  "_", // Replace semicolons which can interfere with parameter parsing
		"|":  "_", // Replace pipes which are used for command piping in shells
		"*":  "_", // Replace asterisks which are wildcard characters in many contexts
		"<":  "_", // Replace less-than signs which are HTML tag delimiters
		">":  "_", // Replace greater-than signs which are HTML tag delimiters
		"+":  "_",
	}

	// Apply all character replacements using the defined mapping
	for old, new := range replacements {
		sanitized = strings.ReplaceAll(sanitized, old, new)
	}

	// Consolidate consecutive underscores to improve readability and prevent
	// excessive underscore sequences that could result from multiple adjacent
	// special characters in the original name
	for strings.Contains(sanitized, "__") {
		sanitized = strings.ReplaceAll(sanitized, "__", "_")
	}

	// Remove leading and trailing underscores for clean, professional appearance
	// in URLs and user interfaces while maintaining internal structure
	return strings.Trim(sanitized, "_")
}

// ObfuscateURL implements privacy protection for sensitive URLs by masking path components,
// query parameters, and fragments while preserving essential connection information
// (protocol and hostname) needed for debugging and operational monitoring. The function
// provides a balanced approach to privacy that maintains debugging utility while
// protecting authentication tokens, private paths, and sensitive parameters.
//
// The obfuscation strategy preserves network-level information that is generally
// not sensitive (protocol scheme, hostname, port) while masking application-level
// details that commonly contain authentication tokens, private paths, session
// identifiers, and other confidential information that should not appear in logs.
//
// URL components are handled as follows:
//   - Protocol scheme (http/https): Preserved for connection debugging
//   - Hostname and port: Preserved for network troubleshooting
//   - Path: Replaced with "***" to hide private directories and endpoints
//   - Query parameters: Replaced with "***" to hide authentication tokens and session data
//   - Fragment: Replaced with "***" to hide client-side routing information
//
// Parameters:
//   - urlStr: complete URL string potentially containing sensitive authentication or path information
//
// Returns:
//   - string: privacy-protected URL with sensitive components masked, or "***OBFUSCATED***" if parsing fails
func ObfuscateURL(urlStr string) string {

	// Return empty string unchanged to avoid processing overhead
	if urlStr == "" {
		return ""
	}

	// Parse URL structure to enable component-level obfuscation
	u, err := url.Parse(urlStr)
	if err != nil {

		// Return generic obfuscation marker if URL parsing fails completely
		// This handles malformed URLs or non-standard URL formats gracefully
		return "***OBFUSCATED***"
	}

	// Construct obfuscated URL starting with preserved connection information
	result := u.Scheme + "://" + u.Host

	// Obfuscate path component if present and non-root
	// Root paths ("/") are preserved as they contain no sensitive information
	if u.Path != "" && u.Path != "/" {
		result += "/***"
	}

	// Obfuscate query parameters if present
	// Query parameters commonly contain authentication tokens, session IDs,
	// and other sensitive information that should be masked in logs
	if u.RawQuery != "" {
		result += "?***"
	}

	// Obfuscate fragment component if present
	// Fragments may contain client-side routing information or sensitive anchors
	if u.Fragment != "" {
		result += "#***"
	}

	return result
}

// FormatBytes converts raw byte counts into human-readable format using appropriate
// unit prefixes (B, KB, MB, GB, TB, PB) with intelligent precision scaling based
// on the magnitude of the value. The function provides consistent, professional
// formatting for displaying file sizes, bandwidth measurements, and memory usage
// statistics in user interfaces and logging output.
//
// The formatting algorithm implements several user experience optimizations:
//   - Automatic unit selection based on value magnitude for optimal readability
//   - Variable precision scaling to avoid unnecessary decimal places for large values
//   - Consistent width formatting for alignment in tabular displays
//   - Professional appearance suitable for both technical logs and user interfaces
//
// Precision rules optimize display clarity:
//   - Values ≥ 100: No decimal places (e.g., "150 MB")
//   - Values ≥ 10: One decimal place (e.g., "15.7 GB")
//   - Values < 10: Two decimal places (e.g., "1.23 TB")
//   - Raw bytes: Always displayed without decimal places
//
// Parameters:
//   - bytes: raw byte count as 64-bit signed integer supporting large file sizes and negative values
//
// Returns:
//   - string: human-readable size representation with appropriate unit suffix and precision
func FormatBytes(bytes int64) string {

	// Handle small values that don't require unit conversion
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}

	// Define unit progression for automatic selection
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	value := float64(bytes)
	unitIndex := 0

	// Convert to appropriate unit by successive division by 1024
	// Continue until value is under 1024 or we reach the largest supported unit
	for value >= 1024 && unitIndex < len(units)-1 {
		value /= 1024
		unitIndex++
	}

	// Apply precision scaling based on value magnitude for optimal readability
	if value >= 100 {

		// Large values: no decimal places needed for clarity
		return fmt.Sprintf("%.0f %s", value, units[unitIndex])
	} else if value >= 10 {

		// Medium values: one decimal place for reasonable precision
		return fmt.Sprintf("%.1f %s", value, units[unitIndex])
	} else {

		// Small values: two decimal places for maximum precision
		return fmt.Sprintf("%.2f %s", value, units[unitIndex])
	}
}
