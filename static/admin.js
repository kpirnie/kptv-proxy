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
        this.loadGlobalSettings();
        this.loadSources();
        this.loadEPGs();
        this.loadStats();
        this.loadActiveChannels();
        this.loadAllChannels();
        this.loadLogs();
        this.initScrollToTop();
        this.initTabs();
        this.initModals();
        this.setupEventListeners();
        this.startAutoRefresh();
        document.getElementById('footer-year').textContent = new Date().getFullYear();
    }

    initScrollToTop() {
        const scrollBtn = document.getElementById('scroll-to-top');
        if (scrollBtn) {
            window.addEventListener('scroll', () => {
                if (window.scrollY > 100) {
                    scrollBtn.classList.add('visible');
                } else {
                    scrollBtn.classList.remove('visible');
                }
            });
            scrollBtn.addEventListener('click', (e) => {
                e.preventDefault();
                window.scrollTo({ top: 0, behavior: 'smooth' });
            });
        }
    }

    initTabs() {
        const tabBtns = document.querySelectorAll('.tab-btn');
        const tabContents = document.querySelectorAll('.tab-content > div');

        tabBtns.forEach(btn => {
            btn.addEventListener('click', () => {
                const tabIndex = btn.getAttribute('data-tab');

                tabBtns.forEach(b => {
                    b.classList.remove('active', 'border-kptv-blue', 'text-kptv-blue');
                    b.classList.add('border-transparent');
                });

                btn.classList.add('active', 'border-kptv-blue', 'text-kptv-blue');
                btn.classList.remove('border-transparent');

                tabContents.forEach(content => content.classList.remove('active'));
                tabContents[tabIndex].classList.add('active');
            });
        });

        document.querySelectorAll('.source-tab-btn').forEach(btn => {
            btn.addEventListener('click', () => {
                const tabIndex = btn.getAttribute('data-tab');
                const sourceTabBtns = document.querySelectorAll('.source-tab-btn');
                const sourceTabContents = document.querySelectorAll('.source-tab-content > div');

                sourceTabBtns.forEach(b => {
                    b.classList.remove('active', 'border-kptv-blue', 'text-kptv-blue');
                    b.classList.add('border-transparent');
                });

                btn.classList.add('active', 'border-kptv-blue', 'text-kptv-blue');
                btn.classList.remove('border-transparent');

                sourceTabContents.forEach(content => content.classList.remove('active'));
                sourceTabContents[tabIndex].classList.add('active');
            });
        });
    }

    initModals() {
        const modals = [
            { id: 'source-modal', closeId: 'close-source-modal', cancelId: 'cancel-source-btn' },
            { id: 'epg-modal', closeId: 'close-epg-modal', cancelId: 'cancel-epg-btn' },
            { id: 'stream-selector-modal', closeId: 'close-stream-selector-modal', cancelId: 'cancel-stream-selector-btn' }
        ];

        modals.forEach(({ id, closeId, cancelId }) => {
            const modal = document.getElementById(id);
            const closeBtn = document.getElementById(closeId);
            const cancelBtn = document.getElementById(cancelId);

            if (closeBtn) closeBtn.addEventListener('click', () => this.hideModal(id));
            if (cancelBtn) cancelBtn.addEventListener('click', () => this.hideModal(id));

            modal.addEventListener('click', (e) => {
                if (e.target === modal) this.hideModal(id);
            });
        });
    }

    showModal(modalId) {
        document.getElementById(modalId).classList.add('active');
    }

    hideModal(modalId) {
        document.getElementById(modalId).classList.remove('active');
    }

    setupEventListeners() {
        document.getElementById('global-settings-form').addEventListener('submit', (e) => {
            e.preventDefault();
            this.saveGlobalSettings();
        });
        document.getElementById('load-global-settings').addEventListener('click', () => {
            this.loadGlobalSettings();
        });

        document.getElementById('restart-btn').addEventListener('click', () => {
            this.restartService();
        });

        document.getElementById('add-source-btn').addEventListener('click', () => {
            this.showSourceModal();
        });

        document.getElementById('save-source-btn').addEventListener('click', () => {
            this.saveSource();
        });

        document.getElementById('add-epg-btn').addEventListener('click', () => {
            this.showEPGModal();
        });

        document.getElementById('save-epg-btn').addEventListener('click', () => {
            this.saveEPG();
        });

        document.getElementById('refresh-channels').addEventListener('click', () => {
            this.loadActiveChannels();
        });
        document.getElementById('refresh-all-channels').addEventListener('click', () => {
            this.currentPage = 1;
            this.filteredChannels = null;
            document.getElementById('channel-search').value = '';
            this.loadAllChannels();
        });
        document.getElementById('refresh-logs').addEventListener('click', () => {
            this.loadLogs();
        });

        document.getElementById('clear-logs').addEventListener('click', () => {
            this.clearLogs();
        });

        document.getElementById('channel-search').addEventListener('input', (e) => {
            this.currentPage = 1;
            this.filterChannels(e.target.value);
        });

        document.getElementById('log-level').addEventListener('change', (e) => {
            this.filterLogs(e.target.value);
        });

        const watcherCheckbox = document.querySelector('input[name="watcherEnabled"]');
        if (watcherCheckbox) {
            watcherCheckbox.addEventListener('change', (e) => {
                this.toggleWatcher(e.target.checked);
            });
        }

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

        const pageSelector = document.getElementById('page-selector');
        if (pageSelector) {
            pageSelector.innerHTML = '';
            for (let i = 1; i <= totalPages; i++) {
                const option = document.createElement('option');
                option.value = i;
                option.textContent = i;
                option.selected = i === this.currentPage;
                pageSelector.appendChild(option);
            }
        }

        const firstBtn = document.getElementById('first-page').parentElement;
        const prevBtn = document.getElementById('prev-page').parentElement;
        const nextBtn = document.getElementById('next-page').parentElement;
        const lastBtn = document.getElementById('last-page').parentElement;

        if (this.currentPage === 1) {
            firstBtn.classList.add('opacity-50', 'pointer-events-none');
            prevBtn.classList.add('opacity-50', 'pointer-events-none');
        } else {
            firstBtn.classList.remove('opacity-50', 'pointer-events-none');
            prevBtn.classList.remove('opacity-50', 'pointer-events-none');
        }

        if (this.currentPage === totalPages || totalPages === 0) {
            nextBtn.classList.add('opacity-50', 'pointer-events-none');
            lastBtn.classList.add('opacity-50', 'pointer-events-none');
        } else {
            nextBtn.classList.remove('opacity-50', 'pointer-events-none');
            lastBtn.classList.remove('opacity-50', 'pointer-events-none');
        }
    }

    startAutoRefresh() {
        this.refreshInterval = setInterval(() => {
            this.loadStats();
            this.loadActiveChannels();

            const searchInput = document.getElementById('channel-search');
            const currentSearch = searchInput ? searchInput.value.trim() : '';
            const currentPage = this.currentPage;

            this.loadAllChannels().then(() => {
                if (currentSearch) {
                    this.filteredChannels = this.allChannels.filter(channel =>
                        channel.name.toLowerCase().includes(currentSearch.toLowerCase()) ||
                        (channel.group && channel.group.toLowerCase().includes(currentSearch.toLowerCase()))
                    );
                } else {
                    this.filteredChannels = null;
                }

                this.currentPage = currentPage;
                const totalPages = Math.ceil((this.filteredChannels || this.allChannels || []).length / this.pageSize);

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

    async loadGlobalSettings() {
        try {
            const config = await this.apiCall('/api/config');
            this.config = config;
            this.populateGlobalSettingsForm(config);
        } catch (error) {
            this.populateGlobalSettingsForm({
                baseURL: "http://localhost:8080",
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

        const watcherCheckbox = form.querySelector('input[name="watcherEnabled"]');
        if (watcherCheckbox && config.watcherEnabled !== undefined) {
            watcherCheckbox.checked = config.watcherEnabled;
        }

        if (config.logLevel) {
            const logLevelRadios = form.querySelectorAll('input[name="logLevel"]');
            logLevelRadios.forEach(radio => {
                radio.checked = radio.value === config.logLevel.toUpperCase();
            });
        } else {
            const infoRadio = form.querySelector('input[name="logLevel"][value="INFO"]');
            if (infoRadio) infoRadio.checked = true;
        }

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
            } else if (input.type === 'radio') {
                newConfig[key] = value;
            } else {
                newConfig[key] = value;
            }
        }

        form.querySelectorAll('input[type="checkbox"]').forEach(checkbox => {
            if (!formData.has(checkbox.name)) {
                newConfig[checkbox.name] = false;
            }
        });

        newConfig.debug = true;

        if (!newConfig.logLevel) {
            const checkedLogLevel = form.querySelector('input[name="logLevel"]:checked');
            newConfig.logLevel = checkedLogLevel ? checkedLogLevel.value : 'INFO';
        }

        const ffmpegModeCheckbox = form.querySelector('input[name="ffmpegMode"]');
        if (ffmpegModeCheckbox) {
            newConfig.ffmpegMode = ffmpegModeCheckbox.checked;
        }

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
            const currentConfig = await this.apiCall('/api/config');

            const mergedConfig = {
                ...currentConfig,
                ...newConfig
            };

            if (currentConfig.sources) {
                mergedConfig.sources = currentConfig.sources;
            }

            await this.apiCall('/api/config', {
                method: 'POST',
                body: JSON.stringify(mergedConfig)
            });

            this.showNotification('Global settings saved successfully!', 'success');

            setTimeout(() => {
                this.restartService();
            }, 1000);
        } catch (error) {
            this.showNotification('Failed to save global settings: ' + error.message, 'danger');
        }
    }

    async loadSources() {
        try {
            const config = await this.apiCall('/api/config');
            this.renderSources(config.sources || []);
        } catch (error) {
            document.getElementById('sources-container').innerHTML =
                '<div class="bg-orange-900/20 border border-orange-600 text-orange-100 px-4 py-3 rounded">Failed to load sources</div>';
        }
    }

    async loadEPGs() {
        try {
            const config = await this.apiCall('/api/config');
            this.renderEPGs(config.epgs || []);
        } catch (error) {
            document.getElementById('epgs-container').innerHTML =
                '<div class="bg-orange-900/20 border border-orange-600 text-orange-100 px-4 py-3 rounded">Failed to load EPGs</div>';
        }
    }

    renderEPGs(epgs) {
        const container = document.getElementById('epgs-container');

        if (epgs.length === 0) {
            container.innerHTML = '<div class="bg-orange-900/20 border border-orange-600 text-orange-100 px-4 py-3 rounded">No EPGs configured</div>';
            return;
        }

        container.innerHTML = epgs.map((epg, index) => `
        <div class="source-item">
            <div class="flex justify-between items-center">
                <div class="flex-1">
                    <h4 class="text-lg font-semibold mb-1">${this.escapeHtml(epg.name)}</h4>
                    <div class="text-gray-400 text-sm">${this.obfuscateUrl(epg.url)}</div>
                </div>
                <div class="flex items-center">
                    <span class="status-indicator status-active"></span>
                    <span class="text-sm text-gray-400">Order: ${epg.order}</span>
                </div>
            </div>
            <div class="mt-4 pt-4 border-t border-kptv-border flex gap-2">
                <button class="px-3 py-1 bg-kptv-blue hover:bg-kptv-blue-light rounded text-sm transition-colors flex items-center space-x-1" onclick="kptvAdmin.editEPG(${index})">
                    <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z"></path>
                    </svg>
                    <span>Edit</span>
                </button>
                <button class="px-3 py-1 bg-red-700 hover:bg-red-600 rounded text-sm transition-colors flex items-center space-x-1" onclick="kptvAdmin.deleteEPG(${index})">
                    <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16"></path>
                    </svg>
                    <span>Delete</span>
                </button>
            </div>
        </div>
    `).join('');
    }

    showEPGModal(epgIndex = null) {
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

        this.showModal('epg-modal');
    }

    populateEPGForm(epg, index) {
        document.getElementById('epg-index').value = index;
        document.getElementById('epg-name').value = epg.name || '';
        document.getElementById('epg-url').value = epg.url || '';
        document.getElementById('epg-order').value = epg.order || 1;
    }

    clearEPGForm() {
        document.getElementById('epg-index').value = '';
        document.getElementById('epg-form').reset();
        document.getElementById('epg-order').value = 1;
    }

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

            this.hideModal('epg-modal');
            this.showNotification('EPG saved successfully!', 'success');
            this.loadEPGs();

            setTimeout(() => {
                this.restartService();
            }, 500);

        } catch (error) {
            this.showNotification('Failed to save EPG: ' + error.message, 'danger');
        }
    }

    editEPG(index) {
        this.showEPGModal(index);
    }

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

            setTimeout(() => {
                this.restartService();
            }, 500);

        } catch (error) {
            this.showNotification('Failed to delete EPG', 'danger');
        }
    }

    renderSources(sources) {
        const container = document.getElementById('sources-container');

        if (sources.length === 0) {
            container.innerHTML = '<div class="bg-orange-900/20 border border-orange-600 text-orange-100 px-4 py-3 rounded">No sources configured</div>';
            return;
        }

        container.innerHTML = sources.map((source, index) => `
            <div class="source-item">
                <div class="flex justify-between items-center mb-3">
                    <div class="flex-1">
                        <h4 class="text-lg font-semibold mb-1">${this.escapeHtml(source.name)}</h4>
                        <div class="text-gray-400 text-sm">${this.obfuscateUrl(source.url)}</div>
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
                        <div class="text-sm text-truncate">${this.escapeHtml(source.userAgent)}</div>
                    </div>
                ` : ''}
                ${source.reqOrigin ? `
                    <div class="mb-2">
                        <div class="text-sm text-gray-400">Origin</div>
                        <div class="text-sm text-truncate">${this.escapeHtml(source.reqOrigin)}</div>
                    </div>
                ` : ''}
                ${source.reqReferrer ? `
                    <div class="mb-2">
                        <div class="text-sm text-gray-400">Referrer</div>
                        <div class="text-sm text-truncate">${this.escapeHtml(source.reqReferrer)}</div>
                    </div>
                ` : ''}
                <div class="mt-4 pt-4 border-t border-kptv-border flex gap-2">
                    <button class="px-3 py-1 bg-kptv-blue hover:bg-kptv-blue-light rounded text-sm transition-colors flex items-center space-x-1" onclick="kptvAdmin.editSource(${index})">
                        <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z"></path>
                        </svg>
                        <span>Edit</span>
                    </button>
                    <button class="px-3 py-1 bg-red-700 hover:bg-red-600 rounded text-sm transition-colors flex items-center space-x-1" onclick="kptvAdmin.deleteSource(${index})">
                        <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16"></path>
                        </svg>
                        <span>Delete</span>
                    </button>
                </div>
            </div>
        `).join('');
    }

    showSourceModal(sourceIndex = null) {
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

        this.showModal('source-modal');
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
                this.showNotification('Name and URL are required', 'danger');
                return;
            }

            const config = await this.apiCall('/api/config');

            if (!config.sources) {
                config.sources = [];
            }

            if (index === '') {
                config.sources.push(source);
            } else {
                config.sources[parseInt(index)] = source;
            }

            await this.apiCall('/api/config', {
                method: 'POST',
                body: JSON.stringify(config)
            });

            this.hideModal('source-modal');
            this.showNotification('Source saved successfully!', 'success');
            this.loadSources();

            setTimeout(() => {
                this.restartService();
            }, 500);

        } catch (error) {
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

            setTimeout(() => {
                this.restartService();
            }, 1000);
        } catch (error) {
            this.showNotification('Failed to delete source', 'danger');
        }
    }

    async loadStats() {
        try {
            const stats = await this.apiCall('/api/stats');
            this.updateStatsDisplay(stats);
        } catch (error) {
            this.updateStatsDisplay({
                totalChannels: 0,
                activeStreams: 0,
                totalSources: 0,
                totalEpgs: 0,
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
        const elements = {
            'total-channels': stats.totalChannels || 0,
            'active-streams': stats.activeStreams || 0,
            'total-sources': stats.totalSources || 0,
            'total-epgs': stats.totalEpgs || 0,
            'connected-clients': stats.connectedClients || 0,
            'uptime': stats.uptime || '0m',
            'memory-usage': stats.memoryUsage || '0 MB',
            'cache-status': stats.cacheStatus || 'Unknown',
            'total-connections': stats.totalConnections || 0,
            'bytes-transferred': stats.bytesTransferred || '0 B',
            'active-restreamers': stats.activeRestreamers || 0,
            'stream-errors': stats.streamErrors || 0,
            'response-time': stats.responseTime || '0ms',
            'proxy-clients': stats.connectedClients || 0,
            'upstream-connections': stats.upstreamConnections || 0,
            'active-channels-stat': stats.activeChannels || 0
        };

        Object.entries(elements).forEach(([id, value]) => {
            const el = document.getElementById(id);
            if (el) el.textContent = value;
        });

        const watcherStatus = stats.watcherEnabled ? 'Enabled' : 'Disabled';
        const watcherElement = document.getElementById('watcher-status');
        if (watcherElement) {
            watcherElement.textContent = watcherStatus;
        }

        const connectionEfficiencyEl = document.getElementById('connection-efficiency');
        if (connectionEfficiencyEl) {
            const clients = stats.connectedClients || 0;
            const upstream = stats.upstreamConnections || 0;
            if (upstream > 0) {
                connectionEfficiencyEl.textContent = `${(clients / upstream).toFixed(1)}:1`;
            } else {
                connectionEfficiencyEl.textContent = 'N/A';
            }
        }

        const avgClientsPerStreamEl = document.getElementById('avg-clients-per-stream');
        if (avgClientsPerStreamEl) {
            if (stats.avgClientsPerStream > 0) {
                avgClientsPerStreamEl.textContent = stats.avgClientsPerStream.toFixed(1);
            } else {
                avgClientsPerStreamEl.textContent = 'N/A';
            }
        }
    }

    async loadActiveChannels() {
        try {
            const channels = await this.apiCall('/api/channels/active');
            this.renderActiveChannels(channels);
        } catch (error) {
            document.getElementById('active-channels-list').innerHTML =
                '<div class="bg-orange-900/20 border border-orange-600 text-orange-100 px-4 py-3 rounded">No active channels or failed to load</div>';
        }
    }

    renderActiveChannels(channels) {
        const container = document.getElementById('active-channels-list');

        if (channels.length === 0) {
            container.innerHTML = '<div class="bg-orange-900/20 border border-orange-600 text-orange-100 px-4 py-3 rounded">No active channels</div>';
            return;
        }

        container.innerHTML = channels.map(channel => {
            const safeId = channel.name.replace(/[^a-zA-Z0-9]/g, '_');
            return `
            <div class="channel-item">
                <div class="flex justify-between items-center">
                    <div class="flex items-center flex-1">
                        <img src="${channel.logoURL || 'https://cdn.kcp.im/tv/kptv-icon.png'}" 
                            alt="${this.escapeHtml(channel.name)}" 
                            class="w-12 h-12 object-cover rounded mr-3"
                            onerror="this.src='https://cdn.kcp.im/tv/kptv-icon.png'">
                        <div class="flex-1">
                            <div class="font-semibold">${this.escapeHtml(channel.name)}</div>
                            <div class="text-sm text-gray-400">
                                <span class="connection-dot status-active"></span>
                                ${channel.clients || 0} client(s) connected
                            </div>
                            <div class="mt-2 flex flex-wrap gap-1" id="stats-${safeId}">
                                <span class="text-xs text-gray-400">Loading stats...</span>
                            </div>
                        </div>
                    </div>
                    <div class="text-right text-sm ml-4">
                        <div class="text-gray-400">Bytes: ${this.formatBytes(channel.bytesTransferred || 0)}</div>
                        <div class="text-gray-400">Source: ${channel.currentSource || 'Unknown'}</div>
                        <button class="mt-2 px-3 py-1 bg-kptv-gray-light border border-kptv-border hover:bg-kptv-border rounded text-sm transition-colors flex items-center space-x-1" onclick="kptvAdmin.showStreamSelector('${this.escapeHtml(channel.name).replace(/'/g, "\\'")}')">
                            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z"></path>
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z"></path>
                            </svg>
                            <span>Streams</span>
                        </button>
                    </div>
                </div>
            </div>
            `;
        }).join('');

        channels.forEach(channel => {
            this.loadChannelStats(channel.name);
        });
    }

    async loadChannelStats(channelName) {
        try {
            const encodedChannelName = encodeURIComponent(channelName);
            const statsData = await this.apiCall(`/api/channels/${encodedChannelName}/stats`);
            this.renderChannelStatsBadges(channelName, statsData);
        } catch (error) {
            const safeId = channelName.replace(/[^a-zA-Z0-9]/g, '_');
            const container = document.getElementById(`stats-${safeId}`);
            if (container) {
                container.innerHTML = '<span class="stat-badge">No Stats</span>';
            }
        }
    }

    renderChannelStatsBadges(channelName, stats) {
        const safeId = channelName.replace(/[^a-zA-Z0-9]/g, '_');
        const container = document.getElementById(`stats-${safeId}`);

        if (!container) return;

        if (!stats.streaming || !stats.valid) {
            container.innerHTML = '<span class="stat-badge">No Stats</span>';
            return;
        }

        const badges = [];

        if (stats.videoResolution) {
            badges.push(`<span class="stat-badge">${stats.videoResolution}</span>`);
        }

        if (stats.fps > 0) {
            badges.push(`<span class="stat-badge">${stats.fps.toFixed(0)}fps</span>`);
        }

        if (stats.videoCodec) {
            badges.push(`<span class="stat-badge">${stats.videoCodec.toUpperCase()}</span>`);
        }

        if (stats.audioCodec) {
            badges.push(`<span class="stat-badge">${stats.audioCodec.toUpperCase()}</span>`);
        }

        if (stats.bitrate > 0) {
            badges.push(`<span class="stat-badge">${this.formatBitrate(stats.bitrate)}</span>`);
        }

        container.innerHTML = badges.length > 0 ? badges.join(' ') : '<span class="stat-badge">No Stats</span>';
    }

    async showStreamSelector(channelName) {
        try {
            const encodedChannelName = encodeURIComponent(channelName);
            const data = await this.apiCall(`/api/channels/${encodedChannelName}/streams`);
            this.renderStreamSelector(data);
            this.showModal('stream-selector-modal');
        } catch (error) {
            this.showNotification('Failed to load streams for channel', 'danger');
        }
    }

    async loadAllChannels() {
        try {
            const channels = await this.apiCall('/api/channels');
            this.allChannels = channels;
            this.renderCurrentPage();
            return channels;
        } catch (error) {
            document.getElementById('all-channels-list').innerHTML =
                '<div class="bg-red-900/20 border border-red-600 text-red-100 px-4 py-3 rounded">Failed to load channels</div>';
            throw error;
        }
    }

    renderAllChannels(channels) {
        const container = document.getElementById('all-channels-list');

        if (channels.length === 0) {
            container.innerHTML = '<div class="bg-orange-900/20 border border-orange-600 text-orange-100 px-4 py-3 rounded">No channels found</div>';
            return;
        }

        const fragment = document.createDocumentFragment();

        channels.forEach(channel => {
            const safeId = channel.name.replace(/[^a-zA-Z0-9]/g, '_');
            const div = document.createElement('div');
            div.className = `channel-item ${channel.active ? '' : 'channel-inactive'}`;
            div.innerHTML = `
                <div class="flex justify-between items-center">
                    <div class="flex items-center flex-1">
                        <img src="${channel.logoURL || 'https://cdn.kcp.im/tv/kptv-icon.png'}" 
                            alt="${this.escapeHtml(channel.name)}" 
                            class="w-12 h-12 object-cover rounded mr-3"
                            onerror="this.src='https://cdn.kcp.im/tv/kptv-icon.png'">
                        <div class="flex-1">
                            <div class="font-semibold">${this.escapeHtml(channel.name)}</div>
                            <div class="text-sm text-gray-400">
                                Group: ${channel.group || 'Uncategorized'} | 
                                Sources: ${channel.sources || 0} | 
                                Status: ${channel.active ? `Active (${channel.clients} clients)` : 'Inactive'}
                            </div>
                            ${channel.active ? `
                            <div class="mt-2 flex flex-wrap gap-1" id="stats-all-${safeId}">
                                <span class="text-xs text-gray-400">Loading stats...</span>
                            </div>
                            ` : ''}
                        </div>
                    </div>
                    <div class="flex items-center ml-4">
                        <span class="status-indicator ${channel.active ? 'status-active' : 'status-error'}"></span>
                        <button class="ml-2 px-3 py-1 bg-kptv-gray-light border border-kptv-border hover:bg-kptv-border rounded text-sm transition-colors flex items-center space-x-1" onclick="kptvAdmin.showStreamSelector('${this.escapeHtml(channel.name).replace(/'/g, "\\'")}')">
                            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z"></path>
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z"></path>
                            </svg>
                            <span class="hidden sm:inline">Streams</span>
                        </button>
                    </div>
                </div>
            `;
            fragment.appendChild(div);
        });

        container.innerHTML = '';
        container.appendChild(fragment);

        channels.forEach(channel => {
            if (channel.active) {
                this.loadChannelStatsForAllTab(channel.name);
            }
        });
    }

    async loadChannelStatsForAllTab(channelName) {
        try {
            const encodedChannelName = encodeURIComponent(channelName);
            const statsData = await this.apiCall(`/api/channels/${encodedChannelName}/stats`);
            this.renderChannelStatsBadgesForAllTab(channelName, statsData);
        } catch (error) {
            const safeId = channelName.replace(/[^a-zA-Z0-9]/g, '_');
            const container = document.getElementById(`stats-all-${safeId}`);
            if (container) {
                container.innerHTML = '<span class="stat-badge">No Stats</span>';
            }
        }
    }

    renderChannelStatsBadgesForAllTab(channelName, stats) {
        const safeId = channelName.replace(/[^a-zA-Z0-9]/g, '_');
        const container = document.getElementById(`stats-all-${safeId}`);

        if (!container) return;

        if (!stats.streaming || !stats.valid) {
            container.innerHTML = '<span class="stat-badge">No Stats</span>';
            return;
        }

        const badges = [];

        if (stats.videoResolution) {
            badges.push(`<span class="stat-badge">${stats.videoResolution}</span>`);
        }

        if (stats.fps > 0) {
            badges.push(`<span class="stat-badge">${stats.fps.toFixed(0)}fps</span>`);
        }

        if (stats.videoCodec) {
            badges.push(`<span class="stat-badge">${stats.videoCodec.toUpperCase()}</span>`);
        }

        if (stats.audioCodec) {
            badges.push(`<span class="stat-badge">${stats.audioCodec.toUpperCase()}</span>`);
        }

        if (stats.bitrate > 0) {
            badges.push(`<span class="stat-badge">${this.formatBitrate(stats.bitrate)}</span>`);
        }

        container.innerHTML = badges.length > 0 ? badges.join(' ') : '<span class="stat-badge">No Stats</span>';
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

        this.currentPage = 1;
        this.renderCurrentPage();
    }

    async loadLogs() {
        try {
            const logs = await this.apiCall('/api/logs');
            this.allLogs = logs;
            this.renderLogs(logs);
        } catch (error) {
            document.getElementById('logs-container').innerHTML =
                '<div class="bg-red-900/20 border border-red-600 text-red-100 px-4 py-3 rounded">Failed to load logs</div>';
        }
    }

    renderLogs(logs) {
        const container = document.getElementById('logs-container');

        if (logs.length === 0) {
            container.innerHTML = '<div class="text-center text-gray-400">No logs available</div>';
            return;
        }

        container.innerHTML = logs.map(log => `
            <div class="log-entry log-${log.level}">
                <span class="text-gray-500 text-xs">${log.timestamp}</span> 
                [${log.level.toUpperCase()}] ${this.escapeHtml(log.message)}
            </div>
        `).join('');

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

    showLoadingOverlay(message = 'Loading...') {
        const overlay = document.getElementById('loading-overlay');
        overlay.querySelector('div').innerHTML = `
            <div class="spinner mx-auto"></div>
            <div class="mt-2 text-white">${message}</div>
        `;
        overlay.classList.add('active');
    }

    hideLoadingOverlay() {
        document.getElementById('loading-overlay').classList.remove('active');
    }

    showNotification(message, type = 'primary') {
        const container = document.getElementById('notification-container');
        const notification = document.createElement('div');

        const bgColors = {
            primary: 'bg-kptv-blue',
            success: 'bg-green-700',
            warning: 'bg-orange-700',
            danger: 'bg-red-700'
        };

        notification.className = `notification ${bgColors[type]} text-white px-4 py-3 rounded shadow-lg`;
        notification.textContent = message;

        container.appendChild(notification);

        setTimeout(() => {
            notification.remove();
        }, 5000);
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

    formatBitrate(bitrate) {
        if (bitrate < 1000) return bitrate + ' bps';
        if (bitrate < 1000000) return (bitrate / 1000).toFixed(1) + ' Kbps';
        return (bitrate / 1000000).toFixed(2) + ' Mbps';
    }

    renderStreamSelector(data) {
        document.getElementById('stream-selector-title').textContent = `Select Stream - ${data.channelName}`;

        const container = document.getElementById('stream-selector-content');

        if (data.streams.length === 0) {
            container.innerHTML = '<div class="bg-orange-900/20 border border-orange-600 text-orange-100 px-4 py-3 rounded">No streams found</div>';
            return;
        }

        this.currentChannelName = data.channelName;
        this.currentStreamData = data.streams;
        this.originalStreamOrder = data.streams.map((_, index) => index);

        container.innerHTML = `
            <div class="mb-4">
                <button class="px-4 py-2 bg-kptv-gray-light border border-kptv-border hover:bg-kptv-border rounded transition-colors hidden" id="save-stream-order">
                    <svg class="w-4 h-4 inline mr-1" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7"></path>
                    </svg>
                    Save Order
                </button>
                <span class="text-sm text-gray-400 ml-2">Use arrows to reorder</span>
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

    renderStreamCards(data) {
        const customOrder = data.streams.map((_, i) => i);
        const orderedStreams = customOrder.map(originalIndex => data.streams[originalIndex]);

        return orderedStreams.map((stream, displayIndex) => {
            const originalIndex = stream.index;
            const isDead = stream.attributes['dead'] === 'true';
            const deadReason = stream.attributes['dead_reason'] || 'unknown';
            const reasonText = deadReason === 'manual' ? 'Manually Killed' :
                deadReason === 'auto_blocked' ? 'Auto-Blocked (Too Many Failures)' : 'Dead';
            const cardClass = originalIndex === data.currentStreamIndex ? 'bg-kptv-blue/20 border-kptv-blue' :
                isDead ? 'bg-kptv-gray-light' : 'bg-kptv-gray';

            const isFirst = displayIndex === 0;
            const isLast = displayIndex === orderedStreams.length - 1;

            return `
                <div class="stream-card ${cardClass} ${isDead ? 'dead-stream' : ''} border border-kptv-border rounded p-3 mb-2" data-original-index="${originalIndex}" data-display-index="${displayIndex}">
                    <div class="flex justify-between items-center">
                        <div class="flex items-center flex-1">
                            <div class="flex flex-col mr-2">
                                <button class="stream-order-btn mb-1" 
                                        onclick="kptvAdmin.moveStreamUp('${this.escapeHtml(data.channelName)}', ${displayIndex}); return false;"
                                        ${isFirst ? 'disabled' : ''}>
                                    <svg class="w-3 h-3" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 15l7-7 7 7"></path>
                                    </svg>
                                </button>
                                <button class="stream-order-btn" 
                                        onclick="kptvAdmin.moveStreamDown('${this.escapeHtml(data.channelName)}', ${displayIndex}); return false;"
                                        ${isLast ? 'disabled' : ''}>
                                    <svg class="w-3 h-3" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7"></path>
                                    </svg>
                                </button>
                            </div>
                            <div class="flex-1">
                                <div class="font-bold flex items-center gap-2">
                                    Stream ${displayIndex + 1}
                                    ${originalIndex === data.preferredStreamIndex ? '<span class="px-2 py-0.5 bg-green-700 text-white text-xs rounded">Preferred</span>' : ''}
                                    ${originalIndex === data.currentStreamIndex ? '<span class="px-2 py-0.5 bg-kptv-blue text-white text-xs rounded">Current</span>' : ''}
                                    ${isDead ? `<span class="px-2 py-0.5 bg-red-700 text-white text-xs rounded" title="${reasonText}">DEAD</span>` : ''}
                                </div>
                                <div class="text-sm text-gray-400">
                                    Source: ${stream.sourceName} (Order: ${stream.sourceOrder})
                                </div>
                                <div class="text-sm text-gray-400 text-truncate">
                                    ${stream.url}
                                </div>
                                ${stream.attributes['tvg-name'] ? `
                                    <div class="text-sm">
                                        Name: ${this.escapeHtml(stream.attributes['tvg-name'])}
                                    </div>
                                ` : ''}
                                ${stream.attributes['group-title'] ? `
                                    <div class="text-sm">
                                        Group: ${this.escapeHtml(stream.attributes['group-title'])}
                                    </div>
                                ` : ''}
                            </div>
                        </div>
                        <div class="flex items-center gap-2 ml-4">
                            ${isDead ?
                    `<a href="#" class="text-green-500 hover:text-green-400" title="Make Live (${reasonText})" onclick="kptvAdmin.reviveStream('${this.escapeHtml(data.channelName)}', ${originalIndex}); return false;">
                                        <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"></path>
                                        </svg>
                                    </a>` :
                    `<a href="#" class="text-kptv-blue hover:text-kptv-blue-light" title="Activate Stream" onclick="kptvAdmin.selectStream('${this.escapeHtml(data.channelName)}', ${originalIndex}); return false;">
                                        <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M14.752 11.168l-3.197-2.132A1 1 0 0010 9.87v4.263a1 1 0 001.555.832l3.197-2.132a1 1 0 000-1.664z"></path>
                                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 12a9 9 0 11-18 0 9 9 0 0118 0z"></path>
                                        </svg>
                                    </a>
                                    <a href="#" class="text-red-500 hover:text-red-400" title="Mark as Dead" onclick="kptvAdmin.killStream('${this.escapeHtml(data.channelName)}', ${originalIndex}); return false;">
                                        <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M18.364 18.364A9 9 0 005.636 5.636m12.728 12.728A9 9 0 015.636 5.636m12.728 12.728L5.636 5.636"></path>
                                        </svg>
                                    </a>`
                }
                        </div>
                    </div>
                </div>
            `;
        }).join('');
    }

    async saveStreamOrder() {
        const streamCards = document.querySelectorAll('.stream-card');
        const newOrder = [];

        streamCards.forEach(card => {
            const originalIndex = parseInt(card.getAttribute('data-original-index'));
            newOrder.push(originalIndex);
        });

        try {
            const encodedChannelName = encodeURIComponent(this.currentChannelName);
            await this.apiCall(`/api/channels/${encodedChannelName}/order`, {
                method: 'POST',
                body: JSON.stringify({ streamOrder: newOrder })
            });

            this.showNotification('Stream order saved successfully!', 'success');

            const saveButton = document.getElementById('save-stream-order');
            if (saveButton) {
                saveButton.classList.add('hidden');
            }

        } catch (error) {
            this.showNotification('Failed to save stream order: ' + error.message, 'danger');
        }
    }

    async selectStream(channelName, streamIndex) {
        try {
            const encodedChannelName = encodeURIComponent(channelName);
            await this.apiCall(`/api/channels/${encodedChannelName}/stream`, {
                method: 'POST',
                body: JSON.stringify({ streamIndex: streamIndex })
            });

            this.showNotification(`Stream changed to index ${streamIndex} for ${channelName}`, 'success');

            setTimeout(() => {
                this.showStreamSelector(channelName);
            }, 2000);

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

            setTimeout(() => {
                this.showStreamSelector(channelName);
            }, 1000);

        } catch (error) {
            this.showNotification('Failed to revive stream', 'danger');
        }
    }

    async toggleWatcher(enable) {
        try {
            await this.apiCall('/api/watcher/toggle', {
                method: 'POST',
                body: JSON.stringify({ enabled: enable })
            });

            const status = enable ? 'enabled' : 'disabled';
            this.showNotification(`Stream watcher ${status} successfully!`, 'success');

            const checkbox = document.querySelector('input[name="watcherEnabled"]');
            if (checkbox) {
                checkbox.checked = enable;
            }

            this.loadStats();
        } catch (error) {
            this.showNotification(`Failed to ${enable ? 'enable' : 'disable'} watcher: ` + error.message, 'danger');

            const checkbox = document.querySelector('input[name="watcherEnabled"]');
            if (checkbox) {
                checkbox.checked = !enable;
            }

            throw error;
        }
    }

    moveStreamUp(channelName, displayIndex) {
        if (displayIndex === 0) return;

        const container = document.getElementById('streams-container');
        const cards = Array.from(container.querySelectorAll('.stream-card'));

        const currentOrder = cards.map(card => parseInt(card.getAttribute('data-original-index')));

        [currentOrder[displayIndex], currentOrder[displayIndex - 1]] = [currentOrder[displayIndex - 1], currentOrder[displayIndex]];

        document.getElementById('save-stream-order').classList.remove('hidden');

        const data = {
            channelName: channelName,
            currentStreamIndex: this.getCurrentStreamIndex(),
            preferredStreamIndex: this.getPreferredStreamIndex(),
            streams: currentOrder.map(originalIndex => this.currentStreamData[originalIndex])
        };

        container.innerHTML = this.renderStreamCards(data);
    }

    moveStreamDown(channelName, displayIndex) {
        const container = document.getElementById('streams-container');
        const cards = Array.from(container.querySelectorAll('.stream-card'));

        if (displayIndex >= cards.length - 1) return;

        const currentOrder = cards.map(card => parseInt(card.getAttribute('data-original-index')));

        [currentOrder[displayIndex], currentOrder[displayIndex + 1]] = [currentOrder[displayIndex + 1], currentOrder[displayIndex]];

        document.getElementById('save-stream-order').classList.remove('hidden');

        const data = {
            channelName: channelName,
            currentStreamIndex: this.getCurrentStreamIndex(),
            preferredStreamIndex: this.getPreferredStreamIndex(),
            streams: currentOrder.map(originalIndex => this.currentStreamData[originalIndex])
        };

        container.innerHTML = this.renderStreamCards(data);
    }

    getCurrentStreamIndex() {
        const currentCard = document.querySelector('.bg-kptv-blue\\/20');
        return currentCard ? parseInt(currentCard.getAttribute('data-original-index')) : 0;
    }

    getPreferredStreamIndex() {
        return this.currentStreamData.findIndex(s => s.index === this.currentStreamData[0].index);
    }
}

let kptvAdmin;
document.addEventListener('DOMContentLoaded', () => {
    kptvAdmin = new KPTVAdmin();
});