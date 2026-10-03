Unicode true
!include "MUI2.nsh"
!include "x64.nsh"
!include "WinVer.nsh"
!include "LogicLib.nsh"
!ifndef VERSION
 !define VERSION "0.1.2"
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
 ; Explicit manifest; no personal configurations are shipped.
 File "${PAYLOAD_DIR}\FturnRaw.exe"
 SetOutPath "$INSTDIR\runtime"
 File "${PAYLOAD_DIR}\runtime\raw-client.exe"
 File "${PAYLOAD_DIR}\runtime\wintun.dll"
 File "${PAYLOAD_DIR}\runtime\routes.ps1"
 File "setup-support.ps1"
 SetOutPath "$INSTDIR\licenses"
 File "${PAYLOAD_DIR}\licenses\LICENSE"
 File "${PAYLOAD_DIR}\licenses\UI-LICENSE.txt"
 File "${PAYLOAD_DIR}\licenses\WINTUN-LICENSE.txt"
 File "${PAYLOAD_DIR}\licenses\SYSTRAY-LICENSE.txt"
 File "${PAYLOAD_DIR}\licenses\ATTRIBUTION.md"
 File "${PAYLOAD_DIR}\licenses\THIRD-PARTY-NOTICES.txt"
 SetOutPath "$INSTDIR\docs"
 File "${PAYLOAD_DIR}\docs\README.md"
 ; Old owned public files can be removed after prerequisites succeed.
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
 Delete "$INSTDIR\setup-support.ps1"
 SetOutPath "$INSTDIR"
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
 nsExec::ExecToStack '"$SYSDIR\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -ExecutionPolicy Bypass -File "$INSTDIR\runtime\setup-support.ps1" -Action Uninstall -InstallDir "$INSTDIR"'
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
 Delete "$INSTDIR\Uninstall.exe"
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
 Delete "$INSTDIR\setup-support.ps1"
 Delete "$INSTDIR\runtime\raw-client.exe"
 Delete "$INSTDIR\runtime\wintun.dll"
 Delete "$INSTDIR\runtime\routes.ps1"
 Delete "$INSTDIR\runtime\setup-support.ps1"
 Delete "$INSTDIR\licenses\LICENSE"
 Delete "$INSTDIR\licenses\UI-LICENSE.txt"
 Delete "$INSTDIR\licenses\WINTUN-LICENSE.txt"
 Delete "$INSTDIR\licenses\SYSTRAY-LICENSE.txt"
 Delete "$INSTDIR\licenses\ATTRIBUTION.md"
 Delete "$INSTDIR\licenses\THIRD-PARTY-NOTICES.txt"
 Delete "$INSTDIR\docs\README.md"
 RMDir "$INSTDIR\runtime"
 RMDir "$INSTDIR\licenses"
 RMDir "$INSTDIR\docs"
 ; Personal data, legacy configurations and unrelated files are preserved.
 ; Never recursively remove an install directory or the user's AppData.
 RMDir "$INSTDIR"
 Delete "$SMPROGRAMS\${APP_NAME}\${APP_NAME}.lnk"
 Delete "$SMPROGRAMS\${APP_NAME}\Удалить.lnk"
 RMDir "$SMPROGRAMS\${APP_NAME}"
 Delete "$DESKTOP\${APP_NAME}.lnk"
 DeleteRegKey ${REG_HIVE} "${REG_KEY}"
SectionEnd
