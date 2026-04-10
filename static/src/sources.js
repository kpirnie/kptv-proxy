/**
 * Fetches all configured sources from the API and renders
 * them into the sources container.
 * @returns {Promise<void>}
 */
async function loadSources() {
    try {
        const config = await apiCall('/api/config');
        renderSources(config.sources || []);
    } catch (error) {
        document.getElementById('sources-container').innerHTML =
            '<div class="bg-orange-900/20 border border-orange-600 text-orange-100 px-4 py-3 rounded">Failed to load sources</div>';
    }
}

/**
 * Renders source cards into the sources container, showing connection
 * settings, optional headers, and edit/delete action buttons.
 * @param {Array<Object>} sources - Source config objects from API
 */
function renderSources(sources) {
    const container = document.getElementById('sources-container');

    if (sources.length === 0) {
        container.innerHTML = '<div class="bg-orange-900/20 border border-orange-600 text-orange-100 px-4 py-3 rounded">No sources configured</div>';
        return;
    }

    container.innerHTML = sources.map((source, index) => `
        <div class="source-item">
            <div class="flex justify-between items-center mb-3">
                <div class="flex-1">
                    <h4 class="text-lg font-semibold mb-1">${escapeHtml(source.name)}</h4>
                    <div class="text-gray-400 text-sm">${obfuscateUrl(source.url)}</div>
                </div>
                <div class="flex items-center">
                    <span class="status-indicator status-active"></span>
                    <span class="text-sm text-gray-400">Order: ${source.order}</span>
                </div>
            </div>
            <div class="grid grid-cols-2 sm:grid-cols-4 gap-3 mb-3">
                <div class="text-sm">
                    <div class="text-gray-400">Max Connections</div>
                    <div>${source.maxConnections}</div>
                </div>
                <div class="text-sm">
                    <div class="text-gray-400">Timeout</div>
                    <div>${source.maxStreamTimeout}</div>
                </div>
                <div class="text-sm">
                    <div class="text-gray-400">Max Retries</div>
                    <div>${source.maxRetries}</div>
                </div>
                <div class="text-sm">
                    <div class="text-gray-400">Min Data Size</div>
                    <div>${source.minDataSize} KB</div>
                </div>
            </div>
            ${source.userAgent ? `
                <div class="mb-2">
                    <div class="text-sm text-gray-400">User Agent</div>
                    <div class="text-sm text-truncate">${escapeHtml(source.userAgent)}</div>
                </div>
            ` : ''}
            ${source.reqOrigin ? `
                <div class="mb-2">
                    <div class="text-sm text-gray-400">Origin</div>
                    <div class="text-sm text-truncate">${escapeHtml(source.reqOrigin)}</div>
                </div>
            ` : ''}
            ${source.reqReferrer ? `
                <div class="mb-2">
                    <div class="text-sm text-gray-400">Referrer</div>
                    <div class="text-sm text-truncate">${escapeHtml(source.reqReferrer)}</div>
                </div>
            ` : ''}
            <div class="mt-4 pt-4 border-t border-kptv-border flex gap-2">
                <button class="px-3 py-1 bg-kptv-blue hover:bg-kptv-blue-light rounded text-sm transition-colors flex items-center space-x-1"
                    onclick="editSource(${index})">
                    <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z"></path>
                    </svg>
                    <span>Edit</span>
                </button>
                <button class="px-3 py-1 bg-red-700 hover:bg-red-600 rounded text-sm transition-colors flex items-center space-x-1"
                    onclick="deleteSource(${index})">
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
 * Opens the source modal for adding a new source,
 * clearing the form and setting the modal title.
 */
function showSourceModal(sourceIndex = null) {
    const title = document.getElementById('source-modal-title');

    if (sourceIndex !== null) {
        title.textContent = 'Edit Source';
        if (adminConfig && adminConfig.sources && adminConfig.sources[sourceIndex]) {
            populateSourceForm(adminConfig.sources[sourceIndex], sourceIndex);
        } else {
            showNotification('Config not loaded yet', 'warning');
            loadGlobalSettings();
        }
    } else {
        title.textContent = 'Add Source';
        clearSourceForm();
    }

    showModal('source-modal');
}

/**
 * Populates the source modal form fields with values from an existing source config.
 * @param {Object} source - Source config object to populate from
 * @param {number} index - Index of the source in the config array
 */
function populateSourceForm(source, index) {
    document.getElementById('source-index').value = index;
    document.getElementById('source-name').value = source.name || '';
    document.getElementById('source-url').value = source.url || '';
    document.getElementById('source-username').value = source.username || '';
    document.getElementById('source-password').value = source.password || '';
    document.getElementById('source-order').value = source.order || 1;
    document.getElementById('source-max-connections').value = source.maxConnections || 5;
    document.getElementById('source-max-stream-timeout').value = source.maxStreamTimeout || '10s';
    document.getElementById('source-retry-delay').value = source.retryDelay || '5s';
    document.getElementById('source-max-retries').value = source.maxRetries || 3;
    document.getElementById('source-max-failures').value = source.maxFailuresBeforeBlock || 5;
    document.getElementById('source-min-data-size').value = source.minDataSize || 2;
    document.getElementById('source-user-agent').value = source.userAgent || '';
    document.getElementById('source-origin').value = source.reqOrigin || '';
    document.getElementById('source-referrer').value = source.reqReferrer || '';
    document.getElementById('source-live-include-regex').value = source.liveIncludeRegex || '';
    document.getElementById('source-live-exclude-regex').value = source.liveExcludeRegex || '';
    document.getElementById('source-series-include-regex').value = source.seriesIncludeRegex || '';
    document.getElementById('source-series-exclude-regex').value = source.seriesExcludeRegex || '';
    document.getElementById('source-vod-include-regex').value = source.vodIncludeRegex || '';
    document.getElementById('source-vod-exclude-regex').value = source.vodExcludeRegex || '';
}

/**
 * Resets all source modal form fields to their default values.
 */
function clearSourceForm() {
    document.getElementById('source-index').value = '';
    document.getElementById('source-form').reset();
    document.getElementById('source-username').value = '';
    document.getElementById('source-password').value = '';
    document.getElementById('source-order').value = 1;
    document.getElementById('source-max-connections').value = 5;
    document.getElementById('source-max-stream-timeout').value = '10s';
    document.getElementById('source-retry-delay').value = '5s';
    document.getElementById('source-max-retries').value = 3;
    document.getElementById('source-max-failures').value = 5;
    document.getElementById('source-min-data-size').value = 2;
    document.getElementById('source-live-include-regex').value = '';
    document.getElementById('source-live-exclude-regex').value = '';
    document.getElementById('source-series-include-regex').value = '';
    document.getElementById('source-series-exclude-regex').value = '';
    document.getElementById('source-vod-include-regex').value = '';
    document.getElementById('source-vod-exclude-regex').value = '';
}

/**
 * Reads the source modal form, validates required fields, and saves
 * the source to the config via the API. Triggers a restart after save.
 * @returns {Promise<void>}
 */
async function saveSource() {
    try {
        const index = document.getElementById('source-index').value;
        const source = {
            name: document.getElementById('source-name').value,
            url: document.getElementById('source-url').value,
            username: document.getElementById('source-username').value || '',
            password: document.getElementById('source-password').value || '',
            order: parseInt(document.getElementById('source-order').value) || 1,
            maxConnections: parseInt(document.getElementById('source-max-connections').value) || 5,
            maxStreamTimeout: document.getElementById('source-max-stream-timeout').value || '30s',
            retryDelay: document.getElementById('source-retry-delay').value || '5s',
            maxRetries: parseInt(document.getElementById('source-max-retries').value) || 3,
            maxFailuresBeforeBlock: parseInt(document.getElementById('source-max-failures').value) || 5,
            minDataSize: parseInt(document.getElementById('source-min-data-size').value) || 2,
            userAgent: document.getElementById('source-user-agent').value || '',
            reqOrigin: document.getElementById('source-origin').value || '',
            reqReferrer: document.getElementById('source-referrer').value || '',
            liveIncludeRegex: document.getElementById('source-live-include-regex').value || '',
            liveExcludeRegex: document.getElementById('source-live-exclude-regex').value || '',
            seriesIncludeRegex: document.getElementById('source-series-include-regex').value || '',
            seriesExcludeRegex: document.getElementById('source-series-exclude-regex').value || '',
            vodIncludeRegex: document.getElementById('source-vod-include-regex').value || '',
            vodExcludeRegex: document.getElementById('source-vod-exclude-regex').value || ''
        };

        if (!source.name || !source.url) {
            showNotification('Name and URL are required', 'danger');
            return;
        }

        const config = await apiCall('/api/config');
        if (!config.sources) config.sources = [];
        if (index === '') {
            config.sources.push(source);
        } else {
            config.sources[parseInt(index)] = source;
        }
        delete config.xcOutputAccounts;
        delete config.epgs;
        delete config.sdAccounts;
        await apiCall('/api/config', { method: 'POST', body: JSON.stringify(config) });

        hideModal('source-modal');
        showNotification('Source saved successfully!', 'success');
        loadSources();
        setTimeout(() => restartService(), 500);
    } catch (error) {
        showNotification('Failed to save source: ' + error.message, 'danger');
    }
}

/**
 * Opens the source modal pre-populated with the source at the given index.
 * @param {number} index - Zero-based index of the source to edit
 */
function editSource(index) {
    showSourceModal(index);
}

/**
 * Deletes the source at the given index from config after user confirmation.
 * Triggers a restart after deletion.
 * @param {number} index - Zero-based index of the source to delete
 * @returns {Promise<void>}
 */
async function deleteSource(index) {
    if (!confirm('Are you sure you want to delete this source?')) return;

    try {
        const config = await apiCall('/api/config');
        config.sources.splice(index, 1);
        delete config.xcOutputAccounts;
        delete config.epgs;
        delete config.sdAccounts;
        await apiCall('/api/config', { method: 'POST', body: JSON.stringify(config) });
        showNotification('Source deleted successfully!', 'success');
        loadSources();
        setTimeout(() => restartService(), 1000);
    } catch (error) {
        showNotification('Failed to delete source', 'danger');
    }
}