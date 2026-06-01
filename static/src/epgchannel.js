let currentEPGChannelName = null;

// module-level cache of channel -> epg_id mappings
let epgMappings = {};

/**
 * Fetches all channel EPG mappings into the module-level cache.
 * Called on page load and after any mapping change.
 * @returns {Promise<void>}
 */
async function loadEPGMappings() {
    try {
        epgMappings = await apiCall('/api/channels/epg-mappings');
    } catch (error) {
        epgMappings = {};
    }
}

/**
 * Returns true if the given channel has an EPG mapping assigned.
 * @param {string} channelName
 * @returns {boolean}
 */
function hasEPGMapping(channelName) {
    return !!epgMappings[channelName];
}

/**
 * Opens the EPG channel mapping modal for a channel, loading the
 * current mapping and initializing the search input.
 * @param {string} channelName - Channel to map EPG for
 * @returns {Promise<void>}
 */
async function showEPGChannelModal(channelName) {
    currentEPGChannelName = channelName;
    document.getElementById('epg-channel-modal-title').textContent = `EPG Mapping - ${channelName}`;
    document.getElementById('epg-channel-search').value = '';
    document.getElementById('epg-channel-results').innerHTML = '';
    document.getElementById('epg-channel-current').textContent = 'Loading current mapping...';

    try {
        const mapping = await apiCall(`/api/channels/${encodeURIComponent(channelName)}/epg`);
        renderCurrentEPGMapping(mapping);
    } catch (error) {
        document.getElementById('epg-channel-current').textContent = 'No mapping set';
    }

    showModal('epg-channel-modal');

    // wire up search with debounce after modal is shown
    const searchInput = document.getElementById('epg-channel-search');
    searchInput.oninput = debounceEPGSearch;
    searchInput.focus();

    document.getElementById('clear-epg-channel-btn').onclick = clearEPGChannelMapping;
}

/**
 * Renders the current EPG mapping status in the modal header area.
 * @param {Object} mapping - Mapping object with epg_id and epg_name
 */
function renderCurrentEPGMapping(mapping) {
    const el = document.getElementById('epg-channel-current');
    if (mapping.epg_id) {
        el.innerHTML = `Current mapping: <span class="text-white font-semibold">${escapeHtml(mapping.epg_name)}</span> <span class="text-gray-500">(${escapeHtml(mapping.epg_id)})</span>`;
    } else {
        el.textContent = 'No mapping set';
    }
}

let epgSearchTimer = null;

/**
 * Debounced handler for the EPG channel search input.
 * Waits 300ms after last keystroke before firing the search.
 */
function debounceEPGSearch() {
    clearTimeout(epgSearchTimer);
    epgSearchTimer = setTimeout(runEPGChannelSearch, 300);
}

/**
 * Fetches fuzzy search results from the EPG index and renders them.
 * @returns {Promise<void>}
 */
async function runEPGChannelSearch() {
    const q = document.getElementById('epg-channel-search').value.trim();
    const resultsEl = document.getElementById('epg-channel-results');

    if (!q) {
        resultsEl.innerHTML = '';
        return;
    }

    resultsEl.innerHTML = '<div class="text-sm text-gray-500">Searching...</div>';

    try {
        const results = await apiCall(`/api/epg/search?q=${encodeURIComponent(q)}`);
        renderEPGSearchResults(results);
    } catch (error) {
        resultsEl.innerHTML = '<div class="text-sm text-red-400">Search failed</div>';
    }
}

/**
 * Renders EPG search result rows. Each row shows the channel ID and
 * display names and is clickable to select that mapping.
 * @param {Array<Object>} results - EPGChannel entries from search API
 */
function renderEPGSearchResults(results) {
    const el = document.getElementById('epg-channel-results');

    if (!results || results.length === 0) {
        el.innerHTML = '<div class="text-sm text-gray-500">No results found</div>';
        return;
    }

    el.innerHTML = results.map(ch => {
        const names = (ch.DisplayNames || []).join(', ');
        return `
            <div class="flex justify-between items-center px-3 py-2 bg-kptv-gray-light border border-kptv-border rounded hover:border-kptv-blue cursor-pointer transition-colors"
                onclick="selectEPGChannel('${escapeHtml(ch.ID)}', '${escapeHtml((ch.DisplayNames || [''])[0])}')">
                <div>
                    <div class="text-sm font-semibold text-white">${escapeHtml(ch.ID)}</div>
                    <div class="text-xs text-gray-400">${escapeHtml(names)}</div>
                </div>
                <svg class="w-4 h-4 text-gray-500" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7"></path>
                </svg>
            </div>
        `;
    }).join('');
}

/**
 * Saves the selected EPG channel mapping for the current channel.
 * @param {string} epgID - XMLTV channel id value
 * @param {string} epgName - Primary display name
 * @returns {Promise<void>}
 */
async function selectEPGChannel(epgID, epgName) {
    try {
        await apiCall(`/api/channels/${encodeURIComponent(currentEPGChannelName)}/epg`, {
            method: 'POST',
            body: JSON.stringify({ epg_id: epgID, epg_name: epgName })
        });
        showNotification(`EPG mapped to ${epgName} for ${currentEPGChannelName}`, 'success');
        renderCurrentEPGMapping({ epg_id: epgID, epg_name: epgName });
        document.getElementById('epg-channel-search').value = '';
        document.getElementById('epg-channel-results').innerHTML = '';
    } catch (error) {
        showNotification('Failed to save EPG mapping', 'danger');
    }
}

/**
 * Clears the EPG channel mapping for the current channel after confirmation.
 * @returns {Promise<void>}
 */
async function clearEPGChannelMapping() {
    if (!confirm(`Clear EPG mapping for ${currentEPGChannelName}?`)) return;

    try {
        await apiCall(`/api/channels/${encodeURIComponent(currentEPGChannelName)}/epg`, {
            method: 'DELETE'
        });
        showNotification(`EPG mapping cleared for ${currentEPGChannelName}`, 'success');
        renderCurrentEPGMapping({ epg_id: '', epg_name: '' });
    } catch (error) {
        showNotification('Failed to clear EPG mapping', 'danger');
    }
}