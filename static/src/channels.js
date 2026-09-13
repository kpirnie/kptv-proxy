/**
 * Fetches all active streaming channels from the API and renders
 * them into the dashboard active channels list with client counts
 * and stream stat badges.
 * @returns {Promise<void>}
 */
async function loadActiveChannels() {
  try {
    const channels = await apiCall("/api/channels/active");
    renderActiveChannels(channels);
  } catch (error) {
    document.getElementById("active-channels-list").innerHTML =
      '<div class="bg-orange-900/20 border border-orange-600 text-orange-100 px-4 py-3 rounded">No active channels or failed to load</div>';
  }
}

/**
 * Renders active channel cards into the dashboard active channels list,
 * including logo, client count, source info, and a streams button.
 * Triggers async stat badge loading for each channel after render.
 * @param {Array<Object>} channels - Active channel objects from API
 */
function renderActiveChannels(channels) {
  const container = document.getElementById("active-channels-list");

  if (channels.length === 0) {
    container.innerHTML =
      '<div class="bg-orange-900/20 border border-orange-600 text-orange-100 px-4 py-3 rounded">No active channels</div>';
    return;
  }

  container.innerHTML = channels
    .map((channel) => {
      const safeId = channel.name.replace(/[^a-zA-Z0-9]/g, "_");
      return `
        <div class="channel-item">
            <div class="flex justify-between items-center">
                <div class="flex items-center flex-1">
                    <img src="${escapeAttr(channel.logoURL || "https://cdn.kevp.us/tv/kptv-icon.png")}"
                        alt="${escapeAttr(channel.name)}"
                        class="w-12 h-12 object-cover rounded mr-3"
                        onerror="this.src='https://cdn.kevp.us/tv/kptv-icon.png'">
                    <div class="flex-1">
                        <div class="font-semibold">
                            ${escapeHtml(channel.name)}
                            ${epgReady && hasEPGMapping(channel.name) ? `<span class="inline-block w-2 h-2 rounded-full bg-green-500 ml-1" title="EPG Mapped"></span>` : ""}
                        </div>
                        <div class="text-sm text-gray-400">
                            <span class="connection-dot status-active"></span>
                            ${channel.clients || 0} client(s) connected
                        </div>
                        ${epgReady && channel.now ? `<div class="text-xs text-gray-400 mt-1">Now: ${escapeHtml(channel.now)}</div>` : ""}
                        <div class="mt-2 flex flex-wrap gap-1" id="stats-${safeId}">
                            <span class="text-xs text-gray-400">Loading stats...</span>
                        </div>
                    </div>
                </div>
                <div class="text-right text-sm ml-4">
                    <div class="text-gray-400">Bytes: ${formatBytes(channel.bytesTransferred || 0)}</div>
                    <div class="text-gray-400">Source: ${channel.currentSource || "Unknown"}</div>
                    <div class="flex gap-2 mt-2">
                        <button title="Streams" class="px-3 py-1 bg-kptv-gray-light border border-kptv-border hover:bg-kptv-border rounded text-xs transition-colors flex items-center space-x-1"
                            data-action="streams" data-channel="${escapeAttr(channel.name)}">
                            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z"></path>
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z"></path>
                            </svg>
                        </button>
                        <button title="EPG" class="px-3 py-1 bg-kptv-gray-light border border-kptv-border rounded text-xs transition-colors flex items-center space-x-1 ${epgReady ? "hover:bg-kptv-border" : "opacity-50 cursor-not-allowed"}"
                            ${epgReady ? `data-action="epg" data-channel="${escapeAttr(channel.name)}"` : "disabled"}>
                        <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 7V3m8 4V3m-9 8h10M5 21h14a2 2 0 002-2V7a2 2 0 00-2-2H5a2 2 0 00-2 2v12a2 2 0 002 2z"></path>
                            </svg>
                        </button>
                        <button title="Logo" class="px-3 py-1 bg-kptv-gray-light border border-kptv-border hover:bg-kptv-border rounded text-xs transition-colors flex items-center space-x-1"
                            data-action="logo" data-channel="${escapeAttr(channel.name)}">
                            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 16l4.586-4.586a2 2 0 012.828 0L16 16m-2-2l1.586-1.586a2 2 0 012.828 0L20 14m-6-6h.01M6 20h12a2 2 0 002-2V6a2 2 0 00-2-2H6a2 2 0 00-2 2v12a2 2 0 002 2z"></path>
                            </svg>
                        </button>
                    </div>
                </div>
            </div>
        </div>
        `;
    })
    .join("");

  container.onclick = function (e) {
    const btn = e.target.closest("button[data-action]");
    if (!btn || !container.contains(btn)) return;
    if (btn.dataset.action === "streams") showStreamSelector(btn.dataset.channel);
    else if (btn.dataset.action === "epg") showEPGChannelModal(btn.dataset.channel);
    else if (btn.dataset.action === "logo") showChannelLogoModal(btn.dataset.channel);
  };

  channels.forEach((channel) => loadChannelStats(channel.name));
}

/**
 * Fetches and renders FFprobe stat badges for a channel in the
 * dashboard active channels list.
 * @param {string} channelName - Channel name to fetch stats for
 * @returns {Promise<void>}
 */
async function loadChannelStats(channelName) {
  try {
    const statsData = await apiCall(
      `/api/channels/${encodeURIComponent(channelName)}/stats`,
    );
    renderChannelStatsBadges(channelName, statsData);
  } catch (error) {
    const safeId = channelName.replace(/[^a-zA-Z0-9]/g, "_");
    const container = document.getElementById(`stats-${safeId}`);
    if (container)
      container.innerHTML = '<span class="stat-badge">No Stats</span>';
  }
}

/**
 * Renders FFprobe stream stat badges (resolution, fps, codecs, bitrate)
 * into the active channels list stat container for a given channel.
 * @param {string} channelName - Channel name for DOM targeting
 * @param {Object} stats - Stream stats object from /api/channels/{channel}/stats
 */
function renderChannelStatsBadges(channelName, stats) {
  const safeId = channelName.replace(/[^a-zA-Z0-9]/g, "_");
  const container = document.getElementById(`stats-${safeId}`);
  if (!container) return;

  if (!stats.streaming || !stats.valid) {
    container.innerHTML = '<span class="stat-badge">No Stats</span>';
    return;
  }

  const badges = [];
  if (stats.videoResolution)
    badges.push(`<span class="stat-badge">${stats.videoResolution}</span>`);
  if (stats.fps > 0)
    badges.push(`<span class="stat-badge">${stats.fps.toFixed(0)}fps</span>`);
  if (stats.videoCodec)
    badges.push(
      `<span class="stat-badge">${stats.videoCodec.toUpperCase()}</span>`,
    );
  if (stats.audioCodec)
    badges.push(
      `<span class="stat-badge">${stats.audioCodec.toUpperCase()}</span>`,
    );
  if (stats.bitrate > 0)
    badges.push(
      `<span class="stat-badge">${formatBitrate(stats.bitrate)}</span>`,
    );

  container.innerHTML =
    badges.length > 0
      ? badges.join(" ")
      : '<span class="stat-badge">No Stats</span>';
}

/**
 * Fetches the current page of channels from the API, applying the active group
 * filter and search term server-side. The group list is only returned when the
 * cached generation token is stale, so unchanged polls carry just the page.
 * @returns {Promise<Array>} The fetched page of channels
 */
async function loadAllChannels() {
  const params = new URLSearchParams();
  params.set("page", currentPage);
  params.set("size", pageSize);

  if (activeGroupFilter) params.set("group", activeGroupFilter);

  const searchInput = document.getElementById("channel-search");
  const searchTerm = searchInput ? searchInput.value.trim() : "";
  if (searchTerm) params.set("q", searchTerm);

  if (channelGeneration !== null) params.set("gen", channelGeneration);

  try {
    const result = await apiCall(`/api/channels?${params.toString()}`);

    allChannels = result.channels || [];
    channelTotal = result.total || 0;
    channelGeneration = result.generation;
    epgReady = result.epgReady === true;

    if (result.groups) {
      channelGroups = result.groups;
      renderGroupFilterButtons();
    }

    renderCurrentPage();
    return allChannels;
  } catch (error) {
    document.getElementById("all-channels-list").innerHTML =
      '<div class="bg-red-900/20 border border-red-600 text-red-100 px-4 py-3 rounded">Failed to load channels</div>';
    throw error;
  }
}

/**
 * Renders a page slice of channels into the all-channels list,
 * including logo, group, source count, status indicator, and streams button.
 * Triggers async stat badge loading for active channels after render.
 * @param {Array<Object>} channels - Channel slice for the current page
 */
function renderAllChannels(channels) {
  const container = document.getElementById("all-channels-list");

  if (channels.length === 0) {
    container.innerHTML =
      '<div class="bg-orange-900/20 border border-orange-600 text-orange-100 px-4 py-3 rounded">No channels found</div>';
    return;
  }

  const fragment = document.createDocumentFragment();

  channels.forEach((channel) => {
    const safeId = channel.name.replace(/[^a-zA-Z0-9]/g, "_");
    const div = document.createElement("div");
    div.className = `channel-item ${channel.active ? "" : "channel-inactive"}`;
    div.innerHTML = `
            <div class="flex justify-between items-center">
                <div class="flex items-center flex-1">
                    <img src="${channel.logoURL || "https://cdn.kevp.us/tv/kptv-icon.png"}"
                        alt="${escapeHtml(channel.name)}"
                        class="w-12 h-12 object-cover rounded mr-3"
                        onerror="this.src='https://cdn.kevp.us/tv/kptv-icon.png'">
                    <div class="flex-1">
                        <div class="font-semibold">
                            ${escapeHtml(channel.name)}
                            ${epgReady && hasEPGMapping(channel.name) ? `<span class="inline-block w-2 h-2 rounded-full bg-green-500 ml-1" title="EPG Mapped"></span>` : ""}
                        </div>
                        <div class="text-sm text-gray-400">
                            Group: ${escapeHtml(channel.group || "Uncategorized")} |
                            Sources: ${channel.sources || 0} |
                            Status: ${channel.active ? `Active (${channel.clients} clients)` : "Inactive"}
                        </div>
                        ${channel.active
        ? `
                        <div class="mt-2 flex flex-wrap gap-1" id="stats-all-${safeId}">
                            <span class="text-xs text-gray-400">Loading stats...</span>
                        </div>
                        `
        : ""
      }
                    </div>
                </div>
                <div class="flex items-center ml-4">
                    <span class="status-indicator ${channel.active ? "status-active" : "status-error"}"></span>
                    <div class="flex gap-2 mt-2">
                        <button title="Streams" class="px-3 py-1 bg-kptv-gray-light border border-kptv-border hover:bg-kptv-border rounded text-xs transition-colors flex items-center space-x-1"
                            data-action="streams" data-channel="${escapeAttr(channel.name)}">
                            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z"></path>
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z"></path>
                            </svg>
                        </button>
                        <button title="EPG" class="px-3 py-1 bg-kptv-gray-light border border-kptv-border rounded text-xs transition-colors flex items-center space-x-1 ${epgReady ? "hover:bg-kptv-border" : "opacity-50 cursor-not-allowed"}"
                            ${epgReady ? `data-action="epg" data-channel="${escapeAttr(channel.name)}"` : "disabled"}>
                            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 7V3m8 4V3m-9 8h10M5 21h14a2 2 0 002-2V7a2 2 0 00-2-2H5a2 2 0 00-2 2v12a2 2 0 002 2z"></path>
                            </svg>
                        </button>
                        <button title="Logo" class="px-3 py-1 bg-kptv-gray-light border border-kptv-border hover:bg-kptv-border rounded text-xs transition-colors flex items-center space-x-1"
                            data-action="logo" data-channel="${escapeAttr(channel.name)}">
                            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 16l4.586-4.586a2 2 0 012.828 0L16 16m-2-2l1.586-1.586a2 2 0 012.828 0L20 14m-6-6h.01M6 20h12a2 2 0 002-2V6a2 2 0 00-2-2H6a2 2 0 00-2 2v12a2 2 0 002 2z"></path>
                            </svg>
                        </button>
                    </div>
                </div>
            </div>
        `;
    fragment.appendChild(div);
  });

  container.innerHTML = "";
  container.appendChild(fragment);

  container.onclick = function (e) {
    const btn = e.target.closest("button[data-action]");
    if (!btn || !container.contains(btn)) return;
    if (btn.dataset.action === "streams") showStreamSelector(btn.dataset.channel);
    else if (btn.dataset.action === "epg") showEPGChannelModal(btn.dataset.channel);
    else if (btn.dataset.action === "logo") showChannelLogoModal(btn.dataset.channel);
  };

  channels.forEach((channel) => {
    if (channel.active) loadChannelStatsForAllTab(channel.name);
  });
}

/**
 * Fetches FFprobe stat badges for a channel in the all-channels tab.
 * @param {string} channelName - Channel name to fetch stats for
 * @returns {Promise<void>}
 */
async function loadChannelStatsForAllTab(channelName) {
  try {
    const statsData = await apiCall(
      `/api/channels/${encodeURIComponent(channelName)}/stats`,
    );
    renderChannelStatsBadgesForAllTab(channelName, statsData);
  } catch (error) {
    const safeId = channelName.replace(/[^a-zA-Z0-9]/g, "_");
    const container = document.getElementById(`stats-all-${safeId}`);
    if (container)
      container.innerHTML = '<span class="stat-badge">No Stats</span>';
  }
}

/**
 * Renders FFprobe stream stat badges into the all-channels tab
 * stat container for a given channel.
 * @param {string} channelName - Channel name for DOM targeting
 * @param {Object} stats - Stream stats object from API
 */
function renderChannelStatsBadgesForAllTab(channelName, stats) {
  const safeId = channelName.replace(/[^a-zA-Z0-9]/g, "_");
  const container = document.getElementById(`stats-all-${safeId}`);
  if (!container) return;

  if (!stats.streaming || !stats.valid) {
    container.innerHTML = '<span class="stat-badge">No Stats</span>';
    return;
  }

  const badges = [];
  if (stats.videoResolution)
    badges.push(`<span class="stat-badge">${stats.videoResolution}</span>`);
  if (stats.fps > 0)
    badges.push(`<span class="stat-badge">${stats.fps.toFixed(0)}fps</span>`);
  if (stats.videoCodec)
    badges.push(
      `<span class="stat-badge">${stats.videoCodec.toUpperCase()}</span>`,
    );
  if (stats.audioCodec)
    badges.push(
      `<span class="stat-badge">${stats.audioCodec.toUpperCase()}</span>`,
    );
  if (stats.bitrate > 0)
    badges.push(
      `<span class="stat-badge">${formatBitrate(stats.bitrate)}</span>`,
    );

  container.innerHTML =
    badges.length > 0
      ? badges.join(" ")
      : '<span class="stat-badge">No Stats</span>';
}

/**
 * Debounces a search term change, resetting to page 1 and refetching from the
 * server once typing settles.
 * @param {string} searchTerm - Text to filter channel names and groups by
 */
function filterChannels(searchTerm) {
  clearTimeout(channelSearchTimer);
  channelSearchTimer = setTimeout(() => {
    currentPage = 1;
    loadAllChannels();
  }, 300);
}

/** Renders the fetched page of channels and updates pagination controls. */
function renderCurrentPage() {
  if (!allChannels) return;

  renderAllChannels(allChannels);
  updatePaginationInfo();
}

/**
 * Updates pagination info text, page selector options, and
 * enables/disables first/prev/next/last buttons based on current page.
 */
function updatePaginationInfo() {
  const totalPages = Math.ceil(channelTotal / pageSize);
  const startIndex = channelTotal === 0 ? 0 : (currentPage - 1) * pageSize + 1;
  const endIndex = Math.min(startIndex + pageSize - 1, channelTotal);

  document.getElementById("channel-pagination-info").textContent =
    `Showing ${startIndex}-${endIndex} of ${channelTotal} channels`;
  document.getElementById("current-page").textContent = currentPage;

  const pageSelector = document.getElementById("page-selector");
  if (pageSelector) {
    pageSelector.innerHTML = "";
    for (let i = 1; i <= totalPages; i++) {
      const option = document.createElement("option");
      option.value = i;
      option.textContent = i;
      option.selected = i === currentPage;
      pageSelector.appendChild(option);
    }
  }

  const firstBtn = document.getElementById("first-page").parentElement;
  const prevBtn = document.getElementById("prev-page").parentElement;
  const nextBtn = document.getElementById("next-page").parentElement;
  const lastBtn = document.getElementById("last-page").parentElement;

  if (currentPage === 1) {
    firstBtn.classList.add("opacity-50", "pointer-events-none");
    prevBtn.classList.add("opacity-50", "pointer-events-none");
  } else {
    firstBtn.classList.remove("opacity-50", "pointer-events-none");
    prevBtn.classList.remove("opacity-50", "pointer-events-none");
  }

  if (currentPage === totalPages || totalPages === 0) {
    nextBtn.classList.add("opacity-50", "pointer-events-none");
    lastBtn.classList.add("opacity-50", "pointer-events-none");
  } else {
    nextBtn.classList.remove("opacity-50", "pointer-events-none");
    lastBtn.classList.remove("opacity-50", "pointer-events-none");
  }
}

/** Navigates to the previous page if not already on page 1. */
function previousPage() {
  if (currentPage > 1) {
    currentPage--;
    loadAllChannels();
  }
}

/** Navigates to the next page if not already on the last page. */
function nextPage() {
  const totalPages = Math.ceil(channelTotal / pageSize);
  if (currentPage < totalPages) {
    currentPage++;
    loadAllChannels();
  }
}

/** Navigates to the first page. */
function goToFirstPage() {
  if (currentPage !== 1) {
    currentPage = 1;
    loadAllChannels();
  }
}

/** Navigates to the last page. */
function goToLastPage() {
  const totalPages = Math.ceil(channelTotal / pageSize);
  if (currentPage !== totalPages && totalPages > 0) {
    currentPage = totalPages;
    loadAllChannels();
  }
}

/**
 * Navigates to a specific page number if valid and different from current.
 * @param {number} page - Target page number (1-based)
 */
function goToPage(page) {
  const totalPages = Math.ceil(channelTotal / pageSize);
  if (page >= 1 && page <= totalPages && page !== currentPage) {
    currentPage = page;
    loadAllChannels();
  }
}

/**
 * Reads the saved group filter from the kptv_group_filter cookie.
 * @returns {string} Saved group name or empty string
 */
function getGroupFilterCookie() {
  const match = document.cookie.match(/(?:^|;\s*)kptv_group_filter=([^;]*)/);
  return match ? decodeURIComponent(match[1]) : "";
}

/**
 * Writes the active group filter to the kptv_group_filter cookie (session lifetime).
 * @param {string} group
 */
function setGroupFilterCookie(group) {
  document.cookie = `kptv_group_filter=${encodeURIComponent(group)};path=/`;
}

/**
 * Renders group filter buttons from the server-supplied group set, clearing a
 * saved selection that no longer matches any existing group.
 */
function renderGroupFilterButtons() {
  const container = document.getElementById("group-filter-btns");
  if (!container) return;

  if (activeGroupFilter && !channelGroups.some((g) => g.name === activeGroupFilter)) {
    activeGroupFilter = null;
    setGroupFilterCookie("");
  }

  const allBtn = `<button class="group-filter-btn px-3 py-1 rounded text-xs border transition-colors ${!activeGroupFilter ? "bg-kptv-blue border-kptv-blue text-white" : "bg-kptv-gray-light border-kptv-border hover:border-kptv-blue"}"
        data-group="">All</button>`;

  const groupBtns = channelGroups
    .map(
      (g) => `
        <button class="group-filter-btn px-3 py-1 rounded text-xs border transition-colors ${activeGroupFilter === g.name ? "bg-kptv-blue border-kptv-blue text-white" : "bg-kptv-gray-light border-kptv-border hover:border-kptv-blue"}"
            data-group="${escapeAttr(g.name)}">${escapeHtml(g.name)}</button>
    `,
    )
    .join("");

  container.innerHTML = allBtn + groupBtns;

  container.onclick = function (e) {
    const btn = e.target.closest(".group-filter-btn");
    if (!btn) return;
    activeGroupFilter = btn.dataset.group || null;
    setGroupFilterCookie(activeGroupFilter || "");
    currentPage = 1;
    applyChannelFilters();
    // update active state
    container.querySelectorAll(".group-filter-btn").forEach((b) => {
      const isActive = (b.dataset.group || null) === activeGroupFilter;
      b.classList.toggle("bg-kptv-blue", isActive);
      b.classList.toggle("border-kptv-blue", isActive);
      b.classList.toggle("text-white", isActive);
      b.classList.toggle("bg-kptv-gray-light", !isActive);
      b.classList.toggle("border-kptv-border", !isActive);
    });
  };
}

/** Refetches the channel list with the current group filter and search term. */
function applyChannelFilters() {
  loadAllChannels();
}
