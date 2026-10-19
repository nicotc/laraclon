# Prueba de Nido de punta a punta en un Windows limpio: la VM de Hyper-V de crear-vm.ps1.
# La lanza probar-en-vm.ps1, que recoge los resultados de C:\resultados.
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$R = 'C:\resultados'
$H = 'C:\nido'
$nido = Join-Path $H 'nido.exe'
$shims = Join-Path $H 'shims'
$report = New-Object System.Collections.ArrayList
$utf8 = New-Object System.Text.UTF8Encoding $false
$Host.UI.RawUI.WindowTitle = 'Nido - prueba en la VM'
Start-Transcript -Path (Join-Path $R 'transcript.txt') -Force | Out-Null
Write-Host 'Prueba automática de Nido: tarda alrededor de una hora.' -ForegroundColor Yellow
Write-Host 'No cierres esta ventana hasta que aparezca "Prueba terminada".' -ForegroundColor Yellow
$stepNo = 0

function Save-Report {
    [IO.File]::WriteAllText((Join-Path $R 'report.json'), ($report | ConvertTo-Json -Depth 4), $utf8)
}

function Step([string]$name, [scriptblock]$body) {
    $script:stepNo++
    $Host.UI.RawUI.WindowTitle = "Nido - paso $($script:stepNo): $name"
    Write-Host ''
    Write-Host "=== $name ===" -ForegroundColor Cyan
    $sw = [Diagnostics.Stopwatch]::StartNew()
    $ok = $true
    try { $detail = (& $body | Out-String).Trim() } catch { $ok = $false; $detail = $_.Exception.Message }
    $sw.Stop()
    $secs = [math]::Round($sw.Elapsed.TotalSeconds, 1)
    [void]$report.Add([pscustomobject]@{ paso = $name; ok = $ok; segundos = $secs; detalle = $detail })
    if ($ok) { Write-Host "OK ($secs s)" -ForegroundColor Green } else { Write-Host "FALLO ($secs s)" -ForegroundColor Red }
    if ($detail) { Write-Host $detail }
    Save-Report
}

# Runs a program with a time limit and returns its output; throws on timeout or non-zero exit.
function Invoke-Tool([string]$exe, [string]$arguments, [int]$seconds = 300, [string]$dir = 'C:\') {
    $out = [IO.Path]::GetTempFileName()
    $err = [IO.Path]::GetTempFileName()
    $p = Start-Process -FilePath $exe -ArgumentList $arguments -WorkingDirectory $dir -NoNewWindow -PassThru `
        -RedirectStandardOutput $out -RedirectStandardError $err
    $null = $p.Handle # without an open handle, Windows PowerShell 5.1 loses ExitCode
    if (-not $p.WaitForExit($seconds * 1000)) {
        try { $p.Kill() } catch {}
        throw "$([IO.Path]::GetFileName($exe)) $arguments tardó más de $seconds s"
    }
    $p.WaitForExit()
    $text = ("$(Get-Content $out -Raw -Encoding UTF8)" + "$(Get-Content $err -Raw -Encoding UTF8)").Trim()
    if ($p.ExitCode -ne 0) { throw "código $($p.ExitCode): $text" }
    return $text
}

# Not named Curl: in Windows PowerShell 5.1 the alias curl (Invoke-WebRequest) wins over functions.
function Invoke-Curl([string]$arguments) {
    $text = & curl.exe -s --ssl-no-revoke --max-time 60 @($arguments -split ' ') 2>&1 | Out-String
    return $text.Trim()
}

Step 'Entorno de Windows' {
    # Windows and Edge updating themselves in the middle of a run made test machines die or restart
    # (Windows Sandbox: 0x80370106); a machine that goes back to a checkpoint needs neither.
    try {
        $edgeUpdate = 'HKLM:\SOFTWARE\Policies\Microsoft\EdgeUpdate'
        New-Item -Path $edgeUpdate -Force | Out-Null
        New-ItemProperty -Path $edgeUpdate -Name UpdateDefault -Value 0 -PropertyType DWord -Force | Out-Null
        New-ItemProperty -Path $edgeUpdate -Name AutoUpdateCheckPeriodMinutes -Value 0 -PropertyType DWord -Force | Out-Null
        Get-ScheduledTask -TaskName 'MicrosoftEdgeUpdate*' -ErrorAction SilentlyContinue | Disable-ScheduledTask -ErrorAction SilentlyContinue | Out-Null
        Stop-Service wuauserv -Force -ErrorAction SilentlyContinue
        Set-Service wuauserv -StartupType Disabled -ErrorAction SilentlyContinue
    } catch {}
    $os = Get-CimInstance Win32_OperatingSystem
    $script:admin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole(
        [Security.Principal.WindowsBuiltInRole]::Administrator)
    $script:vcBefore = @('vcruntime140.dll', 'vcruntime140_1.dll', 'msvcp140.dll' | Where-Object {
        -not (Test-Path "$env:SystemRoot\System32\$_") }).Count -eq 0
    "Windows $($os.Caption) build $($os.BuildNumber) · usuario $env:USERNAME · administrador: $script:admin · runtime de Visual C++ presente: $script:vcBefore"
}

Step 'Copiar Nido a C:\nido' {
    New-Item -ItemType Directory -Force $H | Out-Null
    Copy-Item 'C:\nido-dist\*.exe' $H
    (Get-ChildItem $H -Filter *.exe | ForEach-Object { "$($_.Name) $([math]::Round($_.Length / 1MB, 1)) MB" }) -join ', '
}

if (-not $script:vcBefore) {
    Step 'nido vcredist install (instalador oficial de Microsoft, firma comprobada)' {
        (Invoke-Tool $nido 'vcredist install C:\nido-dist\vc_redist.x64.exe' 900) + "`n" + (Invoke-Tool $nido 'vcredist status' 60)
    }
}

# A Windows with nothing installed: everything Nido needs comes from the official sources.
Step 'nido setup en un Windows sin nada: PHP, Apache, MySQL 8.4, Node LTS y Composer' {
    $out = Invoke-Tool $nido 'setup' 2400
    $list = Invoke-Tool $nido 'versions' 60
    foreach ($c in 'PHP', 'Apache', 'MySQL', 'Node') {
        if ($list -notmatch "(?m)^[* ]\s*$c\s") { throw "nido setup no dejó $c instalado:`n$out`n$list" }
    }
    "$((($out -split "`n") | Where-Object { $_ -notmatch ' MB de ' } | Select-Object -Last 3) -join "`n")`n$list"
}

Step 'PHP 8.4, 8.3 y 7.2 de windows.php.net (7.2 del archivo de versiones anteriores)' {
    $lines = foreach ($v in '8.4', '8.3', '7.2') {
        # The progress line ends with a carriage return, so "Instalado" may share its line.
        (Invoke-Tool $nido "php install $v" 900) -split "[`r`n]+" | Where-Object { $_ -match '^\s*Instalado' } | ForEach-Object { $_.Trim() }
    }
    Invoke-Tool $nido 'php use 8.4' 120 | Out-Null
    "$($lines -join "`n")`n$(Invoke-Tool $nido 'php list' 60)"
}

Step 'Carpeta de proyectos C:\nido\www, puertos 80/443 y MySQL 8.4 en el 3306' {
    $cfg = [IO.File]::ReadAllText("$H\config\nido.toml")
    if ($cfg -notmatch 'http_port = 80\b' -or $cfg -notmatch 'https_port = 443\b') { throw "el proxy no usa 80/443:`n$cfg" }
    if ($cfg -notmatch 'nido\\\\www' -or -not (Test-Path "$H\www")) { throw "la carpeta de proyectos no es $H\www:`n$cfg" }
    $svc = Invoke-Tool $nido 'service list' 60
    if ($svc -notmatch 'mysql-8\.4\s+\S+\s+puerto\s+3306') { throw "MySQL 8.4 no quedó en el 3306:`n$svc" }
    Invoke-Tool $nido 'shims' 120
}

Step 'Sitio de prueba demo.test' {
    New-Item -ItemType Directory -Force 'C:\nido\www\demo\public' | Out-Null
    $php = @'
<?php
header('Content-Type: application/json; charset=utf-8');
try {
    $pdo = new PDO('mysql:host=127.0.0.1;port=3306', 'root', '', [PDO::ATTR_TIMEOUT => 3]);
    $mysql = $pdo->query('SELECT VERSION()')->fetchColumn();
} catch (Throwable $e) {
    $mysql = 'error: ' . $e->getMessage();
}
echo json_encode(['php' => PHP_VERSION, 'https' => $_SERVER['HTTPS'] ?? null, 'host' => $_SERVER['HTTP_HOST'] ?? null,
    'mysql' => $mysql, 'php_ini' => php_ini_loaded_file()], JSON_UNESCAPED_SLASHES);
'@
    [IO.File]::WriteAllText('C:\nido\www\demo\public\index.php', $php, $utf8)
    # Plain HTML for the browser check (Edge shows JSON through its own viewer).
    $html = '<!doctype html><title>Nido</title><p id="https"><?= $_SERVER[''HTTPS''] ?? ''off'' ?></p>'
    [IO.File]::WriteAllText('C:\nido\www\demo\public\estado.php', $html, $utf8)
    'C:\nido\www\demo\public\index.php y estado.php'
}

Step 'Proyecto Laravel nuevo (composer create-project laravel/laravel)' {
    $out = Invoke-Tool "$shims\composer.exe" 'create-project laravel/laravel C:\nido\www\laravel-demo --prefer-dist --no-interaction --no-progress' 1200
    $envFile = 'C:\nido\www\laravel-demo\.env'
    $t = [IO.File]::ReadAllText($envFile)
    $t = $t -replace '(?m)^APP_URL=.*$', 'APP_URL=https://laravel-demo.test'
    $t = $t -replace '(?m)^DB_CONNECTION=.*$', 'DB_CONNECTION=mysql'
    $t = $t -replace '(?m)^#?\s*DB_HOST=.*$', 'DB_HOST=127.0.0.1'
    $t = $t -replace '(?m)^#?\s*DB_PORT=.*$', 'DB_PORT=3306'
    $t = $t -replace '(?m)^#?\s*DB_DATABASE=.*$', 'DB_DATABASE=laravel_demo'
    $t = $t -replace '(?m)^#?\s*DB_USERNAME=.*$', 'DB_USERNAME=root'
    $t = $t -replace '(?m)^#?\s*DB_PASSWORD=.*$', 'DB_PASSWORD='
    [IO.File]::WriteAllText($envFile, $t, $utf8)
    $version = Invoke-Tool "$shims\php.exe" 'artisan --version' 120 'C:\nido\www\laravel-demo'
    "$version`n" + (($out -split "`n") | Select-Object -Last 6) -join "`n"
}

Step 'npm install y npm run build (Vite) en el proyecto Laravel' {
    $i = Invoke-Tool "$shims\npm.exe" 'install --no-audit --no-fund' 1200 'C:\nido\www\laravel-demo'
    $b = Invoke-Tool "$shims\npm.exe" 'run build' 600 'C:\nido\www\laravel-demo'
    ((($i -split "`n") | Select-Object -Last 2) + (($b -split "`n") | Select-Object -Last 4)) -join "`n"
}

Step 'Confiar en la CA local (nido ca trust)' {
    Write-Host 'Si Windows muestra un aviso para instalar el certificado "Nido CA local", pulsa Sí.' -ForegroundColor Yellow
    $p = Start-Process $nido -ArgumentList 'ca trust' -NoNewWindow -PassThru
    $null = $p.Handle
    if ($p.WaitForExit(120000) -and $p.ExitCode -eq 0) { return 'aceptada con nido ca trust (almacén del usuario)' }
    try { $p.Kill() } catch {}
    Get-Process certutil -ErrorAction SilentlyContinue | Stop-Process -Force
    if (-not $script:admin) { throw 'nadie confirmó el aviso y la sesión no es de administrador para el plan B' }
    $out = & certutil.exe -addstore Root "$H\ssl\nido-ca.crt" 2>&1 | Out-String
    if ($LASTEXITCODE -ne 0) { throw "certutil: $out" }
    'el aviso no se confirmó a tiempo; plan B: CA añadida al almacén de la máquina con certutil'
}

Step 'Archivo hosts (nido hosts sync)' {
    $p = Start-Process $nido -ArgumentList 'hosts sync' -NoNewWindow -PassThru
    $null = $p.Handle
    if ($p.WaitForExit(120000) -and $p.ExitCode -eq 0) {
        $how = 'nido hosts sync (elevación de Windows)'
    } else {
        try { $p.Kill() } catch {}
        if (-not $script:admin) { throw 'no se concedió el permiso y la sesión no es de administrador para el plan B' }
        Invoke-Tool $nido "hosts-apply $H\run\hosts-names.txt" 60 | Out-Null
        $how = 'plan B: nido hosts-apply directo (la sesión ya es de administrador)'
    }
    $lines = Select-String -Path "$env:SystemRoot\System32\drivers\etc\hosts" -Pattern 'Nido|\.test' | ForEach-Object { $_.Line.Trim() }
    "$how`n" + ($lines -join "`n")
}

# Starts Nido with its tray icon and waits until Apache, MySQL and the PHP pool are up.
function Start-Nido {
    Invoke-Tool $nido 'run' 180 | Out-Null
    $deadline = (Get-Date).AddSeconds(240)
    $st = ''
    do {
        Start-Sleep -Seconds 2
        try { $st = Invoke-Tool $nido 'status' 30 } catch { $st = $_.Exception.Message }
        $ready = $st -match 'apache\s+en marcha' -and $st -match 'mysql-8\.4\s+en marcha' -and $st -match 'php-cgi-9103\s+en marcha'
    } while (-not $ready -and (Get-Date) -lt $deadline)
    if (-not $ready) { throw $st }
    $st
}

Step 'Arrancar Nido con la bandeja (nido run)' { Start-Nido }

Step 'Base de datos y migraciones de Laravel en MySQL' {
    Invoke-Tool "$shims\mysql.exe" '-e "CREATE DATABASE IF NOT EXISTS laravel_demo"' 60 | Out-Null
    Invoke-Tool "$shims\php.exe" 'artisan migrate --force' 300 'C:\nido\www\laravel-demo'
}

Step 'MariaDB desde el catálogo, en paralelo con MySQL (nido service install)' {
    $out = Invoke-Tool $nido 'service install mariadb 11.8' 900
    $deadline = (Get-Date).AddSeconds(120)
    do {
        Start-Sleep -Seconds 2
        $list = Invoke-Tool $nido 'service list' 30
    } while ($list -notmatch 'mariadb-11\.8\s+\S+\s+puerto\s+\d+\s+en marcha' -and (Get-Date) -lt $deadline)
    if ($list -notmatch 'mariadb-11\.8\s+\S+\s+puerto\s+(\d+)\s+en marcha') { throw "MariaDB no arrancó:`n$list" }
    $port = $Matches[1]
    $client = Get-ChildItem "$H\bin\mariadb\*\bin\mariadb.exe" | Select-Object -First 1
    $version = Invoke-Tool $client.FullName "--host=127.0.0.1 --port=$port --user=root -N -e `"SELECT VERSION()`"" 60
    if ($list -notmatch 'mysql-8\.4\s+\S+\s+puerto\s+\d+\s+en marcha') { throw "MySQL dejó de estar en marcha:`n$list" }
    # The MariaDB client may add a warning about TLS on stderr; keep the version line.
    $v = ($version -split "`n" | Where-Object { $_ -match '^\s*\d+\.\d+\.\d+' } | Select-Object -First 1)
    if (-not $v) { throw "MariaDB no respondió: $version" }
    "$(($out -split "`n" | Select-Object -Last 1).Trim())`nMariaDB responde en el puerto ${port}: $($v.Trim())`n$list"
}

Step 'PostgreSQL, Redis y Mailpit desde el catálogo (PostgreSQL sin permisos de administrador)' {
    foreach ($k in 'postgresql', 'redis', 'mailpit') { Invoke-Tool $nido "service install $k latest" 1500 | Out-Null }
    $running = { param($kind) $list -match "$kind-[\d.]+\s+\S+\s+puerto\s+\d+\s+en marcha" }
    $deadline = (Get-Date).AddSeconds(240)
    do {
        Start-Sleep -Seconds 2
        $list = Invoke-Tool $nido 'service list' 30
    } while (-not ((& $running 'postgresql') -and (& $running 'redis') -and (& $running 'mailpit')) -and (Get-Date) -lt $deadline)
    $pg = Invoke-Tool "$shims\psql.exe" '-t -A -c "select version()"' 60
    if ($pg -notmatch 'PostgreSQL \d') { throw "PostgreSQL no respondió: $pg`n$list" }
    $pong = Invoke-Tool "$shims\redis-cli.exe" 'ping' 30
    if ($pong -notmatch 'PONG') { throw "Redis no respondió: $pong`n$list" }
    # PHP's mail() goes to Mailpit through the SMTP settings Nido writes in php.ini.
    $code = "var_export(mail('cliente@ejemplo.test', 'Prueba de Nido', 'Hola', 'From: app@nido.test'));"
    $sent = Invoke-Tool "$shims\php.exe" "-r `"$code`"" 60
    $inbox = (Invoke-Curl 'http://127.0.0.1:8025/api/v1/messages') | ConvertFrom-Json
    if ($sent -notmatch 'true' -or $inbox.total -lt 1) { throw "Mailpit no recibió el correo de mail(): $sent" }
    "PostgreSQL: $(($pg -split "`n")[0])`nRedis: $pong`nMailpit: $($inbox.total) correo(s) enviados con mail() de PHP"
}

Step 'https://demo.test con el hosts y la CA de Windows (sin --resolve ni --cacert)' {
    $raw = Invoke-Curl 'https://demo.test/'
    $j = $raw | ConvertFrom-Json
    if ($j.https -ne 'on') { throw "PHP no ve HTTPS: $raw" }
    if ("$($j.mysql)" -notmatch '^\d') { throw "sin MySQL: $raw" }
    $raw
}

Step 'http://demo.test redirige a https' {
    $r = Invoke-Curl '-o NUL -w %{http_code}->%{redirect_url} http://demo.test/hola'
    if ($r -ne '301->https://demo.test/hola') { throw $r }
    $r
}

Step 'Laravel por HTTPS: https://laravel-demo.test' {
    $code = Invoke-Curl "-o $R\laravel-inicio.html -w %{http_code} https://laravel-demo.test/"
    $html = Get-Content "$R\laravel-inicio.html" -Raw -Encoding UTF8
    if ($code -ne '200' -or $html -notmatch 'Laravel') { throw "HTTP $code" }
    $up = Invoke-Curl '-o NUL -w %{http_code} https://laravel-demo.test/up'
    if ($up -ne '200') { throw "/up respondió $up" }
    "página de inicio: HTTP $code (guardada en laravel-inicio.html) · /up: HTTP $up"
}

Step 'Edge abre https://demo.test sin aviso de certificado' {
    $edge = "${env:ProgramFiles(x86)}\Microsoft\Edge\Application\msedge.exe"
    if (-not (Test-Path $edge)) { throw 'Edge no está instalado' }
    $dom = Join-Path $env:TEMP 'edge-dom.html'
    # Started by an administrator, Edge restarts itself without rights and this one prints nothing.
    $p = Start-Process $edge -ArgumentList '--headless=new --do-not-de-elevate --disable-gpu --no-first-run --user-data-dir=C:\edge-prueba --dump-dom https://demo.test/estado.php' `
        -NoNewWindow -PassThru -RedirectStandardOutput $dom
    $null = $p.Handle
    if (-not $p.WaitForExit(120000)) { try { $p.Kill() } catch {}; throw 'Edge tardó demasiado' }
    $text = "$(Get-Content $dom -Raw -Encoding UTF8)"
    if ($text -notmatch 'id="https">on<') { throw "Edge no mostró la página: $($text.Substring(0, [Math]::Min(300, $text.Length)))" }
    'Edge cargó la página por HTTPS confiando en la CA de Nido'
}

Step 'Cambio de PHP en caliente (8.4 -> 8.3 -> 8.4)' {
    $lines = @()
    foreach ($v in '8.3', '8.4') {
        Invoke-Tool $nido "php use $v" 120 | Out-Null
        $j = (Invoke-Curl 'https://demo.test/') | ConvertFrom-Json
        if (-not $j.php.StartsWith($v)) { throw "tras 'php use $v' la web responde PHP $($j.php)" }
        $lines += "php use $v -> la web responde PHP $($j.php)"
    }
    $lines -join "`n"
}

Step 'PHP fijado por sitio: demo.test con PHP 7.2 y el resto con el global (nido site php)' {
    try {
        Invoke-Tool $nido 'site php demo 7.2' 300 | Out-Null
        $body = Invoke-Curl 'https://demo.test/'
        $j = try { $body | ConvertFrom-Json } catch { throw "demo.test no devolvió JSON: $body" }
        if (-not "$($j.php)".StartsWith('7.2')) { throw "demo.test responde PHP $($j.php) en vez de 7.2" }
        $up = Invoke-Curl '-o NUL -w %{http_code} https://laravel-demo.test/up'
        if ($up -ne '200') { throw "laravel-demo.test (PHP global) respondió HTTP $up" }
        # In the terminal, php and composer follow the folder: Composer 2.2 LTS for a PHP older than 7.2.5.
        $inside = Invoke-Tool "$shims\php.exe" '-r "echo PHP_VERSION;"' 60 'C:\nido\www\demo'
        $outside = Invoke-Tool "$shims\php.exe" '-r "echo PHP_VERSION;"' 60 'C:\nido\www\laravel-demo'
        $composer = ((Invoke-Tool "$shims\composer.exe" '--version' 120 'C:\nido\www\demo') -split "`n" | Select-Object -First 1).Trim()
        if (-not $inside.StartsWith('7.2') -or $outside.StartsWith('7.2')) { throw "terminal: $inside dentro de demo, $outside fuera" }
        # Switching the global PHP leaves the pinned site alone.
        Invoke-Tool $nido 'php use 8.3' 120 | Out-Null
        $while83 = ((Invoke-Curl 'https://demo.test/') | ConvertFrom-Json).php
        Invoke-Tool $nido 'php use 8.4' 120 | Out-Null
        if (-not "$while83".StartsWith('7.2')) { throw "con el PHP global en 8.3, demo.test respondió PHP $while83" }
    } finally {
        # The next steps use demo.test with the global PHP, also when this one fails.
        Invoke-Tool $nido 'site php demo global' 120 | Out-Null
    }
    $back = ((Invoke-Curl 'https://demo.test/') | ConvertFrom-Json).php
    if (-not "$back".StartsWith('8.4')) { throw "de vuelta al global, demo.test respondió PHP $back" }
    "web: demo.test PHP $($j.php) (MySQL $($j.mysql)) · laravel-demo.test /up HTTP $up`n" +
    "terminal: php $inside en C:\nido\www\demo, $outside en C:\nido\www\laravel-demo · $composer`n" +
    "con el global en 8.3, demo.test siguió en $while83 · de vuelta al global: $back"
}

Step 'Extensiones de PHP: Xdebug descargado y soap incluida (nido php ext)' {
    Invoke-Tool $nido 'php ext on xdebug' 300 | Out-Null
    Invoke-Tool $nido 'php ext on soap' 120 | Out-Null
    $page = "<?php echo json_encode(['xdebug' => extension_loaded('xdebug'), 'soap' => extension_loaded('soap')]);"
    [IO.File]::WriteAllText('C:\nido\www\demo\public\ext.php', $page, $utf8)
    $web = (Invoke-Curl 'https://demo.test/ext.php') | ConvertFrom-Json
    $cli = Invoke-Tool "$shims\php.exe" '-m' 60
    if (-not $web.xdebug -or -not $web.soap) { throw "la web no las carga: $($web | ConvertTo-Json -Compress)" }
    if ($cli -notmatch '(?m)^Xdebug' -or $cli -notmatch '(?m)^soap') { throw "php -m no las muestra:`n$cli" }
    Invoke-Tool $nido 'php ext off xdebug' 120 | Out-Null
    Invoke-Tool $nido 'php ext off soap' 120 | Out-Null
    $after = (Invoke-Curl 'https://demo.test/ext.php') | ConvertFrom-Json
    if ($after.xdebug -or $after.soap) { throw "siguen activas tras apagarlas: $($after | ConvertTo-Json -Compress)" }
    $dll = Get-ChildItem "$H\bin\php-ext" -Recurse -Filter php_xdebug.dll | Select-Object -First 1
    "web y terminal con Xdebug y soap ($($dll.FullName.Substring($H.Length + 1)), $([math]::Round($dll.Length / 1KB)) KB); apagadas otra vez: $($after | ConvertTo-Json -Compress)"
}

Step 'Xdebug solo en demo.test, y phpx en la terminal (nido site debug)' {
    # ext.php is the page the extensions step left in demo.
    Invoke-Tool $nido 'site debug demo on' 300 | Out-Null
    $demo = (Invoke-Curl 'https://demo.test/ext.php') | ConvertFrom-Json
    $other = Invoke-Tool "$shims\php.exe" '-r "echo extension_loaded(''xdebug'') ? ''con'' : ''sin'';"' 60
    $phpx = Invoke-Tool "$shims\phpx.exe" '-r "echo extension_loaded(''xdebug'') ? ''con'' : ''sin'';"' 60
    Invoke-Tool $nido 'site debug demo off' 120 | Out-Null
    $after = (Invoke-Curl 'https://demo.test/ext.php') | ConvertFrom-Json
    if (-not $demo.xdebug -or $after.xdebug -or $phpx.Trim() -ne 'con' -or $other.Trim() -ne 'sin') {
        throw "demo con Xdebug: $($demo.xdebug), después: $($after.xdebug); phpx $phpx Xdebug; php $other Xdebug"
    }
    "demo.test con Xdebug mientras se depura y sin él después · terminal: phpx con Xdebug, php sin él"
}

Step 'Adminer en https://db.localhost: entra como root sin contraseña y ve laravel_demo' {
    Invoke-Tool $nido 'service install adminer latest' 300 | Out-Null
    $jar = Join-Path $env:TEMP 'adminer-cookies.txt'
    $url = 'https://db.localhost/?server=127.0.0.1:3306&username=root'
    $c = @('-s', '--ssl-no-revoke', '--max-time', '60', '--resolve', 'db.localhost:443:127.0.0.1', '-c', $jar, '-b', $jar)
    $form = & curl.exe @c $url | Out-String
    if ($form -notmatch 'Adminer') { throw "db.localhost no muestra Adminer: $($form.Substring(0, [Math]::Min(300, $form.Length)))" }
    $post = & curl.exe @c -o NUL -w '%{http_code}' --data-urlencode 'auth[driver]=server' --data-urlencode 'auth[server]=127.0.0.1:3306' `
        --data-urlencode 'auth[username]=root' --data-urlencode 'auth[password]=' --data-urlencode 'auth[db]=' $url
    $page = & curl.exe @c $url | Out-String
    if ($page -notmatch 'laravel_demo') { throw "Adminer no lista laravel_demo tras entrar (POST: HTTP $post)" }
    "$(([regex]::Match($page, '<title>([^<]*)</title>')).Groups[1].Value) · entrada sin contraseña: HTTP $post · lista laravel_demo"
}

# The test installs the runtime of Visual Studio 2022 (14.44) first. Apache Lounge builds Apache with
# Visual Studio 2026, for which Microsoft asks a newer runtime; it runs anyway, and Nido recommends it.
Step 'Apache de Apache Lounge (lo instaló nido setup) con el runtime de Visual C++ 14.44' {
    $list = Invoke-Tool $nido 'apache list' 30
    if ($list -notmatch '\*\s+Apache\s+(\d+\.\d+\.\d+)\s.*download') { throw "el Apache activo no es el descargado:`n$list" }
    $ver = $Matches[1]
    $j = (Invoke-Curl 'https://demo.test/') | ConvertFrom-Json
    if ($j.https -ne 'on' -or "$($j.mysql)" -notmatch '^\d') { throw "demo.test con Apache ${ver}: $($j | ConvertTo-Json -Compress)" }
    $up = Invoke-Curl '-o NUL -w %{http_code} https://laravel-demo.test/up'
    if ($up -ne '200') { throw "laravel-demo.test/up con Apache ${ver}: HTTP $up" }
    $notice = ((Invoke-Tool $nido 'status' 30) -split "`n" | Where-Object { $_ -match 'Visual C\+\+' }) -join ' '
    "demo.test (PHP $($j.php), MySQL $($j.mysql)) y laravel-demo.test responden con Apache $ver`n" +
    "runtime: $((Get-Item "$env:SystemRoot\System32\vcruntime140.dll").VersionInfo.FileVersion) · aviso: $notice`n$list"
}

Step 'Actualizar Visual C++ con Nido en marcha (nido vcredist install)' {
    $dll = "$env:SystemRoot\System32\vcruntime140.dll"
    $before = (Get-Item $dll).VersionInfo.FileVersion
    # Windows may only finish replacing DLLs that running programs hold open after a restart.
    try { $out = Invoke-Tool $nido 'vcredist install C:\nido-dist\vc_redist-reciente.x64.exe' 900 } catch { $out = $_.Exception.Message }
    $after = (Get-Item $dll).VersionInfo.FileVersion
    Start-Sleep -Seconds 11 # Nido re-reads the runtime every 10 s
    $notice = ((Invoke-Tool $nido 'status' 30) -split "`n" | Where-Object { $_ -match 'Visual C\+\+' }) -join ' '
    $status = Invoke-Tool $nido 'vcredist status' 60
    $code = Invoke-Curl '-o NUL -w %{http_code} https://demo.test/'
    if ($code -ne '200') { throw "demo.test dejó de responder tras actualizar Visual C++: HTTP $code" }
    if ($after -eq $before -and $status -notmatch 'reiniciar') { throw "el runtime sigue en $before y no queda nada pendiente:`n$out`n$status" }
    "vcruntime140.dll: $before -> $after`n$((($out -split "`n") | Select-Object -Last 2) -join ' ')`n$status`naviso de Nido: $(if ($notice) { $notice } else { 'ninguno' })"
}

Step 'Nginx y FrankenPHP desde el catálogo, un sitio en cada uno (nido site server)' {
    Invoke-Tool $nido 'service install nginx latest' 600 | Out-Null
    Invoke-Tool $nido 'service install frankenphp latest' 900 | Out-Null
    Invoke-Tool $nido 'site server laravel-demo nginx' 120 | Out-Null
    Invoke-Tool $nido 'site server demo frankenphp' 120 | Out-Null
    Start-Sleep -Seconds 2
    $code = Invoke-Curl "-o $R\laravel-nginx.html -w %{http_code} https://laravel-demo.test/"
    $html = Get-Content "$R\laravel-nginx.html" -Raw -Encoding UTF8
    if ($code -ne '200' -or $html -notmatch 'Laravel') { throw "Laravel con Nginx respondió HTTP $code" }
    $head = Invoke-Curl '-I https://laravel-demo.test/up'
    if ($head -notmatch '(?im)^Server:\s*nginx') { throw "laravel-demo.test no lo sirve Nginx: $head" }
    $j = (Invoke-Curl 'https://demo.test/') | ConvertFrom-Json
    if ($j.https -ne 'on' -or "$($j.mysql)" -notmatch '^\d' -or "$($j.php)" -notmatch '^\d') { throw "FrankenPHP: $($j | ConvertTo-Json -Compress)" }
    $out = "laravel-demo.test con Nginx: HTTP $code`ndemo.test con FrankenPHP: PHP $($j.php) propio, HTTPS $($j.https), MySQL $($j.mysql)"
    Invoke-Tool $nido 'site server laravel-demo apache' 120 | Out-Null
    Invoke-Tool $nido 'site server demo apache' 120 | Out-Null
    $out
}

Step 'Proyecto nuevo con nido new: kit de Vue, MySQL y pnpm' {
    # pnpm is not installed yet: Nido installs it on the way, like Laravel's installer with --pnpm.
    $out = Invoke-Tool $nido 'new tienda-vue --kit vue --db mysql --pnpm' 2400
    # A new site waits for its line in hosts (one more permission); --resolve stands in for it here.
    $code = Invoke-Curl "--resolve tienda-vue.test:443:127.0.0.1 -o $R\tienda-vue.html -w %{http_code} https://tienda-vue.test/"
    $html = Get-Content "$R\tienda-vue.html" -Raw -Encoding UTF8
    if ($code -ne '200' -or $html -notmatch 'data-page' -or $html -notmatch '/build/assets/') { throw "tienda-vue.test respondió HTTP $code sin la página de Inertia compilada:`n$out" }
    $tables = Invoke-Tool "$shims\mysql.exe" '-N -e "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=''tienda_vue''"' 60
    if ([int]$tables.Trim() -lt 5) { throw "la base tienda_vue tiene $tables tablas" }
    if (-not (Test-Path 'C:\nido\www\tienda-vue\pnpm-lock.yaml') -or (Test-Path 'C:\nido\www\tienda-vue\package-lock.json')) { throw "tienda-vue no quedó con pnpm-lock.yaml en lugar de package-lock.json:`n$out" }
    "$((($out -split "`n") | Select-Object -Last 1).Trim())`nhttps://tienda-vue.test: HTTP $code con Inertia y assets de Vite · MySQL tienda_vue: $($tables.Trim()) tablas · pnpm-lock.yaml"
}

Step 'Proyecto clonado de GitHub y preparado (nido new --git): Composer, .env, MySQL y Vite' {
    $out = Invoke-Tool $nido 'new demo-git --git https://github.com/laravel/laravel.git --db mysql' 2400
    $code = Invoke-Curl '--resolve demo-git.test:443:127.0.0.1 -o NUL -w %{http_code} https://demo-git.test/'
    $tables = Invoke-Tool "$shims\mysql.exe" '-N -e "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=''demo_git''"' 60
    $ready = (Test-Path 'C:\nido\www\demo-git\vendor\autoload.php') -and (Test-Path 'C:\nido\www\demo-git\public\build\manifest.json')
    if ($code -ne '200' -or [int]$tables.Trim() -lt 5 -or -not $ready) { throw "demo-git.test: HTTP $code, $tables tablas, preparado: $ready`n$out" }
    "$((($out -split "`n") | Select-Object -Last 1).Trim())`nhttps://demo-git.test: HTTP $code · MySQL demo_git: $($tables.Trim()) tablas · vendor y frontend compilado"
}

Step 'WordPress con nido new --wordpress: en español, enlaces permanentes y acceso de admin' {
    $out = Invoke-Tool $nido 'new "Blog de Prueba" --wordpress --db mysql' 1200
    $pass = ([regex]'contraseña (\S+)').Match($out).Groups[1].Value
    if (-not $pass) { throw "la salida no trae la contraseña de admin:`n$out" }
    $site = '--resolve blog-de-prueba.test:443:127.0.0.1 https://blog-de-prueba.test'
    $html = Invoke-Curl "$site/"
    $post = Invoke-Curl "-o NUL -w %{http_code} $site/hola-mundo/"
    $login = Invoke-Curl "-b wordpress_test_cookie=WP%20Cookie%20check -o NUL -w %{http_code}:%{redirect_url} --data-urlencode log=admin --data-urlencode pwd=$pass --data-urlencode testcookie=1 $site/wp-login.php"
    if ($html -notmatch '<html[^>]*lang="es' -or $post -ne '200' -or $login -notmatch '^302:.*/wp-admin/') {
        throw "portada en español: $($html -match 'lang=.es'), /hola-mundo/: HTTP $post, acceso: $login`n$out"
    }
    "$((($out -split "`n") | Select-Object -Last 2) -join "`n")`nportada en español · /hola-mundo/: HTTP $post · admin entra ($login)"
}

Step 'Symfony con nido new --symfony --webapp: .htaccess de Apache, Doctrine en MySQL y Mailpit' {
    $out = Invoke-Tool $nido 'new demo-symfony --symfony --webapp --db mysql' 1800
    $site = '--resolve demo-symfony.test:443:127.0.0.1 https://demo-symfony.test'
    $welcome = Invoke-Curl "$site/"
    $missing = Invoke-Curl "$site/no-existe"
    $dir = 'C:\nido\www\demo-symfony'
    $db = Invoke-Tool "$shims\php.exe" 'bin/console dbal:run-sql "SELECT DATABASE()"' 120 $dir
    if ($welcome -notmatch 'Welcome to Symfony' -or $missing -notmatch 'No route found' -or $db -notmatch 'demo_symfony') {
        throw "bienvenida: $($welcome -match 'Welcome to Symfony'), ruta inexistente por Symfony: $($missing -match 'No route found'), base: $db`n$out"
    }
    "$((($out -split "`n") | Select-Object -Last 1).Trim())`nbienvenida de Symfony · /no-existe responde el router (el .htaccess reescribe) · Doctrine conectado a demo_symfony"
}

Step 'Plantilla propia: laravel-demo guardado como plantilla y un proyecto nuevo desde ella (nido site template)' {
    Invoke-Tool $nido 'site template laravel-demo base-demo' 300 | Out-Null
    $saved = 'C:\nido\config\plantillas\base-demo'
    if ((Test-Path "$saved\vendor") -or (Test-Path "$saved\.env") -or -not (Test-Path "$saved\artisan")) { throw "la plantilla no quedó como debía en $saved" }
    $out = Invoke-Tool $nido 'new desde-plantilla --receta base-demo' 1800
    $code = Invoke-Curl '--resolve desde-plantilla.test:443:127.0.0.1 -o NUL -w %{http_code} https://desde-plantilla.test/'
    if ($code -ne '200') { throw "desde-plantilla.test: HTTP $code`n$out" }
    "$((($out -split "`n") | Select-Object -Last 1).Trim())`nplantilla sin vendor ni .env · https://desde-plantilla.test: HTTP $code"
}

Step 'Procesos del proyecto: Vite con HTTPS, cola y tareas (nido site proc)' {
    $hot = 'C:\nido\www\laravel-demo\public\hot'
    Invoke-Tool $nido 'site proc laravel-demo vite on' 120 | Out-Null
    $deadline = (Get-Date).AddSeconds(90)
    while (-not (Test-Path $hot) -and (Get-Date) -lt $deadline) { Start-Sleep -Milliseconds 500 }
    if (-not (Test-Path $hot)) { throw "Vite no arrancó:`n$(Get-Content "$H\logs\laravel-demo-vite.log" -Tail 15 -Encoding UTF8 | Out-String)" }
    $devUrl = (Get-Content $hot -Raw).Trim()
    if ($devUrl -ne 'https://laravel-demo.test:5173') { throw "public/hot apunta a $devUrl" }
    $client = Invoke-Curl '-o NUL -w %{http_code} https://laravel-demo.test:5173/@vite/client'
    $page = Invoke-Curl 'https://laravel-demo.test/'
    if ($client -ne '200' -or $page -notmatch 'laravel-demo\.test:5173/@vite/client') { throw "el servidor de Vite (HTTP $client) o la página no van por HTTPS" }
    Invoke-Tool $nido 'site proc laravel-demo queue on' 120 | Out-Null
    Invoke-Tool $nido 'site proc laravel-demo schedule on' 120 | Out-Null
    Start-Sleep -Seconds 5
    $st = Invoke-Tool $nido 'status' 30
    $running = @('vite', 'queue', 'schedule' | Where-Object { $st -match "$_-laravel-demo\s+en marcha" })
    foreach ($p in 'vite', 'queue', 'schedule') { Invoke-Tool $nido "site proc laravel-demo $p off" 120 | Out-Null }
    if ($running.Count -ne 3) { throw "en marcha solo: $($running -join ', ')`n$st" }
    if (Test-Path $hot) { throw 'public/hot quedó después de apagar Vite' }
    "Vite en $devUrl con el certificado de Nido (/@vite/client: HTTP $client) y la página cargándolo · en marcha: $($running -join ', ') · apagados y public/hot borrado"
}

Step 'Node, npm, npx, Composer y PHP desde shims\' {
    # Invoke-Tool, not &: Composer writes to stderr, which Windows PowerShell 5.1 turns into errors.
    @(
        "node $(Invoke-Tool "$shims\node.exe" '-v' 60)",
        "npm $(Invoke-Tool "$shims\npm.exe" '-v' 120)",
        "npx $(Invoke-Tool "$shims\npx.exe" '--version' 120)",
        ((Invoke-Tool "$shims\composer.exe" '--version' 120) -split "`n" | Select-Object -First 1),
        ((Invoke-Tool "$shims\php.exe" '-v' 60) -split "`n" | Select-Object -First 1)
    ) -join "`n"
}

Step 'Reverb en wss://reverb.laravel-demo.test: evento de PHP a un cliente WebSocket (nido site proc reverb)' {
    $dir = 'C:\nido\www\laravel-demo'
    # Laravel's installer gives npm 60 s, too little on a slow machine: the frontend part goes apart.
    Invoke-Tool "$shims\php.exe" 'artisan install:broadcasting --reverb --without-node --no-interaction' 900 $dir | Out-Null
    Invoke-Tool "$shims\npm.exe" 'install --save-dev laravel-echo pusher-js --no-audit --no-fund' 1200 $dir | Out-Null
    Invoke-Tool "$shims\npm.exe" 'run build' 600 $dir | Out-Null
    Invoke-Tool $nido 'rescan' 120 | Out-Null
    Invoke-Tool $nido 'site proc laravel-demo reverb on' 900 | Out-Null
    # reverb.laravel-demo.test goes to the hosts file like the site names.
    $p = Start-Process $nido -ArgumentList 'hosts sync' -NoNewWindow -PassThru
    $null = $p.Handle
    if (-not ($p.WaitForExit(120000) -and $p.ExitCode -eq 0)) {
        try { $p.Kill() } catch {}
        Invoke-Tool $nido "hosts-apply $H\run\hosts-names.txt" 60 | Out-Null
    }
    $key = ((Get-Content "$dir\.env" | Where-Object { $_ -match '^REVERB_APP_KEY=' }) -split '=', 2)[1].Trim('"')
    # Node's own WebSocket client, trusting Nido's CA like the browser does, waits for one event.
    $js = @"
const ws = new WebSocket('wss://reverb.laravel-demo.test/app/${key}?protocol=7&client=js&version=8.4.0');
ws.onmessage = e => {
  console.log(e.data);
  const m = JSON.parse(e.data);
  if (m.event === 'pusher:connection_established') ws.send(JSON.stringify({ event: 'pusher:subscribe', data: { auth: '', channel: 'pruebas' } }));
  if (m.event === 'saludo') process.exit(0);
};
ws.onerror = e => { console.log('error: ' + e.message); process.exit(1); };
setTimeout(() => { console.log('sin evento'); process.exit(2); }, 40000);
"@
    [IO.File]::WriteAllText("$R\reverb-cliente.js", $js, $utf8)
    $env:NODE_EXTRA_CA_CERTS = "$H\ssl\nido-ca.crt"
    $client = Start-Process "$shims\node.exe" -ArgumentList "$R\reverb-cliente.js" -NoNewWindow -PassThru -RedirectStandardOutput "$R\reverb-cliente.txt"
    $null = $client.Handle
    Start-Sleep -Seconds 5
    # PHP reaches Reverb the same way: https://reverb.laravel-demo.test through the proxy.
    Invoke-Tool "$shims\php.exe" "artisan tinker --execute=""Illuminate\Support\Facades\Broadcast::connection('reverb')->broadcast(['pruebas'], 'saludo', ['texto' => 'hola desde Nido']);""" 120 $dir | Out-Null
    $client.WaitForExit(45000) | Out-Null
    $got = Get-Content "$R\reverb-cliente.txt" -Raw
    Invoke-Tool $nido 'site proc laravel-demo reverb off' 120 | Out-Null
    if ($client.ExitCode -ne 0 -or $got -notmatch 'hola desde Nido') { throw "el cliente no recibió el evento:`n$got" }
    "wss://reverb.laravel-demo.test: conexión, suscripción y evento enviado desde PHP`n$got"
}

Step 'Node: líneas de nodejs.org, otra versión, yarn y pnpm (nido node)' {
    $lines = Invoke-Tool $nido 'node remote' 120
    if ($lines -notmatch 'LTS') { throw "sin líneas LTS:`n$lines" }
    $global = (Invoke-Tool "$shims\node.exe" '-v' 60).Trim()
    $inst = Invoke-Tool $nido 'node install 22' 900
    $v22 = ([regex]'Node (22\.\d+\.\d+)').Match($inst).Groups[1].Value
    if (-not $v22) { throw "Node 22 no se instaló:`n$inst" }
    # A second Node does not replace the global one, and Nido deletes the folders it downloaded.
    if ((Invoke-Tool "$shims\node.exe" '-v' 60).Trim() -ne $global) { throw "el Node global cambió al instalar $v22" }
    Invoke-Tool $nido "node uninstall $v22" 120 | Out-Null
    if (Test-Path "$H\bin\node\$v22") { throw "quedó la carpeta de Node $v22" }
    Invoke-Tool $nido 'node install pnpm' 600 | Out-Null # the Vue project above may have installed it already
    $yarn = Invoke-Tool $nido 'node install yarn' 600
    $versions = "pnpm $((Invoke-Tool "$shims\pnpm.exe" '--version' 120).Trim()) · yarn $((Invoke-Tool "$shims\yarn.exe" '--version' 120).Trim())"
    "$(($lines -split "`n" | Where-Object { $_ -match 'LTS|actual' }) -join ' |')`nNode $v22 instalado junto al global ($global) y borrado · $versions"
}

# The whole environment at the end of the run, the way the health page sees it: every service in
# use and the database of every site must answer.
Step 'Página de salud en https://salud.localhost: PHP, servicios y la base de cada sitio' {
    $c = @('-s', '--ssl-no-revoke', '--max-time', '120', '--resolve', 'salud.localhost:443:127.0.0.1')
    $raw = (& curl.exe @c -w "`n%{http_code}" 'https://salud.localhost/?formato=json' | Out-String).TrimEnd()
    $lines = $raw -split "`n"
    $code = $lines[-1].Trim()
    $r = ($lines[0..($lines.Count - 2)] -join "`n") | ConvertFrom-Json
    $html = & curl.exe @c 'https://salud.localhost/' | Out-String
    $bad = @(@($r.servicios) + @($r.sitios) | Where-Object { $_.estado -eq 'fallo' } | ForEach-Object { "$($_.servicio)$($_.sitio): $($_.detalle)" })
    $mysql = @($r.servicios | Where-Object { $_.servicio -match '^MySQL' -and $_.estado -eq 'ok' })
    $site = $r.sitios | Where-Object { $_.sitio -eq 'laravel-demo.test' }
    if ($html -notmatch 'Salud del entorno' -or -not $r.php.version -or $mysql.Count -eq 0 -or $site.estado -ne 'ok') {
        throw "página: $($html -match 'Salud del entorno'), PHP $($r.php.version), MySQL OK: $($mysql.Count), laravel-demo.test: $($site.estado) $($site.detalle)`n$($bad -join "`n")"
    }
    if (-not $r.ok -or $code -ne '200') { throw "HTTP $code con $($bad.Count) fallo(s):`n$($bad -join "`n")" }
    "HTTP $code · $($r.resumen.ok) de $($r.resumen.total) OK · PHP $($r.php.version)`n" +
    ((@($r.servicios) | ForEach-Object { "$($_.servicio): $($_.estado) · $($_.detalle)" }) -join "`n") + "`n" +
    ((@($r.sitios) | Where-Object { $_.estado -ne 'sin-bd' } | ForEach-Object { "$($_.sitio): $($_.estado) · $($_.bd)" }) -join "`n")
}

Step 'Panel en https://localhost' {
    $code = Invoke-Curl '-o NUL -w %{http_code} https://localhost/'
    if ($code -ne '200') { throw "HTTP $code" }
    "HTTP $code"
}

Step 'Estado final (nido status)' { Invoke-Tool $nido 'status' 30 }

Step 'Cerrar Nido (nido quit)' {
    Invoke-Tool $nido 'quit' 60 | Out-Null
    $deadline = (Get-Date).AddSeconds(60)
    do {
        Start-Sleep -Milliseconds 500
        $left = @(Get-CimInstance Win32_Process | Where-Object { $_.Name -eq 'nidow.exe' -or ($_.CommandLine -match 'C:\\nido\\') })
    } while ($left.Count -gt 0 -and (Get-Date) -lt $deadline)
    if ($left.Count -gt 0) { throw "quedan procesos: $(($left | ForEach-Object Name) -join ', ')" }
    'sin procesos de Nido'
}

# Logs and generated configuration for later review.
New-Item -ItemType Directory -Force "$R\logs", "$R\config" | Out-Null
Copy-Item "$H\logs\*" "$R\logs" -ErrorAction SilentlyContinue
Copy-Item "$H\config\*" "$R\config" -ErrorAction SilentlyContinue
Copy-Item "$H\generated\apache\*\httpd.conf" "$R\config" -ErrorAction SilentlyContinue

$ok = @($report | Where-Object { $_.ok }).Count
$bad = @($report | Where-Object { -not $_.ok }).Count
$md = "# Prueba de Nido en una VM de Windows`n`n$(Get-Date -Format 'yyyy-MM-dd HH:mm') · $ok pasos bien, $bad con fallo`n`n| Paso | Resultado | Segundos |`n|---|---|---|`n"
foreach ($s in $report) { $md += "| $($s.paso) | $(if ($s.ok) { 'OK' } else { 'FALLO' }) | $($s.segundos) |`n" }
foreach ($s in $report) { $md += "`n## $($s.paso)`n`n``````text`n$($s.detalle)`n```````n" }
[IO.File]::WriteAllText("$R\report.md", $md, $utf8)
[IO.File]::WriteAllText("$R\done.json", (@{ ok = $ok; fallos = $bad; fin = (Get-Date -Format o) } | ConvertTo-Json), $utf8)

# The last step closed Nido to check that nothing was left behind; whoever looks at the VM
# should find it running, so it starts again with the Laravel project and the panel open.
$demo = 'Nido sigue arrancado para que lo pruebes: su icono está junto al reloj (quizá bajo ^), y Edge tiene abiertos el proyecto Laravel y el panel.'
try {
    Start-Nido | Out-Null
    $edgePolicy = 'HKLM:\SOFTWARE\Policies\Microsoft\Edge'
    New-Item -Path $edgePolicy -Force | Out-Null
    New-ItemProperty -Path $edgePolicy -Name HideFirstRunExperience -Value 1 -PropertyType DWord -Force | Out-Null
    # Through Explorer, like a double click, so they do not depend on this window.
    Start-Process explorer.exe 'https://laravel-demo.test/'
    Start-Sleep -Seconds 3
    Start-Process explorer.exe "$H\nidow.exe" # a second start opens the panel of the running Nido
} catch {
    $demo = "No se pudo dejar Nido arrancado: $($_.Exception.Message)"
}
Stop-Transcript | Out-Null

$Host.UI.RawUI.WindowTitle = 'Nido - prueba terminada'
Write-Host ''
Write-Host "Prueba terminada: $ok pasos bien, $bad con fallo." -ForegroundColor Cyan
Write-Host $demo -ForegroundColor Cyan
Write-Host 'La próxima prueba vuelve la VM al checkpoint «limpia».' -ForegroundColor Cyan
while ($true) { Start-Sleep -Seconds 3600 } # keeps this message on screen
