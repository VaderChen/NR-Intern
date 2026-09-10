; NR-Intern Windows 安裝程式。
;
; 每位使用者安裝，不需要系統管理員：這個產品的使用者常常不是設定它的人，
; 而 UAC 提示與「請找 IT」是最容易讓人停在這一步的兩件事。安裝位置因此放在
; $LOCALAPPDATA，登錄檔寫 HKCU。
;
; 由 src/cmd/release 以 -D 傳入 PAYLOAD_DIR、OUTPUT_FILE、APP_VERSION、
; NUMERIC_VERSION、APP_ARCH，以及選用的 WITH_NETPASS。
Unicode true
RequestExecutionLevel user
!include "MUI2.nsh"
!include "LogicLib.nsh"
!include "x64.nsh"
Name "NR-Intern"
OutFile "${OUTPUT_FILE}"
InstallDir "$LOCALAPPDATA\Programs\NR-Intern"
InstallDirRegKey HKCU "Software\NR-Intern" "InstallDir"
SetCompressor /SOLID lzma
VIProductVersion "${NUMERIC_VERSION}"
VIAddVersionKey "ProductName" "NR-Intern"
VIAddVersionKey "FileDescription" "NR-Intern Installer"
VIAddVersionKey "FileVersion" "${APP_VERSION}"
VIAddVersionKey "LegalCopyright" "NR-Intern"
!define MUI_ICON "${INSTALLER_ICON}"
!define MUI_UNICON "${INSTALLER_ICON}"
!define MUI_ABORTWARNING
!define MUI_FINISHPAGE_RUN "$INSTDIR\nr-intern-desktop.exe"
!define MUI_LANGDLL_REGISTRY_ROOT HKCU
!define MUI_LANGDLL_REGISTRY_KEY "Software\NR-Intern"
!define MUI_LANGDLL_REGISTRY_VALUENAME "InstallerLanguage"
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "TradChinese"
!insertmacro MUI_LANGUAGE "English"
!insertmacro MUI_LANGUAGE "Japanese"
!insertmacro MUI_LANGUAGE "Korean"
LangString InUse ${LANG_TRADCHINESE} "請先結束 NR-Intern，再按重試。"
LangString InUse ${LANG_ENGLISH} "Quit NR-Intern, then retry."
LangString InUse ${LANG_JAPANESE} "NR-Intern を終了してから再試行してください。"
LangString InUse ${LANG_KOREAN} "NR-Intern을 종료한 후 다시 시도하세요."
LangString WrongArch ${LANG_TRADCHINESE} "此安裝程式不適用於目前的 Windows 架構。"
LangString WrongArch ${LANG_ENGLISH} "This installer does not match your Windows architecture."
LangString WrongArch ${LANG_JAPANESE} "このインストーラーは Windows のアーキテクチャに対応していません。"
LangString WrongArch ${LANG_KOREAN} "이 설치 프로그램은 Windows 아키텍처와 맞지 않습니다."

; 覆寫正在執行的 EXE 會失敗，而 NSIS 預設只是略過那個檔案，安裝看起來成功、
; 裝出來的卻是新舊混合。先開檔測試，佔用中就請使用者關掉再重試。
!macro CheckFileClosed FILE
retry_${FILE}:
 IfFileExists "$INSTDIR\${FILE}" 0 done_${FILE}
 System::Call 'kernel32::CreateFileW(w "$INSTDIR\${FILE}", i 0x40000000, i 0, p 0, i 3, i 0, p 0) p.r0'
 ${If} $0 == -1
  MessageBox MB_RETRYCANCEL|MB_ICONEXCLAMATION "$(InUse)" IDRETRY retry_${FILE}
  Abort
 ${EndIf}
 System::Call 'kernel32::CloseHandle(p r0)'
done_${FILE}:
!macroend

Function .onInit
 SetShellVarContext current
 !insertmacro MUI_LANGDLL_DISPLAY
!if "${APP_ARCH}" == "arm64"
 ${IfNot} ${IsNativeARM64}
!else
 ${IfNot} ${IsNativeAMD64}
!endif
  MessageBox MB_OK|MB_ICONSTOP "$(WrongArch)"
  Abort
 ${EndIf}
FunctionEnd

Function un.onInit
 SetShellVarContext current
 !insertmacro MUI_UNGETLANGUAGE
FunctionEnd

Section "NR-Intern"
 !insertmacro CheckFileClosed "nr-intern-desktop.exe"
 !insertmacro CheckFileClosed "nr-intern-server.exe"
 SetOutPath "$INSTDIR"
 File "${PAYLOAD_DIR}/nr-intern-desktop.exe"
 File "${PAYLOAD_DIR}/nr-intern-server.exe"
!ifdef WITH_NETPASS
 SetOutPath "$INSTDIR\netpass-client"
 File "${PAYLOAD_DIR}/netpass-client/NetPassClient.exe"
 SetOutPath "$INSTDIR"
!endif
 WriteUninstaller "$INSTDIR\Uninstall.exe"
 CreateDirectory "$SMPROGRAMS\NR-Intern"
 CreateShortcut "$SMPROGRAMS\NR-Intern\NR-Intern.lnk" "$INSTDIR\nr-intern-desktop.exe"
 CreateShortcut "$DESKTOP\NR-Intern.lnk" "$INSTDIR\nr-intern-desktop.exe"
 WriteRegStr HKCU "Software\NR-Intern" "InstallDir" "$INSTDIR"
 WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\NR-Intern" "DisplayName" "NR-Intern"
 WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\NR-Intern" "DisplayVersion" "${APP_VERSION}"
 WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\NR-Intern" "InstallLocation" "$INSTDIR"
 WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\NR-Intern" "DisplayIcon" "$INSTDIR\nr-intern-desktop.exe"
 WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\NR-Intern" "UninstallString" '$\"$INSTDIR\Uninstall.exe$\"'
 WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\NR-Intern" "QuietUninstallString" '$\"$INSTDIR\Uninstall.exe$\" /S'
 WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\NR-Intern" "NoModify" 1
 WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\NR-Intern" "NoRepair" 1
SectionEnd

Section "Uninstall"
 !insertmacro CheckFileClosed "nr-intern-desktop.exe"
 !insertmacro CheckFileClosed "nr-intern-server.exe"
 Delete "$INSTDIR\nr-intern-desktop.exe"
 Delete "$INSTDIR\nr-intern-server.exe"
 Delete "$INSTDIR\netpass-client\NetPassClient.exe"
 RMDir "$INSTDIR\netpass-client"
 Delete "$INSTDIR\Uninstall.exe"
 Delete "$DESKTOP\NR-Intern.lnk"
 Delete "$SMPROGRAMS\NR-Intern\NR-Intern.lnk"
 RMDir "$SMPROGRAMS\NR-Intern"
 ; 只移除自己放進去的檔案與空目錄。使用者的資料不在這裡，而萬一有人把別的
 ; 東西放進安裝目錄，遞迴刪除會一併帶走。
 RMDir "$INSTDIR"
 DeleteRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\NR-Intern"
 DeleteRegValue HKCU "Software\NR-Intern" "InstallDir"
SectionEnd
