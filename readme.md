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
- **Variant Selection**: Intelligently selects optimal stream variants (lowest bandwidth by default for reliability)
- **Channel Deduplication**: Groups identical channels from different sources
- **Smart Failover**: Seamlessly switches between sources when streams fail

### 🚀 **Dual Operating Modes**
- **Direct Proxy Mode**: Each client gets independent connections to upstream sources
- **Restreaming Mode** *(Recommended)*: Single upstream connection shared among multiple clients
  - Reduces load on upstream providers
  - Prevents rate limiting and 429 errors  
  - More efficient bandwidth usage
  - Automatic cleanup of inactive streams

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

**Restreaming Mode Benefits:**
- ✅ Reduces provider load (prevents 429 rate limit errors)
- ✅ Better for providers with strict connection limits  
- ✅ More efficient bandwidth usage
- ✅ Automatic stream cleanup

**Direct Proxy Mode:**
- ✅ Each client gets independent connection
- ✅ Better isolation between clients
- ❌ Higher provider load
- ❌ More prone to rate limiting

### Stream Management
| Variable | Default | Description |
|----------|---------|-------------|
| `MAX_RETRIES` | `3` | Retry attempts per stream failure |
| `MAX_FAILURES_BEFORE_BLOCK` | `5` | Failures before blocking a stream |
| `RETRY_DELAY` | `5s` | Delay between retry attempts |
| `IMPORT_REFRESH_INTERVAL` | `12h` | How often to refresh source playlists |

### Performance Tuning
| Variable | Default | Description |
|----------|---------|-------------|
| `WORKER_THREADS` | `4` | Parallel workers for import processing |
| `MAX_BUFFER_SIZE` | `268435456` | Total buffer size (256MB) |
| `BUFFER_SIZE_PER_STREAM` | `16777216` | Per-stream buffer (16MB) |

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
| `GET /playlist.m3u8` | Unified playlist (all channels) |
| `GET /{group}/playlist.m3u8` | Group-filtered playlist |
| `GET /stream/{channel}` | Stream proxy with automatic failover |
| `GET /metrics` | Prometheus metrics |

## Advanced Usage Examples

### High-Performance Configuration
```yaml
# For servers handling many concurrent clients
WORKER_THREADS: "20"
MAX_BUFFER_SIZE: "536870912"    # 512MB
BUFFER_SIZE_PER_STREAM: "33554432"  # 32MB  
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

## Master Playlist Handling

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
✅ Selects lowest bandwidth (most reliable)
✅ Logs all available variants
✅ Falls back gracefully on errors
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

**Problem**: High memory usage
- ✅ Reduce `MAX_BUFFER_SIZE` and `BUFFER_SIZE_PER_STREAM`
- ✅ Decrease `WORKER_THREADS`
- ✅ Enable cache expiration: `CACHE_DURATION: "5m"`

## Client Configuration Examples

### VLC Media Player
```
Network → Open Network Stream → http://your-server:9500/playlist.m3u8
```

### Kodi/LibreELEC
```
Add-ons → PVR IPTV Simple Client
M3U Play List URL: http://your-server:9500/playlist.m3u8
```

### Android/iOS IPTV Apps
```
Playlist URL: http://your-server:9500/playlist.m3u8
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
MAX_BUFFER_SIZE: "1073741824"  # 1GB
```

### For Low-Resource Systems
```yaml
WORKER_THREADS: "2"
MAX_BUFFER_SIZE: "67108864"    # 64MB
BUFFER_SIZE_PER_STREAM: "4194304"  # 4MB
```

## Contributing

1. Fork the repository
2. Create a feature branch: `git checkout -b feature/amazing-feature`
3. Commit changes: `git commit -m 'Add amazing feature'`
4. Push to branch: `git push origin feature/amazing-feature`
5. Open a Pull Request

## License

This project is provided as-is for educational and personal use.

---

**Need Help?** Enable debug mode (`DEBUG: "true"`) and check the logs for detailed information about stream processing, connection attempts, and error details.