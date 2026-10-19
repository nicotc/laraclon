// Panel de Nido: lee el estado del API local y envía las acciones con el token de la sesión.
const token = document.querySelector('meta[name="nido-token"]').content;
let state = null;
// Lo que el usuario está haciendo y debe sobrevivir a cada refresco del estado.
const ui = { tab: 'resumen', menu: null, filter: 'todos', dialog: false, catalog: {}, catalogError: {}, pending: {}, choice: {}, dismissed: {}, jobOpen: {},
  ext: null, extVersion: '', extBusy: {}, node: null };

const INSTALLABLE = ['mysql', 'mariadb', 'postgresql', 'mongodb', 'redis', 'mailpit', 'meilisearch', 'apache', 'nginx', 'frankenphp', 'adminer'];
// Databases Adminer opens (its MongoDB support needs a PHP driver Nido does not set up for it).
const ADMINER_KINDS = ['mysql', 'mariadb', 'postgresql'];
// Services by section of the Servicios tab, in display order.
const DB_KINDS = ['mysql', 'mariadb', 'postgresql', 'mongodb'];
const OTHER_KINDS = ['redis', 'mailpit', 'meilisearch'];
const KIND_INFO = {
  mysql: { name: 'MySQL', desc: 'Base de datos · compatible con MySQL' },
  mariadb: { name: 'MariaDB', desc: 'Base de datos · compatible con MySQL' },
  postgresql: { name: 'PostgreSQL', desc: 'Base de datos · puerto 5432' },
  mongodb: { name: 'MongoDB', desc: 'Base de datos de documentos · puerto 27017, con mongosh' },
  redis: { name: 'Redis', desc: 'Caché y colas · puerto 6379' },
  mailpit: { name: 'Mailpit', desc: 'Correo de prueba: lo que envía Laravel, visto en el navegador' },
  meilisearch: { name: 'Meilisearch', desc: 'Búsqueda para Laravel Scout · puerto 7700' },
};
// Web servers a site can choose besides Apache; they only run while some site uses them.
const SERVERS = {
  nginx: { name: 'Nginx', version: s => s.nginx, php: () => 'con el mismo PHP global',
    desc: 'Servidor web: cada sitio puede elegir Apache o Nginx.' },
  frankenphp: { name: 'FrankenPHP', version: s => s.frankenphp, php: s => `con su propio PHP ${s.frankenphp_php || ''}`.trim(),
    desc: 'Servidor con su propio PHP integrado; más adelante, modo worker para Laravel Octane.' },
};
const LOGO = { apache: 'A', nginx: 'N', frankenphp: 'F', mysql: 'My', mariadb: 'Ma', postgresql: 'Pg', mongodb: 'Mo', redis: 'R', mailpit: '✉', meilisearch: 'Me', adminer: 'Ad',
  node: 'JS', pm: 'pm' };
// Jobs that change what the Node section shows.
const NODE_KINDS = ['node', 'pnpm', 'yarn'];

// A kind is shown as a full card when it can be installed or something of it is installed.
function isAvailable(g) {
  return INSTALLABLE.includes(g.kind) || g.instances.length > 0 || (state.jobs || []).some(j => j.kind === g.kind);
}

function groupOf(kind) {
  return (state.databases || []).find(g => g.kind === kind) || { kind, name: KIND_INFO[kind].name, instances: [] };
}

async function call(method, path, body) {
  const res = await fetch(path, {
    method,
    headers: { 'X-Nido-Token': token, 'Content-Type': 'application/json' },
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
  if (tab === 'php') { loadExtensions(); loadNode(); }
  if (tab === 'salud') renderHealth();
}

// ---------- salud ----------

const HEALTH_STATE = { ok: 'OK', fallo: 'FALLO', apagado: 'apagado', iniciando: 'iniciando', 'sin-bd': '—' };

// renderHealthDot paints the «Salud» tab: green when everything answers, red when something fails.
function renderHealthDot(h) {
  const dot = document.getElementById('health-dot');
  dot.className = 'health-dot' + (!h ? '' : h.ok ? ' ok' : ' bad');
  dot.title = !h ? 'Todavía sin comprobar' : h.ok ? `Todo OK (${h.resumen.ok} de ${h.resumen.total})` : 'Algo falla: mira la pestaña Salud';
}

async function checkHealthNow(button) {
  button.disabled = true;
  button.textContent = 'Comprobando…';
  try {
    await call('POST', '/api/health/check');
    await refresh();
  } catch (e) {
    toast('No se pudo comprobar: ' + e.message);
  }
  renderHealth();
}

// renderHealth shows the last check: the PHP, every service in use and the database of each site.
function renderHealth() {
  const view = document.getElementById('health-view');
  const h = state && state.health;
  const check = el('button', { class: 'secondary', onclick: e => checkHealthNow(e.target) }, 'Comprobar ahora');
  const page = state && state.health_url ? el('a', { href: state.health_url + '?formato=json', target: '_blank', class: 'muted' }, 'JSON para otros programas ↗') : null;
  if (!h) {
    view.replaceChildren(el('div', { class: 'card' }, el('p', {}, 'Nido comprueba la salud un momento después de arrancar y luego cada minuto.'),
      el('div', { class: 'row' }, check)));
    return;
  }
  const bad = h.resumen.total - h.resumen.ok;
  const head = el('div', { class: 'health-head' },
    el('span', { class: 'health-badge ' + (h.ok ? 'ok' : 'bad') }, h.error ? 'Sin respuesta' : h.ok ? `Todo OK · ${h.resumen.ok} de ${h.resumen.total}` : `${bad} con problemas · ${h.resumen.ok} de ${h.resumen.total} OK`),
    el('span', { class: 'muted' }, 'comprobado ' + new Date(h.checked).toLocaleTimeString() + ' · cada minuto, sin abrir el navegador'),
    el('span', { class: 'spacer' }), page, check);
  const chip = st => el('span', { class: 'health-state ' + st }, HEALTH_STATE[st] || st);
  const services = (h.servicios || []).length
    ? el('table', { class: 'health-table' }, el('tr', {}, el('th', {}, 'Servicio'), el('th', {}, 'Dirección'), el('th', {}, 'Respuesta'), el('th', {}, '')),
      ...h.servicios.map(s => el('tr', {}, el('td', {}, s.servicio), el('td', {}, el('code', {}, s.direccion)), el('td', { class: 'muted' }, s.detalle), el('td', {}, chip(s.estado)))))
    : el('p', { class: 'muted' }, 'No hay bases de datos ni servicios instalados.');
  // The failing sites go first: they are what the user comes to look at.
  const sites = (h.sitios || []).filter(s => s.estado !== 'sin-bd')
    .sort((a, b) => (a.estado === 'fallo' ? 0 : 1) - (b.estado === 'fallo' ? 0 : 1));
  const sitesTable = sites.length
    ? el('table', { class: 'health-table' }, el('tr', {}, el('th', {}, 'Sitio'), el('th', {}, 'PHP'), el('th', {}, 'Base de datos'), el('th', {}, 'Respuesta'), el('th', {}, '')),
      ...sites.map(s => el('tr', {}, el('td', {}, s.url ? el('a', { href: s.url, target: '_blank' }, s.sitio) : s.sitio), el('td', {}, s.php),
        el('td', {}, el('code', {}, s.bd)), el('td', { class: 'muted' }, s.detalle), el('td', {}, chip(s.estado)))))
    : el('p', { class: 'muted' }, 'Ningún sitio usa todavía una base de datos.');
  const without = (h.sitios || []).length - sites.length;
  const php = h.php || {};
  view.replaceChildren(...[head,
    h.error ? el('div', { class: 'row-error' }, h.error) : null,
    el('h3', {}, 'PHP global'),
    el('p', {}, el('b', {}, 'PHP ' + (php.version || '?')), el('span', { class: 'muted' }, ` · ${php.arquitectura || ''} · memoria ${php.memoria || ''}`),
      (php.otras || []).length ? el('span', { class: 'muted' }, ' · también en marcha: ' + php.otras.join(', ')) : null),
    el('h3', {}, 'Bases de datos y servicios'), services,
    el('h3', {}, 'Sitios y su base de datos'), sitesTable,
    without ? el('p', { class: 'muted' }, `${without} sitio(s) más no usan base de datos.`) : null].filter(Boolean));
}

// ---------- estado ----------

function render() {
  const s = state;
  renderAlerts(s);
  const health = document.getElementById('health-link');
  health.hidden = !s.health_url;
  health.onclick = e => { e.preventDefault(); showTab('salud'); };
  renderHealthDot(s.health);
  if (ui.tab === 'salud') renderHealth();
  document.querySelectorAll('.php-select').forEach(sel => {
    if (document.activeElement === sel) return;
    sel.replaceChildren(...(s.php_versions || []).map(v => el('option', { value: v, selected: v === s.php }, 'PHP ' + v)));
  });
  document.querySelectorAll('.node-version').forEach(n => { n.textContent = s.node ? 'v' + s.node : 'no instalado'; });
  const news = (s.node_updates || [])[0];
  document.querySelectorAll('.node-note').forEach(n => {
    n.dataset.text = n.dataset.text || n.textContent;
    n.textContent = news ? nodeUpdateText(news, s.node) : n.dataset.text;
  });
  // The Node section follows installs and switches made here, in another window or with the CLI.
  const nodeKey = (s.jobs || []).filter(j => NODE_KINDS.includes(j.kind) && !j.finished).map(j => j.id).join() + '|' + s.node;
  if (ui.tab === 'php' && ui.nodeKey !== undefined && nodeKey !== ui.nodeKey) loadNode();
  ui.nodeKey = nodeKey;
  if (ui.tab === 'php') renderNode();
  const extSel = document.getElementById('ext-version');
  if (document.activeElement !== extSel) {
    extSel.replaceChildren(...(s.php_versions || []).map(v => el('option', { value: v, selected: v === (ui.extVersion || s.php) },
      'PHP ' + v + (v === s.php ? ' (global)' : ''))));
  }
  // The list follows the global PHP until another version is chosen.
  if (ui.tab === 'php' && !ui.extVersion && ui.ext && ui.ext.version && ui.ext.version !== s.php) loadExtensions();
  document.getElementById('processes').replaceChildren(...(s.services || []).map(sv =>
    el('li', { title: sv.last_error || '' }, el('span', {}, sv.name + (sv.port ? ` :${sv.port}` : '')), statePill(sv.state))));
  renderSites();
  // No se redibuja mientras escribes un puerto o eliges una versión.
  const view = document.getElementById('services-view');
  const editing = view.contains(document.activeElement) && /^(INPUT|SELECT)$/.test(document.activeElement.tagName);
  if (!editing) renderServices();
  if (ui.dialog) renderDialog();
  // The form is not redrawn while you type; a creation in progress is.
  if (ui.projectOpen && ui.project.job) renderProjectDialog();
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
    alerts.append(alertBox('Windows todavía no confía en la CA local de Nido: el navegador avisará en los sitios HTTPS.',
      'Confiar en la CA', () => act('POST', '/api/ca/trust')));
  }
  if (s.hosts_missing && s.hosts_missing.length) {
    alerts.append(alertBox(`${s.hosts_missing.length} sitio(s) no están en el archivo hosts (por ejemplo ${s.hosts_missing[0]}).`,
      'Actualizar hosts (pide permiso)', () => act('POST', '/api/hosts/sync')));
  }
  const rt = s.runtime || {};
  if (rt.missing) {
    alerts.append(alertBox('Falta el runtime de Visual C++ de Microsoft: sin él no arrancan PHP, Apache ni MySQL.',
      'Instalar Visual C++ (pide permiso)', () => act('POST', '/api/vcredist/install'), true));
  } else if (rt.needs && rt.restart) {
    alerts.append(alertBox(`El runtime de Visual C++ que pide ${rt.for} ya está instalado: Windows lo termina de aplicar al reiniciar.`));
  } else if (rt.needs) {
    alerts.append(alertBox(`${rt.for} se compiló para el runtime de Visual C++ ${rt.needs} o más nuevo y Windows tiene ${rt.version.split('.').slice(0, 2).join('.')}; Microsoft recomienda actualizarlo.`,
      'Actualizar Visual C++ (pide permiso)', () => act('POST', '/api/vcredist/install')));
  }
  // A new LTS line or a security release of the Node in use, until it is installed or put off.
  for (const u of s.node_updates || []) {
    const busy = (s.jobs || []).some(j => j.kind === 'node' && j.version === u.version && !j.finished);
    if (!(u.line || u.security) || busy || nodeNewsDismissed(u.version)) continue;
    const box = alertBox(nodeUpdateText(u, s.node), u.installed ? `Usar v${u.version}` : u.line ? 'Instalar y usar' : 'Actualizar Node', () => applyNodeUpdate(u));
    box.append(el('button', { class: 'secondary', onclick: () => dismissNodeNews(u.version) }, 'Ahora no'));
    alerts.append(box);
  }
  for (const [what, msg] of Object.entries(s.problems || {})) {
    if (what === 'visual-c++') continue; // shown above, with its button
    alerts.append(alertBox(`${what}: ${firstLine(msg)}`, null, null, true));
  }
}

// renderProjectJobs shows the projects being created (from the panel or with 'nido new') above the sites.
function renderProjectJobs() {
  const box = document.getElementById('project-jobs');
  const jobs = (state.jobs || []).filter(j => j.kind === 'project' && !ui.dismissed[j.id]);
  box.replaceChildren(...jobs.map(j => {
    const close = el('button', { class: 'small secondary', onclick: () => { ui.dismissed[j.id] = true; renderProjectJobs(); } }, 'Cerrar');
    if (j.finished && j.error) {
      return el('div', { class: 'job error' }, el('span', {}, `No se pudo crear ${j.series}: ${j.error}`), el('span', { class: 'spacer' }), close);
    }
    if (j.finished) {
      const site = (state.sites || []).find(s => s.name === j.series);
      return el('div', { class: 'job' }, el('span', {}, el('b', {}, j.series), ` (${j.version}) está listo`),
        site ? el('a', { href: site.url, target: '_blank' }, 'Abrir ' + site.url + ' ↗') : null,
        j.note ? el('span', { class: 'note' }, j.note) : null, el('span', { class: 'spacer' }), close);
    }
    const open = ui.jobOpen[j.id] !== false; // open unless closed: it shows that the job works
    const last = (j.tail || []).slice(-1)[0];
    return el('div', { class: 'job-row' },
      el('div', { class: 'job' }, el('span', {}, el('b', {}, j.series), ` (${j.version}): ${j.phase}… `, el('span', { class: 'muted' }, elapsed(j.started))),
        el('div', { class: 'progress indeterminate' }, el('span')),
        el('button', { class: 'small secondary', onclick: () => { ui.jobOpen[j.id] = !open; renderProjectJobs(); } }, open ? 'Ocultar detalle' : 'Ver detalle'),
        el('button', { class: 'small danger', onclick: () => act('POST', '/api/jobs/cancel', { id: j.id }) }, 'Cancelar')),
      open ? jobProgress(j) : el('div', { class: 'job-last' }, aliveDot(j), last ? el('code', {}, last) : null));
  }));
}

// ---------- progress of long jobs ----------

const isZeroTime = t => !t || t.startsWith('0001-');
const clock = secs => `${Math.floor(secs / 60)}:${String(secs % 60).padStart(2, '0')}`;
const secondsSince = t => Math.max(0, Math.floor((Date.now() - Date.parse(t)) / 1000));
const elapsed = t => isZeroTime(t) ? '' : clock(secondsSince(t));
const span = (a, b) => clock(Math.max(0, Math.round((Date.parse(b) - Date.parse(a)) / 1000)));

// aliveDot says whether the programs of a job are printing: the sign that it really works.
function aliveDot(j) {
  if (isZeroTime(j.output)) return el('span', { class: 'job-alive' }, el('span', { class: 'dot' }), 'esperando la primera salida…');
  const quiet = secondsSince(j.output);
  const text = quiet < 5 ? 'trabajando · última salida hace ' + quiet + ' s'
    : `sin salida desde hace ${clock(quiet)}: una descarga o un paso largo sigue en curso`;
  return el('span', { class: 'job-alive' + (quiet >= 30 ? ' quiet' : '') }, el('span', { class: 'dot' }), text);
}

// jobProgress shows a long job: total time, each phase with its own, and what the programs print.
function jobProgress(j) {
  const steps = (j.steps || []).map(s => {
    const now = isZeroTime(s.ended);
    return el('li', { class: now ? 'now' : 'done' }, el('span', { class: 'ico' }, now ? '›' : '✓'), el('span', {}, s.name),
      el('span', { class: 't' }, now ? elapsed(s.started) : span(s.started, s.ended)));
  });
  return el('div', { class: 'job-progress' },
    el('div', { class: 'job-head' }, el('b', {}, `${j.series} · ${j.version}`), el('span', { class: 'spacer' }), el('span', { class: 'elapsed' }, elapsed(j.started))),
    steps.length ? el('ul', { class: 'job-steps' }, ...steps) : null,
    (j.tail || []).length ? el('pre', { class: 'job-tail' }, j.tail.join('\n')) : null,
    aliveDot(j));
}

function renderSites() {
  if (!state) return;
  renderProjectJobs();
  const tbody = document.getElementById('sites');
  if (tbody.contains(document.activeElement) && document.activeElement.tagName === 'SELECT') return;
  const q = document.getElementById('filter').value.toLowerCase();
  const list = (state.sites || []).filter(x => !q || x.host.includes(q) || x.folder.toLowerCase().includes(q));
  tbody.replaceChildren(...list.map(x => el('tr', {},
    el('td', {}, el('a', { href: x.url, target: '_blank' }, x.host), ' ',
      el('button', { class: 'ghost', title: 'Guardar como plantilla para proyectos nuevos', onclick: () => saveTemplate(x) }, '⧉')),
    el('td', {}, x.kind),
    el('td', {}, serverPicker(x)),
    el('td', {}, phpPicker(x)),
    el('td', {}, processToggles(x)),
    el('td', { class: 'folder' }, x.doc_root),
    el('td', {}, String(x.port)),
  )));
}

// serverPicker lets a site choose among Apache and the web servers that are installed.
function serverPicker(site) {
  const names = { apache: ('Apache ' + (state.apache || '')).trim() };
  for (const [k, info] of Object.entries(SERVERS)) {
    const v = info.version(state);
    if (v || site.server === k) names[k] = (`${info.name} ${v || ''}` + (k === 'frankenphp' && state.frankenphp_php ? ` (PHP ${state.frankenphp_php})` : '')).trim();
  }
  if (Object.keys(names).length === 1) return el('span', { class: 'svc-sub' }, names.apache);
  const sel = el('select', { 'aria-label': 'Servidor de ' + site.host },
    ...Object.entries(names).map(([k, label]) => el('option', { value: k, selected: site.server === k }, label)));
  sel.addEventListener('change', () => act('POST', '/api/sites/server', { site: site.name, server: sel.value }));
  return sel;
}

const PROCESS_HELP = {
  vite: 'npm run dev con HTTPS (el certificado del sitio)',
  queue: 'php artisan queue:work',
  schedule: 'php artisan schedule:work',
  reverb: 'WebSockets de Laravel (reverb:start)',
};

// processToggles switches a site's processes (Vite, the queue, the scheduler); one that is on also
// starts with «Iniciar todo».
const MISSING_LABEL = { vendor: 'paquetes de Composer', '.env': '.env', frontend: 'frontend compilado' };

function processToggles(site) {
  const buttons = (site.processes || []).map(p => {
    const key = `proc-${site.name}-${p.id}`;
    const help = p.id === 'reverb' ? `${PROCESS_HELP.reverb} en wss://reverb.${site.host}` : PROCESS_HELP[p.id];
    const title = `${help} · ${p.on ? p.state + ' (clic para detenerlo)' : 'clic para arrancarlo'}`;
    return el('button', { class: 'proc' + (p.on ? ' on' : '') + (p.state === 'en marcha' ? ' running' : ''), disabled: !!ui.pending[key], title,
      onclick: () => act('POST', '/api/sites/process', { site: site.name, id: p.id, on: !p.on }, key) }, p.label);
  });
  // A project that is not ready (cloned or copied) gets what it lacks with one click.
  if (site.missing && site.missing.length) {
    const busy = (state.jobs || []).some(j => j.kind === 'project' && j.series === site.name && !j.finished);
    buttons.unshift(el('button', { class: 'proc warn', disabled: busy, title: 'Le falta: ' + site.missing.map(m => MISSING_LABEL[m] || m).join(', '),
      onclick: () => setupProject(site) }, busy ? 'Preparando…' : 'Preparar'));
  }
  return buttons.length ? el('div', { class: 'procs' }, ...buttons) : el('span', { class: 'svc-sub' }, '—');
}

async function setupProject(site) {
  try {
    await call('POST', '/api/projects/setup', { site: site.name });
    toast(`Preparando ${site.host}: Composer, .env, base de datos y frontend, lo que falte`);
  } catch (e) {
    alert(e.message);
  }
  await refresh();
}

// phpPicker keeps a site on the global PHP or pins another installed version (older projects).
function phpPicker(site) {
  if (site.server === 'frankenphp') return el('span', { class: 'svc-sub' }, ('propio ' + (state.frankenphp_php || '')).trim());
  const versions = [...(state.php_versions || [])];
  if (site.php && !versions.includes(site.php)) versions.unshift(site.php);
  const sel = el('select', { 'aria-label': 'PHP de ' + site.host, title: site.php ? `Fijado: PHP ${site.php_version || site.php}` : 'Usa el PHP global' },
    el('option', { value: '', selected: !site.php }, `global (${state.php || '—'})`),
    ...versions.map(v => el('option', { value: v, selected: site.php === v },
      v === site.php && site.php_version && site.php_version !== v ? `${v} (${site.php_version})` : v)));
  sel.addEventListener('change', () => act('POST', '/api/sites/php', { site: site.name, php: sel.value }));
  // Xdebug for this site alone; Nido fetches it for that PHP when it lacks it.
  const key = 'debug-' + site.name;
  const debug = el('button', { class: 'proc' + (site.debug ? ' on running' : ''), disabled: !!ui.pending[key],
    title: site.debug ? 'Con Xdebug: cada petición se conecta al editor en 127.0.0.1:9003 (clic para quitarlo)' : 'Depurar este sitio con Xdebug',
    onclick: () => act('POST', '/api/sites/debug', { site: site.name, on: !site.debug }, key) }, 'Xdebug');
  return el('div', { class: 'procs' }, sel, debug);
}

// ---------- servicios ----------

function renderServices() {
  if (!state) return;
  const s = state;
  // Each section: full cards for what can be installed, small «próximamente» cards for the rest.
  const section = (title, kinds) => {
    const groups = kinds.map(groupOf);
    const soon = groups.filter(g => !isAvailable(g));
    return [el('div', { class: 'group-title' }, title), ...groups.filter(isAvailable).map(dbCard),
      soon.length ? el('div', { class: 'grid3' }, ...soon.map(g => soonCard({ kind: g.kind, name: KIND_INFO[g.kind].name, desc: KIND_INFO[g.kind].desc }))) : null];
  };
  document.getElementById('services-view').replaceChildren(
    el('div', { class: 'toolbar' },
      el('h2', {}, 'Servicios'),
      chips(),
      el('span', { class: 'spacer' }),
      el('button', { onclick: openDialog }, '＋ Añadir servicio')),
    ...phpBars(s),
    el('div', { class: 'group-title' }, 'Servidores web'),
    el('div', { class: 'grid3' }, apacheCard(s), serverCard(s, 'nginx'), serverCard(s, 'frankenphp')),
    ...section('Bases de datos', DB_KINDS),
    ...section('Caché, búsqueda y correo', OTHER_KINDS),
    el('div', { class: 'group-title' }, 'Herramientas'),
    el('div', { class: 'grid3' }, adminerCard(s)),
  );
}

// adminerGroup describes Adminer like a database group, so the install picker and job lines work alike.
function adminerGroup(s) {
  return { kind: 'adminer', name: 'Adminer', instances: s.adminer ? [{ series: s.adminer.split('.').slice(0, 2).join('.') }] : [] };
}

function adminerCard(s) {
  const st = (s.services || []).find(x => x.name === 'adminer');
  const jobs = (s.jobs || []).filter(j => j.kind === 'adminer' && !ui.dismissed[j.id] && (!j.finished || j.error));
  if (!s.adminer) {
    return el('div', { class: 'svc dashed' },
      el('div', { class: 'svc-head' }, logo('adminer'), el('div', {}, el('div', { class: 'svc-title' }, 'Adminer'), el('div', { class: 'svc-sub' }, 'sin instalar'))),
      el('div', { class: 'svc-body' }, el('div', { class: 'svc-sub' }, 'Las bases de datos en el navegador (MySQL, MariaDB, PostgreSQL y SQLite), en db.localhost.')),
      ...jobs.map(j => jobLine(j, 'Adminer')),
      el('div', { class: 'svc-foot' }, ...installPicker(adminerGroup(s))));
  }
  return el('div', { class: 'svc' },
    el('div', { class: 'svc-head' }, logo('adminer'),
      el('div', {}, el('div', { class: 'svc-title' }, 'Adminer'), el('div', { class: 'svc-sub' }, 'versión ' + s.adminer)),
      el('span', { class: 'spacer' }), statePill(st ? st.state : 'detenido')),
    el('div', { class: 'svc-body' }, el('div', { class: 'svc-sub' }, 'Entra con root (MySQL y MariaDB) o postgres, sin contraseña. Solo responde a este equipo.')),
    ...jobs.map(j => jobLine(j, 'Adminer')),
    el('div', { class: 'svc-foot' }, el('a', { href: s.adminer_url, target: '_blank' }, 'Abrir ' + s.adminer_url.replace(/\/$/, '') + ' ↗')));
}

// adminerLink opens Adminer on one instance, with its server and user already filled in.
function adminerLink(kind, port) {
  const q = kind === 'postgresql' ? `pgsql=127.0.0.1:${port}&username=postgres` : `server=127.0.0.1:${port}&username=root`;
  return state.adminer_url + '?' + q;
}

function chips() {
  const opts = [['todos', 'Todos'], ['marcha', 'En marcha'], ['detenidos', 'Detenidos']];
  return el('div', { class: 'chips' }, ...opts.map(([k, label]) =>
    el('span', { class: 'chip' + (ui.filter === k ? ' on' : ''), onclick: () => { ui.filter = k; renderServices(); } }, label)));
}

const passes = i => ui.filter === 'todos' || (ui.filter === 'marcha' ? i.state === 'en marcha' : i.state !== 'en marcha');

// phpBars shows the global PHP's pool and, below it, the pool of each version some site pinned.
function phpBars(s) {
  const pools = s.php_pools || [];
  const procs = p => (s.services || []).filter(x => p.ports.some(port => x.name === 'php-cgi-' + port));
  const label = list => {
    const up = list.filter(x => x.state === 'en marcha').length;
    return !list.length ? 'detenido' : up === list.length ? 'en marcha' : `${up} de ${list.length} en marcha`;
  };
  const range = p => p.ports.length ? ` (puertos ${p.ports[0]}–${p.ports[p.ports.length - 1]})` : '';
  const global = pools.find(p => p.global) || { ports: [] };
  const bars = [el('div', { class: 'phpbar' },
    statePill(label(procs(global))),
    el('span', {}, el('b', {}, 'PHP global ' + (s.php || '—')), ` · grupo de ${global.ports.length} procesos `, el('span', { class: 'mono' }, 'php-cgi'), range(global)),
    el('span', { class: 'spacer' }),
    el('a', { href: '#php', onclick: e => { e.preventDefault(); showTab('php'); } }, 'Cambiar versión en «PHP y Node» →'))];
  for (const p of pools.filter(p => !p.global)) {
    const who = p.sites === 1 ? '1 sitio' : p.sites + ' sitios';
    bars.push(el('div', { class: 'phpbar' },
      statePill(label(procs(p))),
      el('span', {}, el('b', {}, 'PHP ' + p.version + (p.debug ? ' con Xdebug' : '')), (p.debug ? ` para ${who} en depuración` : ` fijado por ${who}`) + ` · ${p.ports.length} procesos `, el('span', { class: 'mono' }, 'php-cgi'), range(p)),
      el('span', { class: 'spacer' }),
      el('a', { href: '#sitios', onclick: e => { e.preventDefault(); showTab('sitios'); } }, 'Cambiarlo en «Sitios» →')));
  }
  return bars;
}

// apacheGroup describes the installed Apache versions like a database group. They are all series
// 2.4, so the catalog is compared by exact version.
function apacheGroup(s) {
  return { kind: 'apache', name: 'Apache', byVersion: true, instances: (s.apache_versions || []).map(v => ({ series: v })) };
}

function apacheCard(s) {
  const st = (s.services || []).find(x => x.name === 'apache');
  const g = apacheGroup(s);
  const jobs = (s.jobs || []).filter(j => j.kind === 'apache' && !ui.dismissed[j.id] && (!j.finished || j.error));
  if (!s.apache) {
    return el('div', { class: 'svc dashed' },
      el('div', { class: 'svc-head' }, logo('apache'), el('div', {}, el('div', { class: 'svc-title' }, 'Apache'), el('div', { class: 'svc-sub' }, 'sin instalar'))),
      el('div', { class: 'svc-body' }, el('div', { class: 'svc-sub' }, 'El servidor de los sitios: sin él solo funcionan los que eligen Nginx o FrankenPHP.')),
      ...jobs.map(j => jobLine(j, 'Apache')),
      el('div', { class: 'svc-foot' }, ...installPicker(g)));
  }
  const users = (s.sites || []).filter(x => x.server === 'apache').length;
  let version = 'versión ' + s.apache;
  if (s.apache_versions.length > 1) {
    version = el('select', { 'aria-label': 'Versión de Apache',
      onchange: e => act('POST', '/api/apache', { version: e.target.value }) },
      ...s.apache_versions.map(v => el('option', { value: v, selected: v === s.apache }, 'versión ' + v)));
  }
  const cat = ui.catalog.apache;
  const newer = (cat || []).some(r => !s.apache_versions.includes(r.version));
  return el('div', { class: 'svc' },
    el('div', { class: 'svc-head' }, logo('apache'),
      el('div', {}, el('div', { class: 'svc-title' }, 'Apache'), el('div', { class: 'svc-sub' }, version)),
      el('span', { class: 'spacer' }), statePill(st ? st.state : 'detenido')),
    el('div', { class: 'svc-body' }, el('div', { class: 'svc-sub' },
      `Atiende ${users === 1 ? '1 sitio' : users + ' sitios'}, cada uno en su puerto interno, con PHP por FastCGI.`)),
    ...jobs.map(j => jobLine(j, 'Apache')),
    el('div', { class: 'svc-foot' }, ...(newer ? ['Disponible:', ...installPicker(g)] : ['Servidor de los sitios que no eligen otro'])));
}

// serverGroup describes a web server like a database group, so the install picker and job lines work alike.
function serverGroup(s, kind) {
  const v = SERVERS[kind].version(s);
  return { kind, name: SERVERS[kind].name, instances: v ? [{ series: v.split('.').slice(0, 2).join('.') }] : [] };
}

function serverCard(s, kind) {
  const info = SERVERS[kind];
  const version = info.version(s);
  const g = serverGroup(s, kind);
  const st = (s.services || []).find(x => x.name === kind);
  const users = (s.sites || []).filter(x => x.server === kind).length;
  const jobs = (s.jobs || []).filter(j => j.kind === kind && !ui.dismissed[j.id] && (!j.finished || j.error));
  const toSites = el('a', { href: '#sitios', onclick: e => { e.preventDefault(); showTab('sitios'); } }, 'Elegirlo en un sitio →');
  if (!version) {
    return el('div', { class: 'svc dashed' },
      el('div', { class: 'svc-head' }, logo(kind), el('div', {}, el('div', { class: 'svc-title' }, info.name), el('div', { class: 'svc-sub' }, 'sin instalar'))),
      el('div', { class: 'svc-body' }, el('div', { class: 'svc-sub' }, info.desc)),
      ...jobs.map(j => jobLine(j, info.name)),
      el('div', { class: 'svc-foot' }, ...installPicker(g)));
  }
  return el('div', { class: 'svc' },
    el('div', { class: 'svc-head' }, logo(kind),
      el('div', {}, el('div', { class: 'svc-title' }, info.name), el('div', { class: 'svc-sub' }, 'versión ' + version)),
      el('span', { class: 'spacer' }), statePill(st ? st.state : (users ? 'detenido' : 'sin sitios'))),
    el('div', { class: 'svc-body' }, el('div', { class: 'svc-sub' }, users
      ? `Atiende ${users === 1 ? '1 sitio' : users + ' sitios'}, ${info.php(s)}.`
      : 'Ningún sitio lo usa todavía: arranca solo cuando un sitio lo elige en la pestaña Sitios.')),
    ...jobs.map(j => jobLine(j, info.name)),
    el('div', { class: 'svc-foot' }, toSites));
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
  if (i.source === 'download') notes.push('descargada');
  if (i.external) notes.push('usa los datos de ' + shortPath(i.data_dir));
  return el('tr', {},
    el('td', {}, el('input', { type: 'radio', name: 'def-' + g.kind, checked: i.default, title: 'Predeterminada: puerto estándar y atajo mysql',
      onchange: () => act('POST', '/api/services/default', { key: i.key }, i.key) })),
    el('td', {}, el('span', { class: 'ver' }, i.version || i.series), ' ', el('span', { class: 'svc-sub' }, notes.join(' · ')),
      i.web_url && i.state === 'en marcha' ? el('span', { class: 'svc-sub' }, ' · ', el('a', { href: i.web_url, target: '_blank' }, 'abrir ↗')) : null,
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
    ADMINER_KINDS.includes(g.kind) && state.adminer_url ? item('Abrir en Adminer', () => window.open(adminerLink(g.kind, i.port), '_blank')) : null,
    item('Ver log', () => act('POST', '/api/services/open', { key: i.key, what: 'log' })),
    item('Abrir carpeta de datos', () => act('POST', '/api/services/open', { key: i.key, what: 'data' })),
    item('Copiar datos de conexión (.env)', () => copyEnv(g, i)),
    el('div', { class: 'sep' }),
    item(i.source === 'download' ? 'Desinstalar (conserva los datos)' : 'Quitar de Nido (sus archivos no se tocan)', () => uninstall(g, i), 'red'));
}

function installPicker(g) {
  const cat = ui.catalog[g.kind];
  if (!cat) {
    return [el('span', { class: 'svc-sub' }, ui.catalogError[g.kind] ? 'No se pudo leer el catálogo: ' + ui.catalogError[g.kind] : 'Consultando las versiones disponibles…')];
  }
  const key = r => g.byVersion ? r.version : r.series;
  const have = new Set(g.instances.map(i => i.series));
  const busy = new Set((state.jobs || []).filter(j => j.kind === g.kind && !j.finished).map(key));
  const options = cat.filter(r => !have.has(key(r)) && !busy.has(key(r)));
  if (!options.length) return [el('span', { class: 'svc-sub' }, 'Ya tienes todas las versiones del catálogo')];
  const sel = el('select', { 'aria-label': 'Versión' }, ...options.map(r =>
    el('option', { value: key(r) }, r.version + (r.note ? ' · ' + r.note : '') + (r.size > 0 ? ` · ${mb(r.size)} MB` : ''))));
  if (options.some(r => key(r) === ui.choice[g.kind])) sel.value = ui.choice[g.kind];
  sel.addEventListener('change', () => { ui.choice[g.kind] = sel.value; });
  return [sel, el('button', { class: 'small' + (g.instances.length ? ' secondary' : ''), onclick: () => installSeries(g.kind, sel.value) },
    g.instances.length ? 'Instalar otra' : 'Instalar')];
}

function jobLine(j, name) {
  if (j.finished && j.error) {
    return el('div', { class: 'job error' }, el('span', {}, `No se pudo instalar ${name} ${j.version}: ${j.error}`), el('span', { class: 'spacer' }),
      el('button', { class: 'small secondary', onclick: () => { ui.dismissed[j.id] = true; renderServices(); renderNode(); if (ui.dialog) renderDialog(); } }, 'Cerrar'));
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
    : `¿Quitar ${what} de Nido? Sus archivos no se tocan y los datos se conservan.`;
  if (confirm(msg)) act('POST', '/api/services/uninstall', { key: i.key }, i.key);
}

// envLines is what a Laravel .env needs to reach an instance.
function envLines(kind, port) {
  switch (kind) {
    case 'postgresql': return ['DB_CONNECTION=pgsql', 'DB_HOST=127.0.0.1', `DB_PORT=${port}`, 'DB_DATABASE=', 'DB_USERNAME=postgres', 'DB_PASSWORD='];
    case 'mongodb': return [`MONGODB_URI=mongodb://127.0.0.1:${port}`];
    case 'redis': return ['REDIS_CLIENT=predis', 'REDIS_HOST=127.0.0.1', `REDIS_PORT=${port}`, 'REDIS_PASSWORD=null'];
    case 'mailpit': return ['MAIL_MAILER=smtp', 'MAIL_HOST=127.0.0.1', `MAIL_PORT=${port}`, 'MAIL_USERNAME=null', 'MAIL_PASSWORD=null'];
    case 'meilisearch': return ['SCOUT_DRIVER=meilisearch', `MEILISEARCH_HOST=http://127.0.0.1:${port}`];
  }
  return [`DB_CONNECTION=${kind === 'mariadb' ? 'mariadb' : 'mysql'}`, 'DB_HOST=127.0.0.1', `DB_PORT=${port}`, 'DB_DATABASE=', 'DB_USERNAME=root', 'DB_PASSWORD='];
}

async function copyEnv(g, i) {
  const text = envLines(g.kind, i.port).join('\n');
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
  for (const kind of INSTALLABLE) {
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

// ---------- extensiones de PHP ----------

async function loadExtensions() {
  try {
    ui.ext = await call('GET', '/api/php/extensions?version=' + encodeURIComponent(ui.extVersion));
  } catch (e) {
    ui.ext = { error: e.message, extensions: [] };
  }
  renderExtensions();
}

const EXT_TAG = { descargable: 'se descarga', descargada: 'descargada' };

function renderExtensions() {
  const box = document.getElementById('extensions');
  if (!ui.ext) { box.replaceChildren(el('span', { class: 'muted' }, 'Cargando…')); return; }
  if (ui.ext.error) { box.replaceChildren(el('span', { class: 'muted' }, ui.ext.error)); return; }
  box.replaceChildren(...ui.ext.extensions.map(x => {
    const busy = ui.extBusy[x.name];
    const check = el('input', { type: 'checkbox', checked: x.enabled, disabled: !!busy });
    check.addEventListener('change', () => toggleExtension(x, check.checked));
    const tag = EXT_TAG[x.source];
    return el('label', { class: busy ? 'busy' : '', title: x.default ? 'Nido la activa por defecto' : '' }, check,
      el('span', {}, x.name), tag ? el('span', { class: 'tag' + (x.source === 'descargable' ? ' get' : '') }, tag) : null);
  }));
}

async function toggleExtension(x, on) {
  ui.extBusy[x.name] = true;
  renderExtensions();
  try {
    ui.ext = await call('POST', '/api/php/extensions', { version: ui.ext.version, name: x.name, enabled: on });
    toast(`${x.name} ${on ? 'activada' : 'desactivada'} en PHP ${ui.ext.version}`);
  } catch (e) {
    alert(e.message);
    await loadExtensions();
  } finally {
    delete ui.extBusy[x.name];
    renderExtensions();
  }
}

// ---------- Node y gestores de paquetes ----------

const NODE_SOURCE = { download: 'descargada' };
const NODE_DISMISSED = 'nido-node-avisos';

function nodeUpdateText(u, global) {
  if (u.line) return `Salió Node ${u.version.split('.')[0]}, la nueva LTS («${u.lts}»): v${u.version}.`;
  if (u.security) return `Node v${u.version} corrige fallos de seguridad de tu versión (v${global}).`;
  return `Hay una versión nueva de tu línea de Node: v${u.version}.`;
}

const nodeNewsDismissed = v => (localStorage.getItem(NODE_DISMISSED) || '').split(',').includes(v);

function dismissNodeNews(v) {
  const list = (localStorage.getItem(NODE_DISMISSED) || '').split(',').filter(Boolean);
  localStorage.setItem(NODE_DISMISSED, [...list, v].join(','));
  render();
}

async function loadNode(refreshList) {
  try {
    ui.node = await call('GET', '/api/node' + (refreshList ? '?refresh=1' : ''));
  } catch (e) {
    ui.node = { error: e.message, installed: [], lines: [], updates: [], tools: [] };
  }
  renderNode();
}

function renderNode() {
  const view = document.getElementById('node-view');
  if (!state || (view.contains(document.activeElement) && document.activeElement.tagName === 'SELECT')) return;
  if (!ui.node) { view.replaceChildren(el('div', { class: 'svc-sub' }, 'Consultando las versiones de Node…')); return; }
  view.replaceChildren(nodeCard(ui.node), toolsCard(ui.node));
}

function nodeCard(n) {
  const jobs = (state.jobs || []).filter(j => j.kind === 'node' && !ui.dismissed[j.id] && (!j.finished || j.error));
  const busy = new Set((state.jobs || []).filter(j => j.kind === 'node' && !j.finished).map(j => j.version));
  const cur = n.installed.find(v => v.in_use);
  const line = cur && n.lines.find(r => r.series === cur.version.split('.')[0]);
  const news = (n.updates || []).filter(u => !busy.has(u.version)).map(u => el('div', { class: 'news' + (u.line || u.security ? ' warn' : '') },
    el('span', {}, nodeUpdateText(u, n.global)), el('span', { class: 'spacer' }),
    el('button', { class: 'small', onclick: () => applyNodeUpdate(u) }, u.installed ? `Usar v${u.version}` : u.line ? 'Instalar y usar' : 'Actualizar')));
  const rows = n.installed.map(v => el('tr', {},
    el('td', {}, el('span', { class: 'ver' }, 'v' + v.version), ' ', el('span', { class: 'svc-sub' }, NODE_SOURCE[v.source] || v.source)),
    el('td', { class: 'right' }, v.in_use ? el('span', { class: 'pill def' }, 'global') : [
      el('button', { class: 'small secondary', onclick: () => useNode(v.version) }, 'Usar'),
      el('button', { class: 'ghost', title: v.removable ? 'Borrar esta versión' : 'Quitar de Nido (sus archivos no se tocan)', onclick: () => removeNode(v) }, '✕')])));
  const have = new Set(n.installed.map(v => v.version));
  const options = n.lines.filter(r => !have.has(r.version) && !busy.has(r.version));
  let picker;
  if (n.error) {
    picker = [el('span', { class: 'svc-sub' }, 'No se pudo leer la lista de nodejs.org: ' + firstLine(n.error))];
  } else if (!options.length) {
    picker = [el('span', { class: 'svc-sub' }, 'Tienes la última versión de cada línea con soporte')];
  } else {
    const sel = el('select', { 'aria-label': 'Versión de Node' }, ...options.map(r => el('option', { value: r.version }, `v${r.version} · ${r.note}`)));
    picker = ['Instalar:', sel, el('button', { class: 'small secondary', onclick: () => installNode(sel.value, false) }, 'Instalar')];
  }
  return el('div', { class: 'svc' },
    el('div', { class: 'svc-head' }, logo('node'),
      el('div', {}, el('div', { class: 'svc-title' }, 'Node'),
        el('div', { class: 'svc-sub' }, cur ? `v${cur.version}${line ? ' · ' + line.note : ''} · con npm y npx` : 'sin instalar'))),
    ...news,
    n.installed.length ? el('table', { class: 'rows' }, el('tbody', {}, ...rows)) : null,
    ...jobs.map(j => jobLine(j, 'Node')),
    el('div', { class: 'svc-foot' }, ...picker, el('span', { class: 'spacer' }), 'Descarga de nodejs.org'));
}

function toolsCard(n) {
  const rows = (n.tools || []).map(t => {
    const job = (state.jobs || []).find(j => j.kind === t.name && !j.finished);
    const failed = (state.jobs || []).find(j => j.kind === t.name && j.finished && j.error && !ui.dismissed[j.id]);
    return el('tr', {},
      el('td', {}, el('span', { class: 'ver' }, t.name), ' ', el('span', { class: 'svc-sub' }, t.version ? 'v' + t.version : 'sin instalar'),
        failed ? el('div', { class: 'row-error' }, firstLine(failed.error), ' ',
          el('a', { href: '#', onclick: e => { e.preventDefault(); ui.dismissed[failed.id] = true; renderNode(); } }, 'cerrar')) : null),
      el('td', { class: 'right' }, job ? el('span', { class: 'svc-sub' }, job.phase + '…') : t.version ? [
        el('button', { class: 'small secondary', title: 'Instala la última versión', onclick: () => nodeTool(t.name, true) }, 'Actualizar'),
        el('button', { class: 'ghost', title: 'Quitar ' + t.name, onclick: () => nodeTool(t.name, false) }, '✕')]
        : el('button', { class: 'small', disabled: !n.global, onclick: () => nodeTool(t.name, true) }, 'Instalar')));
  });
  return el('div', { class: 'svc' },
    el('div', { class: 'svc-head' }, logo('pm'),
      el('div', {}, el('div', { class: 'svc-title' }, 'Gestores de paquetes'),
        el('div', { class: 'svc-sub' }, 'npm viene con Node. pnpm y yarn se instalan como paquetes globales de npm y sirven con cualquier versión.'))),
    el('table', { class: 'rows' }, el('tbody', {}, ...rows)),
    el('div', { class: 'svc-foot' }, 'Cada proyecto usa el suyo, según su lockfile: Vite, «Preparar» y «Nuevo proyecto» lo respetan.'));
}

async function installNode(version, use) {
  try {
    await call('POST', '/api/node/install', { version, use });
    toast(`Descargando Node v${version} de nodejs.org…`);
  } catch (e) {
    alert(e.message);
  }
  await refresh();
}

async function useNode(version) {
  try {
    await call('POST', '/api/node/use', { version });
    toast(`Node v${version} es ahora el global (terminal, proyectos y Vite)`);
  } catch (e) {
    alert(e.message);
  }
  await refresh();
  await loadNode();
}

async function applyNodeUpdate(u) {
  if (u.installed) return useNode(u.version);
  await installNode(u.version, true);
  if (ui.tab !== 'php') showTab('php');
}

async function removeNode(v) {
  const msg = v.removable ? `¿Borrar Node v${v.version}? Se borra su carpeta (${v.path}).` : `¿Quitar Node v${v.version} de Nido? Sus archivos no se tocan.`;
  if (!confirm(msg)) return;
  try {
    await call('POST', '/api/node/uninstall', { version: v.version });
    toast(`Node v${v.version} quitado`);
  } catch (e) {
    alert(e.message);
  }
  await refresh();
  await loadNode();
}

async function nodeTool(name, on) {
  if (!on && !confirm(`¿Quitar ${name}? Si un proyecto lo necesita, «Preparar» lo vuelve a instalar.`)) return;
  try {
    await call('POST', '/api/node/tool', { name, on });
  } catch (e) {
    alert(e.message);
  }
  await refresh();
}

// ---------- «Nuevo proyecto» ----------

const KITS = [
  ['', 'Sin kit', 'Laravel con Blade'],
  ['react', 'React', 'Inertia, React y Tailwind'],
  ['vue', 'Vue', 'Inertia, Vue y Tailwind'],
  ['svelte', 'Svelte', 'Inertia, Svelte y Tailwind'],
  ['livewire', 'Livewire', 'Livewire y Tailwind'],
];
ui.project = { source: 'laravel', name: '', kit: '', db: 'sqlite', pm: 'npm', locale: 'es_ES', webapp: true, root: '', git: '', branch: '',
  server: '', php: '', dbName: '', dbUser: false, seed: false, job: null, error: null };
// WordPress languages offered first (any WordPress locale works from 'nido new --idioma').
const WP_LOCALES = [['es_ES', 'Español (España)'], ['es_MX', 'Español (México)'], ['es_AR', 'Español (Argentina)'], ['es_CO', 'Español (Colombia)'],
  ['es_CL', 'Español (Chile)'], ['en_US', 'English (US)'], ['pt_BR', 'Português (Brasil)'], ['fr_FR', 'Français'], ['de_DE', 'Deutsch'], ['it_IT', 'Italiano']];
// Package managers a new project can install its frontend with, like 'laravel new --pnpm'.
const PMS = [['npm', 'npm (viene con Node)'], ['pnpm', 'pnpm'], ['yarn', 'yarn']];

// slug turns a name into the site name the server will use ("Mi Tienda" → "mi-tienda").
const slug = s => s.toLowerCase().normalize('NFD').replace(/[̀-ͯ]/g, '')
  .replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 63);

// projectDBOptions lists where a new project's data can go: SQLite or any installed database
// server (its key, "mysql-8.4"), the default of each kind first.
function projectDBOptions() {
  const opts = [['sqlite', 'SQLite (un archivo dentro del proyecto)']];
  for (const kind of ['mysql', 'mariadb', 'postgresql']) {
    const g = groupOf(kind);
    const list = [...g.instances].sort((a, b) => (b.default ? 1 : 0) - (a.default ? 1 : 0));
    for (const i of list) opts.push([i.key, `${g.name} ${i.version} · puerto ${i.port}${i.default ? ' (predeterminada)' : ''}`]);
  }
  return opts;
}

// A database choice is SQLite, an instance key or '' (the project's own).
const dbKind = choice => choice && choice !== 'sqlite' ? choice.split('-')[0] : choice;

// serverOptions are the web servers a new site can start on: the ones installed.
function serverOptions() {
  const opts = [['', `Apache ${state.apache || ''}`.trim()]];
  opts.push(state.nginx ? ['nginx', `Nginx ${state.nginx}`] : ['nginx', 'Nginx (instálalo en Servicios)', true]);
  opts.push(state.frankenphp ? ['frankenphp', `FrankenPHP ${state.frankenphp} (con su PHP ${state.frankenphp_php})`] : ['frankenphp', 'FrankenPHP (instálalo en Servicios)', true]);
  return opts;
}

const options = (list, current) => list.map(([k, label, disabled]) => el('option', { value: k, selected: current === k, disabled: !!disabled }, label));

function openProjectDialog() {
  ui.projectOpen = true;
  document.getElementById('project-dialog').hidden = false;
  renderProjectDialog();
  const input = document.getElementById('project-name');
  if (input) input.focus();
}

function closeProjectDialog() {
  ui.projectOpen = false;
  document.getElementById('project-dialog').hidden = true;
}

function renderProjectDialog() {
  if (!state) return;
  const p = ui.project;
  const body = document.getElementById('project-body');
  const job = p.job && (state.jobs || []).find(j => j.id === p.job);
  const again = () => {
    ui.project = { source: p.source, name: '', kit: p.kit, db: p.db, pm: p.pm, locale: p.locale, webapp: p.webapp, root: p.root, git: '', branch: '',
      server: p.server, php: p.php, dbName: '', dbUser: p.dbUser, seed: p.seed, job: null, error: null };
    renderProjectDialog();
  };
  if (job && job.finished && !job.error) {
    const site = (state.sites || []).find(s => s.name === job.series);
    body.replaceChildren(
      el('div', {}, el('b', {}, 'Listo. '), site ? el('a', { href: site.url, target: '_blank' }, 'Abrir ' + site.url + ' ↗') : job.series),
      el('div', { class: 'svc-sub' }, site ? 'Carpeta: ' + site.folder : ''),
      job.note ? el('div', { class: 'note' }, job.note) : null,
      el('div', { class: 'row' }, el('span', { class: 'spacer' }), el('button', { class: 'secondary', onclick: again }, 'Crear otro'),
        el('button', { onclick: closeProjectDialog }, 'Cerrar')));
    return;
  }
  if (job && job.finished) {
    body.replaceChildren(el('div', { class: 'job error' }, el('span', {}, `No se pudo crear ${job.series}: ${job.error}`)),
      el('div', { class: 'row' }, el('span', { class: 'spacer' }), el('button', { class: 'secondary', onclick: () => { p.job = null; renderProjectDialog(); } }, 'Volver')));
    return;
  }
  if (job) {
    body.replaceChildren(jobProgress(job),
      el('div', { class: 'row' }, el('span', { class: 'svc-sub' }, 'Puedes cerrar esta ventana: la creación sigue y el sitio aparece en la lista al terminar.'),
        el('span', { class: 'spacer' }),
        el('button', { class: 'small danger', onclick: () => act('POST', '/api/jobs/cancel', { id: job.id }) }, 'Cancelar'),
        el('button', { class: 'small secondary', onclick: closeProjectDialog }, 'Seguir en segundo plano')));
    return;
  }
  const name = el('input', { id: 'project-name', type: 'text', value: p.name, placeholder: 'mi-tienda', autocomplete: 'off', spellcheck: 'false' });
  const preview = el('div', { class: 'svc-sub' });
  const roots = state.roots || [];
  const showPreview = () => {
    const s = slug(name.value);
    preview.textContent = s ? `https://${s}.test · en ${(p.root || roots[0] || '').replace(/\\$/, '')}\\${s}` : 'El nombre da la dirección: mi-tienda → https://mi-tienda.test';
  };
  name.addEventListener('input', () => { p.name = name.value; showPreview(); });
  name.addEventListener('keydown', e => { if (e.key === 'Enter') createProject(); });
  showPreview();
  const fromGit = p.source === 'git';
  const wordpress = p.source === 'wordpress';
  const symfony = p.source === 'symfony';
  const recipe = p.source === 'recipe';
  const sources = el('div', { class: 'chips' }, ...[['laravel', 'Laravel nuevo'], ['wordpress', 'WordPress'], ['symfony', 'Symfony'], ['git', 'Desde Git'], ['recipe', 'Plantilla']].map(([k, label]) =>
    el('span', { class: 'chip' + (p.source === k ? ' on' : ''), onclick: () => {
      p.source = k;
      if (k === 'git' && p.db === 'sqlite') p.db = '';
      if (k === 'laravel' && p.db === '') p.db = 'sqlite';
      // WordPress runs on MySQL or MariaDB only.
      if (k === 'wordpress' && !['mysql', 'mariadb'].includes(dbKind(p.db))) p.db = (projectDBOptions().find(([d]) => ['mysql', 'mariadb'].includes(dbKind(d))) || [''])[0];
      if (k !== 'wordpress' && p.source !== 'git' && !p.db) p.db = 'sqlite';
      if (k === 'recipe') { p.db = ''; loadRecipes(); }
      renderProjectDialog();
    } }, label)));
  let kits;
  if (fromGit) {
    const url = el('input', { type: 'text', value: p.git, placeholder: 'https://github.com/empresa/proyecto.git', autocomplete: 'off', spellcheck: 'false' });
    url.addEventListener('input', () => {
      p.git = url.value;
      // The repository's name is a good site name until the user writes another.
      const repo = (url.value.split(/[\\/]/).pop() || '').replace(/\.git$/i, '');
      if (!p.nameTouched && repo) { p.name = repo; name.value = repo; showPreview(); }
    });
    const branch = el('input', { type: 'text', value: p.branch, placeholder: 'la principal', autocomplete: 'off', spellcheck: 'false' });
    branch.addEventListener('input', () => { p.branch = branch.value; });
    kits = el('div', { class: 'field' }, el('span', {}, 'Repositorio'), url,
      el('div', { class: 'row' }, el('span', { class: 'svc-sub' }, 'Rama'), branch));
  } else if (recipe) {
    const list = ui.recipes || [];
    const usable = list.filter(r => !r.error);
    if (!p.recipe && usable.length) p.recipe = usable[0].id;
    const sel = el('select', { 'aria-label': 'Plantilla' }, ...usable.map(r => el('option', { value: r.id, selected: p.recipe === r.id }, r.name)));
    sel.addEventListener('change', () => { p.recipe = sel.value; renderProjectDialog(); });
    const chosen = usable.find(r => r.id === p.recipe);
    const broken = list.filter(r => r.error).map(r => el('div', { class: 'row-error' }, `${r.id}.toml: ${r.error}`));
    kits = el('div', { class: 'field' }, el('span', {}, 'Plantilla'),
      ui.recipes ? (usable.length ? sel : el('div', { class: 'svc-sub' }, 'No hay plantillas todavía.')) : el('div', { class: 'svc-sub' }, 'Cargando…'),
      chosen ? el('div', { class: 'svc-sub' }, recipeSummary(chosen)) : null, ...broken,
      el('div', { class: 'svc-sub' }, 'Son archivos .toml en config\\recetas, o un proyecto guardado con «⧉» en la lista de sitios.'));
  } else if (symfony) {
    const webapp = el('input', { type: 'checkbox', checked: p.webapp });
    webapp.addEventListener('change', () => { p.webapp = webapp.checked; renderProjectDialog(); });
    kits = el('div', { class: 'field' }, el('span', {}, 'Paquetes'),
      el('label', { class: 'check' }, webapp, el('span', {}, 'Aplicación web completa (webapp): Twig, Doctrine, formularios, seguridad y correo')),
      el('div', { class: 'svc-sub' }, 'Sin ella queda el esqueleto mínimo (symfony/skeleton), para una API o un microservicio. Composer elige la versión de Symfony que admite tu PHP.'));
  } else if (wordpress) {
    const locale = el('select', { 'aria-label': 'Idioma' }, ...WP_LOCALES.map(([k, label]) => el('option', { value: k, selected: p.locale === k }, label)));
    locale.addEventListener('change', () => { p.locale = locale.value; });
    kits = el('label', { class: 'field' }, el('span', {}, 'Idioma'), locale,
      el('div', { class: 'svc-sub' }, 'Nido descarga WordPress y su traducción, crea la base de datos y wp-config.php, e instala el sitio con un usuario admin y una contraseña generada.'));
  } else {
    kits = el('div', { class: 'field' }, el('span', {}, 'Kit de inicio'), el('div', { class: 'kits' }, ...KITS.map(([k, label, desc]) => {
      const radio = el('input', { type: 'radio', name: 'kit', value: k, checked: p.kit === k });
      radio.addEventListener('change', () => { p.kit = k; });
      return el('label', { class: 'kit' }, radio, el('span', {}, el('b', {}, label), el('span', { class: 'svc-sub' }, desc)));
    })));
  }
  let dbOptions = projectDBOptions();
  if (fromGit) dbOptions.unshift(['', 'La que diga el proyecto (.env.example)']);
  if (recipe) dbOptions.unshift(['', 'La que diga la plantilla']);
  if (wordpress) dbOptions = dbOptions.filter(([k]) => ['mysql', 'mariadb'].includes(dbKind(k)));
  // An older choice by kind ("mysql") becomes that kind's default instance.
  if (['mysql', 'mariadb', 'postgresql'].includes(p.db)) p.db = (dbOptions.find(([k]) => dbKind(k) === p.db) || [p.db])[0];
  const dbSel = el('select', { 'aria-label': 'Base de datos' }, ...options(dbOptions, p.db));
  dbSel.addEventListener('change', () => { p.db = dbSel.value; renderProjectDialog(); });
  // The database's name, its own user and sample data, when the project gets a database server.
  const server = p.db && p.db !== 'sqlite';
  const dbNameInput = el('input', { type: 'text', value: p.dbName, placeholder: slug(p.name || 'mi-proyecto').replace(/-/g, '_'), autocomplete: 'off', spellcheck: 'false' });
  dbNameInput.addEventListener('input', () => { p.dbName = dbNameInput.value; });
  const ownUser = el('input', { type: 'checkbox', checked: p.dbUser });
  ownUser.addEventListener('change', () => { p.dbUser = ownUser.checked; });
  const seed = el('input', { type: 'checkbox', checked: p.seed });
  seed.addEventListener('change', () => { p.seed = seed.checked; });
  const dbBox = el('div', { class: 'box' }, el('h4', {}, 'Base de datos'),
    el('div', { class: 'cols' },
      el('label', { class: 'field' }, el('span', {}, 'Dónde'), dbOptions.length ? dbSel
        : el('div', { class: 'row-error' }, 'WordPress necesita MySQL o MariaDB: instala uno en Servicios.')),
      server && !fromGit && !recipe ? el('label', { class: 'field' }, el('span', {}, 'Nombre de la base'), dbNameInput) : null),
    server && !fromGit && !recipe ? el('label', { class: 'check' }, ownUser, el('span', {}, 'Usuario propio para el proyecto, con una contraseña generada (si no, root sin contraseña)')) : null,
    !fromGit && !recipe && !wordpress && !symfony ? el('label', { class: 'check' }, seed, el('span', {}, 'Sembrar datos de ejemplo (los seeders de Laravel)')) : null);
  // The web server and PHP of the new site; FrankenPHP brings its own PHP.
  const serverSel = el('select', { 'aria-label': 'Servidor web' }, ...options(serverOptions(), p.server));
  serverSel.addEventListener('change', () => { p.server = serverSel.value; if (p.server === 'frankenphp') p.php = ''; renderProjectDialog(); });
  const phpSel = el('select', { 'aria-label': 'PHP', disabled: p.server === 'frankenphp' },
    ...options(p.server === 'frankenphp' ? [['', `el de FrankenPHP (${state.frankenphp_php})`]]
      : [['', `el global (${state.php})`], ...(state.php_versions || []).filter(v => v !== state.php).map(v => [v, `${v} (solo este sitio)`])], p.php));
  phpSel.addEventListener('change', () => { p.php = phpSel.value; });
  name.addEventListener('input', () => { p.nameTouched = true; });
  const pmSel = el('select', { 'aria-label': 'Gestor de paquetes' }, ...PMS.map(([k, label]) => el('option', { value: k, selected: (p.pm || 'npm') === k }, label)));
  pmSel.addEventListener('change', () => { p.pm = pmSel.value; });
  const pmField = fromGit || wordpress || symfony || recipe ? null : el('label', { class: 'field' }, el('span', {}, 'Paquetes de JavaScript'), pmSel);
  const envBox = el('div', { class: 'box' }, el('h4', {}, 'Entorno'),
    el('div', { class: 'cols' },
      el('label', { class: 'field' }, el('span', {}, 'Servidor web'), serverSel),
      el('label', { class: 'field' }, el('span', {}, 'PHP'), phpSel),
      pmField),
    el('div', { class: 'svc-sub' }, 'Se pueden cambiar después en la lista de sitios. pnpm y yarn se instalan solos si hacen falta.'));
  let rootField = null;
  if (roots.length > 1) {
    const rootSel = el('select', { 'aria-label': 'Carpeta' }, ...roots.map(r => el('option', { value: r, selected: (p.root || roots[0]) === r }, r)));
    rootSel.addEventListener('change', () => { p.root = rootSel.value; showPreview(); });
    rootField = el('label', { class: 'field' }, el('span', {}, 'Carpeta de proyectos'), rootSel);
  }
  // replaceChildren, unlike el, would show a null as the text "null".
  body.replaceChildren(...[
    sources,
    fromGit ? kits : null,
    el('label', { class: 'field' }, el('span', {}, 'Nombre'), name, preview),
    fromGit ? null : kits,
    envBox,
    symfony && !p.webapp ? null : dbBox,
    rootField,
    p.error ? el('div', { class: 'row-error' }, p.error) : null,
    el('div', { class: 'row' }, el('span', { class: 'spacer' }), el('button', { onclick: createProject }, fromGit ? 'Clonar y preparar' : 'Crear proyecto')),
  ].filter(Boolean));
}

async function createProject() {
  const p = ui.project;
  p.error = null;
  try {
    const body = { name: p.name, kit: p.kit, db: dbKind(p.db), pm: p.pm, root: p.root, server: p.server, php: p.php };
    if (p.db && p.db !== 'sqlite') Object.assign(body, { db_key: p.db, db_name: p.dbName.trim(), db_user: p.dbUser });
    if (p.seed && !['git', 'recipe', 'wordpress', 'symfony'].includes(p.source)) body.seed = true;
    if (p.source === 'git') Object.assign(body, { kit: '', git: p.git, branch: p.branch });
    if (p.source === 'wordpress') Object.assign(body, { type: 'wordpress', kit: '', pm: '', locale: p.locale });
    if (p.source === 'symfony') Object.assign(body, { type: 'symfony', kit: '', pm: '', webapp: p.webapp, db: p.webapp ? dbKind(p.db) : '' });
    if (p.source === 'recipe') Object.assign(body, { kit: '', pm: '', recipe: p.recipe });
    const j = await call('POST', '/api/projects', body);
    p.job = j.id;
    await refresh();
  } catch (e) {
    p.error = e.message;
  }
  renderProjectDialog();
}

// ---------- Plantillas (recetas) ----------

const RECIPE_SOURCES = { laravel: 'Laravel', wordpress: 'WordPress', symfony: 'Symfony', composer: 'paquete de Composer', git: 'repositorio Git', folder: 'proyecto guardado' };

function recipeSummary(r) {
  const s = r.source || {};
  const from = s.type === 'laravel' && s.kit ? `Laravel con ${s.kit}` : s.type === 'composer' ? `${RECIPE_SOURCES.composer} ${s.package}` : RECIPE_SOURCES[s.type] || s.type;
  const steps = (r.steps || []).length;
  return `${from}${r.database ? ' · ' + r.database : ''}${steps ? ` · ${steps} paso${steps > 1 ? 's' : ''}: ${r.steps.join(' · ')}` : ''}`;
}

async function loadRecipes() {
  try {
    ui.recipes = await call('GET', '/api/recipes');
  } catch (e) {
    ui.recipes = [];
    ui.project.error = e.message;
  }
  if (ui.projectOpen) renderProjectDialog();
}

async function saveTemplate(site) {
  const name = prompt(`Guardar ${site.host} como plantilla. Se copia sin vendor, node_modules ni .env.\nNombre de la plantilla:`, site.name);
  if (!name) return;
  try {
    const r = await call('POST', '/api/sites/template', { site: site.name, name });
    toast(`Plantilla «${r.name}» guardada: está en «Nuevo proyecto → Plantilla»`);
    ui.recipes = null;
  } catch (e) {
    alert(e.message);
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
  const all = [...DB_KINDS, ...OTHER_KINDS].map(groupOf);
  const groups = [apacheGroup(state), serverGroup(state, 'nginx'), serverGroup(state, 'frankenphp'), ...all.filter(isAvailable), adminerGroup(state)];
  const tiles = groups.map(g => {
    const job = (state.jobs || []).find(j => j.kind === g.kind && !j.finished);
    const cat = ui.catalog[g.kind];
    const n = g.instances.length;
    return el('div', { class: 'tile' + (job ? ' busy' : '') },
      el('div', { class: 'top' }, logo(g.kind),
        el('div', {}, el('div', { class: 'name' }, g.name, ' ', n ? el('span', { class: 'pill' }, n === 1 ? '1 instalada' : `${n} instaladas`) : null),
          el('div', { class: 'desc' }, { apache: 'Servidor web de los sitios', nginx: 'Servidor web · por sitio', frankenphp: 'Servidor web con su propio PHP · por sitio',
            adminer: 'Bases de datos en el navegador · db.localhost' }[g.kind] || KIND_INFO[g.kind].desc))),
      job ? jobLine(job, g.name) : el('div', { class: 'act' }, ...installPicker(g)),
      el('div', { class: 'src' }, cat && cat.length ? `Descarga de ${cat[0].source}` : ''));
  });
  const soon = all.filter(g => !isAvailable(g)).map(g => el('div', { class: 'tile soon' },
    el('div', { class: 'top' }, logo(g.kind), el('div', {}, el('div', { class: 'name' }, KIND_INFO[g.kind].name, ' ', el('span', { class: 'pill' }, 'próximamente')),
      el('div', { class: 'desc' }, KIND_INFO[g.kind].desc)))));
  document.getElementById('dialog-body').replaceChildren(...tiles, ...soon);
}

// ---------- arranque ----------

document.querySelectorAll('[data-action]').forEach(b => b.addEventListener('click', () => act('POST', '/api/' + b.dataset.action)));
document.querySelectorAll('.php-select').forEach(sel => sel.addEventListener('change', e => act('POST', '/api/php', { version: e.target.value })));
document.getElementById('filter').addEventListener('input', renderSites);
document.querySelectorAll('#tabs a').forEach(a => a.addEventListener('click', e => { e.preventDefault(); showTab(a.dataset.tab); }));
document.getElementById('dialog-close').addEventListener('click', closeDialog);
document.getElementById('ext-version').addEventListener('change', e => { ui.extVersion = e.target.value; loadExtensions(); });
document.getElementById('node-refresh').addEventListener('click', async e => {
  e.target.disabled = true;
  await loadNode(true);
  e.target.disabled = false;
  await refresh();
});
document.getElementById('new-project').addEventListener('click', openProjectDialog);
document.getElementById('project-close').addEventListener('click', closeProjectDialog);
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
// #nuevo opens «Nuevo proyecto» right away (the tray's «Nuevo proyecto…» links there).
showTab(firstTab === 'nuevo' ? 'sitios' : ['resumen', 'sitios', 'servicios', 'php', 'salud'].includes(firstTab) ? firstTab : 'resumen');
refresh().then(() => { if (firstTab === 'nuevo') openProjectDialog(); schedule(); });
