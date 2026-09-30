/**
 * MCP Gateway Admin Console - lightweight progressive enhancement.
 */

const normalizeTableText = value => String(value ?? '').trim().toLocaleLowerCase();

function persistTableState(search, sortKey, sortDirection) {
  if (typeof window === 'undefined' || !window.history || !window.location || typeof URL === 'undefined') return;
  const url = new URL(window.location.href);
  if (search) url.searchParams.set('q', search);
  else url.searchParams.delete('q');
  if (sortKey && sortDirection) {
    url.searchParams.set('sort', sortKey);
    url.searchParams.set('dir', sortDirection);
  } else {
    url.searchParams.delete('sort');
    url.searchParams.delete('dir');
  }
  window.history.replaceState(null, '', url);
}

function setupDataTable(table) {
  const tbody = table.tBodies?.[0] || table.querySelector('tbody');
  if (!tbody) return;

  const rows = Array.from(tbody.querySelectorAll('tr')).filter(row => row.children.length > 1);
  rows.forEach((row, index) => { row.dataset.originalIndex = String(index); });

  let search = '';
  let sortKey = '';
  let sortDirection = '';
  let highImpactOnly = false;

  const apply = () => {
    rows.forEach(row => {
      const matchesSearch = !search || normalizeTableText(row.textContent).includes(normalizeTableText(search));
      const matchesImpact = !highImpactOnly || row.dataset.highImpact === 'true';
      row.hidden = !(matchesSearch && matchesImpact);
    });

    const header = sortKey ? table.querySelector('th[data-sort-key="' + sortKey + '"]') : null;
    if (!header || !sortDirection) {
      rows
        .slice()
        .sort((a, b) => Number(a.dataset.originalIndex) - Number(b.dataset.originalIndex))
        .forEach(row => tbody.appendChild(row));
      persistTableState(search, '', '');
      return;
    }

    const column = Array.from(header.parentElement.children).indexOf(header);
    const numeric = header.dataset.sortType === 'number';
    rows
      .slice()
      .sort((a, b) => {
        const av = a.children[column]?.dataset.sortValue ?? a.children[column]?.textContent ?? '';
        const bv = b.children[column]?.dataset.sortValue ?? b.children[column]?.textContent ?? '';
        let result;
        if (numeric) result = Number(av) - Number(bv);
        else result = String(av).localeCompare(String(bv), undefined, { numeric: true, sensitivity: 'base' });
        return sortDirection === 'desc' ? -result : result;
      })
      .forEach(row => tbody.appendChild(row));

    persistTableState(search, sortKey, sortDirection);
  };

  const searchLabel = table.dataset.searchLabel;
  if (searchLabel && typeof document.createElement === 'function') {
    const controls = document.createElement('div');
    controls.className = 'mb-3 flex flex-wrap items-center gap-2';
    const input = document.createElement('input');
    input.type = 'search';
    input.className = 'field max-w-md';
    input.placeholder = searchLabel;
    input.setAttribute('aria-label', searchLabel);
    controls.appendChild(input);
    table.parentElement?.insertAdjacentElement('beforebegin', controls);

    if (typeof window !== 'undefined' && window.location && typeof URLSearchParams !== 'undefined') {
      const params = new URLSearchParams(window.location.search || '');
      search = params.get('q') || '';
      input.value = search;
    }

    input.addEventListener('input', () => {
      search = input.value;
      apply();
    });
  }

  if (table.dataset.highImpactToggle === 'true' && typeof document.createElement === 'function') {
    const controls = document.createElement('div');
    controls.className = 'mb-3 flex items-center gap-2';
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'btn-secondary';
    button.textContent = 'High impact only';
    button.setAttribute('aria-pressed', 'false');
    button.addEventListener('click', () => {
      highImpactOnly = !highImpactOnly;
      button.setAttribute('aria-pressed', String(highImpactOnly));
      button.textContent = highImpactOnly ? 'Show all grants' : 'High impact only';
      apply();
    });
    controls.appendChild(button);
    table.parentElement?.insertAdjacentElement('beforebegin', controls);
  }

  table.querySelectorAll('th[data-sort-key]').forEach(header => {
    const label = header.textContent.trim();
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'inline-flex items-center gap-1 text-left hover:text-white';
    button.textContent = label;
    const indicator = document.createElement('span');
    indicator.setAttribute('aria-hidden', 'true');
    button.appendChild(indicator);
    header.textContent = '';
    header.appendChild(button);
    header.setAttribute('aria-sort', 'none');

    button.addEventListener('click', () => {
      if (sortKey !== header.dataset.sortKey) {
        sortKey = header.dataset.sortKey;
        sortDirection = 'asc';
      } else if (sortDirection === 'asc') {
        sortDirection = 'desc';
      } else if (sortDirection === 'desc') {
        sortKey = '';
        sortDirection = '';
      } else {
        sortDirection = 'asc';
      }

      table.querySelectorAll('th[data-sort-key]').forEach(other => {
        const active = other === header && Boolean(sortDirection);
        other.setAttribute('aria-sort', active ? (sortDirection === 'asc' ? 'ascending' : 'descending') : 'none');
        const marker = other.querySelector('button span[aria-hidden="true"]');
        if (marker) marker.textContent = active ? (sortDirection === 'asc' ? ' ↑' : ' ↓') : '';
      });
      apply();
    });
  });

  if (typeof window !== 'undefined' && window.location && typeof URLSearchParams !== 'undefined') {
    const params = new URLSearchParams(window.location.search || '');
    const requestedSort = params.get('sort') || '';
    const requestedDirection = params.get('dir') || '';
    const header = requestedSort ? table.querySelector('th[data-sort-key="' + requestedSort + '"]') : null;
    if (header && ['asc', 'desc'].includes(requestedDirection)) {
      sortKey = requestedSort;
      sortDirection = requestedDirection;
      header.setAttribute('aria-sort', sortDirection === 'asc' ? 'ascending' : 'descending');
      const marker = header.querySelector('button span[aria-hidden="true"]');
      if (marker) marker.textContent = sortDirection === 'asc' ? ' ↑' : ' ↓';
    }
  }

  apply();
}

function setupProjectScope(targetSelect) {
  const projectSelectId = targetSelect?.dataset?.projectSelect;
  if (!projectSelectId) return;
  const projectSelect = document.getElementById(projectSelectId);
  if (!projectSelect) return;

  const apply = () => {
    const targetId = targetSelect.value;
    Array.from(projectSelect.options || []).forEach(option => {
      const projectTargetId = option.dataset?.targetId || '';
      const available = option.value === '*' || (targetId !== '*' && projectTargetId === targetId);
      option.hidden = !available;
      option.disabled = !available;
    });

    const selected = projectSelect.options?.[projectSelect.selectedIndex];
    if (selected?.disabled) projectSelect.value = '*';
  };

  targetSelect.addEventListener('change', apply);
  apply();
}

function applyGrantPreset(capabilityInputs, capabilities, checked) {
  const selected = new Set(capabilities || []);
  (capabilityInputs || []).forEach(input => {
    if (selected.has(input.value)) input.checked = Boolean(checked);
  });
}

function isHighImpactGrantSelection(selectedCapabilities) {
  const selected = new Set(selectedCapabilities || []);
  return selected.has('*') || selected.has('target_admin');
}

function summarizeGrantAccess(selectedCapabilities, presets) {
  const selected = new Set(selectedCapabilities || []);
  const uncovered = new Set(selected);
  const ordered = (presets || [])
    .map(preset => ({ ...preset, capabilities: Array.from(preset.capabilities || []) }))
    .sort((a, b) => b.capabilities.length - a.capabilities.length || String(a.name).localeCompare(String(b.name)));

  const summary = [];
  ordered.forEach(preset => {
    const contained = preset.capabilities.length > 0 && preset.capabilities.every(capability => selected.has(capability));
    const addsCoverage = preset.capabilities.some(capability => uncovered.has(capability));
    if (!contained || !addsCoverage) return;
    summary.push({ name: preset.name, highImpact: Boolean(preset.highImpact) });
    preset.capabilities.forEach(capability => uncovered.delete(capability));
  });

  Array.from(uncovered).sort().forEach(capability => {
    summary.push({ name: capability, highImpact: capability === '*' || capability === 'target_admin' });
  });
  return summary;
}

function setupGrantBuilder(builder) {
  if (!builder) return;
  const form = builder.closest?.('form') || document.querySelector?.('[data-grant-form]');
  const capabilityInputs = Array.from(builder.querySelectorAll?.('[data-grant-capability]') || []);
  const presetInputs = Array.from(builder.querySelectorAll?.('[data-grant-preset]') || []);
  const summary = builder.querySelector?.('[data-grant-summary]');
  const highImpactRegion = form?.querySelector?.('[data-high-impact-confirm]');
  const highImpactCheckbox = form?.querySelector?.('[data-high-impact-checkbox]');

  const presetModels = presetInputs.map(input => ({
    input,
    name: input.parentElement?.querySelector?.('label')?.textContent?.replace('⚠', '').trim() || input.id || 'Preset',
    highImpact: (input.parentElement?.textContent || '').includes('⚠'),
    capabilities: String(input.dataset?.capabilities || '').split(',').map(value => value.trim()).filter(Boolean),
  }));

  const selectedSet = () => new Set(capabilityInputs.filter(input => input.checked).map(input => input.value));

  const render = () => {
    const selected = selectedSet();
    presetModels.forEach(preset => {
      preset.input.checked = preset.capabilities.length > 0 && preset.capabilities.every(capability => selected.has(capability));
    });

    const highImpact = isHighImpactGrantSelection(selected);
    if (highImpactRegion) highImpactRegion.hidden = !highImpact;
    if (highImpactCheckbox) {
      highImpactCheckbox.required = highImpact;
      if (!highImpact) highImpactCheckbox.checked = false;
    }

    if (!summary || typeof document.createElement !== 'function') return;
    summary.textContent = '';
    const items = summarizeGrantAccess(selected, presetModels);
    if (items.length === 0) {
      const empty = document.createElement('span');
      empty.className = 'text-slate-500';
      empty.textContent = 'Nothing selected.';
      summary.appendChild(empty);
      return;
    }
    items.forEach(item => {
      const chip = document.createElement('span');
      chip.className = item.highImpact
        ? 'rounded border border-amber-800 bg-amber-950/30 px-2 py-1 text-amber-200'
        : 'rounded border border-slate-700 bg-slate-900 px-2 py-1 text-slate-200';
      chip.textContent = item.name + (item.highImpact ? ' ⚠' : '');
      summary.appendChild(chip);
    });
  };

  presetModels.forEach(preset => {
    preset.input.addEventListener('change', () => {
      applyGrantPreset(capabilityInputs, preset.capabilities, preset.input.checked);
      render();
    });
  });
  capabilityInputs.forEach(input => input.addEventListener('change', render));
  render();
}

function setupTooltip(trigger) {
  const tooltipId = trigger?.getAttribute?.('aria-describedby');
  if (!tooltipId) return;
  const tooltip = document.getElementById?.(tooltipId);
  if (!tooltip) return;

  let openTimer = null;
  let closeTimer = null;
  const cancelTimers = () => {
    if (openTimer) clearTimeout(openTimer);
    if (closeTimer) clearTimeout(closeTimer);
    openTimer = null;
    closeTimer = null;
  };
  const open = (delay = 0) => {
    cancelTimers();
    openTimer = setTimeout(() => {
      tooltip.hidden = false;
      trigger.setAttribute?.('aria-expanded', 'true');
    }, delay);
  };
  const close = (delay = 0) => {
    cancelTimers();
    closeTimer = setTimeout(() => {
      tooltip.hidden = true;
      trigger.setAttribute?.('aria-expanded', 'false');
    }, delay);
  };

  trigger.setAttribute?.('aria-expanded', 'false');
  trigger.addEventListener('mouseenter', () => open(600));
  trigger.addEventListener('mouseleave', event => {
    if (tooltip.contains?.(event.relatedTarget)) return;
    close(100);
  });
  trigger.addEventListener('focus', () => open(0));
  trigger.addEventListener('blur', event => {
    if (tooltip.contains?.(event.relatedTarget)) return;
    close(100);
  });
  trigger.addEventListener('click', () => {
    if (tooltip.hidden) open(0);
    else close(0);
  });
  tooltip.addEventListener?.('mouseenter', cancelTimers);
  tooltip.addEventListener?.('mouseleave', event => {
    if (event.relatedTarget === trigger) return;
    close(100);
  });
  tooltip.addEventListener?.('focusin', cancelTimers);
  tooltip.addEventListener?.('focusout', event => {
    if (event.relatedTarget === trigger || tooltip.contains?.(event.relatedTarget)) return;
    close(100);
  });
}

if (typeof globalThis !== 'undefined') {
  globalThis.MCPGrantUI = { applyGrantPreset, isHighImpactGrantSelection, summarizeGrantAccess };
}

document.addEventListener('DOMContentLoaded', () => {
  const navButton = document.querySelector('[data-nav-toggle]');
  const navigation = document.getElementById('primary-navigation');

  const closeNavigation = () => {
    const returnFocus = navigation?.contains(document.activeElement);
    navigation?.classList.add('hidden');
    navButton?.setAttribute('aria-expanded', 'false');
    if (returnFocus) navButton?.focus();
  };

  navButton?.classList.remove('hidden');
  navButton?.classList.add('inline-flex');
  navigation?.classList.add('hidden');

  navButton?.addEventListener('click', () => {
    const opening = navigation.classList.contains('hidden');
    navigation.classList.toggle('hidden');
    navButton.setAttribute('aria-expanded', String(opening));
  });

  document.addEventListener('keydown', e => {
    if (e.key !== 'Escape') return;
    closeNavigation();
    document.querySelectorAll?.('[data-tooltip-trigger]').forEach(trigger => {
      const tooltipId = trigger.getAttribute?.('aria-describedby');
      const tooltip = tooltipId ? document.getElementById?.(tooltipId) : null;
      if (tooltip) tooltip.hidden = true;
      trigger.setAttribute?.('aria-expanded', 'false');
    });
  });

  document.querySelectorAll('[data-confirm]').forEach(el => {
    el.addEventListener('click', e => {
      const msg = el.getAttribute('data-confirm') || 'Are you sure?';
      if (!confirm(msg)) e.preventDefault();
    });
  });

  document.querySelectorAll('[data-project-select]').forEach(setupProjectScope);
  document.querySelectorAll('table[data-admin-table]').forEach(setupDataTable);
  document.querySelectorAll('[data-grant-builder]').forEach(setupGrantBuilder);
  document.querySelectorAll('[data-tooltip-trigger]').forEach(setupTooltip);
});
