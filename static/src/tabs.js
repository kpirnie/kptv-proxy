/**
 * Reads the saved main tab index from the kptv_active_tab cookie.
 * @returns {number} Saved tab index, or 0 when unset
 */
function getActiveTabCookie() {
    const match = document.cookie.match(/(?:^|;\s*)kptv_active_tab=([^;]*)/);
    return match ? (parseInt(decodeURIComponent(match[1]), 10) || 0) : 0;
}

/**
 * Writes the active main tab index to the kptv_active_tab cookie (session lifetime).
 * @param {number} tabIndex
 */
function setActiveTabCookie(tabIndex) {
    document.cookie = `kptv_active_tab=${encodeURIComponent(tabIndex)};path=/`;
}

/**
 * Initializes the main navigation tab system, attaching click handlers
 * to all tab buttons and managing active state for tab content panels.
 * Also initializes the source modal sub-tab system, and restores the
 * previously active main tab from the kptv_active_tab cookie.
 */
function initTabs() {
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
            setActiveTabCookie(tabIndex);
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

    document.querySelectorAll('.srctype-tab-btn').forEach(btn => {
        btn.addEventListener('click', () => {
            const tabIndex = btn.getAttribute('data-tab');
            const srcTypeBtns = document.querySelectorAll('.srctype-tab-btn');
            const srcTypeContents = document.querySelectorAll('.srctype-tab-content > div');

            srcTypeBtns.forEach(b => {
                b.classList.remove('active', 'border-kptv-blue', 'text-kptv-blue');
                b.classList.add('border-transparent');
            });

            btn.classList.add('active', 'border-kptv-blue', 'text-kptv-blue');
            btn.classList.remove('border-transparent');

            srcTypeContents.forEach(content => content.classList.remove('active'));
            srcTypeContents[tabIndex].classList.add('active');
        });
    });

    const savedTab = getActiveTabCookie();
    if (savedTab > 0 && savedTab < tabBtns.length) switchTab(savedTab);
}

/**
 * Programmatically switches the active main tab by index,
 * updating both button states and content panel visibility.
 * Used by stat card click handlers to navigate directly to a tab.
 * @param {number} tabIndex - Zero-based index of the tab to activate
 */
function switchTab(tabIndex) {
    const tabBtns = document.querySelectorAll('.tab-btn');
    const tabContents = document.querySelectorAll('.tab-content > div');

    tabBtns.forEach(b => {
        b.classList.remove('active', 'border-kptv-blue', 'text-kptv-blue');
        b.classList.add('border-transparent');
    });

    tabBtns[tabIndex].classList.add('active', 'border-kptv-blue', 'text-kptv-blue');
    tabBtns[tabIndex].classList.remove('border-transparent');

    tabContents.forEach(content => content.classList.remove('active'));
    tabContents[tabIndex].classList.add('active');
    setActiveTabCookie(tabIndex);
}