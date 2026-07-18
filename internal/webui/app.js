import { setFilterText, setFocusEnabled, setLastTimestamp } from './state.js';
import { loadRequests, loadIgnored, loadFocused, confirmIgnoreHost, confirmUnignoreHost, confirmFocusHost, confirmUnfocusHost } from './api.js';
import { renderList, selectRequest, showTab, toggleIgnoredPanel, toggleFocusedPanel, onListScroll, invalidateFilterCache, escapeHtml } from './render.js';

document.getElementById('filterInput').addEventListener('input', (e) => {
    setFilterText(e.target.value.trim());
    invalidateFilterCache();
    document.getElementById('requestList').scrollTop = 0;
    renderList();
});

document.getElementById('ignoredBtn').addEventListener('click', toggleIgnoredPanel);
document.getElementById('focusBtn').addEventListener('click', toggleFocusedPanel);

document.getElementById('refreshBtn').addEventListener('click', () => {
    setLastTimestamp('');
    loadRequests();
});

document.getElementById('focusEnabled').addEventListener('change', (e) => {
    setFocusEnabled(e.target.checked);
    invalidateFilterCache();
    document.getElementById('requestList').scrollTop = 0;
    renderList();
});

document.getElementById('focusAddBtn').addEventListener('click', () => {
    const input = document.getElementById('focusInput');
    const pattern = input.value.trim();
    if (pattern) {
        confirmFocusHost(pattern);
        input.value = '';
    }
});

document.getElementById('focusInput').addEventListener('keydown', (e) => {
    if (e.key === 'Enter') {
        const input = e.target;
        const pattern = input.value.trim();
        if (pattern) {
            confirmFocusHost(pattern);
            input.value = '';
        }
    }
});

document.getElementById('ignoreAddBtn').addEventListener('click', () => {
    const input = document.getElementById('ignoreInput');
    const pattern = input.value.trim();
    if (pattern) {
        confirmIgnoreHost(pattern);
        input.value = '';
    }
});

document.getElementById('ignoreInput').addEventListener('keydown', (e) => {
    if (e.key === 'Enter') {
        const input = e.target;
        const pattern = input.value.trim();
        if (pattern) {
            confirmIgnoreHost(pattern);
            input.value = '';
        }
    }
});

document.getElementById('ignoredPanel').addEventListener('click', (e) => {
    if (e.target.closest('.ignored-panel-close')) {
        toggleIgnoredPanel();
        return;
    }
    const btn = e.target.closest('[data-action="unignore-item"]');
    if (btn) {
        confirmUnignoreHost(btn.dataset.host);
    }
});

document.getElementById('focusedPanel').addEventListener('click', (e) => {
    if (e.target.closest('.ignored-panel-close')) {
        toggleFocusedPanel();
        return;
    }
    const btn = e.target.closest('[data-action="unfocus-item"]');
    if (btn) {
        confirmUnfocusHost(btn.dataset.host);
    }
});

document.getElementById('requestList').addEventListener('click', (e) => {
    const item = e.target.closest('.request-item');
    if (item && item.dataset.id) {
        selectRequest(item.dataset.id);
    }
});

document.getElementById('detailPanel').addEventListener('click', (e) => {
    const btn = e.target.closest('[data-action]');
    if (!btn) return;
    switch (btn.dataset.action) {
        case 'ignore':
            confirmIgnoreHost(btn.dataset.host);
            break;
        case 'unignore':
            confirmUnignoreHost(btn.dataset.host);
            break;
        case 'focus':
            confirmFocusHost(btn.dataset.host);
            break;
        case 'unfocus':
            confirmUnfocusHost(btn.dataset.host);
            break;
        case 'tab':
            showTab(btn, btn.dataset.tab);
            break;
        case 'toggle-body':
            toggleBody(btn.dataset.target, btn.dataset.mode);
            break;
        case 'prettify-body':
            prettifyBody(btn.dataset.target);
            break;
        case 'copy-body':
            copyBody(btn.dataset.target);
            break;
    }
});

function toggleBody(target, mode) {
    const pre = document.querySelector(`pre[data-body-target="${target}"]`);
    if (!pre) return;

    const decoded = pre.dataset.decoded;
    const raw = pre.dataset.raw;

    pre.textContent = mode === 'raw' ? (raw || '[no raw data]') : decoded;

    const viewer = pre.closest('.body-viewer');
    if (viewer) {
        viewer.querySelectorAll('.body-tool[data-action="toggle-body"]').forEach(b => {
            b.classList.toggle('active', b.dataset.mode === mode);
        });
    }
}

function prettifyBody(target) {
    const pre = document.querySelector(`pre[data-body-target="${target}"]`);
    if (!pre) return;

    try {
        const obj = JSON.parse(pre.textContent);
        pre.textContent = JSON.stringify(obj, null, 2);
    } catch (e) {
        // not JSON, nothing to prettify
    }
}

function copyBody(target) {
    const pre = document.querySelector(`pre[data-body-target="${target}"]`);
    if (!pre) return;

    navigator.clipboard.writeText(pre.textContent).then(() => {
        const viewer = pre.closest('.body-viewer');
        if (viewer) {
            const btn = viewer.querySelector('[data-action="copy-body"]');
            if (btn) {
                const orig = btn.textContent;
                btn.textContent = 'Copied!';
                setTimeout(() => btn.textContent = orig, 1500);
            }
        }
    });
}

const ICON_COLLAPSE = '<svg width="12" height="12" viewBox="0 0 12 12"><polyline points="8,2 4,6 8,10" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>';
const ICON_EXPAND = '<svg width="12" height="12" viewBox="0 0 12 12"><polyline points="4,2 8,6 4,10" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>';

let scrollRAF = null;
document.getElementById('requestList').addEventListener('scroll', () => {
    if (scrollRAF) return;
    scrollRAF = requestAnimationFrame(() => {
        onListScroll();
        scrollRAF = null;
    });
});

document.getElementById('toggleListBtn').addEventListener('click', () => {
    const container = document.getElementById('container');
    const hidden = container.classList.toggle('list-hidden');
    document.getElementById('toggleListBtn').innerHTML = hidden ? ICON_EXPAND : ICON_COLLAPSE;
    localStorage.setItem('gospy-list-hidden', hidden);
});

if (localStorage.getItem('gospy-list-hidden') === 'true') {
    document.getElementById('container').classList.add('list-hidden');
    document.getElementById('toggleListBtn').innerHTML = ICON_EXPAND;
}

loadRequests();
loadIgnored();
loadFocused();
setInterval(() => {
    if (document.getElementById('autoRefresh').checked) loadRequests();
}, 2000);
