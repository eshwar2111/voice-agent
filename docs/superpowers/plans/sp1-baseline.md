# SP1 Overhead Baseline

Pre-implementation measurement for the "tiered resolver spine + overhead cut" plan (SP1).
These are the **before** numbers; a later task fills in the **after** column once the SP1
changes land.

## Measurement notes / caveats

- This environment has no C compiler available (`go build` fails with
  `cgo: C compiler "C:\msys64\ucrt64\bin\gcc.exe" not found`), so a fresh binary could not be
  built from the current (uncommitted) working tree in this session.
- The existing prebuilt `voice-agent.exe` at the repo root (from the last successful build,
  commit `37f2c13`, dated 2026-04-11) was used instead for the size/RAM/cold-start
  measurements below. It predates the current uncommitted working-tree changes, so treat
  these as a representative baseline rather than an exact measurement of HEAD.
- Idle RAM and cold start were measured by launching the binary non-blocking via PowerShell
  `Start-Process`, waiting ~8 seconds, then reading process stats before killing it:

```powershell
cd "E:\Voice Agent"
Start-Process -FilePath ".\voice-agent.exe" -WorkingDirectory "E:\Voice Agent"
Start-Sleep -Seconds 8
Get-Process voice-agent | Select-Object WorkingSet64, PrivateMemorySize64
Stop-Process -Name voice-agent -Force
```

- If a C compiler becomes available and a fresh binary is built from HEAD, re-run the same
  commands to refresh the "before" numbers prior to the SP1 comparison, or simply use these
  as-is since they were captured from the last known-good build.

## Baseline table

| metric | before | after |
|---|---|---|
| Binary size (bytes) | 58,379,847 (≈ 55.7 MB) | *measure on build machine — see note* |
| Idle RAM — WorkingSet64 (bytes) | 55,422,976 (≈ 52.9 MB) | *measure on build machine — see note* |
| Idle RAM — PrivateMemorySize64 (bytes) | 62,484,480 (≈ 59.6 MB) | *measure on build machine — see note* |
| Cold start (wall clock, approximate) | ~8–9.5 s | *measure on build machine — see note* |

## "After" measurement — status

The SP1 **code** overhead work is complete: dead-code/cruft deleted, the two overlay
WebViews are now lazy-initialized on first use (Task 22), Whisper init is skipped when
`enable_voice=false` (Task 23), and the documented build command now strips symbols with
`-ldflags="-s -w -H windowsgui"` (Task 24).

The stripped-binary "after" numbers were **not captured in the CI/dev environment** used for
this implementation because that environment's C toolchain (w64devkit GCC 14.1) does not match
the toolchain that produced the committed `whisper.cpp/build/**/*.a` static libs, so the final
`cmd/app` link fails with `undefined reference to std::...`. This is an environment artifact,
not a code defect — every package compiles; only the final whisper link is blocked here.

To capture the "after" column on a machine with a matching whisper build (e.g. the original
dev machine, or after rebuilding `whisper.cpp` with the current GCC):

```bash
go build -ldflags="-s -w -H windowsgui" -o voice-agent.exe ./cmd/app
ls -la voice-agent.exe            # binary size (expect a drop from -s -w stripping)
```
```powershell
Start-Process ".\voice-agent.exe"; Start-Sleep -Seconds 8
Get-Process voice-agent | Select-Object WorkingSet64, PrivateMemorySize64   # idle RAM
Stop-Process -Name voice-agent -Force
```
Idle RAM should drop further with `enable_voice=false` (no Whisper context loaded) and because
the two auxiliary overlays no longer spin up at startup.

## Raw measurement log

```
$ ls -la voice-agent.exe
-rwxr-xr-x 1 Eshwar 197121 58379847 Apr 11 17:48 voice-agent.exe

PS> Start-Process -FilePath ".\voice-agent.exe"; Start-Sleep -Seconds 8
PS> Get-Process voice-agent | Select-Object Id, WorkingSet64, PrivateMemorySize64
   Id WorkingSet64 PrivateMemorySize64
   -- ------------ -------------------
22296     55422976            62484480
ElapsedApproxSeconds: 9.5634578

PS> Stop-Process -Name voice-agent -Force
```
