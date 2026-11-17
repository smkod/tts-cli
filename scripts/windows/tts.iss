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
OutputBaseFilename=tts-{#AppVersion}-windows-amd64-setup
WizardStyle=modern

[Files]
Source: "{#BinaryDir}\tts.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#BinaryDir}\README.md"; DestDir: "{app}"; Flags: ignoreversion

[Code]
const
  PathOwnerKey = 'Software\smkod\tts-cli';

procedure CurStepChanged(CurStep: TSetupStep);
var
  UserPath, AppDir, OwnedDir: String;
begin
  if CurStep <> ssPostInstall then
    Exit;
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
