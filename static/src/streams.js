/**
 * Fetches stream list for a channel and opens the stream selector modal.
 * Stores channel name and stream data at module level for use by
 * drag-and-drop and save operations.
 * @param {string} channelName - Channel name to load streams for
 * @returns {Promise<void>}
 */
async function showStreamSelector(channelName) {
    try {
        const data = await apiCall(`/api/channels/${encodeURIComponent(channelName)}/streams`);
        renderStreamSelector(data);
        showModal('stream-selector-modal');
    } catch (error) {
        showNotification('Failed to load streams for channel', 'danger');
    }
}

/**
 * Renders the stream selector modal content including the status indicator
 * and all stream cards, then initializes drag-and-drop sorting.
 * @param {Object} data - Channel streams response from API
 */
function renderStreamSelector(data) {
    document.getElementById('stream-selector-title').textContent = `Select Stream - ${data.channelName}`;

    const container = document.getElementById('stream-selector-content');

    if (data.streams.length === 0) {
        container.innerHTML = '<div class="bg-orange-900/20 border border-orange-600 text-orange-100 px-4 py-3 rounded">No streams found</div>';
        return;
    }

    currentChannelName = data.channelName;
    currentStreamData = data.streams;

    container.innerHTML = `
        <div class="mb-4 flex items-center gap-3">
            <span class="text-sm text-gray-400">Drag to reorder streams</span>
            <span id="order-save-status" class="text-xs text-gray-500"></span>
        </div>
        <div id="streams-container">
            ${renderStreamCards(data)}
        </div>
    `;

    initStreamDragDrop(data.channelName);
}

/**
 * Renders all stream cards for the stream selector modal.
 * Each card shows stream metadata, dead/current/preferred badges,
 * a drag handle, and action buttons for activate/kill/revive/copy.
 * @param {Object} data - Channel streams response from API
 * @returns {string} HTML string of rendered stream cards
 */
function renderStreamCards(data) {
    return data.streams.map((stream, displayIndex) => {
        const originalIndex = stream.index;
        const isDead = stream.attributes['dead'] === 'true';
        const deadReason = stream.attributes['dead_reason'] || 'unknown';
        const reasonText = deadReason === 'manual' ? 'Manually Killed' :
            deadReason === 'auto_blocked' ? 'Auto-Blocked (Too Many Failures)' : 'Dead';
        const cardClass = originalIndex === data.currentStreamIndex
            ? 'bg-kptv-blue/20 border-kptv-blue'
            : isDead ? 'bg-kptv-gray-light' : 'bg-kptv-gray';

        return `
            <div class="stream-card ${cardClass} ${isDead ? 'dead-stream' : ''} border border-kptv-border rounded p-3 mb-2 cursor-grab active:cursor-grabbing"
                data-original-index="${originalIndex}"
                data-display-index="${displayIndex}"
                draggable="true">
                <div class="flex justify-between items-center">
                    <div class="flex items-center flex-1">
                        <div class="flex items-center mr-3 text-gray-600 cursor-grab">
                            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 8h16M4 16h16"></path>
                            </svg>
                        </div>
                        <div class="flex-1">
                            <div class="font-bold flex items-center gap-2">
                                Stream ${displayIndex + 1}
                                ${originalIndex === data.preferredStreamIndex ? '<span class="px-2 py-0.5 bg-green-700 text-white text-xs rounded">Preferred</span>' : ''}
                                ${originalIndex === data.currentStreamIndex ? '<span class="px-2 py-0.5 bg-kptv-blue text-white text-xs rounded">Current</span>' : ''}
                                ${isDead ? `<span class="px-2 py-0.5 bg-red-700 text-white text-xs rounded" title="${reasonText}">DEAD</span>` : ''}
                            </div>
                            ${stream.attributes['tvg-name'] ? `
                                <div class="text-sm">Name: ${escapeHtml(stream.attributes['tvg-name'])}</div>
                            ` : ''}
                            ${stream.attributes['group-title'] ? `
                                <div class="text-sm">Group: ${escapeHtml(stream.attributes['group-title'])}</div>
                            ` : ''}
                            <div class="text-sm text-gray-400">
                                Source: ${stream.sourceName} (Order: ${stream.sourceOrder})
                            </div>
                        </div>
                    </div>
                    <div class="flex items-center gap-2 ml-4">
                        ${isDead
                            ? `<a href="#" class="text-green-500 hover:text-green-400" title="Make Live (${reasonText})"
                                onclick="reviveStream('${escapeHtml(data.channelName)}', ${originalIndex}); return false;">
                                <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"></path>
                                </svg>
                              </a>`
                            : `<a href="#" class="text-kptv-blue hover:text-kptv-blue-light" title="Activate Stream"
                                onclick="selectStream('${escapeHtml(data.channelName)}', ${originalIndex}); return false;">
                                <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M14.752 11.168l-3.197-2.132A1 1 0 0010 9.87v4.263a1 1 0 001.555.832l3.197-2.132a1 1 0 000-1.664z"></path>
                                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 12a9 9 0 11-18 0 9 9 0 0118 0z"></path>
                                </svg>
                              </a>
                              <a href="#" class="text-red-500 hover:text-red-400" title="Mark as Dead"
                                onclick="killStream('${escapeHtml(data.channelName)}', ${originalIndex}); return false;">
                                <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M18.364 18.364A9 9 0 005.636 5.636m12.728 12.728A9 9 0 015.636 5.636m12.728 12.728L5.636 5.636"></path>
                                </svg>
                              </a>`
                        }
                        <a href="#"
                            class="${data.obfuscated ? 'text-gray-600 cursor-not-allowed' : 'text-gray-400 hover:text-white'}"
                            title="${data.obfuscated ? 'URL obfuscated - cannot copy' : 'Copy stream URL'}"
                            onclick="${data.obfuscated ? 'return false;' : `copyToClipboard('${stream.url}', 'Stream URL copied'); return false;`}">
                            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z"></path>
                            </svg>
                        </a>
                    </div>
                </div>
            </div>
        `;
    }).join('');
}

/**
 * Initializes drag-and-drop sorting on the streams container.
 * Attaches dragstart, dragover, and dragend events to stream cards.
 * Auto-saves order to server after a successful drop with 600ms debounce.
 * @param {string} channelName - Channel name used when saving the new order
 */
function initStreamDragDrop(channelName) {
    const container = document.getElementById('streams-container');
    if (!container) return;

    let dragCard = null;
    let debounceTimer = null;

    container.addEventListener('dragstart', (e) => {
        dragCard = e.target.closest('.stream-card');
        if (!dragCard) return;
        dragCard.classList.add('opacity-50');
        e.dataTransfer.effectAllowed = 'move';
    });

    container.addEventListener('dragover', (e) => {
        e.preventDefault();
        e.dataTransfer.dropEffect = 'move';
        const target = e.target.closest('.stream-card');
        if (!target || target === dragCard) return;

        const rect = target.getBoundingClientRect();
        const midY = rect.top + rect.height / 2;
        if (e.clientY < midY) {
            container.insertBefore(dragCard, target);
        } else {
            container.insertBefore(dragCard, target.nextSibling);
        }
    });

    container.addEventListener('dragend', () => {
        if (!dragCard) return;
        dragCard.classList.remove('opacity-50');
        dragCard = null;

        // Update display indices after drop
        container.querySelectorAll('.stream-card').forEach((card, idx) => {
            card.setAttribute('data-display-index', idx);
        });

        // Debounced auto-save
        clearTimeout(debounceTimer);
        const statusEl = document.getElementById('order-save-status');
        if (statusEl) statusEl.textContent = 'Saving...';

        debounceTimer = setTimeout(async () => {
            await saveStreamOrder();
            if (statusEl) {
                statusEl.textContent = 'Saved';
                setTimeout(() => statusEl.textContent = '', 2000);
            }
        }, 600);
    });
}

/**
 * Saves the current stream card DOM order to the server.
 * Reads data-original-index from each card in current DOM order
 * and POSTs the resulting array to the channel order endpoint.
 * @returns {Promise<void>}
 */
async function saveStreamOrder() {
    const cards = document.querySelectorAll('.stream-card');
    if (!cards.length) return;

    const newOrder = Array.from(cards).map(card =>
        parseInt(card.getAttribute('data-original-index'))
    );

    try {
        await apiCall(`/api/channels/${encodeURIComponent(currentChannelName)}/order`, {
            method: 'POST',
            body: JSON.stringify({ streamOrder: newOrder })
        });
    } catch (error) {
        const statusEl = document.getElementById('order-save-status');
        if (statusEl) statusEl.textContent = 'Save failed';
        showNotification('Failed to save stream order: ' + error.message, 'danger');
    }
}

/**
 * Sends a request to switch the active stream for a channel to the given index.
 * Reloads the stream selector after a short delay to reflect the change.
 * @param {string} channelName - Channel to switch stream on
 * @param {number} streamIndex - Zero-based index of the stream to activate
 * @returns {Promise<void>}
 */
async function selectStream(channelName, streamIndex) {
    try {
        await apiCall(`/api/channels/${encodeURIComponent(channelName)}/stream`, {
            method: 'POST',
            body: JSON.stringify({ streamIndex })
        });
        showNotification(`Stream changed to index ${streamIndex} for ${channelName}`, 'success');
        setTimeout(() => showStreamSelector(channelName), 2000);
        loadActiveChannels();
    } catch (error) {
        showNotification('Failed to change stream', 'danger');
    }
}

/**
 * Marks a stream as dead on the server after user confirmation,
 * preventing it from being used in automatic failover.
 * Reloads the stream selector after completion.
 * @param {string} channelName - Channel containing the stream
 * @param {number} streamIndex - Zero-based index of the stream to kill
 * @returns {Promise<void>}
 */
async function killStream(channelName, streamIndex) {
    if (!confirm('Are you sure you want to mark this stream as dead? It will not be used for playback.')) return;

    try {
        await apiCall(`/api/channels/${encodeURIComponent(channelName)}/kill-stream`, {
            method: 'POST',
            body: JSON.stringify({ streamIndex })
        });
        showNotification(`Stream ${streamIndex + 1} marked as dead for ${channelName}`, 'warning');
        setTimeout(() => showStreamSelector(channelName), 1000);
    } catch (error) {
        showNotification('Failed to mark stream as dead', 'danger');
    }
}

/**
 * Removes a stream from the dead streams database, restoring it
 * to active rotation. Reloads the stream selector after completion.
 * @param {string} channelName - Channel containing the stream
 * @param {number} streamIndex - Zero-based index of the stream to revive
 * @returns {Promise<void>}
 */
async function reviveStream(channelName, streamIndex) {
    try {
        await apiCall(`/api/channels/${encodeURIComponent(channelName)}/revive-stream`, {
            method: 'POST',
            body: JSON.stringify({ streamIndex })
        });
        showNotification(`Stream ${streamIndex + 1} revived for ${channelName}`, 'success');
        setTimeout(() => showStreamSelector(channelName), 1000);
    } catch (error) {
        showNotification('Failed to revive stream', 'danger');
    }
}