/**
 * Channel logo management — set-logo modal, uploads, library picker,
 * and the bulk EPG pull.
 */

let currentLogoChannelName = null;

/**
 * Opens the set-logo modal for a channel, loading its current assignment
 * and the uploaded logo library.
 */
async function showChannelLogoModal(channelName) {
    currentLogoChannelName = channelName;

    document.getElementById("channel-logo-modal-title").textContent = `Channel Logo - ${channelName}`;
    document.getElementById("channel-logo-url").value = "";
    document.getElementById("channel-logo-file").value = "";
    document.getElementById("channel-logo-current").textContent = "Loading...";
    document.getElementById("channel-logo-library").innerHTML = "";

    showModal("channel-logo-modal");

    await refreshChannelLogoCurrent();
    loadLogoLibrary();

    document.getElementById("save-channel-logo-url-btn").onclick = saveChannelLogoURL;
    document.getElementById("upload-channel-logo-btn").onclick = uploadChannelLogo;
    document.getElementById("pull-channel-logo-epg-btn").onclick = pullChannelLogoFromEPG;
    document.getElementById("clear-channel-logo-btn").onclick = clearChannelLogo;
}

/**
 * Reloads the current assignment line and preview image in the modal.
 */
async function refreshChannelLogoCurrent() {
    const label = document.getElementById("channel-logo-current");
    const img = document.getElementById("channel-logo-current-img");

    try {
        const current = await apiCall(`/api/channels/${encodeURIComponent(currentLogoChannelName)}/logo`);
        label.textContent = current.kind ? `Source: ${current.kind}` : "No logo set — using EPG, provider, or default";
        img.src = current.kind === "upload" ? `/api/logos/${current.value}` : current.value || "";
    } catch (err) {
        label.textContent = "Failed to load current logo";
    }
}

/**
 * Renders the uploaded logo library as a clickable grid.
 */
async function loadLogoLibrary() {
    const grid = document.getElementById("channel-logo-library");

    try {
        const hashes = await apiCall("/api/logos");

        if (!hashes || hashes.length === 0) {
            grid.innerHTML = '<div class="text-sm text-gray-500 col-span-6">No uploaded logos yet</div>';
            return;
        }

        grid.innerHTML = hashes
            .map(
                (hash) => `
        <img src="/api/logos/${escapeAttr(hash)}"
            class="w-full h-14 object-contain bg-kptv-gray-light border border-kptv-border rounded cursor-pointer hover:border-kptv-blue logo-library-item"
            data-hash="${escapeAttr(hash)}" alt="">
    `
            )
            .join("");

        grid.onclick = function (e) {
            const item = e.target.closest(".logo-library-item");
            if (item) {
                setChannelLogo({ kind: "upload", value: item.dataset.hash });
            }
        };
    } catch (err) {
        grid.innerHTML = '<div class="text-sm text-red-400 col-span-6">Failed to load library</div>';
    }
}

/**
 * Saves a pasted logo URL as a manual override.
 */
function saveChannelLogoURL() {
    const value = document.getElementById("channel-logo-url").value.trim();

    if (!value) {
        showNotification("Enter a logo URL first", "warning");
        return;
    }

    setChannelLogo({ kind: "override", value: value });
}

/**
 * Pulls the icon from the channel's mapped EPG entry.
 */
function pullChannelLogoFromEPG() {
    setChannelLogo({ kind: "epg", value: "" });
}

/**
 * Posts a logo assignment and refreshes the modal and channel lists.
 */
async function setChannelLogo(payload) {
    try {
        await apiCall(`/api/channels/${encodeURIComponent(currentLogoChannelName)}/logo`, {
            method: "POST",
            body: JSON.stringify(payload),
        });
        showNotification(`Logo updated for ${currentLogoChannelName}`, "success");
        await refreshChannelLogoCurrent();
        loadAllChannels();
    } catch (err) {
        showNotification("Failed to set logo: " + err.message, "danger");
    }
}

/**
 * Uploads a logo file for the current channel. Multipart, so it bypasses
 * apiCall's JSON content type.
 */
async function uploadChannelLogo() {
    const input = document.getElementById("channel-logo-file");

    if (!input.files || input.files.length === 0) {
        showNotification("Choose a file first", "warning");
        return;
    }

    const form = new FormData();
    form.append("logo", input.files[0]);

    try {
        const response = await fetch(`/api/channels/${encodeURIComponent(currentLogoChannelName)}/logo`, {
            method: "POST",
            body: form,
        });

        if (!response.ok) {
            throw new Error(await response.text());
        }

        showNotification(`Logo uploaded for ${currentLogoChannelName}`, "success");
        input.value = "";
        await refreshChannelLogoCurrent();
        loadLogoLibrary();
        loadAllChannels();
    } catch (err) {
        showNotification("Failed to upload logo: " + err.message, "danger");
    }
}

/**
 * Clears the channel's logo assignment.
 */
async function clearChannelLogo() {
    if (!confirm(`Clear the logo assignment for ${currentLogoChannelName}?`)) {
        return;
    }

    try {
        await apiCall(`/api/channels/${encodeURIComponent(currentLogoChannelName)}/logo`, { method: "DELETE" });
        showNotification(`Logo cleared for ${currentLogoChannelName}`, "success");
        await refreshChannelLogoCurrent();
        loadAllChannels();
    } catch (err) {
        showNotification("Failed to clear logo: " + err.message, "danger");
    }
}

/**
 * Assigns the mapped EPG icon to every mapped channel that has one.
 */
async function bulkPullLogosFromEPG() {
    if (!confirm("Pull logos from the mapped EPG for every mapped channel?")) {
        return;
    }

    showLoadingOverlay("Pulling EPG logos...");

    try {
        const result = await apiCall("/api/logos/pull-epg", { method: "POST" });
        hideLoadingOverlay();
        showNotification(`Updated ${result.updated} channel logos`, "success");
        loadAllChannels();
    } catch (err) {
        hideLoadingOverlay();
        showNotification("Failed to pull EPG logos: " + err.message, "danger");
    }
}
