/**
 * Fetches current system statistics from the API and updates
 * all stat display elements on the dashboard.
 * Falls back to zeroed values on error to prevent stale displays.
 * @returns {Promise<void>}
 */
async function loadStats() {
    try {
        const stats = await apiCall('/api/stats');
        updateStatsDisplay(stats);
    } catch (error) {
        updateStatsDisplay({
            totalChannels: 0,
            activeStreams: 0,
            totalSources: 0,
            totalEpgs: 0,
            connectedClients: 0,
            uptime: '0m',
            memoryUsage: '0 MB',
            cacheStatus: 'Disabled',
            workerThreads: 4,
            totalConnections: 0,
            bytesTransferred: '0 B',
            activeRestreamers: 0,
            streamErrors: 0,
            responseTime: '0ms'
        });
    }
}

/**
 * Updates all stat card and metric row elements with fresh values
 * from the provided stats object. Also calculates derived metrics
 * such as connection efficiency and average clients per stream.
 * @param {Object} stats - Stats response object from /api/stats
 */
function updateStatsDisplay(stats) {
    const elements = {
        'total-channels': stats.totalChannels || 0,
        'active-streams': stats.activeStreams || 0,
        'total-sources': stats.totalSources || 0,
        'total-epgs': stats.totalEpgs || 0,
        'connected-clients': stats.connectedClients || 0,
        'uptime': stats.uptime || '0m',
        'memory-usage': stats.memoryUsage || '0 MB',
        'cache-status': stats.cacheStatus || 'Unknown',
        'total-connections': stats.totalConnections || 0,
        'bytes-transferred': stats.bytesTransferred || '0 B',
        'active-restreamers': stats.activeRestreamers || 0,
        'stream-errors': stats.streamErrors || 0,
        'response-time': stats.responseTime || '0ms',
        'proxy-clients': stats.connectedClients || 0,
        'upstream-connections': stats.upstreamConnections || 0,
        'active-channels-stat': stats.activeChannels || 0
    };

    Object.entries(elements).forEach(([id, value]) => {
        const el = document.getElementById(id);
        if (el) el.textContent = value;
    });

    // Watcher status
    const watcherElement = document.getElementById('watcher-status');
    if (watcherElement) {
        watcherElement.textContent = stats.watcherEnabled ? 'Enabled' : 'Disabled';
    }

    // Connection efficiency ratio (clients:upstream)
    const connectionEfficiencyEl = document.getElementById('connection-efficiency');
    if (connectionEfficiencyEl) {
        const clients = stats.connectedClients || 0;
        const upstream = stats.upstreamConnections || 0;
        connectionEfficiencyEl.textContent = upstream > 0
            ? `${(clients / upstream).toFixed(1)}:1`
            : 'N/A';
    }

    // Average clients per active stream
    const avgClientsEl = document.getElementById('avg-clients-per-stream');
    if (avgClientsEl) {
        avgClientsEl.textContent = stats.avgClientsPerStream > 0
            ? stats.avgClientsPerStream.toFixed(1)
            : 'N/A';
    }
}