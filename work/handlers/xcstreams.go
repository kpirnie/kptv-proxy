package handlers

import (
	"fmt"
	"hash/fnv"
	"kptv-proxy/work/localscan"
	"kptv-proxy/work/proxy"
	"kptv-proxy/work/types"
	"kptv-proxy/work/utils"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// xcChannelBatch is a lightweight name+channel pair for sorted iteration.
type xcChannelBatch struct {
	name        string
	channel     *types.Channel
	sourceOrder int
	importOrder int
}

var (
	streamIDIndex    atomic.Pointer[map[int]string]
	streamIDIndexGen atomic.Uint64
	streamIDIndexMu  sync.Mutex
)

// getSortedChannels snapshots the channel map and returns it sorted alphabetically
// by channel name. All XC output functions must use this instead of ranging the
// map directly to guarantee consistent ordering across every response.
func getSortedChannels(sp *proxy.StreamProxy) []xcChannelBatch {
	batch := make([]xcChannelBatch, 0, 1000)
	sp.Channels.Range(func(name string, ch *types.Channel) bool {
		batch = append(batch, xcChannelBatch{name: name, channel: ch})
		return true
	})
	if sp.Config.SortField == "preserve-order" {
		snapshotXCOriginalOrder(batch)
		sort.Slice(batch, func(i, j int) bool {
			return xcChannelOriginalOrderLess(batch[i], batch[j])
		})
	} else {
		sort.Slice(batch, func(i, j int) bool {
			return strings.ToLower(batch[i].name) < strings.ToLower(batch[j].name)
		})
	}
	return batch
}

// snapshotXCOriginalOrder fills each entry's original-order keys under a single
// read lock per channel, so the sort comparator never takes a lock.
func snapshotXCOriginalOrder(batch []xcChannelBatch) {
	for i := range batch {
		batch[i].sourceOrder, batch[i].importOrder = xcChannelOriginalOrder(batch[i].channel)
	}
}

func xcChannelOriginalOrderLess(a, b xcChannelBatch) bool {
	if a.sourceOrder != b.sourceOrder {
		return a.sourceOrder < b.sourceOrder
	}
	if a.importOrder != b.importOrder {
		return a.importOrder < b.importOrder
	}
	return strings.ToLower(a.name) < strings.ToLower(b.name)
}

func xcChannelOriginalOrder(ch *types.Channel) (int, int) {
	ch.Mu.RLock()
	defer ch.Mu.RUnlock()

	if len(ch.Streams) == 0 {
		return int(^uint(0) >> 1), int(^uint(0) >> 1)
	}

	sourceOrder := ch.Streams[0].Source.Order
	importOrder := ch.Streams[0].ImportOrder
	for _, stream := range ch.Streams[1:] {
		if stream.Source.Order < sourceOrder || (stream.Source.Order == sourceOrder && stream.ImportOrder < importOrder) {
			sourceOrder = stream.Source.Order
			importOrder = stream.ImportOrder
		}
	}
	return sourceOrder, importOrder
}

// streamIDFromName generates a stable positive integer stream ID from a channel name
// using FNV32a hashing to produce consistent IDs across restarts.
func streamIDFromName(name string) int {
	h := fnv.New32a()
	h.Write([]byte(name))
	id := int(h.Sum32() & 0x7FFFFFFF)
	if id == 0 {
		id = 1
	}
	return id
}

// categoryIDFromName generates a stable string category ID from a group name.
func categoryIDFromName(name string) string {
	h := fnv.New32a()
	h.Write([]byte(name))
	id := int(h.Sum32() & 0x7FFFFFFF)
	if id == 0 {
		id = 1
	}
	return fmt.Sprintf("%d", id)
}

// groupTitleOf returns a channel's category label, falling back to the source's
// tvg-group and then to All so uncategorized channels still land in a real category.
func groupTitleOf(attrs map[string]string) string {
	if group := attrs["group-title"]; group != "" {
		return group
	}
	if group := attrs["tvg-group"]; group != "" {
		return group
	}
	return "All"
}

// buildXCStreamURL constructs an XC direct-source URL for a content type, using the
// stream's real container extension rather than assuming MPEG-TS.
func buildXCStreamURL(baseURL, contentType, username, password string, streamID int, extension string) string {
	pathType := "live"
	suffix := "ts"
	switch contentType {
	case "vod":
		pathType = "movie"
		suffix = utils.NormalizeContainerExtension(extension)
	case "series":
		pathType = "series"
		suffix = utils.NormalizeContainerExtension(extension)
	}
	return fmt.Sprintf("%s/%s/%s/%s/%d.%s", baseURL, pathType, username, password, streamID, suffix)
}

// findChannelByStreamID resolves an XC stream ID to a channel name through an
// index rebuilt whenever the import generation moves, rather than walking the
// full channel map on every stream request.
func findChannelByStreamID(sp *proxy.StreamProxy, id int) string {
	gen := sp.ImportGeneration()

	m := streamIDIndex.Load()
	if m == nil || streamIDIndexGen.Load() != gen {
		m = rebuildStreamIDIndex(sp, gen)
	}
	return (*m)[id]
}

// rebuildStreamIDIndex snapshots stream ID to channel name for the supplied
// import generation.
func rebuildStreamIDIndex(sp *proxy.StreamProxy, gen uint64) *map[int]string {
	streamIDIndexMu.Lock()
	defer streamIDIndexMu.Unlock()

	if m := streamIDIndex.Load(); m != nil && streamIDIndexGen.Load() == gen {
		return m
	}

	index := make(map[int]string)
	sp.Channels.Range(func(name string, _ *types.Channel) bool {
		index[streamIDFromName(name)] = name
		return true
	})

	streamIDIndex.Store(&index)
	streamIDIndexGen.Store(gen)
	return &index
}

// getChannelContentType returns the content type for a channel.
// Caller must hold the channel read lock.
func getChannelContentType(ch *types.Channel) string {
	if len(ch.Streams) == 0 {
		return "live"
	}
	return string(utils.ContentTypeOfStream(ch.Streams[0]))
}

// buildStreamList iterates sorted channels and builds the XC stream list for a
// given content type. Channels are always ordered alphabetically by name.
func buildStreamList(sp *proxy.StreamProxy, contentType, baseURL, username, password string) []xcStream {
	var streams []xcStream
	num := 1

	// channel-name -> mapped epg_id; unmapped channels fall back to the dummy id
	epgMap := proxy.ChannelEPGMap()

	for _, item := range getSortedChannels(sp) {
		item.channel.Mu.RLock()

		if len(item.channel.Streams) == 0 {
			item.channel.Mu.RUnlock()
			continue
		}

		chType := getChannelContentType(item.channel)
		if chType != contentType {
			item.channel.Mu.RUnlock()
			continue
		}

		stream := item.channel.Streams[0]
		attrs := stream.Attributes
		extension := utils.NormalizeContainerExtension(stream.ContainerExtension)
		item.channel.Mu.RUnlock()

		streamID := streamIDFromName(item.name)
		group := groupTitleOf(attrs)
		logo := attrs["tvg-logo"]
		tvgID := proxy.EPGIDForChannel(item.name, epgMap)

		directURL := buildXCStreamURL(baseURL, contentType, username, password, streamID, extension)

		s := xcStream{
			Num:               num,
			Name:              item.name,
			StreamType:        contentType,
			StreamID:          streamID,
			SeriesID:          seriesIDForType(contentType, streamID),
			StreamIcon:        logo,
			EPGChannelID:      tvgID,
			Added:             "0",
			CategoryID:        categoryIDFromName(group),
			CustomSid:         "",
			TVArchive:         0,
			DirectSource:      directURL,
			TVArchiveDuration: 0,
		}
		if contentType == "vod" || contentType == "series" {
			s.ContainerExtension = extension
		}

		streams = append(streams, s)
		num++
	}

	if contentType == "series" {
		for _, e := range localscan.SeriesForExport() {
			seriesID := localscan.XCStreamID(e.Hash)

			name := e.Series
			if name == "" {
				name = e.Display
			}

			streams = append(streams, xcStream{
				Num:          num,
				Name:         name,
				StreamType:   "series",
				SeriesID:     seriesID,
				StreamIcon:   localArtURL(baseURL, username, password, e, "poster"),
				EPGChannelID: "",
				Added:        "0",
				CategoryID:   categoryIDFromName(localscan.SeriesCategory(e)),
				CustomSid:    "",
				DirectSource: "",
			})
			num++
		}
		return streams
	}

	for _, e := range localscan.EntriesForContentType(contentType) {
		extension := utils.NormalizeContainerExtension(localscan.ContainerExtension(e))
		streamID := localscan.XCStreamID(e.Hash)

		logo := ""
		if e.Poster != "" {
			logo = fmt.Sprintf("%s/localart/%s/%s/%s/poster", baseURL, username, password, e.Hash)
		}

		streams = append(streams, xcStream{
			Num:                num,
			Name:               e.Display,
			StreamType:         contentType,
			StreamID:           streamID,
			StreamIcon:         logo,
			EPGChannelID:       "",
			Added:              "0",
			CategoryID:         categoryIDFromName(e.GroupTitle),
			CustomSid:          "",
			TVArchive:          0,
			DirectSource:       buildXCStreamURL(baseURL, contentType, username, password, streamID, extension),
			TVArchiveDuration:  0,
			ContainerExtension: extension,
		})
		num++
	}

	return streams
}

// buildCategoryList iterates sorted channels and returns unique categories for a
// given content type. Category order follows first-seen in alphabetical channel order.
func buildCategoryList(sp *proxy.StreamProxy, contentType string) []xcCategory {
	seen := make(map[string]bool)
	var categories []xcCategory

	for _, item := range getSortedChannels(sp) {
		item.channel.Mu.RLock()

		if len(item.channel.Streams) == 0 {
			item.channel.Mu.RUnlock()
			continue
		}

		chType := getChannelContentType(item.channel)
		group := groupTitleOf(item.channel.Streams[0].Attributes)
		item.channel.Mu.RUnlock()

		if chType != contentType || seen[group] {
			continue
		}

		seen[group] = true
		categories = append(categories, xcCategory{
			CategoryID:   categoryIDFromName(group),
			CategoryName: group,
			ParentID:     0,
		})
	}

	if contentType == "series" {
		for _, e := range localscan.SeriesForExport() {
			group := localscan.SeriesCategory(e)
			if group == "" || seen[group] {
				continue
			}
			seen[group] = true
			categories = append(categories, xcCategory{
				CategoryID:   categoryIDFromName(group),
				CategoryName: group,
				ParentID:     0,
			})
		}
		return categories
	}

	for _, e := range localscan.EntriesForContentType(contentType) {
		if e.GroupTitle == "" || seen[e.GroupTitle] {
			continue
		}
		seen[e.GroupTitle] = true
		categories = append(categories, xcCategory{
			CategoryID:   categoryIDFromName(e.GroupTitle),
			CategoryName: e.GroupTitle,
			ParentID:     0,
		})
	}

	return categories
}
