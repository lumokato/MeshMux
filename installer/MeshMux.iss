#define MyAppName "MeshMux"
#define MyAppVersion GetEnv("MESHMUX_VERSION")
#if MyAppVersion == ""
#define MyAppVersion "0.3.0"
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
DefaultDirName={autopf}\MeshMux
UsePreviousAppDir=no
DefaultGroupName=MeshMux
DisableProgramGroupPage=yes
OutputDir=..\release
OutputBaseFilename=MeshMux-Setup-{#MyAppVersion}
Compression=lzma
SolidCompression=yes
SetupLogging=yes
WizardStyle=modern
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
PrivilegesRequired=admin
UsedUserAreasWarning=no
SetupIconFile=..\assets\meshmux.ico
UninstallDisplayIcon={app}\MeshMux.exe
LicenseFile=..\LICENSE

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

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
Name: "{userstartup}\MeshMux"; Filename: "{app}\MeshMux.exe"

[Run]
Filename: "{app}\MeshMux.exe"; Description: "Launch MeshMux"; Flags: nowait postinstall skipifsilent runasoriginaluser

[Registry]
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueType: none; ValueName: "MeshMux"; Flags: deletevalue uninsdeletevalue

[UninstallRun]
Filename: "{app}\meshmux-cli.exe"; Parameters: "service stop"; Flags: runhidden waituntilterminated skipifdoesntexist; RunOnceId: "StopMeshMuxService"
Filename: "{app}\meshmux-cli.exe"; Parameters: "service remove"; Flags: runhidden waituntilterminated skipifdoesntexist; RunOnceId: "RemoveMeshMuxService"

[Code]
function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  ResultCode: Integer;
  StatusCode: Integer;
  ServiceCLI: String;
begin
  Result := '';
  ServiceCLI := ExpandConstant('{app}\meshmux-cli.exe');
  if FileExists(ServiceCLI) and
     Exec(ServiceCLI, 'service status', '', SW_HIDE, ewWaitUntilTerminated, StatusCode) and
     (StatusCode = 0) then
  begin
    if (not Exec(ServiceCLI, 'service stop', '', SW_HIDE, ewWaitUntilTerminated, ResultCode)) or
       (ResultCode <> 0) then
      Result := Format('MeshMux Core could not be stopped (exit code %d). Installation was not changed.', [ResultCode]);
  end;
end;

procedure CurStepChanged(CurStep: TSetupStep);
var
  ResultCode: Integer;
  ServiceCLI: String;
  UserConfig: String;
begin
  if CurStep <> ssPostInstall then
    exit;

  Exec(ExpandConstant('{cmd}'), '/c taskkill /IM MeshMux.exe /F >nul 2>&1', '', SW_HIDE,
    ewWaitUntilTerminated, ResultCode);
  ServiceCLI := ExpandConstant('{app}\meshmux-cli.exe');
  UserConfig := ExpandConstant('{localappdata}\MeshMux\meshmux.local.json');
  ResultCode := -1;
  if (not Exec(ServiceCLI, 'service activate-if-ready -config "' + UserConfig + '"', '', SW_HIDE,
    ewWaitUntilTerminated, ResultCode)) or (ResultCode <> 0) then
    RaiseException(Format('MeshMux service activation failed (exit code %d). See %%LocalAppData%%\MeshMux\logs\service-command.log.', [ResultCode]));
end;
