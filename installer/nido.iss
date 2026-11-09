; Instalador de Nido (Inno Setup 6). Lo compila scripts\build.ps1 -Installer o la release de GitHub.
;
; Pide permiso de administrador UNA vez y deja hecho todo lo que después Nido pediría por separado:
; runtime de Visual C++, CA local de confianza para el equipo y DNS comodín de *.test (sin hosts).
; Luego, como el usuario que instala, descarga PHP, Apache, MySQL, Node y Composer ('nido setup').

#ifndef AppVersion
  #define AppVersion "0.4.0-dev"
#endif
#ifndef Arch
  #define Arch "amd64"
#endif
#ifndef SourceDir
  #define SourceDir "..\dist\" + Arch
#endif

[Setup]
AppId={{6F4C2B1E-5A7D-4E0B-9C31-7E2A1D8B9F40}
AppName=Nido
AppVersion={#AppVersion}
AppVerName=Nido {#AppVersion}
AppPublisher=Nicolas Testagrossa
AppPublisherURL=https://github.com/nicotc/laraclon
; Sin espacios: Apache, PHP y MySQL los llevan mal en sus rutas.
DefaultDirName=C:\nido
DisableDirPage=no
DisableProgramGroupPage=yes
PrivilegesRequired=admin
OutputBaseFilename=Nido-Setup-{#AppVersion}-{#Arch}
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
UninstallDisplayIcon={app}\nidow.exe
#if Arch == "arm64"
ArchitecturesAllowed=arm64
ArchitecturesInstallIn64BitMode=arm64
#else
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
#endif
MinVersion=10.0.17763

[Languages]
Name: "es"; MessagesFile: "compiler:Languages\Spanish.isl"
Name: "en"; MessagesFile: "compiler:Default.isl"

[Dirs]
; Nido escribe aquí (descargas, datos, logs) con el usuario normal, sin permisos de administrador.
Name: "{app}"; Permissions: users-modify

[Files]
Source: "{#SourceDir}\nido.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceDir}\nidow.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceDir}\nido-shim.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceDir}\README.md"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceDir}\LICENSE"; DestDir: "{app}"; Flags: ignoreversion

[Tasks]
Name: "download"; Description: "Descargar ahora PHP, Apache, MySQL, Node y Composer (unos 400 MB)"
Name: "path"; Description: "Usar php, composer, node y git en cualquier terminal (PATH de tu usuario)"

[Run]
; Como administrador, una sola vez: Visual C++, CA de confianza y DNS comodín.
Filename: "{app}\nido.exe"; Parameters: "admin-setup"; StatusMsg: "Preparando Windows (Visual C++, certificados y DNS de .test)…"; Flags: runhidden waituntilterminated
; Como el usuario que instala: lo suyo vive en {app} y en su perfil.
Filename: "{app}\nido.exe"; Parameters: "setup"; StatusMsg: "Descargando PHP, Apache, MySQL, Node y Composer…"; Flags: runasoriginaluser waituntilterminated; Tasks: download
Filename: "{app}\nido.exe"; Parameters: "path add"; Flags: runasoriginaluser runhidden waituntilterminated; Tasks: path
Filename: "{app}\nidow.exe"; Description: "Abrir Nido"; Flags: runasoriginaluser nowait postinstall skipifsilent

[UninstallRun]
Filename: "{app}\nido.exe"; Parameters: "quit"; Flags: runhidden waituntilterminated; RunOnceId: "NidoQuit"
Filename: "{app}\nido.exe"; Parameters: "admin-cleanup"; Flags: runhidden waituntilterminated; RunOnceId: "NidoCleanup"
Filename: "{app}\nido.exe"; Parameters: "path remove"; Flags: runhidden waituntilterminated; RunOnceId: "NidoPath"
Filename: "{app}\nido.exe"; Parameters: "autostart off"; Flags: runhidden waituntilterminated; RunOnceId: "NidoAutostart"

[UninstallDelete]
; Programas descargados y archivos generados. Los datos (data\) y tus proyectos (www\) se conservan.
Type: filesandordirs; Name: "{app}\bin"
Type: filesandordirs; Name: "{app}\cache"
Type: filesandordirs; Name: "{app}\generated"
Type: filesandordirs; Name: "{app}\run"
Type: filesandordirs; Name: "{app}\shims"
Type: filesandordirs; Name: "{app}\tmp"
