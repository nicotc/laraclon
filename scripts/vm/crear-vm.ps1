# Crea la máquina virtual de Hyper-V donde se prueba clon de punta a punta. Windows se aplica
# directamente al disco virtual desde el ISO (sin asistente de instalación): una cuenta de
# administrador «clon» que entra sola, la carpeta bin de Laragon copiada dentro y un checkpoint
# «limpia» con la sesión ya iniciada, al que vuelve cada prueba.
# Necesita permisos de administrador: lo lanza 'probar-en-vm.ps1 -Crear', que pide el permiso.
param(
    [Parameter(Mandatory)] [string]$Iso,
    [Parameter(Mandatory)] [string]$PasswordFile, # contraseña de la cuenta clon; se borra al leerla
    [string]$VmRoot = 'C:\VMs',
    [string]$Name = 'clon-pruebas',
    [ValidateRange(2, 64)] [int]$MemoryGB = 6,
    [int]$Cpu = 4,
    [int]$DiskGB = 128,
    [string]$LaragonBin = 'C:\laragon\bin',
    [string]$TimeZone = (Get-TimeZone).Id,
    [string]$AddUser = '' # usuario que entra en «Administradores de Hyper-V» (DOMINIO\usuario)
)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
. (Join-Path $PSScriptRoot 'comun.ps1')
$vmDir = Join-Path $VmRoot $Name
New-Item -ItemType Directory -Force $vmDir | Out-Null
Start-Transcript -Path (Join-Path $vmDir 'crear-vm.log') -Force | Out-Null

function Say([string]$text) { Write-Host "$(Get-Date -Format 'HH:mm:ss')  $text" -ForegroundColor Cyan }

$password = (Get-Content $PasswordFile -Raw).Trim()
Remove-Item $PasswordFile -Force
$cred = New-Object PSCredential ('clon', (ConvertTo-SecureString $password -AsPlainText -Force))
$vhdx = Join-Path $vmDir "Virtual Hard Disks\$Name.vhdx"
$isoMounted = $false
$vhdMounted = $false
try {
    if ($AddUser) {
        # Hyper-V Administrators manage VMs without elevating; it takes effect on the next sign-in.
        if (-not (Get-LocalGroupMember -SID 'S-1-5-32-578' | Where-Object { $_.Name -eq $AddUser })) {
            Add-LocalGroupMember -SID 'S-1-5-32-578' -Member $AddUser
            Say "$AddUser añadido a «Administradores de Hyper-V» (vale desde el próximo inicio de sesión)"
        }
    }
    if (Get-VM -Name $Name -ErrorAction SilentlyContinue) { throw "ya existe una VM llamada $Name" }
    if (Test-Path $vhdx) {
        Say 'Borrando el disco a medio hacer de un intento anterior'
        Dismount-VHD -Path $vhdx -ErrorAction SilentlyContinue
        Remove-Item $vhdx -Force
    }

    Say "Montando $Iso"
    $isoImage = Mount-DiskImage -ImagePath $Iso -PassThru
    $isoMounted = $true
    $isoLetter = ($isoImage | Get-Volume).DriveLetter
    $wim = "$($isoLetter):\sources\install.wim"
    if (-not (Test-Path $wim)) { $wim = "$($isoLetter):\sources\install.esd" }
    $edition = $null
    foreach ($img in Get-WindowsImage -ImagePath $wim) {
        $info = Get-WindowsImage -ImagePath $wim -Index $img.ImageIndex
        if ($info.EditionId -eq 'Professional') { $edition = $info; break }
    }
    if (-not $edition) { throw "el ISO no trae Windows Pro" }
    $language = @($edition.Languages)[0]
    $admins = if ($language -like 'es-*') { 'Administradores' } else { 'Administrators' }
    Say "Edición: $($edition.ImageName) $($edition.Version) ($language)"

    Say "Creando el disco virtual de $DiskGB GB (dinámico)"
    New-Item -ItemType Directory -Force (Split-Path $vhdx) | Out-Null
    New-VHD -Path $vhdx -SizeBytes ($DiskGB * 1GB) -Dynamic | Out-Null
    $disk = Mount-VHD -Path $vhdx -Passthru | Get-Disk
    $vhdMounted = $true
    Initialize-Disk -Number $disk.Number -PartitionStyle GPT
    $efi = New-Partition -DiskNumber $disk.Number -Size 260MB -GptType '{c12a7328-f81f-11d2-ba4b-00a0c93ec93b}'
    Format-Volume -Partition $efi -FileSystem FAT32 -NewFileSystemLabel 'System' -Confirm:$false | Out-Null
    New-Partition -DiskNumber $disk.Number -Size 16MB -GptType '{e3c9e316-0b5c-4db8-817d-f92df00215ae}' | Out-Null
    $win = New-Partition -DiskNumber $disk.Number -UseMaximumSize -GptType '{ebd0a0a2-b9e5-4433-87c0-68b6b72699c7}'
    Format-Volume -Partition $win -FileSystem NTFS -NewFileSystemLabel 'Windows' -Confirm:$false | Out-Null
    $efi | Add-PartitionAccessPath -AssignDriveLetter
    $win | Add-PartitionAccessPath -AssignDriveLetter
    $efiLetter = (Get-Partition -DiskNumber $disk.Number -PartitionNumber $efi.PartitionNumber).DriveLetter
    $winLetter = (Get-Partition -DiskNumber $disk.Number -PartitionNumber $win.PartitionNumber).DriveLetter

    Say "Aplicando Windows al disco (unos minutos)"
    Expand-WindowsImage -ImagePath $wim -Index $edition.ImageIndex -ApplyPath "$($winLetter):\" | Out-Null
    & bcdboot.exe "$($winLetter):\Windows" /s "$($efiLetter):" /f UEFI | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "bcdboot terminó con el código $LASTEXITCODE" }

    # The first boot creates the clon account and signs in by itself, in Spanish and without the
    # online-account and network screens.
    $esc = [Security.SecurityElement]::Escape($password)
    $shell = 'processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS"'
    $unattend = @"
<?xml version="1.0" encoding="utf-8"?>
<unattend xmlns="urn:schemas-microsoft-com:unattend" xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">
  <settings pass="specialize">
    <component name="Microsoft-Windows-Shell-Setup" $shell>
      <ComputerName>CLON-PRUEBAS</ComputerName>
      <TimeZone>$TimeZone</TimeZone>
    </component>
    <component name="Microsoft-Windows-Deployment" $shell>
      <RunSynchronous>
        <RunSynchronousCommand wcm:action="add">
          <Order>1</Order>
          <Path>reg add HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\OOBE /v BypassNRO /t REG_DWORD /d 1 /f</Path>
        </RunSynchronousCommand>
      </RunSynchronous>
    </component>
  </settings>
  <settings pass="oobeSystem">
    <component name="Microsoft-Windows-International-Core" $shell>
      <InputLocale>$language</InputLocale>
      <SystemLocale>$language</SystemLocale>
      <UILanguage>$language</UILanguage>
      <UserLocale>$language</UserLocale>
    </component>
    <component name="Microsoft-Windows-Shell-Setup" $shell>
      <OOBE>
        <HideEULAPage>true</HideEULAPage>
        <HideOEMRegistrationScreen>true</HideOEMRegistrationScreen>
        <HideOnlineAccountScreens>true</HideOnlineAccountScreens>
        <HideWirelessSetupInOOBE>true</HideWirelessSetupInOOBE>
        <ProtectYourPC>3</ProtectYourPC>
      </OOBE>
      <UserAccounts>
        <LocalAccounts>
          <LocalAccount wcm:action="add">
            <Name>clon</Name>
            <DisplayName>clon</DisplayName>
            <Group>$admins</Group>
            <Password><Value>$esc</Value><PlainText>true</PlainText></Password>
          </LocalAccount>
        </LocalAccounts>
      </UserAccounts>
      <AutoLogon>
        <Enabled>true</Enabled>
        <Username>clon</Username>
        <LogonCount>999999</LogonCount>
        <Password><Value>$esc</Value><PlainText>true</PlainText></Password>
      </AutoLogon>
    </component>
  </settings>
</unattend>
"@
    New-Item -ItemType Directory -Force "$($winLetter):\Windows\Panther" | Out-Null
    [IO.File]::WriteAllText("$($winLetter):\Windows\Panther\unattend.xml", $unattend, (New-Object Text.UTF8Encoding $false))

    if ($LaragonBin -and (Test-Path $LaragonBin)) {
        Say "Copiando $LaragonBin a la VM (unos minutos)"
        $copyLog = Join-Path $vmDir 'robocopy-laragon.log'
        & robocopy.exe $LaragonBin "$($winLetter):\laragon\bin" /E /R:1 /W:1 /MT:16 /NFL /NDL /NP "/LOG:$copyLog" | Out-Null
        if ($LASTEXITCODE -ge 16) { throw "robocopy no pudo copiar $LaragonBin (código $LASTEXITCODE; mira $copyLog)" }
        # 8 and up: some files failed (Laragon running keeps a few open). The tests only need its
        # programs, so the copy goes on and the log says which.
        if ($LASTEXITCODE -ge 8) { Say "Aviso: algunos archivos de Laragon no se copiaron (en uso o sin permiso); detalle en $copyLog" }
    }

    Dismount-VHD -Path $vhdx
    $vhdMounted = $false
    Dismount-DiskImage -ImagePath $Iso | Out-Null
    $isoMounted = $false

    Say "Creando la VM $Name ($Cpu procesadores, hasta $MemoryGB GB)"
    $switch = Get-VMSwitch -Name 'Default Switch' -ErrorAction SilentlyContinue
    if (-not $switch) { $switch = Get-VMSwitch -SwitchType Internal | Select-Object -First 1 }
    if (-not $switch) { throw 'no hay ningún conmutador virtual (el «Default Switch» de Hyper-V da Internet a la VM)' }
    New-VM -Name $Name -Generation 2 -MemoryStartupBytes 2GB -VHDPath $vhdx -Path $VmRoot -SwitchName $switch.Name | Out-Null
    Set-VMProcessor -VMName $Name -Count $Cpu
    Set-ClonVmHardware $Name $MemoryGB
    Set-VMFirmware -VMName $Name -EnableSecureBoot On -SecureBootTemplate MicrosoftWindows
    try {
        Set-VMKeyProtector -VMName $Name -NewLocalKeyProtector
        Enable-VMTPM -VMName $Name
    } catch { Say "sin TPM virtual: $($_.Exception.Message)" }
    # Guest Services lets the host copy files into the VM.
    Get-VMIntegrationService -VMName $Name | Where-Object { -not $_.Enabled } | Enable-VMIntegrationService
    Set-VM -Name $Name -AutomaticCheckpointsEnabled $false -CheckpointType Standard -AutomaticStopAction Save

    Say 'Arrancando Windows por primera vez (crea la cuenta y entra solo; unos minutos)'
    Start-VM -Name $Name
    Wait-ClonDesktop $Name $cred 30

    Say 'Preparando Windows para las pruebas'
    Invoke-Command -VMName $Name -Credential $cred -ScriptBlock {
        # A test machine that never sleeps, locks or updates itself in the middle of a run.
        powercfg /change monitor-timeout-ac 0
        powercfg /change standby-timeout-ac 0
        powercfg /hibernate off
        Stop-Service wuauserv -Force -ErrorAction SilentlyContinue
        Set-Service wuauserv -StartupType Disabled -ErrorAction SilentlyContinue
        $au = 'HKLM:\SOFTWARE\Policies\Microsoft\Windows\WindowsUpdate\AU'
        New-Item -Path $au -Force | Out-Null
        New-ItemProperty -Path $au -Name NoAutoUpdate -Value 1 -PropertyType DWord -Force | Out-Null
        $edge = 'HKLM:\SOFTWARE\Policies\Microsoft\Edge'
        New-Item -Path $edge -Force | Out-Null
        New-ItemProperty -Path $edge -Name HideFirstRunExperience -Value 1 -PropertyType DWord -Force | Out-Null
        New-ItemProperty -Path 'HKCU:\Control Panel\Desktop' -Name ScreenSaveActive -Value '0' -Force | Out-Null
        # Requests for administrator rights are granted without a prompt nobody would answer.
        Set-ItemProperty -Path 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System' -Name ConsentPromptBehaviorAdmin -Value 0 -Type DWord
        "Windows $((Get-CimInstance Win32_OperatingSystem).Caption) build $([Environment]::OSVersion.Version.Build)"
    } | ForEach-Object { Say $_ }
    # A clean restart before the checkpoint: Windows installs the updates it downloaded on its first
    # start, before Windows Update was turned off. Otherwise «limpia» keeps them pending, and they
    # get installed whenever the VM shuts down properly.
    Say 'Reiniciando Windows una vez (instala las actualizaciones que bajó al arrancar; puede tardar)'
    Stop-VM -Name $Name -Force
    Start-VM -Name $Name
    Wait-ClonDesktop $Name $cred 45
    Start-Sleep -Seconds 60 # right after signing in, Windows is still starting things

    Say 'Guardando el checkpoint «limpia» (con la sesión iniciada)'
    Checkpoint-VM -Name $Name -SnapshotName 'limpia'
    Say "Lista: VM $Name en $vmDir. Cada prueba vuelve al checkpoint «limpia»."
} catch {
    Write-Host "ERROR: $($_.Exception.Message)" -ForegroundColor Red
    if ($vhdMounted) { Dismount-VHD -Path $vhdx -ErrorAction SilentlyContinue }
    if ($isoMounted) { Dismount-DiskImage -ImagePath $Iso -ErrorAction SilentlyContinue | Out-Null }
    Stop-Transcript | Out-Null
    exit 1
}
Stop-Transcript | Out-Null
exit 0
