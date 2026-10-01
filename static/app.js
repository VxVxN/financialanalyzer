// Financial Analyzer — behaviour shared by every page: theme switching, the
// company search palette ("/" or Ctrl+K) and the comparison tray. Pages talk
// to it through window.FA.
(function () {
    'use strict';

    const root = document.documentElement;

    function storageGet(key) {
        try { return localStorage.getItem(key); } catch (e) { return null; }
    }
    function storageSet(key, value) {
        try { localStorage.setItem(key, value); } catch (e) { /* private mode: keep in memory only */ }
    }

    // ---------------------------------------------------------------- theme
    function theme() {
        return root.getAttribute('data-theme') === 'dark' ? 'dark' : 'light';
    }
    function setTheme(t) {
        root.setAttribute('data-theme', t);
        storageSet('theme', t);
        window.dispatchEvent(new CustomEvent('fa:themechange', { detail: t }));
    }
    document.querySelectorAll('[data-theme-toggle]').forEach(function (btn) {
        btn.addEventListener('click', function () { setTheme(theme() === 'dark' ? 'light' : 'dark'); });
    });

    // ---------------------------------------------------------------- compare tray
    // Eight = the validated categorical palette of the charts.
    const MAX_COMPARE = 8;
    // sanitize turns stored or requested input into a list of distinct names,
    // at most MAX_COMPARE long.
    function sanitize(list) {
        if (!Array.isArray(list)) return [];
        const out = [];
        list.forEach(function (c) {
            if (typeof c === 'string' && c && out.indexOf(c) < 0) out.push(c);
        });
        return out.slice(0, MAX_COMPARE);
    }
    function parseStored(raw) {
        try { return sanitize(JSON.parse(raw || '[]')); } catch (e) { return []; }
    }
    let compareSet = parseStored(storageGet('compare'));

    const tray = document.getElementById('compareTray');
    const trayList = document.getElementById('compareTrayList');
    const trayGo = document.getElementById('compareTrayGo');
    const compareListeners = [];

    function compareURL(list) {
        return '/compare?companies=' + list.map(encodeURIComponent).join(',');
    }
    function renderTray() {
        if (!tray) return;
        // The comparison page shows the selection itself.
        const onCompare = location.pathname === '/compare';
        tray.hidden = compareSet.length === 0 || onCompare;
        document.body.classList.toggle('has-tray', !tray.hidden);
        trayList.replaceChildren();
        compareSet.forEach(function (c) {
            const li = document.createElement('li');
            li.append(document.createTextNode(c));
            const rm = document.createElement('button');
            rm.type = 'button';
            rm.setAttribute('aria-label', 'Убрать ' + c + ' из сравнения');
            rm.textContent = '×';
            rm.addEventListener('click', function () { compare.remove(c); });
            li.append(rm);
            trayList.append(li);
        });
        trayGo.href = compareURL(compareSet);
    }
    function compareChanged() {
        storageSet('compare', JSON.stringify(compareSet));
        renderTray();
        compareListeners.forEach(function (fn) { fn(compareSet.slice()); });
    }
    const compare = {
        list: function () { return compareSet.slice(); },
        has: function (c) { return compareSet.indexOf(c) >= 0; },
        add: function (c) {
            if (compare.has(c) || compareSet.length >= MAX_COMPARE) return false;
            compareSet.push(c);
            compareChanged();
            return true;
        },
        remove: function (c) {
            compareSet = compareSet.filter(function (x) { return x !== c; });
            compareChanged();
        },
        toggle: function (c) { if (compare.has(c)) compare.remove(c); else compare.add(c); return compare.has(c); },
        // set replaces the selection; false when it had to be cut to the limit.
        set: function (list) {
            const clean = sanitize(list);
            const distinct = Array.isArray(list) ? list.filter(function (c, i) { return list.indexOf(c) === i; }).length : 0;
            compareSet = clean;
            compareChanged();
            return distinct <= MAX_COMPARE;
        },
        clear: function () { compareSet = []; compareChanged(); },
        url: compareURL,
        max: MAX_COMPARE,
        onChange: function (fn) { compareListeners.push(fn); },
    };
    document.querySelectorAll('[data-compare-clear]').forEach(function (btn) {
        btn.addEventListener('click', compare.clear);
    });
    // Another tab changed the selection.
    window.addEventListener('storage', function (e) {
        if (e.key !== 'compare') return;
        compareSet = parseStored(e.newValue);
        renderTray();
        compareListeners.forEach(function (fn) { fn(compareSet.slice()); });
    });
    renderTray();

    // ---------------------------------------------------------------- search palette
    const palette = document.getElementById('palette');
    const input = document.getElementById('paletteInput');
    const list = document.getElementById('paletteList');
    let companies = null; // [{company, category}], loaded on first open
    let matches = [];
    let active = 0;

    function loadCompanies() {
        if (companies) return Promise.resolve(companies);
        return fetch('/api/companies-with-categories')
            .then(function (r) { if (!r.ok) throw new Error(r.status); return r.json(); })
            .then(function (data) { companies = Array.isArray(data) ? data : []; return companies; });
    }
    function renderMatches() {
        const q = input.value.trim().toLowerCase();
        matches = (companies || []).filter(function (c) {
            return !q || c.company.toLowerCase().indexOf(q) >= 0 || (c.category || '').toLowerCase().indexOf(q) >= 0;
        });
        // Ticker prefix matches first.
        matches.sort(function (a, b) {
            const pa = a.company.toLowerCase().indexOf(q) === 0 ? 0 : 1;
            const pb = b.company.toLowerCase().indexOf(q) === 0 ? 0 : 1;
            return pa - pb || a.company.localeCompare(b.company);
        });
        matches = matches.slice(0, 30);
        active = Math.min(active, Math.max(matches.length - 1, 0));
        list.replaceChildren();
        if (!matches.length) {
            const li = document.createElement('li');
            li.className = 'empty-row';
            li.textContent = companies ? 'Ничего не найдено' : 'Загрузка…';
            list.append(li);
            input.removeAttribute('aria-activedescendant');
            return;
        }
        matches.forEach(function (c, i) {
            const li = document.createElement('li');
            li.id = 'palette-opt-' + i;
            li.setAttribute('role', 'option');
            li.setAttribute('aria-selected', i === active ? 'true' : 'false');
            const t = document.createElement('span');
            t.className = 'ticker';
            t.textContent = c.company;
            const cat = document.createElement('span');
            cat.className = 'muted small';
            cat.textContent = c.category || '';
            li.append(t, cat);
            li.addEventListener('mousemove', function () { if (active !== i) { active = i; markActive(); } });
            li.addEventListener('click', function () { openCompany(c.company); });
            list.append(li);
        });
        markActive();
    }
    function markActive() {
        Array.prototype.forEach.call(list.children, function (li, i) {
            if (li.getAttribute('role') === 'option') li.setAttribute('aria-selected', i === active ? 'true' : 'false');
        });
        const el = document.getElementById('palette-opt-' + active);
        if (el) {
            input.setAttribute('aria-activedescendant', el.id);
            el.scrollIntoView({ block: 'nearest' });
        }
    }
    function openCompany(name) {
        location.href = '/company/' + encodeURIComponent(name);
    }
    function openPalette() {
        if (!palette || palette.open) return;
        input.value = '';
        active = 0;
        palette.showModal();
        renderMatches();
        loadCompanies().then(renderMatches, function () {
            list.replaceChildren();
            const li = document.createElement('li');
            li.className = 'empty-row';
            li.textContent = 'Не удалось загрузить список компаний';
            list.append(li);
        });
    }
    if (palette) {
        document.querySelectorAll('[data-palette-open]').forEach(function (b) { b.addEventListener('click', openPalette); });
        input.addEventListener('input', function () { active = 0; renderMatches(); });
        input.addEventListener('keydown', function (e) {
            if (e.key === 'ArrowDown') { e.preventDefault(); active = Math.min(active + 1, matches.length - 1); markActive(); }
            else if (e.key === 'ArrowUp') { e.preventDefault(); active = Math.max(active - 1, 0); markActive(); }
            else if (e.key === 'Enter') { e.preventDefault(); if (matches[active]) openCompany(matches[active].company); }
        });
        palette.addEventListener('click', function (e) { if (e.target === palette) palette.close(); });
        document.addEventListener('keydown', function (e) {
            const t = e.target;
            const typing = t && (t.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName));
            if ((e.key === '/' && !typing && !e.ctrlKey && !e.metaKey && !e.altKey) ||
                ((e.ctrlKey || e.metaKey) && (e.key === 'k' || e.key === 'K'))) {
                e.preventDefault();
                openPalette();
            }
        });
    }

    // ---------------------------------------------------------------- formatting
    const nf = function (digits) {
        return new Intl.NumberFormat('ru-RU', { minimumFractionDigits: digits, maximumFractionDigits: digits });
    };
    const fmt = {
        // Money stored in billions of RUB.
        money: function (v) {
            if (v === null || v === undefined || Number.isNaN(v)) return '—';
            if (Math.abs(v) >= 1000) return nf(2).format(v / 1000) + ' трлн';
            return nf(Math.abs(v) >= 100 ? 0 : 1).format(v) + ' млрд';
        },
        bln: function (v) {
            if (v === null || v === undefined) return '—';
            return nf(Math.abs(v) >= 100 ? 0 : 1).format(v);
        },
        ratio: function (v) { return v === null || v === undefined ? '—' : nf(2).format(v); },
        pct: function (v, signed) {
            if (v === null || v === undefined) return '—';
            return (signed && v > 0 ? '+' : '') + nf(1).format(v).replace('-', '−') + '%';
        },
        date: function (iso) {
            if (!iso) return '';
            const p = iso.split('-');
            return p.length === 3 ? p[2] + '.' + p[1] + '.' + p[0] : iso;
        },
    };

    function esc(s) {
        return String(s).replace(/[&<>"']/g, function (c) {
            return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
        });
    }

    window.FA = {
        theme: theme,
        setTheme: setTheme,
        onThemeChange: function (fn) { window.addEventListener('fa:themechange', function (e) { fn(e.detail); }); },
        compare: compare,
        fmt: fmt,
        esc: esc,
        storage: { get: storageGet, set: storageSet },
    };
})();
