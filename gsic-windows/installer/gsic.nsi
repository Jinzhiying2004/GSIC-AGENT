; GSIC Windows installer (NSIS 3, per-user install, no admin required).
; Build from the installer/ directory (gsic.exe must sit next to this script):
;   makensis /DVERSION=4.39 /DVERSION_CODE=439 /DOUTFILE=../GSIC-Setup-4.39.exe gsic.nsi
;
; Per-user install (%LOCALAPPDATA%\Programs\GSIC) is deliberate: the
; self-updater replaces the exe in place, which needs a directory the
; user can write without elevation. Program Files would break updates.
; Data (token, Tailscale login, logs) stays in %APPDATA%\GSIC and is
; kept across reinstalls and uninstalls.

!include "MUI2.nsh"

!ifndef VERSION
  !define VERSION "4.39"
!endif
!ifndef VERSION_CODE
  !define VERSION_CODE 439
!endif
!ifndef OUTFILE
  !define OUTFILE "GSIC-Setup-${VERSION}.exe"
!endif

Name "GSIC ${VERSION}"
OutFile "${OUTFILE}"
InstallDir "$LOCALAPPDATA\Programs\GSIC"
InstallDirRegKey HKCU "Software\GSIC" "InstallDir"
RequestExecutionLevel user

SetCompressor /SOLID lzma
SetCompressorDictSize 32

!define MUI_ABORTWARNING
!define MUI_ICON "gsic.ico"
!define MUI_UNICON "gsic.ico"
!define MUI_FINISHPAGE_RUN "$INSTDIR\gsic.exe"
!define MUI_FINISHPAGE_RUN_TEXT "运行 GSIC"

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "SimpChinese"

Section "GSIC" SecMain
  SetOutPath "$INSTDIR"
  ; Stop any running GSIC first (installed gsic.exe, or an old
  ; versioned exe from Downloads). NOTE: no bare gsic*.exe wildcard:
  ; it would also match this installer (GSIC-Setup-*.exe) and kill it.
  ExecWait 'taskkill /F /IM gsic.exe' $0
  ExecWait 'taskkill /F /FI "IMAGENAME eq gsic-4.*.exe"' $0
  Sleep 1000
  File "gsic.exe"
  WriteUninstaller "$INSTDIR\uninstall.exe"
  ; Shortcuts
  CreateDirectory "$SMPROGRAMS\GSIC"
  CreateShortcut "$SMPROGRAMS\GSIC\GSIC.lnk" "$INSTDIR\gsic.exe"
  CreateShortcut "$SMPROGRAMS\GSIC\卸载 GSIC.lnk" "$INSTDIR\uninstall.exe"
  CreateShortcut "$DESKTOP\GSIC.lnk" "$INSTDIR\gsic.exe"
  ; Autostart (HKCU Run key; GSIC must start at boot).
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "GSIC" '"$INSTDIR\gsic.exe"'
  ; App registration
  WriteRegStr HKCU "Software\GSIC" "InstallDir" "$INSTDIR"
  WriteRegStr HKCU "Software\GSIC" "Version" "${VERSION}"
  WriteRegDWORD HKCU "Software\GSIC" "VersionCode" ${VERSION_CODE}
  ; Add/Remove Programs
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\GSIC" "DisplayName" "GSIC"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\GSIC" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\GSIC" "Publisher" "靳志英"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\GSIC" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\GSIC" "DisplayIcon" "$INSTDIR\gsic.exe"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\GSIC" "UninstallString" "$INSTDIR\uninstall.exe"
  WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\GSIC" "NoModify" 1
  WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\GSIC" "NoRepair" 1
SectionEnd

Section "Uninstall"
  ExecWait 'taskkill /F /IM gsic.exe' $0
  Sleep 1000
  Delete "$INSTDIR\gsic.exe"
  Delete "$INSTDIR\gsic.exe.bak"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"
  Delete "$SMPROGRAMS\GSIC\GSIC.lnk"
  Delete "$SMPROGRAMS\GSIC\卸载 GSIC.lnk"
  RMDir "$SMPROGRAMS\GSIC"
  Delete "$DESKTOP\GSIC.lnk"
  DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "GSIC"
  DeleteRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\GSIC"
  DeleteRegKey HKCU "Software\GSIC"
  ; NOTE: %APPDATA%\GSIC (token, Tailscale login, logs) is intentionally
  ; kept so a reinstall keeps working without re-login.
SectionEnd
