// Panel de clon: lee el estado del API local y envía las acciones con el token de la sesión.
const token = document.querySelector('meta[name="clon-token"]').content;
let state = null;
// Lo que el usuario está haciendo y debe sobrevivir a cada refresco del estado.
const ui = { tab: 'resumen', menu: null, filter: 'todos', dialog: false, catalog: {}, catalogError: {}, pending: {}, choice: {}, dismissed: {} };

const DB_KINDS = ['mysql', 'mariadb'];
const LOGO = { apache: 'A', nginx: 'N', frankenphp: 'F', mysql: 'My', mariadb: 'Ma', postgres: 'Pg', mongodb: 'Mo', redis: 'R', mailpit: '✉', meilisearch: 'Me' };
const SOON = {
  web: [
    { kind: 'nginx', name: 'Nginx', desc: 'Servidor web: cada sitio podrá elegir Apache o Nginx.' },
    { kind: 'frankenphp', name: 'FrankenPHP', desc: 'Servidor con su propio PHP; más adelante, modo worker para Laravel Octane.' },
  ],
  db: [
    { kind: 'postgres', name: 'PostgreSQL', desc: 'Base de datos · puerto 5432' },
    { kind: 'mongodb', name: 'MongoDB', desc: 'Base de datos · puerto 27017' },
  ],
  other: [
    { kind: 'redis', name: 'Redis', desc: 'Caché y colas · puerto 6379' },
    { kind: 'mailpit', name: 'Mailpit', desc: 'Correo de prueba: lo que envía Laravel, visto en el navegador.' },
    { kind: 'meilisearch', name: 'Meilisearch', desc: 'Búsqueda para Laravel Scout · puerto 7700' },
  ],
};

async function call(method, path, body) {
  const res = await fetch(path, {
    method,
    headers: { 'X-Clon-Token': token, 'Content-Type': 'application/json' },
    body: body ? JSON.stringify(body) : undefined,
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || res.statusText);
  return data;
}

// el crea un elemento; los atributos false o null se omiten y true queda como atributo vacío.
function el(tag, attrs = {}, ...children) {
  const n = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v === false || v === null || v === undefined) continue;
    if (k === 'class') n.className = v;
    else if (k.startsWith('on')) n.addEventListener(k.slice(2), v);
    else n.setAttribute(k, v === true ? '' : v);
  }
  for (const c of children.flat()) if (c !== null && c !== undefined && c !== false) n.append(c);
  return n;
}

const mb = n => (n / 1e6).toFixed(n >= 1e8 ? 0 : 1);
const firstLine = s => String(s || '').trim().split(/\r?\n/)[0];
const shortPath = p => { const parts = String(p).split('\\'); return parts.length > 3 ? '…\\' + parts.slice(-2).join('\\') : p; };
const logo = kind => el('div', { class: 'logo lg-' + kind }, LOGO[kind] || kind[0].toUpperCase());

function toast(text) {
  const t = document.getElementById('toast');
  t.textContent = text;
  t.hidden = false;
  clearTimeout(toast.timer);
  toast.timer = setTimeout(() => { t.hidden = true; }, 2800);
}

function alertBox(text, buttonText, action, bad) {
  const box = el('div', { class: 'alert' + (bad ? ' bad' : '') }, el('span', {}, text));
  if (buttonText) box.append(el('button', { onclick: action }, buttonText));
  return box;
}

function statePill(st, busy) {
  if (busy) return el('span', { class: 'pill' }, 'un momento…');
  const cls = st === 'en marcha' ? 'pill ok' : st === 'error' ? 'pill bad' : 'pill';
  return el('span', { class: cls }, st === 'en marcha' ? el('span', { class: 'dot' }) : null, st);
}

// ---------- pestañas ----------

function showTab(tab) {
  ui.tab = tab;
  document.querySelectorAll('#tabs a').forEach(a => a.classList.toggle('active', a.dataset.tab === tab));
  document.querySelectorAll('[data-panel]').forEach(p => { p.hidden = p.dataset.panel !== tab; });
  history.replaceState(null, '', '#' + tab);
  if (tab === 'servicios') loadCatalogs();
}

// ---------- estado ----------

function render() {
  const s = state;
  renderAlerts(s);
  document.querySelectorAll('.php-select').forEach(sel => {
    if (document.activeElement === sel) return;
    sel.replaceChildren(...(s.php_versions || []).map(v => el('option', { value: v, selected: v === s.php }, 'PHP ' + v)));
  });
  document.querySelectorAll('.node-version').forEach(n => { n.textContent = s.node ? 'v' + s.node : 'no instalado'; });
  document.getElementById('processes').replaceChildren(...(s.services || []).map(sv =>
    el('li', { title: sv.last_error || '' }, el('span', {}, sv.name + (sv.port ? ` :${sv.port}` : '')), statePill(sv.state))));
  renderSites();
  // No se redibuja mientras escribes un puerto o eliges una versión.
  const view = document.getElementById('services-view');
  const editing = view.contains(document.activeElement) && /^(INPUT|SELECT)$/.test(document.activeElement.tagName);
  if (!editing) renderServices();
  if (ui.dialog) renderDialog();
  document.getElementById('count-sites').textContent = (s.sites || []).length || '';
  const instances = (s.databases || []).reduce((n, g) => n + g.instances.length, 0);
  document.getElementById('count-services').textContent = instances || '';
  const db = s.db ? `${s.db} (puerto ${s.db_port})` : 'sin base de datos';
  document.getElementById('footer').textContent =
    `Instalación: ${s.home} · Apache ${s.apache || '—'} · ${db} · proxy ${s.http_port}/${s.https_port}`;
}

function renderAlerts(s) {
  const alerts = document.getElementById('alerts');
  alerts.replaceChildren();
  if (!s.ca_trusted) {
    alerts.append(alertBox('Windows todavía no confía en la CA local de clon: el navegador avisará en los sitios HTTPS.',
      'Confiar en la CA', () => act('POST', '/api/ca/trust')));
  }
  if (s.hosts_missing && s.hosts_missing.length) {
    alerts.append(alertBox(`${s.hosts_missing.length} sitio(s) no están en el archivo hosts (por ejemplo ${s.hosts_missing[0]}).`,
      'Actualizar hosts (pide permiso)', () => act('POST', '/api/hosts/sync')));
  }
  for (const [what, msg] of Object.entries(s.problems || {})) {
    alerts.append(alertBox(`${what}: ${firstLine(msg)}`, null, null, true));
  }
}

function renderSites() {
  if (!state) return;
  const q = document.getElementById('filter').value.toLowerCase();
  const list = (state.sites || []).filter(x => !q || x.host.includes(q) || x.folder.toLowerCase().includes(q));
  document.getElementById('sites').replaceChildren(...list.map(x => el('tr', {},
    el('td', {}, el('a', { href: x.url, target: '_blank' }, x.host)),
    el('td', {}, x.kind),
    el('td', { class: 'folder' }, x.doc_root),
    el('td', {}, String(x.port)),
  )));
}

// ---------- servicios ----------

function renderServices() {
  if (!state) return;
  const s = state;
  const dbs = s.databases || [];
  document.getElementById('services-view').replaceChildren(
    el('div', { class: 'toolbar' },
      el('h2', {}, 'Servicios'),
      chips(),
      el('span', { class: 'spacer' }),
      el('button', { onclick: openDialog }, '＋ Añadir servicio')),
    phpBar(s),
    el('div', { class: 'group-title' }, 'Servidores web'),
    el('div', { class: 'grid3' }, apacheCard(s), ...SOON.web.map(soonCard)),
    el('div', { class: 'group-title' }, 'Bases de datos'),
    ...dbs.map(dbCard),
    el('div', { class: 'grid3' }, ...SOON.db.map(soonCard)),
    el('div', { class: 'group-title' }, 'Caché, búsqueda y correo'),
    el('div', { class: 'grid3' }, ...SOON.other.map(soonCard)),
  );
}

function chips() {
  const opts = [['todos', 'Todos'], ['marcha', 'En marcha'], ['detenidos', 'Detenidos']];
  return el('div', { class: 'chips' }, ...opts.map(([k, label]) =>
    el('span', { class: 'chip' + (ui.filter === k ? ' on' : ''), onclick: () => { ui.filter = k; renderServices(); } }, label)));
}

const passes = i => ui.filter === 'todos' || (ui.filter === 'marcha' ? i.state === 'en marcha' : i.state !== 'en marcha');

function phpBar(s) {
  const pool = (s.services || []).filter(x => x.name.startsWith('php-cgi-'));
  const up = pool.filter(x => x.state === 'en marcha').length;
  const label = !pool.length ? 'detenido' : up === pool.length ? 'en marcha' : `${up} de ${pool.length} en marcha`;
  const range = pool.length ? ` (puertos ${pool[0].port}–${pool[pool.length - 1].port})` : '';
  return el('div', { class: 'phpbar' },
    statePill(label),
    el('span', {}, el('b', {}, 'PHP global ' + (s.php || '—')), ` · grupo de ${pool.length} procesos `, el('span', { class: 'mono' }, 'php-cgi'), range),
    el('span', { class: 'spacer' }),
    el('a', { href: '#php', onclick: e => { e.preventDefault(); showTab('php'); } }, 'Cambiar versión en «PHP y Node» →'));
}

function apacheCard(s) {
  const st = (s.services || []).find(x => x.name === 'apache');
  return el('div', { class: 'svc' },
    el('div', { class: 'svc-head' }, logo('apache'),
      el('div', {}, el('div', { class: 'svc-title' }, 'Apache'), el('div', { class: 'svc-sub' }, s.apache ? 'versión ' + s.apache : 'no instalado')),
      el('span', { class: 'spacer' }), statePill(st ? st.state : 'detenido')),
    el('div', { class: 'svc-body' }, el('div', { class: 'svc-sub' },
      `Atiende ${(s.sites || []).length} sitios, cada uno en su puerto interno, con PHP por FastCGI.`)),
    el('div', { class: 'svc-foot' }, 'Servidor de todos los sitios'));
}

function soonCard(x) {
  return el('div', { class: 'svc dashed soon' },
    el('div', { class: 'svc-head' }, logo(x.kind),
      el('div', {}, el('div', { class: 'svc-title' }, x.name), el('div', { class: 'svc-sub' }, 'próximamente'))),
    el('div', { class: 'svc-body' }, el('div', { class: 'svc-sub' }, x.desc)),
    el('div', { class: 'svc-foot' }, 'Llega en la fase 2'));
}

function dbCard(g) {
  const jobs = (state.jobs || []).filter(j => j.kind === g.kind && !ui.dismissed[j.id] && (!j.finished || j.error));
  const installed = g.instances.length > 0;
  const running = g.instances.filter(i => i.state === 'en marcha').length;
  const card = el('div', { class: 'svc' + (installed || jobs.length ? '' : ' dashed') },
    el('div', { class: 'svc-head' }, logo(g.kind),
      el('div', {}, el('div', { class: 'svc-title' }, g.name),
        el('div', { class: 'svc-sub' }, installed
          ? `${g.instances.length === 1 ? '1 versión instalada' : g.instances.length + ' versiones instaladas'} · ${running} en marcha`
          : 'sin instalar'))));
  if (installed) {
    card.append(el('table', { class: 'rows' },
      el('thead', {}, el('tr', {},
        el('th', { style: 'width:100px' }, 'Por defecto'), el('th', {}, 'Versión'), el('th', { style: 'width:110px' }, 'Puerto'),
        el('th', { style: 'width:170px' }, 'Con «Iniciar todo»'), el('th', { style: 'width:120px' }, 'Estado'),
        el('th', { style: 'width:60px' }), el('th', { style: 'width:50px' }))),
      el('tbody', {}, ...g.instances.filter(passes).map(i => instanceRow(g, i)))));
  }
  for (const j of jobs) card.append(jobLine(j, g.name));
  const cat = ui.catalog[g.kind];
  card.append(el('div', { class: 'svc-foot' },
    installed ? 'Instalar otra versión:' : 'Instalar:', ...installPicker(g),
    el('span', { class: 'spacer' }),
    cat && cat.length ? `Descarga de ${cat[0].source}` : ''));
  return card;
}

function instanceRow(g, i) {
  const busy = ui.pending[i.key];
  const on = i.state === 'en marcha' || i.state === 'iniciando';
  const port = el('input', { class: 'port', type: 'number', min: 1024, max: 65535, value: i.port, title: 'Puerto' });
  port.addEventListener('change', () => settings(i.key, { port: Number(port.value) }));
  port.addEventListener('keydown', e => {
    if (e.key === 'Enter') port.blur();
    if (e.key === 'Escape') { port.value = i.port; port.blur(); }
  });
  const auto = el('input', { type: 'checkbox', checked: i.autostart, disabled: i.default });
  auto.addEventListener('change', () => settings(i.key, { autostart: auto.checked }));
  const notes = [];
  if (i.source === 'laragon') notes.push('de Laragon');
  else if (i.source === 'download') notes.push('descargada');
  if (i.external) notes.push('usa los datos de ' + shortPath(i.data_dir));
  return el('tr', {},
    el('td', {}, el('input', { type: 'radio', name: 'def-' + g.kind, checked: i.default, title: 'Predeterminada: puerto estándar y atajo mysql',
      onchange: () => act('POST', '/api/services/default', { key: i.key }, i.key) })),
    el('td', {}, el('span', { class: 'ver' }, i.version || i.series), ' ', el('span', { class: 'svc-sub' }, notes.join(' · ')),
      i.error && i.state !== 'en marcha' ? el('div', { class: 'row-error' }, firstLine(i.error)) : null),
    el('td', {}, port),
    el('td', {}, el('label', { class: 'check', title: i.default ? 'La predeterminada siempre arranca' : '' }, auto,
      el('span', { class: 'svc-sub' }, i.default ? 'siempre' : i.autostart ? 'sí' : 'solo a mano'))),
    el('td', {}, statePill(i.state, busy)),
    el('td', {}, el('button', { class: 'switch' + (on ? ' on' : ''), title: on ? 'Detener' : 'Iniciar', disabled: !!busy,
      onclick: () => act('POST', on ? '/api/services/stop' : '/api/services/start', { key: i.key }, i.key) })),
    el('td', { class: 'right' },
      el('button', { class: 'ghost', title: 'Más acciones', onclick: e => { e.stopPropagation(); ui.menu = ui.menu === i.key ? null : i.key; renderServices(); } }, '⋯'),
      ui.menu === i.key ? instanceMenu(g, i) : null));
}

function instanceMenu(g, i) {
  const item = (text, fn, cls) => el('div', { class: 'item' + (cls ? ' ' + cls : ''), onclick: e => { e.stopPropagation(); ui.menu = null; renderServices(); fn(); } }, text);
  return el('div', { class: 'menu' },
    item('Ver log', () => act('POST', '/api/services/open', { key: i.key, what: 'log' })),
    item('Abrir carpeta de datos', () => act('POST', '/api/services/open', { key: i.key, what: 'data' })),
    item('Copiar datos de conexión (.env)', () => copyEnv(g, i)),
    el('div', { class: 'sep' }),
    item(i.source === 'download' ? 'Desinstalar (conserva los datos)' : 'Quitar de clon (sus archivos no se tocan)', () => uninstall(g, i), 'red'));
}

function installPicker(g) {
  const cat = ui.catalog[g.kind];
  if (!cat) {
    return [el('span', { class: 'svc-sub' }, ui.catalogError[g.kind] ? 'No se pudo leer el catálogo: ' + ui.catalogError[g.kind] : 'Consultando las versiones disponibles…')];
  }
  const have = new Set(g.instances.map(i => i.series));
  const busy = new Set((state.jobs || []).filter(j => j.kind === g.kind && !j.finished).map(j => j.series));
  const options = cat.filter(r => !have.has(r.series) && !busy.has(r.series));
  if (!options.length) return [el('span', { class: 'svc-sub' }, 'Ya tienes todas las versiones del catálogo')];
  const sel = el('select', { 'aria-label': 'Versión' }, ...options.map(r =>
    el('option', { value: r.series }, r.version + (r.note ? ' · ' + r.note : '') + (r.size > 0 ? ` · ${mb(r.size)} MB` : ''))));
  if (options.some(r => r.series === ui.choice[g.kind])) sel.value = ui.choice[g.kind];
  sel.addEventListener('change', () => { ui.choice[g.kind] = sel.value; });
  return [sel, el('button', { class: 'small' + (g.instances.length ? ' secondary' : ''), onclick: () => installSeries(g.kind, sel.value) },
    g.instances.length ? 'Instalar otra' : 'Instalar')];
}

function jobLine(j, name) {
  if (j.finished && j.error) {
    return el('div', { class: 'job error' }, el('span', {}, `No se pudo instalar ${name} ${j.version}: ${j.error}`), el('span', { class: 'spacer' }),
      el('button', { class: 'small secondary', onclick: () => { ui.dismissed[j.id] = true; renderServices(); if (ui.dialog) renderDialog(); } }, 'Cerrar'));
  }
  const known = j.phase === 'descargando' && j.total > 0;
  const pct = known ? Math.floor(j.done * 100 / j.total) : 0;
  return el('div', { class: 'job' },
    el('span', {}, `${j.version}: ${j.phase}`),
    el('div', { class: 'progress' + (known ? '' : ' indeterminate') }, el('span', known ? { style: `width:${pct}%` } : {})),
    el('span', { class: 'svc-sub' }, known ? `${mb(j.done)} de ${mb(j.total)} MB` : ''),
    j.phase === 'descargando' ? el('button', { class: 'small danger', onclick: () => act('POST', '/api/jobs/cancel', { id: j.id }) }, 'Cancelar') : null);
}

// ---------- acciones ----------

// act envía una acción. Con key solo se bloquea esa instancia; sin key, todos los botones.
async function act(method, path, body, key) {
  if (key) { ui.pending[key] = true; renderServices(); } else document.querySelectorAll('button').forEach(b => { b.disabled = true; });
  try {
    const out = await call(method, path, body);
    if (out && out.sites) state = out;
    return out;
  } catch (e) {
    alert(e.message);
  } finally {
    if (key) delete ui.pending[key]; else document.querySelectorAll('button').forEach(b => { b.disabled = false; });
    await refresh();
  }
}

const settings = (key, patch) => act('POST', '/api/services/settings', { key, ...patch }, key);

function uninstall(g, i) {
  const what = `${g.name} ${i.version}`;
  const msg = i.source === 'download'
    ? `¿Desinstalar ${what}? Se borran sus programas; los datos (${i.data_dir}) se conservan.`
    : `¿Quitar ${what} de clon? Sus archivos no se tocan y los datos se conservan.`;
  if (confirm(msg)) act('POST', '/api/services/uninstall', { key: i.key }, i.key);
}

async function copyEnv(g, i) {
  const text = [`DB_CONNECTION=${g.kind === 'mariadb' ? 'mariadb' : 'mysql'}`, 'DB_HOST=127.0.0.1', `DB_PORT=${i.port}`,
    'DB_DATABASE=', 'DB_USERNAME=root', 'DB_PASSWORD='].join('\n');
  try {
    await navigator.clipboard.writeText(text);
    toast('Datos de conexión copiados: pégalos en el .env de tu proyecto');
  } catch {
    alert(text);
  }
}

async function installSeries(kind, series) {
  if (!series) return;
  try {
    await call('POST', '/api/services/install', { kind, version: series });
    toast('Instalación en marcha');
  } catch (e) {
    alert(e.message);
  }
  await refresh();
}

async function loadCatalogs(refreshList) {
  for (const kind of DB_KINDS) {
    if (ui.catalog[kind] && !refreshList) continue;
    try {
      ui.catalog[kind] = await call('GET', `/api/catalog?kind=${kind}${refreshList ? '&refresh=1' : ''}`);
      delete ui.catalogError[kind];
    } catch (e) {
      ui.catalogError[kind] = e.message;
    }
    renderServices();
    if (ui.dialog) renderDialog();
  }
}

// ---------- «Añadir servicio» ----------

function openDialog() {
  ui.dialog = true;
  document.getElementById('dialog').hidden = false;
  renderDialog();
  loadCatalogs();
}

function closeDialog() {
  ui.dialog = false;
  document.getElementById('dialog').hidden = true;
}

function renderDialog() {
  if (!state) return;
  const tiles = (state.databases || []).map(g => {
    const job = (state.jobs || []).find(j => j.kind === g.kind && !j.finished);
    const cat = ui.catalog[g.kind];
    const n = g.instances.length;
    return el('div', { class: 'tile' + (job ? ' busy' : '') },
      el('div', { class: 'top' }, logo(g.kind),
        el('div', {}, el('div', { class: 'name' }, g.name, ' ', n ? el('span', { class: 'pill' }, n === 1 ? '1 instalada' : `${n} instaladas`) : null),
          el('div', { class: 'desc' }, 'Base de datos · compatible con MySQL'))),
      job ? jobLine(job, g.name) : el('div', { class: 'act' }, ...installPicker(g)),
      el('div', { class: 'src' }, cat && cat.length ? `Descarga de ${cat[0].source}` : ''));
  });
  const soon = [...SOON.web, ...SOON.db, ...SOON.other].map(x => el('div', { class: 'tile soon' },
    el('div', { class: 'top' }, logo(x.kind), el('div', {}, el('div', { class: 'name' }, x.name, ' ', el('span', { class: 'pill' }, 'próximamente')),
      el('div', { class: 'desc' }, x.desc)))));
  document.getElementById('dialog-body').replaceChildren(...tiles, ...soon);
}

// ---------- arranque ----------

document.querySelectorAll('[data-action]').forEach(b => b.addEventListener('click', () => act('POST', '/api/' + b.dataset.action)));
document.querySelectorAll('.php-select').forEach(sel => sel.addEventListener('change', e => act('POST', '/api/php', { version: e.target.value })));
document.getElementById('filter').addEventListener('input', renderSites);
document.querySelectorAll('#tabs a').forEach(a => a.addEventListener('click', e => { e.preventDefault(); showTab(a.dataset.tab); }));
document.getElementById('dialog-close').addEventListener('click', closeDialog);
document.getElementById('dialog').addEventListener('click', e => { if (e.target.id === 'dialog') closeDialog(); });
document.addEventListener('keydown', e => {
  if (e.key !== 'Escape') return;
  if (ui.dialog) closeDialog();
  if (ui.menu) { ui.menu = null; renderServices(); }
});
document.addEventListener('click', () => { if (ui.menu) { ui.menu = null; renderServices(); } });

async function refresh() {
  try {
    state = await call('GET', '/api/status');
    render();
  } catch (e) {
    document.getElementById('alerts').replaceChildren(alertBox('No se pudo leer el estado: ' + e.message, null, null, true));
  }
}

// Cada 5 s, o cada segundo mientras se instala algo.
function schedule() {
  const busy = state && (state.jobs || []).some(j => !j.finished);
  setTimeout(async () => { await refresh(); schedule(); }, busy ? 1000 : 5000);
}

const firstTab = location.hash.slice(1);
showTab(['resumen', 'sitios', 'servicios', 'php'].includes(firstTab) ? firstTab : 'resumen');
refresh().then(schedule);
