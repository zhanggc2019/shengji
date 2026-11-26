Unicode true
!define WAILS_INSTALL_SCOPE "user"
!define REQUEST_EXECUTION_LEVEL "user"
!define PRODUCT_EXECUTABLE "Shengji.exe"
!define SHENGJI_INSTALL_KEY "Software\OddMeta\Shengji"
!include "wails_tools.nsh"
!include "FileFunc.nsh"
!include "MUI2.nsh"
!include "Sections.nsh"

Name "${INFO_PRODUCTNAME}"
OutFile "${ODDMINUTES_INSTALLER}"
InstallDir "$LOCALAPPDATA\Programs\OddMeta\Shengji"
InstallDirRegKey HKCU "${SHENGJI_INSTALL_KEY}" "InstallDir"
ManifestDPIAware true
VIProductVersion "${INFO_PRODUCTVERSION}.0"
VIAddVersionKey "CompanyName" "${INFO_COMPANYNAME}"
VIAddVersionKey "ProductName" "${INFO_PRODUCTNAME}"
VIAddVersionKey "LegalCopyright" "${INFO_COPYRIGHT}"
VIAddVersionKey "FileDescription" "声记 Windows 安装程序"
VIAddVersionKey "FileVersion" "${INFO_PRODUCTVERSION}"
VIAddVersionKey "ProductVersion" "${INFO_PRODUCTVERSION}"
!define MUI_ICON "oddminutes.ico"
!define MUI_UNICON "oddminutes.ico"
!define MUI_COMPONENTSPAGE_NODESC

!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_COMPONENTS
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"
!insertmacro MUI_LANGUAGE "SimpChinese"
LangString DesktopShortcutLabel ${LANG_ENGLISH} "Create a desktop shortcut"
LangString DesktopShortcutLabel ${LANG_SIMPCHINESE} "创建桌面快捷方式"

Section "声记" SEC_APP
  SectionIn RO
  !insertmacro wails.setShellContext
  !insertmacro wails.webview2runtime
  SetOutPath "$INSTDIR"
  !insertmacro wails.files
  !ifdef ODDMINUTES_SHERPA
    SetOutPath "$INSTDIR\sherpa"
    File "${ODDMINUTES_SHERPA}\OddMinutes-sherpa.exe"
    File "${ODDMINUTES_SHERPA}\sherpa-onnx-c-api.dll"
    File "${ODDMINUTES_SHERPA}\onnxruntime.dll"
    File "${ODDMINUTES_SHERPA}\Sherpa-ONNX-LICENSE.txt"
    File "${ODDMINUTES_SHERPA}\ONNX-Runtime-LICENSE.txt"
    File "${ODDMINUTES_SHERPA}\ONNX-Runtime-ThirdPartyNotices.txt"
    SetOutPath "$INSTDIR"
  !endif
  CreateShortcut "$SMPROGRAMS\声记.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
  !insertmacro wails.writeUninstaller
  WriteRegStr HKCU "${SHENGJI_INSTALL_KEY}" "InstallDir" "$INSTDIR"
SectionEnd

Section "$(DesktopShortcutLabel)" SEC_DESKTOP_SHORTCUT
  CreateShortcut "$DESKTOP\声记.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
SectionEnd

Section "-Save desktop shortcut choice"
  ${If} ${SectionIsSelected} ${SEC_DESKTOP_SHORTCUT}
    WriteRegDWORD HKCU "${SHENGJI_INSTALL_KEY}" "DesktopShortcut" 1
  ${Else}
    ReadRegDWORD $0 HKCU "${SHENGJI_INSTALL_KEY}" "DesktopShortcut"
    ${If} $0 == 1
      Delete "$DESKTOP\声记.lnk"
    ${EndIf}
    WriteRegDWORD HKCU "${SHENGJI_INSTALL_KEY}" "DesktopShortcut" 0
  ${EndIf}
SectionEnd

Section "Uninstall"
  !insertmacro wails.setShellContext
  Delete "$SMPROGRAMS\声记.lnk"
  Delete "$INSTDIR\${PRODUCT_EXECUTABLE}"
  Delete "$INSTDIR\sherpa\OddMinutes-sherpa.exe"
  Delete "$INSTDIR\sherpa\sherpa-onnx-c-api.dll"
  Delete "$INSTDIR\sherpa\onnxruntime.dll"
  Delete "$INSTDIR\sherpa\Sherpa-ONNX-LICENSE.txt"
  Delete "$INSTDIR\sherpa\ONNX-Runtime-LICENSE.txt"
  Delete "$INSTDIR\sherpa\ONNX-Runtime-ThirdPartyNotices.txt"
  RMDir "$INSTDIR\sherpa"
  !insertmacro wails.deleteUninstaller
  ReadRegStr $0 HKCU "${SHENGJI_INSTALL_KEY}" "InstallDir"
  ${If} $0 == $INSTDIR
    ReadRegDWORD $1 HKCU "${SHENGJI_INSTALL_KEY}" "DesktopShortcut"
    ${If} $1 == 1
      Delete "$DESKTOP\声记.lnk"
    ${EndIf}
    DeleteRegValue HKCU "${SHENGJI_INSTALL_KEY}" "DesktopShortcut"
    DeleteRegValue HKCU "${SHENGJI_INSTALL_KEY}" "InstallDir"
  ${EndIf}
  DeleteRegKey /ifempty HKCU "${SHENGJI_INSTALL_KEY}"
  RMDir "$INSTDIR"
SectionEnd

; Restore the shortcut choice and wait for the old application during upgrades.
Function .onInit
  !insertmacro wails.checkArchitecture
  SetRegView 64
  ReadRegDWORD $0 HKCU "${SHENGJI_INSTALL_KEY}" "DesktopShortcut"
  ${If} $0 == 0
    !insertmacro UnselectSection ${SEC_DESKTOP_SHORTCUT}
  ${EndIf}
  ClearErrors
  ${GetParameters} $0
  ${GetOptions} "$0" "/SHENGJI_PID=" $1
  IfErrors ready
  ; Wait for the existing instance to flush the library and release the executable.
  System::Call 'kernel32::OpenProcess(i 0x00100000, i 0, i r1) p .r2'
  StrCmp $2 0 ready
  System::Call 'kernel32::WaitForSingleObject(p r2, i 30000) i .r3'
  System::Call 'kernel32::CloseHandle(p r2)'
  StrCmp $3 0 ready
  MessageBox MB_OK|MB_ICONSTOP "声记尚未退出。请从托盘退出声记后重新安装。"
  Abort
ready:
FunctionEnd
