[![Build Main](https://github.com/kpirnie/kptv-proxy/actions/workflows/docker-build.yml/badge.svg?branch=main)](https://github.com/kpirnie/kptv-proxy/actions?query=workflow%3A%22Build+and+Push+Docker+Image%22+branch%3Amain)
[![Build Develop](https://github.com/kpirnie/kptv-proxy/actions/workflows/docker-build.yml/badge.svg?branch=develop)](https://github.com/kpirnie/kptv-proxy/actions?query=workflow%3A%22Build+and+Push+Docker+Image%22+branch%3Adevelop)
[![Go Version](https://img.shields.io/badge/Go-1.26.1-00ADD8?logo=go&logoColor=white)](https://golang.org/)
[![Debian](https://img.shields.io/badge/Base-Debian%20Trixie%20Slim-A81D33?logo=debian&logoColor=white)](https://www.debian.org/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![www](https://img.shields.io/badge/www-kevinpirnie.com-blue?logo=google-chrome&logoColor=white)](https://kevinpirnie.com)
[![Discord](https://img.shields.io/badge/Discord-Join-5865F2?logo=discord&logoColor=white)](https://discord.gg/bd4Qan3PaN)

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

- **Hardware Acceleration**: Support for GPU-accelerated encoding/decoding via host device passthrough
- **Advanced Codec Support**: Handle complex video formats and containers
- **Custom Arguments**: Configurable pre-input and pre-output FFmpeg arguments
- **Automatic Detection**: Intelligent stream format detection and processing
- **Resource Optimization**: Efficient memory usage and CPU optimization
- **Format Conversion**: Real-time transcoding and format adaptation
- **Ad-Break Handling**: Properly manages MPEG-TS discontinuities from ad-insertion systems

### 📡 **Xtream Codes Output**

- **XC-Compatible API**: Expose your aggregated streams via a full Xtream Codes compatible API
- **Multi-Account Support**: Create multiple XC output accounts with independent credentials
- **Per-Account Content Control**: Enable or disable Live, VOD, and Series per account
- **Connection Limits**: Configurable maximum connections per account
- **M3U Playlist Export**: `/get.php` endpoint for M3U playlist generation
- **XMLTV EPG**: Full EPG passthrough via `/xmltv.php`
- **Quick Copy**: Copy base URL, username, and password directly from the admin interface

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
                    │  • XC Output API        │
                    └────────────┬────────────┘
                                 │
                    ┌────────────▼────────────┐
                    │   Unified M3U8 + XC     │
                    │   /pl  /player_api.php  │
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

## Quick Start

**Prerequisites**: Docker or Podman installed

1. **Create settings directory:**

```bash
mkdir settings
```

2. **Create your configuration file** at `settings/config.json`:

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
  "responseHeaderTimeout": "10s",
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

3. **Start the proxy:**

```bash
# Docker
docker compose up -d

# Or Podman
podman-compose up -d
```

4. **Access your services:**

```
Unified Playlist:        http://your-server-ip:9500/pl
Group Filtered Playlist: http://your-server-ip:9500/pl/{group}
Admin Interface:         http://your-server-ip:9500/
XC API:                  http://your-server-ip:9500/player_api.php
```

## Docker Compose Examples

### Standard Setup (default)

```yaml
services:
  kptv-proxy:
    image: ghcr.io/kpirnie/kptv-proxy:latest
    container_name: kptv_proxy
    restart: unless-stopped
    ports:
      - WHATEVER_PORT_YOU_WANT_TO_USE:8080
    volumes:
      - ./settings/:/settings/
      # To utilize FFmpeg, install it on your host and mount the binaries:
      #- /usr/local/bin/ffmpeg:/usr/local/bin/ffmpeg:ro
      #- /usr/local/bin/ffprobe:/usr/local/bin/ffprobe:ro
    healthcheck:
      test: [ "CMD", "curl", "-v", "http://127.0.0.1:8080/api/stats" ]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 30s
```

### Hardware Accelerated FFmpeg Setup

For hardware-accelerated transcoding, FFmpeg reads and decodes using your GPU via VAAPI. This requires:

- FFmpeg and FFprobe installed on the **host machine**
- The host's DRI render node passed through to the container (`/dev/dri`)
- The host's VA-API driver libraries mounted into the container
- The container user added to the `video` and `render` groups

```yaml
services:
  kptv-proxy:
    image: ghcr.io/kpirnie/kptv-proxy:latest
    container_name: kptv_proxy
    restart: unless-stopped
    ports:
      - WHATEVER_PORT_YOU_WANT_TO_USE:8080
    volumes:
      - ./settings/:/settings/
      # FFmpeg and FFprobe binaries from host
      - /usr/local/bin/ffmpeg:/usr/local/bin/ffmpeg:ro
      - /usr/local/bin/ffprobe:/usr/local/bin/ffprobe:ro
      # VA-API driver libraries from host
      # Debian/Ubuntu: /usr/lib/x86_64-linux-gnu/dri
      # RHEL/Fedora:   /usr/lib64/dri
      - /usr/lib/x86_64-linux-gnu/dri:/usr/lib/x86_64-linux-gnu/dri:ro
    devices:
      - /dev/dri:/dev/dri
    group_add:
      - video
      - render
    healthcheck:
      test: [ "CMD", "curl", "-v", "http://127.0.0.1:8080/api/stats" ]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 30s
```

> **Note**: The render node is typically `/dev/dri/renderD128`. If you have multiple GPUs it may be `renderD129` or higher. Verify with `ls /dev/dri/` on the host.

The corresponding `config.json` FFmpeg section for hardware acceleration:

```json
{
  "ffmpegMode": true,
  "ffmpegPreInput": [
    "-hide_banner",
    "-loglevel", "panic",
    "-hwaccel", "vaapi",
    "-hwaccel_output_format", "vaapi",
    "-hwaccel_device", "/dev/dri/renderD128",
    "-fflags", "+genpts+discardcorrupt+igndts",
    "-analyzeduration", "250000",
    "-probesize", "4194304",
    "-max_delay", "250000",
    "-copytb", "1"
  ],
  "ffmpegPreOutput": [
    "-f", "mpegts",
    "-c", "copy",
    "-copyts",
    "-max_muxing_queue_size", "2048",
    "-flush_packets", "0",
    "-mpegts_flags", "initial_discontinuity",
    "-mpegts_copyts", "1"
  ]
}
```

## Web Admin Interface

Access the admin interface at `http://your-server:port/` for comprehensive management.

### Custom Styling

Create `/settings/custom.css` to customize the admin interface appearance. This file is automatically loaded.

**Base Framework**: TailWindCSS. See: <https://tailwindcss.com/docs/styling-with-utility-classes>

### Dashboard

- **Real-Time Statistics**: Total channels, active streams, connected clients, memory usage
- **System Status**: Server uptime, cache status, worker thread count, FFmpeg mode
- **Traffic Metrics**: Connection counts, bytes transferred, stream errors
- **Active Channels**: Live view of currently streaming channels with client counts, codec info, resolution, and bitrate badges
- **Auto-Refresh**: Updates every 5 seconds

### Global Settings

- Edit all configuration parameters through intuitive web forms
- FFmpeg mode toggle and argument configuration
- Validation and error handling for all settings
- Save changes and trigger graceful restart to apply new configuration

### Xtream Codes Output Accounts

Create XC-compatible output accounts to expose your aggregated streams to any Xtream Codes compatible player (Tivimate, IPTV Smarters, etc.).

**Per-account configuration:**

| Setting | Description |
|---------|-------------|
| Name | Friendly name for the account |
| Username | XC login username |
| Password | XC login password (auto-generate available) |
| Max Connections | Maximum simultaneous streams for this account |
| Enable Live | Include live TV streams |
| Enable Series | Include series content |
| Enable VOD | Include video on demand |

**Quick copy buttons** on each account card allow you to instantly copy:
- Base URL (for configuring players)
- Username
- Password

**XC API Endpoints:**

| Endpoint | Description |
|----------|-------------|
| `GET /player_api.php` | Main XC API endpoint |
| `GET /get.php` | M3U playlist export |
| `GET /xmltv.php` | EPG data |
| `GET /live/{user}/{pass}/{id}` | Live stream |
| `GET /movie/{user}/{pass}/{id}` | VOD stream |
| `GET /series/{user}/{pass}/{id}` | Series stream |

### Source Management

- **Add/Edit Sources**: Full configuration interface for IPTV sources
- **Per-Source Settings**: Custom timeouts, retry logic, connection limits
- **Custom Headers**: Configure User-Agent, Origin, Referrer per source
- **Content Filtering**: Per-source regex filters for Live, VOD, and Series content
- **Priority Management**: Reorder sources by priority for failover
- **XC API Sources**: Set username and password for Xtream Codes API sources

### Channel Monitoring

- **All Channels View**: Complete list of available channels with status
- **Stream Selection**: Choose specific streams for each channel with activate/kill controls
- **Dead Stream Management**: Mark streams as dead or revive them with visual indicators
- **Real-Time Status**: Active/inactive indicators with client counts
- **Search & Filter**: Find channels by name or group
- **Auto-Refresh**: Live updates of channel status

### Live Logs

- **Real-Time Viewing**: Live log stream with auto-scrolling
- **Level Filtering**: Filter by error, warning, info, debug levels
- **Search Functionality**: Find specific log entries
- **Clear Logs**: Remove old entries to maintain performance

## FFmpeg Integration

KPTV Proxy supports two streaming modes. In order to utilize FFmpeg, you must have it installed on your host machine and mount the binaries into the container.

### Go Restreaming Mode (Default)

- Pure Go implementation
- Efficient memory usage
- Fast startup times
- Basic stream processing

### FFmpeg Mode

- Hardware acceleration support via host GPU passthrough
- Advanced codec handling
- Handles MPEG-TS discontinuities (ad breaks, stream transitions)
- Comprehensive format support

**Simple copy mode (no GPU):**

```json
{
  "ffmpegMode": true,
  "ffmpegPreInput": [],
  "ffmpegPreOutput": ["-c", "copy", "-f", "mpegts"]
}
```

**Common FFmpeg Arguments:**

*Pre-Input:*

- `"-re"` - Read input at native frame rate
- `"-rtsp_transport", "tcp"` - Use TCP for RTSP
- `"-fflags", "nobuffer"` - Disable input buffering
- `"-hwaccel", "vaapi"` - Enable VAAPI hardware acceleration
- `"-hwaccel_device", "/dev/dri/renderD128"` - Specify render device

*Pre-Output:*

- `"-c", "copy"` - Copy streams without re-encoding
- `"-c:v", "h264_vaapi"` - H.264 encoding via VAAPI
- `"-c:a", "aac"` - AAC audio encoding
- `"-f", "mpegts"` - MPEG-TS output format
- `"-mpegts_flags", "initial_discontinuity"` - Handle ad-break discontinuities

## Dead Stream Management

### Features

- **Manual Stream Control**: Mark streams as dead or revive them from the admin interface
- **Automatic Blocking**: Streams exceeding failure thresholds are auto-blocked
- **Persistent Storage**: Dead stream data stored in `/settings/dead-streams.json`
- **Automatic Skipping**: Proxy skips dead streams during failover
- **Stream Revival**: Dead streams can be restored when functional again

### How It Works

1. In the admin interface, navigate to any channel and click **Streams**
2. Each stream shows:
   - **▶️ Activate**: Switch to this specific stream
   - **🚫 Kill**: Mark this stream as dead
   - **🔄 Revive**: Restore a dead stream
3. Dead streams are persisted to `/settings/dead-streams.json`
4. During automatic failover, dead streams are skipped

## Stream Watcher - Automatic Monitoring

The Stream Watcher runs as a background service monitoring active streams every 30 seconds (15 seconds in debug mode).

**Monitored Conditions:**

- **Buffer Throughput**: Detects streams delivering less than 200KB per check interval
- **Stream Activity**: Monitors data flow timestamps (120 second inactivity threshold)
- **Context Status**: Detects stuck or cancelled stream contexts
- **FFprobe Stats**: Monitors stream stats staleness (10 minute threshold)

**Automatic Actions:**

- **Consecutive Failure Tracking**: Requires 3 total failures before switching
- **Intelligent Failover**: Automatically switches to next available healthy stream
- **Seamless Transition**: Maintains client connections during stream changes
- **Self-Recovery**: Clears failure counters when stream recovers without intervention

## APP Endpoints

| Endpoint | Description |
|----------|-------------|
| `GET /` | Web admin interface |
| `GET /metrics` | Prometheus metrics |
| `GET /pl` | Unified playlist (all channels) |
| `GET /pl/{group}` | Group-filtered playlist |
| `GET /s/{channel}` | Stream proxy with automatic failover |
| `GET /epg` | EPG |
| `GET /epg.xml` | EPG |

## API Endpoints

| Endpoint | Description |
|----------|-------------|
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

| Setting | Default | Description |
|---------|---------|-------------|
| `baseURL` | `"http://localhost:8080"` | Base URL for generated stream links |
| `bufferSizePerStream` | `16` | Per-stream buffer size in MB |
| `cacheEnabled` | `true` | Enable playlist caching |
| `cacheDuration` | `"30m"` | Cache lifetime |
| `importRefreshInterval` | `"12h"` | How often to refresh source playlists |
| `workerThreads` | `4` | Parallel workers for import processing |
| `debug` | `false` | Enable verbose logging |
| `obfuscateUrls` | `true` | Hide source URLs in logs |
| `sortField` | `"tvg-name"` | Sort streams by attribute |
| `sortDirection` | `"asc"` | Sort direction: `asc` or `desc` |
| `streamTimeout` | `"10s"` | Global timeout for stream validation |
| `maxConnectionsToApp` | `100` | Maximum total connections to the application |
| `watcherEnabled` | `true` | Enable automatic stream monitoring |
| `ffmpegMode` | `false` | Use FFmpeg instead of Go streaming |
| `ffmpegPreInput` | `[]` | FFmpeg arguments before `-i` |
| `ffmpegPreOutput` | `[]` | FFmpeg arguments before output |
| `responseHeaderTimeout` | `"10s"` | Timeout for response headers from source |

### Per-Source Settings

| Setting | Required | Description | Example |
|---------|----------|-------------|---------|
| `name` | Yes | Friendly name | `"Primary IPTV"` |
| `url` | Yes | M3U8 playlist URL or XC base URL | `"http://provider.com/list.m3u8"` |
| `username` | No | XC API username | `"user123"` |
| `password` | No | XC API password | `"pass456"` |
| `order` | No | Priority order (lower = higher priority) | `1` |
| `maxConnections` | No | Max concurrent connections | `5` |
| `maxStreamTimeout` | No | Timeout for streams | `"30s"` |
| `retryDelay` | No | Delay between retries | `"5s"` |
| `maxRetries` | No | Retry attempts per failure | `3` |
| `maxFailuresBeforeBlock` | No | Failures before blocking | `5` |
| `minDataSize` | No | Minimum data size in KB | `2` |
| `userAgent` | No | Custom User-Agent header | `"VLC/3.0.18"` |
| `reqOrigin` | No | Custom Origin header | `"https://provider.com"` |
| `reqReferrer` | No | Custom Referrer header | `"https://provider.com/player"` |
| `liveIncludeRegex` | No | Only include live streams matching pattern | `".*USA.*"` |
| `liveExcludeRegex` | No | Exclude live streams matching pattern | `".*adult.*"` |
| `seriesIncludeRegex` | No | Only include series matching pattern | `""` |
| `seriesExcludeRegex` | No | Exclude series matching pattern | `""` |
| `vodIncludeRegex` | No | Only include VOD matching pattern | `""` |
| `vodExcludeRegex` | No | Exclude VOD matching pattern | `""` |

### XC Output Account Settings

| Setting | Required | Description |
|---------|----------|-------------|
| `name` | Yes | Friendly account name |
| `username` | Yes | XC login username |
| `password` | Yes | XC login password |
| `maxConnections` | No | Max simultaneous streams (default: 10) |
| `enableLive` | No | Include live streams (default: true) |
| `enableSeries` | No | Include series (default: false) |
| `enableVOD` | No | Include VOD (default: false) |

Example `xcOutputAccounts` config:

```json
{
  "xcOutputAccounts": [
    {
      "name": "My Tivimate Account",
      "username": "myuser",
      "password": "mypassword",
      "maxConnections": 4,
      "enableLive": true,
      "enableSeries": false,
      "enableVOD": false
    }
  ]
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

- ✅ Verify FFmpeg is installed on your host: `$(which ffmpeg) -version`
- ✅ Verify both `ffmpeg` and `ffprobe` binaries are mounted into the container
- ✅ Check FFmpeg arguments in debug logs
- ✅ Test with simple arguments first: `["-c", "copy"]`
- ✅ For hardware acceleration, verify `/dev/dri` device passthrough and driver library mount

**Problem**: Hardware acceleration not working

- ✅ Verify render node exists: `ls /dev/dri/` on host
- ✅ Confirm correct render node in config (usually `renderD128`)
- ✅ Verify VA-API driver library path for your distro is correctly mounted
- ✅ Confirm `group_add: [video, render]` is set in compose file
- ✅ Test with `vainfo` on the host to confirm VAAPI is functional

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

**Problem**: Ad-break freezes on channels like Pluto TV

- ✅ Enable FFmpeg mode with `-mpegts_flags initial_discontinuity` in pre-output args
- ✅ Add `-fflags +genpts+discardcorrupt+igndts` to pre-input args
- ✅ The stream watcher will detect and recover from prolonged stalls automatically

## Client Configuration Examples

### VLC Media Player

```
Network → Open Network Stream → http://your-server:9500/pl
```

### Kodi/LibreELEC

```
Add-ons → PVR IPTV Simple Client
M3U Play List URL: http://your-server:9500/pl
```

### Tivimate / IPTV Smarters (Xtream Codes)

```
Server URL:  http://your-server:9500
Username:    your-xc-account-username
Password:    your-xc-account-password
```

### Android/iOS IPTV Apps

```
Playlist URL: http://your-server:9500/pl
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
- **XC Passwords**: Use the built-in password generator for strong account credentials

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

KPTV Proxy will always remain free and open-source. If this project has enhanced your IPTV experience, consider supporting its continued development:

<https://www.paypal.com/paypalme/kevinpirnie>

## Contributing

1. Fork the repository
2. Create a feature branch: `git checkout -b feature/amazing-feature`
3. Commit changes: `git commit -m 'Add amazing feature'`
4. Push to branch: `git push origin feature/amazing-feature`
5. Open a Pull Request

---

## Third-Party Software

**FFmpeg License Information:**

- KPTV Proxy uses FFmpeg and FFprobe for stream processing and validation; they are not included in the container image
- FFmpeg is used as an external binary mounted from the host
- FFmpeg source code: [https://ffmpeg.org/download.html](https://ffmpeg.org/download.html)
- FFmpeg is licensed under LGPL v2.1 or later

**Patent Considerations:**
FFmpeg may use patented algorithms for various multimedia codecs. Patent laws vary by jurisdiction. For commercial use, consult legal counsel regarding potential patent licensing requirements in your jurisdiction.

## License

MIT License - see [LICENSE](LICENSE) file for details.

### Third-Party Licenses

- **FFmpeg/FFprobe**: Licensed under LGPL v2.1 or later - [https://ffmpeg.org/legal.html](https://ffmpeg.org/legal.html)
- **TailwindCSS**: MIT License - [https://tailwindcss.com/](https://tailwindcss.com/)

---

**Need Help?** Use the web admin interface at `http://your-server:port/` for easy configuration management, customize the appearance with `/settings/custom.css`, or enable debug mode and check the logs for detailed information. The automatic Stream Watcher will help maintain stream reliability in the background, and FFmpeg integration provides advanced streaming capabilities for complex media formats.

**Still Need Help?** Hit me up on Discord: [https://discord.gg/bd4Qan3PaN](https://discord.gg/bd4Qan3PaN)