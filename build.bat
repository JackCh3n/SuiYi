@echo off
REM ============================================================
REM SuiYi unified build script (desktop GUI + local server in one exe)
REM
REM   build.bat         build build\suiyi.exe and start it (no console window)
REM   build.bat debug   build and start it WITH the console window (-debug)
REM
REM Build flags:
REM   -tags production  Wails must use the embedded frontend (dev mode fails)
REM   -H=windowsgui     hide the console window by default; -debug allocates it
REM
REM NOTE: keep this file pure ASCII to avoid codepage issues
REM ============================================================
setlocal
cd /d "%~dp0"

echo [1/3] Killing running suiyi / llama-server processes...
taskkill /F /IM suiyi.exe /T >nul 2>&1
taskkill /F /IM suiyi-gui.exe /T >nul 2>&1
taskkill /F /IM llama-server.exe /T >nul 2>&1
powershell -NoProfile -Command "Start-Sleep -Milliseconds 800" >nul 2>&1

echo [2/3] Generating icon resource (winres/syso) ...
if not exist "%USERPROFILE%\go\bin\rsrc.exe" (
  echo   Installing rsrc tool...
  go install github.com/akavel/rsrc@latest
  if errorlevel 1 (
    echo   rsrc install FAILED - icon resource skipped.
    goto :build
  )
)
"%USERPROFILE%\go\bin\rsrc.exe" -arch amd64 -ico assets\appicon.ico -o rsrc_windows_amd64.syso
if errorlevel 1 echo   rsrc FAILED - building without icon.

:build
echo [3/3] Building build\suiyi.exe ...
if not exist build mkdir build
go build -tags production -trimpath -ldflags "-s -w -H=windowsgui" -o build\suiyi.exe .
if errorlevel 1 (
  echo Build FAILED. Check Go toolchain and code.
  pause
  exit /b 1
)
echo Built: build\suiyi.exe

if /I "%~1"=="debug" (
  echo Starting with console window: build\suiyi.exe -debug
  start "SuiYi" "build\suiyi.exe" -debug
) else (
  echo Starting desktop GUI: build\suiyi.exe
  echo   (run "build.bat debug" to see the console / logs)
  start "" "build\suiyi.exe"
)
endlocal
