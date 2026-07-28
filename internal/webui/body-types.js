import { selectedId } from './state.js';

function escapeHtml(s) {
    if (!s) return '';
    return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

function formatBytes(bytes) {
    if (bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
}

const BINARY_ICON_SVG = '<svg class="binary-icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="14" y="14" width="4" height="6" rx="2"/><rect x="6" y="4" width="4" height="6" rx="2"/><path d="M6 20h4"/><path d="M14 10h4"/><path d="M6 14h2v6"/><path d="M14 4h2v6"/></svg>';

const bodyTypes = [];

let _refreshDetail = null;
let _createMonacoEditor = null;
let _mapContentType = null;
let _renderCurrentContent = null;
let _savedToolbarHtml = null;
let _activeMonacoEditor = null;

export function initBodyTypes(deps) {
    _refreshDetail = deps.refreshDetail;
    _createMonacoEditor = deps.createMonacoEditor;
    _mapContentType = deps.mapContentType;
    _renderCurrentContent = deps.renderCurrentContent;
}

export function getActiveEditor() { return _activeMonacoEditor; }
export function getSavedToolbarHtml() { return _savedToolbarHtml; }

export function registerBodyType(config) {
    bodyTypes.push(config);
}

export function detectBodyType(contentType, entry, isBinaryBody) {
    if (!contentType) return isBinaryBody ? 'binary' : 'text';
    const ct = contentType.toLowerCase();
    for (const t of bodyTypes) {
        if (t.detect && t.detect(ct, entry, isBinaryBody)) return t.name;
    }
    return isBinaryBody ? 'binary' : 'text';
}

export function detectBodyTypeFromDOM(target) {
    const panel = document.querySelector(`.section-panel[data-body-target="${target}"]`);
    if (!panel) return 'text';
    for (const t of bodyTypes) {
        if (t.detectFromDOM && t.detectFromDOM(panel)) return t.name;
    }
    return 'text';
}

export function getEntryData(entry, contentType, isBinaryBody) {
    const bodyType = detectBodyType(contentType, entry, isBinaryBody);
    const config = getTypeConfig(bodyType);
    return config?.getEntryData ? config.getEntryData(entry) : {};
}

function getTypeConfig(name) {
    return bodyTypes.find(t => t.name === name) || bodyTypes.find(t => t.name === 'text');
}

export function getKebabItems(bodyType, target, canEdit, hasEdited, entryId) {
    const config = getTypeConfig(bodyType);
    return config ? config.getKebabItems(target, canEdit, hasEdited, entryId) : [];
}

export function renderContent(bodyType, target, data) {
    const config = getTypeConfig(bodyType);
    return config ? config.renderContent(target, data) : '';
}

export function postRenderBody(target) {
    const type = detectBodyTypeFromDOM(target);
    const config = getTypeConfig(type);
    if (config?.postRender) config.postRender(target);
}

export function isEditable(bodyType) {
    const config = getTypeConfig(bodyType);
    return config ? config.isEditable : false;
}

export function editBody(target) {
    const type = detectBodyTypeFromDOM(target);
    const config = getTypeConfig(type);
    if (config?.edit) config.edit(target);
}

export function saveBody(target) {
    const type = detectBodyTypeFromDOM(target);
    const config = getTypeConfig(type);
    if (config?.save) config.save(target);
}

export function cancelBody(target) {
    const type = detectBodyTypeFromDOM(target);
    const config = getTypeConfig(type);
    if (config?.cancel) config.cancel(target);
}

export function setBodyView(target, view) {
    const sectionPanel = document.querySelector(`.section-panel[data-body-target="${target}"]`);
    if (!sectionPanel) return;
    sectionPanel.querySelectorAll('[data-action="set-view"]').forEach(b => {
        b.classList.toggle('active', b.dataset.view === view);
    });
    const type = detectBodyTypeFromDOM(target);
    const config = getTypeConfig(type);
    if (config?.setView) config.setView(target, view);
}

export function copyBody(target) {
    const type = detectBodyTypeFromDOM(target);
    const config = getTypeConfig(type);
    if (config?.copy) config.copy(target);
}

// ── Text body type ──────────────────────────────────────────────

registerBodyType({
    name: 'text',
    isEditable: true,

    getKebabItems(target, canEdit, hasEdited, entryId) {
        const items = [];
        items.push({ action: 'copy-body', label: '⧉ Copy', target });
        if (canEdit) items.push({ action: 'edit-body', label: '✎ Edit', target });
        if (hasEdited) items.push({ action: 'revert-body', label: '↩ Revert', target });
        return items;
    },

    renderContent(target, data) {
        const { body, rawBody, compression, isModified, modifiedBody, isMocked, mockedBody, hasEdited, editedBody, defaultContent } = data;
        const displayBody = body;
        return `<pre class="body-content" data-body-target="${target}" data-decoded="${escapeHtml((isMocked && mockedBody) ? mockedBody : body)}" data-raw="${escapeHtml(rawBody)}" data-edited="${escapeHtml(hasEdited ? editedBody : '')}" data-modified="${escapeHtml(isModified ? modifiedBody : '')}" data-mocked="${escapeHtml(isMocked ? body : '')}" data-compression="${compression}" data-view-mode="pretty" data-content-mode="${defaultContent}">${escapeHtml(displayBody)}</pre>`;
    },

    setView(target, view) {
        const sectionPanel = document.querySelector(`.section-panel[data-body-target="${target}"]`);
        if (!sectionPanel) return;
        const pre = sectionPanel.querySelector('pre[data-body-target]');
        if (pre) {
            pre.dataset.viewMode = view;
            _renderCurrentContent(target);
        }
    },

    edit(target) {
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
        const autoLang = _mapContentType(contentType);
        const savedLang = localStorage.getItem('gospy-editor-lang');
        const lang = autoLang || savedLang || 'json';
        const tools = sectionPanel.querySelector('.content-toolbar');
        _savedToolbarHtml = tools.innerHTML;
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
        _createMonacoEditor(editorContainer, content, lang).then((editor) => {
            _activeMonacoEditor = editor;

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
    },

    save(target) {
        const pre = document.querySelector(`pre[data-body-target="${target}"]`);
        if (!pre) return;
        const sectionPanel = pre.closest('.section-panel');
        if (!sectionPanel) return;

        if (!_activeMonacoEditor) return;
        const value = _activeMonacoEditor.getValue();
        let formatted = value;
        try {
            const parsed = JSON.parse(value);
            formatted = JSON.stringify(parsed, null, 2);
        } catch {
            // not JSON
        }

        fetch(`/api/requests/${selectedId}/body`, {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ target, body: formatted })
        }).then(r => r.json()).then(() => {
            pre.dataset.edited = formatted;
            pre.textContent = formatted;

            _activeMonacoEditor.dispose();
            _activeMonacoEditor = null;
            const container = sectionPanel.querySelector('.monaco-editor-container');
            if (container) container.remove();
            pre.style.display = '';

            if (_savedToolbarHtml) {
                const toolsDiv = sectionPanel.querySelector('.content-toolbar');
                let html = _savedToolbarHtml;
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
                _savedToolbarHtml = null;
            }
            pre.dataset.contentMode = 'edited';
            const kebabMenu = sectionPanel.querySelector('.kebab-menu');
            if (kebabMenu && !kebabMenu.querySelector('[data-action="revert-body"]')) {
                kebabMenu.insertAdjacentHTML('beforeend', '<div class="menu-item" data-action="revert-body" data-target="' + target + '">↩ Revert</div>');
            }
            _renderCurrentContent(target);
        }).catch(e => console.error('Failed to save body:', e));
    },

    cancel(target) {
        const pre = document.querySelector(`pre[data-body-target="${target}"]`);
        if (!pre) return;
        const sectionPanel = pre.closest('.section-panel');
        if (!sectionPanel) return;

        if (_activeMonacoEditor) {
            _activeMonacoEditor.dispose();
            _activeMonacoEditor = null;
        }

        const container = sectionPanel.querySelector('.monaco-editor-container');
        if (container) container.remove();

        pre.style.display = '';
        const tools = sectionPanel.querySelector('.content-toolbar');
        if (_savedToolbarHtml) {
            tools.innerHTML = _savedToolbarHtml;
            _savedToolbarHtml = null;
        }
        _renderCurrentContent(target);
    },

    copy(target) {
        const pre = document.querySelector(`pre[data-body-target="${target}"]`);
        if (!pre) return;
        const content = pre.dataset.edited || pre.dataset.decoded || pre.textContent || '';
        navigator.clipboard.writeText(content);
    },
});

// ── Binary body type ────────────────────────────────────────────

registerBodyType({
    name: 'binary',
    isEditable: false,

    detectFromDOM(panel) {
        const pre = panel.querySelector('pre[data-binary]');
        if (!pre) return false;
        return !panel.querySelector('.proto-tree[data-body-target]')
            && !panel.querySelector('.multipart-parts[data-body-target]');
    },

    getKebabItems(target, canEdit, hasEdited, entryId) {
        return [
            { action: 'copy-hex', label: '⧉ Copy hex', target },
            { action: 'download-bin', label: '⬇ Download .bin', target, entryId },
        ];
    },

    renderContent(target, data) {
        const { bodyHex, contentType, bodySize, bodyTarget } = data;
        return `<div class="binary-placeholder" data-body-target="${target}">${BINARY_ICON_SVG} Binary ${escapeHtml(bodyTarget)} body — ${escapeHtml(contentType || 'unknown')} (${formatBytes(bodySize)})</div><pre class="body-content" data-body-target="${target}" data-binary="true" data-view-mode="raw" style="display:none">${escapeHtml(bodyHex)}</pre>`;
    },

    setView(target, view) {
        const sectionPanel = document.querySelector(`.section-panel[data-body-target="${target}"]`);
        if (!sectionPanel) return;
        const placeholder = sectionPanel.querySelector('.binary-placeholder');
        const pre = sectionPanel.querySelector('pre[data-body-target]');
        if (placeholder) placeholder.style.display = view === 'pretty' ? '' : 'none';
        if (pre) pre.style.display = view === 'raw' ? '' : 'none';
    },

    copy(target) {
        const pre = document.querySelector(`pre[data-body-target="${target}"][data-binary]`);
        if (!pre) return;
        navigator.clipboard.writeText(pre.textContent || '');
    },
});

// ── Multipart body type ─────────────────────────────────────────

registerBodyType({
    name: 'multipart',
    isEditable: true,

    detect(ct) {
        return ct.includes('multipart/form-data');
    },

    detectFromDOM(panel) {
        return !!panel.querySelector('.multipart-parts[data-body-target]');
    },

    getEntryData(entry) {
        return { parsedMultipart: entry?.parsedMultipart || [] };
    },

    getKebabItems(target, canEdit, hasEdited, entryId) {
        const items = [];
        items.push({ action: 'copy-body', label: '⧉ Copy', target });
        if (canEdit) items.push({ action: 'edit-body', label: '✎ Edit', target });
        if (hasEdited) items.push({ action: 'revert-body', label: '↩ Revert', target });
        items.push({ action: 'download-bin', label: '⬇ Download .bin', target, entryId });
        return items;
    },

    renderContent(target, data) {
        const { parsedMultipart, bodyHex, body, bodyTarget } = data;
        const multipartHasBinary = parsedMultipart.some(p => p.isBinary);

        const partCards = parsedMultipart.map((p, i) => {
            if (p.isBinary) {
                const badges = [];
                if (p.contentType) badges.push(`<span class="body-badge">${escapeHtml(p.contentType)}</span>`);
                if (p.filename) badges.push(`<span class="body-badge">${escapeHtml(p.filename)}</span>`);
                if (p.size) badges.push(`<span class="body-badge">${formatBytes(p.size)}</span>`);
                return `<div class="multipart-field multipart-binary" data-part-index="${i}">
                    <div class="multipart-field-header">
                        <span class="multipart-field-name">${escapeHtml(p.name)}</span>
                        ${badges.join('')}
                    </div>
                    <div class="multipart-binary-preview">${BINARY_ICON_SVG} Binary data</div>
                    <pre class="multipart-field-hex" data-view-mode="raw" style="display:none">${escapeHtml(p.hexPreview || '')}</pre>
                </div>`;
            }
            const badges = [];
            if (p.contentType) badges.push(`<span class="body-badge">${escapeHtml(p.contentType)}</span>`);
            return `<div class="multipart-field" data-part-index="${i}">
                <div class="multipart-field-header">
                    <span class="multipart-field-name">${escapeHtml(p.name)}</span>
                    ${badges.join('')}
                </div>
                <pre class="multipart-field-value" data-part-name="${escapeHtml(p.name)}">${escapeHtml(p.value || '')}</pre>
            </div>`;
        }).join('');

        const rawDisplay = multipartHasBinary ? escapeHtml(bodyHex || body) : escapeHtml(body);
        return `<div class="multipart-parts" data-body-target="${target}" data-view-mode="pretty">${partCards}</div><pre class="body-content multipart-raw" data-body-target="${target}" data-multipart="true" data-view-mode="raw" style="display:none">${rawDisplay}</pre>`;
    },

    setView(target, view) {
        const sectionPanel = document.querySelector(`.section-panel[data-body-target="${target}"]`);
        if (!sectionPanel) return;
        const multipartParts = sectionPanel.querySelector('.multipart-parts[data-body-target]');
        const pre = sectionPanel.querySelector('pre[data-body-target]');
        if (multipartParts) multipartParts.style.display = view === 'pretty' ? '' : 'none';
        if (pre) pre.style.display = view === 'raw' ? '' : 'none';
    },

    postRender(target) {
        const sectionPanel = document.querySelector(`.section-panel[data-body-target="${target}"]`);
        if (!sectionPanel) return;
        sectionPanel.querySelectorAll('.multipart-field:not(.multipart-binary) .multipart-field-value').forEach(pre => {
            try {
                const obj = JSON.parse(pre.textContent);
                if (typeof obj !== 'object' || obj === null) return;
                const container = document.createElement('div');
                container.className = 'json-viewer-container multipart-json';
                pre.parentNode.insertBefore(container, pre);
                const viewer = new JSONViewer();
                container.appendChild(viewer.getContainer());
                viewer.showJSON(obj, -1, 1);
                pre.style.display = 'none';
            } catch {
                // not valid JSON, keep as raw text
            }
        });
    },

    edit(target) {
        const sectionPanel = document.querySelector(`.section-panel[data-body-target="${target}"]`);
        if (!sectionPanel) return;

        // Force pretty mode
        const viewBtns = sectionPanel.querySelectorAll('[data-action="set-view"]');
        const prettyBtn = Array.from(viewBtns).find(b => b.dataset.view === 'pretty');
        if (prettyBtn && !prettyBtn.classList.contains('active')) {
            const config = getTypeConfig('multipart');
            if (config?.setView) config.setView(target, 'pretty');
            viewBtns.forEach(b => b.classList.toggle('active', b.dataset.view === 'pretty'));
        }

        const container = sectionPanel.querySelector('.multipart-parts[data-body-target]');
        if (!container) return;

        container.querySelectorAll('.json-viewer-container.multipart-json').forEach(el => {
            el.style.display = 'none';
        });
        container.querySelectorAll('.multipart-field:not(.multipart-binary) .multipart-field-value').forEach(el => {
            el.style.display = '';
        });

        container.querySelectorAll('.multipart-field-value').forEach(el => {
            el.dataset.original = el.textContent;
            el.contentEditable = 'true';
        });
        container.classList.add('multipart-editing');

        const tools = sectionPanel.querySelector('.content-toolbar');
        if (tools) {
            _savedToolbarHtml = tools.innerHTML;
            tools.innerHTML = `<div class="body-tools-group">
                <button class="body-tool body-tool-save" data-action="save-body" data-target="${target}">Save</button>
                <button class="body-tool body-tool-cancel" data-action="cancel-body" data-target="${target}">Cancel</button>
            </div>`;
        }
    },

    save(target) {
        const sectionPanel = document.querySelector(`.section-panel[data-body-target="${target}"]`);
        if (!sectionPanel) return;
        const container = sectionPanel.querySelector('.multipart-parts[data-body-target]');
        if (!container) return;

        const parts = [];
        container.querySelectorAll('.multipart-field:not(.multipart-binary)').forEach(field => {
            const name = field.querySelector('.multipart-field-name').textContent;
            const valueEl = field.querySelector('.multipart-field-value');
            parts.push({ name, value: valueEl.textContent });
        });

        fetch(`/api/requests/${selectedId}/body-multipart`, {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ parts })
        }).then(r => r.json()).then(() => {
            _savedToolbarHtml = null;
            _refreshDetail();
        }).catch(e => console.error('Failed to save multipart:', e));
    },

    cancel(target) {
        const sectionPanel = document.querySelector(`.section-panel[data-body-target="${target}"]`);
        if (!sectionPanel) return;
        const container = sectionPanel.querySelector('.multipart-parts[data-body-target]');
        if (!container) return;

        container.querySelectorAll('.multipart-field-value').forEach(el => {
            if (el.dataset.original !== undefined) {
                el.textContent = el.dataset.original;
                delete el.dataset.original;
            }
            el.contentEditable = 'false';
        });
        container.classList.remove('multipart-editing');

        const tools = sectionPanel.querySelector('.content-toolbar');
        if (tools && _savedToolbarHtml) {
            tools.innerHTML = _savedToolbarHtml;
            _savedToolbarHtml = null;
        }

        container.querySelectorAll('.json-viewer-container.multipart-json').forEach(el => el.remove());
        container.querySelectorAll('.multipart-field:not(.multipart-binary) .multipart-field-value').forEach(pre => {
            try {
                const obj = JSON.parse(pre.textContent);
                if (typeof obj !== 'object' || obj === null) return;
                const vc = document.createElement('div');
                vc.className = 'json-viewer-container multipart-json';
                pre.parentNode.insertBefore(vc, pre);
                const viewer = new JSONViewer();
                vc.appendChild(viewer.getContainer());
                viewer.showJSON(obj, -1, 1);
                pre.style.display = 'none';
            } catch {
                pre.style.display = '';
            }
        });
    },

    copy(target) {
        const container = document.querySelector(`.multipart-parts[data-body-target="${target}"]`);
        if (!container) return;
        const parts = [];
        container.querySelectorAll('.multipart-field:not(.multipart-binary)').forEach(field => {
            const name = field.querySelector('.multipart-field-name').textContent;
            const value = field.querySelector('.multipart-field-value').textContent;
            parts.push(`${name}=${value}`);
        });
        navigator.clipboard.writeText(parts.join('\n'));
    },
});

// ── Protobuf body type ──────────────────────────────────────────

function protoTypeBadge(wireType) {
    const map = {
        varint: 'varint',
        fixed32: 'fixed32',
        fixed64: 'fixed64',
        string: 'string',
        message: 'message',
        bytes: 'bytes',
    };
    return map[wireType] || wireType;
}

function protoValueHtml(field) {
    switch (field.wireType) {
        case 'varint':
        case 'fixed32':
        case 'fixed64': {
            let inner = `<span class="proto-val">${field.value}</span>`;
            if (field.zigzagValue !== undefined && field.zigzagValue !== null) {
                inner += ` <span class="proto-zigzag">(zigzag ${field.zigzagValue})</span>`;
            }
            return `<span class="proto-val-nowrap">${inner}</span>`;
        }
        case 'string': {
            const s = field.value;
            return `<span class="proto-val proto-val-string">${escapeHtml(s)}</span>`;
        }
        case 'bytes':
            return `<span class="proto-val proto-val-hex">${escapeHtml(String(field.value || ''))}</span>`;
        default:
            return `<span class="proto-val">${escapeHtml(String(field.value || ''))}</span>`;
    }
}

function renderProtoTable(fields) {
    if (!fields || fields.length === 0) return '';
    const rows = fields.map(f => {
        const hasSubs = f.subFields && f.subFields.length > 0;
        const badge = protoTypeBadge(f.wireType);
        const badgeLabel = hasSubs ? `${badge} (${f.byteSize} bytes)` : badge;
        let valueCell;
        if (hasSubs) {
            valueCell = renderProtoTable(f.subFields);
        } else {
            valueCell = protoValueHtml(f);
        }
        const byteRange = f.byteEnd != null ? `${f.byteOffset}-${f.byteEnd}` : '';
        return `<tr>
            <td class="proto-td-proto">${f.fieldNumber}</td>
            <td class="proto-td-type"><span class="proto-badge proto-badge-${f.wireType}">${badgeLabel}</span></td>
            <td class="proto-td-bytes">${byteRange}</td>
            <td class="proto-td-value">${valueCell}</td>
        </tr>`;
    }).join('');
    return `<table class="proto-table"><thead><tr><th>Field</th><th>Type</th><th>Bytes</th><th>Value</th></tr></thead><tbody>${rows}</tbody></table>`;
}

registerBodyType({
    name: 'protobuf',
    isEditable: false,

    detect(ct, entry) {
        return (ct.includes('protobuf') || ct.includes('x-protobuf')) && entry?.parsedProtobuf?.length > 0;
    },

    detectFromDOM(panel) {
        return !!panel.querySelector('.proto-tree[data-body-target]');
    },

    getEntryData(entry) {
        return { parsedProtobuf: entry?.parsedProtobuf || [] };
    },

    getKebabItems(target, canEdit, hasEdited, entryId) {
        return [
            { action: 'copy-hex', label: '⧉ Copy hex', target },
            { action: 'download-bin', label: '⬇ Download .bin', target, entryId },
        ];
    },

    renderContent(target, data) {
        const { bodyHex, parsedProtobuf } = data;
        const treeHtml = parsedProtobuf && parsedProtobuf.length > 0
            ? renderProtoTable(parsedProtobuf)
            : '<span class="proto-empty">No fields decoded</span>';

        return `<div class="proto-tree" data-body-target="${target}" data-view-mode="pretty">${treeHtml}</div><pre class="body-content" data-body-target="${target}" data-binary="true" data-view-mode="raw" style="display:none">${escapeHtml(bodyHex || '')}</pre>`;
    },

    setView(target, view) {
        const sectionPanel = document.querySelector(`.section-panel[data-body-target="${target}"]`);
        if (!sectionPanel) return;
        const tree = sectionPanel.querySelector('.proto-tree[data-body-target]');
        const pre = sectionPanel.querySelector('pre[data-body-target]');
        if (tree) tree.style.display = view === 'pretty' ? '' : 'none';
        if (pre) pre.style.display = view === 'raw' ? '' : 'none';
    },

    copy(target) {
        const pre = document.querySelector(`pre[data-body-target="${target}"][data-binary]`);
        if (!pre) return;
        navigator.clipboard.writeText(pre.textContent || '');
    },
});
