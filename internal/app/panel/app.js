// Panel de clon: lee el estado del API local y envía las acciones con el token de la sesión.
const token = document.querySelector('meta[name="clon-token"]').content;
let state = null;

async function call(method, path, body) {
  const res = await fetch(path, {
    method,
    headers: { 'X-Clon-Token': token, 'Content-Type': 'application/json' },
    body: body ? JSON.stringify(body) : undefined,
  });
  const data = await res.json();
  if (!res.ok) throw new Error(data.error || res.statusText);
  return data;
}

function el(tag, attrs = {}, ...children) {
  const n = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === 'class') n.className = v; else if (k.startsWith('on')) n.addEventListener(k.slice(2), v); else n.setAttribute(k, v);
  }
  for (const c of children) n.append(c);
  return n;
}

function alertBox(text, buttonText, action, bad) {
  const box = el('div', { class: 'alert' + (bad ? ' bad' : '') }, el('span', {}, text));
  if (buttonText) box.append(el('button', { onclick: action }, buttonText));
  return box;
}

function render() {
  const s = state;
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
    alerts.append(alertBox(`${what}: ${msg}`, null, null, true));
  }

  const php = document.getElementById('php');
  php.replaceChildren(...(s.php_versions || []).map(v => el('option', v === s.php ? { value: v, selected: '' } : { value: v }, 'PHP ' + v)));
  document.getElementById('node').textContent = s.node ? 'v' + s.node : 'no instalado';

  const services = document.getElementById('services');
  services.replaceChildren(...(s.services || []).map(sv => {
    const cls = sv.state === 'en marcha' ? 'ok' : (sv.state === 'error' ? 'bad' : '');
    return el('li', { title: sv.last_error || '' }, el('span', {}, sv.name + (sv.port ? ` :${sv.port}` : '')), el('span', { class: 'state ' + cls }, sv.state));
  }));

  renderSites();
  document.getElementById('footer').textContent =
    `Instalación: ${s.home} · Apache ${s.apache || '—'} · MySQL ${s.mysql || '—'} (puerto ${s.mysql_port}) · proxy ${s.http_port}/${s.https_port}`;
}

function renderSites() {
  const q = document.getElementById('filter').value.toLowerCase();
  const list = (state.sites || []).filter(x => !q || x.host.includes(q) || x.folder.toLowerCase().includes(q));
  document.getElementById('site-count').textContent = `(${state.sites ? state.sites.length : 0})`;
  document.getElementById('sites').replaceChildren(...list.map(x => el('tr', {},
    el('td', {}, el('a', { href: x.url, target: '_blank' }, x.host)),
    el('td', {}, x.kind),
    el('td', { class: 'folder' }, x.doc_root),
    el('td', {}, String(x.port)),
  )));
}

async function act(method, path, body) {
  document.querySelectorAll('button').forEach(b => b.disabled = true);
  try {
    const out = await call(method, path, body);
    if (out && out.sites) state = out;
  } catch (e) {
    alert(e.message);
  } finally {
    document.querySelectorAll('button').forEach(b => b.disabled = false);
    await refresh();
  }
}

async function refresh() {
  try {
    state = await call('GET', '/api/status');
    render();
  } catch (e) {
    document.getElementById('alerts').replaceChildren(alertBox('No se pudo leer el estado: ' + e.message, null, null, true));
  }
}

document.querySelectorAll('[data-action]').forEach(b => b.addEventListener('click', () => act('POST', '/api/' + b.dataset.action)));
document.getElementById('php').addEventListener('change', e => act('POST', '/api/php', { version: e.target.value }));
document.getElementById('filter').addEventListener('input', renderSites);
refresh();
setInterval(refresh, 5000);
