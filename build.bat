@echo off
setlocal
cd /d "%~dp0"
set GOWORK=off
set GOPROXY=off
set GOSUMDB=off
set CGO_ENABLED=0
go run ./cmd/mary-skelter-deps restore --platform windows-amd64
if errorlevel 1 exit /b %errorlevel%
go run ./cmd/mary-skelter-deps restore-go
if errorlevel 1 exit /b %errorlevel%
if not exist "toolchain\bin\windows-amd64\bcn-tid-go.exe" (
  if not exist "toolchain\bin\windows-amd64" mkdir "toolchain\bin\windows-amd64"
  pushd tools\bcn-tid-go
  set GOTOOLCHAIN=local
  go build -trimpath -buildvcs=false -o "..\..\toolchain\bin\windows-amd64\bcn-tid-go.exe" .
  if errorlevel 1 (popd & exit /b 1)
  popd
)
if not exist "toolchain\bin\windows-amd64\mary-skelter-runtime-qa.exe" (
  go build -trimpath -buildvcs=false -o "toolchain\bin\windows-amd64\mary-skelter-runtime-qa.exe" ./cmd/mary-skelter-runtime-qa
  if errorlevel 1 exit /b %errorlevel%
)
go run ./cmd/mary-skelter-builder %*
