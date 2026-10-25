; aggrega-cli/packaging/windows/aggrega.nsi
;
; The Windows installer for aggrega, compiled by NSIS's makensis through
; scripts/build-windows-installer.sh, which says how it is built and signed.
;
; It installs aggrega.exe for the user who runs it, with no administrator
; rights, into %LOCALAPPDATA%\Programs\aggrega, the folder install.ps1 installs
; into too, and adds that folder to the user's PATH. It registers an uninstaller
; in the user's list of installed apps, which removes aggrega.exe, the PATH
; entry and itself. Run again, by the same release or a later one, it installs
; over what is there. /S installs with no window and no question, and
; uninstall.exe /S uninstalls the same way.
;
; The user's PATH is edited with the EnVar plug-in, which reads and writes the
; whole value whatever its length, keeps the kind of value Windows stores it as,
; and tells Windows the environment changed. The installer runs it as a plug-in;
; the uninstaller carries no plug-in of its own and runs the copy the installer
; placed beside aggrega.exe, so every program file the installer carries is one
; that is signed before the installer is built.
;
; Defined on the makensis command line by the build script:
;   VERSION      the release version, as the aggrega binary reports it
;   VI_VERSION   the same version as four numbers, for the version resource
;   AGGREGA_EXE  the aggrega.exe to install
;   PLUGIN_DIR   the folder holding EnVar.dll, the plug-in built for x86-unicode
;   FINALIZER    the script makensis runs on the uninstaller it generates
;   OUTFILE      the installer to write

!include LogicLib.nsh

Unicode true
Target x86-unicode
SetCompressor /SOLID lzma
; File records no modification time, so the installer's bytes do not depend on
; when its inputs were written.
SetDateSave off
RequestExecutionLevel user

Name "Aggrega"
OutFile "${OUTFILE}"
InstallDir "$LOCALAPPDATA\Programs\aggrega"
ShowInstDetails show
ShowUninstDetails show

; The version resource names no copyright holder, which makensis would warn of.
!pragma warning disable 9100
VIProductVersion "${VI_VERSION}"
VIFileVersion "${VI_VERSION}"
VIAddVersionKey /LANG=1033 "ProductName" "Aggrega"
VIAddVersionKey /LANG=1033 "CompanyName" "Cognitio S.r.l."
VIAddVersionKey /LANG=1033 "FileDescription" "Aggrega Setup"
VIAddVersionKey /LANG=1033 "FileVersion" "${VERSION}"
VIAddVersionKey /LANG=1033 "ProductVersion" "${VERSION}"

!addplugindir /x86-unicode "${PLUGIN_DIR}"

; makensis writes the uninstaller it generated to a file and runs this command
; on it before storing it in the installer; the build script either keeps a copy
; of it, to be signed, or puts the signed copy in its place.
!uninstfinalize 'bash "${FINALIZER}" finalize-uninstaller "%1"' = 0

Page instfiles
UninstPage uninstConfirm
UninstPage instfiles

!define UNINSTALL_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\aggrega"

; Stops the installer, or the uninstaller, with a message: shown in a window,
; and in silent mode only in its log, its exit code then being 2.
!macro STOP MESSAGE
  MessageBox MB_ICONSTOP|MB_OK "${MESSAGE}" /SD IDOK
  Abort "${MESSAGE}"
!macroend

Section "aggrega"
  SetOutPath "$INSTDIR"
  ; A file that cannot be replaced, most often an aggrega.exe still running, is
  ; reported and stops the installer rather than being skipped in silence.
  SetOverwrite try

  ClearErrors
  File "/oname=aggrega.exe" "${AGGREGA_EXE}"
  ${If} ${Errors}
    !insertmacro STOP "$INSTDIR\aggrega.exe could not be replaced. If aggrega is running, started by an editor or an AI client, close that and run the installer again."
  ${EndIf}

  ClearErrors
  File "/oname=EnVar.dll" "${PLUGIN_DIR}/EnVar.dll"
  ${If} ${Errors}
    !insertmacro STOP "$INSTDIR\EnVar.dll could not be replaced."
  ${EndIf}

  ClearErrors
  WriteUninstaller "$INSTDIR\uninstall.exe"
  ${If} ${Errors}
    !insertmacro STOP "$INSTDIR\uninstall.exe could not be written."
  ${EndIf}
  SetOverwrite on

  ; Adds the folder to the user's PATH unless it is there already, so
  ; installing over an earlier copy leaves one entry.
  EnVar::SetHKCU
  EnVar::AddValue "Path" "$INSTDIR"
  Pop $0
  ${If} $0 != 0
    !insertmacro STOP "$INSTDIR could not be added to your PATH (EnVar error $0)."
  ${EndIf}

  ClearErrors
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayName" "Aggrega"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "Publisher" "Cognitio S.r.l."
  WriteRegStr HKCU "${UNINSTALL_KEY}" "URLInfoAbout" "https://aggrega.ai"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegStr HKCU "${UNINSTALL_KEY}" "QuietUninstallString" '"$INSTDIR\uninstall.exe" /S'
  WriteRegDWORD HKCU "${UNINSTALL_KEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINSTALL_KEY}" "NoRepair" 1
  ${If} ${Errors}
    !insertmacro STOP "The uninstaller could not be registered in your list of installed apps."
  ${EndIf}
SectionEnd

Section "Uninstall"
  ; aggrega.exe goes first: when it is running it cannot be removed, and the
  ; uninstaller stops before it has changed anything.
  ClearErrors
  Delete "$INSTDIR\aggrega.exe"
  ${If} ${FileExists} "$INSTDIR\aggrega.exe"
    !insertmacro STOP "$INSTDIR\aggrega.exe could not be removed. If aggrega is running, started by an editor or an AI client, close that and run the uninstaller again."
  ${EndIf}

  ; The PATH entry is removed with the EnVar plug-in the installer placed beside
  ; aggrega.exe, run from a copy in the uninstaller's own temporary folder so
  ; that the one beside aggrega.exe can be removed while it runs.
  ${If} ${FileExists} "$INSTDIR\EnVar.dll"
    InitPluginsDir
    ClearErrors
    CopyFiles /SILENT "$INSTDIR\EnVar.dll" "$PLUGINSDIR\EnVar.dll"
    ${If} ${Errors}
      !insertmacro STOP "The PATH entry for $INSTDIR could not be removed: EnVar.dll could not be copied."
    ${EndIf}
    Push "$INSTDIR"
    Push "Path"
    CallInstDLL "$PLUGINSDIR\EnVar.dll" DeleteValue
    Pop $0
    ; 0: removed; 3: the user has no PATH; 5: the folder was not on it.
    ${If} $0 != 0
    ${AndIf} $0 != 3
    ${AndIf} $0 != 5
      !insertmacro STOP "The PATH entry for $INSTDIR could not be removed (EnVar error $0)."
    ${EndIf}
  ${Else}
    DetailPrint "$INSTDIR\EnVar.dll is missing, so the PATH entry for $INSTDIR is left as it is."
  ${EndIf}

  Delete "$INSTDIR\EnVar.dll"
  Delete "$INSTDIR\uninstall.exe"
  DeleteRegKey HKCU "${UNINSTALL_KEY}"
  ; The folder goes only when nothing else is left in it.
  SetOutPath "$TEMP"
  RMDir "$INSTDIR"
SectionEnd
