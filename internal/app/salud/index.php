<?php
// Página de salud de Nido (https://salud.localhost): qué PHP corre, qué servicios hay y si cada
// conexión responde, incluida la base de datos de cada sitio. Lo detecta todo sola: pregunta a Nido
// por su API y prueba cada conexión con PHP, como lo haría un proyecto. Con ?formato=json responde
// para programas: HTTP 200 si todo está bien, 503 si algo falla.
// Nido la genera al arrancar: no la edites. Funciona con PHP 7.2 o más nuevo.

error_reporting(E_ALL);
ini_set('display_errors', '0');

$config = require __DIR__ . '/config.php';

function ms($start)
{
    return (int) round((microtime(true) - $start) * 1000);
}

function corto($text)
{
    $text = trim(preg_replace('/\s+/', ' ', (string) $text));
    return strlen($text) > 160 ? substr($text, 0, 157) . '…' : $text;
}

// Nido's status through its API: the port and the key are in run\control.json while it runs.
function estado_de_nido($controlFile)
{
    $raw = @file_get_contents($controlFile);
    $control = $raw === false ? null : json_decode($raw, true);
    if (!is_array($control) || empty($control['port']) || empty($control['token'])) {
        return [null, 'Nido no está corriendo (no se encontró ' . $controlFile . ')'];
    }
    $context = stream_context_create(['http' => [
        'method' => 'GET',
        'header' => 'X-Nido-Token: ' . $control['token'] . "\r\n",
        'timeout' => 5,
        'ignore_errors' => true,
    ]]);
    $body = @file_get_contents('http://127.0.0.1:' . (int) $control['port'] . '/api/status', false, $context);
    $status = $body === false ? null : json_decode($body, true);
    if (!is_array($status)) {
        return [null, 'Nido no respondió en su API (puerto ' . (int) $control['port'] . ')'];
    }
    return [$status, ''];
}

function probar_pdo($dsn, $user, $password, $versionSql, $countSql)
{
    $driver = strstr($dsn, ':', true);
    if (!class_exists('PDO') || !in_array($driver, PDO::getAvailableDrivers(), true)) {
        return ['ok' => false, 'detalle' => 'a este PHP le falta la extensión pdo_' . $driver];
    }
    $start = microtime(true);
    try {
        $pdo = new PDO($dsn, $user, $password, [PDO::ATTR_TIMEOUT => 3, PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION]);
        $parts = [];
        if ($versionSql !== '') {
            $parts[] = (string) $pdo->query($versionSql)->fetchColumn();
        }
        if ($countSql !== '') {
            $count = (int) $pdo->query($countSql)->fetchColumn();
            $parts[] = $count . ($count === 1 ? ' base' : ' bases');
        }
        $parts[] = ms($start) . ' ms';
        return ['ok' => true, 'detalle' => implode(' · ', $parts)];
    } catch (Throwable $e) {
        return ['ok' => false, 'detalle' => corto($e->getMessage())];
    }
}

function abrir_socket($port, &$error)
{
    $errno = 0;
    $errstr = '';
    $socket = @fsockopen('127.0.0.1', (int) $port, $errno, $errstr, 3);
    if (!$socket) {
        $error = corto($errstr !== '' ? $errstr : 'no acepta conexiones');
        return null;
    }
    stream_set_timeout($socket, 3);
    return $socket;
}

function probar_redis($port)
{
    $start = microtime(true);
    $error = '';
    $socket = abrir_socket($port, $error);
    if (!$socket) {
        return ['ok' => false, 'detalle' => $error];
    }
    fwrite($socket, "PING\r\n");
    $pong = trim((string) fgets($socket));
    $version = '';
    fwrite($socket, "INFO server\r\n");
    $head = trim((string) fgets($socket));
    if (strlen($head) > 1 && $head[0] === '$') {
        $info = (string) stream_get_contents($socket, (int) substr($head, 1));
        if (preg_match('/redis_version:(\S+)/', $info, $m)) {
            $version = $m[1] . ' · ';
        }
    }
    fclose($socket);
    if ($pong !== '+PONG') {
        return ['ok' => false, 'detalle' => 'respondió ' . corto($pong) . ' a PING'];
    }
    return ['ok' => true, 'detalle' => $version . 'PONG · ' . ms($start) . ' ms'];
}

function probar_smtp($port)
{
    $start = microtime(true);
    $error = '';
    $socket = abrir_socket($port, $error);
    if (!$socket) {
        return ['ok' => false, 'detalle' => $error];
    }
    $banner = trim((string) fgets($socket));
    fwrite($socket, "QUIT\r\n");
    fclose($socket);
    if (strpos($banner, '220') !== 0) {
        return ['ok' => false, 'detalle' => 'saludo SMTP inesperado: ' . corto($banner)];
    }
    return ['ok' => true, 'detalle' => 'SMTP listo · ' . ms($start) . ' ms'];
}

function probar_http($url)
{
    $start = microtime(true);
    $context = stream_context_create(['http' => ['timeout' => 3, 'ignore_errors' => true]]);
    $body = @file_get_contents($url, false, $context);
    if ($body === false) {
        return ['ok' => false, 'detalle' => 'no responde en ' . $url];
    }
    return ['ok' => true, 'detalle' => corto($body) . ' · ' . ms($start) . ' ms'];
}

function probar_tcp($port)
{
    $start = microtime(true);
    $error = '';
    $socket = abrir_socket($port, $error);
    if (!$socket) {
        return ['ok' => false, 'detalle' => $error];
    }
    fclose($socket);
    return ['ok' => true, 'detalle' => 'acepta conexiones · ' . ms($start) . ' ms'];
}

// One service instance of Nido, tried the way a project connects to it.
function probar_servicio($kind, $port)
{
    switch ($kind) {
        case 'mysql':
        case 'mariadb':
            return probar_pdo("mysql:host=127.0.0.1;port=$port", 'root', '', 'SELECT VERSION()',
                "SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME NOT IN ('mysql','information_schema','performance_schema','sys')");
        case 'postgresql':
            return probar_pdo("pgsql:host=127.0.0.1;port=$port;dbname=postgres", 'postgres', '', 'SHOW server_version',
                "SELECT COUNT(*) FROM pg_database WHERE NOT datistemplate AND datname <> 'postgres'");
        case 'redis':
            return probar_redis($port);
        case 'mailpit':
            return probar_smtp($port);
        case 'meilisearch':
            return probar_http("http://127.0.0.1:$port/health");
        default:
            return probar_tcp($port);
    }
}

// .env files: KEY=value lines, with optional quotes and # comments.
function leer_env($file)
{
    $values = [];
    $lines = @file($file, FILE_IGNORE_NEW_LINES | FILE_SKIP_EMPTY_LINES);
    foreach ($lines ?: [] as $line) {
        if (!preg_match('/^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$/', $line, $m)) {
            continue;
        }
        $value = trim($m[2]);
        if ($value !== '' && ($value[0] === '"' || $value[0] === "'")) {
            $quote = $value[0];
            $end = strpos($value, $quote, 1);
            $value = $end === false ? substr($value, 1) : substr($value, 1, $end - 1);
        } else {
            $value = trim(preg_replace('/\s+#.*$/', '', $value));
        }
        $values[$m[1]] = $value;
    }
    return $values;
}

// The database a site uses, read from its own settings: Laravel's .env, Symfony's DATABASE_URL or
// WordPress's wp-config.php. Null when it has none.
function base_del_sitio($folder)
{
    $env = file_exists("$folder/.env") ? leer_env("$folder/.env") : [];
    foreach (['.env.local', '.env.dev.local'] as $extra) {
        if (file_exists("$folder/$extra")) {
            $env = array_merge($env, leer_env("$folder/$extra"));
        }
    }
    if (!empty($env['DB_CONNECTION'])) {
        $driver = strtolower($env['DB_CONNECTION']);
        $db = [
            'driver' => $driver === 'mariadb' ? 'mysql' : $driver,
            'nombre' => $driver,
            'host' => isset($env['DB_HOST']) ? $env['DB_HOST'] : '127.0.0.1',
            'port' => isset($env['DB_PORT']) ? $env['DB_PORT'] : '',
            'database' => isset($env['DB_DATABASE']) ? $env['DB_DATABASE'] : '',
            'user' => isset($env['DB_USERNAME']) ? $env['DB_USERNAME'] : '',
            'password' => isset($env['DB_PASSWORD']) ? $env['DB_PASSWORD'] : '',
        ];
        if ($driver === 'sqlite' && ($db['database'] === '' || $db['database'] === 'laravel')) {
            $db['database'] = "$folder/database/database.sqlite";
        }
        return $db;
    }
    if (!empty($env['DATABASE_URL'])) {
        $url = str_replace('%kernel.project_dir%', $folder, $env['DATABASE_URL']);
        $parts = parse_url($url);
        if ($parts === false || empty($parts['scheme'])) {
            return null;
        }
        $scheme = strtolower($parts['scheme']);
        if ($scheme === 'sqlite') {
            $path = preg_replace('#^sqlite:/{2,3}#', '', $url);
            return ['driver' => 'sqlite', 'nombre' => 'sqlite', 'database' => $path, 'host' => '', 'port' => '', 'user' => '', 'password' => ''];
        }
        return [
            'driver' => in_array($scheme, ['postgresql', 'postgres', 'pgsql'], true) ? 'pgsql' : 'mysql',
            'nombre' => $scheme,
            'host' => isset($parts['host']) ? $parts['host'] : '127.0.0.1',
            'port' => isset($parts['port']) ? (string) $parts['port'] : '',
            'database' => isset($parts['path']) ? ltrim($parts['path'], '/') : '',
            'user' => isset($parts['user']) ? urldecode($parts['user']) : '',
            'password' => isset($parts['pass']) ? urldecode($parts['pass']) : '',
        ];
    }
    if (file_exists("$folder/wp-config.php")) {
        $text = (string) @file_get_contents("$folder/wp-config.php");
        $get = function ($name) use ($text) {
            return preg_match("/define\(\s*['\"]" . $name . "['\"]\s*,\s*['\"]([^'\"]*)['\"]/", $text, $m) ? $m[1] : '';
        };
        $host = $get('DB_HOST');
        $port = '';
        if (strpos($host, ':') !== false) {
            list($host, $port) = explode(':', $host, 2);
        }
        return ['driver' => 'mysql', 'nombre' => 'mysql', 'host' => $host ?: '127.0.0.1', 'port' => $port,
            'database' => $get('DB_NAME'), 'user' => $get('DB_USER'), 'password' => $get('DB_PASSWORD')];
    }
    return null;
}

function probar_base_del_sitio($db)
{
    switch ($db['driver']) {
        case 'mysql':
            $port = $db['port'] !== '' ? $db['port'] : '3306';
            return [$db['nombre'] . ' · ' . $db['database'] . ' en ' . $db['host'] . ':' . $port,
                probar_pdo("mysql:host={$db['host']};port=$port;dbname={$db['database']}", $db['user'], $db['password'], 'SELECT VERSION()', '')];
        case 'pgsql':
            $port = $db['port'] !== '' ? $db['port'] : '5432';
            return [$db['nombre'] . ' · ' . $db['database'] . ' en ' . $db['host'] . ':' . $port,
                probar_pdo("pgsql:host={$db['host']};port=$port;dbname={$db['database']}", $db['user'], $db['password'], 'SHOW server_version', '')];
        case 'sqlite':
            $label = 'sqlite · ' . basename($db['database']);
            if (!file_exists($db['database'])) {
                return [$label, ['ok' => false, 'detalle' => 'no existe ' . $db['database']]];
            }
            return [$label, probar_pdo('sqlite:' . $db['database'], null, null, 'SELECT sqlite_version()', '')];
    }
    return [$db['nombre'], ['ok' => false, 'detalle' => 'esta página no sabe probar ' . $db['nombre']]];
}

function revisar($config)
{
    $extensions = ['pdo_mysql', 'mysqli', 'pdo_pgsql', 'pdo_sqlite', 'openssl', 'curl', 'mbstring', 'intl', 'gd',
        'zip', 'fileinfo', 'sodium', 'bcmath', 'exif', 'Zend OPcache', 'xdebug', 'redis', 'mongodb'];
    $loaded = [];
    foreach ($extensions as $ext) {
        $loaded[$ext === 'Zend OPcache' ? 'opcache' : $ext] = extension_loaded($ext);
    }
    $report = [
        'ok' => true,
        'nido' => $config['nido'],
        'momento' => time(),
        'php' => [
            'version' => PHP_VERSION,
            'arquitectura' => (PHP_INT_SIZE * 8) . ' bits ' . (ZEND_THREAD_SAFE ? 'TS' : 'NTS'),
            'ini' => (string) php_ini_loaded_file(),
            'memoria' => (string) ini_get('memory_limit'),
            'extensiones' => $loaded,
            'otras' => [],
        ],
        'error' => '',
        'servicios' => [],
        'sitios' => [],
        'resumen' => ['ok' => 0, 'total' => 0],
    ];
    list($status, $error) = estado_de_nido($config['control']);
    if ($status === null) {
        $report['ok'] = false;
        $report['error'] = $error;
        return $report;
    }
    $count = function ($ok) use (&$report) {
        $report['resumen']['total']++;
        if ($ok) {
            $report['resumen']['ok']++;
        } else {
            $report['ok'] = false;
        }
    };
    foreach (isset($status['php_pools']) ? $status['php_pools'] : [] as $pool) {
        if (empty($pool['global'])) {
            $report['php']['otras'][] = $pool['version'] . (empty($pool['debug']) ? '' : ' con Xdebug');
        }
    }
    foreach (isset($status['databases']) ? $status['databases'] : [] as $group) {
        foreach ($group['instances'] as $instance) {
            $row = [
                'servicio' => $group['name'] . ' ' . $instance['series'],
                'clave' => $instance['key'],
                'direccion' => '127.0.0.1:' . $instance['port'],
                'estado' => 'apagado',
                'detalle' => $instance['default'] ? 'predeterminado, apagado' : 'apagado',
            ];
            if ($instance['state'] === 'en marcha') {
                $result = probar_servicio($group['kind'], $instance['port']);
                $row['estado'] = $result['ok'] ? 'ok' : 'fallo';
                $row['detalle'] = $result['detalle'];
                $count($result['ok']);
            } elseif ($instance['state'] === 'error') {
                $row['estado'] = 'fallo';
                $row['detalle'] = corto(isset($instance['error']) ? $instance['error'] : 'no arrancó');
                $count(false);
            } elseif ($instance['state'] === 'iniciando') {
                $row['estado'] = 'iniciando';
                $row['detalle'] = 'arrancando…';
            }
            $report['servicios'][] = $row;
        }
    }
    foreach (isset($status['sites']) ? $status['sites'] : [] as $site) {
        $row = [
            'sitio' => $site['host'],
            'url' => isset($site['url']) ? $site['url'] : '',
            'php' => !empty($site['php']) ? $site['php'] : $status['php'] . ' (global)',
            'servidor' => isset($site['server']) ? $site['server'] : '',
            'bd' => '',
            'estado' => 'sin-bd',
            'detalle' => 'no usa base de datos',
        ];
        if ($row['servidor'] === 'frankenphp' && !empty($status['frankenphp_php'])) {
            $row['php'] = $status['frankenphp_php'] . ' (el de FrankenPHP)';
        }
        $db = base_del_sitio($site['folder']);
        if ($db !== null) {
            list($label, $result) = probar_base_del_sitio($db);
            $row['bd'] = $label;
            $row['estado'] = $result['ok'] ? 'ok' : 'fallo';
            $row['detalle'] = $result['detalle'];
            $count($result['ok']);
        }
        $report['sitios'][] = $row;
    }
    return $report;
}

$report = revisar($config);

if (isset($_GET['formato']) && $_GET['formato'] === 'json') {
    http_response_code($report['ok'] ? 200 : 503);
    header('Content-Type: application/json; charset=utf-8');
    header('Cache-Control: no-store');
    echo json_encode($report, JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE | JSON_INVALID_UTF8_SUBSTITUTE);
    return;
}
header('Cache-Control: no-store');
?>
<!DOCTYPE html>
<html lang="es">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Salud · Nido</title>
<link rel="icon" href="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 64 64'%3E%3Crect x='2' y='2' width='60' height='60' rx='16' fill='%23D97706'/%3E%3Cpath d='M23 14C17 14 17 18 17 24v4c0 3-2 4-5 4 3 0 5 1 5 4v4c0 6 0 10 6 10M41 14c6 0 6 4 6 10v4c0 3 2 4 5 4-3 0-5 1-5 4v4c0 6 0 10-6 10' fill='none' stroke='%23FFF7ED' stroke-width='4.5' stroke-linecap='round'/%3E%3Ccircle cx='25.6' cy='38' r='4' fill='%23FFF7ED'/%3E%3Ccircle cx='32' cy='34.5' r='4' fill='%23FFF7ED'/%3E%3Ccircle cx='38.4' cy='38' r='4' fill='%23FFF7ED'/%3E%3C/svg%3E">
<style>
  :root { --fg: #1c1917; --muted: #78716c; --line: #e7e5e4; --bg: #fafaf9; --brand: #b45309; --brand-dark: #7c2d12;
          --brand-soft: #fff7ed; --brand-line: #fed7aa; --ok: #166534; --ok-soft: #dcfce7; --bad: #991b1b; --bad-soft: #fee2e2; }
  * { box-sizing: border-box; }
  body { margin: 0; background: var(--bg); color: var(--fg); font: 14px/1.5 "Segoe UI", system-ui, sans-serif; }
  header { display: flex; align-items: center; gap: 14px; padding: 16px 28px; background: var(--brand-soft);
           border-bottom: 1px solid var(--brand-line); }
  header svg { width: 40px; height: 40px; flex: none; }
  header h1 { margin: 0; font-size: 20px; color: var(--brand-dark); }
  header .sub { color: #9a3412; font-size: 12.5px; }
  .overall { margin-left: auto; font-weight: 700; padding: 7px 16px; border-radius: 999px; white-space: nowrap; }
  .overall.ok { background: var(--ok-soft); color: var(--ok); }
  .overall.bad { background: var(--bad-soft); color: var(--bad); }
  main { padding: 20px 28px 28px; display: grid; grid-template-columns: 300px 1fr; gap: 24px; max-width: 1280px; }
  h2 { margin: 0 0 8px; font-size: 12.5px; text-transform: uppercase; letter-spacing: .5px; color: var(--muted); }
  .card { background: #fff; border: 1px solid var(--line); border-radius: 12px; padding: 14px 16px; }
  .php-version { font-size: 28px; font-weight: 700; line-height: 1.2; }
  .meta { color: var(--muted); font-size: 12.5px; margin: 4px 0 10px; word-break: break-all; }
  .exts { display: flex; flex-wrap: wrap; gap: 5px; }
  .ext { font-size: 11.5px; padding: 1px 8px; border-radius: 6px; background: #f5f5f4; color: var(--muted); }
  .ext.on { background: var(--ok-soft); color: var(--ok); }
  .note { color: var(--muted); font-size: 12.5px; margin-top: 10px; }
  table { width: 100%; border-collapse: collapse; background: #fff; border: 1px solid var(--line); border-radius: 12px;
          overflow: hidden; margin-bottom: 22px; }
  th { text-align: left; font-size: 12px; color: var(--muted); font-weight: 600; padding: 8px 12px; background: #fafaf9;
       border-bottom: 1px solid var(--line); }
  td { padding: 8px 12px; border-bottom: 1px solid #f5f5f4; vertical-align: top; }
  tr:last-child td { border-bottom: none; }
  td code { font: 12.5px Consolas, monospace; color: #57534e; }
  td.detalle { color: #57534e; font-size: 13px; }
  td.php { white-space: nowrap; }
  .st { font-weight: 700; font-size: 12px; padding: 2px 10px; border-radius: 999px; white-space: nowrap; }
  .st.ok { background: var(--ok-soft); color: var(--ok); }
  .st.fallo { background: var(--bad-soft); color: var(--bad); }
  .st.apagado, .st.sin-bd, .st.iniciando { background: #f5f5f4; color: var(--muted); }
  .error { background: var(--bad-soft); color: var(--bad); border-radius: 12px; padding: 14px 16px; font-weight: 600; }
  .empty { color: var(--muted); padding: 10px 2px 20px; }
  footer { padding: 0 28px 24px; color: var(--muted); font-size: 12.5px; }
  footer code { font: 12.5px Consolas, monospace; background: #f5f5f4; padding: 1px 6px; border-radius: 6px; }
  a { color: var(--brand); }
  @media (max-width: 860px) { main { grid-template-columns: 1fr; } }
</style>
</head>
<body>
<header>
  <svg viewBox="0 0 64 64" aria-hidden="true">
    <defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#F59E0B"/><stop offset="1" stop-color="#B45309"/></linearGradient></defs>
    <rect x="2" y="2" width="60" height="60" rx="16" fill="url(#g)"/>
    <path d="M23 14 C17 14 17 18 17 24 L17 28 C17 31 15 32 12 32 C15 32 17 33 17 36 L17 40 C17 46 17 50 23 50" fill="none" stroke="#FFF7ED" stroke-width="4.5" stroke-linecap="round" stroke-linejoin="round"/>
    <path d="M41 14 C47 14 47 18 47 24 L47 28 C47 31 49 32 52 32 C49 32 47 33 47 36 L47 40 C47 46 47 50 41 50" fill="none" stroke="#FFF7ED" stroke-width="4.5" stroke-linecap="round" stroke-linejoin="round"/>
    <ellipse cx="25.6" cy="38" rx="3.7" ry="4.6" fill="#FFF7ED"/><ellipse cx="32" cy="34.5" rx="3.7" ry="4.6" fill="#FFF7ED"/><ellipse cx="38.4" cy="38" rx="3.7" ry="4.6" fill="#FFF7ED"/>
  </svg>
  <div>
    <h1>Salud del entorno</h1>
    <div class="sub">Nido lo detecta todo solo y lo vuelve a comprobar cada 5 segundos · <span id="hora"></span></div>
  </div>
  <span id="overall" class="overall"></span>
</header>
<main>
  <section>
    <h2>PHP que corre esta página</h2>
    <div class="card" id="php"></div>
  </section>
  <section>
    <div id="error"></div>
    <h2>Bases de datos y servicios</h2>
    <div id="servicios"></div>
    <h2>Sitios y la base de datos que usan</h2>
    <div id="sitios"></div>
  </section>
</main>
<footer>Para vigilarlo desde otro programa: <code>https://salud.localhost/?formato=json</code> responde HTTP 200 si todo está bien y 503 si algo falla.</footer>
<script>
const inicial = <?= json_encode($report, JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE | JSON_INVALID_UTF8_SUBSTITUTE | JSON_HEX_TAG | JSON_HEX_AMP) ?>;
const ESTADO = { ok: 'OK', fallo: 'FALLO', apagado: 'apagado', iniciando: 'iniciando', 'sin-bd': '—' };
const esc = s => String(s == null ? '' : s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const chip = e => `<span class="st ${esc(e)}">${esc(ESTADO[e] || e)}</span>`;

function render(r) {
  const overall = document.getElementById('overall');
  const total = r.resumen.total;
  overall.className = 'overall ' + (r.ok ? 'ok' : 'bad');
  overall.textContent = r.error ? '✗ Nido no responde'
    : r.ok ? `✓ Todo OK · ${r.resumen.ok} de ${total}` : `✗ ${total - r.resumen.ok} con problemas · ${r.resumen.ok} de ${total} OK`;
  document.title = (r.ok ? '✓ ' : '✗ ') + 'Salud · Nido';
  document.getElementById('hora').textContent = 'última comprobación ' + new Date(r.momento * 1000).toLocaleTimeString();

  const p = r.php;
  const exts = Object.entries(p.extensiones).map(([n, on]) => `<span class="ext${on ? ' on' : ''}">${esc(n)}</span>`).join('');
  document.getElementById('php').innerHTML =
    `<div class="php-version">PHP ${esc(p.version)}</div>` +
    `<div class="meta">${esc(p.arquitectura)} · memoria ${esc(p.memoria)}<br>${esc(p.ini || 'sin php.ini')}</div>` +
    `<div class="exts">${exts}</div>` +
    (p.otras.length ? `<div class="note">También en marcha para sitios fijados: PHP ${p.otras.map(esc).join(', ')}.</div>` : '') +
    `<div class="note">Nido ${esc(r.nido)}</div>`;

  document.getElementById('error').innerHTML = r.error ? `<p class="error">${esc(r.error)}</p>` : '';

  document.getElementById('servicios').innerHTML = r.servicios.length
    ? '<table><tr><th>Servicio</th><th>Dirección</th><th>Respuesta</th><th>Estado</th></tr>' +
      r.servicios.map(s => `<tr><td>${esc(s.servicio)}</td><td><code>${esc(s.direccion)}</code></td>` +
        `<td class="detalle">${esc(s.detalle)}</td><td>${chip(s.estado)}</td></tr>`).join('') + '</table>'
    : `<p class="empty">${r.error ? '' : 'Todavía no hay ninguna base de datos ni servicio instalado (se instalan en el panel, en «Servicios»).'}</p>`;

  document.getElementById('sitios').innerHTML = r.sitios.length
    ? '<table><tr><th>Sitio</th><th>PHP</th><th>Base de datos</th><th>Respuesta</th><th>Estado</th></tr>' +
      r.sitios.map(s => `<tr><td>${s.url ? `<a href="${esc(s.url)}">${esc(s.sitio)}</a>` : esc(s.sitio)}</td><td class="php">${esc(s.php)}</td>` +
        `<td>${s.bd ? `<code>${esc(s.bd)}</code>` : ''}</td><td class="detalle">${esc(s.detalle)}</td><td>${chip(s.estado)}</td></tr>`).join('') + '</table>'
    : `<p class="empty">${r.error ? '' : 'Todavía no hay sitios: cada carpeta en la carpeta de proyectos es uno.'}</p>`;
}

async function refrescar() {
  try {
    const res = await fetch('?formato=json', { cache: 'no-store' });
    render(await res.json());
  } catch (e) {
    const overall = document.getElementById('overall');
    overall.className = 'overall bad';
    overall.textContent = '✗ Nido no responde';
  }
}

render(inicial);
setInterval(refrescar, 5000);
</script>
</body>
</html>
