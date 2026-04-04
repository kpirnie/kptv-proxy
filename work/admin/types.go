package admin

// StatsResponse represents comprehensive system statistics exposed through the admin API,
// providing operational metrics for monitoring, debugging, and capacity planning purposes.
type StatsResponse struct {
	TotalChannels       int     `json:"totalChannels"`
	ActiveStreams       int     `json:"activeStreams"`
	TotalSources        int     `json:"totalSources"`
	TotalEpgs           int     `json:"totalEpgs"`
	ConnectedClients    int     `json:"connectedClients"`
	Uptime              string  `json:"uptime"`
	MemoryUsage         string  `json:"memoryUsage"`
	CacheStatus         string  `json:"cacheStatus"`
	WorkerThreads       int     `json:"workerThreads"`
	TotalConnections    int     `json:"totalConnections"`
	BytesTransferred    string  `json:"bytesTransferred"`
	ActiveRestreamers   int     `json:"activeRestreamers"`
	StreamErrors        int     `json:"streamErrors"`
	ResponseTime        string  `json:"responseTime"`
	WatcherEnabled      bool    `json:"watcherEnabled"`
	UpstreamConnections int     `json:"upstreamConnections"`
	ActiveChannels      int     `json:"activeChannels"`
	AvgClientsPerStream float64 `json:"avgClientsPerStream"`
}

// ChannelResponse provides comprehensive channel information for admin interface display,
// including operational status, client statistics, and metadata for monitoring and management.
type ChannelResponse struct {
	Name             string `json:"name"`
	Active           bool   `json:"active"`
	Clients          int    `json:"clients"`
	BytesTransferred int64  `json:"bytesTransferred"`
	CurrentSource    string `json:"currentSource"`
	Group            string `json:"group"`
	Sources          int    `json:"sources"`
	URL              string `json:"url"`
	LogoURL          string `json:"logoURL"`
}

// StreamInfo provides detailed information about individual streams within a channel,
// including source metadata, ordering, and attributes for advanced channel management.
type StreamInfo struct {
	Index       int               `json:"index"`
	URL         string            `json:"url"`
	SourceName  string            `json:"sourceName"`
	SourceOrder int               `json:"sourceOrder"`
	Attributes  map[string]string `json:"attributes"`
}

// ChannelStreamsResponse provides comprehensive stream information for a specific channel,
// including current streaming state, preferred configuration, and detailed stream listings.
type ChannelStreamsResponse struct {
	ChannelName          string       `json:"channelName"`
	CurrentStreamIndex   int          `json:"currentStreamIndex"`
	PreferredStreamIndex int          `json:"preferredStreamIndex"`
	Obfuscated           bool         `json:"obfuscated"`
	Streams              []StreamInfo `json:"streams"`
}
