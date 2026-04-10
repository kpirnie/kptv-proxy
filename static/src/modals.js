/**
 * Initializes all admin interface modals by attaching close and cancel
 * button handlers, and backdrop click dismissal for each modal.
 * Called once during application initialization.
 */
function initModals() {
    const modals = [
        { id: 'source-modal', closeId: 'close-source-modal', cancelId: 'cancel-source-btn' },
        { id: 'epg-modal', closeId: 'close-epg-modal', cancelId: 'cancel-epg-btn' },
        { id: 'stream-selector-modal', closeId: 'close-stream-selector-modal', cancelId: 'cancel-stream-selector-btn' },
        { id: 'xc-account-modal', closeId: 'close-xc-account-modal', cancelId: 'cancel-xc-account-btn' },
        { id: 'sd-account-modal', closeId: 'close-sd-account-modal', cancelId: 'cancel-sd-account-btn' },
        { id: 'token-modal', closeId: 'close-token-modal', cancelId: 'cancel-token-btn' },
        { id: 'token-reveal-modal', closeId: 'close-token-reveal-modal', cancelId: 'close-token-reveal-btn' }
    ];

    modals.forEach(({ id, closeId, cancelId }) => {
        const modal = document.getElementById(id);
        const closeBtn = document.getElementById(closeId);
        const cancelBtn = document.getElementById(cancelId);

        if (closeBtn) closeBtn.addEventListener('click', () => hideModal(id));
        if (cancelBtn) cancelBtn.addEventListener('click', () => hideModal(id));

        // Dismiss modal on backdrop click
        modal.addEventListener('click', (e) => {
            if (e.target === modal) hideModal(id);
        });
    });
}

/**
 * Shows a modal dialog by adding the active class.
 * @param {string} modalId - The ID of the modal element to show
 */
function showModal(modalId) {
    document.getElementById(modalId).classList.add('active');
}

/**
 * Hides a modal dialog by removing the active class.
 * @param {string} modalId - The ID of the modal element to hide
 */
function hideModal(modalId) {
    document.getElementById(modalId).classList.remove('active');
}

/**
 * Shows the loading overlay with a custom message while
 * long-running operations such as restarts are in progress.
 * @param {string} message - Message to display in the overlay
 */
function showLoadingOverlay(message = 'Loading...') {
    const overlay = document.getElementById('loading-overlay');
    overlay.querySelector('div').innerHTML = `
        <div class="spinner mx-auto"></div>
        <div class="mt-2 text-white">${message}</div>
    `;
    overlay.classList.add('active');
}

/**
 * Hides the loading overlay after a long-running operation completes.
 */
function hideLoadingOverlay() {
    document.getElementById('loading-overlay').classList.remove('active');
}