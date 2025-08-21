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
- Flexible source configuration with connection limits
- Customizable stream sorting by any M3U8 attribute
- URL obfuscation for privacy and security
- Custom HTTP headers (User-Agent, Origin, Referrer)
- Configurable timeouts and buffer sizes
- Debug mode with extensive logging

### 📊 **Monitoring & Metrics**
- Prometheus metrics integration
- Connection tracking per channel and source
- Stream error monitoring and categorization
- Health check endpoints
- Detailed logging with configurable verbosity

## Architecture Overview

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│   IPTV Source 1 │    │   IPTV Source 2  │    │   IPTV Source N │
│   (5 max conns) │    │  (10 max conns)  │    │  (3 max conns)  │
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
                    │  • Failover logic       │
                    │  • Connection mgmt      │
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

## Restreaming Architecture

**How It Works:**
```
┌─────────────┐    ┌─────────────────┐    ┌──────────────┐
│ IPTV Source │───▶│   KPTV Proxy    │───▶│   Client 1   │
│             │    │ ┌─────────────┐ │    ├──────────────┤
│ (1 stream   │    │ │ Restreamer  │ │───▶│   Client 2   │  
│  connection)│    │ │  - Buffer   │ │    ├──────────────┤
│             │    │ │  - Segments │ │───▶│   Client N   │
└─────────────┘    │ └─────────────┘ │    └──────────────┘
                   └─────────────────┘
```

**Benefits:**
- ✅ Prevents 429 rate limit errors from providers
- ✅ Reduces bandwidth costs and server load  
- ✅ Better for providers with strict connection limits
- ✅ Automatic stream cleanup when no clients connected
- ✅ Real-time client connection management

## How Channel Grouping Works

The proxy intelligently groups channels with the same name from different sources:

**Input Sources:**
```
Source 1: BBC One, CNN, ESPN
Source 2: BBC One, Fox News, ESPN  
Source 3: CNN, ESPN, Discovery
```

**Unified Output:**
```
BBC One    → [Source1/BBC_One, Source2/BBC_One] (auto-failover)
CNN        → [Source1/CNN, Source3/CNN] (auto-failover)  
ESPN       → [Source1/ESPN, Source2/ESPN, Source3/ESPN] (auto-failover)
Fox News   → [Source2/Fox_News] (single source)
Discovery  → [Source3/Discovery] (single source)
```

## Advanced HLS Stream Handling

The proxy includes sophisticated HLS processing that handles complex streaming scenarios:

### **Tracking URL Resolution**
Automatically detects and resolves ad-insertion tracking URLs:
```
Input:  https://provider.com/beacon/track?redirect_url=https%3A%2F%2Freal-video.ts
Output: https://real-video.ts (extracted and decoded)
```

### **Format Error Recovery**  
Handles streams with format quirks (like BBC America) by:
- Detecting problematic ffprobe validation patterns
- Bypassing validation for known problematic formats
- Attempting direct streaming when validation fails

### **Multi-Variant Testing**
For master playlists, tests variants from highest to lowest quality:
```
Testing variants:
✅ 1920x1080 (5000 kbps) - Success → Stream this quality
❌ 1280x720  (3000 kbps) - Failed  → Try next
❌ 854x480   (1500 kbps) - Failed  → Try next
```

## Master Playlist Processing

The proxy automatically detects HLS master playlists and intelligently selects the best variant:

```
Input Master Playlist:
#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=1000000,RESOLUTION=720x480
low.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=3000000,RESOLUTION=1280x720  
med.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=5000000,RESOLUTION=1920x1080
high.m3u8

Proxy Selection Logic:
✅ Selects highest quality first (most reliable providers)
✅ Falls back to lower quality on errors
✅ Logs all available variants
✅ Handles tracking URLs automatically
```

## Quick Start

**Prerequisites**: Docker or Podman installed

1. **Get the configuration:**
```bash
wget https://raw.githubusercontent.com/your-repo/kptv-proxy/main/docker-compose.example.yaml -O docker-compose.yaml
```

2. **Configure your sources** - Edit `docker-compose.yaml`:
```yaml
environment:
  # Replace with your IPTV provider URLs
  SOURCES: "http://provider1.com/playlist.m3u8|5,http://provider2.com/playlist.m3u8|10"
  BASE_URL: "http://your-server-ip:9500"
```

3. **Start the proxy:**
```bash
# Docker
docker compose up -d

# Or Podman  
podman-compose up -d
```

4. **Access your unified playlist:**
```
http://your-server-ip:9500/playlist.m3u8
```

**That's it!** Your IPTV sources are now unified into a single playlist with automatic failover.

## Configuration Reference

### Core Settings
| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8080` | Server port |
| `BASE_URL` | `http://localhost:8080` | Base URL for generated stream links |
| `SOURCES` | Required | Comma-separated list: `URL\|MaxConns,URL\|MaxConns` |

**Why Restreaming?**
- ✅ Reduces provider load (prevents 429 rate limit errors)
- ✅ Better for providers with strict connection limits  
- ✅ More efficient bandwidth usage
- ✅ Automatic stream cleanup

### Stream Management
| Variable | Default | Description |
|----------|---------|-------------|
| `MAX_RETRIES` | `3` | Retry attempts per stream failure |
| `MAX_FAILURES_BEFORE_BLOCK` | `5` | Failures before blocking a stream |
| `RETRY_DELAY` | `5s` | Delay between retry attempts |
| `MIN_DATA_SIZE` | `1` | Minimum amount of data the stream must contain to be considered valid in KB |
| `IMPORT_REFRESH_INTERVAL` | `12h` | How often to refresh source playlists |

### Performance Tuning
| Variable | Default | Description |
|----------|---------|-------------|
| `WORKER_THREADS` | `4` | Parallel workers for import processing |
| `MAX_BUFFER_SIZE` | `256` | Total buffer size (256MB) |
| `BUFFER_SIZE_PER_STREAM` | `16` | Per-stream buffer (16MB) |
| `STREAM_TIMEOUT` | `10s` | Timeout for stream validation and ffprobe |

### Customization
| Variable | Default | Description |
|----------|---------|-------------|
| `SORT_FIELD` | `tvg-name` | Sort streams by: `tvg-name`, `tvg-id`, `group-title`, etc. |
| `SORT_DIRECTION` | `asc` | Sort direction: `asc` or `desc` |
| `USER_AGENT` | `VLC/3.0.18 LibVLC/3.0.18` | Custom User-Agent header |
| `REQ_ORIGIN` | `` | Optional Origin header |
| `REQ_REFERRER` | `` | Optional Referrer header |

### Privacy & Security
| Variable | Default | Description |
|----------|---------|-------------|
| `OBFUSCATE_URLS` | `true` | Hide source URLs in logs for privacy |
| `DEBUG` | `false` | Enable verbose logging |

### Caching
| Variable | Default | Description |
|----------|---------|-------------|
| `CACHE_ENABLED` | `true` | Enable playlist caching |
| `CACHE_DURATION` | `30m` | Cache lifetime |

## API Endpoints

| Endpoint | Description |
|----------|-------------|
| `GET /playlist` | Unified playlist (all channels) |
| `GET /{group}/playlist` | Group-filtered playlist |
| `GET /stream/{channel}` | Stream proxy with automatic failover |
| `GET /metrics` | Prometheus metrics |

## Advanced Usage Examples

### High-Performance Configuration
```yaml
# For servers handling many concurrent clients
WORKER_THREADS: "20"
MAX_BUFFER_SIZE: "512"    # 512MB
BUFFER_SIZE_PER_STREAM: "32"  # 32MB  
```

### Multi-Provider Setup with Priorities
```yaml
# Primary provider (lower connection limit = higher priority in failover)
# Backup providers (higher limits)
SOURCES: "http://premium.iptv.com/list.m3u8|3,http://backup1.iptv.com/list.m3u8|8,http://backup2.iptv.com/list.m3u8|15"
```

### Custom Sorting and Grouping
```yaml
# Sort by channel group, then by name  
SORT_FIELD: "group-title"
SORT_DIRECTION: "asc"

# Custom headers for specific providers
USER_AGENT: "Mozilla/5.0 (Smart TV; Linux)"
REQ_ORIGIN: "https://provider.com"
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
```

### Key Metrics (Prometheus)
- `iptv_proxy_active_connections` - Active connections per channel
- `iptv_proxy_bytes_transferred` - Data transfer metrics
- `iptv_proxy_stream_errors` - Error counts by type
- `iptv_proxy_clients_connected` - Connected clients per channel

### Common Issues & Solutions

**Problem**: No channels in playlist
- ✅ Verify source URLs are accessible: `curl -I http://source.com/playlist.m3u8`
- ✅ Check logs for parsing errors: `DEBUG: "true"`
- ✅ Ensure M3U8 format is valid

**Problem**: Streams failing to play  
- ✅ Enable debug mode to see detailed connection attempts
- ✅ Try increasing `MAX_RETRIES` and `RETRY_DELAY`
- ✅ Check if sources have connection limits
- ✅ Verify `BASE_URL` is accessible from clients

**Problem**: Rate limiting (429 errors)
- ✅ Reduce connection limits in `SOURCES`
- ✅ Increase `RETRY_DELAY`
- ✅ Use restreaming architecture (automatically enabled)

**Problem**: High memory usage
- ✅ Reduce `MAX_BUFFER_SIZE` and `BUFFER_SIZE_PER_STREAM`
- ✅ Decrease `WORKER_THREADS`
- ✅ Enable cache expiration: `CACHE_DURATION: "5m"`

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
- **Access Control**: Consider adding authentication for private deployments  
- **Source Privacy**: Enable `OBFUSCATE_URLS` to hide provider URLs in logs
- **Container Security**: Runs as non-root user (UID 1000)
- **HTTPS**: Use HTTPS sources when available

## Performance Optimization

### For High-Concurrency (100+ clients)
```yaml
WORKER_THREADS: "20"
MAX_BUFFER_SIZE: "1024"  # 1GB
```

### For Low-Resource Systems
```yaml
WORKER_THREADS: "2"
MAX_BUFFER_SIZE: "64"    # 64MB
BUFFER_SIZE_PER_STREAM: "4"  # 4MB
```

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

**Need Help?** Enable debug mode (`DEBUG: "true"`) and check the logs for detailed information about stream processing, connection attempts, and error details.