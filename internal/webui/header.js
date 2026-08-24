// header.js - reusable header action bar.
//
// Renders an ordered list of `items` into a container and interleaves a
// .header-sep between adjacent visible units. Items that belong to the same
// unit (e.g. a button and its own checkbox) set `sep: false` to glue
// themselves to the previous item. Items hidden in a given mode declare
// `hiddenIn: ['<mode>']`: they stay in the DOM (so ids and events keep
// working) but are display:none, and their separators collapse - no
// orphaned palotes can survive a mode change.
let containerEl = null;
let items = [];
let mode = 'normal';

export function initHeader(containerId, headerItems) {
  containerEl = document.getElementById(containerId);
  items = headerItems;
  render();
}

export function setHeaderMode(nextMode) {
  if (nextMode === mode) return;
  mode = nextMode;
  render();
}

function render() {
  // Snapshot checkbox states before innerHTML wipe restores them after DOM rebuild.
  const cbStates = {};
  for (const item of items) {
    const el = document.getElementById(item.id);
    if (!el) continue;
    const cb = el.querySelector('input[type="checkbox"]');
    if (cb) cbStates[item.id] = cb.checked;
  }

  const visible = items.filter((item) => !(item.hiddenIn || []).includes(mode));
  let html = '';
  let hasPrev = false;
  for (const item of visible) {
    if (hasPrev && item.sep !== false) html += '<div class="header-sep"></div>';
    html += item.html;
    hasPrev = true;
  }
  containerEl.innerHTML = html;
  for (const item of items) {
    const el = document.getElementById(item.id);
    if (!el) continue;
    const hidden = (item.hiddenIn || []).includes(mode);
    el.style.display = hidden ? 'none' : '';
    if (cbStates[item.id] !== undefined) {
      const cb = el.querySelector('input[type="checkbox"]');
      if (cb) cb.checked = cbStates[item.id];
    }
    if (hidden || !item.events) continue;
    for (const [type, handler] of Object.entries(item.events)) {
      el.addEventListener(type, handler);
    }
  }
}
