// Panel de clon: lee el estado del API local y envía las acciones con el token de la sesión.
const token = document.querySelector('meta[name="clon-token"]').content;
let state = null;
// Lo que el usuario está haciendo y debe sobrevivir a cada refresco del estado.
const ui = { tab: 'resumen', menu: null, filter: 'todos', dialog: false, catalog: {}, catalogError: {}, pending: {}, choice: {}, dismissed: {},
  ext: null, extVersion: '', extBusy: {} };

const INSTALLABLE = ['mysql', 'mariadb', 'postgresql', 'mongodb', 'redis', 'mailpit', 'meilisearch', 'apache', 'nginx', 'frankenphp', 'adminer'];
// Databases Adminer opens (its MongoDB support needs a PHP driver clon does not set up for it).
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
const LOGO = { apache: 'A', nginx: 'N', frankenphp: 'F', mysql: 'My', mariadb: 'Ma', postgresql: 'Pg', mongodb: 'Mo', redis: 'R', mailpit: '✉', meilisearch: 'Me', adminer: 'Ad' };

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
  if (tab === 'php') loadExtensions();
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
    alerts.append(alertBox('Windows todavía no confía en la CA local de clon: el navegador avisará en los sitios HTTPS.',
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
  for (const [what, msg] of Object.entries(s.problems || {})) {
    if (what === 'visual-c++') continue; // shown above, with its button
    alerts.append(alertBox(`${what}: ${firstLine(msg)}`, null, null, true));
  }
}

// renderProjectJobs shows the projects being created (from the panel or with 'clon new') above the sites.
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
        site ? el('a', { href: site.url, target: '_blank' }, 'Abrir ' + site.url + ' ↗') : null, el('span', { class: 'spacer' }), close);
    }
    return el('div', { class: 'job' }, el('span', {}, el('b', {}, j.series), ` (${j.version}): ${j.phase}…`),
      el('div', { class: 'progress indeterminate' }, el('span')),
      el('button', { class: 'small danger', onclick: () => act('POST', '/api/jobs/cancel', { id: j.id }) }, 'Cancelar'));
  }));
}

function renderSites() {
  if (!state) return;
  renderProjectJobs();
  const tbody = document.getElementById('sites');
  if (tbody.contains(document.activeElement) && document.activeElement.tagName === 'SELECT') return;
  const q = document.getElementById('filter').value.toLowerCase();
  const list = (state.sites || []).filter(x => !q || x.host.includes(q) || x.folder.toLowerCase().includes(q));
  tbody.replaceChildren(...list.map(x => el('tr', {},
    el('td', {}, el('a', { href: x.url, target: '_blank' }, x.host)),
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
};

// processToggles switches a site's processes (Vite, the queue, the scheduler); one that is on also
// starts with «Iniciar todo».
function processToggles(site) {
  if (!site.processes || !site.processes.length) return el('span', { class: 'svc-sub' }, '—');
  return el('div', { class: 'procs' }, ...site.processes.map(p => {
    const key = `proc-${site.name}-${p.id}`;
    const title = `${PROCESS_HELP[p.id]} · ${p.on ? p.state + ' (clic para detenerlo)' : 'clic para arrancarlo'}`;
    return el('button', { class: 'proc' + (p.on ? ' on' : '') + (p.state === 'en marcha' ? ' running' : ''), disabled: !!ui.pending[key], title,
      onclick: () => act('POST', '/api/sites/process', { site: site.name, id: p.id, on: !p.on }, key) }, p.label);
  }));
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
  // Xdebug for this site alone; clon fetches it for that PHP when it lacks it.
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
  if (i.source === 'laragon') notes.push('de Laragon');
  else if (i.source === 'download') notes.push('descargada');
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
    item(i.source === 'download' ? 'Desinstalar (conserva los datos)' : 'Quitar de clon (sus archivos no se tocan)', () => uninstall(g, i), 'red'));
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
    return el('label', { class: busy ? 'busy' : '', title: x.default ? 'clon la activa por defecto' : '' }, check,
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

// ---------- «Nuevo proyecto» ----------

const KITS = [
  ['', 'Sin kit', 'Laravel con Blade'],
  ['react', 'React', 'Inertia, React y Tailwind'],
  ['vue', 'Vue', 'Inertia, Vue y Tailwind'],
  ['svelte', 'Svelte', 'Inertia, Svelte y Tailwind'],
  ['livewire', 'Livewire', 'Livewire y Tailwind'],
];
ui.project = { name: '', kit: '', db: 'sqlite', root: '', job: null, error: null };

// slug turns a name into the site name the server will use ("Mi Tienda" → "mi-tienda").
const slug = s => s.toLowerCase().normalize('NFD').replace(/[̀-ͯ]/g, '')
  .replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 63);

function projectDBOptions() {
  const opts = [['sqlite', 'SQLite (un archivo dentro del proyecto)']];
  for (const kind of ['mysql', 'mariadb', 'postgresql']) {
    const g = groupOf(kind);
    const def = g.instances.find(i => i.default);
    if (def) opts.push([kind, `${g.name} ${def.version} (puerto ${def.port})`]);
  }
  return opts;
}

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
  const again = () => { ui.project = { name: '', kit: p.kit, db: p.db, root: p.root, job: null, error: null }; renderProjectDialog(); };
  if (job && job.finished && !job.error) {
    const site = (state.sites || []).find(s => s.name === job.series);
    body.replaceChildren(
      el('div', {}, el('b', {}, 'Listo. '), site ? el('a', { href: site.url, target: '_blank' }, 'Abrir ' + site.url + ' ↗') : job.series),
      el('div', { class: 'svc-sub' }, site ? 'Carpeta: ' + site.folder : ''),
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
    body.replaceChildren(el('div', { class: 'job' },
      el('span', {}, `${job.series} · ${job.version}: ${job.phase}…`),
      el('div', { class: 'progress indeterminate' }, el('span')),
      el('button', { class: 'small danger', onclick: () => act('POST', '/api/jobs/cancel', { id: job.id }) }, 'Cancelar')),
      el('div', { class: 'svc-sub' }, 'Puedes cerrar esta ventana: la creación sigue y el sitio aparece en la lista al terminar.'));
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
  const kits = el('div', { class: 'kits' }, ...KITS.map(([k, label, desc]) => {
    const radio = el('input', { type: 'radio', name: 'kit', value: k, checked: p.kit === k });
    radio.addEventListener('change', () => { p.kit = k; });
    return el('label', { class: 'kit' }, radio, el('span', {}, el('b', {}, label), el('span', { class: 'svc-sub' }, desc)));
  }));
  const dbSel = el('select', { 'aria-label': 'Base de datos' }, ...projectDBOptions().map(([k, label]) => el('option', { value: k, selected: p.db === k }, label)));
  dbSel.addEventListener('change', () => { p.db = dbSel.value; });
  let rootField = null;
  if (roots.length > 1) {
    const rootSel = el('select', { 'aria-label': 'Carpeta' }, ...roots.map(r => el('option', { value: r, selected: (p.root || roots[0]) === r }, r)));
    rootSel.addEventListener('change', () => { p.root = rootSel.value; showPreview(); });
    rootField = el('label', { class: 'field' }, el('span', {}, 'Carpeta de proyectos'), rootSel);
  }
  // replaceChildren, unlike el, would show a null as the text "null".
  body.replaceChildren(...[
    el('label', { class: 'field' }, el('span', {}, 'Nombre'), name, preview),
    el('div', { class: 'field' }, el('span', {}, 'Kit de inicio'), kits),
    el('label', { class: 'field' }, el('span', {}, 'Base de datos'), dbSel),
    rootField,
    p.error ? el('div', { class: 'row-error' }, p.error) : null,
    el('div', { class: 'row' }, el('span', { class: 'spacer' }), el('button', { onclick: createProject }, 'Crear proyecto')),
  ].filter(Boolean));
}

async function createProject() {
  const p = ui.project;
  p.error = null;
  try {
    const j = await call('POST', '/api/projects', { name: p.name, kit: p.kit, db: p.db, root: p.root });
    p.job = j.id;
    await refresh();
  } catch (e) {
    p.error = e.message;
  }
  renderProjectDialog();
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
showTab(['resumen', 'sitios', 'servicios', 'php'].includes(firstTab) ? firstTab : 'resumen');
refresh().then(schedule);
