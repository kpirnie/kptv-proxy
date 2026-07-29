// work/localscan/enrich.go
package localscan

import (
	"kptv-proxy/work/logger"

	"go.senan.xyz/taglib"
)

// Enrich dispatches to the correct enrichment path for the entry's media type.
// Enrichment is strictly additive — path-derived fields are never overwritten.
// Satisfies the Enricher signature used by Scanner.
func Enrich(e *MediaEntry) {
	switch e.MediaType {
	case "music":
		enrichAudio(e)
		enrichNFO(e)
	case "movies", "shows":
		enrichVideo(e)
		enrichNFO(e)
		enrichRemote(e)
	}
}

// enrichAudio populates Duration and Year on a music entry, preferring
// ffprobe for duration and falling back to taglib when it is unavailable.
func enrichAudio(e *MediaEntry) {
	if FFProbeAvailable() {
		if dur, err := DurationViaFFProbe(e.Path); err == nil {
			e.Duration = dur
		} else {
			logger.Debug("{localscan/enrich - enrichAudio} ffprobe failed for %s: %v", e.Path, err)
			taglibDuration(e)
		}
	} else {
		taglibDuration(e)
	}

	if e.Year == "" {
		taglibYear(e)
	}
}

// enrichVideo populates Duration on a movie or show entry. ffprobe is the only
// source for video duration here; when unavailable the value stays -1 and the
// NFO runtime may fill it instead.
func enrichVideo(e *MediaEntry) {
	if !FFProbeAvailable() {
		return
	}
	dur, err := DurationViaFFProbe(e.Path)
	if err != nil {
		logger.Debug("{localscan/enrich - enrichVideo} ffprobe failed for %s: %v", e.Path, err)
		return
	}
	e.Duration = dur
}

// taglibDuration reads track length from embedded audio properties.
func taglibDuration(e *MediaEntry) {
	props, err := taglib.ReadProperties(e.Path)
	if err != nil {
		logger.Debug("{localscan/enrich - taglibDuration} failed for %s: %v", e.Path, err)
		return
	}
	e.Duration = int(props.Length.Seconds())
}

// taglibYear reads the year tag, normalising full dates down to the year part.
func taglibYear(e *MediaEntry) {
	tags, err := taglib.ReadTags(e.Path)
	if err != nil {
		return
	}
	if years := tags["YEAR"]; len(years) > 0 && years[0] != "" {
		y := years[0]
		if len(y) >= 4 {
			e.Year = y[:4]
		} else {
			e.Year = y
		}
	}
}
