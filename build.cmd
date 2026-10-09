@echo off
setlocal

set VERSION=1.0.1
set OUTDIR=dist
set OUTNAME=LANFileShare_v%VERSION%.exe

if not exist "%OUTDIR%" mkdir "%OUTDIR%"

echo [1/3] gofmt
gofmt -l .
if errorlevel 1 goto :fail

echo [2/3] go vet
go vet ./...
if errorlevel 1 goto :fail

echo [3/3] go build %OUTNAME%
set CGO_ENABLED=0
go build -trimpath -ldflags "-s -w" -o "%OUTDIR%\%OUTNAME%" .
if errorlevel 1 goto :fail

echo.
echo 构建完成: %OUTDIR%\%OUTNAME%
dir /b "%OUTDIR%"
exit /b 0

:fail
echo.
echo 构建失败，请检查上面的输出。
exit /b 1
