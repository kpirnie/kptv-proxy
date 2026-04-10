/**
 * Module-level state shared across all admin modules.
 * Declared here so all modules in the concatenated bundle have access.
 */
let adminConfig = null;
let allChannels = null;
let allLogs = null;
let currentPage = 1;
let pageSize = 50;
let filteredChannels = null;
let currentChannelName = null;
let currentStreamData = null;
let refreshInterval = null;

/**
 * Performs an authenticated API call to the given endpoint,
 * returning parsed JSON on success or throwing on HTTP error.
 * Shows a notification and rethrows on failure.
 * @param {string} endpoint - API path to call
 * @param {Object} options - Fetch options (method, body, headers)
 * @returns {Promise<any>} Parsed JSON response
 */
async function apiCall(endpoint, options = {}) {
    try {
        const response = await fetch(endpoint, {
            headers: {
                'Content-Type': 'application/json',
                ...options.headers
            },
            ...options
        });

        if (!response.ok) {
            throw new Error(`HTTP ${response.status}: ${response.statusText}`);
        }

        return await response.json();
    } catch (error) {
        console.error('API call failed:', error);
        showNotification('API call failed: ' + error.message, 'danger');
        throw error;
    }
}

/**
 * Starts the 5-second auto-refresh interval, updating stats,
 * active channels, and the all-channels list on each tick.
 * Preserves the current search filter and page position across refreshes.
 */
function startAutoRefresh() {
    refreshInterval = setInterval(async () => {
        loadStats();
        loadActiveChannels();

        const searchInput = document.getElementById('channel-search');
        const currentSearch = searchInput ? searchInput.value.trim() : '';
        const savedPage = currentPage;

        await loadAllChannels();

        filteredChannels = currentSearch
            ? allChannels.filter(channel =>
                channel.name.toLowerCase().includes(currentSearch.toLowerCase()) ||
                (channel.group && channel.group.toLowerCase().includes(currentSearch.toLowerCase()))
            )
            : null;

        const totalPages = Math.ceil((filteredChannels || allChannels || []).length / pageSize);
        currentPage = (savedPage > totalPages && totalPages > 0) ? totalPages
            : (totalPages === 0 ? 1 : savedPage);

        renderCurrentPage();
    }, 5000);
}

/**
 * Stops the auto-refresh interval if it is currently running.
 */
function stopAutoRefresh() {
    if (refreshInterval) clearInterval(refreshInterval);
}

/**
 * Initializes the scroll-to-top button, showing it after
 * the user scrolls down 100px and hiding it when at the top.
 */
function initScrollToTop() {
    const scrollBtn = document.getElementById('scroll-to-top');
    if (!scrollBtn) return;

    window.addEventListener('scroll', () => {
        scrollBtn.classList.toggle('visible', window.scrollY > 100);
    });

    scrollBtn.addEventListener('click', (e) => {
        e.preventDefault();
        window.scrollTo({ top: 0, behavior: 'smooth' });
    });
}

/**
 * Fetches the current config from the API and populates the
 * global settings form with all field values.
 * Falls back to sensible defaults on error.
 * @returns {Promise<void>}
 */
async function loadGlobalSettings() {
    try {
        const config = await apiCall('/api/config');
        adminConfig = config;
        populateGlobalSettingsForm(config);
    } catch (error) {
        populateGlobalSettingsForm({
            baseURL: 'http://localhost:8080',
            bufferSizePerStream: 8,
            cacheEnabled: true,
            cacheDuration: '360m',
            importRefreshInterval: '12h',
            workerThreads: 4,
            debug: false,
            obfuscateUrls: true,
            sortField: 'tvg-type',
            sortDirection: 'asc',
            streamTimeout: '30s',
            maxConnectionsToApp: 100
        });
    }
}

/**
 * Populates all global settings form fields from a config object,
 * handling checkboxes, radio buttons, and FFmpeg argument arrays.
 * @param {Object} config - Config object from the API
 */
function populateGlobalSettingsForm(config) {
    const form = document.getElementById('global-settings-form');

    Object.keys(config).forEach(key => {
        const input = form.querySelector(`[name="${key}"]`);
        if (input) {
            if (input.type === 'checkbox') {
                input.checked = config[key];
            } else {
                input.value = config[key];
            }
        }
    });

    // Watcher checkbox
    const watcherCheckbox = form.querySelector('input[name="watcherEnabled"]');
    if (watcherCheckbox && config.watcherEnabled !== undefined) {
        watcherCheckbox.checked = config.watcherEnabled;
    }

    // Log level radio buttons
    if (config.logLevel) {
        form.querySelectorAll('input[name="logLevel"]').forEach(radio => {
            radio.checked = radio.value === config.logLevel.toUpperCase();
        });
    } else {
        const infoRadio = form.querySelector('input[name="logLevel"][value="INFO"]');
        if (infoRadio) infoRadio.checked = true;
    }

    // FFmpeg mode checkbox
    const ffmpegCheckbox = form.querySelector('input[name="ffmpegMode"]');
    if (ffmpegCheckbox && config.ffmpegMode !== undefined) {
        ffmpegCheckbox.checked = config.ffmpegMode;
    }

    // FFmpeg pre-input args (array -> space-separated string)
    const ffmpegPreInput = form.querySelector('input[name="ffmpegPreInput"]');
    if (ffmpegPreInput && config.ffmpegPreInput) {
        ffmpegPreInput.value = Array.isArray(config.ffmpegPreInput)
            ? config.ffmpegPreInput.join(' ')
            : config.ffmpegPreInput;
    }

    // FFmpeg pre-output args (array -> space-separated string)
    const ffmpegPreOutput = form.querySelector('input[name="ffmpegPreOutput"]');
    if (ffmpegPreOutput && config.ffmpegPreOutput) {
        ffmpegPreOutput.value = Array.isArray(config.ffmpegPreOutput)
            ? config.ffmpegPreOutput.join(' ')
            : config.ffmpegPreOutput;
    }
}

/**
 * Reads the global settings form, merges changes with the current
 * server config (preserving sources and other arrays), saves via the API,
 * and triggers a graceful restart to apply the new configuration.
 * @returns {Promise<void>}
 */
async function saveGlobalSettings() {
    const form = document.getElementById('global-settings-form');
    const formData = new FormData(form);
    const newConfig = {};

    for (let [key, value] of formData.entries()) {
        const input = form.querySelector(`[name="${key}"]`);
        if (input.type === 'checkbox') {
            newConfig[key] = input.checked;
        } else if (input.type === 'number') {
            newConfig[key] = parseInt(value) || 0;
        } else {
            newConfig[key] = value;
        }
    }

    // Ensure unchecked checkboxes are included as false
    form.querySelectorAll('input[type="checkbox"]').forEach(checkbox => {
        if (!formData.has(checkbox.name)) newConfig[checkbox.name] = false;
    });

    // Always force debug true (existing behaviour)
    newConfig.debug = true;

    // Log level from radio group
    if (!newConfig.logLevel) {
        const checkedLogLevel = form.querySelector('input[name="logLevel"]:checked');
        newConfig.logLevel = checkedLogLevel ? checkedLogLevel.value : 'INFO';
    }

    // FFmpeg mode
    const ffmpegModeCheckbox = form.querySelector('input[name="ffmpegMode"]');
    if (ffmpegModeCheckbox) newConfig.ffmpegMode = ffmpegModeCheckbox.checked;

    // FFmpeg args (space-separated string -> array)
    const ffmpegPreInput = form.querySelector('input[name="ffmpegPreInput"]');
    newConfig.ffmpegPreInput = (ffmpegPreInput && ffmpegPreInput.value)
        ? ffmpegPreInput.value.split(' ').filter(arg => arg.length > 0)
        : [];

    const ffmpegPreOutput = form.querySelector('input[name="ffmpegPreOutput"]');
    newConfig.ffmpegPreOutput = (ffmpegPreOutput && ffmpegPreOutput.value)
        ? ffmpegPreOutput.value.split(' ').filter(arg => arg.length > 0)
        : [];

    try {
        const currentConfig = await apiCall('/api/config');
        const mergedConfig = { ...currentConfig, ...newConfig };

        // Always preserve sources from the server copy
        if (currentConfig.sources) mergedConfig.sources = currentConfig.sources;

        delete mergedConfig.xcOutputAccounts;
        delete mergedConfig.epgs;
        delete mergedConfig.sdAccounts;
        await apiCall('/api/config', {
            method: 'POST',
            body: JSON.stringify(mergedConfig)
        });

        showNotification('Global settings saved successfully!', 'success');
        setTimeout(() => restartService(), 1000);
    } catch (error) {
        showNotification('Failed to save global settings: ' + error.message, 'danger');
    }
}

/**
 * Sends a graceful restart request to the server after user confirmation.
 * Stops auto-refresh during the restart and resumes it after completion.
 * @returns {Promise<void>}
 */
async function restartService() {
    if (!confirm('Are you sure you want to restart the KPTV Proxy service? This will temporarily interrupt all streams.')) return;

    showLoadingOverlay('Restarting KPTV Proxy...');
    stopAutoRefresh();

    try {
        const result = await apiCall('/api/restart', { method: 'POST' });
        hideLoadingOverlay();
        showNotification(result.message || 'Restart request sent successfully!', 'warning');
        startAutoRefresh();
        setTimeout(() => {
            loadGlobalSettings();
            loadSources();
            loadStats();
        }, 2000);
    } catch (error) {
        hideLoadingOverlay();
        showNotification('Failed to restart service', 'danger');
        startAutoRefresh();
    }
}

/**
 * Sends a request to toggle the stream watcher on or off,
 * reverting the checkbox state on failure.
 * @param {boolean} enable - Whether to enable or disable the watcher
 * @returns {Promise<void>}
 */
async function toggleWatcher(enable) {
    try {
        await apiCall('/api/watcher/toggle', {
            method: 'POST',
            body: JSON.stringify({ enabled: enable })
        });
        showNotification(`Stream watcher ${enable ? 'enabled' : 'disabled'} successfully!`, 'success');
        loadStats();
    } catch (error) {
        showNotification(`Failed to ${enable ? 'enable' : 'disable'} watcher: ` + error.message, 'danger');
        const checkbox = document.querySelector('input[name="watcherEnabled"]');
        if (checkbox) checkbox.checked = !enable;
        throw error;
    }
}

/**
 * Wires up all event listeners for the admin interface after the DOM is ready.
 * Connects stat cards, form submissions, buttons, search, pagination,
 * log controls, and modal triggers to their handler functions.
 */
function setupEventListeners() {

    // Stat card navigation shortcuts
    document.getElementById('total-channels').parentElement.addEventListener('click', () => switchTab(5));
    document.getElementById('total-sources').parentElement.addEventListener('click', () => switchTab(3));
    document.getElementById('total-epgs').parentElement.addEventListener('click', () => switchTab(4));

    // Global settings form
    document.getElementById('global-settings-form').addEventListener('submit', (e) => {
        e.preventDefault();
        saveGlobalSettings();
    });
    document.getElementById('load-global-settings').addEventListener('click', () => loadGlobalSettings());

    // Restart button
    document.getElementById('restart-btn').addEventListener('click', () => restartService());

    // Source buttons
    document.getElementById('add-source-btn').addEventListener('click', () => showSourceModal());
    document.getElementById('save-source-btn').addEventListener('click', () => saveSource());

    // EPG buttons
    document.getElementById('add-epg-btn').addEventListener('click', () => showEPGModal());
    document.getElementById('save-epg-btn').addEventListener('click', () => saveEPG());

    // Channel refresh buttons
    document.getElementById('refresh-channels').addEventListener('click', () => loadActiveChannels());
    document.getElementById('refresh-all-channels').addEventListener('click', () => {
        currentPage = 1;
        filteredChannels = null;
        document.getElementById('channel-search').value = '';
        loadAllChannels();
    });

    // Log controls
    document.getElementById('refresh-logs').addEventListener('click', () => loadLogs());
    document.getElementById('clear-logs').addEventListener('click', () => clearLogs());

    // Channel search
    document.getElementById('channel-search').addEventListener('input', (e) => {
        currentPage = 1;
        filterChannels(e.target.value);
    });

    // Log level filter
    document.getElementById('log-level').addEventListener('change', (e) => filterLogs(e.target.value));

    // Watcher toggle
    const watcherCheckbox = document.querySelector('input[name="watcherEnabled"]');
    if (watcherCheckbox) {
        watcherCheckbox.addEventListener('change', (e) => toggleWatcher(e.target.checked));
    }

    // Pagination controls
    document.getElementById('prev-page').addEventListener('click', (e) => { e.preventDefault(); previousPage(); });
    document.getElementById('next-page').addEventListener('click', (e) => { e.preventDefault(); nextPage(); });
    document.getElementById('first-page').addEventListener('click', (e) => { e.preventDefault(); goToFirstPage(); });
    document.getElementById('last-page').addEventListener('click', (e) => { e.preventDefault(); goToLastPage(); });
    document.getElementById('page-selector').addEventListener('change', (e) => {
        const page = parseInt(e.target.value);
        if (!isNaN(page)) goToPage(page);
    });

    // XC account buttons
    document.getElementById('add-xc-account-btn').addEventListener('click', () => showXCAccountModal());
    document.getElementById('save-xc-account-btn').addEventListener('click', () => saveXCAccount());

    // SD account buttons
    document.getElementById('add-sd-account-btn').addEventListener('click', () => showSDAccountModal());
    document.getElementById('sd-discover-btn').addEventListener('click', () => discoverSDLineups());
    document.getElementById('save-sd-account-btn').addEventListener('click', () => saveSDAccount());
    document.getElementById('sd-back-btn').addEventListener('click', () => sdShowStep(1));
    document.getElementById('sd-select-all').addEventListener('click', () => {
        document.querySelectorAll('#sd-lineups-list input[type="checkbox"]').forEach(cb => cb.checked = true);
    });
    document.getElementById('sd-select-none').addEventListener('click', () => {
        document.querySelectorAll('#sd-lineups-list input[type="checkbox"]').forEach(cb => cb.checked = false);
    });

    document.getElementById('add-token-btn').addEventListener('click', () => showAddTokenModal());
    document.getElementById('save-token-btn').addEventListener('click', () => saveToken());
}

/**
 * Application entry point — runs after the DOM is fully loaded.
 * Initializes all UI subsystems, loads initial data, and starts auto-refresh.
 */
document.addEventListener('DOMContentLoaded', () => {
    const noticeParams = new URLSearchParams(window.location.search);
    if (noticeParams.get('notice') === 'already_logged_in') {
        showNotification('You are already logged in.', 'warning');
        window.history.replaceState({}, document.title, '/');
    }
    initScrollToTop();
    initTabs();
    initModals();
    setupEventListeners();

    loadGlobalSettings();
    loadSources();
    loadEPGs();
    loadStats();
    loadActiveChannels();
    loadAllChannels();
    loadLogs();
    loadXCAccounts();
    loadSDAccounts();
    loadTokens();

    startAutoRefresh();

    document.getElementById('footer-year').textContent = new Date().getFullYear();
});