import { setFilterText, setFocusEnabled, setLastTimestamp, selectedId, requests, rules, setRules, processFilter, setProcessFilter, setSignatureCache } from './state.js';
import { loadRequests, loadIgnored, loadFocused, confirmIgnoreHost, confirmUnignoreHost, confirmFocusHost, confirmUnfocusHost, loadRules, createRule, updateRule, deleteRule, toggleRule, checkMatch } from './api.js';
import { renderList, selectRequest, showTab, toggleIgnoredPanel, toggleFocusedPanel, toggleRulesPanel, renderRulesList, onListScroll, invalidateFilterCache, escapeHtml, SVG_EDIT, SVG_REVERT, openRuleModal, closeRuleModal, openRuleModalFromRequest } from './render.js';

document.getElementById('filterInput').addEventListener('input', (e) => {
    setFilterText(e.target.value.trim());
    invalidateFilterCache();
    document.getElementById('requestList').scrollTop = 0;
    renderList();
});

document.getElementById('ignoredBtn').addEventListener('click', toggleIgnoredPanel);
document.getElementById('focusBtn').addEventListener('click', toggleFocusedPanel);
document.getElementById('rulesBtn').addEventListener('click', toggleRulesPanel);

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

document.getElementById('rulesPanel').addEventListener('click', (e) => {
    if (e.target.closest('.ignored-panel-close')) {
        toggleRulesPanel();
        return;
    }
    if (e.target.closest('#addRuleBtn')) {
        openRuleModal(null);
        return;
    }
    const toggleBtn = e.target.closest('[data-action="toggle-rule"]');
    if (toggleBtn) {
        const ruleId = toggleBtn.dataset.ruleId;
        const rule = rules.find(r => r.id === ruleId);
        if (rule && !rule.enabled) {
            const matches = rules.filter(r => r.id !== ruleId && r.enabled &&
                r.match.method === rule.match.method &&
                r.match.host === rule.match.host &&
                r.match.url_pattern === rule.match.url_pattern);
            if (matches.length > 0) {
                if (!confirm(`Activating this rule will deactivate "${matches[0].name}" which has the same match. Continue?`)) return;
            }
        }
        toggleRule(ruleId);
        return;
    }
    const editBtn = e.target.closest('[data-action="edit-rule"]');
    if (editBtn) {
        const rule = rules.find(r => r.id === editBtn.dataset.ruleId);
        if (rule) openRuleModal(rule);
        return;
    }
    const deleteBtn = e.target.closest('[data-action="delete-rule"]');
    if (deleteBtn) {
        if (confirm('Delete this rule?')) {
            deleteRule(deleteBtn.dataset.ruleId);
        }
        return;
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
        case 'toggle-menu':
            toggleKebabMenu(btn.closest('.kebab'));
            break;
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
        case 'copy-headers':
            copyHeaders(btn.dataset.target);
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
        case 'create-rule-from-request':
            createRuleFromRequest();
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
        case 'edit-headers':
            editHeaders(btn.closest('.tab-content')?.querySelector('.headers-container'));
            break;
        case 'save-headers':
            saveHeaders(selectedId);
            break;
        case 'cancel-headers':
            cancelHeadersEdit(btn.closest('.headers-container'));
            break;
        case 'add-header':
            addHeaderRow(btn.closest('.headers-container'));
            break;
        case 'remove-header':
            removeHeaderRow(btn);
            break;
        case 'set-url-content': {
            const mode = btn.dataset.content;
            const pre = btn.closest('.tab-content')?.querySelector('pre[data-url-original]');
            if (!pre) break;
            const method = pre.textContent.split(' ')[0];
            if (mode === 'original') {
                pre.textContent = method + ' ' + pre.dataset.urlOriginal;
            } else {
                pre.textContent = method + ' ' + pre.dataset.urlModified;
            }
            btn.closest('.body-tools-group')?.querySelectorAll('.body-tool').forEach(b => b.classList.remove('active'));
            btn.classList.add('active');
            break;
        }
        case 'set-header-content': {
            const mode = btn.dataset.content;
            const target = btn.dataset.target || 'request';
            const container = btn.closest('.tab-content')?.querySelector(`.headers-container[data-target="${target}"]`);
            if (!container) break;
            if (mode === 'original') {
                container.innerHTML = container.dataset.originalHtml;
            } else if (mode === 'mocked' && container.dataset.mockedHtml) {
                container.innerHTML = container.dataset.mockedHtml;
            } else if (mode === 'modified' && container.dataset.modifiedHtml) {
                container.innerHTML = container.dataset.modifiedHtml;
            } else {
                container.innerHTML = container.dataset.editedHtml || container.dataset.originalHtml;
            }
            container.dataset.headerMode = mode;
            btn.closest('.body-tools-group')?.querySelectorAll('.body-tool').forEach(b => b.classList.remove('active'));
            btn.classList.add('active');
            break;
        }
        case 'revert-headers':
            if (!selectedId) break;
            fetch(`/api/requests/${selectedId}/headers`, { method: 'DELETE' })
                .then(r => r.json()).then(() => refreshDetail())
                .catch(e => console.error('Revert headers failed:', e));
            break;
    }
    if (btn.dataset.action !== 'toggle-menu') {
        closeAllKebabMenus();
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
    const sectionPanel = pre.closest('.section-panel');
    if (sectionPanel) {
        sectionPanel.querySelectorAll('[data-action="set-view"]').forEach(b => {
            b.classList.toggle('active', b.dataset.view === view);
        });
    }
    renderCurrentContent(target);
}

function setContent(target, content) {
    const pre = document.querySelector(`pre[data-body-target="${target}"]`);
    if (!pre) return;
    pre.dataset.contentMode = content;
    const sectionPanel = pre.closest('.section-panel');
    if (sectionPanel) {
        sectionPanel.querySelectorAll('[data-action="set-content"]').forEach(b => {
            b.classList.toggle('active', b.dataset.content === content);
        });
    }
    renderCurrentContent(target);
}

function renderCurrentContent(target) {
    const pre = document.querySelector(`pre[data-body-target="${target}"]`);
    if (!pre) return;
    const sectionPanel = pre.closest('.section-panel');
    if (!sectionPanel) return;

    const contentMode = pre.dataset.contentMode || 'original';
    const viewMode = pre.dataset.viewMode || 'raw';

    let content;
    switch (contentMode) {
        case 'edited': content = pre.dataset.edited || ''; break;
        case 'modified': content = pre.dataset.modified || ''; break;
        case 'mocked': content = pre.dataset.mocked || ''; break;
        default: content = pre.dataset.decoded || pre.dataset.raw || ''; break;
    }

    const contentBlock = sectionPanel.querySelector('.content-block');
    const existingTree = contentBlock?.querySelector('.json-viewer-container');
    if (existingTree) existingTree.remove();

    if (viewMode === 'pretty') {
        try {
            const obj = JSON.parse(content);
            const container = document.createElement('div');
            container.className = 'json-viewer-container';
            if (contentBlock) contentBlock.appendChild(container);
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
    navigator.clipboard.writeText(content);
}

function copyHeaders(target) {
    const container = document.querySelector(`.headers-container[data-target="${target}"]`);
    if (!container) return;

    const rows = container.querySelectorAll('.header-row');
    if (rows.length === 0) return;

    const lines = [];
    rows.forEach(row => {
        const key = row.dataset.key || row.querySelector('.header-key')?.textContent?.replace(/:$/, '') || '';
        const values = row.dataset.values ? JSON.parse(row.dataset.values) : [row.querySelector('.header-value')?.textContent || ''];
        values.forEach(v => lines.push(key + ': ' + v));
    });

    navigator.clipboard.writeText(lines.join('\n'));
}

let activeMonacoEditor = null;
let savedToolbarHtml = null;
let modalMonacoEditors = { modifyBody: null, mockReqBody: null, mockRespBody: null };

function disposeModalMonacoEditors() {
    for (const key of Object.keys(modalMonacoEditors)) {
        if (modalMonacoEditors[key]) {
            modalMonacoEditors[key].dispose();
            modalMonacoEditors[key] = null;
        }
    }
}

function initModalMonaco(containerId, value, editorKey) {
    const container = document.getElementById(containerId);
    if (!container) return;
    if (modalMonacoEditors[editorKey]) {
        modalMonacoEditors[editorKey].dispose();
        modalMonacoEditors[editorKey] = null;
    }
    const lang = mapContentType('application/json');
    createMonacoEditor(container, value || '', lang).then(editor => {
        modalMonacoEditors[editorKey] = editor;
    });
}

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
    const sectionPanel = pre.closest('.section-panel');
    if (!sectionPanel) return;

    const existingTree = sectionPanel.querySelector('.json-viewer-container');
    if (existingTree) {
        existingTree.remove();
        pre.style.display = '';
    }

    const contentType = sectionPanel.dataset.contentType || '';
    const autoLang = mapContentType(contentType);
    const savedLang = localStorage.getItem('gospy-editor-lang');
    const lang = autoLang || savedLang || 'json';
    const tools = sectionPanel.querySelector('.content-toolbar');
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
    const sectionPanel = pre.closest('.section-panel');
    if (!sectionPanel) return;

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

            activeMonacoEditor.dispose();
            activeMonacoEditor = null;
            const container = sectionPanel.querySelector('.monaco-editor-container');
            if (container) container.remove();
            pre.style.display = '';

            if (savedToolbarHtml) {
                const toolsDiv = sectionPanel.querySelector('.content-toolbar');
                let html = savedToolbarHtml;
                if (!toolsDiv.querySelector('.body-badge-edited')) {
                    const compression = pre.dataset.compression || '';
                    html = `<div class="toolbar-left">
                        <div class="body-tools-group">
                            <button class="body-tool body-view active" data-action="set-view" data-target="${target}" data-view="pretty">Pretty</button>
                            <button class="body-tool body-view" data-action="set-view" data-target="${target}" data-view="raw">Raw</button>
                        </div>
                        <div class="divider-v"></div>
                        <div class="body-tools-group">
                            <button class="body-tool body-content" data-action="set-content" data-target="${target}" data-content="original">Original</button>
                            <button class="body-tool body-content active" data-action="set-content" data-target="${target}" data-content="edited">Edited</button>
                        </div>
                    </div>
                    <div class="toolbar-right">
                        ${compression ? `<span class="body-badge body-badge-compression">${escapeHtml(compression)}</span>` : ''}
                        <span class="body-badge body-badge-edited">edited</span>
                    </div>`;
                }
                toolsDiv.innerHTML = html;
                savedToolbarHtml = null;
            }
            pre.dataset.contentMode = 'edited';
            const kebabMenu = sectionPanel.querySelector('.kebab-menu');
            if (kebabMenu && !kebabMenu.querySelector('[data-action="revert-body"]')) {
                kebabMenu.insertAdjacentHTML('beforeend', '<div class="menu-item" data-action="revert-body" data-target="' + target + '">↩ Revert</div>');
            }
            renderCurrentContent(target);
        }).catch(e => console.error('Failed to save body:', e));
    }
}

function cancelBody(target) {
    const pre = document.querySelector(`pre[data-body-target="${target}"]`);
    if (!pre) return;
    const sectionPanel = pre.closest('.section-panel');
    if (!sectionPanel) return;

    if (activeMonacoEditor) {
        activeMonacoEditor.dispose();
        activeMonacoEditor = null;
    }

    const container = sectionPanel.querySelector('.monaco-editor-container');
    if (container) container.remove();

    pre.style.display = '';
    const tools = sectionPanel.querySelector('.content-toolbar');
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
loadRules();
setInterval(() => {
    if (document.getElementById('autoRefresh').checked) { loadRequests(); }
}, 2000);

function createRuleFromRequest() {
    if (!selectedId) return;
    fetch(`/api/request-rule?id=${selectedId}`)
        .then(r => r.json())
        .then(entry => openRuleModalFromRequest(entry))
        .catch(e => console.error('Failed to load request for rule creation:', e));
}

function editHeaders(container) {
    if (!container || container.dataset.editing === 'true') return;
    container.dataset.editing = 'true';
    container.dataset.original = container.innerHTML;

    container.querySelectorAll('.header-row').forEach(row => {
        const key = row.dataset.key || '';
        const values = JSON.parse(row.dataset.values || '[]');
        const val = values.join(', ');
        row.innerHTML = `<input class="header-key-input" value="${escapeHtml(key)}" /><span class="header-colon">:</span><input class="header-value-input" value="${escapeHtml(val)}" /><button class="header-remove" data-action="remove-header" title="Remove">&times;</button>`;
    });

    const toolbar = document.createElement('div');
    toolbar.className = 'headers-toolbar';
    toolbar.innerHTML = `<button class="body-tool body-tool-save" data-action="save-headers">Save</button><button class="body-tool body-tool-cancel" data-action="cancel-headers">Cancel</button><button class="body-tool" data-action="add-header">+ Add</button>`;
    container.appendChild(toolbar);
}

function saveHeaders(id) {
    const container = document.querySelector('.headers-container[data-target="request"]');
    if (!container) return;

    const headers = {};
    container.querySelectorAll('.header-row').forEach(row => {
        const keyInput = row.querySelector('.header-key-input');
        const valInput = row.querySelector('.header-value-input');
        if (keyInput && valInput) {
            const key = keyInput.value.trim();
            if (key) {
                headers[key] = [valInput.value];
            }
        }
    });

    fetch(`/api/requests/${id}/headers`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ headers })
    }).then(r => r.json()).then(() => {
        const container = document.querySelector('.headers-container[data-target="request"]');
        if (!container) return;

        function buildViewRows(hdrs) {
            if (!hdrs || Object.keys(hdrs).length === 0) return '<div style="color:#666">No headers</div>';
            return Object.entries(hdrs).map(([k, v]) => {
                const val = Array.isArray(v) ? v.join(', ') : v;
                const dv = Array.isArray(v) ? JSON.stringify(v) : JSON.stringify([v]);
                return `<div class="header-row" data-key="${escapeHtml(k)}" data-values='${escapeHtml(dv)}'><span class="header-key">${escapeHtml(k)}:</span><span class="header-value">${escapeHtml(val)}</span></div>`;
            }).join('');
        }

        const editedHtml = buildViewRows(headers);
        const origHtml = container.dataset.originalHtml || container.dataset.editedHtml || '';

        container.innerHTML = editedHtml;
        container.dataset.editedHtml = editedHtml;
        container.dataset.originalHtml = origHtml;
        container.dataset.headerMode = 'edited';
        delete container.dataset.editing;
        delete container.dataset.original;

        const sectionPanel = container.closest('.section-panel');
        if (!sectionPanel) return;

        let toolbar = sectionPanel.querySelector('.content-toolbar');
        if (!toolbar) {
            toolbar = document.createElement('div');
            toolbar.className = 'content-toolbar';
            const contentBlock = sectionPanel.querySelector('.content-block');
            contentBlock.insertBefore(toolbar, contentBlock.firstChild);
        }
        toolbar.innerHTML = `
            <div class="toolbar-left">
                <div class="body-tools-group">
                    <button class="body-tool body-content" data-action="set-header-content" data-content="original">Original</button>
                    <button class="body-tool body-content active" data-action="set-header-content" data-content="edited">Edited</button>
                </div>
            </div>
            <div class="toolbar-right">
                <span class="body-badge body-badge-edited">edited</span>
            </div>`;
            const kebabMenu = sectionPanel.querySelector('.kebab-menu');
            if (kebabMenu && !kebabMenu.querySelector('[data-action="revert-headers"]')) {
                kebabMenu.insertAdjacentHTML('beforeend', '<div class="menu-item" data-action="revert-headers">↩ Revert</div>');
            }
    }).catch(e => console.error('Save headers failed:', e));
}

function cancelHeadersEdit(container) {
    if (!container) return;
    container.innerHTML = container.dataset.original;
    delete container.dataset.editing;
    delete container.dataset.original;
}

function addHeaderRow(container) {
    if (!container) return;
    const toolbar = container.querySelector('.headers-toolbar');
    const row = document.createElement('div');
    row.className = 'header-row';
    row.dataset.key = '';
    row.dataset.values = '[""]';
    row.innerHTML = `<input class="header-key-input" value="" placeholder="Key" /><span class="header-colon">:</span><input class="header-value-input" value="" placeholder="Value" /><button class="header-remove" data-action="remove-header" title="Remove">&times;</button>`;
    container.insertBefore(row, toolbar);
    row.querySelector('.header-key-input').focus();
}

function removeHeaderRow(btn) {
    const row = btn.closest('.inline-header-row') || btn.closest('.header-row');
    if (row) row.remove();
}

function addInlineHeaderRow(containerId) {
    const container = document.getElementById(containerId);
    if (!container) return;
    const row = document.createElement('div');
    row.className = 'inline-header-row';
    row.innerHTML = '<input class="inline-header-key" placeholder="Key"><span class="header-colon">:</span><input class="inline-header-value" placeholder="Value"><button class="header-remove" title="Remove">&times;</button>';
    container.appendChild(row);
    row.querySelector('.inline-header-key').focus();
}

function readInlineHeaders(containerId) {
    const container = document.getElementById(containerId);
    if (!container) return {};
    const headers = {};
    container.querySelectorAll('.inline-header-row').forEach(row => {
        const key = row.querySelector('.inline-header-key')?.value.trim();
        const val = row.querySelector('.inline-header-value')?.value;
        if (key) {
            if (headers[key]) {
                headers[key].push(val);
            } else {
                headers[key] = [val];
            }
        }
    });
    return headers;
}

document.getElementById('ruleModalClose').addEventListener('click', () => { disposeModalMonacoEditors(); closeRuleModal(); });
document.getElementById('ruleModalCancelBtn').addEventListener('click', () => { disposeModalMonacoEditors(); closeRuleModal(); });

document.getElementById('ruleModal').addEventListener('modal-opening', disposeModalMonacoEditors);
document.getElementById('ruleModal').addEventListener('click', (e) => {
    if (e.target.classList.contains('modal-overlay')) { disposeModalMonacoEditors(); closeRuleModal(); }
});

document.querySelectorAll('input[name="ruleRequestAction"]').forEach(radio => {
    radio.addEventListener('change', (e) => {
        const action = e.target.value;
        document.getElementById('modifyRequestSection').style.display = action === 'modify' ? '' : 'none';
        document.getElementById('mockRequestSection').style.display = action === 'mock' ? '' : 'none';
        const showResponse = action === 'passthrough' || action === 'modify';
        document.getElementById('responseSection').style.display = showResponse ? '' : 'none';
        if (!showResponse) {
            const realRadio = document.querySelector('input[name="ruleResponseAction"][value="real"]');
            if (realRadio) realRadio.checked = true;
        }

        if (action === 'modify') {
            const container = document.getElementById('modifyBodyEditor');
            initModalMonaco('modifyBodyEditor', container?.dataset.initialBody || '', 'modifyBody');
        }
        if (action === 'mock') {
            const container = document.getElementById('mockRequestBodyEditor');
            initModalMonaco('mockRequestBodyEditor', container?.dataset.initialBody || '', 'mockReqBody');
        }
    });
});

document.querySelectorAll('input[name="ruleResponseAction"]').forEach(radio => {
    radio.addEventListener('change', (e) => {
        document.getElementById('mockResponseSection').style.display = e.target.value === 'response_mock' ? '' : 'none';
        if (e.target.value === 'response_mock') {
            const container = document.getElementById('mockResponseBodyEditor');
            initModalMonaco('mockResponseBodyEditor', container?.dataset.initialBody || '', 'mockRespBody');
        }
    });
});

document.getElementById('addModifyHeader').addEventListener('click', () => addInlineHeaderRow('modifyHeaders'));
document.getElementById('addMockReqHeader').addEventListener('click', () => addInlineHeaderRow('mockRequestHeaders'));
document.getElementById('addMockRespHeader').addEventListener('click', () => addInlineHeaderRow('mockResponseHeaders'));

document.getElementById('modifyHeaders').addEventListener('click', (e) => {
    if (e.target.closest('.header-remove')) removeHeaderRow(e.target);
});
document.getElementById('mockRequestHeaders').addEventListener('click', (e) => {
    if (e.target.closest('.header-remove')) removeHeaderRow(e.target);
});
document.getElementById('mockResponseHeaders').addEventListener('click', (e) => {
    if (e.target.closest('.header-remove')) removeHeaderRow(e.target);
});

['modifyHeaders', 'mockRequestHeaders', 'mockResponseHeaders'].forEach(id => {
    document.getElementById(id).addEventListener('paste', (e) => {
        const target = e.target;
        if (!target.classList.contains('inline-header-key') && !target.classList.contains('inline-header-value')) return;
        const text = (e.clipboardData || window.clipboardData).getData('text');
        if (!text) return;
        const lines = text.split(/\r?\n/).filter(l => l.includes(':'));
        if (lines.length === 0) return;
        e.preventDefault();
        const container = document.getElementById(id);
        container.innerHTML = '';
        let lastValueInput = null;
        lines.forEach(line => {
            const idx = line.indexOf(':');
            const key = line.substring(0, idx).trim();
            const value = line.substring(idx + 1).trim();
            const row = document.createElement('div');
            row.className = 'inline-header-row';
            row.innerHTML = `<input class="inline-header-key" value="${escapeHtml(key)}"><span class="header-colon">:</span><input class="inline-header-value" value="${escapeHtml(value)}"><button class="header-remove" title="Remove">&times;</button>`;
            container.appendChild(row);
            lastValueInput = row.querySelector('.inline-header-value');
        });
        if (lastValueInput) lastValueInput.focus();
    });
});

document.getElementById('ruleModalSaveBtn').addEventListener('click', async () => {
    const modal = document.getElementById('ruleModal');
    const id = modal.dataset.ruleId;
    const name = document.getElementById('ruleName').value.trim();
    if (!name) { alert('Rule name is required'); return; }

    const method = document.getElementById('ruleMethod').value;
    const host = document.getElementById('ruleHost').value.trim();
    const urlPattern = document.getElementById('ruleUrl').value.trim();
    const reqAction = document.querySelector('input[name="ruleRequestAction"]:checked').value;
    const respAction = document.querySelector('input[name="ruleResponseAction"]:checked').value;

    const rule = {
        name,
        match: { method, host, url_pattern: urlPattern },
        action: reqAction,
        enabled: true,
    };

    if (reqAction === 'modify') {
        rule.modified_request = {
            host: document.getElementById('modifyHost').value.trim(),
            url: document.getElementById('modifyUrl').value.trim(),
            headers: readInlineHeaders('modifyHeaders'),
            body: modalMonacoEditors.modifyBody?.getValue() || '',
        };
    }

    if (reqAction === 'mock') {
        rule.mock_response = {
            status: parseInt(document.getElementById('mockRequestStatus').value) || 200,
            headers: readInlineHeaders('mockRequestHeaders'),
            body: modalMonacoEditors.mockReqBody?.getValue() || '',
        };
    }

    const canResponseMock = reqAction === 'passthrough' || reqAction === 'modify';
    if (canResponseMock && respAction === 'response_mock') {
        rule.action = 'response_mock';
        rule.mock_response = {
            status: parseInt(document.getElementById('mockResponseStatus').value) || 200,
            headers: readInlineHeaders('mockResponseHeaders'),
            body: modalMonacoEditors.mockRespBody?.getValue() || '',
        };
    }

    if (id) {
        rule.id = id;
        await updateRule(id, rule);
    } else {
        const result = await createRule(rule);
        if (result && result.deactivated && result.deactivated.length > 0) {
            setTimeout(() => alert(`Deactivated conflicting rules: ${result.deactivated.join(', ')}`), 100);
        }
    }
    disposeModalMonacoEditors();
    closeRuleModal();
});

let matchCheckTimeout = null;
function checkMatchDebounced() {
    clearTimeout(matchCheckTimeout);
    matchCheckTimeout = setTimeout(async () => {
        const modal = document.getElementById('ruleModal');
        if (!modal.classList.contains('open')) return;
        const method = document.getElementById('ruleMethod').value;
        const host = document.getElementById('ruleHost').value.trim();
        const urlPattern = document.getElementById('ruleUrl').value.trim();
        if (!method && !host && !urlPattern) {
            document.getElementById('matchWarning').style.display = 'none';
            return;
        }
        const excludeId = modal.dataset.ruleId || '';
        const matches = await checkMatch(method, host, urlPattern, excludeId);
        const warning = document.getElementById('matchWarning');
        if (matches.length > 0) {
            warning.style.display = '';
            warning.innerHTML = `A rule with this match already exists: <strong>${escapeHtml(matches[0].name)}</strong>. <a data-action="edit-matching-rule" data-rule-id="${matches[0].id}" style="color:#f0883e;cursor:pointer;text-decoration:underline">Edit it instead</a>`;
        } else {
            warning.style.display = 'none';
        }
    }, 300);
}

document.getElementById('ruleMethod').addEventListener('change', checkMatchDebounced);
document.getElementById('ruleHost').addEventListener('input', checkMatchDebounced);
document.getElementById('ruleUrl').addEventListener('input', checkMatchDebounced);

document.getElementById('ruleModal').addEventListener('click', (e) => {
    const link = e.target.closest('[data-action="edit-matching-rule"]');
    if (link) {
        const rule = rules.find(r => r.id === link.dataset.ruleId);
        if (rule) {
            disposeModalMonacoEditors();
            closeRuleModal();
            setTimeout(() => openRuleModal(rule), 100);
        }
    }
});

function toggleKebabMenu(kebab) {
    const menu = kebab.querySelector('.kebab-menu');
    if (!menu) return;
    const isOpen = menu.classList.contains('open');
    closeAllKebabMenus();
    if (!isOpen) menu.classList.add('open');
}

function closeAllKebabMenus() {
    document.querySelectorAll('.kebab-menu.open').forEach(m => m.classList.remove('open'));
}

document.addEventListener('click', (e) => {
    if (e.target.closest('.kebab, .kebab-menu')) return;
    closeAllKebabMenus();
});

// Filter chips
const filterChips = document.getElementById('filterChips');
const filterOverflowPanel = document.getElementById('filterOverflowPanel');
const filterOverflowChips = document.getElementById('filterOverflowChips');
const overflowAddFilterBtn = document.getElementById('overflowAddFilterBtn');

function buildChipHTML(type, label, value, countText) {
    const closeSVG = `<svg width="16" height="16" viewBox="0 0 16 16" fill="none"><path d="M4 4L12 12M12 4L4 12" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>`;
    return `<span class="filter-chip grouped" data-type="${type}"><span class="filter-chip-label">${escapeHtml(label)}:</span> <span class="filter-chip-value">${escapeHtml(value)}</span>${countText ? `<span class="filter-chip-count">${escapeHtml(countText)}</span>` : ''}<span class="filter-chip-close" data-type="${type}">${closeSVG}</span></span>`;
}

function getFilterChipsData() {
    const chips = [];
    if (processFilter.length > 0) {
        const names = processFilter.slice(0, 2).join(', ');
        const extra = processFilter.length > 2 ? ` +${processFilter.length - 2}` : '';
        chips.push({ type: 'process', html: buildChipHTML('process', 'Process', names + extra) });
    }
    return chips;
}

function renderFilterChips() {
    const chips = getFilterChipsData();
    if (chips.length === 0) {
        filterChips.innerHTML = '';
        closeOverflowPanel();
        return;
    }

    filterChips.innerHTML = chips.map(c => c.html).join('');

    requestAnimationFrame(() => {
        if (filterChips.scrollWidth > filterChips.clientWidth + 2) {
            let lastFit = 0;
            const savedHTML = filterChips.innerHTML;
            const chipEls = filterChips.querySelectorAll('.filter-chip');
            for (let i = 0; i < chipEls.length; i++) {
                chipEls[i].style.flexShrink = '0';
            }
            let cumWidth = 0;
            const available = filterChips.clientWidth - 40;
            for (let i = 0; i < chipEls.length; i++) {
                const w = chipEls[i].getBoundingClientRect().width + 6;
                if (cumWidth + w > available) break;
                cumWidth += w;
                lastFit = i + 1;
            }
            if (lastFit < chipEls.length) {
                const overflowCount = chipEls.length - lastFit;
                const visible = chips.slice(0, lastFit).map(c => c.html).join('');
                filterChips.innerHTML = visible + `<span class="filter-chips-more" id="filterChipsMore">+${overflowCount} more</span>`;
                document.getElementById('filterChipsMore').addEventListener('click', toggleOverflowPanel);
            }
        }
        renderOverflowChips();
    });
}

function renderOverflowChips() {
    const chips = getFilterChipsData();
    filterOverflowChips.innerHTML = chips.map(c => c.html).join('');
}

function toggleOverflowPanel() {
    if (filterOverflowPanel.style.display === 'none') {
        openOverflowPanel();
    } else {
        closeOverflowPanel();
    }
}

function openOverflowPanel() {
    renderOverflowChips();
    filterOverflowPanel.style.display = 'flex';
}

function closeOverflowPanel() {
    filterOverflowPanel.style.display = 'none';
}

overflowAddFilterBtn.addEventListener('click', (e) => {
    e.stopPropagation();
    closeOverflowPanel();
    openFilterPopover();
});

document.addEventListener('click', (e) => {
    if (!e.target.closest('.filter-overflow-panel') && !e.target.closest('.filter-chips-more')) {
        closeOverflowPanel();
    }
});

filterChips.addEventListener('click', (e) => {
    const close = e.target.closest('.filter-chip-close');
    if (close) {
        if (close.dataset.type === 'process') setProcessFilter([]);
        renderFilterChips();
        invalidateFilterCache();
        renderList();
        return;
    }
    const chip = e.target.closest('.filter-chip');
    if (chip && chip.dataset.type === 'process') {
        filterPopover.style.display = 'block';
        showStep2Process();
    }
});

filterOverflowChips.addEventListener('click', (e) => {
    const close = e.target.closest('.filter-chip-close');
    if (close) {
        if (close.dataset.type === 'process') setProcessFilter([]);
        closeOverflowPanel();
        renderFilterChips();
        invalidateFilterCache();
        renderList();
        return;
    }
    const chip = e.target.closest('.filter-chip');
    if (chip) {
        closeOverflowPanel();
        if (chip.dataset.type === 'process') { filterPopover.style.display = 'block'; showStep2Process(); }
    }
});

// Add filter popover
const addFilterBtn = document.getElementById('addFilterBtn');
const filterPopover = document.getElementById('filterPopover');
const filterStep1 = document.getElementById('filterStep1');
const filterStep2Process = document.getElementById('filterStep2Process');
const filterTypeSearch = document.getElementById('filterTypeSearch');
const modalProcessInput = document.getElementById('modalProcessInput');
const modalProcessDropdown = document.getElementById('modalProcessDropdown');
const filterAddBtn = document.getElementById('filterAddBtn');
const filterMatchCount = document.getElementById('filterMatchCount');
const filterClearBtn = document.getElementById('filterClearBtn');

function openFilterPopover() {
    filterPopover.style.display = 'block';
    filterClearBtn.style.display = 'none';
    filterStep1.style.display = '';
    filterStep2Process.style.display = 'none';
    filterTypeSearch.value = '';
    filterTypeSearch.focus();
}

function closeFilterPopover() {
    filterPopover.style.display = 'none';
    filterStep1.style.display = '';
    filterStep2Process.style.display = 'none';
}

function showStep2Process() {
    filterStep1.style.display = 'none';
    filterStep2Process.style.display = '';
    filterClearBtn.style.display = '';
    modalProcessInput.value = '';
    modalSelectedProcesses = [...processFilter];
    modalProcessInput.focus();
    renderModalProcessDropdown('');
}

function goBackToStep1() {
    filterStep2Process.style.display = 'none';
    filterStep1.style.display = '';
    filterTypeSearch.value = '';
    filterTypeSearch.focus();
    filterTypeSearch.dispatchEvent(new Event('input'));
}

addFilterBtn.addEventListener('click', (e) => {
    e.stopPropagation();
    if (filterPopover.style.display !== 'none') {
        closeFilterPopover();
    } else {
        openFilterPopover();
    }
});

document.addEventListener('click', (e) => {
    if (!e.target.closest('.filter-add-btn') && !e.target.closest('.filter-popover') && !e.target.closest('.filter-chips') && !e.target.closest('.filter-overflow-panel')) {
        closeFilterPopover();
    }
});

document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') closeFilterPopover();
});

filterTypeSearch.addEventListener('input', () => {
    const q = filterTypeSearch.value.toLowerCase();
    const items = filterStep1.querySelectorAll('.filter-popover-item');
    items.forEach(item => {
        item.style.display = (!q || item.textContent.toLowerCase().includes(q)) ? '' : 'none';
    });
});

filterStep1.addEventListener('click', (e) => {
    const item = e.target.closest('.filter-popover-item');
    if (!item || item.classList.contains('disabled')) return;
    if (item.dataset.type === 'process') showStep2Process();
});

document.getElementById('filterTypeBack')?.addEventListener('click', goBackToStep1);

const modalProcessBack = filterStep2Process.querySelector('.filter-type-back');
if (modalProcessBack) modalProcessBack.addEventListener('click', goBackToStep1);

function getProcessCounts() {
    const counts = {};
    requests.forEach(r => {
        if (r.clientProcess) {
            const name = r.clientDisplayName || r.clientProcess;
            counts[name] = (counts[name] || 0) + 1;
        }
    });
    return counts;
}

let modalSelectedProcesses = [];

function renderModalProcessDropdown(query) {
    const counts = getProcessCounts();
    const processes = Object.keys(counts).sort();
    const q = (query || '').toLowerCase();
    const items = processes.filter(p => !q || p.toLowerCase().includes(q));

    modalProcessDropdown.innerHTML = items.map(p => {
        const selected = modalSelectedProcesses.includes(p);
        return `<div class="process-filter-option${selected ? ' selected' : ''}" data-process="${escapeHtml(p)}"><span class="check">${selected ? '✓' : ''}</span><span>${escapeHtml(p)}</span><span class="count">${counts[p] || 0}</span></div>`;
    }).join('');

    updateFilterAddCount();
    filterClearBtn.style.display = modalSelectedProcesses.length > 0 ? '' : 'none';
}

function updateFilterAddCount() {
    const total = requests.length;
    let matching = total;
    if (modalSelectedProcesses.length > 0) {
        matching = requests.filter(r => {
            const name = r.clientDisplayName || r.clientProcess;
            return modalSelectedProcesses.includes(name);
        }).length;
    }
    filterMatchCount.textContent = `${matching.toLocaleString()} requests`;
}

modalProcessInput.addEventListener('input', () => {
    renderModalProcessDropdown(modalProcessInput.value);
});

modalProcessDropdown.addEventListener('click', (e) => {
    const option = e.target.closest('.process-filter-option');
    if (!option) return;
    const process = option.dataset.process;
    if (modalSelectedProcesses.includes(process)) {
        modalSelectedProcesses = modalSelectedProcesses.filter(p => p !== process);
    } else {
        modalSelectedProcesses.push(process);
    }
    requestAnimationFrame(() => renderModalProcessDropdown(modalProcessInput.value));
});

filterAddBtn.addEventListener('click', (e) => {
    e.stopPropagation();
    setProcessFilter([...modalSelectedProcesses]);
    renderFilterChips();
    invalidateFilterCache();
    renderList();
    goBackToStep1();
});

filterClearBtn.addEventListener('click', (e) => {
    e.stopPropagation();
    modalSelectedProcesses = [];
    renderModalProcessDropdown(modalProcessInput.value);
});

renderFilterChips();

// SSE for signature updates
let eventSource = null;
function connectSSE() {
    eventSource = new EventSource('/api/process/events');
    eventSource.onmessage = (e) => {
        try {
            const data = JSON.parse(e.data);
            if (data.filePath) {
                const signedEl = document.getElementById('originSigned');
                const pathEl = document.getElementById('originPath');
                if (signedEl && pathEl && pathEl.getAttribute('title') === data.filePath) {
                    if (data.isSigned) {
                        signedEl.innerHTML = `<span class="origin-status signed">✓ Signed by ${escapeHtml(data.signerName || 'Unknown')}</span>`;
                    } else {
                        signedEl.innerHTML = '<span class="origin-status unsigned">✗ Unsigned</span>';
                    }
                }
            }
        } catch (err) {}
    };
    eventSource.onerror = () => {
        eventSource.close();
        setTimeout(connectSSE, 3000);
    };
}
connectSSE();
