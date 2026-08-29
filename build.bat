@echo off
chcp 65001 >nul
REM ============================================================
REM 随译 SuiYi 本地构建脚本
REM   [1] 终止运行中的服务进程（suiyi.exe / llama-server.exe）
REM   [2] 构建 build\suiyi.exe
REM   [3] 启动服务并打开浏览器
REM ============================================================
setlocal

cd /d "%~dp0"

echo [1/3] 终止运行中的 suiyi / llama-server 进程...
taskkill /F /IM suiyi.exe /T >nul 2>&1
taskkill /F /IM llama-server.exe /T >nul 2>&1
timeout /t 1 /nobreak >nul

echo [2/3] 构建 build\suiyi.exe ...
if not exist build mkdir build
go build -trimpath -ldflags "-s -w" -o build\suiyi.exe .
if errorlevel 1 (
  echo 构建失败！请检查 Go 环境与代码。
  pause
  exit /b 1
)
echo 构建完成: build\suiyi.exe

echo [3/3] 启动服务并打开浏览器...
start "SuiYi" "build\suiyi.exe" serve
timeout /t 3 /nobreak >nul
start "" http://127.0.0.1:8848

echo 完成。管理界面: http://127.0.0.1:8848
endlocal
