# Windows installer (NSIS)

The Windows x64 installer uses NSIS 3 and packages an explicit allowlist from `dist/windows-ui-clean`. Never build a public installer from a personal `*-ready` folder. The public manifest contains only binaries, scripts, documentation and licenses; no connection example or user-data files are packaged.

```powershell
.\scripts\build-raw-installer.ps1 -Version 0.1.2
```

Use `-SkipBuild` only when the public UI bundle has already been built from this source. Artifacts are `dist/fturn-raw-<version>-windows-x64-setup.exe` and the public portable ZIP. `THIRD-PARTY-NOTICES.txt` includes the license texts from modules actually used by the core/UI and the Go standard library. The Wails product version must match the installer version.

The installer requests administrator privileges and installs to Program Files. It creates desktop / Start Menu shortcuts and an uninstall registration. It checks for a running GUI or installed core and stops rather than terminating them. Installation does not launch or connect the client unless the user chooses the finish-page launch checkbox.

`installer/setup-support.ps1` checks Microsoft WebView2 Runtime using the documented HKLM and HKCU registry values. If missing, it downloads the Evergreen bootstrapper from Microsoft's HTTPS endpoint, verifies a valid Microsoft Authenticode signature, runs `/silent /install`, and checks the resulting runtime version. See [Microsoft distribution documentation](https://learn.microsoft.com/en-us/microsoft-edge/webview2/concepts/distribution).

Uninstall cleans only this installation's saved routes using the existing ownership checks in `routes.ps1`, and startup tasks whose executable and description identify this installation. It leaves tasks belonging to another portable copy alone. It deletes an explicit list of program files, never recursively removes the chosen install directory, and preserves legacy configuration and `%LOCALAPPDATA%\fturn-raw` profiles/settings.

Checks:

```powershell
.\scripts\test-raw-installer-support.ps1
.\scripts\test-raw-installer.ps1
```

The support tests mock OS/network APIs and verify process guards, Runtime detection, signature checks and task ownership. The NSIS smoke test compiles a separately identified **test variant**: user-level registration, unique test shortcuts, no prerequisite installation, no route or task changes, no application launch. It installs in `dist/installer-smoke/installed`, verifies payload hashes, upgrades and uninstalls, and verifies that real AppData files have not changed. It leaves test-only legacy configuration and user-note sentinels to demonstrate preservation. This does not replace testing the elevated installer on a clean Windows VM.

## Separate public repository

`scripts/export-raw-source.ps1` exports only tracked source files from the configured allowlist, substitutes the Raw README and Windows build workflow, and starts no network operations. The separate repository has fresh history; the original experimental repository and personal dist folders are not published.

The root contains FturnRaw.exe and Uninstall.exe. Runtime executables/scripts are installed under runtime, legal texts under licenses and instructions under docs. The installer removes only known old public program files when upgrading a flat layout. The client migrates and archives old private JSON files in AppData after verifying the backup. Core identity and VK cooldown use LocalAppData/fturn-raw/core. The UI build compiles only the client core, without rebuilding server cores.

The CI release plan in scripts/ci/release-plan.json controls whether server packages are also built/published. Client-only 0.1.2 sets serverPackages=false; separate server build commands remain available.
