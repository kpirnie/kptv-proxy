// work/localscan/extensions.go
package localscan

// ExtensionsByType maps a media type name to the set of file extensions it
// owns. Extensions are lowercase and include the leading dot.
var ExtensionsByType = map[string]map[string]bool{
	"music": {
		".mp3": true, ".flac": true, ".aac": true, ".ogg": true,
		".wav": true, ".wma": true, ".m4a": true, ".opus": true,
		".aiff": true, ".ape": true, ".wv": true,
	},
	"movies": {
		".mp4": true, ".mkv": true, ".avi": true, ".mov": true,
		".wmv": true, ".m4v": true, ".flv": true, ".webm": true,
		".ts": true, ".m2ts": true, ".mpg": true, ".mpeg": true,
	},
	"shows": {
		".mp4": true, ".mkv": true, ".avi": true, ".mov": true,
		".wmv": true, ".m4v": true, ".flv": true, ".webm": true,
		".ts": true, ".m2ts": true, ".mpg": true, ".mpeg": true,
	},
}

// MatchExtension returns true if the given lowercase extension belongs to mediaType.
func MatchExtension(mediaType, ext string) bool {
	exts, ok := ExtensionsByType[mediaType]
	if !ok {
		return false
	}
	return exts[ext]
}
