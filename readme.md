# KPTV Proxy Restreamer

A high-performance Go-based IPTV proxy server that aggregates streams from multiple sources, provides automatic failover, and serves them through a unified M3U8 playlist.

## Features

- **Multiple Source Support**: Configure multiple IPTV sources with connection limits
- **Automatic Channel Grouping**: Streams are automatically grouped by channel name
- **Failover Support**: Automatic failover to next available stream on failure
- **Connection Management**: Per-source connection limits to prevent overload
- **Smart Buffering**: Configurable buffer sizes with buffer pool for optimal memory usage
- **Caching**: Built-in caching for playlists and channel data
- **Sorting**: Customizable stream sorting by any EXTINF attribute
- **Retry Logic**: Configurable retry attempts with exponential backoff
- **Worker Pool**: Parallel processing with configurable worker threads
- **Debug Mode**: Extensive logging for troubleshooting
- **Docker Support**: Ready-to-use Docker configuration
- **Restreaming Mode**: Single upstream connection shared among multiple clients (NEW!)
- **Custom HTTP Headers**: Configurable User-Agent, Origin, and Referrer headers
- **Connection Keep-Alive**: HTTP keep-alive for better performance

## Quick Start

1. Clone the repository:
```bash
git clone <repository-url>
cd kptv-proxy
```

2. Configure your sources in `docker-compose.yaml`:
```yaml
SOURCES: "http://source1.com/playlist.m3u8:5,http://source2.com/playlist.m3u8:10"
```

3. Start the service:
```bash
docker-compose up -d
```

4. Access your unified playlist at:
```
http://localhost:8080/playlist.m3u8
```

## Configuration

All configuration is done through environment variables in `docker-compose.yaml`:

### Server Settings
- `PORT`: Server port (default: 8080)
- `BASE_URL`: Base URL for stream links (default: http://localhost:8080)

### Source Configuration
- `SOURCES`: Comma-separated list of sources with format `URL|MaxConnections`
  - Example: `http://source1.com/playlist.m3u8|5,http://source2.com/playlist.m3u8|10`

### Buffer Settings
- `MAX_BUFFER_SIZE`: Maximum total buffer size in bytes (default: 10MB)
- `BUFFER_SIZE_PER_STREAM`: Buffer size per stream in bytes (default: 1MB)
- `MIN_DATA_SIZE`: Minimum data required for successful stream (default: 1KB)

### Cache Settings
- `CACHE_ENABLED`: Enable/disable caching (default: true)
- `CACHE_DURATION`: Cache duration (default: 5m)

### Import Settings
- `IMPORT_REFRESH_INTERVAL`: How often to refresh source imports (default: 30m)

### Retry Settings
- `MAX_RETRIES`: Maximum retry attempts per stream (default: 3)
- `MAX_FAILURES_BEFORE_BLOCK`: Failures before marking stream as blocked (default: 10)
- `RETRY_DELAY`: Delay between retry attempts (default: 5s)

### Performance Settings
- `WORKER_THREADS`: Number of parallel workers for import (default: 10)
- `ENABLE_RESTREAMING`: Enable single upstream connection mode (default: true)

### HTTP Client Settings
- `USER_AGENT`: Custom User-Agent header (default: kptv-proxy/1.0)
- `REQ_ORIGIN`: Optional Origin header for requests
- `REQ_REFERRER`: Optional Referrer header for requests
- `HEALTH_CHECK_TIMEOUT`: Request timeout duration (default: 30s)

### Sorting Settings
- `SORT_FIELD`: EXTINF attribute to sort by (default: tvg-name)
- `SORT_DIRECTION`: Sort direction - asc or desc (default: asc)

### Debug Settings
- `DEBUG`: Enable verbose logging (default: false)

## How It Works

### Standard Mode (ENABLE_RESTREAMING=false)
1. **Import Phase**: The proxy fetches M3U8 playlists from all configured sources
2. **Channel Grouping**: Streams with the same name are grouped into channels
3. **Sorting**: Streams within each channel are sorted based on configuration
4. **Client Request**: When a client requests the playlist, a unified M3U8 is generated
5. **Stream Proxy**: When a client plays a stream:
   - Each client gets their own upstream connection
   - On failure, automatically tries the next stream in the group
   - Manages connections to prevent source overload
   - Buffers data for smooth playback

### Restreaming Mode (ENABLE_RESTREAMING=true) - Recommended
1. **Single Upstream Connection**: Only one connection per channel to the upstream provider
2. **Client Multiplexing**: Multiple clients share the same upstream stream
3. **Automatic Cleanup**: Inactive channels are cleaned up after 10 seconds
4. **Benefits**:
   - Reduces load on upstream providers (prevents 429 errors)
   - Better for providers with connection limits
   - More efficient bandwidth usage
   - Prevents rate limiting issues

## Endpoints

- `GET /playlist.m3u8`: Returns the unified M3U8 playlist
- `GET /stream/{channel}`: Proxies the actual stream data with automatic failover

## Advanced Usage

### Custom Source Configuration

Sources are configured as a comma-separated list with the format `URL|MaxConnections`:

```bash
SOURCES="http://premium.iptv.com/playlist.m3u8|3,http://backup.iptv.com/playlist.m3u8|5,http://free.iptv.com/playlist.m3u8|10"
```

This configuration:
- Allows 3 concurrent connections to premium source
- Allows 5 concurrent connections to backup source
- Allows 10 concurrent connections to free source

### Sorting Options

You can sort streams by any EXTINF attribute:

```yaml
SORT_FIELD: "tvg-id"        # Sort by channel ID
SORT_FIELD: "group-title"   # Sort by group
SORT_FIELD: "tvg-logo"      # Sort by logo URL
SORT_DIRECTION: "desc"      # Reverse order
```

### Cache Management

The cache can be configured with different durations:

```yaml
CACHE_DURATION: "1m"   # 1 minute cache
CACHE_DURATION: "1h"   # 1 hour cache
CACHE_DURATION: "0"    # Disable cache
```

### Performance Tuning

For high-load environments:

```yaml
WORKER_THREADS: "50"              # Increase parallel workers
MAX_BUFFER_SIZE: "52428800"       # 50MB buffer
BUFFER_SIZE_PER_STREAM: "5242880" # 5MB per stream
```

### Debug Mode

Enable debug mode to see detailed logs:

```yaml
DEBUG: "true"
```

This will log:
- All HTTP requests
- Stream parsing details
- Failover attempts
- Cache operations
- Connection management

## Monitoring

### Health Check

The Docker container includes a health check that verifies the playlist endpoint:

```bash
docker-compose ps  # Check health status
```

### Logs

Monitor real-time logs:

```bash
docker-compose logs -f kptv-proxy
```

Filter for errors:

```bash
docker-compose logs kptv-proxy | grep ERROR
```

### Metrics

When debug mode is enabled, you can track:
- Number of active connections per source
- Failed streams and retry attempts
- Cache hit/miss rates
- Channel and stream counts

## Troubleshooting

### Common Issues

1. **No streams appearing in playlist**
   - Check source URLs are accessible
   - Verify M3U8 format is valid
   - Enable debug mode to see parsing errors

2. **Streams failing to play**
   - Check network connectivity
   - Verify source streams are active
   - Increase retry attempts and delays

3. **High memory usage**
   - Reduce buffer sizes
   - Limit worker threads
   - Enable cache expiration

4. **Connection refused errors**
   - Ensure port is not in use
   - Check firewall settings
   - Verify Docker networking

### Debug Commands

Test source accessibility:
```bash
curl -I http://source.com/playlist.m3u8
```

Check generated playlist:
```bash
curl http://localhost:8080/playlist.m3u8
```

Test stream proxy:
```bash
curl -I http://localhost:8080/stream/ChannelName
```

## Security Considerations

- Run as non-root user in Docker
- Use HTTPS sources when possible
- Implement rate limiting for production use
- Consider adding authentication for private deployments

## Contributing

1. Fork the repository
2. Create a feature branch
3. Commit your changes
4. Push to the branch
5. Create a Pull Request

## License

This project is provided as-is for educational and personal use.
