/**
 * Fetches the current log buffer from the API and renders
 * entries into the log container with level-based styling.
 * @returns {Promise<void>}
 */
async function loadLogs() {
    try {
        const logs = await apiCall('/api/logs');
        allLogs = logs;
        renderLogs(logs);
    } catch (error) {
        document.getElementById('logs-container').innerHTML =
            '<div class="bg-red-900/20 border border-red-600 text-red-100 px-4 py-3 rounded">Failed to load logs</div>';
    }
}

/**
 * Renders an array of log entries into the log container,
 * applying level-specific border and background styling to each entry.
 * Auto-scrolls to the bottom after rendering.
 * @param {Array<{timestamp: string, level: string, message: string}>} logs - Log entries to render
 */
function renderLogs(logs) {
    const container = document.getElementById('logs-container');

    if (logs.length === 0) {
        container.innerHTML = '<div class="text-center text-gray-400">No logs available</div>';
        return;
    }

    container.innerHTML = logs.map(log => `
        <div class="log-entry log-${log.level}">
            <span class="text-gray-500 text-xs">${log.timestamp}</span>
            [${log.level.toUpperCase()}] ${escapeHtml(log.message)}
        </div>
    `).join('');

    container.scrollTop = container.scrollHeight;
}

/**
 * Filters the cached log buffer by severity level and re-renders
 * the log container with only matching entries.
 * @param {string} level - Log level to filter by, or 'all' for no filtering
 */
function filterLogs(level) {
    if (!allLogs) return;

    if (level === 'all') {
        renderLogs(allLogs);
    } else {
        renderLogs(allLogs.filter(log => log.level === level));
    }
}

/**
 * Clears all log entries from the server-side buffer after
 * user confirmation, then reloads the now-empty log view.
 * @returns {Promise<void>}
 */
async function clearLogs() {
    if (!confirm('Are you sure you want to clear all logs?')) return;

    try {
        await apiCall('/api/logs', { method: 'DELETE' });
        showNotification('Logs cleared successfully!', 'success');
        loadLogs();
    } catch (error) {
        showNotification('Failed to clear logs', 'danger');
    }
}