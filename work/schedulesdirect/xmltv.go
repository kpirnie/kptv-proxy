package schedulesdirect

import (
	"fmt"
	"kptv-proxy/work/logger"
	"strings"
	"time"
)

// GenerateXMLTV converts fetched SD account data into XMLTV-formatted
// channel and programme string slices, matching the format expected by
// FetchEPGData in work/proxy/epg.go.
func GenerateXMLTV(data *AccountData) (channels []string, programmes []string) {
	channels = generateChannels(data)
	programmes = generateProgrammes(data)

	logger.Debug("{schedulesdirect/xmltv - GenerateXMLTV} Account %s: generated %d channels, %d programmes",
		data.AccountName, len(channels), len(programmes))

	return channels, programmes
}

// generateChannels converts SD station data into XMLTV <channel> elements.
func generateChannels(data *AccountData) []string {
	results := make([]string, 0, len(data.Stations))

	for _, station := range data.Stations {
		var sb strings.Builder

		sb.WriteString(fmt.Sprintf(`<channel id="%s">`, xmlEscape(station.StationID)))
		sb.WriteString("\n")

		// Display name — prefer callsign, fall back to name
		displayName := station.Callsign
		if displayName == "" {
			displayName = station.Name
		}
		sb.WriteString(fmt.Sprintf(`  <display-name>%s</display-name>`, xmlEscape(displayName)))
		sb.WriteString("\n")

		// Full name if different from callsign
		if station.Name != "" && station.Name != displayName {
			sb.WriteString(fmt.Sprintf(`  <display-name>%s</display-name>`, xmlEscape(station.Name)))
			sb.WriteString("\n")
		}

		// Channel number if available
		if ch, ok := data.ChannelMap[station.StationID]; ok && ch != "" {
			sb.WriteString(fmt.Sprintf(`  <display-name>%s</display-name>`, xmlEscape(ch)))
			sb.WriteString("\n")
		}

		// Logo
		if station.Logo != nil && station.Logo.URL != "" {
			sb.WriteString(fmt.Sprintf(`  <icon src="%s"`, xmlEscape(station.Logo.URL)))
			if station.Logo.Width > 0 {
				sb.WriteString(fmt.Sprintf(` width="%d"`, station.Logo.Width))
			}
			if station.Logo.Height > 0 {
				sb.WriteString(fmt.Sprintf(` height="%d"`, station.Logo.Height))
			}
			sb.WriteString(" />")
			sb.WriteString("\n")
		}

		sb.WriteString("</channel>")
		sb.WriteString("\n")

		results = append(results, sb.String())
	}

	return results
}

// generateProgrammes converts SD schedule and program data into XMLTV <programme> elements.
func generateProgrammes(data *AccountData) []string {
	// Pre-calculate total capacity for slice allocation
	total := 0
	for _, airings := range data.Schedule {
		total += len(airings)
	}
	results := make([]string, 0, total)

	for stationID, airings := range data.Schedule {
		for _, airing := range airings {
			prog, ok := data.Programs[airing.ProgramID]
			if !ok {
				logger.Debug("{schedulesdirect/xmltv - generateProgrammes} No program detail for %s, skipping",
					airing.ProgramID)
				continue
			}

			xml := buildProgramme(stationID, airing, prog)
			if xml != "" {
				results = append(results, xml)
			}
		}
	}

	return results
}

// buildProgramme constructs a single XMLTV <programme> element from an airing
// and its associated program detail.
func buildProgramme(stationID string, airing SDAiring, prog SDProgram) string {
	// Parse air time
	startTime, err := time.Parse(time.RFC3339, airing.AirDateTime)
	if err != nil {
		logger.Warn("{schedulesdirect/xmltv - buildProgramme} Could not parse airDateTime %s: %v",
			airing.AirDateTime, err)
		return ""
	}

	stopTime := startTime.Add(time.Duration(airing.Duration) * time.Second)

	// XMLTV time format: 20060102150405 +0000
	const xmltvTime = "20060102150405 -0700"
	start := startTime.UTC().Format(xmltvTime)
	stop := stopTime.UTC().Format(xmltvTime)

	// Extract title
	title := ""
	if len(prog.Titles) > 0 {
		title = prog.Titles[0].Title120
	}
	if title == "" {
		title = airing.ProgramID
	}

	var sb strings.Builder

	sb.WriteString(fmt.Sprintf(`<programme start="%s" stop="%s" channel="%s">`,
		start, stop, xmlEscape(stationID)))
	sb.WriteString("\n")

	// Title
	sb.WriteString(fmt.Sprintf(`  <title lang="en">%s</title>`, xmlEscape(title)))
	sb.WriteString("\n")

	// Episode title
	if prog.EpisodeTitle != "" {
		sb.WriteString(fmt.Sprintf(`  <sub-title lang="en">%s</sub-title>`, xmlEscape(prog.EpisodeTitle)))
		sb.WriteString("\n")
	}

	// Description — prefer long, fall back to short
	desc := extractDescription(prog)
	if desc != "" {
		sb.WriteString(fmt.Sprintf(`  <desc lang="en">%s</desc>`, xmlEscape(desc)))
		sb.WriteString("\n")
	}

	// Credits block
	credits := buildCredits(prog)
	if credits != "" {
		sb.WriteString(credits)
	}

	// Original air date
	if prog.OriginalAirDate != "" {
		sb.WriteString(fmt.Sprintf(`  <date>%s</date>`, xmlEscape(strings.ReplaceAll(prog.OriginalAirDate, "-", ""))))
		sb.WriteString("\n")
	}

	// Genres/categories
	for _, genre := range prog.Genres {
		sb.WriteString(fmt.Sprintf(`  <category lang="en">%s</category>`, xmlEscape(genre)))
		sb.WriteString("\n")
	}

	// Season/episode numbers (XMLTV onscreen format)
	episodeNum := extractEpisodeNum(prog)
	if episodeNum != "" {
		sb.WriteString(fmt.Sprintf(`  <episode-num system="xmltv_ns">%s</episode-num>`, episodeNum))
		sb.WriteString("\n")
	}

	// New flag
	if airing.New {
		sb.WriteString(`  <new />`)
		sb.WriteString("\n")
	} else {
		sb.WriteString(`  <previously-shown />`)
		sb.WriteString("\n")
	}

	// Live/tape flag
	if airing.LiveTapeDelay == "Live" {
		sb.WriteString(`  <live />`)
		sb.WriteString("\n")
	}

	// Premiere/finale
	if airing.IsPremiereOrFinale != "" {
		sb.WriteString(fmt.Sprintf(`  <premiere>%s</premiere>`, xmlEscape(airing.IsPremiereOrFinale)))
		sb.WriteString("\n")
	}

	// Content rating
	for _, rating := range prog.ContentRating {
		if rating.Body != "" && rating.Code != "" {
			sb.WriteString(fmt.Sprintf(`  <rating system="%s"><value>%s</value></rating>`,
				xmlEscape(rating.Body), xmlEscape(rating.Code)))
			sb.WriteString("\n")
		}
	}

	sb.WriteString("</programme>")
	sb.WriteString("\n")

	return sb.String()
}

// buildCredits constructs the XMLTV <credits> block from cast/crew data.
func buildCredits(prog SDProgram) string {
	if len(prog.Cast) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("  <credits>\n")

	for _, member := range prog.Cast {
		role := strings.ToLower(member.Role)
		tag := ""

		switch {
		case strings.Contains(role, "director"):
			tag = "director"
		case strings.Contains(role, "writer"):
			tag = "writer"
		case strings.Contains(role, "producer"):
			tag = "producer"
		case strings.Contains(role, "presenter"):
			tag = "presenter"
		case strings.Contains(role, "host"):
			tag = "presenter"
		case strings.Contains(role, "actor"), strings.Contains(role, "voice"):
			tag = "actor"
		default:
			tag = "actor"
		}

		if member.CharacterName != "" && tag == "actor" {
			sb.WriteString(fmt.Sprintf(`    <%s role="%s">%s</%s>`,
				tag, xmlEscape(member.CharacterName), xmlEscape(member.Name), tag))
		} else {
			sb.WriteString(fmt.Sprintf(`    <%s>%s</%s>`,
				tag, xmlEscape(member.Name), tag))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("  </credits>\n")
	return sb.String()
}

// extractDescription returns the best available description for a program,
// preferring long descriptions over short ones, and English over other languages.
func extractDescription(prog SDProgram) string {
	if prog.Descriptions == nil {
		return ""
	}

	// Try long description first
	for _, d := range prog.Descriptions.Description1000 {
		if strings.EqualFold(d.DescriptionLanguage, "en") {
			return d.Description
		}
	}
	if len(prog.Descriptions.Description1000) > 0 {
		return prog.Descriptions.Description1000[0].Description
	}

	// Fall back to short description
	for _, d := range prog.Descriptions.Description100 {
		if strings.EqualFold(d.DescriptionLanguage, "en") {
			return d.Description
		}
	}
	if len(prog.Descriptions.Description100) > 0 {
		return prog.Descriptions.Description100[0].Description
	}

	return ""
}

// extractEpisodeNum builds an xmltv_ns format episode number string
// (season.episode.part) from Gracenote metadata if available.
func extractEpisodeNum(prog SDProgram) string {
	for _, meta := range prog.Metadata {
		if meta.Gracenote == nil {
			continue
		}
		gn := meta.Gracenote
		if gn.Season > 0 || gn.Episode > 0 {
			// xmltv_ns is zero-indexed
			return fmt.Sprintf("%d.%d.0", gn.Season-1, gn.Episode-1)
		}
	}
	return ""
}

// xmlEscape replaces XML special characters with their entity equivalents.
func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return s
}
