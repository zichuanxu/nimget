; Preserve the install location and registration used by prior NimGet packages.
Unicode true
!include "MUI2.nsh"
!include "LogicLib.nsh"
!include "WinVer.nsh"
!include "x64.nsh"

!define PRODUCT "NimGet"
!define INSTALL_KEY "Software\nimget"
!define UNINST_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\nimget"
!define RUN_KEY "Software\Microsoft\Windows\CurrentVersion\Run"
!define WEBVIEW2_KEY "Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}"
!define WEBVIEW2_URL "https://developer.microsoft.com/microsoft-edge/webview2/#download"

Name "${PRODUCT} ${VERSION}"
OutFile "${OUTFILE}"
InstallDir "$LOCALAPPDATA\Programs\nimget"
InstallDirRegKey HKCU "${INSTALL_KEY}" "InstallDir"
RequestExecutionLevel user
AllowSkipFiles off
; Embedded file dates would make the installer depend on the build time.
SetDateSave off
SetCompressor /SOLID lzma
VIProductVersion "${NUMVERSION}.0"
VIAddVersionKey "CompanyName" "amemiya02"
VIAddVersionKey "ProductName" "${PRODUCT}"
VIAddVersionKey "ProductVersion" "${VERSION}"
VIAddVersionKey "FileVersion" "${NUMVERSION}"
VIAddVersionKey "Comments" "Source commit: ${SOURCECOMMIT}"
VIAddVersionKey "FileDescription" "NimGet installer"
VIAddVersionKey "LegalCopyright" "Copyright 2026 amemiya02. Apache-2.0."

!define MUI_ICON "${ICON}"
!define MUI_UNICON "${ICON}"
!define MUI_FINISHPAGE_RUN "$INSTDIR\nimget.exe"
!define MUI_ABORTWARNING
!insertmacro MUI_PAGE_LICENSE "${LICENSE}"
!define MUI_PAGE_CUSTOMFUNCTION_PRE SkipUpgradeDirectoryPage
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!define MUI_UNCONFIRMPAGE_TEXT_TOP "Remove NimGet and its shortcuts. Your queue, settings, and downloaded files will be kept."
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

; Keep Setup and the uninstaller from writing the same install concurrently.
!macro LockLifecycle
  ; Setups released before the move to amemiya02 hold the legacy name.
  System::Call 'kernel32::OpenMutexW(i 0x00100000, i 0, w "io.github.zichuanxu.nimget.setup") p .r0'
  ${If} $0 != 0
    System::Call 'kernel32::CloseHandle(p r0)'
    MessageBox MB_OK|MB_ICONEXCLAMATION "Another NimGet Setup or uninstall is in progress." /SD IDOK
    SetErrorLevel 5
    Quit
  ${EndIf}
  System::Call 'kernel32::CreateMutexW(p 0, i 0, w "io.github.amemiya02.nimget.setup") p .r0 ?e'
  Pop $1
  ${If} $0 == 0
  ${OrIf} $1 == 183
    MessageBox MB_OK|MB_ICONEXCLAMATION "Another NimGet Setup or uninstall is in progress." /SD IDOK
    SetErrorLevel 5
    Quit
  ${EndIf}
  ; Windows closes the mutex handle with this process.
!macroend

; Wails v3 names the existing single-instance mutex from the ID in app/main.go.
; Releases before the move to amemiya02 used the legacy ID, so check both.
; Ask users to quit from the tray so the desktop can checkpoint its queue.
!macro CheckNimGetIsClosed
  !insertmacro CheckNimGetMutex "wails-app-io.github.amemiya02.nimget-sim"
  !insertmacro CheckNimGetMutex "wails-app-io.github.zichuanxu.nimget-sim"
!macroend

!macro CheckNimGetMutex NAME
  System::Call 'kernel32::OpenMutexW(i 0x00100000, i 0, w "${NAME}") p .r0'
  ${If} $0 != 0
    System::Call 'kernel32::CloseHandle(p r0)'
    MessageBox MB_OK|MB_ICONEXCLAMATION "Quit NimGet from its tray menu and run this again." /SD IDOK
    SetErrorLevel 2
    Quit
  ${EndIf}
!macroend

Var RegisteredInstallDir

Function .onInit
  ${IfNot} ${AtLeastWin10}
  ${OrIfNot} ${IsNativeAMD64}
    MessageBox MB_OK|MB_ICONEXCLAMATION "NimGet needs 64-bit Windows 10 or Windows 11 on an x64 processor." /SD IDOK
    SetErrorLevel 3
    Quit
  ${EndIf}
  !insertmacro CheckNimGetIsClosed
  !insertmacro LockLifecycle
  ReadRegStr $RegisteredInstallDir HKCU "${INSTALL_KEY}" "InstallDir"
  ${If} $RegisteredInstallDir != ""
    ; The command-line /D option can override InstallDirRegKey.
    StrCpy $INSTDIR $RegisteredInstallDir
  ${EndIf}
  ; The 32-bit view reads the documented WOW6432Node HKLM location.
  SetRegView 32
  ReadRegStr $0 HKLM "${WEBVIEW2_KEY}" "pv"
  ${If} $0 == ""
  ${OrIf} $0 == "0.0.0.0"
    ReadRegStr $0 HKCU "${WEBVIEW2_KEY}" "pv"
  ${EndIf}
  ${If} $0 == ""
  ${OrIf} $0 == "0.0.0.0"
    MessageBox MB_YESNO|MB_ICONEXCLAMATION "Install the Microsoft Edge WebView2 Evergreen Runtime and run NimGet Setup again. Open Microsoft's download page?" /SD IDNO IDNO missing
    ExecShell "open" "${WEBVIEW2_URL}"
    missing:
    SetErrorLevel 4
    Quit
  ${EndIf}
FunctionEnd

Function SkipUpgradeDirectoryPage
  ${If} $RegisteredInstallDir != ""
    Abort
  ${EndIf}
FunctionEnd

Section "NimGet"
  !insertmacro CheckNimGetIsClosed
  SetShellVarContext current
  SetOutPath "$INSTDIR"
  File "/oname=nimget.exe" "${BINARY}"
  File "/oname=LICENSE.txt" "${LICENSE}"
  File "/oname=NOTICE.txt" "${NOTICE}"
  WriteUninstaller "$INSTDIR\uninstall.exe"
  CreateShortcut "$SMPROGRAMS\NimGet.lnk" "$INSTDIR\nimget.exe"
  WriteRegStr HKCU "${INSTALL_KEY}" "InstallDir" "$INSTDIR"
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayName" "${PRODUCT}"
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${UNINST_KEY}" "Publisher" "amemiya02"
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayIcon" "$INSTDIR\nimget.exe"
  WriteRegStr HKCU "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNINST_KEY}" "URLInfoAbout" "https://github.com/amemiya02/nimget"
  WriteRegStr HKCU "${UNINST_KEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegStr HKCU "${UNINST_KEY}" "QuietUninstallString" '"$INSTDIR\uninstall.exe" /S'
  WriteRegDWORD HKCU "${UNINST_KEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINST_KEY}" "NoRepair" 1
SectionEnd

Function un.onInit
  !insertmacro CheckNimGetIsClosed
  !insertmacro LockLifecycle
  ReadRegStr $RegisteredInstallDir HKCU "${INSTALL_KEY}" "InstallDir"
  ${If} $RegisteredInstallDir != $INSTDIR
    MessageBox MB_OK|MB_ICONEXCLAMATION "This folder is not the registered NimGet installation. Uninstall NimGet from Windows Settings." /SD IDOK
    SetErrorLevel 6
    Quit
  ${EndIf}
FunctionEnd

; Delete only installed files. Even downloads saved in the install folder stay.
Section "Uninstall"
  !insertmacro CheckNimGetIsClosed
  SetShellVarContext current
  Delete "$INSTDIR\nimget.exe"
  ${If} ${FileExists} "$INSTDIR\nimget.exe"
    MessageBox MB_OK|MB_ICONEXCLAMATION "NimGet could not be removed. Close it from the tray and retry." /SD IDOK
    SetErrorLevel 2
    Quit
  ${EndIf}
  Delete "$SMPROGRAMS\NimGet.lnk"
  Delete "$INSTDIR\LICENSE.txt"
  Delete "$INSTDIR\NOTICE.txt"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"
  DeleteRegKey HKCU "${UNINST_KEY}"
  DeleteRegValue HKCU "${INSTALL_KEY}" "InstallDir"
  DeleteRegKey /ifempty HKCU "${INSTALL_KEY}"
  ReadRegStr $0 HKCU "${RUN_KEY}" "nimget"
  ${If} $0 == '"$INSTDIR\nimget.exe"'
  ${OrIf} $0 == "$INSTDIR\nimget.exe"
    DeleteRegValue HKCU "${RUN_KEY}" "nimget"
  ${EndIf}
SectionEnd
