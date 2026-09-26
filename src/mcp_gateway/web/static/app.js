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

function setupGrantForm() {
  const form = document.getElementById('grant-form');
  if (!form || typeof form.querySelector !== 'function') return;

  const wildcard = form.querySelector('input[name="capabilities"][value="*"]');
  if (!wildcard) return;
  const ordinary = Array.from(form.querySelectorAll('input[name="capabilities"]'))
    .filter(input => input !== wildcard);
  const targetShell = form.querySelector('input[name="target_shell"]');

  const sync = () => {
    const coveredByWildcard = wildcard.checked;
    [...ordinary, targetShell].filter(Boolean).forEach(input => {
      if (coveredByWildcard) input.checked = false;
      input.disabled = coveredByWildcard;
    });
  };

  wildcard.addEventListener('change', sync);
  sync();
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
    if (e.key === 'Escape') closeNavigation();
  });

  document.querySelectorAll('[data-confirm]').forEach(el => {
    el.addEventListener('click', e => {
      const msg = el.getAttribute('data-confirm') || 'Are you sure?';
      if (!confirm(msg)) e.preventDefault();
    });
  });

  document.querySelectorAll('table[data-admin-table]').forEach(setupDataTable);
  setupGrantForm();
});
