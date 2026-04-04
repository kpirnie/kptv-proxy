/**
 * Escapes HTML special characters to prevent XSS vulnerabilities
 * when inserting user-provided content into the DOM.
 * @param {string} text - Raw text to escape
 * @returns {string} HTML-safe string
 */
function escapeHtml(text) {
    if (!text) return '';
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}

/**
 * Obfuscates a URL for display, showing only protocol and hostname
 * while masking path and query string components.
 * @param {string} url - Full URL to obfuscate
 * @returns {string} Obfuscated URL string
 */
function obfuscateUrl(url) {
    if (!url) return '';
    try {
        const urlObj = new URL(url);
        let result = urlObj.protocol + '//' + urlObj.hostname;
        if (urlObj.pathname && urlObj.pathname !== '/') result += '/***';
        if (urlObj.search) result += '?***';
        return result;
    } catch {
        return '***OBFUSCATED***';
    }
}

/**
 * Formats a raw byte count into a human-readable string with appropriate
 * unit suffix (B, KB, MB, GB, TB).
 * @param {number} bytes - Raw byte count
 * @returns {string} Formatted size string
 */
function formatBytes(bytes) {
    if (bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
}

/**
 * Formats a bitrate value into a human-readable string with appropriate
 * unit suffix (bps, Kbps, Mbps).
 * @param {number} bitrate - Raw bitrate in bits per second
 * @returns {string} Formatted bitrate string
 */
function formatBitrate(bitrate) {
    if (bitrate < 1000) return bitrate + ' bps';
    if (bitrate < 1000000) return (bitrate / 1000).toFixed(1) + ' Kbps';
    return (bitrate / 1000000).toFixed(2) + ' Mbps';
}

/**
 * Copies text to the clipboard using the modern Clipboard API with
 * fallback to execCommand for older browsers.
 * @param {string} text - Text to copy
 * @param {string} successMessage - Notification message on success
 * @returns {Promise<void>}
 */
async function copyToClipboard(text, successMessage = 'Copied to clipboard') {
    try {
        if (navigator.clipboard && window.isSecureContext) {
            await navigator.clipboard.writeText(text);
        } else {
            const textarea = document.createElement('textarea');
            textarea.value = text;
            textarea.style.position = 'fixed';
            textarea.style.opacity = '0';
            document.body.appendChild(textarea);
            textarea.focus();
            textarea.select();
            const success = document.execCommand('copy');
            document.body.removeChild(textarea);
            if (!success) throw new Error('execCommand failed');
        }
        showNotification(successMessage, 'success');
    } catch (error) {
        showNotification('Failed to copy: ' + error.message, 'danger');
    }
}

/**
 * Generates a cryptographically random alphanumeric password of the
 * specified length using the Web Crypto API.
 * @param {number} length - Desired password length (default: 24)
 * @returns {string} Random password string
 */
function generatePassword(length = 24) {
    const chars = 'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789';
    return Array.from(crypto.getRandomValues(new Uint8Array(length)))
        .map(b => chars[b % chars.length])
        .join('');
}

/**
 * Displays a temporary toast notification in the top-right corner.
 * Auto-dismisses after 5 seconds.
 * @param {string} message - Notification message text
 * @param {string} type - Notification type: primary, success, warning, danger
 */
function showNotification(message, type = 'primary') {
    const container = document.getElementById('notification-container');
    const notification = document.createElement('div');
    const bgColors = {
        primary: 'bg-kptv-blue',
        success: 'bg-green-700',
        warning: 'bg-orange-700',
        danger: 'bg-red-700'
    };
    notification.className = `notification ${bgColors[type]} text-white px-4 py-3 rounded shadow-lg`;
    notification.textContent = message;
    container.appendChild(notification);
    setTimeout(() => notification.remove(), 5000);
}