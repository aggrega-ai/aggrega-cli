; aggrega-cli/packaging/windows/aggrega.nsi
;
; The Windows installer for aggrega, compiled by NSIS's makensis through
; scripts/build-windows-installer.sh, which says how it is built and signed.
;
; It installs aggrega.exe for the user who runs it, with no administrator
; rights, by default into %LOCALAPPDATA%\Programs\aggrega, the folder
; install.ps1 installs into too, and adds that folder to the user's PATH. It is
; a Modern UI wizard built from pages makensis itself implements, so it runs no
; plug-in but EnVar: a welcome page, showing welcome.txt; a license page,
; showing the license the repository's LICENSE holds, which the user accepts
; with I Agree before anything is installed; a components page, where Aggrega
; itself is always installed and adding its folder to the PATH is selected and
; can be cleared; the install folder, which can be changed; the progress; and a
; finish page, showing what this install did with the PATH and finish.txt's
; next steps. The welcome and finish pages are NSIS's license page too, with no
; acceptance control. The install places the license beside aggrega.exe as
; LICENSE. It registers an uninstaller in the user's list of installed apps,
; which removes aggrega.exe, LICENSE, the PATH entry and itself, after a
; confirmation page. Run again, by the same release or a later one, it offers
; the folder it installed into last and installs over what is there. /S
; installs with no window and no question, into that folder or the default one
; and with the PATH entry, and uninstall.exe /S uninstalls the same way.
;
; The user's PATH is edited with the EnVar plug-in, which reads and writes the
; whole value whatever its length, keeps the kind of value Windows stores it as,
; and tells Windows the environment changed. The installer runs it as a plug-in;
; the uninstaller carries no plug-in of its own and runs the copy the installer
; placed beside aggrega.exe, so every program file the installer carries is one
; that is signed before the installer is built. The build script deletes NSIS's
; own plug-ins before compiling, so a call to any plug-in but EnVar - by a page,
; a macro from an NSIS include, or this script - fails the compile.
;
; aggrega.ico, the installer's and the uninstaller's icon, and
; installer-header.bmp, the image at the right of every page's header, are
; copies of the files of the same names in the logo folder of the aggrega-brand
; repository, which its scripts/build-windows-images.mjs writes from the logo
; and the brand colours, and its scripts/check-windows-images.mjs, run by its
; npm test, holds them to that script. They are copies because the installer is
; built by this repository's release alone, from this folder, which reads
; nothing from aggrega-brand. To change them, change them there, run that
; script, and copy both here.
;
; Defined on the makensis command line by the build script:
;   VERSION      the release version, as the aggrega binary reports it
;   VI_VERSION   the same version as four numbers, for the version resource
;   AGGREGA_EXE  the aggrega.exe to install
;   PLUGIN_DIR   the folder holding EnVar.dll, the plug-in built for x86-unicode
;   LICENSE_FILE the license page's text, which the install places as LICENSE:
;                the repository's LICENSE with Windows line endings
;   FINALIZER    the script makensis runs on the uninstaller it generates
;   OUTFILE      the installer to write

!include LogicLib.nsh
!include MUI2.nsh

Unicode true
Target x86-unicode
SetCompressor /SOLID lzma
; File records no modification time, so the installer's bytes do not depend on
; when its inputs were written.
SetDateSave off
RequestExecutionLevel user

!define UNINSTALL_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\aggrega"

Name "Aggrega"
OutFile "${OUTFILE}"
InstallDir "$LOCALAPPDATA\Programs\aggrega"
; A folder chosen at an earlier install is offered again, and /S installs there.
InstallDirRegKey HKCU "${UNINSTALL_KEY}" "InstallLocation"
; The line at the foot of every page, naming the publisher. ${U+00B7} is the
; middle dot, written as its code point so this script stays plain ASCII.
BrandingText "Aggrega ${VERSION} ${U+00B7} Cognitio s.r.l."
ShowInstDetails show
ShowUninstDetails show

VIProductVersion "${VI_VERSION}"
VIFileVersion "${VI_VERSION}"
VIAddVersionKey /LANG=1033 "ProductName" "Aggrega"
VIAddVersionKey /LANG=1033 "CompanyName" "Cognitio s.r.l."
; The copyright line the source files carry.
VIAddVersionKey /LANG=1033 "LegalCopyright" "Copyright 2026 Cognitio s.r.l."
VIAddVersionKey /LANG=1033 "FileDescription" "Aggrega Setup"
VIAddVersionKey /LANG=1033 "FileVersion" "${VERSION}"
VIAddVersionKey /LANG=1033 "ProductVersion" "${VERSION}"

!addplugindir /x86-unicode "${PLUGIN_DIR}"

; makensis writes the uninstaller it generated to a file and runs this command
; on it before storing it in the installer; the build script either keeps a copy
; of it, to be signed, or puts the signed copy in its place.
!uninstfinalize 'bash "${FINALIZER}" finalize-uninstaller "%1"' = 0

; What the finish page says about the PATH, set by the sections that ran: a
; short line above the box, and the paragraph that explains it, first in the box.
Var PathLine
Var PathNote

!define MUI_ICON "${__FILEDIR__}/aggrega.ico"
!define MUI_UNICON "${__FILEDIR__}/aggrega.ico"
!define MUI_HEADERIMAGE
!define MUI_HEADERIMAGE_RIGHT
!define MUI_HEADERIMAGE_BITMAP "${__FILEDIR__}/installer-header.bmp"

!define MUI_PAGE_HEADER_TEXT "Welcome to Aggrega Setup"
!define MUI_PAGE_HEADER_SUBTEXT "aggrega ${VERSION}"
!define MUI_LICENSEPAGE_TEXT_TOP "Setup installs aggrega ${VERSION} on this computer."
!define MUI_LICENSEPAGE_TEXT_BOTTOM "Click Next to continue."
!define MUI_LICENSEPAGE_BUTTON "$(^NextBtn)"
!insertmacro MUI_PAGE_LICENSE "${__FILEDIR__}/welcome.txt"

; The license page, with Modern UI's own header, texts and I Agree button: none
; of the welcome page's settings reach it, since Modern UI clears them once a
; page is declared. Nothing is installed until the user presses I Agree.
!insertmacro MUI_PAGE_LICENSE "${LICENSE_FILE}"

!define MUI_COMPONENTSPAGE_TEXT_TOP "Aggrega is always installed. Add to PATH lets a terminal run it by typing aggrega."
!insertmacro MUI_PAGE_COMPONENTS

!insertmacro MUI_PAGE_DIRECTORY

; The progress page waits for Next once the install is done.
AutoCloseWindow false
!insertmacro MUI_PAGE_INSTFILES

; The header, the top line and the box's first paragraph are set when the page
; is shown, so they name the folder and what this install did with the PATH; the
; top line is one short line, since the label above the box holds one line, and
; FinishShow puts the explanation in the box ahead of finish.txt's text.
; finish.txt's next steps are a deliberate copy of the ones the install commands
; print, in packaging/install/install.ps1 (Write-NextSteps) and
; packaging/install/install.sh (its next steps), in their own wording: the three
; are a text file shown by NSIS, PowerShell and POSIX shell, with no common
; source any of them could read at run time, so a change to the next steps
; changes all three.
!define MUI_PAGE_HEADER_TEXT "Aggrega is installed"
!define MUI_PAGE_HEADER_SUBTEXT "aggrega ${VERSION}, in $INSTDIR"
!define MUI_LICENSEPAGE_TEXT_TOP "$PathLine"
!define MUI_LICENSEPAGE_TEXT_BOTTOM "Click Finish to close Setup."
!define MUI_LICENSEPAGE_BUTTON "&Finish"
!define MUI_PAGE_CUSTOMFUNCTION_SHOW FinishShow
!insertmacro MUI_PAGE_LICENSE "${__FILEDIR__}/finish.txt"

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "English"

; Stops the installer, or the uninstaller, with a message: shown in a window,
; and in silent mode only in its log, its exit code then being 2.
!macro STOP MESSAGE
  MessageBox MB_ICONSTOP|MB_OK "${MESSAGE}" /SD IDOK
  Abort "${MESSAGE}"
!macroend

Section "Aggrega" SecAggrega
  ; Always installed: the components page shows it selected and greyed.
  SectionIn RO
  SetOutPath "$INSTDIR"
  ; A file that cannot be replaced, most often an aggrega.exe still running, is
  ; reported and stops the installer rather than being skipped in silence.
  SetOverwrite try

  ClearErrors
  File "/oname=aggrega.exe" "${AGGREGA_EXE}"
  ${If} ${Errors}
    !insertmacro STOP "$INSTDIR\aggrega.exe could not be replaced. If aggrega is running, started by an editor or an AI client, close that and run the installer again."
  ${EndIf}

  ; EnVar.dll is placed whether or not the PATH is edited: the uninstaller runs
  ; it to remove the folder from the PATH when an install put it there.
  ClearErrors
  File "/oname=EnVar.dll" "${PLUGIN_DIR}/EnVar.dll"
  ${If} ${Errors}
    !insertmacro STOP "$INSTDIR\EnVar.dll could not be replaced."
  ${EndIf}

  ClearErrors
  File "/oname=LICENSE" "${LICENSE_FILE}"
  ${If} ${Errors}
    !insertmacro STOP "$INSTDIR\LICENSE could not be replaced."
  ${EndIf}

  ClearErrors
  WriteUninstaller "$INSTDIR\uninstall.exe"
  ${If} ${Errors}
    !insertmacro STOP "$INSTDIR\uninstall.exe could not be written."
  ${EndIf}
  SetOverwrite on

  ; The section below replaces these when it runs.
  StrCpy $PathLine "aggrega was not added to your PATH."
  StrCpy $PathNote "This install did not add its folder to your PATH, so unless an earlier install put it there, a terminal runs aggrega as $INSTDIR\aggrega.exe."
SectionEnd

Section "Add to PATH" SecPath
  ; Adds the folder to the user's PATH unless it is there already, so
  ; installing over an earlier copy leaves one entry.
  EnVar::SetHKCU
  EnVar::AddValue "Path" "$INSTDIR"
  Pop $0
  ${If} $0 != 0
    !insertmacro STOP "$INSTDIR could not be added to your PATH (EnVar error $0)."
  ${EndIf}
  StrCpy $PathLine "aggrega is on your PATH."
  StrCpy $PathNote "Its folder is on your PATH: a terminal opened from now on runs it as aggrega; one already open keeps the PATH it started with."
SectionEnd

; Registers the uninstaller in the user's list of installed apps, once the
; sections above have run.
Section "-Register"
  ; The size in KB the list shows: that of the files the first section installs,
  ; which makensis records when it compiles; the uninstaller is not counted.
  SectionGetSize ${SecAggrega} $1

  ClearErrors
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayName" "Aggrega"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "Publisher" "Cognitio s.r.l."
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayIcon" "$INSTDIR\uninstall.exe"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "URLInfoAbout" "https://aggrega.ai"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "HelpLink" "https://aggrega.ai/docs/install/"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "URLUpdateInfo" "https://github.com/aggrega-ai/aggrega-cli/releases"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegDWORD HKCU "${UNINSTALL_KEY}" "EstimatedSize" $1
  WriteRegStr HKCU "${UNINSTALL_KEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegStr HKCU "${UNINSTALL_KEY}" "QuietUninstallString" '"$INSTDIR\uninstall.exe" /S'
  WriteRegDWORD HKCU "${UNINSTALL_KEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINSTALL_KEY}" "NoRepair" 1
  ${If} ${Errors}
    !insertmacro STOP "The uninstaller could not be registered in your list of installed apps."
  ${EndIf}
SectionEnd

!insertmacro MUI_FUNCTION_DESCRIPTION_BEGIN
  !insertmacro MUI_DESCRIPTION_TEXT ${SecAggrega} "aggrega.exe, and the uninstaller that removes it."
  !insertmacro MUI_DESCRIPTION_TEXT ${SecPath} "Adds the install folder to your user PATH, so a new terminal runs aggrega by name. Cleared, aggrega is run by its full path."
!insertmacro MUI_FUNCTION_DESCRIPTION_END

; The finish page comes after the install, so there is nothing to go back to
; and nothing to cancel: Back and Cancel are disabled, and Finish closes Setup.
; Its box holds finish.txt, which makensis stores when it compiles; what this
; install did with the PATH is known only now, so it is inserted ahead of that
; text as the box's first paragraph. The box is read-only to the user, so it is
; made writable for the insertion and read-only again. The page is shown once,
; Back being disabled, so the paragraph is inserted once.
Function FinishShow
  GetDlgItem $0 $HWNDPARENT 3
  EnableWindow $0 0
  GetDlgItem $0 $HWNDPARENT 2
  EnableWindow $0 0

  FindWindow $0 "#32770" "" $HWNDPARENT
  GetDlgItem $0 $0 1000
  SendMessage $0 ${EM_SETREADONLY} 0 0
  SendMessage $0 ${EM_SETSEL} 0 0
  SendMessage $0 ${EM_REPLACESEL} 0 "STR:$PathNote$\r$\n$\r$\n"
  SendMessage $0 ${EM_SETREADONLY} 1 0
  SendMessage $0 ${EM_SETSEL} 0 0
FunctionEnd

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
  Delete "$INSTDIR\LICENSE"
  Delete "$INSTDIR\uninstall.exe"
  DeleteRegKey HKCU "${UNINSTALL_KEY}"
  ; The folder goes only when nothing else is left in it.
  SetOutPath "$TEMP"
  RMDir "$INSTDIR"
SectionEnd
