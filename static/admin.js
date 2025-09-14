// KPTV Proxy Admin Interface JavaScript

class KPTVAdmin {
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

    init() {
        // Load initial data
        this.loadGlobalSettings();
        this.loadSources();
        this.loadStats();
        this.loadActiveChannels();
        this.loadAllChannels();
        this.loadLogs();

        // Setup event listeners
        this.setupEventListeners();

        // Start auto-refresh
        this.startAutoRefresh();
    }

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

    // pagination methods:
    previousPage() {
        if (this.currentPage > 1) {
            this.currentPage--;
            this.renderCurrentPage();
        }
    }
    nextPage() {
        const totalPages = Math.ceil((this.filteredChannels || this.allChannels || []).length / this.pageSize);
        if (this.currentPage < totalPages) {
            this.currentPage++;
            this.renderCurrentPage();
        }
    }
    goToFirstPage() {
        if (this.currentPage !== 1) {
            this.currentPage = 1;
            this.renderCurrentPage();
        }
    }
    goToLastPage() {
        const totalPages = Math.ceil((this.filteredChannels || this.allChannels || []).length / this.pageSize);
        if (this.currentPage !== totalPages && totalPages > 0) {
            this.currentPage = totalPages;
            this.renderCurrentPage();
        }
    }
    goToPage(page) {
        const totalPages = Math.ceil((this.filteredChannels || this.allChannels || []).length / this.pageSize);
        if (page >= 1 && page <= totalPages && page !== this.currentPage) {
            this.currentPage = page;
            this.renderCurrentPage();
        }
    }

    renderCurrentPage() {
        const channels = this.filteredChannels || this.allChannels;
        if (!channels) return;
        
        const startIndex = (this.currentPage - 1) * this.pageSize;
        const endIndex = startIndex + this.pageSize;
        const pageChannels = channels.slice(startIndex, endIndex);
        
        this.renderAllChannels(pageChannels);
        this.updatePaginationInfo();
    }

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

    stopAutoRefresh() {
        if (this.refreshInterval) {
            clearInterval(this.refreshInterval);
        }
    }

    // API Methods
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

    // Global Settings
    async loadGlobalSettings() {
        try {
            const config = await this.apiCall('/api/config');
            this.config = config;
            this.populateGlobalSettingsForm(config);
        } catch (error) {
            // Fallback to default values if API fails
            this.populateGlobalSettingsForm({
                baseURL: "http://localhost:8080",
                maxBufferSize: 256,
                bufferSizePerStream: 16,
                cacheEnabled: true,
                cacheDuration: "30m",
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

    async saveGlobalSettings() {
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

        // Add unchecked checkboxes
        form.querySelectorAll('input[type="checkbox"]').forEach(checkbox => {
            if (!formData.has(checkbox.name)) {
                newConfig[checkbox.name] = false;
            }
        });

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
    
    // Sources Management
    async loadSources() {
        try {
            const config = await this.apiCall('/api/config');
            this.renderSources(config.sources || []);
        } catch (error) {
            document.getElementById('sources-container').innerHTML = 
                '<div class="uk-alert uk-alert-danger">Failed to load sources</div>';
        }
    }

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
                        <span class="status-indicator ${this.getSourceStatus(source)}"></span>
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

    getSourceStatus(source) {
        // This would typically come from real-time data
        // For now, return a default status
        return 'status-active';
    }

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
    

    editSource(index) {
        this.showSourceModal(index);
    }

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

    // Statistics
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

    updateStatsDisplay(stats) {
        // Update main stats cards
        const totalChannelsEl = document.getElementById('total-channels');
        const activeStreamsEl = document.getElementById('active-streams');
        const totalSourcesEl = document.getElementById('total-sources');
        const connectedClientsEl = document.getElementById('connected-clients');

        if (totalChannelsEl) totalChannelsEl.textContent = stats.totalChannels || 0;
        if (activeStreamsEl) activeStreamsEl.textContent = stats.activeStreams || 0;
        if (totalSourcesEl) totalSourcesEl.textContent = stats.totalSources || 0;
        if (connectedClientsEl) connectedClientsEl.textContent = stats.connectedClients || 0;

        // Update system status
        const uptimeEl = document.getElementById('uptime');
        const memoryUsageEl = document.getElementById('memory-usage');
        const cacheStatusEl = document.getElementById('cache-status');
        
        if (uptimeEl) uptimeEl.textContent = stats.uptime || '0m';
        if (memoryUsageEl) memoryUsageEl.textContent = stats.memoryUsage || '0 MB';
        if (cacheStatusEl) cacheStatusEl.textContent = stats.cacheStatus || 'Unknown';

        // Update traffic stats
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

        // Update watcher status
        const watcherStatus = stats.watcherEnabled ? 'Enabled' : 'Disabled';
        const watcherElement = document.getElementById('watcher-status');
        if (watcherElement) {
            watcherElement.textContent = watcherStatus;
            watcherElement.className = `watcher-status ${stats.watcherEnabled ? 'enabled' : 'disabled'}`;
        }
    }

    // Channels
    async loadActiveChannels() {
        try {
            const channels = await this.apiCall('/api/channels/active');
            this.renderActiveChannels(channels);
        } catch (error) {
            document.getElementById('active-channels-list').innerHTML = 
                '<div class="uk-alert uk-alert-warning">No active channels or failed to load</div>';
        }
    }

    renderActiveChannels(channels) {
        const container = document.getElementById('active-channels-list');
        
        if (channels.length === 0) {
            container.innerHTML = '<div class="uk-alert uk-alert-warning">No active channels</div>';
            return;
        }

        container.innerHTML = channels.map(channel => `
            <div class="channel-item fade-in">
                <div class="uk-flex uk-flex-between uk-flex-middle">
                    <div class="uk-flex-1">
                        <div class="channel-name">${this.escapeHtml(channel.name)}</div>
                        <div class="channel-details">
                            <span class="connection-dot status-active"></span>
                            ${channel.clients || 0} client(s) connected
                        </div>
                    </div>
                    <div class="uk-text-right uk-text-small">
                        <div class="uk-text-muted">Bytes: ${this.formatBytes(channel.bytesTransferred || 0)}</div>
                        <div class="uk-text-muted">Source: ${channel.currentSource || 'Unknown'}</div>
                        <button class="uk-button uk-button-secondary uk-button-small uk-margin-small-top" onclick="kptvAdmin.showStreamSelector('${this.escapeHtml(channel.name)}')">
                            <span uk-icon="settings"></span> Streams
                        </button>
                    </div>
                </div>
            </div>
        `).join('');
    }

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

    renderAllChannels(channels) {
        const container = document.getElementById('all-channels-list');
        
        if (channels.length === 0) {
            container.innerHTML = '<div class="uk-alert uk-alert-warning">No channels found</div>';
            return;
        }

        // Use document fragment for batch DOM insertion
        const fragment = document.createDocumentFragment();
        
        channels.forEach(channel => {
            const div = document.createElement('div');
            div.className = `channel-item ${channel.active ? '' : 'channel-inactive'} fade-in`;
            div.innerHTML = `
                <div class="uk-flex uk-flex-between uk-flex-middle">
                    <div class="uk-flex-1">
                        <div class="channel-name">${this.escapeHtml(channel.name)}</div>
                        <div class="channel-details">
                            <div class="uk-text-small uk-text-muted">
                                Group: ${channel.group || 'Uncategorized'} | 
                                Sources: ${channel.sources || 0} | 
                                Status: ${channel.active ? `Active (${channel.clients} clients)` : 'Inactive'}
                            </div>
                        </div>
                    </div>
                    <div class="uk-text-right">
                        <span class="status-indicator ${channel.active ? 'status-active' : 'status-error'}"></span>
                        <button class="uk-button uk-button-secondary uk-button-small uk-margin-small-left" onclick="kptvAdmin.showStreamSelector('${this.escapeHtml(channel.name)}')">
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
    }
    

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

    // Logs
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

    filterLogs(level) {
        if (!this.allLogs) return;
        
        if (level === 'all') {
            this.renderLogs(this.allLogs);
        } else {
            const filtered = this.allLogs.filter(log => log.level === level);
            this.renderLogs(filtered);
        }
    }

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

    // Service Management
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

    // Utility Methods
    showLoadingOverlay(message = 'Loading...') {
        const overlay = document.getElementById('loading-overlay');
        overlay.querySelector('div').innerHTML = `
            <div uk-spinner="ratio: 2"></div>
            <div class="uk-margin-small-top">${message}</div>
        `;
        overlay.classList.remove('uk-hidden');
    }

    hideLoadingOverlay() {
        document.getElementById('loading-overlay').classList.add('uk-hidden');
    }

    showNotification(message, type = 'primary') {
        UIkit.notification({
            message: message,
            status: type,
            pos: 'top-right',
            timeout: 5000
        });
    }

    escapeHtml(text) {
        if (!text) return '';
        const div = document.createElement('div');
        div.textContent = text;
        return div.innerHTML;
    }

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

    formatBytes(bytes) {
        if (bytes === 0) return '0 B';
        
        const k = 1024;
        const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
        const i = Math.floor(Math.log(bytes) / Math.log(k));
        
        return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
    }

    async showStreamSelector(channelName) {
        try {
            // Properly encode the channel name for the URL
            const encodedChannelName = encodeURIComponent(channelName);
            const data = await this.apiCall(`/api/channels/${encodedChannelName}/streams`);
            this.renderStreamSelector(data);
            UIkit.modal('#stream-selector-modal').show();
        } catch (error) {
            this.showNotification('Failed to load streams for channel', 'danger');
        }
    }

    renderStreamSelector(data) {
        document.getElementById('stream-selector-title').textContent = `Select Stream - ${data.channelName}`;
        
        const container = document.getElementById('stream-selector-content');
        
        if (data.streams.length === 0) {
            container.innerHTML = '<div class="uk-alert uk-alert-warning">No streams found</div>';
            return;
        }
        
        container.innerHTML = data.streams.map((stream, index) => {
            const isDead = stream.attributes['dead'] === 'true';
            const deadReason = stream.attributes['dead_reason'] || 'unknown';
            const reasonText = deadReason === 'manual' ? 'Manually Killed' : 
                            deadReason === 'auto_blocked' ? 'Auto-Blocked (Too Many Failures)' : 
                            'Dead';
            const cardClass = index === data.currentStreamIndex ? 'uk-card-primary' : 
                            isDead ? 'uk-card-secondary' : 'uk-card-default';
            
            return `
                <div class="uk-card ${cardClass} uk-margin-small ${isDead ? 'dead-stream' : ''}">
                    <div class="uk-card-body uk-padding-small">
                        <div class="uk-flex uk-flex-between uk-flex-middle">
                            <div class="uk-flex-1">
                                <div class="uk-text-bold">
                                    Stream ${index + 1} 
                                    ${index === data.preferredStreamIndex ? '<span class="uk-label uk-label-success uk-margin-small-left">Preferred</span>' : ''}
                                    ${index === data.currentStreamIndex ? '<span class="uk-label uk-label-primary uk-margin-small-left">Current</span>' : ''}
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
                            <div class="uk-text-right uk-flex uk-flex-middle">
                                <div class="uk-flex uk-flex-middle">
                                    ${isDead ? 
                                        `<a href="#" class="uk-icon-link uk-text-success" uk-icon="refresh" uk-tooltip="Make Live (${reasonText})" onclick="kptvAdmin.reviveStream('${data.channelName}', ${index}); return false;"></a>` :
                                        `<a href="#" class="uk-icon-link uk-text-primary uk-margin-small-right" uk-icon="play" uk-tooltip="Activate Stream" onclick="kptvAdmin.selectStream('${data.channelName}', ${index}); return false;"></a>
                                        <a href="#" class="uk-icon-link uk-text-danger" uk-icon="ban" uk-tooltip="Mark as Dead" onclick="kptvAdmin.killStream('${data.channelName}', ${index}); return false;"></a>`
                                    }
                                </div>
                            </div>
                        </div>
                    </div>
                </div>
            `;
        }).join('');
    }

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

    // Watcher Management (add this method to KPTVAdmin class)
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

}

// Initialize the admin interface when the page loads
let kptvAdmin;
document.addEventListener('DOMContentLoaded', () => {
    kptvAdmin = new KPTVAdmin();
});