@echo off
setlocal

cd /d "%~dp0.."
if "%VERSION%"=="" set VERSION=1.0.0
if not exist dist mkdir dist

echo ==^> Running tests
go test ./...
if errorlevel 1 exit /b 1

echo ==^> Building Windows amd64
set CGO_ENABLED=0
set GOOS=windows
set GOARCH=amd64
go build -trimpath -ldflags "-s -w -H windowsgui -X main.version=%VERSION%" -o dist\clipbridge_windows_amd64.exe .
if errorlevel 1 exit /b 1

echo.
echo Built dist\clipbridge_windows_amd64.exe
endlocal
