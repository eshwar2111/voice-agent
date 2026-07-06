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

| metric | before | after (filled at end) |
|---|---|---|
| Binary size (bytes) | 58,379,847 (≈ 55.7 MB) | |
| Binary size (`ls -la voice-agent.exe`) | 58379847 | |
| Idle RAM — WorkingSet64 (bytes) | 55,422,976 (≈ 52.9 MB) | |
| Idle RAM — PrivateMemorySize64 (bytes) | 62,484,480 (≈ 59.6 MB) | |
| Cold start (wall clock, approximate) | ~8-9.5 s (stable by the 8 s wait; total measured wall time ~9.6 s including script overhead) | |

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
