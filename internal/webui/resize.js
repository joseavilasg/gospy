// resize.js - generic drag-to-resize for docked panels.
//
// Dragging `handle` adjusts the `target` element's width or height,
// clamped to [min, max]. With `persistKey`, the dimension is saved to
// localStorage on drag end and restored on attach. Reusable: attach one
// instance per panel/section.
//
// direction: 'vertical' (default) adjusts height; 'horizontal' adjusts width.
export function makeResizable(handle, target, opts = {}) {
  const {
    persistKey = null,
    direction = 'vertical',
    min = 120,
    max = direction === 'horizontal'
      ? () => window.innerWidth * 0.7
      : () => window.innerHeight * 0.7,
    onResize = null,
  } = opts;

  const isH = direction === 'horizontal';
  const cls = isH ? 'resizing-h' : 'resizing-v';

  function setDimension(v) {
    const clamped = Math.round(Math.min(max(), Math.max(min, v)));
    if (isH) {
      target.style.width = `${clamped}px`;
    } else {
      target.style.height = `${clamped}px`;
    }
    if (onResize) onResize(clamped);
  }

  function restore() {
    if (!persistKey) return;
    try {
      const v = parseInt(localStorage.getItem(persistKey), 10);
      if (Number.isFinite(v)) setDimension(v);
    } catch { /* localStorage unavailable */ }
  }

  handle.addEventListener('pointerdown', (e) => {
    if (e.button !== 0) return;
    e.preventDefault();
    const startCoord = isH ? e.clientX : e.clientY;
    const startSize = isH
      ? target.getBoundingClientRect().width
      : target.getBoundingClientRect().height;
    document.body.classList.add(cls);

    const onMove = (ev) => {
      ev.preventDefault();
      const delta = isH ? (startCoord - ev.clientX) : (startCoord - ev.clientY);
      setDimension(startSize + delta);
    };
    const onUp = () => {
      window.removeEventListener('pointermove', onMove);
      window.removeEventListener('pointerup', onUp);
      window.removeEventListener('pointercancel', onUp);
      document.body.classList.remove(cls);
      if (persistKey) {
        try {
          const v = isH
            ? target.getBoundingClientRect().width
            : target.getBoundingClientRect().height;
          localStorage.setItem(persistKey, String(v));
        } catch { /* localStorage unavailable */ }
      }
    };
    window.addEventListener('pointermove', onMove);
    window.addEventListener('pointerup', onUp);
    window.addEventListener('pointercancel', onUp);
  });

  restore();

  return {
    setDimension,
    setHeight: isH ? undefined : setDimension,
    setWidth: isH ? setDimension : undefined,
    restore,
  };
}
