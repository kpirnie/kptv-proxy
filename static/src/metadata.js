/**
 * Fetches a page of local media entries, applying the current source
 * and search filters, and renders them into the metadata list.
 * @returns {Promise<void>}
 */
async function loadMetadata() {
    const params = new URLSearchParams();
    const source = document.getElementById('meta-source-filter').value;
    const search = document.getElementById('meta-search').value.trim();

    if (source) params.set('source', source);
    if (search) params.set('q', search);
    params.set('page', metaPage);
    params.set('size', metaPageSize);

    try {
        const result = await apiCall(`/api/local-media?${params.toString()}`);
        metaEntries = result.entries || [];
        metaTotal = result.total || 0;
        renderMetadata();
    } catch (error) {
        document.getElementById('meta-list').innerHTML =
            '<div class="bg-orange-900/20 border border-orange-600 text-orange-100 px-4 py-3 rounded">Failed to load metadata</div>';
    }
}

/**
 * Renders the current page of media entries and the pagination controls.
 */
function renderMetadata() {
    const list = document.getElementById('meta-list');

    if (metaEntries.length === 0) {
        list.innerHTML = '<div class="bg-orange-900/20 border border-orange-600 text-orange-100 px-4 py-3 rounded">No local media found — add a Local Source and run a scan</div>';
        document.getElementById('meta-pagination').innerHTML = '';
        return;
    }

    list.innerHTML = metaEntries.map((entry, index) => `
        <div class="source-item">
            <div class="flex justify-between items-start gap-3">
                <div class="shrink-0">
                    ${entry.poster
            ? `<img src="/api/local-media/${entry.hash}/art/poster" class="w-12 rounded border border-kptv-border" alt="">`
            : '<div class="w-12 h-16 rounded border border-kptv-border bg-kptv-gray-light"></div>'}
                </div>
                <div class="flex-1 min-w-0">
                    <h4 class="text-base font-semibold mb-1">${escapeHtml(entry.display)}</h4>
                    <div class="text-gray-400 text-sm text-truncate">${escapeHtml(entry.group_title)}</div>
                    <div class="text-gray-500 text-xs text-truncate mt-1">${escapeHtml(entry.path)}</div>
                </div>
                <button class="px-3 py-1 bg-kptv-blue hover:bg-kptv-blue-light rounded text-sm transition-colors flex items-center space-x-1 shrink-0"
                    onclick="showMetaModal(${index})">
                    <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z"></path>
                    </svg>
                    <span>Edit</span>
                </button>
            </div>
        </div>
    `).join('');

    const totalPages = Math.max(1, Math.ceil(metaTotal / metaPageSize));
    document.getElementById('meta-pagination').innerHTML = `
        <div class="text-gray-400">${metaTotal} entries</div>
        <div class="flex items-center gap-2">
            <button class="px-3 py-1 bg-kptv-gray-light border border-kptv-border hover:bg-kptv-border rounded transition-colors"
                onclick="goToMetaPage(${metaPage - 1})" ${metaPage <= 1 ? 'disabled' : ''}>Prev</button>
            <span class="text-gray-400">Page ${metaPage} of ${totalPages}</span>
            <button class="px-3 py-1 bg-kptv-gray-light border border-kptv-border hover:bg-kptv-border rounded transition-colors"
                onclick="goToMetaPage(${metaPage + 1})" ${metaPage >= totalPages ? 'disabled' : ''}>Next</button>
        </div>
    `;
}

/**
 * Navigates the metadata list to the given page.
 * @param {number} page - One-based page number
 */
function goToMetaPage(page) {
    const totalPages = Math.max(1, Math.ceil(metaTotal / metaPageSize));
    if (page < 1 || page > totalPages) return;
    metaPage = page;
    loadMetadata();
}

/**
 * Populates the local source filter dropdown from the loaded local sources.
 */
function populateMetaSourceFilter() {
    const select = document.getElementById('meta-source-filter');
    const current = select.value;

    select.innerHTML = '<option value="">All Local Sources</option>' +
        (allLocalSources || []).map(s =>
            `<option value="${s.id}">${escapeHtml(s.name)}</option>`
        ).join('');

    select.value = current;
}

/**
 * Opens the metadata editor for the entry at the given render index,
 * showing only the field groups relevant to its media type.
 * @param {number} index - Index into metaEntries
 */
function showMetaModal(index) {
    const entry = metaEntries[index];
    metaCurrent = entry;

    document.getElementById('meta-modal-title').textContent = `Edit — ${entry.display}`;
    document.getElementById('meta-hash').value = entry.hash;
    document.getElementById('meta-path').textContent = entry.path;

    document.getElementById('meta-title').value = entry.title || '';
    document.getElementById('meta-sort-title').value = entry.sort_title || '';
    document.getElementById('meta-tagline').value = entry.tagline || '';
    document.getElementById('meta-plot').value = entry.plot || '';
    document.getElementById('meta-year').value = entry.year || '';
    document.getElementById('meta-premiered').value = entry.premiered || '';
    document.getElementById('meta-rating').value = entry.rating || '';
    document.getElementById('meta-genres').value = (entry.genres || []).join(', ');
    document.getElementById('meta-tags').value = (entry.tags || []).join(', ');

    document.getElementById('meta-mpaa').value = entry.mpaa || '';
    document.getElementById('meta-country').value = entry.country || '';
    document.getElementById('meta-critic-rating').value = entry.critic_rating || '';
    document.getElementById('meta-directors').value = (entry.directors || []).join(', ');
    document.getElementById('meta-writers').value = (entry.writers || []).join(', ');
    document.getElementById('meta-studios').value = (entry.studios || []).join(', ');
    document.getElementById('meta-cast').value = (entry.cast || [])
        .map(p => p.role ? `${p.name} as ${p.role}` : p.name).join(', ');

    document.getElementById('meta-collection').value = entry.collection || '';

    document.getElementById('meta-series').value = entry.series || '';
    document.getElementById('meta-season').value = entry.season || '';
    document.getElementById('meta-episode').value = entry.episode || '';
    document.getElementById('meta-episode-title').value = entry.episode_title || '';

    document.getElementById('meta-artist').value = entry.artist || '';
    document.getElementById('meta-album').value = entry.album || '';
    document.getElementById('meta-disc').value = entry.disc || '';
    document.getElementById('meta-track').value = entry.track || '';

    document.getElementById('meta-imdb-id').value = entry.imdb_id || '';
    document.getElementById('meta-tmdb-id').value = entry.tmdb_id || '';
    document.getElementById('meta-tvdb-id').value = entry.tvdb_id || '';

    document.getElementById('meta-poster').value = entry.poster || '';
    document.getElementById('meta-fanart').value = entry.fanart || '';
    renderArtPreview('meta-poster-preview', entry, 'poster');
    renderArtPreview('meta-fanart-preview', entry, 'fanart');

    const isMusic = entry.media_type === 'music';
    toggleMetaFields('meta-video-fields', !isMusic);
    toggleMetaFields('meta-movie-fields', entry.media_type === 'movies');
    toggleMetaFields('meta-show-fields', entry.media_type === 'shows');
    toggleMetaFields('meta-music-fields', isMusic);

    showModal('meta-modal');
}

/**
 * Shows or hides a metadata field group.
 * @param {string} id - Container element ID
 * @param {boolean} visible - Whether the group applies to the current entry
 */
function toggleMetaFields(id, visible) {
    document.getElementById(id).style.display = visible ? '' : 'none';
}

/**
 * Reads the metadata form and writes the changes back through the API,
 * which persists the .nfo sidecar and re-scans the file.
 * @returns {Promise<void>}
 */
async function saveMetadata() {
    if (!metaCurrent) return;

    const payload = {
        ...metaCurrent,
        title: document.getElementById('meta-title').value.trim(),
        sort_title: document.getElementById('meta-sort-title').value.trim(),
        tagline: document.getElementById('meta-tagline').value.trim(),
        plot: document.getElementById('meta-plot').value.trim(),
        year: document.getElementById('meta-year').value.trim(),
        premiered: document.getElementById('meta-premiered').value.trim(),
        rating: parseFloat(document.getElementById('meta-rating').value) || 0,
        critic_rating: parseInt(document.getElementById('meta-critic-rating').value) || 0,
        mpaa: document.getElementById('meta-mpaa').value.trim(),
        country: document.getElementById('meta-country').value.trim(),
        collection: document.getElementById('meta-collection').value.trim(),
        series: document.getElementById('meta-series').value.trim(),
        season: parseInt(document.getElementById('meta-season').value) || 0,
        episode: parseInt(document.getElementById('meta-episode').value) || 0,
        episode_title: document.getElementById('meta-episode-title').value.trim(),
        artist: document.getElementById('meta-artist').value.trim(),
        album: document.getElementById('meta-album').value.trim(),
        disc: parseInt(document.getElementById('meta-disc').value) || 0,
        track: parseInt(document.getElementById('meta-track').value) || 0,
        imdb_id: document.getElementById('meta-imdb-id').value.trim(),
        tmdb_id: document.getElementById('meta-tmdb-id').value.trim(),
        tvdb_id: document.getElementById('meta-tvdb-id').value.trim(),
        poster: document.getElementById('meta-poster').value.trim(),
        fanart: document.getElementById('meta-fanart').value.trim(),
        genres: splitMetaList(document.getElementById('meta-genres').value),
        tags: splitMetaList(document.getElementById('meta-tags').value),
        directors: splitMetaList(document.getElementById('meta-directors').value),
        writers: splitMetaList(document.getElementById('meta-writers').value),
        studios: splitMetaList(document.getElementById('meta-studios').value),
        cast: parseMetaCast(document.getElementById('meta-cast').value)
    };

    showLoadingOverlay('Writing metadata...');
    try {
        await apiCall(`/api/local-media/${metaCurrent.hash}`, {
            method: 'PUT',
            body: JSON.stringify(payload)
        });
        hideLoadingOverlay();
        hideModal('meta-modal');
        showNotification('Metadata saved successfully!', 'success');
        loadMetadata();
    } catch (error) {
        hideLoadingOverlay();
        showNotification('Failed to save metadata: ' + error.message, 'danger');
    }
}

/**
 * Renders a thumbnail preview for an entry's poster or fanart, or a
 * placeholder line when none is set.
 * @param {string} containerId - Target element ID
 * @param {Object} entry - Media entry
 * @param {string} kind - Either 'poster' or 'fanart'
 */
function renderArtPreview(containerId, entry, kind) {
    const container = document.getElementById(containerId);
    const value = kind === 'poster' ? entry.poster : entry.fanart;

    if (!value) {
        container.innerHTML = '<div class="text-sm text-gray-500">None found</div>';
        return;
    }

    container.innerHTML = `<img src="/api/local-media/${entry.hash}/art/${kind}"
        class="max-h-40 rounded border border-kptv-border" alt="${kind}">`;
}

/**
 * Splits a comma-separated input into a trimmed, non-empty string array.
 * @param {string} value
 * @returns {Array<string>}
 */
function splitMetaList(value) {
    return value.split(',').map(v => v.trim()).filter(v => v.length > 0);
}

/**
 * Parses the cast input, where each entry is "Name" or "Name as Role".
 * @param {string} value
 * @returns {Array<Object>} Person objects
 */
function parseMetaCast(value) {
    return splitMetaList(value).map(item => {
        const parts = item.split(/\s+as\s+/i);
        return parts.length > 1
            ? { name: parts[0].trim(), role: parts.slice(1).join(' as ').trim() }
            : { name: item };
    });
}