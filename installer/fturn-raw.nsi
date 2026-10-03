Unicode true
!include "MUI2.nsh"
!include "x64.nsh"
!include "WinVer.nsh"
!include "LogicLib.nsh"
!ifndef VERSION
 !define VERSION "0.1.1"
!endif
!ifndef PAYLOAD_DIR
 !error "PAYLOAD_DIR is required"
!endif
!ifndef OUTPUT_FILE
 !error "OUTPUT_FILE is required"
!endif
!ifdef TEST_MODE
 !define APP_NAME "fturn Raw Installer Test"
 !define REG_HIVE HKCU
 RequestExecutionLevel user
 !define REG_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\FturnRawInstallerTest"
!else
 !define APP_NAME "fturn Raw"
 !define REG_HIVE HKLM
 RequestExecutionLevel admin
 !define REG_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\FturnRaw"
!endif
Name "${APP_NAME}"
OutFile "${OUTPUT_FILE}"
InstallDir "$PROGRAMFILES64\${APP_NAME}"
InstallDirRegKey ${REG_HIVE} "${REG_KEY}" "InstallLocation"
SetCompressor /SOLID lzma
VIProductVersion "${VERSION}.0"
VIAddVersionKey "ProductName" "${APP_NAME}"
VIAddVersionKey "FileDescription" "fturn Raw Windows installer"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "LegalCopyright" "fturn Raw contributors"
!define MUI_ABORTWARNING
!define MUI_ICON "..\ui\newservice\build\windows\icon.ico"
!define MUI_UNICON "..\ui\newservice\build\windows\icon.ico"
!define MUI_FINISHPAGE_RUN "$INSTDIR\FturnRaw.exe"
!define MUI_FINISHPAGE_RUN_TEXT "Запустить fturn Raw"
!define MUI_FINISHPAGE_RUN_NOTCHECKED
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_LICENSE "..\ui\newservice\LICENSE"
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "Russian"
!insertmacro MUI_LANGUAGE "English"
Function .onInit
 ${IfNot} ${RunningX64}
  MessageBox MB_ICONSTOP "Windows x64 is required." /SD IDOK
  SetErrorLevel 1
  Abort
 ${EndIf}
 ${IfNot} ${AtLeastWin10}
  MessageBox MB_ICONSTOP "Windows 10 or newer is required." /SD IDOK
  SetErrorLevel 1
  Abort
 ${EndIf}
 SetRegView 64
!ifdef TEST_MODE
 SetShellVarContext current
!else
 SetShellVarContext all
!endif
 ReadRegStr $0 ${REG_HIVE} "${REG_KEY}" "InstallLocation"
 ${If} $0 != ""
  StrCpy $INSTDIR $0
 ${EndIf}
FunctionEnd
Function un.onInit
 SetRegView 64
!ifdef TEST_MODE
 SetShellVarContext current
!else
 SetShellVarContext all
!endif
FunctionEnd
Section "fturn Raw" MainSection
 SetOutPath "$INSTDIR"
!ifndef TEST_MODE
 InitPluginsDir
 File /oname=$PLUGINSDIR\setup-support.ps1 "setup-support.ps1"
 ${DisableX64FSRedirection}
 nsExec::ExecToStack '"$SYSDIR\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -ExecutionPolicy Bypass -File "$PLUGINSDIR\setup-support.ps1" -Action Prepare -InstallDir "$INSTDIR"'
 ${EnableX64FSRedirection}
 Pop $0
 Pop $1
 ${If} $0 != 0
  MessageBox MB_ICONSTOP "Подготовка не выполнена. Закройте fturn Raw через меню трея и повторите установку.$\r$\n$1" /SD IDOK
  SetErrorLevel 1
  Abort
 ${EndIf}
!endif
 ; Explicit file list prevents accidentally packaging keys or used profiles.
 File "${PAYLOAD_DIR}\FturnRaw.exe"
 File "${PAYLOAD_DIR}\raw-client.exe"
 File "${PAYLOAD_DIR}\wintun.dll"
 File "${PAYLOAD_DIR}\routes.ps1"
 File "${PAYLOAD_DIR}\LICENSE"
 File "${PAYLOAD_DIR}\UI-LICENSE.txt"
 File "${PAYLOAD_DIR}\WINTUN-LICENSE.txt"
 File "${PAYLOAD_DIR}\SYSTRAY-LICENSE.txt"
 File "${PAYLOAD_DIR}\ATTRIBUTION.md"
 File "${PAYLOAD_DIR}\THIRD-PARTY-NOTICES.txt"
 File "${PAYLOAD_DIR}\README.md"
 ; Do not overwrite a legacy configuration during upgrade; profiles use AppData.
 SetOverwrite off
 File "${PAYLOAD_DIR}\connection.json"
 SetOverwrite on
 File "setup-support.ps1"
 WriteUninstaller "$INSTDIR\Uninstall.exe"
 CreateDirectory "$SMPROGRAMS\${APP_NAME}"
 CreateShortcut "$SMPROGRAMS\${APP_NAME}\${APP_NAME}.lnk" "$INSTDIR\FturnRaw.exe"
 CreateShortcut "$SMPROGRAMS\${APP_NAME}\Удалить.lnk" "$INSTDIR\Uninstall.exe"
 CreateShortcut "$DESKTOP\${APP_NAME}.lnk" "$INSTDIR\FturnRaw.exe"
 WriteRegStr ${REG_HIVE} "${REG_KEY}" "DisplayName" "${APP_NAME}"
 WriteRegStr ${REG_HIVE} "${REG_KEY}" "DisplayVersion" "${VERSION}"
 WriteRegStr ${REG_HIVE} "${REG_KEY}" "Publisher" "LiTarPc"
 WriteRegStr ${REG_HIVE} "${REG_KEY}" "DisplayIcon" "$INSTDIR\FturnRaw.exe"
 WriteRegStr ${REG_HIVE} "${REG_KEY}" "InstallLocation" "$INSTDIR"
 WriteRegStr ${REG_HIVE} "${REG_KEY}" "UninstallString" '$\"$INSTDIR\Uninstall.exe$\"'
 WriteRegStr ${REG_HIVE} "${REG_KEY}" "QuietUninstallString" '$\"$INSTDIR\Uninstall.exe$\" /S'
 WriteRegStr ${REG_HIVE} "${REG_KEY}" "URLInfoAbout" "https://github.com/LiTarPc/fturn-raw"
 WriteRegDWORD ${REG_HIVE} "${REG_KEY}" "NoModify" 1
 WriteRegDWORD ${REG_HIVE} "${REG_KEY}" "NoRepair" 1
SectionEnd
Section "Uninstall"
!ifndef TEST_MODE
 ${DisableX64FSRedirection}
 nsExec::ExecToStack '"$SYSDIR\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -ExecutionPolicy Bypass -File "$INSTDIR\setup-support.ps1" -Action Uninstall -InstallDir "$INSTDIR"'
 ${EnableX64FSRedirection}
 Pop $0
 Pop $1
 ${If} $0 != 0
  MessageBox MB_ICONSTOP "Не удалось подготовить удаление. Закройте fturn Raw через меню трея.$\r$\n$1" /SD IDOK
  SetErrorLevel 1
  Abort
 ${EndIf}
!endif
 Delete "$INSTDIR\FturnRaw.exe"
 Delete "$INSTDIR\raw-client.exe"
 Delete "$INSTDIR\wintun.dll"
 Delete "$INSTDIR\routes.ps1"
 Delete "$INSTDIR\LICENSE"
 Delete "$INSTDIR\UI-LICENSE.txt"
 Delete "$INSTDIR\WINTUN-LICENSE.txt"
 Delete "$INSTDIR\SYSTRAY-LICENSE.txt"
 Delete "$INSTDIR\ATTRIBUTION.md"
 Delete "$INSTDIR\THIRD-PARTY-NOTICES.txt"
 Delete "$INSTDIR\README.md"
 ; connection.json may contain legacy personal settings: preserve it.
 Delete "$INSTDIR\setup-support.ps1"
 Delete "$INSTDIR\Uninstall.exe"
 ; Never recursively remove an install directory or the user's AppData.
 RMDir "$INSTDIR"
 Delete "$SMPROGRAMS\${APP_NAME}\${APP_NAME}.lnk"
 Delete "$SMPROGRAMS\${APP_NAME}\Удалить.lnk"
 RMDir "$SMPROGRAMS\${APP_NAME}"
 Delete "$DESKTOP\${APP_NAME}.lnk"
 DeleteRegKey ${REG_HIVE} "${REG_KEY}"
SectionEnd
