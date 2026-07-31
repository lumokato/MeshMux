#define MyAppName "MeshMux"
#define MyAppVersion GetEnv("MESHMUX_VERSION")
#if MyAppVersion == ""
#define MyAppVersion "0.2.1"
#endif
#define MyAppPublisher "lumokato"
#define MyAppURL "https://github.com/lumokato/MeshMux"
#define SourceDir "..\build"

[Setup]
AppId={{7F457D71-CC86-4F60-8E9F-9E1DA7E76A77}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
AppSupportURL={#MyAppURL}
AppUpdatesURL={#MyAppURL}/releases
DefaultDirName={localappdata}\Programs\MeshMux
DefaultGroupName=MeshMux
DisableProgramGroupPage=yes
OutputDir=..\release
OutputBaseFilename=MeshMux-Setup-{#MyAppVersion}
Compression=lzma
SolidCompression=yes
WizardStyle=modern
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
PrivilegesRequired=lowest
SetupIconFile=..\assets\meshmux.ico
UninstallDisplayIcon={app}\MeshMux.exe
LicenseFile=..\LICENSE

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "startup"; Description: "Start MeshMux when Windows starts"; GroupDescription: "Startup:"; Flags: unchecked

[Files]
Source: "{#SourceDir}\MeshMux.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceDir}\meshmux-cli.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceDir}\bin\mihomo.exe"; DestDir: "{app}\bin"; Flags: ignoreversion
Source: "{#SourceDir}\bin\mihomo.exe"; DestDir: "{localappdata}\MeshMux\bin"; Flags: ignoreversion onlyifdoesntexist uninsneveruninstall
Source: "{#SourceDir}\bin\geoip.metadb"; DestDir: "{app}\bin"; Flags: ignoreversion
Source: "{#SourceDir}\bin\geoip.metadb"; DestDir: "{localappdata}\MeshMux"; Flags: ignoreversion uninsneveruninstall
Source: "{#SourceDir}\dashboard\*"; DestDir: "{app}\dashboard"; Flags: ignoreversion recursesubdirs createallsubdirs
Source: "{#SourceDir}\dashboard\*"; DestDir: "{localappdata}\MeshMux\dashboard"; Flags: ignoreversion recursesubdirs createallsubdirs uninsneveruninstall
Source: "{#SourceDir}\meshmux.example.json"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceDir}\meshmux.example.json"; DestDir: "{localappdata}\MeshMux"; DestName: "meshmux.local.json"; Flags: ignoreversion onlyifdoesntexist uninsneveruninstall
Source: "{#SourceDir}\README.md"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceDir}\SECURITY.md"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceDir}\LICENSE"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceDir}\THIRD_PARTY_NOTICES.md"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\MeshMux"; Filename: "{app}\MeshMux.exe"
Name: "{autoprograms}\MeshMux"; Filename: "{app}\MeshMux.exe"

[Run]
Filename: "{app}\MeshMux.exe"; Description: "Launch MeshMux"; Flags: nowait postinstall skipifsilent

[Registry]
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueType: string; ValueName: "MeshMux"; ValueData: """{app}\MeshMux.exe"""; Tasks: startup
