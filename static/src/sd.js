/**
 * Fetches all configured Schedules Direct accounts from the API and renders
 * them into the SD accounts container.
 * @returns {Promise<void>}
 */
async function loadSDAccounts() {
    try {
        const accounts = await apiCall('/api/sd-accounts');
        adminConfig = adminConfig || {};
        adminConfig.sdAccounts = accounts;
        renderSDAccounts(accounts || []);
    } catch (error) {
        document.getElementById('sd-accounts-container').innerHTML =
            '<div class="bg-orange-900/20 border border-orange-600 text-orange-100 px-4 py-3 rounded">Failed to load SD accounts</div>';
    }
}

/**
 * Renders Schedules Direct account cards into the container, showing
 * username, lineup count, enabled status, and edit/delete action buttons.
 * @param {Array<Object>} accounts - SD account config objects from API
 */
function renderSDAccounts(accounts) {
    const container = document.getElementById('sd-accounts-container');

    if (!accounts || accounts.length === 0) {
        container.innerHTML = '<div class="bg-orange-900/20 border border-orange-600 text-orange-100 px-4 py-3 rounded">No Schedules Direct accounts configured</div>';
        return;
    }

    container.innerHTML = accounts.map((account, index) => `
        <div class="source-item bg-gradient-to-br from-green-900/30 to-kptv-gray border-green-800">
            <div class="flex justify-between items-center mb-2">
                <div class="flex-1">
                    <h4 class="text-lg font-semibold mb-1">${escapeHtml(account.name)}</h4>
                    <div class="text-gray-400 text-sm">Username: ${escapeHtml(account.username)}</div>
                    <div class="text-gray-400 text-sm">
                        Lineups: ${account.selectedLineups && account.selectedLineups.length > 0
                            ? account.selectedLineups.length + ' selected'
                            : 'All'}
                    </div>
                </div>
                <div class="flex items-center gap-2 ml-4">
                    <span class="px-2 py-0.5 ${account.enabled ? 'bg-green-700' : 'bg-gray-600'} text-white text-xs rounded">
                        ${account.enabled ? 'Enabled' : 'Disabled'}
                    </span>
                </div>
            </div>
            <div class="mt-4 pt-4 border-t border-kptv-border flex gap-2">
                <button class="px-3 py-1 bg-kptv-blue hover:bg-kptv-blue-light rounded text-sm transition-colors flex items-center space-x-1"
                    onclick="editSDAccount(${index})">
                    <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
                            d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z"></path>
                    </svg>
                    <span>Edit</span>
                </button>
                <button class="px-3 py-1 bg-red-700 hover:bg-red-600 rounded text-sm transition-colors flex items-center space-x-1"
                    onclick="deleteSDAccount(${index})">
                    <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
                            d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16"></path>
                    </svg>
                    <span>Delete</span>
                </button>
            </div>
        </div>
    `).join('');
}

/**
 * Resets the SD account modal form to its initial state,
 * clearing all fields and returning to step 1.
 */
function clearSDForm() {
    document.getElementById('sd-account-index').value = '';
    document.getElementById('sd-account-name').value = '';
    document.getElementById('sd-account-username').value = '';
    document.getElementById('sd-account-password').value = '';
    document.getElementById('sd-account-enabled').checked = true;
    document.getElementById('sd-discover-error').classList.add('hidden');
    document.getElementById('sd-discover-error').textContent = '';
    document.getElementById('sd-lineups-list').innerHTML = '';
    document.getElementById('sd-days-to-fetch').value = 7;
    sdShowStep(1);
}

/**
 * Toggles visibility between the credentials step and lineup selection step
 * of the SD account modal, also controlling the save button visibility.
 * @param {number} step - Step number to show (1 = credentials, 2 = lineups)
 */
function sdShowStep(step) {
    document.getElementById('sd-step-1').classList.toggle('hidden', step !== 1);
    document.getElementById('sd-step-2').classList.toggle('hidden', step !== 2);
    document.getElementById('save-sd-account-btn').classList.toggle('hidden', step !== 2);
}

/**
 * Authenticates to Schedules Direct with the entered credentials and
 * fetches available lineups, advancing the modal to the lineup selection step.
 * Shows an inline error message on failure without closing the modal.
 * @returns {Promise<void>}
 */
async function discoverSDLineups() {
    const username = document.getElementById('sd-account-username').value.trim();
    const password = document.getElementById('sd-account-password').value.trim();
    const errorEl = document.getElementById('sd-discover-error');

    errorEl.classList.add('hidden');
    errorEl.textContent = '';

    if (!username || !password) {
        errorEl.textContent = 'Username and password are required.';
        errorEl.classList.remove('hidden');
        return;
    }

    const btn = document.getElementById('sd-discover-btn');
    btn.disabled = true;
    btn.querySelector('span').textContent = 'Connecting...';

    try {
        const result = await apiCall('/api/sd/discover', {
            method: 'POST',
            body: JSON.stringify({ username, password })
        });

        if (!result.success) throw new Error(result.error || 'Discovery failed');

        // Pre-select lineups already saved for this account if editing
        const index = document.getElementById('sd-account-index').value;
        let savedLineups = [];
        if (index !== '' && adminConfig?.sdAccounts?.[parseInt(index)]) {
            savedLineups = adminConfig.sdAccounts[parseInt(index)].selectedLineups || [];
        }

        renderLineupCheckboxes(result.lineups, savedLineups);
        sdShowStep(2);
    } catch (error) {
        errorEl.textContent = error.message || 'Failed to connect to Schedules Direct.';
        errorEl.classList.remove('hidden');
    } finally {
        btn.disabled = false;
        btn.querySelector('span').textContent = 'Connect & Discover Lineups';
    }
}

/**
 * Renders lineup checkboxes into the lineup selection list,
 * pre-checking lineups that were previously selected.
 * @param {Array<Object>} lineups - Lineup objects from the SD API
 * @param {Array<string>} selectedIDs - Previously selected lineup IDs to pre-check
 */
function renderLineupCheckboxes(lineups, selectedIDs = []) {
    const container = document.getElementById('sd-lineups-list');

    if (!lineups || lineups.length === 0) {
        container.innerHTML = '<div class="text-gray-400 text-sm p-2">No lineups found on this account.</div>';
        return;
    }

    const selectedSet = new Set(selectedIDs);

    container.innerHTML = lineups.map(lineup => `
        <label class="flex items-start gap-2 p-2 hover:bg-kptv-gray rounded cursor-pointer">
            <input type="checkbox"
                class="mt-0.5 sd-lineup-checkbox"
                value="${escapeHtml(lineup.lineup || lineup.lineupID)}"
                ${selectedSet.size === 0 || selectedSet.has(lineup.lineup || lineup.lineupID) ? 'checked' : ''}>
            <div class="flex-1">
                <div class="text-sm font-medium">${escapeHtml(lineup.name)}</div>
                ${lineup.transport ? `<div class="text-xs text-gray-400">${escapeHtml(lineup.transport)}</div>` : ''}
                <div class="text-xs text-gray-500">${escapeHtml(lineup.lineup || lineup.lineupID)}</div>
            </div>
        </label>
    `).join('');
}

/**
 * Opens the SD account modal for adding or editing an account.
 * If editing, pre-populates the form and renders existing lineups on step 2.
 * @param {number|null} accountIndex - Index of account to edit, or null for new
 */
function showSDAccountModal(accountIndex = null) {
    document.getElementById('sd-account-modal-title').textContent =
        accountIndex !== null ? 'Edit Schedules Direct Account' : 'Add Schedules Direct Account';

    clearSDForm();

    if (accountIndex !== null && adminConfig?.sdAccounts?.[accountIndex]) {
        const account = adminConfig.sdAccounts[accountIndex];
        document.getElementById('sd-account-index').value = accountIndex;
        document.getElementById('sd-account-name').value = account.name || '';
        document.getElementById('sd-account-username').value = account.username || '';
        document.getElementById('sd-account-password').value = account.password || '';
        document.getElementById('sd-account-enabled').checked = account.enabled !== false;
        document.getElementById('sd-days-to-fetch').value = account.daysToFetch || 7;

        // If lineups are already saved, show them on step 2 without re-discovering
        if (account.selectedLineups && account.selectedLineups.length > 0) {
            renderLineupCheckboxes(
                account.selectedLineups.map(id => ({ lineup: id, name: id, transport: '' })),
                account.selectedLineups
            );
            sdShowStep(2);
        }
    }

    showModal('sd-account-modal');
}

/**
 * Reads the SD account modal form and saves the account to config via the API.
 * Collects selected lineup IDs from checkboxes before saving.
 * Triggers a restart after save.
 * @returns {Promise<void>}
 */
async function saveSDAccount() {
    const index = document.getElementById('sd-account-index').value;
    const name = document.getElementById('sd-account-name').value.trim();
    const username = document.getElementById('sd-account-username').value.trim();
    const password = document.getElementById('sd-account-password').value.trim();
    const enabled = document.getElementById('sd-account-enabled').checked;
    const daysToFetch = parseInt(document.getElementById('sd-days-to-fetch').value) || 7;

    if (!name || !username || !password) {
        showNotification('Name, username, and password are required', 'danger');
        return;
    }

    const selectedLineups = Array.from(
        document.querySelectorAll('.sd-lineup-checkbox:checked')
    ).map(cb => cb.value);

    const account = { name, username, password, enabled, daysToFetch, selectedLineups };

    try {
        if (index === '') {
            await apiCall('/api/sd-accounts', { method: 'POST', body: JSON.stringify(account) });
        } else {
            const id = adminConfig.sdAccounts[parseInt(index)].id;
            await apiCall(`/api/sd-accounts/${id}`, { method: 'PUT', body: JSON.stringify(account) });
        }

        hideModal('sd-account-modal');
        showNotification('SD account saved successfully!', 'success');
        loadSDAccounts();
        setTimeout(() => restartService(), 500);
    } catch (error) {
        showNotification('Failed to save SD account: ' + error.message, 'danger');
    }
}

/**
 * Opens the SD account modal pre-populated with the account at the given index.
 * @param {number} index - Zero-based index of the account to edit
 */
function editSDAccount(index) {
    showSDAccountModal(index);
}

/**
 * Deletes the SD account at the given index from config after user confirmation.
 * Triggers a restart after deletion.
 * @param {number} index - Zero-based index of the account to delete
 * @returns {Promise<void>}
 */
async function deleteSDAccount(index) {
    if (!confirm('Are you sure you want to delete this Schedules Direct account?')) return;

    try {
        const id = adminConfig.sdAccounts[index].id;
        await apiCall(`/api/sd-accounts/${id}`, { method: 'DELETE' });
        showNotification('SD account deleted successfully!', 'success');
        loadSDAccounts();
        setTimeout(() => restartService(), 500);
    } catch (error) {
        showNotification('Failed to delete SD account', 'danger');
    }
}