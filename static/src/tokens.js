/**
 * Fetches all API tokens from the server and renders them
 * into the tokens container.
 * @returns {Promise<void>}
 */
async function loadTokens() {
    try {
        const tokens = await apiCall('/api/auth/tokens');
        const permissions = await apiCall('/api/auth/permissions');
        renderTokens(tokens, permissions);
    } catch (error) {
        document.getElementById('tokens-container').innerHTML =
            '<div class="bg-orange-900/20 border border-orange-600 text-orange-100 px-4 py-3 rounded">Failed to load API tokens</div>';
    }
}

/**
 * Renders API token cards into the tokens container.
 * @param {Array<Object>} tokens - Token objects from API
 * @param {Object} permissions - Permission constants from API
 */
function renderTokens(tokens, permissions) {
    const container = document.getElementById('tokens-container');

    if (!tokens || tokens.length === 0) {
        container.innerHTML = '<div class="bg-orange-900/20 border border-orange-600 text-orange-100 px-4 py-3 rounded">No API tokens configured</div>';
        return;
    }

    container.innerHTML = tokens.map(token => `
        <div class="source-item">
            <div class="flex justify-between items-center mb-3">
                <div class="flex-1">
                    <h4 class="text-lg font-semibold mb-1">${escapeHtml(token.name)}</h4>
                    <div class="text-gray-400 text-sm">Permissions: ${getPermissionLabels(token.permissions, permissions)}</div>
                </div>
            </div>
            <div class="mt-4 pt-4 border-t border-kptv-border flex gap-2">
                <button class="px-3 py-1 bg-red-700 hover:bg-red-600 rounded text-sm transition-colors flex items-center space-x-1"
                    onclick="deleteToken(${token.id})">
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
 * Returns a comma-separated list of permission labels for a bitmask.
 * @param {number} bitmask - Permission bitmask
 * @param {Object} permissions - Permission constants from API
 * @returns {string} Comma-separated permission labels
 */
function getPermissionLabels(bitmask, permissions) {
    if (bitmask === permissions.all) return 'Full Access';

    const labels = [];
    if (bitmask & permissions.read)        labels.push('Read');
    if (bitmask & permissions.configWrite) labels.push('Config');
    if (bitmask & permissions.restart)     labels.push('Restart');
    if (bitmask & permissions.streams)     labels.push('Streams');
    if (bitmask & permissions.logs)        labels.push('Logs');
    if (bitmask & permissions.xcAccounts)  labels.push('XC Accounts');
    if (bitmask & permissions.epgs)        labels.push('EPGs');
    if (bitmask & permissions.sd)          labels.push('SD');

    return labels.length > 0 ? labels.join(', ') : 'None';
}

/**
 * Opens the add token modal.
 */
function showAddTokenModal() {
    document.getElementById('token-modal-title').textContent = 'Add API Token';
    document.getElementById('token-name').value = '';
    // uncheck all permissions
    document.querySelectorAll('.token-perm-checkbox').forEach(cb => cb.checked = false);
    showModal('token-modal');
}

/**
 * Saves a new API token, displays the raw token once, then reloads.
 * @returns {Promise<void>}
 */
async function saveToken() {
    const name = document.getElementById('token-name').value.trim();
    if (!name) {
        showNotification('Token name is required', 'danger');
        return;
    }

    let permissions = 0;
    document.querySelectorAll('.token-perm-checkbox:checked').forEach(cb => {
        permissions |= parseInt(cb.value);
    });

    try {
        const result = await apiCall('/api/auth/tokens', {
            method: 'POST',
            body: JSON.stringify({ name, permissions })
        });

        hideModal('token-modal');

        // Show the raw token once — it cannot be retrieved again
        document.getElementById('raw-token-value').textContent = result.token;
        showModal('token-reveal-modal');

        loadTokens();
    } catch (error) {
        showNotification('Failed to create token', 'danger');
    }
}

/**
 * Deletes an API token by ID after confirmation.
 * @param {number} id - Token ID to delete
 * @returns {Promise<void>}
 */
async function deleteToken(id) {
    if (!confirm('Are you sure you want to delete this token? This cannot be undone.')) return;

    try {
        await apiCall('/api/auth/tokens', {
            method: 'DELETE',
            body: JSON.stringify({ id })
        });
        showNotification('Token deleted successfully', 'success');
        loadTokens();
    } catch (error) {
        showNotification('Failed to delete token', 'danger');
    }
}