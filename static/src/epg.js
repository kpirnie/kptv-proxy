/**
 * Fetches all configured EPG sources from the API and renders
 * them into the EPGs container.
 * @returns {Promise<void>}
 */
async function loadEPGs() {
    try {
        const config = await apiCall('/api/config');
        renderEPGs(config.epgs || []);
    } catch (error) {
        document.getElementById('epgs-container').innerHTML =
            '<div class="bg-orange-900/20 border border-orange-600 text-orange-100 px-4 py-3 rounded">Failed to load EPGs</div>';
    }
}

/**
 * Renders EPG source cards into the EPGs container, showing name,
 * obfuscated URL, order, and edit/delete action buttons.
 * @param {Array<Object>} epgs - EPG config objects from API
 */
function renderEPGs(epgs) {
    const container = document.getElementById('epgs-container');

    if (epgs.length === 0) {
        container.innerHTML = '<div class="bg-orange-900/20 border border-orange-600 text-orange-100 px-4 py-3 rounded">No EPGs configured</div>';
        return;
    }

    container.innerHTML = epgs.map((epg, index) => `
        <div class="source-item">
            <div class="flex justify-between items-center">
                <div class="flex-1">
                    <h4 class="text-lg font-semibold mb-1">${escapeHtml(epg.name)}</h4>
                    <div class="text-gray-400 text-sm">${obfuscateUrl(epg.url)}</div>
                </div>
                <div class="flex items-center">
                    <span class="status-indicator status-active"></span>
                    <span class="text-sm text-gray-400">Order: ${epg.order}</span>
                </div>
            </div>
            <div class="mt-4 pt-4 border-t border-kptv-border flex gap-2">
                <button class="px-3 py-1 bg-kptv-blue hover:bg-kptv-blue-light rounded text-sm transition-colors flex items-center space-x-1"
                    onclick="editEPG(${index})">
                    <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z"></path>
                    </svg>
                    <span>Edit</span>
                </button>
                <button class="px-3 py-1 bg-red-700 hover:bg-red-600 rounded text-sm transition-colors flex items-center space-x-1"
                    onclick="deleteEPG(${index})">
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
 * Opens the EPG modal for adding or editing an EPG source.
 * @param {number|null} epgIndex - Index of EPG to edit, or null for new
 */
function showEPGModal(epgIndex = null) {
    const title = document.getElementById('epg-modal-title');

    if (epgIndex !== null) {
        title.textContent = 'Edit EPG';
        if (adminConfig && adminConfig.epgs && adminConfig.epgs[epgIndex]) {
            populateEPGForm(adminConfig.epgs[epgIndex], epgIndex);
        }
    } else {
        title.textContent = 'Add EPG';
        clearEPGForm();
    }

    showModal('epg-modal');
}

/**
 * Populates the EPG modal form with values from an existing EPG config.
 * @param {Object} epg - EPG config object to populate from
 * @param {number} index - Index of the EPG in the config array
 */
function populateEPGForm(epg, index) {
    document.getElementById('epg-index').value = index;
    document.getElementById('epg-name').value = epg.name || '';
    document.getElementById('epg-url').value = epg.url || '';
    document.getElementById('epg-order').value = epg.order || 1;
}

/**
 * Resets all EPG modal form fields to their default values.
 */
function clearEPGForm() {
    document.getElementById('epg-index').value = '';
    document.getElementById('epg-form').reset();
    document.getElementById('epg-order').value = 1;
}

/**
 * Reads the EPG modal form, validates required fields, and saves
 * the EPG to the config via the API. Triggers a restart after save.
 * @returns {Promise<void>}
 */
async function saveEPG() {
    try {
        const index = document.getElementById('epg-index').value;
        const epg = {
            name: document.getElementById('epg-name').value,
            url: document.getElementById('epg-url').value,
            order: parseInt(document.getElementById('epg-order').value) || 1
        };

        if (!epg.name || !epg.url) {
            showNotification('Name and URL are required', 'danger');
            return;
        }

        const config = await apiCall('/api/config');
        if (!config.epgs) config.epgs = [];

        if (index === '') {
            config.epgs.push(epg);
        } else {
            config.epgs[parseInt(index)] = epg;
        }

        await apiCall('/api/config', { method: 'POST', body: JSON.stringify(config) });
        hideModal('epg-modal');
        showNotification('EPG saved successfully!', 'success');
        loadEPGs();
        setTimeout(() => restartService(), 500);
    } catch (error) {
        showNotification('Failed to save EPG: ' + error.message, 'danger');
    }
}

/**
 * Opens the EPG modal pre-populated with the EPG at the given index.
 * @param {number} index - Zero-based index of the EPG to edit
 */
function editEPG(index) {
    showEPGModal(index);
}

/**
 * Deletes the EPG at the given index from config after user confirmation.
 * Triggers a restart after deletion.
 * @param {number} index - Zero-based index of the EPG to delete
 * @returns {Promise<void>}
 */
async function deleteEPG(index) {
    if (!confirm('Are you sure you want to delete this EPG?')) return;

    try {
        const config = await apiCall('/api/config');
        config.epgs.splice(index, 1);
        await apiCall('/api/config', { method: 'POST', body: JSON.stringify(config) });
        showNotification('EPG deleted successfully!', 'success');
        loadEPGs();
        setTimeout(() => restartService(), 500);
    } catch (error) {
        showNotification('Failed to delete EPG', 'danger');
    }
}