package admin

import (
	"fmt"
	"log"
	"strings"
	"time"
)

// adminStartTime records the admin interface initialization timestamp for uptime
// calculation and performance monitoring across the administrative interface lifecycle.
var adminStartTime = time.Now()

// formatDuration converts time.Duration to a human-readable string format,
// scaling output from seconds through days based on magnitude.
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	} else if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	} else if d < 24*time.Hour {
		hours := int(d.Hours())
		minutes := int(d.Minutes()) % 60
		return fmt.Sprintf("%dh %dm", hours, minutes)
	} else {
		days := int(d.Hours()) / 24
		hours := int(d.Hours()) % 24
		return fmt.Sprintf("%dd %dh", days, hours)
	}
}

// LogWithAdmin provides enhanced logging that captures output for both standard
// logging and admin interface display simultaneously.
func LogWithAdmin(l *log.Logger, level, format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	l.Printf("[%s] %s", strings.ToUpper(level), message)
	addLogEntry(level, message)
}
