; g8s (The Gatekeepers) - Windows installer
!include "LogicLib.nsh"

!ifndef VERSION
  !define VERSION "0.4.0"
!endif

!ifndef INPUT_DIR
  !define INPUT_DIR "."
!endif

Name "g8s (The Gatekeepers)"
OutFile "g8s_${VERSION}_windows_amd64_installer.exe"
InstallDir "$PROGRAMFILES\g8s"
RequestExecutionLevel admin
Page directory
Page instfiles

Section "Install"
  SetOutPath "$INSTDIR"
  File /r "${INPUT_DIR}\*"
  WriteRegStr HKLM "SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\g8s" "DisplayName" "g8s (The Gatekeepers)"
  WriteRegStr HKLM "SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\g8s" "DisplayVersion" "${VERSION}"
  WriteRegStr HKLM "SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\g8s" "Publisher" "TamLD"
  WriteRegStr HKLM "SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\g8s" "InstallLocation" "$INSTDIR"
  WriteRegStr HKLM "SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\g8s" "UninstallString" "$INSTDIR\Uninstall.exe"
  WriteRegStr HKLM "SYSTEM\CurrentControlSet\Control\Session Manager\Environment" "Path" "$INSTDIR;$ENV{Path}"
  CreateDirectory "$SMPROGRAMS\g8s (The Gatekeepers)"
  CreateShortcut "$SMPROGRAMS\g8s (The Gatekeepers)\g8s.lnk" "$INSTDIR\g8s.exe"
  WriteUninstaller "$INSTDIR\Uninstall.exe"
SectionEnd

Section "Uninstall"
  RMDir /r "$INSTDIR"
  DeleteRegKey HKLM "SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\g8s"
  Delete "$SMPROGRAMS\g8s (The Gatekeepers)\g8s.lnk"
  RMDir "$SMPROGRAMS\g8s (The Gatekeepers)"
SectionEnd
