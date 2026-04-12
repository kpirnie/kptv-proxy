/**
 * Fetches all configured XC output accounts from the API and renders
 * them into the XC accounts container.
 * @returns {Promise<void>}
 */
async function loadXCAccounts() {
    try {
        const accounts = await apiCall('/api/xc-accounts');
        adminConfig = adminConfig || {};
        adminConfig.xcOutputAccounts = accounts;
        renderXCAccounts(accounts);
    } catch (error) {
        document.getElementById('xc-accounts-container').innerHTML =
            '<div class="bg-orange-900/20 border border-orange-600 text-orange-100 px-4 py-3 rounded">Failed to load XC accounts</div>';
    }
}

/**
 * Renders XC output account cards into the container, showing connection
 * limits, content type badges, quick-copy buttons, and edit/delete actions.
 * @param {Array<Object>} accounts - XC account config objects from API
 */
function renderXCAccounts(accounts) {
    const container = document.getElementById('xc-accounts-container');

    if (accounts.length === 0) {
        container.innerHTML = '<div class="bg-orange-900/20 border border-orange-600 text-orange-100 px-4 py-3 rounded">No XC output accounts configured</div>';
        return;
    }

    container.innerHTML = accounts.map((account, index) => `
        <div class="source-item">
            <div class="flex justify-between items-center mb-3">
                <div class="flex-1">
                    <h4 class="text-lg font-semibold mb-1">${escapeHtml(account.name)}</h4>
                    <div class="text-gray-400 text-sm">Max Connections: ${account.maxConnections}</div>
                </div>
                <div class="flex flex-col items-end gap-2 ml-4">
                    <div class="flex items-center gap-2">
                        <button class="font-bold flex items-center gap-1 px-2 py-1 bg-kptv-gray-light border border-kptv-border hover:bg-kptv-border rounded transition-colors text-gray-300 text-sm"
                            title="Xtreme Codes" disabled>XC</button>
                        <button class="flex items-center gap-1 px-2 py-1 bg-kptv-gray-light border border-kptv-border hover:bg-kptv-border rounded transition-colors text-gray-300 text-sm"
                            title="Copy base URL"
                            onclick="copyToClipboard('${(adminConfig?.baseURL || '').replace(/'/g, "\\'")}', 'URL copied')">
                            <svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13.828 10.172a4 4 0 00-5.656 0l-4 4a4 4 0 105.656 5.656l1.102-1.101m-.758-4.899a4 4 0 005.656 0l4-4a4 4 0 00-5.656-5.656l-1.1 1.1"></path>
                            </svg>
                            URL
                        </button>
                        <button class="flex items-center gap-1 px-2 py-1 bg-kptv-gray-light border border-kptv-border hover:bg-kptv-border rounded transition-colors text-gray-300 text-sm"
                            title="Copy username"
                            onclick="copyToClipboard('${escapeHtml(account.username).replace(/'/g, "\\'")}', 'Username copied')">
                            <svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M16 7a4 4 0 11-8 0 4 4 0 018 0zM12 14a7 7 0 00-7 7h14a7 7 0 00-7-7z"></path>
                            </svg>
                            User
                        </button>
                        <button class="flex items-center gap-1 px-2 py-1 bg-kptv-gray-light border border-kptv-border hover:bg-kptv-border rounded transition-colors text-gray-300 text-sm"
                            title="Copy password"
                            onclick="copyToClipboard('${account.password.replace(/'/g, "\\'")}', 'Password copied')">
                            <svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z"></path>
                            </svg>
                            Pass
                        </button>
                    </div>
                    <div class="flex items-center gap-2">
                        <button class="font-bold flex items-center gap-1 px-2 py-1 bg-kptv-gray-light border border-kptv-border hover:bg-kptv-border rounded transition-colors text-gray-300 text-sm"
                            title="M3U Playlists" disabled>M3U</button>
                        <button class="flex items-center gap-1 px-2 py-1 bg-kptv-gray-light border border-kptv-border hover:bg-kptv-border rounded transition-colors text-gray-300 text-sm"
                            title="Copy all playlist URL"
                            onclick="copyToClipboard('${(adminConfig?.baseURL || '').replace(/'/g, "\\'")}' + '/pl/${escapeHtml(account.username)}/${account.password}', 'All playlist URL copied')">
                            <svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13.828 10.172a4 4 0 00-5.656 0l-4 4a4 4 0 105.656 5.656l1.102-1.101m-.758-4.899a4 4 0 005.656 0l4-4a4 4 0 00-5.656-5.656l-1.1 1.1"></path>
                            </svg>
                            All
                        </button>
                        <button class="flex items-center gap-1 px-2 py-1 bg-kptv-gray-light border border-kptv-border hover:bg-kptv-border rounded transition-colors text-gray-300 text-sm"
                            title="Copy live playlist URL"
                            onclick="copyToClipboard('${(adminConfig?.baseURL || '').replace(/'/g, "\\'")}' + '/pl/${escapeHtml(account.username)}/${account.password}/live', 'Live playlist URL copied')">
                            <svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13.828 10.172a4 4 0 00-5.656 0l-4 4a4 4 0 105.656 5.656l1.102-1.101m-.758-4.899a4 4 0 005.656 0l4-4a4 4 0 00-5.656-5.656l-1.1 1.1"></path>
                            </svg>
                            Live
                        </button>
                        <button class="flex items-center gap-1 px-2 py-1 bg-kptv-gray-light border border-kptv-border hover:bg-kptv-border rounded transition-colors text-gray-300 text-sm"
                            title="Copy series playlist URL"
                            onclick="copyToClipboard('${(adminConfig?.baseURL || '').replace(/'/g, "\\'")}' + '/pl/${escapeHtml(account.username)}/${account.password}/series', 'Series playlist URL copied')">
                            <svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13.828 10.172a4 4 0 00-5.656 0l-4 4a4 4 0 105.656 5.656l1.102-1.101m-.758-4.899a4 4 0 005.656 0l4-4a4 4 0 00-5.656-5.656l-1.1 1.1"></path>
                            </svg>
                            Series
                        </button>
                        <button class="flex items-center gap-1 px-2 py-1 bg-kptv-gray-light border border-kptv-border hover:bg-kptv-border rounded transition-colors text-gray-300 text-sm"
                            title="Copy VOD playlist URL"
                            onclick="copyToClipboard('${(adminConfig?.baseURL || '').replace(/'/g, "\\'")}' + '/pl/${escapeHtml(account.username)}/${account.password}/vod', 'VOD playlist URL copied')">
                            <svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13.828 10.172a4 4 0 00-5.656 0l-4 4a4 4 0 105.656 5.656l1.102-1.101m-.758-4.899a4 4 0 005.656 0l4-4a4 4 0 00-5.656-5.656l-1.1 1.1"></path>
                            </svg>
                            VOD
                        </button>
                        <button class="flex items-center gap-1 px-2 py-1 bg-kptv-gray-light border border-kptv-border hover:bg-kptv-border rounded transition-colors text-gray-300 text-sm"
                            title="Copy EPG playlist URL"
                            onclick="copyToClipboard('${(adminConfig?.baseURL || '').replace(/'/g, "\\'")}' + '/epg/${escapeHtml(account.username)}/${account.password}', 'EPG playlist URL copied')">
                            <svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13.828 10.172a4 4 0 00-5.656 0l-4 4a4 4 0 105.656 5.656l1.102-1.101m-.758-4.899a4 4 0 005.656 0l4-4a4 4 0 00-5.656-5.656l-1.1 1.1"></path>
                            </svg>
                            EPG
                        </button>
                    </div>
                </div>
            </div>
            <div class="mt-1 flex gap-1">
                ${account.enableLive ? '<span class="px-2 py-0.5 bg-green-700 text-white text-xs rounded">Live</span>' : ''}
                ${account.enableSeries ? '<span class="px-2 py-0.5 bg-kptv-blue text-white text-xs rounded">Series</span>' : ''}
                ${account.enableVOD ? '<span class="px-2 py-0.5 bg-orange-700 text-white text-xs rounded">VOD</span>' : ''}
            </div>
            <div class="mt-4 pt-4 border-t border-kptv-border flex gap-2">
                <button class="px-3 py-1 bg-kptv-blue hover:bg-kptv-blue-light rounded text-sm transition-colors flex items-center space-x-1"
                    onclick="editXCAccount(${index})">
                    <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z"></path>
                    </svg>
                    <span>Edit</span>
                </button>
                <button class="px-3 py-1 bg-red-700 hover:bg-red-600 rounded text-sm transition-colors flex items-center space-x-1"
                    onclick="deleteXCAccount(${index})">
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
 * Opens the XC account modal for adding or editing an account.
 * @param {number|null} accountIndex - Index of account to edit, or null for new
 */
function showXCAccountModal(accountIndex = null) {
    const title = document.getElementById('xc-account-modal-title');

    if (accountIndex !== null) {
        title.textContent = 'Edit XC Account';
        if (adminConfig && adminConfig.xcOutputAccounts && adminConfig.xcOutputAccounts[accountIndex]) {
            populateXCAccountForm(adminConfig.xcOutputAccounts[accountIndex], accountIndex);
        } else {
            showNotification('Config not loaded yet', 'warning');
            loadGlobalSettings();
        }
    } else {
        title.textContent = 'Add XC Account';
        clearXCAccountForm();
    }

    showModal('xc-account-modal');
}

/**
 * Populates the XC account modal form with values from an existing account.
 * @param {Object} account - XC account config object to populate from
 * @param {number} index - Index of the account in the config array
 */
function populateXCAccountForm(account, index) {
    document.getElementById('xc-account-index').value = index;
    document.getElementById('xc-account-name').value = account.name || '';
    document.getElementById('xc-account-username').value = account.username || '';
    document.getElementById('xc-account-password').value = account.password || '';
    document.getElementById('xc-account-max-connections').value = account.maxConnections || 10;
    document.getElementById('xc-account-enable-live').checked = account.enableLive !== false;
    document.getElementById('xc-account-enable-series').checked = !!account.enableSeries;
    document.getElementById('xc-account-enable-vod').checked = !!account.enableVOD;
}

/**
 * Resets all XC account modal form fields to their default values.
 */
function clearXCAccountForm() {
    document.getElementById('xc-account-index').value = '';
    document.getElementById('xc-account-form').reset();
    document.getElementById('xc-account-max-connections').value = 10;
    document.getElementById('xc-account-enable-live').checked = true;
    document.getElementById('xc-account-enable-series').checked = false;
    document.getElementById('xc-account-enable-vod').checked = false;
}

/**
 * Reads the XC account modal form, validates required fields, and saves
 * the account to the config via the API. Triggers a restart after save.
 * @returns {Promise<void>}
 */
async function saveXCAccount() {
    try {
        const index = document.getElementById('xc-account-index').value;
        const account = {
            name: document.getElementById('xc-account-name').value,
            username: document.getElementById('xc-account-username').value,
            password: document.getElementById('xc-account-password').value,
            maxConnections: parseInt(document.getElementById('xc-account-max-connections').value) || 10,
            enableLive: document.getElementById('xc-account-enable-live').checked,
            enableSeries: document.getElementById('xc-account-enable-series').checked,
            enableVOD: document.getElementById('xc-account-enable-vod').checked
        };

        if (!account.name || !account.username || !account.password) {
            showNotification('Name, username, and password are required', 'danger');
            return;
        }

        if (index === '') {
            await apiCall('/api/xc-accounts', {
                method: 'POST',
                body: JSON.stringify(account)
            });
        } else {
            const id = adminConfig.xcOutputAccounts[parseInt(index)].id;
            await apiCall(`/api/xc-accounts/${id}`, {
                method: 'PUT',
                body: JSON.stringify(account)
            });
        }

        hideModal('xc-account-modal');
        showNotification('XC account saved successfully!', 'success');
        loadXCAccounts();
    } catch (error) {
        showNotification('Failed to save XC account: ' + error.message, 'danger');
    }
}

/**
 * Opens the XC account modal pre-populated with the account at the given index.
 * @param {number} index - Zero-based index of the account to edit
 */
function editXCAccount(index) {
    showXCAccountModal(index);
}

/**
 * Deletes the XC account at the given index from config after user confirmation.
 * Triggers a restart after deletion.
 * @param {number} index - Zero-based index of the account to delete
 * @returns {Promise<void>}
 */
async function deleteXCAccount(index) {
    if (!confirm('Are you sure you want to delete this XC account?')) return;

    try {
        const id = adminConfig.xcOutputAccounts[index].id;
        await apiCall(`/api/xc-accounts/${id}`, { method: 'DELETE' });
        showNotification('XC account deleted successfully!', 'success');
        loadXCAccounts();
    } catch (error) {
        showNotification('Failed to delete XC account', 'danger');
    }
}