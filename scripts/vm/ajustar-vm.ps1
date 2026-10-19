# Rehace el checkpoint «limpia» de la VM de pruebas con los ajustes de ahora: hasta cuánta memoria
# usa, una MAC fija y, si se indica, otra contraseña para la cuenta clon (en Windows y en su inicio
# de sesión automático; se guarda cifrada para tu usuario junto a la VM). Al terminar apaga la VM.
# Basta con estar en el grupo «Administradores de Hyper-V».
#   .\ajustar-vm.ps1 [-Password <nueva>] [-MemoryGB 6] [-VmRoot D:\VMs]
param(
    [string]$Password,
    [ValidateRange(2, 64)] [int]$MemoryGB = 6,
    [string]$VmRoot = 'C:\VMs',
    [string]$Name = 'clon-pruebas'
)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'comun.ps1')
$credFile = Join-Path (Join-Path $VmRoot $Name) 'credencial.xml'
$cred = Import-Clixml $credFile

function Say([string]$text) { Write-Host "$(Get-Date -Format 'HH:mm:ss')  $text" -ForegroundColor Cyan }

Say 'Volviendo la VM al checkpoint «limpia»'
if ((Get-VM -Name $Name).State -ne 'Off') { Stop-VM -Name $Name -TurnOff -Force }
Restore-VMCheckpoint -VMName $Name -Name 'limpia' -Confirm:$false
Start-VM -Name $Name
Wait-ClonDesktop $Name $cred 5

if ($Password) {
    Say 'Cambiando la contraseña de la cuenta clon'
    Invoke-Command -VMName $Name -Credential $cred -ArgumentList $Password -ScriptBlock {
        param($pw)
        net user clon $pw | Out-Null
        if ($LASTEXITCODE -ne 0) { throw "net user terminó con el código $LASTEXITCODE" }
        # Windows signs in by itself with the password Setup kept as an LSA secret (DefaultPassword).
        Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
public static class ClonLsa {
    [StructLayout(LayoutKind.Sequential)] struct Str { public ushort Length; public ushort MaximumLength; public IntPtr Buffer; }
    [StructLayout(LayoutKind.Sequential)] struct Attrs { public int Length; public IntPtr Root; public IntPtr Name; public uint Attributes; public IntPtr Sd; public IntPtr Sqos; }
    [DllImport("advapi32.dll")] static extern uint LsaOpenPolicy(IntPtr system, ref Attrs attrs, uint access, out IntPtr policy);
    [DllImport("advapi32.dll")] static extern uint LsaStorePrivateData(IntPtr policy, ref Str key, ref Str data);
    [DllImport("advapi32.dll")] static extern uint LsaClose(IntPtr policy);
    static Str Make(string s) {
        Str u = new Str();
        u.Buffer = Marshal.StringToHGlobalUni(s);
        u.Length = (ushort)(s.Length * 2);
        u.MaximumLength = (ushort)(s.Length * 2 + 2);
        return u;
    }
    public static void Store(string key, string value) {
        Attrs attrs = new Attrs();
        IntPtr policy;
        uint r = LsaOpenPolicy(IntPtr.Zero, ref attrs, 0x000F0FFF, out policy); // POLICY_ALL_ACCESS
        if (r != 0) throw new Exception("LsaOpenPolicy: " + r);
        Str k = Make(key), v = Make(value);
        r = LsaStorePrivateData(policy, ref k, ref v);
        LsaClose(policy);
        if (r != 0) throw new Exception("LsaStorePrivateData: " + r);
    }
}
'@
        [ClonLsa]::Store('DefaultPassword', $pw)
        $winlogon = 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon'
        Set-ItemProperty $winlogon -Name AutoAdminLogon -Value '1'
        Set-ItemProperty $winlogon -Name DefaultUserName -Value 'clon'
        Remove-ItemProperty $winlogon -Name DefaultPassword -ErrorAction SilentlyContinue
    }
    $cred = New-Object PSCredential ('clon', (ConvertTo-SecureString $Password -AsPlainText -Force))
}

# A clean shutdown through the integration services. Windows installs then the updates it downloaded
# on its first start, before Windows Update was turned off (the tests always cut the power): it
# can take a while once, and leaves «limpia» without pending updates.
Say "Apagando Windows para poner la memoria (hasta $MemoryGB GB) y la MAC fija (si instala actualizaciones, tarda)"
Stop-VM -Name $Name -Force
Set-ClonVmHardware $Name $MemoryGB

# Signing in again by itself also proves the new password reached the automatic sign-in.
Say 'Arrancando de nuevo: Windows entra solo en la cuenta clon'
Start-VM -Name $Name
Wait-ClonDesktop $Name $cred 45
Start-Sleep -Seconds 60 # right after signing in, Windows is still starting things

Say 'Guardando el checkpoint «limpia» de nuevo'
Remove-VMCheckpoint -VMName $Name -Name 'limpia'
while ((Get-VM -Name $Name).OperationalStatus -contains 'MergingDisks') { Start-Sleep -Seconds 5 }
Checkpoint-VM -Name $Name -SnapshotName 'limpia'
if ($Password) { $cred | Export-Clixml $credFile }
# Every test starts from «limpia»: nothing to keep, and the memory goes back to this computer.
Stop-VM -Name $Name -TurnOff -Force
Say "Lista: la VM $Name usa hasta $MemoryGB GB y queda apagada hasta la próxima prueba."
