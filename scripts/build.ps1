# Compila Nido para una o varias arquitecturas de Windows y, si hay Inno Setup, su instalador.
#
#   .\scripts\build.ps1                       amd64, versión de desarrollo
#   .\scripts\build.ps1 -Version 0.4.0 -Arch amd64,arm64 -Installer
#
# Deja en dist\<arch>\ nido.exe, nidow.exe y nido-shim.exe, un .zip por arquitectura y, con
# -Installer, dist\Nido-Setup-<versión>-<arch>.exe (necesita ISCC.exe de Inno Setup 6).
param(
    [string]$Version = "0.4.0-dev",
    [string[]]$Arch = @("amd64"),
    [switch]$Installer
)
$ErrorActionPreference = "Stop"
# "amd64,arm64" llega como una sola cadena con powershell -File.
$Arch = @($Arch | ForEach-Object { $_ -split ',' } | Where-Object { $_ })
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root
$dist = Join-Path $root "dist"
New-Item -ItemType Directory -Force $dist | Out-Null

# Sin -s -w ni -trimpath: Smart App Control bloqueó los binarios reducidos y dejó pasar los normales.
$ldflags = "-X github.com/nicotc/laraclon/internal/app.Version=$Version"
foreach ($a in $Arch) {
    $out = Join-Path $dist $a
    New-Item -ItemType Directory -Force $out | Out-Null
    $env:GOOS = "windows"; $env:GOARCH = $a; $env:CGO_ENABLED = "0"
    Write-Host "Compilando Nido $Version para windows/$a..."
    go build -ldflags $ldflags -o "$out\nido.exe" ./cmd/nido
    if ($LASTEXITCODE) { throw "go build nido ($a)" }
    go build -ldflags $ldflags -o "$out\nido-shim.exe" ./cmd/nido-shim
    if ($LASTEXITCODE) { throw "go build nido-shim ($a)" }
    go build -ldflags "$ldflags -H=windowsgui" -o "$out\nidow.exe" ./cmd/nidow
    if ($LASTEXITCODE) { throw "go build nidow ($a)" }
    Copy-Item README.md, LICENSE $out
    $zip = Join-Path $dist "Nido-$Version-windows-$a.zip"
    Remove-Item $zip -ErrorAction SilentlyContinue
    Compress-Archive -Path "$out\*" -DestinationPath $zip
    Write-Host "  $zip"

    if ($Installer) {
        $iscc = (Get-Command iscc -ErrorAction SilentlyContinue).Source
        if (-not $iscc) { $iscc = "${env:ProgramFiles(x86)}\Inno Setup 6\ISCC.exe" }
        if (-not (Test-Path $iscc)) { throw "No encuentro ISCC.exe (Inno Setup 6): instálalo con 'winget install JRSoftware.InnoSetup'" }
        & $iscc "/DAppVersion=$Version" "/DArch=$a" "/DSourceDir=$out" "/O$dist" installer\nido.iss
        if ($LASTEXITCODE) { throw "Inno Setup ($a)" }
    }
}
Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue
