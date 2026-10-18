# Prueba de clon de punta a punta en un Windows limpio (Windows Sandbox).
# La lanza probar-en-sandbox.ps1; los resultados quedan en C:\resultados (carpeta del host).
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$R = 'C:\resultados'
$H = 'C:\clon'
$clon = Join-Path $H 'clon.exe'
$shims = Join-Path $H 'shims'
$report = New-Object System.Collections.ArrayList
$utf8 = New-Object System.Text.UTF8Encoding $false
$Host.UI.RawUI.WindowTitle = 'clon - prueba en Windows Sandbox'
Start-Transcript -Path (Join-Path $R 'transcript.txt') -Force | Out-Null
# Closing the sandbox window discards the machine; a run was lost that way halfway through.
Write-Host 'Prueba automática de clon: tarda unos 15-20 minutos.' -ForegroundColor Yellow
Write-Host 'No cierres esta ventana ni el sandbox hasta que aparezca "Prueba terminada".' -ForegroundColor Yellow
$stepNo = 0

function Save-Report {
    [IO.File]::WriteAllText((Join-Path $R 'report.json'), ($report | ConvertTo-Json -Depth 4), $utf8)
}

function Step([string]$name, [scriptblock]$body) {
    $script:stepNo++
    $Host.UI.RawUI.WindowTitle = "clon - paso $($script:stepNo): $name - NO CIERRES EL SANDBOX"
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

Step 'Entorno del sandbox' {
    $os = Get-CimInstance Win32_OperatingSystem
    $script:admin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole(
        [Security.Principal.WindowsBuiltInRole]::Administrator)
    $script:vcBefore = @('vcruntime140.dll', 'vcruntime140_1.dll', 'msvcp140.dll' | Where-Object {
        -not (Test-Path "$env:SystemRoot\System32\$_") }).Count -eq 0
    "Windows $($os.Caption) build $($os.BuildNumber) · usuario $env:USERNAME · administrador: $script:admin · runtime de Visual C++ presente: $script:vcBefore"
}

Step 'Copiar clon a C:\clon' {
    New-Item -ItemType Directory -Force $H | Out-Null
    Copy-Item 'C:\clon-dist\*.exe' $H
    (Get-ChildItem $H -Filter *.exe | ForEach-Object { "$($_.Name) $([math]::Round($_.Length / 1MB, 1)) MB" }) -join ', '
}

if (-not $script:vcBefore) {
    Step 'Importar Laragon sin runtime de Visual C++ (cómo lo informa clon)' {
        Invoke-Tool $clon 'import laragon' 180
    }
    Step 'clon vcredist install (instalador oficial de Microsoft, firma comprobada)' {
        (Invoke-Tool $clon 'vcredist install C:\clon-dist\vc_redist.x64.exe' 900) + "`n" + (Invoke-Tool $clon 'vcredist status' 60)
    }
}

Step 'Importar Laragon (binarios compartidos en solo lectura)' { Invoke-Tool $clon 'import laragon' 180 }

Step 'clon setup: Node LTS con npm y npx, Composer y CAs públicas' { Invoke-Tool $clon 'setup' 900 }

Step 'Descargar PHP 8.4 de windows.php.net' { Invoke-Tool $clon 'php install 8.4' 900 }

Step 'Configurar puertos reales (80/443) y MySQL 3306' {
    New-Item -ItemType Directory -Force 'C:\www' | Out-Null
    $cfg = @"
tld = "test"
roots = ['C:\www']

[proxy]
  http_port = 80
  https_port = 443
  lan = false

[php]
  version = "8.4"
  pool_size = 4
  base_port = 9100

[node]
  version = ""

[apache]
  version = ""

[mysql]
  enabled = true
  version = ""
  port = 3306

[hosts]
  manage = true
"@
    [IO.File]::WriteAllText("$H\config\clon.toml", $cfg, $utf8)
    Invoke-Tool $clon 'shims' 120
}

Step 'Sitio de prueba demo.test' {
    New-Item -ItemType Directory -Force 'C:\www\demo\public' | Out-Null
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
    [IO.File]::WriteAllText('C:\www\demo\public\index.php', $php, $utf8)
    # Plain HTML for the browser check (Edge shows JSON through its own viewer).
    $html = '<!doctype html><title>clon</title><p id="https"><?= $_SERVER[''HTTPS''] ?? ''off'' ?></p>'
    [IO.File]::WriteAllText('C:\www\demo\public\estado.php', $html, $utf8)
    'C:\www\demo\public\index.php y estado.php'
}

Step 'Proyecto Laravel nuevo (composer create-project laravel/laravel)' {
    $out = Invoke-Tool "$shims\composer.exe" 'create-project laravel/laravel C:\www\laravel-demo --prefer-dist --no-interaction --no-progress' 1200
    $envFile = 'C:\www\laravel-demo\.env'
    $t = [IO.File]::ReadAllText($envFile)
    $t = $t -replace '(?m)^APP_URL=.*$', 'APP_URL=https://laravel-demo.test'
    $t = $t -replace '(?m)^DB_CONNECTION=.*$', 'DB_CONNECTION=mysql'
    $t = $t -replace '(?m)^#?\s*DB_HOST=.*$', 'DB_HOST=127.0.0.1'
    $t = $t -replace '(?m)^#?\s*DB_PORT=.*$', 'DB_PORT=3306'
    $t = $t -replace '(?m)^#?\s*DB_DATABASE=.*$', 'DB_DATABASE=laravel_demo'
    $t = $t -replace '(?m)^#?\s*DB_USERNAME=.*$', 'DB_USERNAME=root'
    $t = $t -replace '(?m)^#?\s*DB_PASSWORD=.*$', 'DB_PASSWORD='
    [IO.File]::WriteAllText($envFile, $t, $utf8)
    $version = Invoke-Tool "$shims\php.exe" 'artisan --version' 120 'C:\www\laravel-demo'
    "$version`n" + (($out -split "`n") | Select-Object -Last 6) -join "`n"
}

Step 'npm install y npm run build (Vite) en el proyecto Laravel' {
    $i = Invoke-Tool "$shims\npm.exe" 'install --no-audit --no-fund' 1200 'C:\www\laravel-demo'
    $b = Invoke-Tool "$shims\npm.exe" 'run build' 600 'C:\www\laravel-demo'
    ((($i -split "`n") | Select-Object -Last 2) + (($b -split "`n") | Select-Object -Last 4)) -join "`n"
}

Step 'Confiar en la CA local (clon ca trust)' {
    Write-Host 'Si Windows muestra un aviso para instalar el certificado "clon CA local", pulsa Sí.' -ForegroundColor Yellow
    $p = Start-Process $clon -ArgumentList 'ca trust' -NoNewWindow -PassThru
    $null = $p.Handle
    if ($p.WaitForExit(120000) -and $p.ExitCode -eq 0) { return 'aceptada con clon ca trust (almacén del usuario)' }
    try { $p.Kill() } catch {}
    Get-Process certutil -ErrorAction SilentlyContinue | Stop-Process -Force
    if (-not $script:admin) { throw 'nadie confirmó el aviso y la sesión no es de administrador para el plan B' }
    $out = & certutil.exe -addstore Root "$H\ssl\clon-ca.crt" 2>&1 | Out-String
    if ($LASTEXITCODE -ne 0) { throw "certutil: $out" }
    'el aviso no se confirmó a tiempo; plan B: CA añadida al almacén de la máquina con certutil'
}

Step 'Archivo hosts (clon hosts sync)' {
    $p = Start-Process $clon -ArgumentList 'hosts sync' -NoNewWindow -PassThru
    $null = $p.Handle
    if ($p.WaitForExit(120000) -and $p.ExitCode -eq 0) {
        $how = 'clon hosts sync (elevación de Windows)'
    } else {
        try { $p.Kill() } catch {}
        if (-not $script:admin) { throw 'no se concedió el permiso y la sesión no es de administrador para el plan B' }
        Invoke-Tool $clon "hosts-apply $H\run\hosts-names.txt" 60 | Out-Null
        $how = 'plan B: clon hosts-apply directo (la sesión ya es de administrador)'
    }
    $lines = Select-String -Path "$env:SystemRoot\System32\drivers\etc\hosts" -Pattern 'clon|\.test' | ForEach-Object { $_.Line.Trim() }
    "$how`n" + ($lines -join "`n")
}

Step 'Arrancar clon con la bandeja (clon run)' {
    Invoke-Tool $clon 'run' 180 | Out-Null
    $deadline = (Get-Date).AddSeconds(240)
    $st = ''
    do {
        Start-Sleep -Seconds 2
        try { $st = Invoke-Tool $clon 'status' 30 } catch { $st = $_.Exception.Message }
        $ready = $st -match 'apache\s+en marcha' -and $st -match 'mysql-8\.0\s+en marcha' -and $st -match 'php-cgi-9103\s+en marcha'
    } while (-not $ready -and (Get-Date) -lt $deadline)
    if (-not $ready) { throw $st }
    $st
}

Step 'Base de datos y migraciones de Laravel en MySQL' {
    Invoke-Tool "$shims\mysql.exe" '-e "CREATE DATABASE IF NOT EXISTS laravel_demo"' 60 | Out-Null
    Invoke-Tool "$shims\php.exe" 'artisan migrate --force' 300 'C:\www\laravel-demo'
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
    if (-not (Test-Path $edge)) { throw 'Edge no está en el sandbox' }
    $dom = Join-Path $env:TEMP 'edge-dom.html'
    $p = Start-Process $edge -ArgumentList '--headless=new --disable-gpu --no-first-run --user-data-dir=C:\edge-prueba --dump-dom https://demo.test/estado.php' `
        -NoNewWindow -PassThru -RedirectStandardOutput $dom
    $null = $p.Handle
    if (-not $p.WaitForExit(120000)) { try { $p.Kill() } catch {}; throw 'Edge tardó demasiado' }
    $text = "$(Get-Content $dom -Raw -Encoding UTF8)"
    if ($text -notmatch 'id="https">on<') { throw "Edge no mostró la página: $($text.Substring(0, [Math]::Min(300, $text.Length)))" }
    'Edge cargó la página por HTTPS confiando en la CA de clon'
}

Step 'Cambio de PHP en caliente (8.4 -> 8.3 -> 8.4)' {
    $lines = @()
    foreach ($v in '8.3', '8.4') {
        Invoke-Tool $clon "php use $v" 120 | Out-Null
        $j = (Invoke-Curl 'https://demo.test/') | ConvertFrom-Json
        if (-not $j.php.StartsWith($v)) { throw "tras 'php use $v' la web responde PHP $($j.php)" }
        $lines += "php use $v -> la web responde PHP $($j.php)"
    }
    $lines -join "`n"
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

Step 'Panel en https://localhost' {
    $code = Invoke-Curl '-o NUL -w %{http_code} https://localhost/'
    if ($code -ne '200') { throw "HTTP $code" }
    "HTTP $code"
}

Step 'Estado final (clon status)' { Invoke-Tool $clon 'status' 30 }

Step 'Cerrar clon (clon quit)' {
    Invoke-Tool $clon 'quit' 60 | Out-Null
    $deadline = (Get-Date).AddSeconds(60)
    do {
        Start-Sleep -Milliseconds 500
        $left = @(Get-CimInstance Win32_Process | Where-Object { $_.Name -eq 'clonw.exe' -or ($_.CommandLine -match 'C:\\clon\\') })
    } while ($left.Count -gt 0 -and (Get-Date) -lt $deadline)
    if ($left.Count -gt 0) { throw "quedan procesos: $(($left | ForEach-Object Name) -join ', ')" }
    'sin procesos de clon'
}

# Logs and generated configuration for later review.
New-Item -ItemType Directory -Force "$R\logs", "$R\config" | Out-Null
Copy-Item "$H\logs\*" "$R\logs" -ErrorAction SilentlyContinue
Copy-Item "$H\config\*" "$R\config" -ErrorAction SilentlyContinue
Copy-Item "$H\generated\apache\*\httpd.conf" "$R\config" -ErrorAction SilentlyContinue

$ok = @($report | Where-Object { $_.ok }).Count
$bad = @($report | Where-Object { -not $_.ok }).Count
$md = "# Prueba de clon en Windows Sandbox`n`n$(Get-Date -Format 'yyyy-MM-dd HH:mm') · $ok pasos bien, $bad con fallo`n`n| Paso | Resultado | Segundos |`n|---|---|---|`n"
foreach ($s in $report) { $md += "| $($s.paso) | $(if ($s.ok) { 'OK' } else { 'FALLO' }) | $($s.segundos) |`n" }
foreach ($s in $report) { $md += "`n## $($s.paso)`n`n``````text`n$($s.detalle)`n```````n" }
[IO.File]::WriteAllText("$R\report.md", $md, $utf8)
[IO.File]::WriteAllText("$R\done.json", (@{ ok = $ok; fallos = $bad; fin = (Get-Date -Format o) } | ConvertTo-Json), $utf8)
Stop-Transcript | Out-Null

$Host.UI.RawUI.WindowTitle = 'clon - prueba terminada: ya puedes cerrar el sandbox'
Write-Host ''
Write-Host "Prueba terminada: $ok pasos bien, $bad con fallo. Ya puedes cerrar el sandbox (todo lo de dentro se descarta)." -ForegroundColor Cyan
[void][Console]::ReadLine()
