@echo off
REM ============================================================
REM SuiYi desktop GUI build script (Wails v2.12 + WebView2)
REM   Build build\suiyi-gui.exe (requires -tags production)
REM   Note: keep pure ASCII to avoid codepage issues
REM ============================================================
setlocal
cd /d "%~dp0"

echo [1/2] Killing running suiyi / llama-server processes...
taskkill /F /IM suiyi-gui.exe /T >nul 2>&1
taskkill /F /IM suiyi.exe /T >nul 2>&1
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
"%USERPROFILE%\go\bin\rsrc.exe" -arch amd64 -ico gui\assets\appicon.ico -o gui\rsrc_windows_amd64.syso
if errorlevel 1 echo   rsrc FAILED - building without icon.

:build
echo [3/3] Building build\suiyi-gui.exe ...
if not exist build mkdir build
go build -tags production -trimpath -ldflags "-s -w" -o build\suiyi-gui.exe ./gui
if errorlevel 1 (
  echo Build FAILED. Check Go toolchain and code.
  pause
  exit /b 1
)
echo Built: build\suiyi-gui.exe
echo Run:  build\suiyi-gui.exe
endlocal
