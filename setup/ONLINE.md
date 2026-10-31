# Online installer

`TONGXU-Setup-1.0.0.exe` contains the installer UI and icon, not the application. Its filename and metadata identify bootstrapper 1.0.0; the interface does not show a version number. It targets Windows x64 and the system .NET Framework 4.x so that opening setup does not require .NET 10.

After the user clicks install, setup queries `https://api.github.com/repos/iwanttobeacmer666-ctrl/TONGXU/releases/latest`. The repository's latest stable release must provide exactly one `Tongxu-<version>-win-x64.zip` asset with a valid size, SHA-256 digest and canonical GitHub URL. Draft/prerelease metadata, missing packages and invalid checksums stop installation; no older-version or embedded fallback is used. Downloads use HTTPS with bounded redirects to explicitly allowed GitHub/CDN hosts, cancellation, timeout and size limits.

Once a release is resolved, setup detects .NET 10.0 Desktop/Core runtimes. If missing it asks before downloading Microsoft's latest compatible stable 10.0 Desktop Runtime and verifies SHA-512 plus Microsoft Authenticode chain/publisher before execution. That component can request elevation. Tongxu installation remains per-user. Existing .NET versions, PATH and execution policy are not changed by Tongxu setup.

Setup downloads and checks the selected ZIP before extraction. Its `tongxu-package.json` contains `Version`, `SetupProtocol:1`, `RuntimeMajor:10` and a `Files` map of relative paths to SHA-256 values. Necessary files, paths, expanded sizes, per-file hashes and actual desktop assembly version are checked before activation. Unsupported future runtime/protocol requirements prompt use of a newer bootstrapper.

The existing versioned program-directory, shortcut and uninstall implementation is reused. Configuration, login and workspace files remain separate. No development deployment is automatically migrated, and the bot never starts automatically.

## Build and tests

Use the installed SDK/cache and existing Go binary, without new dependencies:

```powershell
dotnet build ..\desktop\tongxu.csproj --no-restore -c Release -o .\desktop-check
.\Build-Setup.ps1 -DesktopDirectory .\desktop-check -OutputDirectory .\new-output
.\new-output\TONGXU-Setup-1.0.0.exe --self-test .\new-evidence .\new-output\Tongxu-1.0.0-win-x64.zip
```

Choose a new output directory each time; prepared ZIPs are not overwritten. Outputs are the small installer, separate program ZIP and checksum sidecars. The ZIP uses an explicit binary/script allowlist and sanitized public config; live operator IDs, credentials, runtime, caches and Codex binaries are excluded. Nothing is uploaded by the build.

The self-test uses an external local ZIP without installing/downloading. It checks manifest/file hashes, extraction, invalid paths, malicious/corrupt packages, stable-release selection and rejection, download size/hash verification, allowed redirect hosts, runtime matching, absence of embedded app resources and offscreen form rendering. The local test fixture currently expects app version 1.0.0.

`TONGXU-Setup-1.0.0.exe --verify-latest <new-evidence-directory>` verifies a published release through the installer's real network client: it downloads the latest program ZIP, checks release/package/file hashes and assembly version, then extracts only into the evidence directory. It does not install runtimes, write registry/shortcuts, or start Tongxu. Use this only when downloading the program for verification is authorized.

## Release contract and status

The public repository is `iwanttobeacmer666-ctrl/TONGXU`. Publish a stable Release such as `v1.0.0`, attach the generated `Tongxu-1.0.0-win-x64.zip`, and ensure GitHub reports its `sha256:` digest. Also attach the small installer if desired. Future `v1.0.1` must contain the matching `Tongxu-1.0.1-win-x64.zip` built from an assembly with version 1.0.1. Bootstrapper 1.0.0 can install later app versions compatible with protocol 1/.NET 10; it is not pinned to app 1.0.0.

This ZIP distribution does not replace the existing desktop MSI updater contract. That updater remains unchanged and still requires MSI release assets.

Publish the program ZIP and bootstrapper together as the stable Release and set it as latest. An older compatible bootstrapper will then resolve the latest program ZIP on every installation. Source code changes alone do not change what the installer downloads. Never reuse a version number for different program contents.

Verified locally: offline compilation and positive/negative tests. Clean-Windows/runtime installation, interactive upgrade/uninstall, CLI login and group tasks still require manual acceptance. The EXE remains unsigned.

API reference: [GitHub Releases](https://docs.github.com/en/rest/releases/releases#get-the-latest-release).
