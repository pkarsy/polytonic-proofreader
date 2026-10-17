@echo off
setlocal
echo NOTE: build.bat has never been run on Windows - see the comments in this file.
rem
rem Build the proofreader for Windows from the single source file.
rem
rem ===========================================================================
rem  UNTESTED ON WINDOWS
rem
rem  This script was written and reviewed on Linux. Its source transformation is
rem  the same one `./build.sh --no-restart` performs, and that path is verified
rem  (it cross-compiles to a PE32+ Windows executable). The batch/PowerShell
rem  invocation below, however, has never been executed on a real Windows
rem  machine. If it fails, the likely culprit is the quoting of the four
rem  `set "PS=..."` lines below.
rem ===========================================================================
rem
rem Windows has no syscall.Exec, so the restart implementation is swapped in a
rem temporary copy for os/exec + os.Exit, and the restart setup is disabled so
rem the button is shown as unavailable. proofreader.go itself is unchanged.
rem
rem Requires the Go toolchain and Windows PowerShell (both ship with Windows 10+).

set "TMPGO=%TEMP%\proofreader_variant.go"

set "PS=$s = Get-Content -Raw -Encoding UTF8 'proofreader.go';"
set "PS=%PS% $s = $s -replace '(?m)^[ \t]*\x22syscall\x22[ \t]*\r?\n','';"
set "PS=%PS% $s = $s -replace '(?m)^[ \t]*return syscall\.Exec\(cmd\[0\], cmd, os\.Environ\(\)\)[ \t]*\r?$','c := exec.Command(cmd[0], cmd[1:]...); c.Env = os.Environ(); c.Stdout = os.Stdout; c.Stderr = os.Stderr; if err := c.Start(); err != nil { return err }; os.Exit(0); return nil';"
set "PS=%PS% $s = $s -replace '(?m)^[ \t]*src := findGoSource\(\)[ \t]*\r?$','src := findGoSource(); src = ``';"
set "PS=%PS% [IO.File]::WriteAllText($env:TEMP + '\proofreader_variant.go', $s, (New-Object Text.UTF8Encoding $false))"

powershell -NoProfile -ExecutionPolicy Bypass -Command "%PS%"
if errorlevel 1 goto fail

go build -o proofreader.exe "%TMPGO%"
if errorlevel 1 goto fail

del "%TMPGO%"
echo Built proofreader.exe (restart disabled)
exit /b 0

:fail
if exist "%TMPGO%" del "%TMPGO%"
echo Build failed
exit /b 1
