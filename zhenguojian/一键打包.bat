@echo off
chcp 65001 >nul
rem zhenguojian one-click Windows build (self-checking launcher)
cd /d %~dp0

set "PS1="
if exist "%~dp0build\build-win.ps1" set "PS1=%~dp0build\build-win.ps1"
if exist "%~dp0packaging\build-win.ps1" set "PS1=%~dp0packaging\build-win.ps1"
if not "%PS1%"=="" goto :toolcheck
echo [X] MISSING  build-win.ps1  (checked build\ and packaging\ next to this bat)
echo     This script is not in git, and Syncthing ignores **/build, so
echo     plain copies/clones will not carry it over. Copy it manually
echo     from the main dev PC, or just download the CI-built zip from
echo     GitHub Releases instead of building on this machine.
goto :end

:toolcheck
set MISSING=
where git    >nul 2>nul || set MISSING=%MISSING% git
where go     >nul 2>nul || set MISSING=%MISSING% go
where python >nul 2>nul || set MISSING=%MISSING% python
if not "%MISSING%"=="" (
  echo [X] MISSING TOOL^S in PATH:%MISSING%
  echo     Building needs Git + Go + Python x64 installed on THIS PC.
  echo     For a runnable program you do NOT need to build anywhere:
  echo     grab zhenGuoJian-windows-x64.zip from the private GitHub Release.
  goto :end
)

powershell -NoProfile -ExecutionPolicy Bypass -File "%PS1%" %*
if errorlevel 1 echo BUILD FAILED - see log above.

:end
echo.
pause
