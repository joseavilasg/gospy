import { setFilterText } from './state.js';
import { loadRequests, loadIgnored, confirmIgnoreHost, confirmUnignoreHost } from './api.js';
import { renderList, renderDetail, selectRequest, showTab, toggleIgnoredPanel } from './render.js';

document.getElementById('filterInput').addEventListener('input', (e) => {
    setFilterText(e.target.value.trim());
    renderList();
});

document.getElementById('ignoredBtn').addEventListener('click', toggleIgnoredPanel);

document.getElementById('refreshBtn').addEventListener('click', () => {
    loadRequests();
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
        case 'tab':
            showTab(btn, btn.dataset.tab);
            break;
    }
});

loadRequests();
loadIgnored();
setInterval(() => {
    if (document.getElementById('autoRefresh').checked) loadRequests();
}, 2000);
