# KPTV Proxy - IPTV Stream Aggregator & Proxy

A high-performance Go-based IPTV proxy server that intelligently aggregates streams from multiple sources, provides automatic channel deduplication, failover capabilities, and serves them through a unified M3U8 playlist with advanced streaming options including FFmpeg integration.

## Key Features

### 🔄 **Multi-Source Aggregation**

- Combines multiple IPTV sources into a single unified playlist
- Intelligent channel grouping by name (deduplicates channels across sources)
- Automatic source prioritization and failover
- Per-source connection limits to prevent provider overload

### 📺 **Advanced Stream Management**  

- **Master Playlist Detection**: Automatically detects and processes HLS master playlists
- **Variant Selection**: Intelligently selects optimal stream variants (highest quality with fallback)
- **Channel Deduplication**: Groups identical channels from different sources
- **Smart Failover**: Seamlessly switches between sources when streams fail
- **Ad-Insertion Handling**: Automatically resolves tracking URLs and beacon redirects
- **Stream Validation**: Uses ffprobe to validate stream quality before serving

### 🚀 **Dual Streaming Architecture**

- **Go Restreaming Mode**: Single upstream connection shared among multiple clients
- **FFmpeg Proxy Mode**: Hardware-accelerated streaming with advanced codec support
- **Provider-Friendly**: Reduces load on upstream providers and prevents rate limiting
- **Automatic Management**: Intelligent connection pooling and cleanup of inactive streams  
- **Scalable**: Supports unlimited clients per channel with minimal resource overhead

### 🎯 **Enhanced HLS Support**

- **Tracking URL Resolution**: Automatically extracts real video URLs from ad-insertion systems
- **Beacon URL Handling**: Supports complex ad systems like AccuWeather's tracking URLs
- **Format Error Recovery**: Handles streams with format quirks (like BBC America)
- **Segment Validation**: Smart validation that skips problematic tracking URLs
- **Multi-Variant Testing**: Tests all available quality variants automatically

### ⚡ **Performance & Reliability**

- Worker pool-based parallel processing
- Ring buffer streaming with configurable sizes
- Built-in caching for playlists and metadata
- Rate limiting and connection management
- Comprehensive retry logic with exponential backoff
- Stream health monitoring and automatic blocking of failed sources

### 🔧 **Advanced Configuration**

- **JSON-based configuration** with per-source customization
- **Per-source settings** for headers, timeouts, retries, and connection limits
- **Flexible source configuration** with custom User-Agent, Origin, and Referrer headers
- **Customizable stream sorting** by any M3U8 attribute
- **URL obfuscation** for privacy and security
- **Configurable timeouts and buffer sizes**
- **Debug mode** with extensive logging
- **FFmpeg integration** with custom pre-input and pre-output arguments

### 📊 **Monitoring & Metrics**

- Prometheus metrics integration
- Connection tracking per channel and source
- Stream error monitoring and categorization
- Health check endpoints
- Detailed logging with configurable verbosity

### 🌐 **Web Admin Interface**

- **Dark Mobile-Friendly Design**: Responsive web interface optimized for all devices
- **Custom CSS Support**: Load custom styles from `/settings/custom.css` for personalized theming
- **Real-Time Dashboard**: Live statistics, active channels, and system monitoring
- **Configuration Management**: Edit global settings and per-source configurations through web UI
- **Source Management**: Add, edit, delete, and reorder IPTV sources with full validation
- **Channel Monitoring**: View all channels with real-time status and client information
- **Live Logs**: Real-time log viewing with filtering by level (error, warning, info, debug)
- **Graceful Restart**: Apply configuration changes with zero-downtime restarts
- **Auto-Refresh**: Dashboard updates every 5 seconds for real-time monitoring

### 🔒 **Dead Stream Management**

- **Stream Health Tracking**: Mark problematic streams as "dead" to prevent automatic selection
- **Manual Stream Control**: Activate specific streams or mark them as unplayable through the admin interface
- **Stream Revival**: Restore previously dead streams when they become functional again
- **Persistent Dead Stream Storage**: Dead stream information stored in `/settings/dead-streams.json`
- **Intelligent Stream Selection**: Proxy automatically skips dead streams during failover
- **Visual Dead Stream Indicators**: Clear visual markers for dead streams in the admin interface

### 🔍 **Automatic Stream Monitoring**

- **Real-Time Health Monitoring**: Continuously monitors active streams for playback issues
- **Intelligent Failover**: Automatically switches to backup streams when problems are detected
- **State-Based Detection**: Monitors buffer health, activity timestamps, and connection status
- **No Additional Network Load**: Uses existing connection state without extra requests
- **Provider-Friendly**: Respects existing connection limits and reuses established connections
- **Configurable Timing**: Adjustable monitoring intervals (default: 30 seconds, 5 consecutive failures)
- **Seamless Switching**: Automatic failover maintains client connections during stream transitions
- **Integration with Existing Logic**: Leverages all existing stream management and failover mechanisms

### 🎬 **FFmpeg Integration**

- **Hardware Acceleration**: Support for GPU-accelerated encoding/decoding
- **Advanced Codec Support**: Handle complex video formats and containers
- **Custom Arguments**: Configurable pre-input and pre-output FFmpeg arguments
- **Automatic Detection**: Intelligent stream format detection and processing
- **Resource Optimization**: Efficient memory usage and CPU optimization
- **Format Conversion**: Real-time transcoding and format adaptation

## Architecture Overview

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│   IPTV Source 1 │    │   IPTV Source 2  │    │   IPTV Source N │
│   (5 max conns) │    │  (10 max conns)  │    │  (3 max conns)  │
│ Custom Headers  │    │ Custom Headers   │    │ Custom Headers  │
└─────────┬───────┘    └─────────┬────────┘    └─────────┬───────┘
          │                      │                       │
          └──────────────────────┼───────────────────────┘
                                 │
                    ┌────────────▼────────────┐
                    │     KPTV Proxy          │
                    │  • Channel grouping     │
                    │  • Master playlist      │
                    │    detection            │
                    │  • Tracking URL         │
                    │    resolution           │
                    │  • Per-source config    │
                    │  • Failover logic       │
                    │  • Connection mgmt      │
                    │  • Web Admin Interface  │
                    │  • Dead Stream Mgmt     │
                    │  • Stream Watcher       │
                    │  • FFmpeg Integration   │
                    └────────────┬────────────┘
                                 │
                    ┌────────────▼────────────┐
                    │   Unified M3U8          │
                    │   /playlist.m3u8        │
                    └─────────────────────────┘
                                 │
          ┌──────────────────────┼──────────────────────┐
          │                      │                      │
┌─────────▼───────┐    ┌─────────▼───────┐    ┌─────────▼───────┐
│    Client 1     │    │    Client 2     │    │    Client N     │
│  (VLC, Kodi,    │    │   (Smart TV,    │    │   (Mobile App,  │
│   etc.)         │    │    etc.)        │    │    etc.)        │
└─────────────────┘    └─────────────────┘    └─────────────────┘
```

## Third-Party Libraries

KPTV Proxy leverages several high-quality open-source libraries:

### Core Dependencies

- **[gorilla/mux](https://github.com/gorilla/mux)** (v1.8.1) - HTTP router and URL matcher for building REST APIs
- **[panjf2000/ants](https://github.com/panjf2000/ants)** (v2.11.3) - High-performance goroutine pool for worker thread management
- **[uber-go/ratelimit](https://github.com/uber-go/ratelimit)** (v0.3.1) - Per-source rate limiting to prevent provider overload

### Streaming & Parsing

- **[grafov/m3u8](https://github.com/grafov/m3u8)** (v0.12.1) - M3U8 playlist parser for HLS stream handling
- **[grafana/regexp](https://github.com/grafana/regexp)** (v0.0.0-20240518133315) - High-performance regular expression engine for content filtering

### Monitoring

- **[prometheus/client_golang](https://github.com/prometheus/client_golang)** (v1.23.0) - Metrics collection and exposition for Prometheus integration
- **[prometheus/client_model](https://github.com/prometheus/client_model)** (v0.6.2) - Data model for Prometheus metrics
- **[prometheus/common](https://github.com/prometheus/common)** (v0.65.0) - Common libraries for Prometheus components
- **[prometheus/procfs](https://github.com/prometheus/procfs)** (v0.16.1) - Process filesystem parsing for system metrics

### External Tools

- **[FFmpeg/FFprobe](https://ffmpeg.org)** - Stream validation, transcoding, and format analysis (LGPL v2.1)
  - Used as external binaries for stream processing
  - Source code available at: <https://ffmpeg.org/download.html>
  - See LICENSE file for complete legal information

### Web Interface

- **[UIKit 3](https://getuikit.com)** (v3.17.11) - Frontend framework for admin interface (MIT License)
- **[Sortable.js](https://sortablejs.github.io/Sortable/)** (v1.15.0) - Drag-and-drop functionality for stream ordering

All dependencies are vendored and their licenses are compatible with the MIT License used by KPTV Proxy.

## Quick Start

**Prerequisites**: Docker or Podman installed

1. **Create settings directory and get configuration:**

```bash
mkdir settings
wget https://raw.githubusercontent.com/your-repo/kptv-proxy/main/docker-compose.example.yaml -O docker-compose.yaml
```

1. **Create your configuration file** - Create `settings/config.json`:

```json
{
  "baseURL": "http://your-server-ip:9500",
  "bufferSizePerStream": 16,
  "cacheEnabled": true,
  "cacheDuration": "30m",
  "importRefreshInterval": "12h",
  "workerThreads": 4,
  "debug": false,
  "obfuscateUrls": true,
  "sortField": "tvg-name",
  "sortDirection": "asc",
  "streamTimeout": "10s",
  "maxConnectionsToApp": 100,
  "watcherEnabled": true,
  "ffmpegMode": false,
  "ffmpegPreInput": [],
  "ffmpegPreOutput": ["-c", "copy"],
  "sources": [
    {
      "name": "Primary IPTV Source",
      "url": "http://provider1.com/playlist.m3u8",
      "order": 1,
      "maxConnections": 5,
      "maxStreamTimeout": "30s",
      "retryDelay": "5s",
      "maxRetries": 3,
      "maxFailuresBeforeBlock": 5,
      "minDataSize": 2,
      "userAgent": "VLC/3.0.18 LibVLC/3.0.18",
      "reqOrigin": "",
      "reqReferrer": ""
    }
  ]
}
```

1. **Start the proxy:**

```bash
# Docker
docker compose up -d

# Or Podman  
podman-compose up -d
```

1. **Access your services:**

```
Unified Playlist: http://your-server-ip:9500/playlist
Group Filtered Playlist: http://your-server-ip:9500/playlist/{group}
Admin Interface:  http://your-server-ip:9500/admin
Stream Management: Use admin interface to activate/kill streams per channel
```

**That's it!** Your IPTV sources are now unified into a single playlist with automatic failover, per-source configuration, intelligent stream monitoring, FFmpeg integration, and a powerful web-based admin interface.

## Web Admin Interface

Access the admin interface at `http://your-server:port/admin` for comprehensive management:

### Custom Styling

You can customize the admin interface appearance by creating a custom CSS file at `/settings/custom.css`. This file will be automatically loaded by the admin interface.

**Base Framework**: The admin interface uses UIKit 3. For comprehensive styling documentation, visit: <https://getuikit.com/docs/introduction>

**Available Custom CSS Classes**:

**Layout & Cards**:

- `.stat-card` - Dashboard statistics cards
- `.success-card` - Green success styling
- `.warning-card` - Orange warning styling  
- `.danger-card` - Red danger styling
- `.loading-overlay` - Full-screen loading indicator

**Channel & Source Management**:

- `.channel-item` - Individual channel list items
- `.channel-inactive` - Styling for inactive channels
- `.source-item` - Individual source configuration items
- `.source-actions` - Action buttons for source management

**Status Indicators**:

- `.status-indicator` - Base status dot styling
- `.status-active` - Green active status
- `.status-warning` - Orange warning status
- `.status-error` - Red error status
- `.connection-dot` - Animated connection indicators

**Stream Management**:

- `.dead-stream` - Styling for dead/blocked streams
- `.stream-selector-current` - Currently active stream
- `.stream-selector-preferred` - Preferred stream indicator
- `.watcher-status` - Stream watcher status indicator

**Logging & Debug**:

- `.log-entry` - Base log entry styling
- `.log-error` - Error level logs
- `.log-warning` - Warning level logs
- `.log-info` - Info level logs  
- `.log-debug` - Debug level logs
- `.log-timestamp` - Log timestamp styling

**UI Elements**:

- `.metric-row` - Statistics display rows
- `.channel-details` - Channel metadata display
- `.channel-name` - Channel name styling
- `.channel-url` - URL display formatting

**Utilities**:

- `.text-truncate` - Text overflow handling
- `.text-monospace` - Monospace font styling
- `.fade-in` - Fade-in animation
- `.loading` - Loading state styling

### Dashboard

- **Real-Time Statistics**: Total channels, active streams, connected clients, memory usage
- **System Status**: Server uptime, cache status, worker thread count, FFmpeg mode
- **Traffic Metrics**: Connection counts, bytes transferred, stream errors
- **Active Channels**: Live view of currently streaming channels with client counts
- **Auto-Refresh**: Updates every 5 seconds for real-time monitoring

### Global Settings

- Edit all configuration parameters through intuitive web forms
- FFmpeg mode toggle and argument configuration
- Validation and error handling for all settings
- Save changes and trigger graceful restart to apply new configuration
- Support for duration formats (30m, 1h, etc.) and all data types

### Source Management

- **Add/Edit Sources**: Full configuration interface for IPTV sources
- **Per-Source Settings**: Custom timeouts, retry logic, connection limits
- **Custom Headers**: Configure User-Agent, Origin, Referrer per source
- **Priority Management**: Reorder sources by priority for failover
- **Real-Time Status**: Live indicators showing source health

### Channel Monitoring

- **All Channels View**: Complete list of available channels with status
- **Stream Selection**: Choose specific streams for each channel with activate/kill controls
- **Dead Stream Management**: Mark streams as dead or revive them with visual indicators
- **Real-Time Status**: Active/inactive indicators with client counts
- **Search & Filter**: Find channels by name or group
- **Group Organization**: Channels organized by group/category
- **Auto-Refresh**: Live updates of channel status

### Live Logs

- **Real-Time Viewing**: Live log stream with auto-scrolling
- **Level Filtering**: Filter by error, warning, info, debug levels
- **Search Functionality**: Find specific log entries
- **Clear Logs**: Remove old entries to maintain performance

### Mobile-Friendly Design

- **Responsive Layout**: Optimized for phones, tablets, and desktop
- **Dark Theme**: Professional dark interface optimized for IPTV environments
- **Touch Controls**: Finger-friendly buttons and controls
- **Collapsible Navigation**: Adaptive interface for small screens

## FFmpeg Integration

KPTV Proxy supports two streaming modes:

### Go Restreaming Mode (Default)

- Pure Go implementation
- Efficient memory usage
- Fast startup times
- Basic stream processing

### FFmpeg Mode

- Hardware acceleration support
- Advanced codec handling
- GPU utilization for encoding/decoding
- Comprehensive format support

**Configuration:**

```json
{
  "ffmpegMode": true,
  "ffmpegPreInput": ["-re", "-rtsp_transport", "tcp"],
  "ffmpegPreOutput": ["-c", "copy", "-f", "mpegts"]
}
```

**Common FFmpeg Arguments:**

*Pre-Input Arguments*:

- `"-re"` - Read input at native frame rate
- `"-rtsp_transport", "tcp"` - Use TCP for RTSP
- `"-fflags", "nobuffer"` - Disable input buffering
- `"-thread_queue_size", "1024"` - Set thread queue size

*Pre-Output Arguments*:

- `"-c", "copy"` - Copy streams without re-encoding
- `"-c:v", "libx264"` - H.264 video encoding
- `"-c:a", "aac"` - AAC audio encoding
- `"-f", "mpegts"` - MPEG-TS output format
- `"-movflags", "frag_keyframe+empty_moov"` - Fragmented MP4

## Dead Stream Management

The KPTV Proxy includes a sophisticated dead stream management system that allows you to mark problematic streams as "dead" and manage them through the web interface.

### Features

- **Manual Stream Control**: Use the admin interface to activate specific streams or mark them as dead
- **Persistent Storage**: Dead stream information is stored in `/settings/dead-streams.json`
- **Automatic Skipping**: The proxy automatically skips dead streams during failover
- **Stream Revival**: Dead streams can be restored when they become functional again

### How It Works

1. **Marking Streams as Dead**: In the admin interface, navigate to any channel and click the stream selection button. Each stream shows:
   - **Activate Button** (▶️): Switch to this specific stream
   - **Kill Button** (🚫): Mark this stream as dead

2. **Dead Stream Storage**: When a stream is marked as dead, its information is stored in `/settings/dead-streams.json`

3. **Automatic Stream Selection**: During playback, the proxy will:
   - Skip dead streams during automatic failover
   - Try the next available live stream
   - Continue to show dead streams in the admin interface for potential revival

4. **Stream Revival**: Dead streams can be restored by:
   - Navigating to the channel in the admin interface
   - Clicking the **Live Button** (🔄) next to the dead stream
   - The stream is removed from the dead streams list and becomes available again

## Stream Watcher - Automatic Monitoring

The KPTV Proxy includes an intelligent stream monitoring system that automatically detects playback issues and switches to backup streams without interrupting client connections.

### How It Works

The Stream Watcher runs as a background service that monitors active streams every 15 seconds (debug mode) or 30 seconds (normal mode) without making additional network requests.

**Monitored Conditions:**

- **Buffer Health**: Detects destroyed or stale buffers
- **Stream Activity**: Monitors data flow and timestamps  
- **Context Status**: Detects cancelled or problematic connections
- **Client Connections**: Tracks active client count
- **FFprobe Analysis**: Deep content validation (when enabled)

**Automatic Actions:**

- **Health Assessment**: Evaluates stream conditions using existing state
- **Consecutive Failure Tracking**: Requires 5 consecutive failures before switching
- **Intelligent Failover**: Automatically switches to next available healthy stream
- **Seamless Transition**: Maintains client connections during stream changes
- **Resource Respect**: Uses existing connection limits and stream management

## API Endpoints

| Endpoint | Description |
|----------|-------------|
| `GET /pl` | Unified playlist (all channels) |
| `GET /playlist` | Unified playlist (all channels) |
| `GET /pl/{group}` | Group-filtered playlist |
| `GET /playlist/{group}` | Group-filtered playlist |
| `GET /s/{channel}` | Stream proxy with automatic failover |
| `GET /epg` | EPG |
| `GET /epg.xml` | EPG |
| `GET /metrics` | Prometheus metrics |
| `GET /admin` | Web admin interface |
| `GET /api/config` | Get current configuration |
| `POST /api/config` | Update configuration |
| `GET /api/stats` | System statistics |
| `GET /api/channels` | All channels |
| `GET /api/channels/active` | Active channels only |
| `GET /api/channels/{channel}/streams` | Get available streams for channel |
| `POST /api/channels/{channel}/stream` | Set active stream for channel |
| `POST /api/channels/{channel}/kill-stream` | Mark stream as dead |
| `POST /api/channels/{channel}/revive-stream` | Revive dead stream |
| `GET /api/logs` | Application logs |
| `DELETE /api/logs` | Clear logs |
| `POST /api/restart` | Graceful restart |
| `POST /api/watcher/toggle` | Enable/disable stream watcher |

## Configuration Reference

### Global Settings

All configuration is done via a JSON file mounted at `/settings/config.json` or through the web admin interface.

| Setting | Default | Description |
|---------|---------|-------------|
| `baseURL` | `"http://localhost:8080"` | Base URL for generated stream links |
| `bufferSizePerStream` | `16` | Per-stream buffer size in MB |
| `cacheEnabled` | `true` | Enable playlist caching |
| `cacheDuration` | `"30m"` | Cache lifetime (e.g., "30m", "1h") |
| `importRefreshInterval` | `"12h"` | How often to refresh source playlists |
| `workerThreads` | `4` | Parallel workers for import processing |
| `debug` | `false` | Enable verbose logging |
| `obfuscateUrls` | `true` | Hide source URLs in logs for privacy |
| `sortField` | `"tvg-name"` | Sort streams by: `tvg-name`, `tvg-id`, `group-title`, etc. |
| `sortDirection` | `"asc"` | Sort direction: `asc` or `desc` |
| `streamTimeout` | `"10s"` | Global timeout for stream validation |
| `maxConnectionsToApp` | `100` | Maximum total connections to the application |
| `watcherEnabled` | `true` | Enable automatic stream monitoring |
| `ffmpegMode` | `false` | Use FFmpeg instead of Go streaming |
| `ffmpegPreInput` | `[]` | FFmpeg arguments before `-i` |
| `ffmpegPreOutput` | `[]` | FFmpeg arguments before output |

### Per-Source Settings

Each source in the `sources` array supports these individual settings:

| Setting | Required | Description | Example |
|---------|----------|-------------|---------|
| `name` | Yes | Friendly name for the source | `"Primary IPTV"` |
| `url` | Yes | M3U8 playlist URL | `"http://provider.com/list.m3u8"` |
| `order` | No | Priority order (lower = higher priority) | `1` |
| `maxConnections` | No | Max concurrent connections to this source | `5` |
| `maxStreamTimeout` | No | Timeout for streams from this source | `"30s"` |
| `retryDelay` | No | Delay between retry attempts | `"5s"` |
| `maxRetries` | No | Retry attempts per stream failure | `3` |
| `maxFailuresBeforeBlock` | No | Failures before blocking a stream | `5` |
| `minDataSize` | No | Minimum data size in KB to consider success | `2` |
| `userAgent` | No | Custom User-Agent header | `"VLC/3.0.18 LibVLC/3.0.18"` |
| `reqOrigin` | No | Custom Origin header | `"https://provider.com"` |
| `reqReferrer` | No | Custom Referrer header | `"https://provider.com/player"` |
| `username` | No | XC API username | `"user123"` |
| `password` | No | XC API password | `"pass456"` |

### Example Docker Compose

```yaml
services:
  kptv-proxy:
    image: ghcr.io/kpirnie/kptv-proxy:latest
    container_name: kptv_proxy
    restart: unless-stopped
    ports:
      - 9500:8080
    volumes:
      - ./settings:/settings  # Mount configuration directory
    # if utilizing hardware accelleration for ffmpeg
    #devices:
      #- /dev/dri:/dev/dri
    
    # Health check
    healthcheck:
      test: ["CMD", "wget", "--quiet", "--tries=1", "--spider", "http://localhost:8080/playlist"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 10s
```

## Advanced Configuration Examples

### FFmpeg Hardware Acceleration

```json
{
  "ffmpegMode": true,
  "ffmpegPreInput": [
    "-hwaccel", "vaapi",
    "-hwaccel_device", "/dev/dri/renderD128",
    "-re"
  ],
  "ffmpegPreOutput": [
    "-c:v", "h264_vaapi",
    "-c:a", "aac",
    "-f", "mpegts"
  ]
}
```

### High-Performance Multi-Provider Setup

```json
{
  "baseURL": "http://your-server:9500",
  "bufferSizePerStream": 32,
  "workerThreads": 20,
  "maxConnectionsToApp": 500,
  "ffmpegMode": true,
  "ffmpegPreOutput": ["-c", "copy", "-f", "mpegts"],
  "sources": [
    {
      "name": "Premium Provider",
      "url": "http://premium.iptv.com/playlist.m3u8",
      "order": 1,
      "maxConnections": 3,
      "maxStreamTimeout": "20s",
      "retryDelay": "3s",
      "maxRetries": 2,
      "maxFailuresBeforeBlock": 3,
      "minDataSize": 5,
      "userAgent": "PREMIUM_CLIENT/1.0"
    }
  ]
}
```

### Xtreme Codes API Configuration

```json
{
  "sources": [
    {
      "name": "XC Provider",
      "url": "http://panel.provider.com",
      "username": "your_username",
      "password": "your_password",
      "order": 1,
      "maxConnections": 5
    }
  ]
}
```

### Custom CSS Example

Create `/settings/custom.css`:

```css
/* Custom dark purple theme */
.stat-card {
    background: linear-gradient(135deg, #6a1b9a, #4a148c) !important;
}

.channel-item {
    border-left-color: #9c27b0 !important;
    background: #1a1a2e !important;
}

.status-active {
    background: #00e676 !important;
    box-shadow: 0 0 10px rgba(0, 230, 118, 0.6) !important;
}

/* Custom brand colors */
:root {
    --uk-primary: #9c27b0 !important;
    --uk-success: #00e676 !important;
}
```

## Monitoring & Troubleshooting

### Health Monitoring

```bash
# Check container health
docker-compose ps

# View real-time logs  
docker-compose logs -f kptv-proxy

# Check FFmpeg mode status
docker-compose logs kptv-proxy | grep FFMPEG

# Monitor stream watcher activity
docker-compose logs kptv-proxy | grep WATCHER
```

### Key Metrics (Prometheus)

- `iptv_proxy_active_connections` - Active connections per channel
- `iptv_proxy_bytes_transferred` - Data transfer metrics
- `iptv_proxy_stream_errors` - Error counts by type
- `iptv_proxy_clients_connected` - Connected clients per channel
- `iptv_proxy_stream_switches_total` - Stream switch events

### Common Issues & Solutions

**Problem**: Configuration not loading

- ✅ Use the web admin interface to edit configuration
- ✅ Check JSON syntax: `cat settings/config.json | jq .`
- ✅ Verify file permissions: `ls -la settings/`
- ✅ Check logs in admin interface or container logs

**Problem**: FFmpeg not working

- ✅ Verify FFmpeg is installed in container: `docker/podman exec -it kptv_proxy ffmpeg -version`
- ✅ Check FFmpeg arguments in debug logs
- ✅ Test with simple arguments first: `["-c", "copy"]`
- ✅ Verify hardware acceleration support if using GPU

**Problem**: Streams failing to play  

- ✅ Monitor channel status in admin interface
- ✅ Toggle between Go and FFmpeg modes in global settings
- ✅ Check per-source retry settings in source management
- ✅ Use dead stream management to mark problematic streams

**Problem**: High CPU usage

- ✅ Disable FFmpeg mode if hardware acceleration unavailable
- ✅ Use `-c copy` instead of transcoding in FFmpeg arguments
- ✅ Reduce `maxConnections` per source in admin interface
- ✅ Monitor active connections in dashboard

## Client Configuration Examples

### VLC Media Player

```
Network → Open Network Stream → http://your-server:9500/playlist
```

### Kodi/LibreELEC

```
Add-ons → PVR IPTV Simple Client
M3U Play List URL: http://your-server:9500/playlist
```

### Android/iOS IPTV Apps

```
Playlist URL: http://your-server:9500/playlist
Format: M3U8/HLS
```

## Security Considerations

- **Network Security**: Run behind reverse proxy (nginx/Cloudflare) for production
- **Admin Interface Security**: Consider adding authentication for admin interface in production
- **Access Control**: Restrict admin interface access to trusted networks
- **Source Privacy**: Enable `obfuscateUrls` to hide provider URLs in logs
- **Container Security**: Runs as non-root user (UID 1000)
- **Custom CSS**: Validate custom CSS to prevent XSS attacks
- **File Permissions**: Protect configuration files with appropriate permissions

## Performance Optimization

### For High-Concurrency (100+ clients)

```json
{
  "workerThreads": 20,
  "maxConnectionsToApp": 500,
  "ffmpegMode": true,
  "ffmpegPreOutput": ["-c", "copy", "-f", "mpegts"]
}
```

### For Low-Resource Systems

```json
{
  "workerThreads": 2,
  "bufferSizePerStream": 4,
  "maxConnectionsToApp": 50,
  "ffmpegMode": false
}
```

## Supporting KPTV Proxy

KPTV Proxy will always remain free and open-source for everyone to use and modify. However, if this project has enhanced your IPTV experience or saved you time, consider supporting its continued development.

Donations help fund server costs, testing equipment, and most importantly, the time needed to add new features, maintain compatibility, and provide community support: <https://www.paypal.com/paypalme/kevinpirnie>

## Contributing

1. Fork the repository
2. Create a feature branch: `git checkout -b feature/amazing-feature`
3. Commit changes: `git commit -m 'Add amazing feature'`
4. Push to branch: `git push origin feature/amazing-feature`
5. Open a Pull Request

---

## Third-Party Software

### FFmpeg/FFprobe

This software uses code of [FFmpeg](http://ffmpeg.org) licensed under the [LGPLv2.1](http://www.gnu.org/licenses/old-licenses/lgpl-2.1.html).

**FFmpeg License Information:**

- KPTV Proxy uses FFmpeg and FFprobe for stream processing and validation
- FFmpeg is used as an external binary and for stream proxying
- FFmpeg source code can be downloaded from: [https://ffmpeg.org/download.html](https://ffmpeg.org/download.html)
- FFmpeg is licensed under LGPL v2.1 or later

**Patent Considerations:**
FFmpeg may use patented algorithms for various multimedia codecs. Patent laws vary by jurisdiction. For commercial use, consult legal counsel regarding potential patent licensing requirements in your jurisdiction.

## License

MIT License - see [LICENSE](LICENSE) file for details.

### Third-Party Licenses

This project incorporates or uses the following third-party software:

- **FFmpeg/FFprobe**: Licensed under LGPL v2.1 or later - [https://ffmpeg.org/legal.html](https://ffmpeg.org/legal.html)
- **UIKit 3**: MIT License - [https://getuikit.com](https://getuikit.com)

---

**Need Help?** Use the web admin interface at `http://your-server:port/admin` for easy configuration management, customize the appearance with `/settings/custom.css`, or enable debug mode and check the logs for detailed information about stream processing, connection attempts, and error details. The automatic Stream Watcher will help maintain stream reliability in the background, and FFmpeg integration provides advanced streaming capabilities for complex media formats.
