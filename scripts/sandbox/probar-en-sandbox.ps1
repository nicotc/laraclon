# Compila clon y lo prueba de punta a punta en Windows Sandbox (un Windows limpio y desechable).
# Comparte en solo lectura los ejecutables de clon y C:\laragon\bin; los resultados vuelven a
# clon\dist\sandbox\<fecha>\resultados. Cerrar la ventana del sandbox descarta todo lo de dentro.
param(
    [int]$MemoryMB = 6144,
    [int]$TimeoutMin = 45
)
$ErrorActionPreference = 'Stop'

$repo = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
# Go: $env:CLON_GO if set, otherwise the go on PATH, otherwise a portable toolchain next to the repo.
$go = $env:CLON_GO
if (-not $go) { $go = (Get-Command go -ErrorAction SilentlyContinue).Source }
if (-not $go) { $go = Join-Path (Split-Path -Parent $repo) '.toolchain\go.ps1' }
if (-not (Test-Path $go)) { throw 'No se encontró Go 1.27 o posterior (instálalo de go.dev o define CLON_GO).' }
$sandboxExe = Join-Path $env:SystemRoot 'System32\WindowsSandbox.exe'
if (-not (Test-Path $sandboxExe)) {
    throw 'Windows Sandbox no está activado (Características de Windows > Espacio aislado de Windows).'
}
if (Get-Process WindowsSandbox, WindowsSandboxClient, WindowsSandboxRemoteSession -ErrorAction SilentlyContinue) {
    throw 'Ya hay un Windows Sandbox abierto; ciérralo antes de lanzar otra prueba.'
}

$run = Join-Path $repo ("dist\sandbox\" + (Get-Date -Format 'yyyyMMdd-HHmmss'))
$stage = Join-Path $run 'clon'
$results = Join-Path $run 'resultados'
New-Item -ItemType Directory -Force $stage, $results | Out-Null

Write-Host "Compilando en $stage…"
& $go -C $repo build -o "$stage\clon.exe" ./cmd/clon
& $go -C $repo build -o "$stage\clon-shim.exe" ./cmd/clon-shim
& $go -C $repo build -ldflags '-H=windowsgui' -o "$stage\clonw.exe" ./cmd/clonw
if ($LASTEXITCODE -ne 0) { throw 'la compilación falló' }
# With a BOM: Windows PowerShell 5.1 reads a .ps1 without one as ANSI and garbles the accents.
$inner = [IO.File]::ReadAllText((Join-Path $PSScriptRoot 'dentro-del-sandbox.ps1'), [Text.Encoding]::UTF8)
[IO.File]::WriteAllText((Join-Path $stage 'dentro-del-sandbox.ps1'), $inner, (New-Object System.Text.UTF8Encoding $true))

# A clean Windows lacks the Visual C++ runtime; downloading it inside the sandbox proved unreliable,
# so the official installer is fetched here (cached) and shared read-only. clon checks its signature.
$cache = Join-Path $repo 'dist\cache'
$vc = Join-Path $cache 'vc_redist.x64.exe'
if (-not (Test-Path $vc)) {
    New-Item -ItemType Directory -Force $cache | Out-Null
    Write-Host 'Descargando el runtime de Visual C++ de Microsoft…'
    & curl.exe -sSL --fail -o $vc 'https://aka.ms/vs/17/release/vc_redist.x64.exe'
    if ($LASTEXITCODE -ne 0) { Remove-Item $vc -ErrorAction SilentlyContinue; throw 'no se pudo descargar vc_redist.x64.exe' }
}
Copy-Item $vc $stage

$wsb = @"
<Configuration>
  <VGpu>Disable</VGpu>
  <Networking>Enable</Networking>
  <MemoryInMB>$MemoryMB</MemoryInMB>
  <MappedFolders>
    <MappedFolder>
      <HostFolder>$stage</HostFolder>
      <SandboxFolder>C:\clon-dist</SandboxFolder>
      <ReadOnly>true</ReadOnly>
    </MappedFolder>
    <MappedFolder>
      <HostFolder>C:\laragon\bin</HostFolder>
      <SandboxFolder>C:\laragon\bin</SandboxFolder>
      <ReadOnly>true</ReadOnly>
    </MappedFolder>
    <MappedFolder>
      <HostFolder>$results</HostFolder>
      <SandboxFolder>C:\resultados</SandboxFolder>
      <ReadOnly>false</ReadOnly>
    </MappedFolder>
  </MappedFolders>
  <LogonCommand>
    <Command>powershell.exe -NoProfile -ExecutionPolicy Bypass -File C:\clon-dist\dentro-del-sandbox.ps1</Command>
  </LogonCommand>
</Configuration>
"@
$wsbPath = Join-Path $run 'clon.wsb'
[IO.File]::WriteAllText($wsbPath, $wsb, (New-Object System.Text.UTF8Encoding $false))

Write-Host 'Abriendo Windows Sandbox (no cierres su ventana hasta que diga "Prueba terminada")…'
# Opened through Explorer, as a double click would, so the sandbox is not a child of this script:
# if whoever runs the script stops it, the test inside still finishes and leaves its results.
$sandboxProcs = 'WindowsSandbox', 'WindowsSandboxClient', 'WindowsSandboxRemoteSession'
Start-Process -FilePath (Join-Path $env:SystemRoot 'explorer.exe') -ArgumentList "`"$wsbPath`""
$openBy = (Get-Date).AddSeconds(45)
while (-not (Get-Process $sandboxProcs -ErrorAction SilentlyContinue) -and (Get-Date) -lt $openBy) { Start-Sleep -Seconds 1 }
if (-not (Get-Process $sandboxProcs -ErrorAction SilentlyContinue)) {
    Write-Host 'Explorer no abrió el sandbox; lo abro directamente.'
    Start-Process -FilePath $sandboxExe -ArgumentList "`"$wsbPath`""
}

$done = Join-Path $results 'done.json'
$deadline = (Get-Date).AddMinutes($TimeoutMin)
$seen = 0
while (-not (Test-Path $done) -and (Get-Date) -lt $deadline) {
    Start-Sleep -Seconds 10
    $reportFile = Join-Path $results 'report.json'
    if (Test-Path $reportFile) {
        # PowerShell 5.1 returns a JSON array as one object; ForEach-Object unrolls it.
        try { $steps = @((Get-Content $reportFile -Raw -Encoding UTF8 | ConvertFrom-Json) | ForEach-Object { $_ }) } catch { continue }
        for (; $seen -lt $steps.Count; $seen++) {
            $s = $steps[$seen]
            Write-Host ("{0,-6} {1,6} s  {2}" -f $(if ($s.ok) { 'OK' } else { 'FALLO' }), $s.segundos, $s.paso)
        }
    }
}
if (-not (Test-Path $done)) { throw "la prueba no terminó en $TimeoutMin minutos; mira $results" }
Write-Host "Resultados: $results"
Get-Content $done -Raw
