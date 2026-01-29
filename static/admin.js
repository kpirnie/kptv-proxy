// KPTV Proxy Admin Interface JavaScript

class KPTVAdmin {

    // Constructor initializes the admin interface state and triggers the complete
    // initialization sequence including data loading, event listener setup, and
    // automatic refresh interval configuration for real-time monitoring capabilities.
    constructor() {
        this.config = null;
        this.stats = null;
        this.refreshInterval = null;
        this.allChannels = null;
        this.allLogs = null;
        this.currentPage = 1;
        this.pageSize = 50;
        this.filteredChannels = null;
        this.init();
    }

    // init performs comprehensive initialization of the admin interface including
    // initial data loading from all API endpoints, event listener registration,
    // automatic refresh timer setup, and scroll-to-top button initialization for
    // complete interface readiness.
    init() {

        // Load initial data and functionality
        this.loadGlobalSettings();
        this.loadSources();
        this.loadEPGs();
        this.loadStats();
        this.loadActiveChannels();
        this.loadAllChannels();
        this.loadLogs();
        this.toTop();

        // Setup event listeners
        this.setupEventListeners();

        // Start auto-refresh
        this.startAutoRefresh();
    }

    // toTop initializes the scroll-to-top button with scroll event monitoring,
    // automatically showing or hiding the button based on scroll position with
    // smooth animations for improved user experience during page navigation.
    toTop() {
        const inTop = document.querySelector('.in-totop');
        if (inTop) {
            window.addEventListener('scroll', function () {
                setTimeout(function () {
                    window.scrollY > 100 ?
                        (inTop.style.opacity = 1, inTop.classList.add("uk-animation-slide-top")) :
                        (inTop.style.opacity -= .1, inTop.classList.remove("uk-animation-slide-top"));
                }, 400);
            });
        }
    }

    // setupEventListeners registers all event handlers for user interactions including
    // form submissions, button clicks, search operations, and pagination controls to
    // enable complete administrative functionality through the web interface.
    setupEventListeners() {

        // Global settings form
        document.getElementById('global-settings-form').addEventListener('submit', (e) => {
            e.preventDefault();
            this.saveGlobalSettings();
        });
        document.getElementById('load-global-settings').addEventListener('click', () => {
            this.loadGlobalSettings();
        });

        // Restart button
        document.getElementById('restart-btn').addEventListener('click', () => {
            this.restartService();
        });

        // Source management
        document.getElementById('add-source-btn').addEventListener('click', () => {
            this.showSourceModal();
        });

        document.getElementById('save-source-btn').addEventListener('click', () => {
            this.saveSource();
        });

        // EPG management
        document.getElementById('add-epg-btn').addEventListener('click', () => {
            this.showEPGModal();
        });

        document.getElementById('save-epg-btn').addEventListener('click', () => {
            this.saveEPG();
        });

        // Refresh buttons
        document.getElementById('refresh-channels').addEventListener('click', () => {
            this.loadActiveChannels();
        });
        document.getElementById('refresh-all-channels').addEventListener('click', () => {
            this.currentPage = 1; // Reset to first page on manual refresh
            this.filteredChannels = null; // Clear any filters
            document.getElementById('channel-search').value = ''; // Clear search input
            this.loadAllChannels();
        });
        document.getElementById('refresh-logs').addEventListener('click', () => {
            this.loadLogs();
        });

        // clear logs
        document.getElementById('clear-logs').addEventListener('click', () => {
            this.clearLogs();
        });

        // Search functionality
        document.getElementById('channel-search').addEventListener('input', (e) => {
            this.currentPage = 1; // Reset to first page when searching
            this.filterChannels(e.target.value);
        });

        // Log level filter
        document.getElementById('log-level').addEventListener('change', (e) => {
            this.filterLogs(e.target.value);
        });

        // Watcher toggle
        const watcherCheckbox = document.querySelector('input[name="watcherEnabled"]');
        if (watcherCheckbox) {
            watcherCheckbox.addEventListener('change', (e) => {
                this.toggleWatcher(e.target.checked);
            });
        }

        // Pagination
        document.getElementById('prev-page').addEventListener('click', (e) => {
            e.preventDefault();
            this.previousPage();
        });
        document.getElementById('next-page').addEventListener('click', (e) => {
            e.preventDefault();
            this.nextPage();
        });
        document.getElementById('first-page').addEventListener('click', (e) => {
            e.preventDefault();
            this.goToFirstPage();
        });
        document.getElementById('last-page').addEventListener('click', (e) => {
            e.preventDefault();
            this.goToLastPage();
        });
        document.getElementById('page-selector').addEventListener('change', (e) => {
            const page = parseInt(e.target.value);
            if (!isNaN(page)) {
                this.goToPage(page);
            }
        });

    }

    // previousPage navigates to the previous page of channels in paginated displays,
    // updating the current page counter and re-rendering the channel list with
    // appropriate pagination controls and status information.
    previousPage() {
        if (this.currentPage > 1) {
            this.currentPage--;
            this.renderCurrentPage();
        }
    }

    // nextPage navigates to the next page of channels in paginated displays,
    // validating page boundaries and updating the display with new channel data
    // while maintaining consistent pagination state across operations.
    nextPage() {
        const totalPages = Math.ceil((this.filteredChannels || this.allChannels || []).length / this.pageSize);
        if (this.currentPage < totalPages) {
            this.currentPage++;
            this.renderCurrentPage();
        }
    }

    // goToFirstPage jumps to the first page of channels in paginated displays,
    // providing quick navigation for users who need to return to the beginning
    // of the channel list regardless of current position.
    goToFirstPage() {
        if (this.currentPage !== 1) {
            this.currentPage = 1;
            this.renderCurrentPage();
        }
    }

    // goToLastPage jumps to the final page of channels in paginated displays,
    // calculating the last page number based on total channels and page size
    // for convenient navigation to the end of the channel list.
    goToLastPage() {
        const totalPages = Math.ceil((this.filteredChannels || this.allChannels || []).length / this.pageSize);
        if (this.currentPage !== totalPages && totalPages > 0) {
            this.currentPage = totalPages;
            this.renderCurrentPage();
        }
    }

    // goToPage navigates to a specific page number in paginated channel displays,
    // validating the requested page number against available pages and updating
    // the display with channel data for the requested page.
    //
    // Parameters:
    //   - page: target page number to display
    goToPage(page) {
        const totalPages = Math.ceil((this.filteredChannels || this.allChannels || []).length / this.pageSize);
        if (page >= 1 && page <= totalPages && page !== this.currentPage) {
            this.currentPage = page;
            this.renderCurrentPage();
        }
    }

    // renderCurrentPage displays the current page of channels by calculating the
    // appropriate slice of the channel array, rendering the channels, and updating
    // pagination information for consistent display state management.
    renderCurrentPage() {
        const channels = this.filteredChannels || this.allChannels;
        if (!channels) return;

        const startIndex = (this.currentPage - 1) * this.pageSize;
        const endIndex = startIndex + this.pageSize;
        const pageChannels = channels.slice(startIndex, endIndex);

        this.renderAllChannels(pageChannels);
        this.updatePaginationInfo();
    }

    // updatePaginationInfo updates all pagination display elements including page
    // counters, page selector dropdown, and navigation button states to reflect
    // the current pagination state and available navigation options.
    updatePaginationInfo() {
        const channels = this.filteredChannels || this.allChannels || [];
        const totalChannels = channels.length;
        const totalPages = Math.ceil(totalChannels / this.pageSize);
        const startIndex = (this.currentPage - 1) * this.pageSize + 1;
        const endIndex = Math.min(startIndex + this.pageSize - 1, totalChannels);

        document.getElementById('channel-pagination-info').textContent =
            `Showing ${startIndex}-${endIndex} of ${totalChannels} channels`;

        document.getElementById('current-page').textContent = this.currentPage;

        // Update page selector dropdown
        const pageSelector = document.getElementById('page-selector');
        if (pageSelector) {
            // Save current value to restore after update
            const currentValue = pageSelector.value;

            // Clear and rebuild options
            pageSelector.innerHTML = '';
            for (let i = 1; i <= totalPages; i++) {
                const option = document.createElement('option');
                option.value = i;
                option.textContent = i;
                option.selected = i === this.currentPage;
                pageSelector.appendChild(option);
            }

            // If current page wasn't in the options (shouldn't happen), select it
            if (pageSelector.value !== this.currentPage.toString()) {
                pageSelector.value = this.currentPage;
            }
        }

        // Update button states
        document.getElementById('first-page').parentElement.classList.toggle('uk-disabled', this.currentPage === 1);
        document.getElementById('prev-page').parentElement.classList.toggle('uk-disabled', this.currentPage === 1);
        document.getElementById('next-page').parentElement.classList.toggle('uk-disabled', this.currentPage === totalPages || totalPages === 0);
        document.getElementById('last-page').parentElement.classList.toggle('uk-disabled', this.currentPage === totalPages || totalPages === 0);
    }

    // startAutoRefresh initiates periodic background data refresh operations that
    // automatically update statistics and channel information every 5 seconds while
    // preserving user interface state including current page, search filters, and
    // page selector position for seamless monitoring experience.
    startAutoRefresh() {

        // Refresh stats and active channels every 5 seconds
        this.refreshInterval = setInterval(() => {
            this.loadStats();
            this.loadActiveChannels();

            // Preserve current page, search state, and page selector
            const searchInput = document.getElementById('channel-search');
            const currentSearch = searchInput ? searchInput.value.trim() : '';
            const currentPage = this.currentPage;

            this.loadAllChannels().then(() => {
                // Restore search filter if it was active
                if (currentSearch) {
                    this.filteredChannels = this.allChannels.filter(channel =>
                        channel.name.toLowerCase().includes(currentSearch.toLowerCase()) ||
                        (channel.group && channel.group.toLowerCase().includes(currentSearch.toLowerCase()))
                    );
                } else {
                    this.filteredChannels = null;
                }

                // Restore the previous page
                this.currentPage = currentPage;
                const totalPages = Math.ceil((this.filteredChannels || this.allChannels || []).length / this.pageSize);

                // If current page is beyond the new total, go to last page
                if (this.currentPage > totalPages && totalPages > 0) {
                    this.currentPage = totalPages;
                } else if (totalPages === 0) {
                    this.currentPage = 1;
                }

                this.renderCurrentPage();
            });
        }, 5000);
    }

    // stopAutoRefresh terminates the periodic refresh timer to prevent unnecessary
    // background operations during interface shutdown or when automatic updates
    // are no longer needed for the current view.
    stopAutoRefresh() {
        if (this.refreshInterval) {
            clearInterval(this.refreshInterval);
        }
    }

    // apiCall executes HTTP requests to admin API endpoints with comprehensive error
    // handling, automatic JSON parsing, and user notification for failures, providing
    // a consistent interface for all administrative operations.
    //
    // Parameters:
    //   - endpoint: API endpoint URL to call
    //   - options: fetch API options object with method, headers, body, etc.
    //
    // Returns:
    //   - Promise: resolves to parsed JSON response data
    async apiCall(endpoint, options = {}) {
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
            this.showNotification('API call failed: ' + error.message, 'danger');
            throw error;
        }
    }

    // loadGlobalSettings fetches current system configuration from the API and
    // populates the global settings form with existing values, handling missing
    // configuration gracefully by providing sensible defaults for all settings.
    async loadGlobalSettings() {
        try {
            const config = await this.apiCall('/api/config');
            this.config = config;
            this.populateGlobalSettingsForm(config);
        } catch (error) {
            // Fallback to default values if API fails
            this.populateGlobalSettingsForm({
                baseURL: "http://localhost:8080",
                //maxBufferSize: 256,
                bufferSizePerStream: 8,
                cacheEnabled: true,
                cacheDuration: "360m",
                importRefreshInterval: "12h",
                workerThreads: 4,
                debug: false,
                obfuscateUrls: true,
                sortField: "tvg-type",
                sortDirection: "asc",
                streamTimeout: "30s",
                maxConnectionsToApp: 100
            });
        }
    }

    // populateGlobalSettingsForm fills the global settings form with configuration
    // values from the provided config object, handling checkboxes, text inputs,
    // and special fields like FFmpeg arrays with appropriate formatting.
    //
    // Parameters:
    //   - config: configuration object containing all system settings
    populateGlobalSettingsForm(config) {
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

        // Handle watcherEnabled specifically if not in config
        const watcherCheckbox = form.querySelector('input[name="watcherEnabled"]');
        if (watcherCheckbox && config.watcherEnabled !== undefined) {
            watcherCheckbox.checked = config.watcherEnabled;
        }

        // Handle FFmpeg settings
        if (config.ffmpegMode !== undefined) {
            const ffmpegCheckbox = form.querySelector('input[name="ffmpegMode"]');
            if (ffmpegCheckbox) ffmpegCheckbox.checked = config.ffmpegMode;
        }
        if (config.ffmpegPreInput) {
            const ffmpegPreInput = form.querySelector('input[name="ffmpegPreInput"]');
            if (ffmpegPreInput) {
                ffmpegPreInput.value = Array.isArray(config.ffmpegPreInput) ?
                    config.ffmpegPreInput.join(' ') : config.ffmpegPreInput;
            }
        }
        if (config.ffmpegPreOutput) {
            const ffmpegPreOutput = form.querySelector('input[name="ffmpegPreOutput"]');
            if (ffmpegPreOutput) {
                ffmpegPreOutput.value = Array.isArray(config.ffmpegPreOutput) ?
                    config.ffmpegPreOutput.join(' ') : config.ffmpegPreOutput;
            }
        }
    }

    // saveGlobalSettings collects form data, validates required fields, processes
    // special configurations like FFmpeg arguments, and submits the complete
    // configuration to the API with automatic restart triggering.
    async saveGlobalSettings() {
        const form = document.getElementById('global-settings-form');
        const formData = new FormData(form);
        const newConfig = {};

        // Process all form fields
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

        // Add unchecked checkboxes (including ffmpegMode)
        form.querySelectorAll('input[type="checkbox"]').forEach(checkbox => {
            if (!formData.has(checkbox.name)) {
                newConfig[checkbox.name] = false;
            }
        });

        // EXPLICITLY HANDLE FFMPEG SETTINGS
        const ffmpegModeCheckbox = form.querySelector('input[name="ffmpegMode"]');
        if (ffmpegModeCheckbox) {
            newConfig.ffmpegMode = ffmpegModeCheckbox.checked;
        }

        // Process FFmpeg arguments
        const ffmpegPreInput = form.querySelector('input[name="ffmpegPreInput"]');
        if (ffmpegPreInput && ffmpegPreInput.value) {
            newConfig.ffmpegPreInput = ffmpegPreInput.value.split(' ').filter(arg => arg.length > 0);
        } else {
            newConfig.ffmpegPreInput = [];
        }

        const ffmpegPreOutput = form.querySelector('input[name="ffmpegPreOutput"]');
        if (ffmpegPreOutput && ffmpegPreOutput.value) {
            newConfig.ffmpegPreOutput = ffmpegPreOutput.value.split(' ').filter(arg => arg.length > 0);
        } else {
            newConfig.ffmpegPreOutput = [];
        }

        try {
            // CRITICAL FIX: Get the current config first to preserve sources
            const currentConfig = await this.apiCall('/api/config');

            // Merge the new global settings with existing config (preserving sources)
            const mergedConfig = {
                ...currentConfig,  // Start with existing config
                ...newConfig       // Override with new global settings
            };

            // Ensure sources array is preserved
            if (currentConfig.sources) {
                mergedConfig.sources = currentConfig.sources;
            }

            await this.apiCall('/api/config', {
                method: 'POST',
                body: JSON.stringify(mergedConfig)
            });

            this.showNotification('Global settings saved successfully!', 'success');

            // Restart the service
            setTimeout(() => {
                this.restartService();
            }, 1000);
        } catch (error) {
            this.showNotification('Failed to save global settings: ' + error.message, 'danger');
        }
    }

    // loadSources fetches and displays all configured stream sources from the API,
    // rendering detailed source information including connection limits, timeouts,
    // and authentication settings for administrative review and management.
    async loadSources() {
        try {
            const config = await this.apiCall('/api/config');
            this.renderSources(config.sources || []);
        } catch (error) {
            document.getElementById('sources-container').innerHTML =
                '<div class="uk-alert uk-alert-danger">Failed to load sources</div>';
        }
    }

    // renderSources generates HTML for displaying all configured sources with
    // comprehensive metadata including connection parameters, retry settings,
    // authentication headers, and operational controls for editing and deletion.
    //
    // Parameters:
    //   - sources: array of source configuration objects to display
    renderSources(sources) {
        const container = document.getElementById('sources-container');

        if (sources.length === 0) {
            container.innerHTML = '<div class="uk-alert uk-alert-warning">No sources configured</div>';
            return;
        }

        container.innerHTML = sources.map((source, index) => `
            <div class="source-item fade-in">
                <div class="uk-flex uk-flex-between uk-flex-middle">
                    <div class="uk-flex-1">
                        <h4 class="uk-margin-remove-bottom">${this.escapeHtml(source.name)}</h4>
                        <div class="uk-text-muted uk-text-small">${this.obfuscateUrl(source.url)}</div>
                    </div>
                    <div class="uk-flex uk-flex-middle">
                        <span class="status-indicator status-active"></span>
                        <span class="uk-text-small uk-text-muted">Order: ${source.order}</span>
                    </div>
                </div>
                <div class="uk-grid-small uk-margin-small-top" uk-grid>
                    <div class="uk-width-1-4@s">
                        <div class="uk-text-small">
                            <div class="uk-text-muted">Max Connections</div>
                            <div>${source.maxConnections}</div>
                        </div>
                    </div>
                    <div class="uk-width-1-4@s">
                        <div class="uk-text-small">
                            <div class="uk-text-muted">Timeout</div>
                            <div>${source.maxStreamTimeout}</div>
                        </div>
                    </div>
                    <div class="uk-width-1-4@s">
                        <div class="uk-text-small">
                            <div class="uk-text-muted">Max Retries</div>
                            <div>${source.maxRetries}</div>
                        </div>
                    </div>
                    <div class="uk-width-1-4@s">
                        <div class="uk-text-small">
                            <div class="uk-text-muted">Min Data Size</div>
                            <div>${source.minDataSize} KB</div>
                        </div>
                    </div>
                </div>
                ${source.userAgent ? `
                    <div class="uk-margin-small-top">
                        <div class="uk-text-small">
                            <div class="uk-text-muted">User Agent</div>
                            <div class="text-truncate">${this.escapeHtml(source.userAgent)}</div>
                        </div>
                    </div>
                ` : ''}
                ${source.reqOrigin ? `
                    <div class="uk-margin-small-top">
                        <div class="uk-text-small">
                            <div class="uk-text-muted">Origin</div>
                            <div class="text-truncate">${this.escapeHtml(source.reqOrigin)}</div>
                        </div>
                    </div>
                ` : ''}
                ${source.reqReferrer ? `
                    <div class="uk-margin-small-top">
                        <div class="uk-text-small">
                            <div class="uk-text-muted">Referrer</div>
                            <div class="text-truncate">${this.escapeHtml(source.reqReferrer)}</div>
                        </div>
                    </div>
                ` : ''}
                <div class="source-actions">
                    <button class="uk-button uk-button-primary uk-button-small" onclick="kptvAdmin.editSource(${index})">
                        <span uk-icon="pencil"></span> Edit
                    </button>
                    <button class="uk-button uk-button-danger uk-button-small uk-margin-small-left" onclick="kptvAdmin.deleteSource(${index})">
                        <span uk-icon="trash"></span> Delete
                    </button>
                </div>
            </div>
        `).join('');
    }

    // showSourceModal displays the source configuration modal dialog for adding
    // new sources or editing existing ones, populating form fields with current
    // values when editing or clearing fields for new source creation.
    //
    // Parameters:
    //   - sourceIndex: optional index of source to edit, null for new sources
    showSourceModal(sourceIndex = null) {
        const modal = UIkit.modal('#source-modal');
        const title = document.getElementById('source-modal-title');

        if (sourceIndex !== null) {
            title.textContent = 'Edit Source';
            if (this.config && this.config.sources && this.config.sources[sourceIndex]) {
                this.populateSourceForm(this.config.sources[sourceIndex], sourceIndex);
            } else {
                this.showNotification('Config not loaded yet', 'warning');
                this.loadGlobalSettings();
            }
        } else {
            title.textContent = 'Add Source';
            this.clearSourceForm();
        }

        modal.show();
    }

    // populateSourceForm fills the source configuration form with values from
    // an existing source object, handling all fields including basic settings,
    // filtering rules, and HTTP headers for complete source editing.
    //
    // Parameters:
    //   - source: source configuration object containing current values
    //   - index: array index of the source being edited
    populateSourceForm(source, index) {
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
        // Populate regex filters
        document.getElementById('source-live-include-regex').value = source.liveIncludeRegex || '';
        document.getElementById('source-live-exclude-regex').value = source.liveExcludeRegex || '';
        document.getElementById('source-series-include-regex').value = source.seriesIncludeRegex || '';
        document.getElementById('source-series-exclude-regex').value = source.seriesExcludeRegex || '';
        document.getElementById('source-vod-include-regex').value = source.vodIncludeRegex || '';
        document.getElementById('source-vod-exclude-regex').value = source.vodExcludeRegex || '';
    }

    // clearSourceForm resets all source configuration form fields to empty or
    // default values, preparing the form for new source creation without any
    // residual data from previous operations.
    clearSourceForm() {
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

    // saveSource collects source configuration data from the form, validates
    // required fields, merges with existing configuration, and submits to the
    // API with comprehensive error handling and user feedback.
    async saveSource() {
        console.log('saveSource function called');

        try {
            const form = document.getElementById('source-form');
            const index = document.getElementById('source-index').value;

            console.log('Form found, index:', index);

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
                // Add regex filters
                liveIncludeRegex: document.getElementById('source-live-include-regex').value || '',
                liveExcludeRegex: document.getElementById('source-live-exclude-regex').value || '',
                seriesIncludeRegex: document.getElementById('source-series-include-regex').value || '',
                seriesExcludeRegex: document.getElementById('source-series-exclude-regex').value || '',
                vodIncludeRegex: document.getElementById('source-vod-include-regex').value || '',
                vodExcludeRegex: document.getElementById('source-vod-exclude-regex').value || ''
            };

            console.log('Source object created:', source);

            // Validate required fields
            if (!source.name || !source.url) {
                this.showNotification('Name and URL are required', 'danger');
                return;
            }

            console.log('About to get config...');
            const response = await fetch('/api/config');
            console.log('Config response status:', response.status);

            if (!response.ok) {
                throw new Error(`HTTP ${response.status}: ${response.statusText}`);
            }

            const config = await response.json();
            console.log('Got config:', config);

            // Ensure sources array exists
            if (!config.sources) {
                config.sources = [];
            }

            if (index === '') {
                config.sources.push(source);
                console.log('Added new source');
            } else {
                config.sources[parseInt(index)] = source;
                console.log('Updated existing source');
            }

            console.log('About to save config...');
            const saveResponse = await fetch('/api/config', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json'
                },
                body: JSON.stringify(config)
            });

            console.log('Save response status:', saveResponse.status);

            if (!saveResponse.ok) {
                const errorText = await saveResponse.text();
                console.error('Save error response:', errorText);
                throw new Error(`HTTP ${saveResponse.status}: ${errorText}`);
            }

            console.log('Save successful');
            UIkit.modal('#source-modal').hide();
            this.showNotification('Source saved successfully!', 'success');
            this.loadSources();

        } catch (error) {
            console.error('Detailed error:', error);
            this.showNotification('Failed to save source: ' + error.message, 'danger');
        }
    }

    // editSource opens the source configuration modal populated with data from
    // the specified source index, enabling modification of existing source
    // settings through the standard form interface.
    //
    // Parameters:
    //   - index: array index of the source to edit
    editSource(index) {
        this.showSourceModal(index);
    }

    // deleteSource removes a source from configuration after user confirmation,
    // updating the configuration file and triggering application restart to
    // apply changes with appropriate error handling and user notification.
    //
    // Parameters:
    //   - index: array index of the source to delete
    async deleteSource(index) {
        if (!confirm('Are you sure you want to delete this source?')) {
            return;
        }

        try {
            const config = await this.apiCall('/api/config');
            config.sources.splice(index, 1);

            await this.apiCall('/api/config', {
                method: 'POST',
                body: JSON.stringify(config)
            });

            this.showNotification('Source deleted successfully!', 'success');
            this.loadSources();

            // Restart the service
            setTimeout(() => {
                this.restartService();
            }, 1000);
        } catch (error) {
            this.showNotification('Failed to delete source', 'danger');
        }
    }

    // After loadSources() method, add:

    // Load EPGs from configuration
    async loadEPGs() {
        try {
            const config = await this.apiCall('/api/config');
            this.renderEPGs(config.epgs || []);
        } catch (error) {
            document.getElementById('epgs-container').innerHTML =
                '<div class="uk-alert uk-alert-danger">Failed to load EPGs</div>';
        }
    }

    // Render EPGs list
    renderEPGs(epgs) {
        const container = document.getElementById('epgs-container');

        if (epgs.length === 0) {
            container.innerHTML = '<div class="uk-alert uk-alert-warning">No EPGs configured</div>';
            return;
        }

        container.innerHTML = epgs.map((epg, index) => `
        <div class="source-item fade-in">
            <div class="uk-flex uk-flex-between uk-flex-middle">
                <div class="uk-flex-1">
                    <h4 class="uk-margin-remove-bottom">${this.escapeHtml(epg.name)}</h4>
                    <div class="uk-text-muted uk-text-small">${this.obfuscateUrl(epg.url)}</div>
                </div>
                <div class="uk-flex uk-flex-middle">
                    <span class="status-indicator status-active"></span>
                    <span class="uk-text-small uk-text-muted">Order: ${epg.order}</span>
                </div>
            </div>
            <div class="source-actions">
                <button class="uk-button uk-button-primary uk-button-small" onclick="kptvAdmin.editEPG(${index})">
                    <span uk-icon="pencil"></span> Edit
                </button>
                <button class="uk-button uk-button-danger uk-button-small uk-margin-small-left" onclick="kptvAdmin.deleteEPG(${index})">
                    <span uk-icon="trash"></span> Delete
                </button>
            </div>
        </div>
    `).join('');
    }

    // Show EPG modal
    showEPGModal(epgIndex = null) {
        const modal = UIkit.modal('#epg-modal');
        const title = document.getElementById('epg-modal-title');

        if (epgIndex !== null) {
            title.textContent = 'Edit EPG';
            if (this.config && this.config.epgs && this.config.epgs[epgIndex]) {
                this.populateEPGForm(this.config.epgs[epgIndex], epgIndex);
            }
        } else {
            title.textContent = 'Add EPG';
            this.clearEPGForm();
        }

        modal.show();
    }

    // Populate EPG form
    populateEPGForm(epg, index) {
        document.getElementById('epg-index').value = index;
        document.getElementById('epg-name').value = epg.name || '';
        document.getElementById('epg-url').value = epg.url || '';
        document.getElementById('epg-order').value = epg.order || 1;
    }

    // Clear EPG form
    clearEPGForm() {
        document.getElementById('epg-index').value = '';
        document.getElementById('epg-form').reset();
        document.getElementById('epg-order').value = 1;
    }

    // Save EPG
    async saveEPG() {
        try {
            const index = document.getElementById('epg-index').value;
            const epg = {
                name: document.getElementById('epg-name').value,
                url: document.getElementById('epg-url').value,
                order: parseInt(document.getElementById('epg-order').value) || 1
            };

            if (!epg.name || !epg.url) {
                this.showNotification('Name and URL are required', 'danger');
                return;
            }

            const config = await this.apiCall('/api/config');

            if (!config.epgs) {
                config.epgs = [];
            }

            if (index === '') {
                config.epgs.push(epg);
            } else {
                config.epgs[parseInt(index)] = epg;
            }

            await this.apiCall('/api/config', {
                method: 'POST',
                body: JSON.stringify(config)
            });

            UIkit.modal('#epg-modal').hide();
            this.showNotification('EPG saved successfully!', 'success');
            this.loadEPGs();
        } catch (error) {
            this.showNotification('Failed to save EPG: ' + error.message, 'danger');
        }
    }

    // Edit EPG
    editEPG(index) {
        this.showEPGModal(index);
    }

    // Delete EPG
    async deleteEPG(index) {
        if (!confirm('Are you sure you want to delete this EPG?')) {
            return;
        }

        try {
            const config = await this.apiCall('/api/config');
            config.epgs.splice(index, 1);

            await this.apiCall('/api/config', {
                method: 'POST',
                body: JSON.stringify(config)
            });

            this.showNotification('EPG deleted successfully!', 'success');
            this.loadEPGs();
        } catch (error) {
            this.showNotification('Failed to delete EPG', 'danger');
        }
    }

    // loadStats fetches current system statistics from the API including performance
    // metrics, resource utilization, client connections, and operational status for
    // display in the dashboard monitoring interface.
    async loadStats() {
        try {
            const stats = await this.apiCall('/api/stats');
            this.updateStatsDisplay(stats);
        } catch (error) {
            // Use mock data if API fails
            this.updateStatsDisplay({
                totalChannels: 0,
                activeStreams: 0,
                totalSources: 0,
                connectedClients: 0,
                uptime: "0m",
                memoryUsage: "0 MB",
                cacheStatus: "Disabled",
                workerThreads: 4,
                totalConnections: 0,
                bytesTransferred: "0 B",
                activeRestreamers: 0,
                streamErrors: 0,
                responseTime: "0ms"
            });
        }
    }

    // updateStatsDisplay updates all statistics display elements in the dashboard
    // with current values from the stats object, formatting numbers and durations
    // appropriately for human-readable presentation.
    //
    // Parameters:
    //   - stats: statistics object containing all system metrics
    updateStatsDisplay(stats) {
        const totalChannelsEl = document.getElementById('total-channels');
        const activeStreamsEl = document.getElementById('active-streams');
        const totalSourcesEl = document.getElementById('total-sources');
        const connectedClientsEl = document.getElementById('connected-clients');

        if (totalChannelsEl) totalChannelsEl.textContent = stats.totalChannels || 0;
        if (activeStreamsEl) activeStreamsEl.textContent = stats.activeStreams || 0;
        if (totalSourcesEl) totalSourcesEl.textContent = stats.totalSources || 0;
        if (connectedClientsEl) connectedClientsEl.textContent = stats.connectedClients || 0;

        const uptimeEl = document.getElementById('uptime');
        const memoryUsageEl = document.getElementById('memory-usage');
        const cacheStatusEl = document.getElementById('cache-status');

        if (uptimeEl) uptimeEl.textContent = stats.uptime || '0m';
        if (memoryUsageEl) memoryUsageEl.textContent = stats.memoryUsage || '0 MB';
        if (cacheStatusEl) cacheStatusEl.textContent = stats.cacheStatus || 'Unknown';

        const totalConnectionsEl = document.getElementById('total-connections');
        const bytesTransferredEl = document.getElementById('bytes-transferred');
        const activeRestreamersEl = document.getElementById('active-restreamers');
        const streamErrorsEl = document.getElementById('stream-errors');
        const responseTimeEl = document.getElementById('response-time');

        if (totalConnectionsEl) totalConnectionsEl.textContent = stats.totalConnections || 0;
        if (bytesTransferredEl) bytesTransferredEl.textContent = stats.bytesTransferred || '0 B';
        if (activeRestreamersEl) activeRestreamersEl.textContent = stats.activeRestreamers || 0;
        if (streamErrorsEl) streamErrorsEl.textContent = stats.streamErrors || 0;
        if (responseTimeEl) responseTimeEl.textContent = stats.responseTime || '0ms';

        const watcherStatus = stats.watcherEnabled ? 'Enabled' : 'Disabled';
        const watcherElement = document.getElementById('watcher-status');
        if (watcherElement) {
            watcherElement.textContent = watcherStatus;
            watcherElement.className = `watcher-status ${stats.watcherEnabled ? 'enabled' : 'disabled'}`;
        }

        const proxyClientsEl = document.getElementById('proxy-clients');
        const upstreamConnectionsEl = document.getElementById('upstream-connections');
        const connectionEfficiencyEl = document.getElementById('connection-efficiency');
        const activeChannelsStatEl = document.getElementById('active-channels-stat');  // NEW
        const avgClientsPerStreamEl = document.getElementById('avg-clients-per-stream');  // NEW

        if (proxyClientsEl) proxyClientsEl.textContent = stats.connectedClients || 0;
        if (upstreamConnectionsEl) upstreamConnectionsEl.textContent = stats.upstreamConnections || 0;

        if (connectionEfficiencyEl) {
            const clients = stats.connectedClients || 0;
            const upstream = stats.upstreamConnections || 0;
            if (upstream > 0) {
                const ratio = (clients / upstream).toFixed(1);
                connectionEfficiencyEl.textContent = `${ratio}:1`;
            } else {
                connectionEfficiencyEl.textContent = 'N/A';
            }
        }

        // NEW - Active Channels
        if (activeChannelsStatEl) {
            activeChannelsStatEl.textContent = stats.activeChannels || 0;
        }

        // NEW - Avg Clients per Stream
        if (avgClientsPerStreamEl) {
            if (stats.avgClientsPerStream > 0) {
                avgClientsPerStreamEl.textContent = stats.avgClientsPerStream.toFixed(1);
            } else {
                avgClientsPerStreamEl.textContent = 'N/A';
            }
        }
    }

    // loadActiveChannels fetches and displays information about channels currently
    // streaming content to connected clients, providing focused operational monitoring
    // data for active streaming sessions.
    async loadActiveChannels() {
        try {
            const channels = await this.apiCall('/api/channels/active');
            this.renderActiveChannels(channels);
        } catch (error) {
            document.getElementById('active-channels-list').innerHTML =
                '<div class="uk-alert uk-alert-warning">No active channels or failed to load</div>';
        }
    }

    // renderActiveChannels generates HTML for displaying active channels with
    // comprehensive information including client counts, bandwidth usage, source
    // details, logos, and real-time streaming statistics for monitoring.
    //
    // Parameters:
    //   - channels: array of active channel objects to display
    renderActiveChannels(channels) {
        const container = document.getElementById('active-channels-list');

        if (channels.length === 0) {
            container.innerHTML = '<div class="uk-alert uk-alert-warning">No active channels</div>';
            return;
        }

        container.innerHTML = channels.map(channel => {
            const safeId = channel.name.replace(/[^a-zA-Z0-9]/g, '_');
            return `
            <div class="channel-item fade-in">
                <div class="uk-flex uk-flex-between uk-flex-middle">
                    <div class="uk-flex uk-flex-middle">
                        <img src="${channel.logoURL || 'https://cdn.kcp.im/tv/kptv-icon.png'}" 
                            alt="${this.escapeHtml(channel.name)}" 
                            style="width: 48px; height: 48px; object-fit: cover; border-radius: 4px; margin-right: 12px;"
                            onerror="this.src='https://cdn.kcp.im/tv/kptv-icon.png'">
                        <div class="uk-flex-1">
                            <div class="channel-name">${this.escapeHtml(channel.name)}</div>
                            <div class="channel-details">
                                <span class="connection-dot status-active"></span>
                                ${channel.clients || 0} client(s) connected
                            </div>
                            <div class="uk-margin-small-top stream-stats-badges" id="stats-${safeId}">
                                <span class="uk-text-meta">Loading stats...</span>
                            </div>
                        </div>
                    </div>
                    <div class="uk-text-right uk-text-small">
                        <div class="uk-text-muted">Bytes: ${this.formatBytes(channel.bytesTransferred || 0)}</div>
                        <div class="uk-text-muted">Source: ${channel.currentSource || 'Unknown'}</div>
                        <button class="uk-button uk-button-secondary uk-button-small uk-margin-small-top" onclick="kptvAdmin.showStreamSelector('${this.escapeHtml(channel.name).replace(/'/g, "\\'")}')">
                            <span uk-icon="settings"></span> Streams
                        </button>
                    </div>
                </div>
            </div>
            `;
        }).join('');

        // Load stats for each active channel
        channels.forEach(channel => {
            this.loadChannelStats(channel.name);
        });
    }

    // loadChannelStats fetches real-time streaming statistics for a specific channel
    // including codec information, resolution, bitrate, and quality metrics gathered
    // through FFprobe analysis for display in the active channels view.
    //
    // Parameters:
    //   - channelName: name of channel to retrieve statistics for
    async loadChannelStats(channelName) {
        try {
            const encodedChannelName = encodeURIComponent(channelName);
            const statsData = await this.apiCall(`/api/channels/${encodedChannelName}/stats`);
            this.renderChannelStatsBadges(channelName, statsData);
        } catch (error) {
            const safeId = channelName.replace(/[^a-zA-Z0-9]/g, '_');
            const container = document.getElementById(`stats-${safeId}`);
            if (container) {
                container.innerHTML = '<span class="uk-badge">No Stats</span>';
            }
        }
    }

    // renderChannelStatsBadges generates HTML badges displaying streaming statistics
    // for a channel including resolution, frame rate, codecs, and bitrate in a
    // compact, visually organized format for dashboard display.
    //
    // Parameters:
    //   - channelName: name of channel for badge container identification
    //   - stats: statistics object containing stream quality metrics
    renderChannelStatsBadges(channelName, stats) {
        const safeId = channelName.replace(/[^a-zA-Z0-9]/g, '_');
        const container = document.getElementById(`stats-${safeId}`);

        if (!container) return;

        if (!stats.streaming || !stats.valid) {
            container.innerHTML = '<span class="uk-badge">No Stats</span>';
            return;
        }

        const badges = [];

        if (stats.videoResolution) {
            badges.push(`<span class="uk-badge stat-badge">${stats.videoResolution}</span>`);
        }

        if (stats.fps > 0) {
            badges.push(`<span class="uk-badge stat-badge">${stats.fps.toFixed(0)}fps</span>`);
        }

        if (stats.videoCodec) {
            badges.push(`<span class="uk-badge stat-badge">${stats.videoCodec.toUpperCase()}</span>`);
        }

        if (stats.audioCodec) {
            badges.push(`<span class="uk-badge stat-badge">${stats.audioCodec.toUpperCase()}</span>`);
        }

        if (stats.bitrate > 0) {
            badges.push(`<span class="uk-badge stat-badge">${this.formatBitrate(stats.bitrate)}</span>`);
        }

        if (badges.length === 0) {
            container.innerHTML = '<span class="uk-badge stat-badge">No Stats</span>';
        } else {
            container.innerHTML = badges.join(' ');
        }
    }

    // renderStreamStats displays detailed streaming statistics in an expanded format
    // with labeled fields for container type, codecs, resolution, frame rate, bitrate,
    // and audio configuration for comprehensive stream analysis.
    //
    // Parameters:
    //   - stats: statistics object containing complete stream metadata
    renderStreamStats(stats) {
        const container = document.getElementById('stream-stats-container');

        if (!stats.streaming || !stats.valid) {
            container.style.display = 'none';
            return;
        }

        container.style.display = 'block';

        document.getElementById('stat-container').textContent = stats.container || 'N/A';
        document.getElementById('stat-stream-type').textContent = stats.streamType || 'N/A';
        document.getElementById('stat-video-codec').textContent = stats.videoCodec || 'N/A';
        document.getElementById('stat-resolution').textContent = stats.videoResolution || 'N/A';
        document.getElementById('stat-fps').textContent = stats.fps > 0 ? stats.fps.toFixed(2) : 'N/A';
        document.getElementById('stat-bitrate').textContent = stats.bitrate > 0 ? this.formatBitrate(stats.bitrate) : 'N/A';
        document.getElementById('stat-audio-codec').textContent = stats.audioCodec || 'N/A';
        document.getElementById('stat-audio-channels').textContent = stats.audioChannels || 'N/A';
    }

    // showStreamSelector fetches and displays available streams for a channel in
    // a modal dialog, enabling stream selection, ordering, and management through
    // an interactive interface with current/preferred stream indicators.
    //
    // Parameters:
    //   - channelName: name of channel to display streams for
    async showStreamSelector(channelName) {
        try {
            const encodedChannelName = encodeURIComponent(channelName);
            const data = await this.apiCall(`/api/channels/${encodedChannelName}/streams`);
            this.renderStreamSelector(data);

            // REMOVE THESE LINES:
            // const statsData = await this.apiCall(`/api/channels/${encodedChannelName}/stats`);
            // this.renderStreamStats(statsData);

            UIkit.modal('#stream-selector-modal').show();
        } catch (error) {
            this.showNotification('Failed to load streams for channel', 'danger');
        }
    }

    // loadAllChannels fetches complete channel listing from the API including both
    // active and inactive channels with metadata for display in the comprehensive
    // channels view with pagination support.
    async loadAllChannels() {
        try {
            const channels = await this.apiCall('/api/channels');
            this.allChannels = channels;

            this.renderCurrentPage();
            return channels;
        } catch (error) {
            document.getElementById('all-channels-list').innerHTML =
                '<div class="uk-alert uk-alert-danger">Failed to load channels</div>';
            throw error;
        }
    }

    // renderAllChannels generates HTML for displaying all channels with status
    // indicators, group classifications, source counts, logos, and streaming
    // statistics organized in a paginated list format.
    //
    // Parameters:
    //   - channels: array of channel objects to display
    renderAllChannels(channels) {
        const container = document.getElementById('all-channels-list');

        if (channels.length === 0) {
            container.innerHTML = '<div class="uk-alert uk-alert-warning">No channels found</div>';
            return;
        }

        // Use document fragment for batch DOM insertion
        const fragment = document.createDocumentFragment();

        channels.forEach(channel => {
            const safeId = channel.name.replace(/[^a-zA-Z0-9]/g, '_');
            const div = document.createElement('div');
            div.className = `channel-item ${channel.active ? '' : 'channel-inactive'} fade-in`;
            div.innerHTML = `
                <div class="uk-flex uk-flex-between uk-flex-middle">
                    <div class="uk-flex uk-flex-middle">
                        <img src="${channel.logoURL || 'https://cdn.kcp.im/tv/kptv-icon.png'}" 
                            alt="${this.escapeHtml(channel.name)}" 
                            style="width: 48px; height: 48px; object-fit: cover; border-radius: 4px; margin-right: 12px;"
                            onerror="this.src='https://cdn.kcp.im/tv/kptv-icon.png'">
                        <div class="uk-flex-1">
                            <div class="channel-name">${this.escapeHtml(channel.name)}</div>
                            <div class="channel-details">
                                <div class="uk-text-small uk-text-muted">
                                    Group: ${channel.group || 'Uncategorized'} | 
                                    Sources: ${channel.sources || 0} | 
                                    Status: ${channel.active ? `Active (${channel.clients} clients)` : 'Inactive'}
                                </div>
                            </div>
                            ${channel.active ? `
                            <div class="uk-margin-small-top stream-stats-badges" id="stats-all-${safeId}">
                                <span class="uk-text-meta">Loading stats...</span>
                            </div>
                            ` : ''}
                        </div>
                    </div>
                    <div class="uk-text-right">
                        <span class="status-indicator ${channel.active ? 'status-active' : 'status-error'}"></span>
                        <button class="uk-button uk-button-secondary uk-button-small uk-margin-small-left" onclick="kptvAdmin.showStreamSelector('${this.escapeHtml(channel.name).replace(/'/g, "\\'")}')">
                            <span uk-icon="settings"></span> Streams
                        </button>
                    </div>
                </div>
            `;
            fragment.appendChild(div);
        });

        // Clear and append in one operation
        container.innerHTML = '';
        container.appendChild(fragment);

        // Load stats for active channels
        channels.forEach(channel => {
            if (channel.active) {
                this.loadChannelStatsForAllTab(channel.name);
            }
        });
    }

    // loadChannelStatsForAllTab fetches streaming statistics for a channel in the
    // context of the all channels view, using separate container IDs to avoid
    // conflicts with active channels view statistics display.
    //
    // Parameters:
    //   - channelName: name of channel to retrieve statistics for
    async loadChannelStatsForAllTab(channelName) {
        try {
            const encodedChannelName = encodeURIComponent(channelName);
            const statsData = await this.apiCall(`/api/channels/${encodedChannelName}/stats`);
            this.renderChannelStatsBadgesForAllTab(channelName, statsData);
        } catch (error) {
            const safeId = channelName.replace(/[^a-zA-Z0-9]/g, '_');
            const container = document.getElementById(`stats-all-${safeId}`);
            if (container) {
                container.innerHTML = '<span class="uk-badge">No Stats</span>';
            }
        }
    }

    // renderChannelStatsBadgesForAllTab generates statistics badges specifically for
    // the all channels view using appropriate container IDs and formatting to maintain
    // visual consistency across different administrative views.
    //
    // Parameters:
    //   - channelName: name of channel for badge container identification
    //   - stats: statistics object containing stream quality metrics
    renderChannelStatsBadgesForAllTab(channelName, stats) {
        const safeId = channelName.replace(/[^a-zA-Z0-9]/g, '_');
        const container = document.getElementById(`stats-all-${safeId}`);

        if (!container) return;

        if (!stats.streaming || !stats.valid) {
            container.innerHTML = '<span class="uk-badge">No Stats</span>';
            return;
        }

        const badges = [];

        if (stats.videoResolution) {
            badges.push(`<span class="uk-badge stat-badge">${stats.videoResolution}</span>`);
        }

        if (stats.fps > 0) {
            badges.push(`<span class="uk-badge stat-badge">${stats.fps.toFixed(0)}fps</span>`);
        }

        if (stats.videoCodec) {
            badges.push(`<span class="uk-badge stat-badge">${stats.videoCodec.toUpperCase()}</span>`);
        }

        if (stats.audioCodec) {
            badges.push(`<span class="uk-badge stat-badge">${stats.audioCodec.toUpperCase()}</span>`);
        }

        if (stats.bitrate > 0) {
            badges.push(`<span class="uk-badge stat-badge">${this.formatBitrate(stats.bitrate)}</span>`);
        }

        if (badges.length === 0) {
            container.innerHTML = '<span class="uk-badge stat-badge">No Stats</span>';
        } else {
            container.innerHTML = badges.join(' ');
        }
    }

    // filterChannels applies search/filter terms to the complete channel list,
    // updating the filtered channels array and resetting pagination to display
    // matching channels with appropriate empty state handling.
    //
    // Parameters:
    //   - searchTerm: text to filter channels by name or group
    filterChannels(searchTerm) {
        if (!this.allChannels) return;

        if (!searchTerm.trim()) {
            this.filteredChannels = null;
        } else {
            this.filteredChannels = this.allChannels.filter(channel =>
                channel.name.toLowerCase().includes(searchTerm.toLowerCase()) ||
                (channel.group && channel.group.toLowerCase().includes(searchTerm.toLowerCase()))
            );
        }

        this.currentPage = 1; // Reset to first page when filtering
        this.renderCurrentPage();
    }

    // loadLogs fetches current application logs from the API for display in the
    // logs view, providing real-time debugging and operational monitoring information
    // through the administrative interface.
    async loadLogs() {
        try {
            const logs = await this.apiCall('/api/logs');
            this.allLogs = logs;
            this.renderLogs(logs);
        } catch (error) {
            document.getElementById('logs-container').innerHTML =
                '<div class="uk-alert uk-alert-danger">Failed to load logs</div>';
        }
    }

    // renderLogs generates HTML for displaying log entries with appropriate styling
    // based on log level, timestamps, and message content, automatically scrolling
    // to show the most recent entries at the bottom.
    //
    // Parameters:
    //   - logs: array of log entry objects to display
    renderLogs(logs) {
        const container = document.getElementById('logs-container');

        if (logs.length === 0) {
            container.innerHTML = '<div class="uk-text-center uk-text-muted">No logs available</div>';
            return;
        }

        container.innerHTML = logs.map(log => `
            <div class="log-entry log-${log.level}">
                <span class="log-timestamp">${log.timestamp}</span> 
                [${log.level.toUpperCase()}] ${this.escapeHtml(log.message)}
            </div>
        `).join('');

        // Auto-scroll to bottom
        container.scrollTop = container.scrollHeight;
    }

    // filterLogs applies log level filtering to the complete log array, displaying
    // only entries matching the specified level or all entries when level is 'all'
    // for focused debugging and monitoring operations.
    //
    // Parameters:
    //   - level: log level to filter by ('all', 'error', 'warning', 'info', 'debug')
    filterLogs(level) {
        if (!this.allLogs) return;

        if (level === 'all') {
            this.renderLogs(this.allLogs);
        } else {
            const filtered = this.allLogs.filter(log => log.level === level);
            this.renderLogs(filtered);
        }
    }

    // clearLogs sends a request to clear the server-side log buffer after user
    // confirmation, providing a clean slate for new log entries during debugging
    // and troubleshooting operations.
    async clearLogs() {
        if (!confirm('Are you sure you want to clear all logs?')) {
            return;
        }

        try {
            await this.apiCall('/api/logs', { method: 'DELETE' });
            this.showNotification('Logs cleared successfully!', 'success');
            this.loadLogs();
        } catch (error) {
            this.showNotification('Failed to clear logs', 'danger');
        }
    }

    // restartService initiates a graceful application restart after user confirmation,
    // displaying a loading overlay during the restart process and managing auto-refresh
    // state to prevent conflicts during the restart sequence.
    async restartService() {
        if (!confirm('Are you sure you want to restart the KPTV Proxy service? This will temporarily interrupt all streams.')) {
            return;
        }

        this.showLoadingOverlay('Restarting KPTV Proxy...');
        this.stopAutoRefresh();

        try {
            const result = await this.apiCall('/api/restart', { method: 'POST' });

            this.hideLoadingOverlay();
            this.showNotification(result.message || 'Restart request sent successfully!', 'warning');
            this.startAutoRefresh();

            // Reload data after restart
            setTimeout(() => {
                this.loadGlobalSettings();
                this.loadSources();
                this.loadStats();
            }, 2000);
        } catch (error) {
            this.hideLoadingOverlay();
            this.showNotification('Failed to restart service', 'danger');
            this.startAutoRefresh();
        }
    }

    // showLoadingOverlay displays a modal loading indicator with customizable message,
    // preventing user interaction during long-running operations like service restarts
    // and configuration updates.
    //
    // Parameters:
    //   - message: text to display in the loading overlay
    showLoadingOverlay(message = 'Loading...') {
        const overlay = document.getElementById('loading-overlay');
        overlay.querySelector('div').innerHTML = `
            <div uk-spinner="ratio: 2"></div>
            <div class="uk-margin-small-top">${message}</div>
        `;
        overlay.classList.remove('uk-hidden');
    }

    // hideLoadingOverlay removes the modal loading indicator, restoring normal user
    // interaction capabilities after completion of long-running operations.
    hideLoadingOverlay() {
        document.getElementById('loading-overlay').classList.add('uk-hidden');
    }

    // showNotification displays a temporary notification message using UIKit's
    // notification system with configurable styling based on message type and
    // automatic dismissal after a timeout period.
    //
    // Parameters:
    //   - message: notification text to display
    //   - type: notification style ('primary', 'success', 'warning', 'danger')
    showNotification(message, type = 'primary') {
        UIkit.notification({
            message: message,
            status: type,
            pos: 'top-right',
            timeout: 5000
        });
    }

    // escapeHtml sanitizes text content for safe HTML insertion by converting
    // special characters to HTML entities, preventing XSS attacks and HTML
    // injection vulnerabilities in dynamically generated content.
    //
    // Parameters:
    //   - text: raw text to sanitize
    //
    // Returns:
    //   - string: HTML-safe escaped text
    escapeHtml(text) {
        if (!text) return '';
        const div = document.createElement('div');
        div.textContent = text;
        return div.innerHTML;
    }

    // obfuscateUrl masks sensitive portions of URLs for display purposes, preserving
    // protocol and hostname while hiding paths and query parameters that may contain
    // authentication tokens or private information.
    //
    // Parameters:
    //   - url: complete URL to obfuscate
    //
    // Returns:
    //   - string: partially masked URL safe for display
    obfuscateUrl(url) {
        if (!url) return '';

        try {
            const urlObj = new URL(url);
            let result = urlObj.protocol + '//' + urlObj.hostname;
            if (urlObj.pathname && urlObj.pathname !== '/') {
                result += '/***';
            }
            if (urlObj.search) {
                result += '?***';
            }
            return result;
        } catch {
            return '***OBFUSCATED***';
        }
    }

    // formatBytes converts raw byte counts to human-readable format with appropriate
    // unit prefixes (B, KB, MB, GB, TB) and precision scaling based on magnitude
    // for professional display of file sizes and bandwidth measurements.
    //
    // Parameters:
    //   - bytes: raw byte count to format
    //
    // Returns:
    //   - string: formatted size with unit suffix
    formatBytes(bytes) {
        if (bytes === 0) return '0 B';

        const k = 1024;
        const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
        const i = Math.floor(Math.log(bytes) / Math.log(k));

        return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
    }

    // formatBitrate converts raw bitrate values to human-readable format with
    // appropriate unit prefixes (bps, Kbps, Mbps) and precision for professional
    // display of streaming quality and bandwidth metrics.
    //
    // Parameters:
    //   - bitrate: raw bitrate in bits per second
    //
    // Returns:
    //   - string: formatted bitrate with unit suffix
    formatBitrate(bitrate) {
        if (bitrate < 1000) return bitrate + ' bps';
        if (bitrate < 1000000) return (bitrate / 1000).toFixed(1) + ' Kbps';
        return (bitrate / 1000000).toFixed(2) + ' Mbps';
    }

    // renderStreamSelector displays the stream selection modal with stream cards
    // showing current status, quality information, and controls for stream activation,
    // ordering, and management operations.
    //
    // Parameters:
    //   - data: channel streams data including stream array and status information
    renderStreamSelector(data) {
        document.getElementById('stream-selector-title').textContent = `Select Stream - ${data.channelName}`;

        const container = document.getElementById('stream-selector-content');

        if (data.streams.length === 0) {
            container.innerHTML = '<div class="uk-alert uk-alert-warning">No streams found</div>';
            return;
        }

        this.currentChannelName = data.channelName;
        this.currentStreamData = data.streams;
        this.originalStreamOrder = data.streams.map((_, index) => index);

        container.innerHTML = `
            <div class="uk-margin-bottom">
                <button class="uk-button uk-button-secondary uk-button-small" id="save-stream-order" style="display: none;">
                    <span uk-icon="check"></span> Save Order
                </button>
                <span class="uk-text-small uk-text-muted uk-margin-left">Use arrows to reorder</span>
            </div>
            <div id="streams-container">
                ${this.renderStreamCards(data)}
            </div>
        `;

        const saveButton = document.getElementById('save-stream-order');
        if (saveButton) {
            saveButton.onclick = () => this.saveStreamOrder();
        }
    }

    // renderStreamCards generates HTML for individual stream cards with comprehensive
    // metadata, status indicators, ordering controls, and action buttons for stream
    // management within the stream selector modal.
    //
    // Parameters:
    //   - data: channel streams data containing stream array and status information
    //
    // Returns:
    //   - string: HTML markup for all stream cards
    renderStreamCards(data) {
        const customOrder = data.streams.map((_, i) => i);
        const orderedStreams = customOrder.map(originalIndex => data.streams[originalIndex]);

        return orderedStreams.map((stream, displayIndex) => {
            const originalIndex = stream.index;
            const isDead = stream.attributes['dead'] === 'true';
            const deadReason = stream.attributes['dead_reason'] || 'unknown';
            const reasonText = deadReason === 'manual' ? 'Manually Killed' :
                deadReason === 'auto_blocked' ? 'Auto-Blocked (Too Many Failures)' : 'Dead';
            const cardClass = originalIndex === data.currentStreamIndex ? 'uk-card-primary' :
                isDead ? 'uk-card-secondary' : 'uk-card-default';

            const isFirst = displayIndex === 0;
            const isLast = displayIndex === orderedStreams.length - 1;

            return `
                <div class="uk-card ${cardClass} uk-margin-small stream-card ${isDead ? 'dead-stream' : ''}" data-original-index="${originalIndex}" data-display-index="${displayIndex}">
                    <div class="uk-card-body uk-padding-small">
                        <div class="uk-flex uk-flex-between uk-flex-middle">
                            <div class="uk-flex uk-flex-middle">
                                <div class="uk-flex uk-flex-column uk-margin-small-right">
                                    <button class="stream-order-btn" 
                                            onclick="kptvAdmin.moveStreamUp('${this.escapeHtml(data.channelName)}', ${displayIndex}); return false;"
                                            ${isFirst ? 'disabled' : ''}>
                                        <span uk-icon="icon: chevron-up; ratio: 0.8"></span>
                                    </button>
                                    <button class="stream-order-btn uk-margin-small-top" 
                                            onclick="kptvAdmin.moveStreamDown('${this.escapeHtml(data.channelName)}', ${displayIndex}); return false;"
                                            ${isLast ? 'disabled' : ''}>
                                        <span uk-icon="icon: chevron-down; ratio: 0.8"></span>
                                    </button>
                                </div>
                                <div class="uk-flex-1">
                                    <div class="uk-text-bold">
                                        Stream ${displayIndex + 1} 
                                        ${originalIndex === data.preferredStreamIndex ? '<span class="uk-label uk-label-success uk-margin-small-left">Preferred</span>' : ''}
                                        ${originalIndex === data.currentStreamIndex ? '<span class="uk-label uk-label-primary uk-margin-small-left">Current</span>' : ''}
                                        ${isDead ? `<span class="uk-label uk-label-danger uk-margin-small-left" uk-tooltip="${reasonText}">DEAD</span>` : ''}
                                    </div>
                                    <div class="uk-text-small uk-text-muted">
                                        Source: ${stream.sourceName} (Order: ${stream.sourceOrder})
                                    </div>
                                    <div class="uk-text-small uk-text-muted text-truncate">
                                        ${stream.url}
                                    </div>
                                    ${stream.attributes['tvg-name'] ? `
                                        <div class="uk-text-small">
                                            Name: ${this.escapeHtml(stream.attributes['tvg-name'])}
                                        </div>
                                    ` : ''}
                                    ${stream.attributes['group-title'] ? `
                                        <div class="uk-text-small">
                                            Group: ${this.escapeHtml(stream.attributes['group-title'])}
                                        </div>
                                    ` : ''}
                                </div>
                            </div>
                            <div class="uk-text-right uk-flex uk-flex-middle">
                                <div class="uk-flex uk-flex-middle">
                                    ${isDead ?
                    `<a href="#" class="uk-icon-link uk-text-success" uk-icon="refresh" uk-tooltip="Make Live (${reasonText})" onclick="kptvAdmin.reviveStream('${this.escapeHtml(data.channelName)}', ${originalIndex}); return false;"></a>` :
                    `<a href="#" class="uk-icon-link uk-text-primary uk-margin-small-right" uk-icon="play" uk-tooltip="Activate Stream" onclick="kptvAdmin.selectStream('${this.escapeHtml(data.channelName)}', ${originalIndex}); return false;"></a>
                                        <a href="#" class="uk-icon-link uk-text-danger" uk-icon="ban" uk-tooltip="Mark as Dead" onclick="kptvAdmin.killStream('${this.escapeHtml(data.channelName)}', ${originalIndex}); return false;"></a>`
                }
                                </div>
                            </div>
                        </div>
                    </div>
                </div>
            `;
        }).join('');
    }

    // saveStreamOrder collects the current stream ordering from the card display,
    // validates the order, and submits to the API for persistent storage with
    // immediate application without requiring restart.
    async saveStreamOrder() {
        const streamCards = document.querySelectorAll('.stream-card');
        const newOrder = [];

        // Get the current display order and map back to original indexes
        streamCards.forEach(card => {
            const originalIndex = parseInt(card.getAttribute('data-original-index'));
            newOrder.push(originalIndex);
        });

        try {
            const encodedChannelName = encodeURIComponent(this.currentChannelName);
            const result = await this.apiCall(`/api/channels/${encodedChannelName}/order`, {
                method: 'POST',
                body: JSON.stringify({ streamOrder: newOrder })
            });

            this.showNotification('Stream order saved successfully!', 'success');

            const saveButton = document.getElementById('save-stream-order');
            if (saveButton) {
                saveButton.style.display = 'none';
            }

        } catch (error) {
            this.showNotification('Failed to save stream order: ' + error.message, 'danger');
        }
    }

    // selectStream activates a specific stream for a channel by sending a stream
    // change request to the API, triggering immediate failover for active streams
    // while updating preferred stream configuration for future connections.
    //
    // Parameters:
    //   - channelName: name of channel containing the stream
    //   - streamIndex: original index of stream to activate
    async selectStream(channelName, streamIndex) {
        try {
            const encodedChannelName = encodeURIComponent(channelName);
            await this.apiCall(`/api/channels/${encodedChannelName}/stream`, {
                method: 'POST',
                body: JSON.stringify({ streamIndex: streamIndex })
            });

            this.showNotification(`Stream changed to index ${streamIndex} for ${channelName}`, 'success');

            // Wait a moment then refresh the stream selector
            setTimeout(() => {
                this.showStreamSelector(channelName);
            }, 2000);

            // Refresh active channels to show updated status
            this.loadActiveChannels();
        } catch (error) {
            this.showNotification('Failed to change stream', 'danger');
        }
    }

    // killStream marks a stream as dead in the persistent database after user
    // confirmation, preventing future use of the problematic stream and updating
    // the stream selector display to reflect the dead status.
    //
    // Parameters:
    //   - channelName: name of channel containing the stream
    //   - streamIndex: original index of stream to mark dead
    async killStream(channelName, streamIndex) {
        if (!confirm('Are you sure you want to mark this stream as dead? It will not be used for playback.')) {
            return;
        }

        try {
            const encodedChannelName = encodeURIComponent(channelName);
            await this.apiCall(`/api/channels/${encodedChannelName}/kill-stream`, {
                method: 'POST',
                body: JSON.stringify({ streamIndex: streamIndex })
            });

            this.showNotification(`Stream ${streamIndex + 1} marked as dead for ${channelName}`, 'warning');

            // Refresh the stream selector
            setTimeout(() => {
                this.showStreamSelector(channelName);
            }, 1000);

        } catch (error) {
            this.showNotification('Failed to mark stream as dead', 'danger');
        }
    }

    // reviveStream removes a stream from the dead streams database, restoring it
    // to active status and updating the stream selector display to show the stream
    // as available for use in failover operations.
    //
    // Parameters:
    //   - channelName: name of channel containing the stream
    //   - streamIndex: original index of stream to revive
    async reviveStream(channelName, streamIndex) {
        try {
            const encodedChannelName = encodeURIComponent(channelName);
            await this.apiCall(`/api/channels/${encodedChannelName}/revive-stream`, {
                method: 'POST',
                body: JSON.stringify({ streamIndex: streamIndex })
            });

            this.showNotification(`Stream ${streamIndex + 1} revived for ${channelName}`, 'success');

            // Refresh the stream selector
            setTimeout(() => {
                this.showStreamSelector(channelName);
            }, 1000);

        } catch (error) {
            this.showNotification('Failed to revive stream', 'danger');
        }
    }

    // toggleWatcher sends a request to enable or disable the stream watcher system,
    // updating the configuration persistently and managing watcher state immediately
    // without requiring application restart for seamless operation.
    //
    // Parameters:
    //   - enable: boolean indicating whether to enable (true) or disable (false) watcher
    async toggleWatcher(enable) {
        try {
            const result = await this.apiCall('/api/watcher/toggle', {
                method: 'POST',
                body: JSON.stringify({ enabled: enable })
            });

            const status = enable ? 'enabled' : 'disabled';
            this.showNotification(`Stream watcher ${status} successfully!`, 'success');

            // Update the checkbox to reflect the change
            const checkbox = document.querySelector('input[name="watcherEnabled"]');
            if (checkbox) {
                checkbox.checked = enable;
            }

            // Reload stats to update the display
            this.loadStats();

            return result;
        } catch (error) {
            this.showNotification(`Failed to ${enable ? 'enable' : 'disable'} watcher: ` + error.message, 'danger');

            // Revert checkbox state on error
            const checkbox = document.querySelector('input[name="watcherEnabled"]');
            if (checkbox) {
                checkbox.checked = !enable;
            }

            throw error;
        }
    }

    // moveStreamUp shifts a stream one position earlier in the display order,
    // swapping it with the preceding stream and triggering re-render with
    // save button display for user confirmation.
    //
    // Parameters:
    //   - channelName: name of channel containing the stream
    //   - displayIndex: current position of stream to move up
    moveStreamUp(channelName, displayIndex) {
        if (displayIndex === 0) return;

        const container = document.getElementById('streams-container');
        const cards = Array.from(container.querySelectorAll('.stream-card'));

        const currentOrder = cards.map(card => parseInt(card.getAttribute('data-original-index')));

        [currentOrder[displayIndex], currentOrder[displayIndex - 1]] = [currentOrder[displayIndex - 1], currentOrder[displayIndex]];

        document.getElementById('save-stream-order').style.display = 'inline-block';

        const data = {
            channelName: channelName,
            currentStreamIndex: this.getCurrentStreamIndex(),
            preferredStreamIndex: this.getPreferredStreamIndex(),
            streams: currentOrder.map(originalIndex => this.currentStreamData[originalIndex])
        };

        container.innerHTML = this.renderStreamCards(data);
    }

    // moveStreamDown shifts a stream one position later in the display order,
    // swapping it with the following stream and triggering re-render with
    // save button display for user confirmation.
    //
    // Parameters:
    //   - channelName: name of channel containing the stream
    //   - displayIndex: current position of stream to move down
    moveStreamDown(channelName, displayIndex) {
        const container = document.getElementById('streams-container');
        const cards = Array.from(container.querySelectorAll('.stream-card'));

        if (displayIndex >= cards.length - 1) return;

        const currentOrder = cards.map(card => parseInt(card.getAttribute('data-original-index')));

        [currentOrder[displayIndex], currentOrder[displayIndex + 1]] = [currentOrder[displayIndex + 1], currentOrder[displayIndex]];

        document.getElementById('save-stream-order').style.display = 'inline-block';

        const data = {
            channelName: channelName,
            currentStreamIndex: this.getCurrentStreamIndex(),
            preferredStreamIndex: this.getPreferredStreamIndex(),
            streams: currentOrder.map(originalIndex => this.currentStreamData[originalIndex])
        };

        container.innerHTML = this.renderStreamCards(data);
    }

    // refreshStreamCards re-renders the stream card display using current ordering
    // state, updating the visual representation after reordering operations while
    // preserving current stream and preferred stream indicators.
    refreshStreamCards() {
        const container = document.getElementById('streams-container');
        const cards = Array.from(container.querySelectorAll('.stream-card'));
        const currentOrder = cards.map(card => parseInt(card.getAttribute('data-original-index')));

        // Create data object with current order
        const data = {
            channelName: this.currentChannelName,
            currentStreamIndex: parseInt(document.querySelector('.uk-card-primary')?.getAttribute('data-original-index') || 0),
            preferredStreamIndex: this.currentStreamData[0].index,
            streams: currentOrder.map(originalIndex => this.currentStreamData[originalIndex])
        };

        container.innerHTML = this.renderStreamCards(data);
    }

    // getCurrentStreamIndex extracts the original stream index from the currently
    // active stream card identified by primary styling, providing accurate stream
    // identification across reordering operations.
    //
    // Returns:
    //   - number: original index of currently active stream
    getCurrentStreamIndex() {
        const currentCard = document.querySelector('.uk-card-primary');
        return currentCard ? parseInt(currentCard.getAttribute('data-original-index')) : 0;
    }

    // getPreferredStreamIndex determines the original index of the preferred stream
    // from the current stream data array, enabling proper preferred stream indicator
    // display during reordering operations.
    //
    // Returns:
    //   - number: original index of preferred stream
    getPreferredStreamIndex() {
        return this.currentStreamData.findIndex(s => s.index === this.currentStreamData[0].index);
    }

}

// Initialize the admin interface when the page loads
let kptvAdmin;
document.addEventListener('DOMContentLoaded', () => {
    kptvAdmin = new KPTVAdmin();
});