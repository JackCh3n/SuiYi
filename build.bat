@echo off
REM ============================================================
REM SuiYi local build script
REM   [1] Kill running service processes (suiyi.exe / llama-server.exe)
REM   [2] Build build\suiyi.exe
REM   [3] Start service and open browser
REM NOTE: keep this file pure ASCII to avoid codepage issues
REM NOTE: use powershell Start-Sleep instead of timeout (timeout
REM       fails with "Input redirection is not supported" when
REM       stdin is redirected)
REM ============================================================
setlocal

cd /d "%~dp0"

echo [1/3] Killing running suiyi / llama-server processes...
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
"%USERPROFILE%\go\bin\rsrc.exe" -arch amd64 -ico gui\assets\appicon.ico -o rsrc_windows_amd64.syso
if errorlevel 1 echo   rsrc FAILED - building without icon.

:build
echo [3/3] Building build\suiyi.exe ...
if not exist build mkdir build
go build -trimpath -ldflags "-s -w" -o build\suiyi.exe .
if errorlevel 1 (
  echo Build FAILED. Check Go toolchain and code.
  pause
  exit /b 1
)
echo Built: build\suiyi.exe

echo [4/4] Starting service and opening browser...
start "SuiYi" "build\suiyi.exe" serve
powershell -NoProfile -Command "Start-Sleep -Seconds 3" >nul 2>&1
start "" http://127.0.0.1:8848

echo Done. Web UI: http://127.0.0.1:8848
endlocal
