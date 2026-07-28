// work/localscan/edit.go
package localscan

import (
	"fmt"
	"kptv-proxy/work/db"
	"kptv-proxy/work/logger"
)

// ApplyEdit persists edited metadata for a stored entry: it writes the .nfo
// sidecar, writes embedded tags and cover art for music, then re-scans the
// single file so the stored row reflects exactly what is now on disk.
//
// The sidecar write is the source of truth — a tag or cover failure is logged
// and does not abort the edit.
func ApplyEdit(edited *MediaEntry) (*MediaEntry, error) {
	if edited.Hash == "" {
		return nil, fmt.Errorf("entry hash is required")
	}

	src, err := db.GetLocalSource(edited.LocalSourceID)
	if err != nil {
		return nil, fmt.Errorf("local source %d not found", edited.LocalSourceID)
	}

	if _, err := WriteNFO(edited); err != nil {
		return nil, err
	}

	if edited.MediaType == "music" {
		if err := WriteAudioTags(edited); err != nil {
			logger.Warn("{localscan/edit - ApplyEdit} tag write failed for %s: %v", edited.Path, err)
		}
		if err := WriteAudioCover(edited); err != nil {
			logger.Warn("{localscan/edit - ApplyEdit} cover write failed for %s: %v", edited.Path, err)
		}
	}

	ResetFSCache()
	return NewScanner(src, Enrich).ScanFile(edited.Path)
}
