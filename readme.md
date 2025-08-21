# KPTV Proxy - IPTV Stream Aggregator & Proxy

A high-performance Go-based IPTV proxy server that intelligently aggregates streams from multiple sources, provides automatic channel deduplication, failover capabilities, and serves them through a unified M3U8 playlist with advanced streaming options.

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

### 🚀 **Restreaming Architecture** 
- **Efficient Resource Usage**: Single upstream connection shared among multiple clients
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

### 📊 **Monitoring & Metrics**
- Prometheus metrics integration
- Connection tracking per channel and source
- Stream error monitoring and categorization
- Health check endpoints
- Detailed logging with configurable verbosity

### 🌐 **Web Admin Interface**
- **Dark Mobile-Friendly Design**: Responsive web interface optimized for all devices
- **Real-Time Dashboard**: Live statistics, active channels, and system monitoring
- **Configuration Management**: Edit global settings and per-source configurations through web UI
- **Source Management**: Add, edit, delete, and reorder IPTV sources with full validation
- **Channel Monitoring**: View all channels with real-time status and client information
- **Live Logs**: Real-time log viewing with filtering by level (error, warning, info, debug)
- **Graceful Restart**: Apply configuration changes with zero-downtime restarts
- **Auto-Refresh**: Dashboard updates every 5 seconds for real-time monitoring

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

## Quick Start

**Prerequisites**: Docker or Podman installed

1. **Create settings directory and get configuration:**
```bash
mkdir settings
wget https://raw.githubusercontent.com/your-repo/kptv-proxy/main/docker-compose.example.yaml -O docker-compose.yaml
```

2. **Create your configuration file** - Create `settings/config.json`:
```json
{
  "baseURL": "http://your-server-ip:9500",
  "maxBufferSize": 256,
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
    },
    {
      "name": "Backup IPTV Source",
      "url": "http://provider2.com/playlist.m3u8",
      "order": 2,
      "maxConnections": 10,
      "maxStreamTimeout": "45s",
      "retryDelay": "10s",
      "maxRetries": 2,
      "maxFailuresBeforeBlock": 3,
      "minDataSize": 1,
      "userAgent": "Mozilla/5.0 (Smart TV; Linux)",
      "reqOrigin": "https://provider2.com",
      "reqReferrer": "https://provider2.com/player"
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
Unified Playlist: http://your-server-ip:9500/playlist
Group Filtered Playlist: http://your-server-ip:9500/{group}/playlist
Admin Interface:  http://your-server-ip:9500/admin
```

**That's it!** Your IPTV sources are now unified into a single playlist with automatic failover, per-source configuration, and a powerful web-based admin interface.

## Web Admin Interface

Access the admin interface at `http://your-server:port/admin` for comprehensive management:

### Dashboard
- **Real-Time Statistics**: Total channels, active streams, connected clients, memory usage
- **System Status**: Server uptime, cache status, worker thread count
- **Traffic Metrics**: Connection counts, bytes transferred, stream errors
- **Active Channels**: Live view of currently streaming channels with client counts
- **Auto-Refresh**: Updates every 5 seconds for real-time monitoring

### Global Settings
- Edit all configuration parameters through intuitive web forms
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

## API Endpoints

| Endpoint | Description |
|----------|-------------|
| `GET /playlist` | Unified playlist (all channels) |
| `GET /{group}/playlist` | Group-filtered playlist |
| `GET /stream/{channel}` | Stream proxy with automatic failover |
| `GET /metrics` | Prometheus metrics |
| `GET /admin` | Web admin interface |
| `GET /api/config` | Get current configuration |
| `POST /api/config` | Update configuration |
| `GET /api/stats` | System statistics |
| `GET /api/channels` | All channels |
| `GET /api/channels/active` | Active channels only |
| `GET /api/logs` | Application logs |
| `POST /api/restart` | Graceful restart |

## Configuration Reference

### Global Settings
All configuration is done via a JSON file mounted at `/settings/config.json` or through the web admin interface.

| Setting | Default | Description |
|---------|---------|-------------|
| `baseURL` | `"http://localhost:8080"` | Base URL for generated stream links |
| `maxBufferSize` | `256` | Total buffer size in MB |
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
    
    # Health check
    healthcheck:
      test: ["CMD", "wget", "--quiet", "--tries=1", "--spider", "http://localhost:8080/playlist"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 10s
```

## Advanced Configuration Examples

### High-Performance Multi-Provider Setup
```json
{
  "baseURL": "http://your-server:9500",
  "maxBufferSize": 512,
  "bufferSizePerStream": 32,
  "workerThreads": 20,
  "maxConnectionsToApp": 500,
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
    },
    {
      "name": "Backup Provider 1",
      "url": "http://backup1.iptv.com/playlist.m3u8", 
      "order": 2,
      "maxConnections": 8,
      "maxStreamTimeout": "30s",
      "retryDelay": "5s",
      "maxRetries": 3,
      "maxFailuresBeforeBlock": 5,
      "minDataSize": 2,
      "userAgent": "VLC/3.0.18 LibVLC/3.0.18"
    },
    {
      "name": "Backup Provider 2",
      "url": "http://backup2.iptv.com/playlist.m3u8",
      "order": 3,
      "maxConnections": 15,
      "maxStreamTimeout": "45s", 
      "retryDelay": "10s",
      "maxRetries": 5,
      "maxFailuresBeforeBlock": 8,
      "minDataSize": 1,
      "userAgent": "Mozilla/5.0 (Smart TV; Linux)",
      "reqOrigin": "https://backup2.iptv.com",
      "reqReferrer": "https://backup2.iptv.com/player"
    }
  ]
}
```

### Provider-Specific Headers Configuration
```json
{
  "sources": [
    {
      "name": "Provider with Custom Headers",
      "url": "http://special-provider.com/list.m3u8",
      "order": 1,
      "maxConnections": 5,
      "userAgent": "SpecialClient/2.1 (Linux; Smart TV)",
      "reqOrigin": "https://special-provider.com",
      "reqReferrer": "https://special-provider.com/tv-guide"
    },
    {
      "name": "Standard Provider",
      "url": "http://standard-provider.com/playlist.m3u8",
      "order": 2,
      "maxConnections": 10,
      "userAgent": "VLC/3.0.18 LibVLC/3.0.18"
    }
  ]
}
```

### Low-Resource Configuration
```json
{
  "maxBufferSize": 64,
  "bufferSizePerStream": 4,
  "workerThreads": 2,
  "maxConnectionsToApp": 50,
  "sources": [
    {
      "name": "Single Provider",
      "url": "http://provider.com/playlist.m3u8",
      "order": 1,
      "maxConnections": 2,
      "maxStreamTimeout": "15s",
      "retryDelay": "3s",
      "maxRetries": 2,
      "minDataSize": 1
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

# Check specific errors
docker-compose logs kptv-proxy | grep ERROR

# View configuration loading
docker-compose logs kptv-proxy | grep "Configuration loaded"
```

### Key Metrics (Prometheus)
- `iptv_proxy_active_connections` - Active connections per channel
- `iptv_proxy_bytes_transferred` - Data transfer metrics
- `iptv_proxy_stream_errors` - Error counts by type
- `iptv_proxy_clients_connected` - Connected clients per channel

### Common Issues & Solutions

**Problem**: Configuration not loading
- ✅ Use the web admin interface to edit configuration
- ✅ Check JSON syntax: `cat settings/config.json | jq .`
- ✅ Verify file permissions: `ls -la settings/`
- ✅ Check logs in admin interface or container logs

**Problem**: No channels in playlist
- ✅ Add sources through the web admin interface
- ✅ Check source URLs are accessible in admin interface
- ✅ Enable debug mode through admin interface
- ✅ Verify M3U8 format is valid
- ✅ Check connection limits aren't too restrictive

**Problem**: Streams failing to play  
- ✅ Monitor channel status in admin interface
- ✅ Check per-source retry settings in source management
- ✅ Verify source-specific connection limits
- ✅ Test custom headers through admin interface
- ✅ Verify `baseURL` is accessible from clients

**Problem**: Rate limiting (429 errors)
- ✅ Reduce `maxConnections` for affected sources in admin interface
- ✅ Increase `retryDelay` for affected sources
- ✅ Monitor active connections in dashboard
- ✅ Check `maxConnectionsToApp` limit in global settings

**Problem**: High memory usage
- ✅ Reduce `maxBufferSize` and `bufferSizePerStream` in admin interface
- ✅ Decrease `workerThreads` in global settings
- ✅ Monitor memory usage in admin dashboard
- ✅ Lower `maxConnections` per source

**Problem**: Provider-specific authentication issues
- ✅ Configure proper headers through source management interface
- ✅ Test different User-Agent strings
- ✅ Use custom Origin and Referrer headers

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
- **HTTPS**: Use HTTPS sources when available
- **Configuration Security**: Protect `/settings/config.json` with appropriate file permissions

## Performance Optimization

### For High-Concurrency (100+ clients)
```json
{
  "workerThreads": 20,
  "maxBufferSize": 1024,
  "maxConnectionsToApp": 500,
  "sources": [
    {
      "maxConnections": 3,
      "maxStreamTimeout": "15s"
    }
  ]
}
```

### For Low-Resource Systems
```json
{
  "workerThreads": 2,
  "maxBufferSize": 64,
  "bufferSizePerStream": 4,
  "maxConnectionsToApp": 50
}
```

### For Provider-Specific Optimization
```json
{
  "sources": [
    {
      "name": "Fast Provider",
      "maxStreamTimeout": "10s",
      "retryDelay": "2s",
      "maxRetries": 2
    },
    {
      "name": "Slow Provider", 
      "maxStreamTimeout": "60s",
      "retryDelay": "15s",
      "maxRetries": 5
    }
  ]
}
```

## Migration from Environment Variables

If you're upgrading from a version that used environment variables, here's how to convert:

**Old (Environment Variables):**
```yaml
environment:
  - SOURCES=http://provider1.com/list.m3u8:5,http://provider2.com/list.m3u8:10
  - USER_AGENT=VLC/3.0.18 LibVLC/3.0.18
  - MAX_RETRIES=3
```

**New (JSON Configuration):**
```json
{
  "sources": [
    {
      "name": "Provider 1",
      "url": "http://provider1.com/list.m3u8",
      "order": 1,
      "maxConnections": 5,
      "maxRetries": 3,
      "userAgent": "VLC/3.0.18 LibVLC/3.0.18"
    },
    {
      "name": "Provider 2", 
      "url": "http://provider2.com/list.m3u8",
      "order": 2,
      "maxConnections": 10,
      "maxRetries": 3,
      "userAgent": "VLC/3.0.18 LibVLC/3.0.18"
    }
  ]
}
```

**Alternatively, use the web admin interface to migrate configuration visually.**

## Third-Party Software

### FFmpeg/FFprobe

This software uses code of [FFmpeg](http://ffmpeg.org) licensed under the [LGPLv2.1](http://www.gnu.org/licenses/old-licenses/lgpl-2.1.html).

**FFmpeg License Information:**
- KPTV Proxy uses FFprobe (part of the FFmpeg project) for stream validation
- FFprobe is used as an external binary (not linked/embedded)
- FFmpeg source code can be downloaded from: [https://ffmpeg.org/download.html](https://ffmpeg.org/download.html)
- FFmpeg is licensed under LGPL v2.1 or later

**Patent Considerations:**
FFmpeg may use patented algorithms for various multimedia codecs. Patent laws vary by jurisdiction. For commercial use, consult legal counsel regarding potential patent licensing requirements in your jurisdiction.

## Contributing

1. Fork the repository
2. Create a feature branch: `git checkout -b feature/amazing-feature`
3. Commit changes: `git commit -m 'Add amazing feature'`
4. Push to branch: `git push origin feature/amazing-feature`
5. Open a Pull Request

## License

MIT License - see [LICENSE](LICENSE) file for details.

### Third-Party Licenses

This project incorporates or uses the following third-party software:

- **FFmpeg/FFprobe**: Licensed under LGPL v2.1 or later - [https://ffmpeg.org/legal.html](https://ffmpeg.org/legal.html)

---

**Need Help?** Use the web admin interface at `http://your-server:port/admin` for easy configuration management, or enable debug mode and check the logs for detailed information about stream processing, connection attempts, and error details.