[Setup]
AppId=smkod.tts-cli
AppName=tts-cli
AppVersion={#AppVersion}
DefaultDirName={localappdata}\Programs\tts-cli
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
ArchitecturesAllowed=x64compatible
ChangesEnvironment=yes
OutputDir=..\..\dist
OutputBaseFilename=ttsi-{#AppVersion}-windows-amd64-setup
WizardStyle=modern
UsePreviousTasks=no

[Tasks]
Name: "gcloud"; Description: "Install Google Cloud CLI (may take a while; requires internet access)"; Flags: unchecked; Check: NeedsGoogleCloudCLI

[Files]
#if FileExists(AddBackslash(BinaryDir) + "ffmpeg.exe")
Source: "{#BinaryDir}\ffmpeg.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#BinaryDir}\FFMPEG-LICENSES\*"; DestDir: "{app}\FFMPEG-LICENSES"; Flags: ignoreversion recursesubdirs createallsubdirs
#endif
Source: "{#BinaryDir}\ttsi.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#BinaryDir}\README.md"; DestDir: "{app}"; Flags: ignoreversion

[Code]
const
  PathOwnerKey = 'Software\smkod\tts-cli';
  GoogleInstallerName = 'GoogleCloudSDKInstaller.exe';
  GoogleInstallerURL = 'https://dl.google.com/dl/cloudsdk/channels/rapid/GoogleCloudSDKInstaller.exe';

function NeedsGoogleCloudCLI: Boolean;
begin
  Result := (FileSearch('gcloud.cmd', GetEnv('PATH')) = '') and
    (FileSearch('gcloud.exe', GetEnv('PATH')) = '');
end;

procedure InstallGoogleCloudCLI;
var
  ExitCode: Integer;
  Installer, VerifyArgs: String;
begin
  try
    WizardForm.StatusLabel.Caption := 'Downloading Google Cloud CLI. This may take a while.';
    DownloadTemporaryFile(GoogleInstallerURL, GoogleInstallerName, '', nil);
    Installer := ExpandConstant('{tmp}\' + GoogleInstallerName);
    VerifyArgs := '-NoProfile -NonInteractive -Command "' +
      '$s = Get-AuthenticodeSignature -LiteralPath ''.\' + GoogleInstallerName + '''' +
      '; if ($s.Status -ne ''Valid'' -or $s.SignerCertificate.GetNameInfo(''SimpleName'', $false) ' +
      '-ne ''Google LLC'') { exit 1 }; exit 0"';
    if not Exec(ExpandConstant('{sys}\WindowsPowerShell\v1.0\powershell.exe'),
        VerifyArgs, ExpandConstant('{tmp}'), SW_HIDE, ewWaitUntilTerminated, ExitCode) or
        (ExitCode <> 0) then
      RaiseException('Could not verify the Google Cloud CLI installer signature.');
    WizardForm.StatusLabel.Caption := 'Installing Google Cloud CLI. This may take a while.';
    if not Exec(Installer, '/S /singleuser /noreporting', '', SW_SHOWNORMAL,
        ewWaitUntilTerminated, ExitCode) then
      RaiseException('Could not start the Google Cloud CLI installer.');
    if ExitCode <> 0 then
      RaiseException(Format('Google Cloud CLI installer exited with code %d.', [ExitCode]));
  except
    Log(GetExceptionMessage);
    SuppressibleMsgBox(GetExceptionMessage + #13#10 +
      'ttsi is installed. You can install Google Cloud CLI separately.',
      mbError, MB_OK, IDOK);
  end;
end;

procedure AddToUserPath;
var
  UserPath, AppDir, OwnedDir: String;
begin
  AppDir := ExpandConstant('{app}');
  if not RegQueryStringValue(HKCU, 'Environment', 'Path', UserPath) then
    UserPath := '';
  if Pos(';' + Uppercase(AppDir) + ';', ';' + Uppercase(UserPath) + ';') <> 0 then
    Exit;

  if RegQueryStringValue(HKCU, PathOwnerKey, 'PathEntry', OwnedDir) and
      (CompareText(OwnedDir, AppDir) <> 0) then
    RaiseException('Uninstall the previous copy before changing the installation directory.');

  if UserPath <> '' then
    UserPath := UserPath + ';';
  if not RegWriteStringValue(HKCU, PathOwnerKey, 'PathEntry', AppDir) then
    RaiseException('Could not record the user PATH entry.');
  if not RegWriteExpandStringValue(HKCU, 'Environment', 'Path', UserPath + AppDir) then
  begin
    RegDeleteValue(HKCU, PathOwnerKey, 'PathEntry');
    RaiseException('Could not update the user PATH.');
  end;
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep <> ssPostInstall then
    Exit;
  AddToUserPath;
  if WizardIsTaskSelected('gcloud') and NeedsGoogleCloudCLI then
    InstallGoogleCloudCLI;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  UserPath, OwnedDir: String;
  EntryPos: Integer;
begin
  if CurUninstallStep <> usUninstall then
    Exit;
  if not RegQueryStringValue(HKCU, PathOwnerKey, 'PathEntry', OwnedDir) then
    Exit;
  if CompareText(OwnedDir, ExpandConstant('{app}')) <> 0 then
    Exit;

  if RegQueryStringValue(HKCU, 'Environment', 'Path', UserPath) then
  begin
    UserPath := ';' + UserPath + ';';
    EntryPos := Pos(';' + Uppercase(OwnedDir) + ';', Uppercase(UserPath));
    if EntryPos > 0 then
    begin
      Delete(UserPath, EntryPos, Length(OwnedDir) + 1);
      if Length(UserPath) <= 2 then
        UserPath := ''
      else
        UserPath := Copy(UserPath, 2, Length(UserPath) - 2);
      if not RegWriteExpandStringValue(HKCU, 'Environment', 'Path', UserPath) then
        RaiseException('Could not remove the user PATH entry.');
    end;
  end;
  RegDeleteValue(HKCU, PathOwnerKey, 'PathEntry');
  RegDeleteKeyIfEmpty(HKCU, PathOwnerKey);
end;
