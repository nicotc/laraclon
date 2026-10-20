# Compila Nido y lo prueba de punta a punta en una VM de Hyper-V: la vuelve al checkpoint «limpia»,
# le copia Nido por PowerShell Direct (sin red), ejecuta dentro-de-la-vm.ps1 en la sesión de Windows
# y trae los resultados a Nido\dist\vm\<fecha>\resultados. Al terminar apaga la VM, para que su
# memoria vuelva al equipo (con -DejarEncendida queda encendida para mirarla).
#
# La primera vez crea la VM (pide permiso de administrador una vez):
#   .\probar-en-vm.ps1 -Crear -Iso <ISO de Windows 11> [-VmRoot D:\VMs]
# Después, cada prueba:
#   .\probar-en-vm.ps1 [-VmRoot D:\VMs] [-DejarEncendida]
# La memoria máxima y la contraseña de la VM se cambian con ajustar-vm.ps1.
param(
    [string]$VmRoot = 'C:\VMs',
    [string]$Name = 'nido-pruebas',
    [switch]$Crear,
    [string]$Iso,
    [string]$Password, # contraseña de la cuenta Nido de la VM nueva ("" = una al azar)
    [ValidateRange(2, 64)] [int]$MemoryGB = 6,
    [int]$TimeoutMin = 120,
    [switch]$DejarEncendida
)
$ErrorActionPreference = 'Stop'
$repo = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$vmDir = Join-Path $VmRoot $Name
$credFile = Join-Path $vmDir 'credencial.xml'
$bom = New-Object System.Text.UTF8Encoding $true

if ($Crear) {
    if (-not $Iso -or -not (Test-Path $Iso)) { throw 'Indica el ISO de Windows 11 con -Iso.' }
    New-Item -ItemType Directory -Force $vmDir | Out-Null
    # The VM account's password (random unless given), kept next to the VM encrypted for this
    # Windows user (DPAPI).
    $password = $Password
    if (-not $password) {
        $chars = 'abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789'
        $bytes = New-Object byte[] 20
        [Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($bytes)
        $password = -join ($bytes | ForEach-Object { $chars[$_ % $chars.Length] })
    }
    (New-Object PSCredential ('nido', (ConvertTo-SecureString $password -AsPlainText -Force))) | Export-Clixml $credFile
    $passwordFile = Join-Path $vmDir 'clave.tmp'
    [IO.File]::WriteAllText($passwordFile, $password)
    $script = Join-Path $PSScriptRoot 'crear-vm.ps1'
    $user = [Security.Principal.WindowsIdentity]::GetCurrent().Name
    $arguments = "-NoProfile -ExecutionPolicy Bypass -File `"$script`" -Iso `"$Iso`" -PasswordFile `"$passwordFile`" " +
        "-VmRoot `"$VmRoot`" -Name $Name -MemoryGB $MemoryGB -AddUser `"$user`""
    Write-Host 'Windows pedirá permiso de administrador para crear la VM; el avance se ve en esa ventana.'
    $p = Start-Process powershell.exe -Verb RunAs -ArgumentList $arguments -PassThru -Wait
    Remove-Item $passwordFile -ErrorAction SilentlyContinue
    if ($p.ExitCode -ne 0) { throw "No se pudo crear la VM; mira $vmDir\crear-vm.log" }
    Write-Host "VM $Name creada en $vmDir."
    Write-Host 'Cierra sesión en Windows y vuelve a entrar una vez: así tu usuario maneja Hyper-V sin pedir permiso.'
    return
}

# Everything this run prints goes to progreso.log too (the window may be elevated and close at the end).
$run = Join-Path $repo ("dist\vm\" + (Get-Date -Format 'yyyyMMdd-HHmmss'))
$stage = Join-Path $run 'nido'
$results = Join-Path $run 'resultados'
New-Item -ItemType Directory -Force $stage, $results | Out-Null
Start-Transcript -Path (Join-Path $run 'progreso.log') -Force | Out-Null

try {
    $vm = Get-VM -Name $Name -ErrorAction Stop
} catch {
    throw "No se puede usar la VM $Name ($($_.Exception.Message)). Si acabas de crearla, cierra sesión y vuelve a entrar (grupo «Administradores de Hyper-V»); si no existe, créala con -Crear -Iso <ISO>."
}
if (-not (Get-VMSnapshot -VMName $Name -Name 'limpia' -ErrorAction SilentlyContinue)) { throw "La VM $Name no tiene el checkpoint «limpia»." }
$cred = Import-Clixml $credFile

# Go: $env:NIDO_GO if set, otherwise the go on PATH, otherwise a portable toolchain next to the repo.
$go = $env:NIDO_GO
if (-not $go) { $go = (Get-Command go -ErrorAction SilentlyContinue).Source }
if (-not $go) { $go = Join-Path (Split-Path -Parent $repo) '.toolchain\go.ps1' }
if (-not (Test-Path $go)) { throw 'No se encontró Go 1.27 o posterior (instálalo de go.dev o define NIDO_GO).' }

Write-Host "Compilando en $stage…"
& $go -C $repo build -o "$stage\nido.exe" ./cmd/nido
& $go -C $repo build -o "$stage\nido-shim.exe" ./cmd/nido-shim
& $go -C $repo build -ldflags '-H=windowsgui' -o "$stage\nidow.exe" ./cmd/nidow
if ($LASTEXITCODE -ne 0) { throw 'la compilación falló' }
# With a BOM: Windows PowerShell 5.1 reads a .ps1 without one as ANSI and garbles the accents.
$inner = [IO.File]::ReadAllText((Join-Path $PSScriptRoot 'dentro-de-la-vm.ps1'), [Text.Encoding]::UTF8)
[IO.File]::WriteAllText((Join-Path $stage 'dentro-de-la-vm.ps1'), $inner, $bom)

# The clean Windows lacks the Visual C++ runtime: the official installers are fetched here (cached).
# Nido checks their signature. First the one of Visual Studio 2022 (14.44) on purpose, later the
# newest, which Apache Lounge's Apache (built with Visual Studio 2026) asks for.
$cache = Join-Path $repo 'dist\cache'
New-Item -ItemType Directory -Force $cache | Out-Null
foreach ($vcFile in @(@{ name = 'vc_redist.x64.exe'; url = 'https://aka.ms/vs/17/release/vc_redist.x64.exe' },
                      @{ name = 'vc_redist-reciente.x64.exe'; url = 'https://aka.ms/vc14/vc_redist.x64.exe' })) {
    $vc = Join-Path $cache $vcFile.name
    if (-not (Test-Path $vc)) {
        Write-Host "Descargando $($vcFile.name) de Microsoft…"
        & curl.exe -sSL --fail -o $vc $vcFile.url
        if ($LASTEXITCODE -ne 0) { Remove-Item $vc -ErrorAction SilentlyContinue; throw "no se pudo descargar $($vcFile.name)" }
    }
    Copy-Item $vc $stage
}

# PowerShell Direct goes through Hyper-V itself: no network, firewall or remoting setup.
function Connect-Vm {
    $by = (Get-Date).AddMinutes(5)
    while ((Get-Date) -lt $by) {
        try { return New-PSSession -VMName $Name -Credential $cred -ErrorAction Stop } catch { Start-Sleep -Seconds 5 }
    }
    throw 'no se pudo conectar con la VM por PowerShell Direct'
}

$done = $false
try {
    Write-Host 'Volviendo la VM al checkpoint «limpia»…'
    if ((Get-VM -Name $Name).State -ne 'Off') { Stop-VM -Name $Name -TurnOff -Force } # its state is thrown away anyway
    Restore-VMCheckpoint -VMName $Name -Name 'limpia' -Confirm:$false
    Start-VM -Name $Name # resumes the signed-in session saved in the checkpoint

    $s = Connect-Vm
    Invoke-Command -Session $s -ArgumentList (Get-Date) -ScriptBlock {
        param($now)
        # The checkpoint's clock is behind until Hyper-V syncs it; certificates need the real time.
        try { Set-Date $now -ErrorAction Stop | Out-Null } catch {}
        Remove-Item C:\nido-dist, C:\resultados -Recurse -Force -ErrorAction SilentlyContinue
        New-Item -ItemType Directory -Force C:\nido-dist, C:\resultados | Out-Null
    }
    # After this computer restarts, the «Default Switch» hands out other addresses than the ones saved
    # in the checkpoint: the VM asks again, and the test only starts with Internet.
    $online = Invoke-Command -Session $s -ScriptBlock {
        # A plain renew asks the old network's server, which no longer answers, and keeps the old
        # address: releasing it first makes the VM ask the network the switch has now.
        ipconfig /release | Out-Null
        ipconfig /renew | Out-Null
        Clear-DnsClientCache
        foreach ($i in 1..12) {
            try { Resolve-DnsName github.com -DnsOnly -ErrorAction Stop | Out-Null; return $true } catch { Start-Sleep -Seconds 5 }
        }
        $false
    }
    if (-not $online) { throw 'La VM no tiene Internet (no resuelve github.com); revisa el «Default Switch» de Hyper-V.' }
    Write-Host 'Copiando Nido a la VM…'
    Copy-Item -ToSession $s -Path "$stage\*" -Destination 'C:\nido-dist' -Recurse
    # The test runs in the signed-in session (tray icon, Edge) the way a user starts it: a scheduled task
    # with the user's own token asks for administrator rights, which this test machine grants without
    # asking, as if someone pressed «Sí». (A task started with the highest rights directly gets a token
    # from another logon, and processes Nido starts without rights, such as PostgreSQL, cannot reach the
    # desktop: 0xC0000142.)
    Invoke-Command -Session $s -ScriptBlock {
        $uac = 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System'
        Set-ItemProperty -Path $uac -Name ConsentPromptBehaviorAdmin -Value 0 -Type DWord
        $inner = "Start-Process powershell.exe -Verb RunAs -ArgumentList '-NoProfile -ExecutionPolicy Bypass -File C:\nido-dist\dentro-de-la-vm.ps1'"
        $action = New-ScheduledTaskAction -Execute 'powershell.exe' -Argument "-NoProfile -WindowStyle Hidden -Command `"$inner`""
        $principal = New-ScheduledTaskPrincipal -UserId 'nido' -LogonType Interactive -RunLevel Limited
        $settings = New-ScheduledTaskSettingsSet -ExecutionTimeLimit (New-TimeSpan -Hours 4) -AllowStartIfOnBatteries
        Register-ScheduledTask -TaskName 'nido-prueba' -Action $action -Principal $principal -Settings $settings -Force | Out-Null
        Start-ScheduledTask -TaskName 'nido-prueba'
    }
    Write-Host "Prueba en marcha dentro de la VM (se ve con: vmconnect.exe localhost $Name)"
    # The test writes its transcript at once; without it in a few minutes, the task did not start.
    $by = (Get-Date).AddMinutes(3)
    do {
        Start-Sleep -Seconds 5
        $started = Invoke-Command -Session $s -ScriptBlock { Test-Path C:\resultados\transcript.txt }
    } while (-not $started -and (Get-Date) -lt $by)
    if (-not $started) {
        $info = Invoke-Command -Session $s -ScriptBlock { Get-ScheduledTaskInfo -TaskName 'nido-prueba' | Select-Object LastRunTime, LastTaskResult }
        throw "La prueba no arrancó dentro de la VM (tarea nido-prueba: última ejecución $($info.LastRunTime), resultado $($info.LastTaskResult))."
    }

    $deadline = (Get-Date).AddMinutes($TimeoutMin)
    $seen = 0
    $peakVm = [double]0 # bytes: an Int32 would overflow past 2 GB
    $minFree = [double]::MaxValue
    while (-not $done -and (Get-Date) -lt $deadline) {
        Start-Sleep -Seconds 15
        # This computer's memory during the run: what the VM takes and what is left for the rest.
        $peakVm = [Math]::Max($peakVm, [double](Get-VM -Name $Name).MemoryAssigned)
        $minFree = [Math]::Min($minFree, [double](Get-CimInstance Win32_OperatingSystem).FreePhysicalMemory * 1KB)
        try {
            if ($s.State -ne 'Opened') { $s = Connect-Vm }
            # The VM reads its own report and sends back finished lines of text.
            $state = Invoke-Command -Session $s -ErrorAction Stop -ScriptBlock {
                $lines = @()
                $raw = Get-Content C:\resultados\report.json -Raw -Encoding UTF8 -ErrorAction SilentlyContinue
                if ($raw) {
                    # PowerShell 5.1 returns a JSON array as one object; ForEach-Object unrolls it.
                    try {
                        $lines = @(($raw | ConvertFrom-Json) | ForEach-Object { $_ } | Where-Object { $_.paso } | ForEach-Object {
                            '{0,-6} {1,6} s  {2}' -f $(if ($_.ok) { 'OK' } else { 'FALLO' }), $_.segundos, $_.paso })
                    } catch {} # read while being written: next round
                }
                [pscustomobject]@{ lines = [string[]]$lines; done = (Test-Path C:\resultados\done.json) }
            }
        } catch { continue } # busy or briefly unreachable: ask again
        $lines = @($state.lines)
        for (; $seen -lt $lines.Count; $seen++) { Write-Host $lines[$seen] }
        $done = $state.done
    }
    if ($s.State -ne 'Opened') { $s = Connect-Vm }
    # The test leaves Nido running for a look by hand, and its transcript may still be open meanwhile.
    Start-Sleep -Seconds 20
    foreach ($item in Invoke-Command -Session $s -ScriptBlock { Get-ChildItem C:\resultados | ForEach-Object FullName }) {
        try { Copy-Item -FromSession $s -Path $item -Destination $results -Recurse -Force -ErrorAction Stop }
        catch { Write-Host "No se pudo traer $item ($($_.Exception.Message))" }
    }

    # As on a real computer: after restarting Windows, Nido starts by itself when the user signs in
    # (the account signs in alone), quietly, and its services answer.
    Write-Host 'Reiniciando Windows en la VM: Nido tiene que arrancar solo al entrar…'
    $bootBefore = Invoke-Command -Session $s -ScriptBlock { (Get-CimInstance Win32_OperatingSystem).LastBootUpTime }
    Invoke-Command -Session $s -ScriptBlock { Restart-Computer -Force } -ErrorAction SilentlyContinue
    Remove-PSSession $s -ErrorAction SilentlyContinue
    $rebooted = $false
    $by = (Get-Date).AddMinutes(10)
    while (-not $rebooted -and (Get-Date) -lt $by) {
        Start-Sleep -Seconds 10
        try {
            $s = New-PSSession -VMName $Name -Credential $cred -ErrorAction Stop
            $boot = Invoke-Command -Session $s -ErrorAction Stop -ScriptBlock { (Get-CimInstance Win32_OperatingSystem).LastBootUpTime }
            if ($boot -ne $bootBefore) { $rebooted = $true } else { Remove-PSSession $s }
        } catch {}
    }
    $after = 'la VM no volvió después de reiniciar'
    if ($rebooted) {
        $by = (Get-Date).AddMinutes(5)
        do {
            Start-Sleep -Seconds 10
            $after = Invoke-Command -Session $s -ScriptBlock {
                $w = Get-CimInstance Win32_Process -Filter "Name='nidow.exe'" | Select-Object -First 1
                if (-not $w) { return 'Nido no arrancó' }
                $st = (& C:\nido\nido.exe status 2>&1 | Out-String)
                if ($st -notmatch 'Proxy HTTPS: en marcha') { return "Nido arrancó ($($w.CommandLine)) pero todavía no sirve: $st" }
                "OK: Nido arrancó solo al entrar en Windows ($($w.CommandLine))`n$st"
            }
        } while ($after -notmatch '^OK' -and (Get-Date) -lt $by)
    }
    Write-Host "Tras reiniciar Windows: $after"
    [IO.File]::WriteAllText((Join-Path $results 'reinicio.txt'), $after, (New-Object System.Text.UTF8Encoding $false))
    if ($DejarEncendida -and $rebooted) {
        # The person who looks at the VM finds the panel open, as after a double click on «Nido».
        Invoke-Command -Session $s -ScriptBlock {
            $action = New-ScheduledTaskAction -Execute 'C:\nido\nidow.exe'
            $principal = New-ScheduledTaskPrincipal -UserId 'nido' -LogonType Interactive -RunLevel Limited
            Register-ScheduledTask -TaskName 'nido-panel' -Action $action -Principal $principal -Force | Out-Null
            Start-ScheduledTask -TaskName 'nido-panel'
        } -ErrorAction SilentlyContinue
    }
    Remove-PSSession $s -ErrorAction SilentlyContinue
    Write-Host ('Memoria: la VM llegó a {0:N1} GB; a este equipo le quedaron como mínimo {1:N1} GB libres.' -f ($peakVm / 1GB), ($minFree / 1GB))
} catch {
    # Into progreso.log too: the transcript ends in finally, before PowerShell would print it.
    Write-Host "ERROR: $($_.Exception.Message)"
    throw
} finally {
    if ($DejarEncendida) {
        Write-Host "La VM sigue encendida, con Nido arrancado solo tras reiniciar Windows y su panel abierto: vmconnect.exe localhost $Name"
    } elseif ((Get-VM -Name $Name).State -ne 'Off') {
        # Every test starts from «limpia»: nothing to keep, and the memory goes back to this computer.
        Stop-VM -Name $Name -TurnOff -Force
        Write-Host 'VM apagada: su memoria vuelve al equipo (con -DejarEncendida queda encendida para mirarla).'
    }
    Stop-Transcript | Out-Null
}
if (-not $done) { throw "La prueba no terminó en $TimeoutMin minutos; resultados parciales en $results" }
Write-Host "Resultados: $results"
Get-Content (Join-Path $results 'done.json') -Raw
