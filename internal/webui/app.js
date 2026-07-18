import { setFilterText, setFocusEnabled, setLastTimestamp, selectedId } from './state.js';
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
        case 'set-view':
            setView(btn.dataset.target, btn.dataset.view);
            break;
        case 'set-content':
            setContent(btn.dataset.target, btn.dataset.content);
            break;
        case 'copy-body':
            copyBody(btn.dataset.target);
            break;
        case 'edit-body':
            editBody(btn.dataset.target);
            break;
        case 'save-body':
            saveBody(btn.dataset.target);
            break;
        case 'cancel-body':
            cancelBody(btn.dataset.target);
            break;
        case 'send-replay':
            sendReplay();
            break;
        case 'revert-body':
            revertBody(btn.dataset.target);
            break;
        case 'goto-replay':
            selectRequest(btn.dataset.id);
            break;
        case 'copy-id': {
            const idSpan = btn.closest('.detail-id-group')?.querySelector('.detail-id');
            if (idSpan) {
                navigator.clipboard.writeText(idSpan.textContent).then(() => {
                    btn.classList.add('copied');
                    setTimeout(() => btn.classList.remove('copied'), 1500);
                });
            }
            break;
        }
        case 'toggle-replays':
            const replaysList = btn.closest('.replays-section').querySelector('.replays-list');
            if (replaysList) replaysList.classList.toggle('collapsed');
            const toggle = btn.querySelector('.replays-toggle');
            if (toggle) toggle.textContent = replaysList.classList.contains('collapsed') ? '▸' : '▾';
            break;
    }
});

document.getElementById('detailPanel').addEventListener('detail-rendered', () => {
    renderCurrentContent('request');
    renderCurrentContent('response');
});

function setView(target, view) {
    const pre = document.querySelector(`pre[data-body-target="${target}"]`);
    if (!pre) return;
    pre.dataset.viewMode = view;
    const viewer = pre.closest('.body-viewer');
    if (viewer) {
        viewer.querySelectorAll('[data-action="set-view"]').forEach(b => {
            b.classList.toggle('active', b.dataset.view === view);
        });
    }
    renderCurrentContent(target);
}

function setContent(target, content) {
    const pre = document.querySelector(`pre[data-body-target="${target}"]`);
    if (!pre) return;
    pre.dataset.contentMode = content;
    const viewer = pre.closest('.body-viewer');
    if (viewer) {
        viewer.querySelectorAll('[data-action="set-content"]').forEach(b => {
            b.classList.toggle('active', b.dataset.content === content);
        });
    }
    renderCurrentContent(target);
}

function renderCurrentContent(target) {
    const pre = document.querySelector(`pre[data-body-target="${target}"]`);
    if (!pre) return;
    const viewer = pre.closest('.body-viewer');
    if (!viewer) return;

    const contentMode = pre.dataset.contentMode || 'original';
    const viewMode = pre.dataset.viewMode || 'raw';

    let content;
    switch (contentMode) {
        case 'edited': content = pre.dataset.edited || ''; break;
        case 'decoded': content = pre.dataset.decoded || ''; break;
        default: content = pre.dataset.raw || pre.dataset.decoded || ''; break;
    }

    const existingTree = viewer.querySelector('.json-viewer-container');
    if (existingTree) existingTree.remove();

    if (viewMode === 'pretty') {
        try {
            const obj = JSON.parse(content);
            const container = document.createElement('div');
            container.className = 'json-viewer-container';
            viewer.appendChild(container);
            const jsonViewer = new JSONViewer();
            container.appendChild(jsonViewer.getContainer());
            jsonViewer.showJSON(obj, -1, 1);
            pre.style.display = 'none';
        } catch (e) {
            pre.textContent = content || '[not valid JSON]';
            pre.style.display = '';
        }
    } else {
        pre.textContent = content || '[no data]';
        pre.style.display = '';
    }
}

function copyBody(target) {
    const pre = document.querySelector(`pre[data-body-target="${target}"]`);
    if (!pre) return;

    const content = pre.dataset.edited || pre.dataset.decoded || pre.textContent || '';
    navigator.clipboard.writeText(content).then(() => {
        const viewer = pre.closest('.body-viewer');
        if (viewer) {
            const btn = viewer.querySelector('[data-action="copy-body"]');
            if (btn) {
                btn.classList.add('copied');
                setTimeout(() => btn.classList.remove('copied'), 1500);
            }
        }
    });
}

let activeMonacoEditor = null;
let savedToolbarHtml = null;

function mapContentType(ct) {
    if (!ct) return 'json';
    const lower = ct.toLowerCase();
    if (lower.includes('json')) return 'json';
    if (lower.includes('html')) return 'html';
    if (lower.includes('css')) return 'css';
    if (lower.includes('javascript') || lower.includes('ecmascript')) return 'javascript';
    if (lower.includes('xml')) return 'xml';
    if (lower.includes('yaml') || lower.includes('yml')) return 'yaml';
    if (lower.includes('sql')) return 'sql';
    if (lower.includes('python')) return 'python';
    return 'plaintext';
}

function editBody(target) {
    const pre = document.querySelector(`pre[data-body-target="${target}"]`);
    if (!pre) return;
    const viewer = pre.closest('.body-viewer');
    if (!viewer) return;

    const existingTree = viewer.querySelector('.json-viewer-container');
    if (existingTree) {
        existingTree.remove();
        pre.style.display = '';
    }

    const contentType = viewer.dataset.contentType || '';
    const autoLang = mapContentType(contentType);
    const savedLang = localStorage.getItem('gospy-editor-lang');
    const lang = autoLang || savedLang || 'json';
    const tools = viewer.querySelector('.body-tools');
    savedToolbarHtml = tools.innerHTML;
    tools.innerHTML = `
        <div class="body-tools-group">
            <button class="body-tool body-tool-save" data-action="save-body" data-target="${target}">Save</button>
            <button class="body-tool body-tool-cancel" data-action="cancel-body" data-target="${target}">Cancel</button>
        </div>
        <select class="body-lang-select" id="editorLangSelect"></select>`;

    const editorContainer = document.createElement('div');
    editorContainer.className = 'monaco-editor-container';
    pre.parentNode.insertBefore(editorContainer, pre.nextSibling);
    pre.style.display = 'none';

    const content = pre.dataset.decoded || pre.textContent || '';
    createMonacoEditor(editorContainer, content, lang).then((editor) => {
        activeMonacoEditor = editor;

        const select = document.getElementById('editorLangSelect');
        const languages = monaco.languages.getLanguages();
        const seen = new Set();
        languages.forEach((l) => {
            if (seen.has(l.id)) return;
            seen.add(l.id);
            const opt = document.createElement('option');
            opt.value = l.id;
            opt.textContent = l.aliases?.[0] || l.id;
            if (l.id === lang) opt.selected = true;
            select.appendChild(opt);
        });

        select.addEventListener('change', () => {
            const lang = select.value;
            monaco.editor.setModelLanguage(editor.getModel(), lang);
            if (!contentType || autoLang === 'plaintext') {
                localStorage.setItem('gospy-editor-lang', lang);
            }
        });

        editor.focus();
    });
}

function saveBody(target) {
    const pre = document.querySelector(`pre[data-body-target="${target}"]`);
    if (!pre) return;
    const viewer = pre.closest('.body-viewer');
    if (!viewer) return;

    if (activeMonacoEditor) {
        const value = activeMonacoEditor.getValue();
        let formatted = value;
        try {
            const parsed = JSON.parse(value);
            formatted = JSON.stringify(parsed, null, 2);
        } catch {
            // not JSON, use raw
        }

        fetch(`/api/requests/${selectedId}/body`, {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ target, body: formatted })
        }).then(r => r.json()).then(() => {
            pre.dataset.edited = formatted;
            pre.textContent = formatted;
            pre.dataset.decoded = formatted;

            activeMonacoEditor.dispose();
            activeMonacoEditor = null;
            const container = viewer.querySelector('.monaco-editor-container');
            if (container) container.remove();
            pre.style.display = '';

            if (savedToolbarHtml) {
                viewer.querySelector('.body-tools').innerHTML = savedToolbarHtml;
                savedToolbarHtml = null;
            }

            refreshDetail();
        }).catch(e => console.error('Failed to save body:', e));
    }
}

function cancelBody(target) {
    const pre = document.querySelector(`pre[data-body-target="${target}"]`);
    if (!pre) return;
    const viewer = pre.closest('.body-viewer');
    if (!viewer) return;

    if (activeMonacoEditor) {
        activeMonacoEditor.dispose();
        activeMonacoEditor = null;
    }

    const container = viewer.querySelector('.monaco-editor-container');
    if (container) container.remove();

    pre.style.display = '';
    const tools = viewer.querySelector('.body-tools');
    if (savedToolbarHtml) {
        tools.innerHTML = savedToolbarHtml;
        savedToolbarHtml = null;
    }
}

function sendReplay() {
    if (!confirm('Send replay? This will execute the request and create a new entry.')) return;

    let body = '';
    if (activeMonacoEditor) {
        body = activeMonacoEditor.getValue();
    } else {
        const pre = document.querySelector('pre[data-body-target="request"]');
        if (pre) body = pre.dataset.edited || pre.dataset.decoded || '';
    }

    fetch(`/api/requests/${selectedId}/replay`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ body })
    }).then(r => r.json()).then(({ id }) => {
        if (activeMonacoEditor) cancelBody('request');
        setLastTimestamp('');
        loadRequests().then(() => selectRequest(id));
    }).catch(e => console.error('Replay failed:', e));
}

function revertBody(target) {
    fetch(`/api/requests/${selectedId}/body?target=${target}`, {
        method: 'DELETE'
    }).then(r => r.json()).then(() => {
        refreshDetail();
    }).catch(e => console.error('Revert failed:', e));
}

function refreshDetail() {
    if (selectedId) {
        selectRequest(selectedId);
    }
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
