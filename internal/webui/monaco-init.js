let monacoReady = null;

function initMonaco() {
    if (monacoReady) return monacoReady;

    monacoReady = new Promise((resolve) => {
        require.config({ paths: { vs: '/monaco/vs' } });
        require(['vs/editor/editor.main'], function () {
            monaco.editor.defineTheme('gospy-dark', {
                base: 'vs-dark',
                inherit: true,
                rules: [],
                colors: {
                    'editor.background': '#0d1117',
                    'editor.foreground': '#e0e0e0',
                    'editor.lineHighlightBackground': '#161b2250',
                    'editorCursor.foreground': '#58a6ff',
                    'editor.selectionBackground': '#264f7850'
                }
            });
            resolve(monaco);
        });
    });

    return monacoReady;
}

function createMonacoEditor(container, value, language) {
    return initMonaco().then((monaco) => {
        const editor = monaco.editor.create(container, {
            value: value || '',
            language: language || 'json',
            theme: 'gospy-dark',
            minimap: { enabled: false },
            fontSize: 17,
            fontFamily: 'monospace',
            lineNumbers: 'on',
            scrollBeyondLastLine: false,
            automaticLayout: true,
            tabSize: 2,
            wordWrap: 'on',
            padding: { top: 10, bottom: 10 },
            scrollbar: {
                verticalScrollbarSize: 8,
                horizontalScrollbarSize: 8
            },
            renderLineHighlight: 'line',
            bracketPairColorization: { enabled: false },
            folding: true,
            unfoldOnClickAfterEnd: false
        });
        return editor;
    });
}
