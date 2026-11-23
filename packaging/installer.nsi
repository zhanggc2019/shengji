Unicode true
!define WAILS_INSTALL_SCOPE "user"
!define REQUEST_EXECUTION_LEVEL "user"
!define PRODUCT_EXECUTABLE "Shengji.exe"
!include "wails_tools.nsh"
!include "MUI2.nsh"

Name "${INFO_PRODUCTNAME}"
OutFile "${ODDMINUTES_INSTALLER}"
InstallDir "$LOCALAPPDATA\Programs\OddMeta\Shengji"
ManifestDPIAware true
VIProductVersion "${INFO_PRODUCTVERSION}.0"
VIAddVersionKey "CompanyName" "${INFO_COMPANYNAME}"
VIAddVersionKey "ProductName" "${INFO_PRODUCTNAME}"
VIAddVersionKey "LegalCopyright" "${INFO_COPYRIGHT}"
VIAddVersionKey "FileDescription" "声记 Windows 安装程序"
VIAddVersionKey "FileVersion" "${INFO_PRODUCTVERSION}"
!define MUI_ICON "oddminutes.ico"
!define MUI_UNICON "oddminutes.ico"

!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"
!insertmacro MUI_LANGUAGE "SimpChinese"

; Verify CPU compatibility using the generated Wails packaging helper.
Function .onInit
  !insertmacro wails.checkArchitecture
FunctionEnd

Section
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
  RMDir "$INSTDIR"
SectionEnd
