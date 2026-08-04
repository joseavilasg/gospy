// resize.js — generic drag-to-resize for docked panels.
//
// Vertical resize: dragging `handle` adjusts the `target` element's height,
// clamped to [min, max]. With `persistKey`, the height is saved to
// localStorage on drag end and restored on attach, so the panel reopens at
// the same size. Reusable: attach one instance per panel/section.
export function makeResizable(handle, target, opts = {}) {
    const {
        persistKey = null,
        min = 120,
        max = () => window.innerHeight * 0.7,
        onResize = null,
    } = opts;

    function setHeight(h) {
        const clamped = Math.round(Math.min(max(), Math.max(min, h)));
        target.style.height = `${clamped}px`;
        if (onResize) onResize(clamped);
    }

    function restore() {
        if (!persistKey) return;
        try {
            const h = parseInt(localStorage.getItem(persistKey), 10);
            if (Number.isFinite(h)) setHeight(h);
        } catch { /* localStorage unavailable */ }
    }

    handle.addEventListener('pointerdown', (e) => {
        if (e.button !== 0) return;
        e.preventDefault();
        const startY = e.clientY;
        const startH = target.getBoundingClientRect().height;
        document.body.classList.add('resizing');

        const onMove = (ev) => {
            ev.preventDefault();
            setHeight(startH - (ev.clientY - startY));
        };
        const onUp = () => {
            window.removeEventListener('pointermove', onMove);
            window.removeEventListener('pointerup', onUp);
            window.removeEventListener('pointercancel', onUp);
            document.body.classList.remove('resizing');
            if (persistKey) {
                try {
                    localStorage.setItem(persistKey, String(target.getBoundingClientRect().height));
                } catch { /* localStorage unavailable */ }
            }
        };
        window.addEventListener('pointermove', onMove);
        window.addEventListener('pointerup', onUp);
        window.addEventListener('pointercancel', onUp);
    });

    restore();

    return { setHeight, restore };
}
