/**
 * Fetches all configured local sources from the API and renders
 * them into the local sources container.
 */
async function loadLocalSources() {
    try {
        const sources = await apiCall('/api/local-sources');
        allLocalSources = sources || [];
        renderLocalSources(allLocalSources);
        populateMetaSourceFilter();
    } catch (error) {
        document.getElementById('local-sources-container').innerHTML =
            '<div class="bg-orange-900/20 border border-orange-600 text-orange-100 px-4 py-3 rounded">Failed to load local sources</div>';
    }
}

/**
 * Renders local source cards showing path, media type, scan state,
 * and scan/edit/delete action buttons.
 * @param {Array<Object>} sources - Local source objects from API
 */
function renderLocalSources(sources) {
    const container = document.getElementById('local-sources-container');

    if (!sources || sources.length === 0) {
        container.innerHTML = '<div class="bg-orange-900/20 border border-orange-600 text-orange-100 px-4 py-3 rounded">No local sources configured</div>';
        return;
    }

    container.innerHTML = sources.map((source, index) => `
        <div class="source-item">
            <div class="flex justify-between items-center mb-3">
                <div class="flex-1">
                    <h4 class="text-lg font-semibold mb-1">${escapeHtml(source.name)}</h4>
                    <div class="text-gray-400 text-sm">${escapeHtml(source.path)}</div>
                </div>
                <div class="flex items-center">
                    <span class="status-indicator ${source.enabled ? 'status-active' : 'status-inactive'}"></span>
                    <span class="text-sm text-gray-400">Order: ${source.order}</span>
                </div>
            </div>
            <div class="grid grid-cols-2 sm:grid-cols-4 gap-3 mb-3">
                <div class="text-sm">
                    <div class="text-gray-400">Media Type</div>
                    <div>${escapeHtml(source.mediaType)}</div>
                </div>
                <div class="text-sm">
                    <div class="text-gray-400">Entries</div>
                    <div>${source.entryCount}</div>
                </div>
                <div class="text-sm">
                    <div class="text-gray-400">Last Scan</div>
                    <div>${formatLastScan(source.lastScan)}</div>
                </div>
                <div class="text-sm">
                    <div class="text-gray-400">Group Prefix</div>
                    <div>${escapeHtml(source.groupPrefix || '-')}</div>
                </div>
            </div>
            <div class="mt-4 pt-4 border-t border-kptv-border flex gap-2">
                <button class="px-3 py-1 bg-green-700 hover:bg-green-600 rounded text-sm transition-colors flex items-center space-x-1"
                    onclick="scanLocalSource(${source.id})">
                    <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"></path>
                    </svg>
                    <span>Scan</span>
                </button>
                <button class="px-3 py-1 bg-kptv-blue hover:bg-kptv-blue-light rounded text-sm transition-colors flex items-center space-x-1"
                    onclick="editLocalSource(${index})">
                    <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z"></path>
                    </svg>
                    <span>Edit</span>
                </button>
                <button class="px-3 py-1 bg-red-700 hover:bg-red-600 rounded text-sm transition-colors flex items-center space-x-1"
                    onclick="deleteLocalSource(${source.id})">
                    <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16"></path>
                    </svg>
                    <span>Delete</span>
                </button>
            </div>
        </div>
    `).join('');
}

/**
 * Formats a Unix scan timestamp for display.
 * @param {number} ts - Unix seconds, or 0 when never scanned
 * @returns {string} Localised timestamp or a dash
 */
function formatLastScan(ts) {
    if (!ts) return 'Never';
    return new Date(ts * 1000).toLocaleString();
}

/**
 * Opens the local source modal, pre-filled when editing an existing entry.
 * @param {Object|null} source - Local source to edit, or null to add
 */
function showLocalSourceModal(source = null) {
    document.getElementById('local-source-modal-title').textContent =
        source ? 'Edit Local Source' : 'Add Local Source';

    document.getElementById('local-source-id').value = source ? source.id : '';
    document.getElementById('local-source-name').value = source ? source.name : '';
    document.getElementById('local-source-path').value = source ? source.path : '';
    document.getElementById('local-source-media-type').value = source ? source.mediaType : 'movies';
    document.getElementById('local-source-group-prefix').value = source ? (source.groupPrefix || '') : '';
    document.getElementById('local-source-inc-regex').value = source ? (source.incRegex || '') : '';
    document.getElementById('local-source-exc-regex').value = source ? (source.excRegex || '') : '';
    document.getElementById('local-source-order').value = source ? source.order : 1;
    document.getElementById('local-source-enabled').checked = source ? source.enabled : true;

    showModal('local-source-modal');
}

/**
 * Opens the local source modal for the entry at the given render index.
 * @param {number} index - Index into allLocalSources
 */
function editLocalSource(index) {
    showLocalSourceModal(allLocalSources[index]);
}

/**
 * Reads the local source form and creates or updates the entry via the API.
 * @returns {Promise<void>}
 */
async function saveLocalSource() {
    const id = document.getElementById('local-source-id').value;

    const payload = {
        name: document.getElementById('local-source-name').value.trim(),
        path: document.getElementById('local-source-path').value.trim(),
        mediaType: document.getElementById('local-source-media-type').value,
        groupPrefix: document.getElementById('local-source-group-prefix').value.trim(),
        incRegex: document.getElementById('local-source-inc-regex').value.trim(),
        excRegex: document.getElementById('local-source-exc-regex').value.trim(),
        order: parseInt(document.getElementById('local-source-order').value) || 1,
        enabled: document.getElementById('local-source-enabled').checked
    };

    if (!payload.name || !payload.path) {
        showNotification('Name and path are required', 'danger');
        return;
    }

    try {
        await apiCall(id ? `/api/local-sources/${id}` : '/api/local-sources', {
            method: id ? 'PUT' : 'POST',
            body: JSON.stringify(payload)
        });

        hideModal('local-source-modal');
        showNotification(`Local source ${id ? 'updated' : 'created'} successfully!`, 'success');
        loadLocalSources();
    } catch (error) {
        showNotification('Failed to save local source: ' + error.message, 'danger');
    }
}

/**
 * Deletes a local source and its stored media entries after confirmation.
 * @param {number} id - Local source ID
 * @returns {Promise<void>}
 */
async function deleteLocalSource(id) {
    if (!confirm('Delete this local source and all of its scanned entries?')) return;

    try {
        await apiCall(`/api/local-sources/${id}`, { method: 'DELETE' });
        showNotification('Local source deleted', 'success');
        loadLocalSources();
    } catch (error) {
        showNotification('Failed to delete local source: ' + error.message, 'danger');
    }
}

/**
 * Runs a manual scan of a single local source.
 * @param {number} id - Local source ID
 * @returns {Promise<void>}
 */
async function scanLocalSource(id) {
    showLoadingOverlay('Scanning local source...');
    try {
        const result = await apiCall(`/api/local-sources/${id}/scan`, { method: 'POST' });
        hideLoadingOverlay();
        showNotification(`Scan complete: ${result.count} entries`, 'success');
        loadLocalSources();
    } catch (error) {
        hideLoadingOverlay();
        showNotification('Scan failed: ' + error.message, 'danger');
    }
}

/**
 * Runs a manual scan across every enabled local source.
 * @returns {Promise<void>}
 */
async function scanAllLocalSources() {
    showLoadingOverlay('Scanning all local sources...');
    try {
        const result = await apiCall('/api/local-sources/scan', { method: 'POST' });
        hideLoadingOverlay();
        showNotification(`Scan complete: ${result.count} entries`, 'success');
        loadLocalSources();
    } catch (error) {
        hideLoadingOverlay();
        showNotification('Scan failed: ' + error.message, 'danger');
    }
}