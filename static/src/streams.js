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
 * Renders the stream selector modal content including the reorder controls
 * and all stream cards, then initializes pointer-driven sorting.
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
    currentStreamData = data;

    container.innerHTML = `
        <div class="mb-4 flex items-center gap-3">
            <span class="text-sm text-gray-400">Drag to reorder &mdash; saves on drop</span>
            <button id="reset-order-btn" type="button"
                class="px-3 py-1 text-xs font-semibold bg-kptv-gray-light border border-kptv-border hover:bg-kptv-border text-white rounded"
                onclick="resetStreamOrder()">
                Reset to Default
            </button>
            <span id="order-save-status" class="text-xs text-gray-500"></span>
        </div>
        <div id="streams-container">
            ${renderStreamCards(data)}
        </div>
    `;

    initStreamDragDrop();
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
                data-hash="${stream.hash}"
                style="touch-action: none;">
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
 * Initializes pointer-driven sorting on the streams container. The DOM is never
 * mutated mid-drag: the grabbed card follows the pointer and the rest are shifted
 * with transforms, so the geometry measured at drag start stays valid for the
 * whole gesture. The destination index is computed once, on release, from which
 * cards' midpoints the grabbed card's centre has passed.
 */
function initStreamDragDrop() {
    const container = document.getElementById('streams-container');
    if (!container) return;

    let cards = [];
    let rects = [];
    let dragCard = null;
    let startIndex = -1;
    let targetIndex = -1;
    let startY = 0;
    let outerHeight = 0;
    let active = false;

    const measure = () => {
        cards = Array.from(container.querySelectorAll('.stream-card'));
        rects = cards.map(c => {
            const r = c.getBoundingClientRect();
            return { top: r.top, height: r.height };
        });
        if (rects.length < 2) {
            outerHeight = rects[startIndex].height;
        } else if (startIndex < rects.length - 1) {
            outerHeight = rects[startIndex + 1].top - rects[startIndex].top;
        } else {
            outerHeight = rects[startIndex].top - rects[startIndex - 1].top;
        }
    };

    const paint = (dy) => {
        const centre = rects[startIndex].top + rects[startIndex].height / 2 + dy;

        targetIndex = 0;
        for (let i = 0; i < rects.length; i++) {
            if (i === startIndex) continue;
            if (centre > rects[i].top + rects[i].height / 2) targetIndex++;
        }

        cards.forEach((card, i) => {
            if (i === startIndex) {
                card.style.transform = `translateY(${dy}px)`;
                return;
            }
            let shift = 0;
            if (i > startIndex && i <= targetIndex) shift = -outerHeight;
            else if (i < startIndex && i >= targetIndex) shift = outerHeight;
            card.style.transform = shift ? `translateY(${shift}px)` : '';
        });
    };

    const clear = () => {
        cards.forEach(card => {
            card.style.transform = '';
            card.style.transition = '';
            card.style.zIndex = '';
            card.style.opacity = '';
        });
        container.style.userSelect = '';
    };

    container.addEventListener('pointerdown', (e) => {
        if (e.button !== 0) return;
        if (e.target.closest('a')) return;

        const card = e.target.closest('.stream-card');
        if (!card) return;

        dragCard = card;
        startY = e.clientY;
        startIndex = Array.from(container.querySelectorAll('.stream-card')).indexOf(card);
        targetIndex = startIndex;
        active = false;
        measure();
        container.setPointerCapture(e.pointerId);
    });

    container.addEventListener('pointermove', (e) => {
        if (!dragCard) return;

        const dy = e.clientY - startY;

        // Below the threshold this is still a click, not a drag.
        if (!active) {
            if (Math.abs(dy) < 4) return;
            active = true;
            container.style.userSelect = 'none';
            dragCard.style.zIndex = '10';
            dragCard.style.opacity = '0.85';
            cards.forEach((c, i) => {
                if (i !== startIndex) c.style.transition = 'transform 120ms ease';
            });
        }

        e.preventDefault();
        paint(dy);
    });

    const finish = (e) => {
        if (!dragCard) return;
        if (container.hasPointerCapture(e.pointerId)) container.releasePointerCapture(e.pointerId);

        const moved = active && targetIndex !== startIndex;
        const from = startIndex;
        const to = targetIndex;

        clear();
        dragCard = null;
        active = false;

        if (moved) commitStreamOrder(from, to);
    };

    container.addEventListener('pointerup', finish);
    container.addEventListener('pointercancel', finish);
}

/**
 * Re-renders just the stream cards, leaving the container element and its
 * delegated pointer listeners in place.
 * @param {Object} data - Channel streams response shape
 */
function renderStreamCardsInto(data) {
    const container = document.getElementById('streams-container');
    if (!container) return;
    container.innerHTML = renderStreamCards(data);
}

/**
 * Applies a completed drag to the local stream list and persists the result as
 * an ordered array of stream hashes. Positions come from the array itself, so
 * the outcome does not depend on the server's ordering when the request lands.
 * @param {number} from - Index the card was grabbed from
 * @param {number} to - Index the card was released at
 * @returns {Promise<void>}
 */
async function commitStreamOrder(from, to) {
    const statusEl = document.getElementById('order-save-status');
    if (statusEl) statusEl.textContent = 'Saving...';

    const streams = currentStreamData.streams.slice();
    streams.splice(to, 0, streams.splice(from, 1)[0]);
    streams.forEach((s, i) => { s.index = i; });

    currentStreamData = {
        ...currentStreamData,
        streams: streams,
        currentStreamIndex: 0,
        preferredStreamIndex: 0
    };
    renderStreamCardsInto(currentStreamData);

    try {
        await apiCall(`/api/channels/${encodeURIComponent(currentChannelName)}/order`, {
            method: 'POST',
            body: JSON.stringify({ streamOrder: streams.map(s => s.hash) })
        });
        if (statusEl) {
            statusEl.textContent = 'Saved';
            setTimeout(() => statusEl.textContent = '', 2000);
        }
    } catch (error) {
        if (statusEl) statusEl.textContent = 'Save failed';
        showNotification('Failed to save stream order: ' + error.message, 'danger');
        showStreamSelector(currentChannelName);
    }
}

/**
 * Clears any custom ordering for the current channel, returning it to the
 * globally configured sort, then reloads the selector.
 * @returns {Promise<void>}
 */
async function resetStreamOrder() {
    if (!confirm('Reset this channel to the default stream order?')) return;

    const statusEl = document.getElementById('order-save-status');
    if (statusEl) statusEl.textContent = 'Resetting...';

    try {
        await apiCall(`/api/channels/${encodeURIComponent(currentChannelName)}/order`, {
            method: 'DELETE'
        });
        showNotification(`Stream order reset for ${currentChannelName}`, 'success');
    } catch (error) {
        showNotification('Failed to reset stream order: ' + error.message, 'danger');
    }

    showStreamSelector(currentChannelName);
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