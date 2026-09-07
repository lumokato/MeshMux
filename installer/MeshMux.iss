#define MyAppName "MeshMux"
#define MyAppVersion GetEnv("MESHMUX_VERSION")
#if MyAppVersion == ""
#error MESHMUX_VERSION must be set explicitly; do not package an unidentified build
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
function RunHidden(const FileName, Params: String; var ExitCode: Integer): Boolean;
begin
  Result := Exec(FileName, Params, '', SW_HIDE, ewWaitUntilTerminated, ExitCode);
end;

function BackupCurrentInstall(var ErrorMessage: String): Boolean;
var
  ExitCode: Integer;
  AppDir, BackupDir: String;
begin
  AppDir := ExpandConstant('{app}');
  BackupDir := ExpandConstant('{commonappdata}\MeshMux\install-backup');
  ForceDirectories(BackupDir);
  if not RunHidden(ExpandConstant('{cmd}'), '/c robocopy "' + AppDir + '" "' + BackupDir + '" /E /COPY:DAT /DCOPY:DAT /R:1 /W:1 /NFL /NDL /NJH /NJS', ExitCode) or (ExitCode > 7) then
  begin
    ErrorMessage := Format('无法保存当前安装现场，未修改安装目录（robocopy exit code %d）。', [ExitCode]);
    Result := False;
    exit;
  end;
  Result := True;
end;

function RestoreCurrentInstall(var ErrorMessage: String): Boolean;
var
  ExitCode: Integer;
  AppDir, BackupDir: String;
begin
  AppDir := ExpandConstant('{app}');
  BackupDir := ExpandConstant('{commonappdata}\MeshMux\install-backup');
  if not DirExists(BackupDir) then
  begin
    ErrorMessage := '找不到安装前的当前现场备份。';
    Result := False;
    exit;
  end;
  if not RunHidden(ExpandConstant('{cmd}'), '/c robocopy "' + BackupDir + '" "' + AppDir + '" /MIR /COPY:DAT /DCOPY:DAT /R:1 /W:1 /NFL /NDL /NJH /NJS', ExitCode) or (ExitCode > 7) then
  begin
    ErrorMessage := Format('恢复安装前当前现场失败（robocopy exit code %d）。', [ExitCode]);
    Result := False;
    exit;
  end;
  Result := True;
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  ResultCode: Integer;
  StatusCode: Integer;
  ServiceCLI: String;
  ErrorMessage: String;
  WasRunning: Boolean;
begin
  Result := '';
  ServiceCLI := ExpandConstant('{app}\meshmux-cli.exe');
  WasRunning := False;
  if FileExists(ServiceCLI) and Exec(ServiceCLI, 'service status', '', SW_HIDE, ewWaitUntilTerminated, StatusCode) and (StatusCode = 0) then
  begin
    WasRunning := True;
    if (not Exec(ServiceCLI, 'service stop', '', SW_HIDE, ewWaitUntilTerminated, ResultCode)) or (ResultCode <> 0) then
    begin
      Result := Format('MeshMux Core could not be stopped (exit code %d). Installation was not changed.', [ResultCode]);
      exit;
    end;
  end;
  if FileExists(ServiceCLI) and not BackupCurrentInstall(ErrorMessage) then
  begin
    if WasRunning then
      Exec(ServiceCLI, 'service start', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
    Result := ErrorMessage + ' Installation was not changed.';
    exit;
  end;
end;

procedure CurStepChanged(CurStep: TSetupStep);
var
  ResultCode: Integer;
  ServiceCLI: String;
  UserConfig: String;
  ErrorMessage: String;
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
  begin
    Exec(ServiceCLI, 'service stop', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
    if RestoreCurrentInstall(ErrorMessage) then
    begin
      ServiceCLI := ExpandConstant('{app}\meshmux-cli.exe');
      if not Exec(ServiceCLI, 'service start -config "' + UserConfig + '"', '', SW_HIDE, ewWaitUntilTerminated, ResultCode) or (ResultCode <> 0) then
        RaiseException(Format('候选版本启动失败，已恢复安装前文件，但恢复后的服务也未启动（exit code %d）。请检查日志后手动启动服务。', [ResultCode]));
      RaiseException('候选版本启动失败，已恢复安装前的当前程序文件和核心；安装未成功。');
    end
    else
      RaiseException('候选版本启动失败，且恢复安装前程序文件失败：' + ErrorMessage + '。服务保持停止，请勿继续使用当前安装目录。');
  end;
end;
