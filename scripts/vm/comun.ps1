# Lo que comparten crear-vm.ps1, ajustar-vm.ps1 y probar-en-vm.ps1 sobre la VM de pruebas.

# A fixed MAC outside the host's pool of dynamic ones: a VM restored from a checkpoint brings back
# its old dynamic MAC, which the «Default Switch» may have taken meanwhile when Windows reset it;
# both then fight over it and the VM is left without network for minutes (Hyper-V-VmSwitch event 28).
$ClonVmMac = '00155DC10A01'

# Dynamic memory: 2 GB to start, down to 1 GB while idle and up to MemoryGB while the tests need it.
# A running VM can only raise its maximum, so this is set with the VM off.
function Set-ClonVmHardware([string]$Name, [int]$MemoryGB) {
    Set-VMMemory -VMName $Name -DynamicMemoryEnabled $true -MinimumBytes 1GB -StartupBytes 2GB `
        -MaximumBytes ($MemoryGB * 1GB) -Buffer 10
    Set-VMNetworkAdapter -VMName $Name -StaticMacAddress $ClonVmMac
}

# Waits until Windows signed in by itself in the clon account (its explorer is running).
function Wait-ClonDesktop([string]$Name, [pscredential]$Credential, [int]$Minutes) {
    $by = (Get-Date).AddMinutes($Minutes)
    while ((Get-Date) -lt $by) {
        try {
            $up = Invoke-Command -VMName $Name -Credential $Credential -ErrorAction Stop -ScriptBlock {
                [bool](Get-Process explorer -ErrorAction SilentlyContinue)
            }
            if ($up) { return }
        } catch {}
        Start-Sleep -Seconds 10
    }
    throw "Windows no entró solo en la cuenta clon en $Minutes minutos"
}
