import { setFilterText, setFocusEnabled } from './state.js';
import { loadRequests, loadIgnored, loadFocused, confirmIgnoreHost, confirmUnignoreHost, confirmFocusHost, confirmUnfocusHost } from './api.js';
import { renderList, selectRequest, showTab, toggleIgnoredPanel, toggleFocusedPanel } from './render.js';

document.getElementById('filterInput').addEventListener('input', (e) => {
    setFilterText(e.target.value.trim());
    renderList();
});

document.getElementById('ignoredBtn').addEventListener('click', toggleIgnoredPanel);
document.getElementById('focusBtn').addEventListener('click', toggleFocusedPanel);

document.getElementById('refreshBtn').addEventListener('click', () => {
    loadRequests();
});

document.getElementById('focusEnabled').addEventListener('change', (e) => {
    setFocusEnabled(e.target.checked);
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
    }
});

loadRequests();
loadIgnored();
loadFocused();
setInterval(() => {
    if (document.getElementById('autoRefresh').checked) loadRequests();
}, 2000);
