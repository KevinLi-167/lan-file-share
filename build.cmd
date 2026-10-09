@echo off
setlocal

set VERSION=1.0.1
set OUTDIR=dist
set OUTNAME=LANFileShare_v%VERSION%.exe

if not exist "%OUTDIR%" mkdir "%OUTDIR%"

echo [1/3] gofmt
rem gofmt -l 只会列出未格式化的文件，退出码仍是 0，所以要自己判空
set FMTBAD=
for /f "delims=" %%i in ('gofmt -l .') do set FMTBAD=1
if defined FMTBAD goto :fail

echo [2/3] go vet
go vet ./...
if errorlevel 1 goto :fail

echo [3/3] go build %OUTNAME%
rem CGO_ENABLED=0 纯静态编译，与 .github/workflows/release.yml 保持一致
set CGO_ENABLED=0
rem -X main.version 把版本号写进二进制（main.go 里 version 必须是变量才能被覆盖）
go build -trimpath -ldflags "-s -w -X main.version=%VERSION%" -o "%OUTDIR%\%OUTNAME%" .
if errorlevel 1 goto :fail

echo.
echo 构建完成: %OUTDIR%\%OUTNAME%
dir /b "%OUTDIR%"
exit /b 0

:fail
echo.
echo 构建失败，请检查上面的输出。
exit /b 1
