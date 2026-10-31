# GitHub 更新与安装包约定

桌面“设置 → 软件更新”已接入版本检查、下载和安装交接。当前源码目录仍按开发版运行，不能直接覆盖升级。安装包按计划稍后制作；完成以下约定和实机验证后，安装版才具备完整自动升级能力。

## 使用过程

启动时后台检查，24 小时内最多自动请求一次。检查失败不弹窗；手动“检查更新”会显示错误，可以再次点击重试。发现新版本后显示版本号和 Release 正文，用户可以下载、稍后提醒或跳过本版。手动检查不受跳过记录限制。

用户同意后下载**完整 MSI 安装包**，校验大小与 SHA-256，再选择现在重启或稍后安装。这里是原位升级，不需要先卸载。它不是差分下载，也不会因为 GitHub 上多了一次代码提交就触发更新。

安装前暂停新任务与新媒体下载，等待已经接收的任务、文件发送和清理完成。已有任务仍能收到审批；等待可以取消，取消后恢复接收新任务。清理结果不明确时不自动安装。安装结束重新打开桌面，群服务继续遵循现有的手动启动方式。

如果桌面在等待更新期间意外退出，重新打开后可以在软件更新中点击“取消更新操作”，恢复后台接收新任务。

“打开更新日志”会打开当前部署的 `runtime/updates`。安装交接日志在其中的 `handoff-*` 目录；失败结果也写入 `install-result.json`。安装失败后重启桌面时会显示失败提示。

## 发布时必须保持一致

- 仓库固定为 `iwanttobeacmer666-ctrl/TONGXU`，使用 GitHub `releases/latest` 接口，不嵌入访问令牌。仓库需要公开。
- 修改 `desktop/tongxu.csproj` 的 `Version`，用同一个三段版本号生成 MSI 的 `ProductVersion` 和 Release tag，例如 `1.0.1` / `v1.0.1`。当前开发基线为 `1.0.0`。不接受预发布后缀或四段 tag。
- 发布正式稳定版 Release，不能是 Draft 或 Prerelease。上传 `Tongxu-1.0.1-win-x64.msi`；ARM64 对应 `Tongxu-1.0.1-win-arm64.msi`。版本与架构必须对应实际构建。
- 安装包必须出现在 Release assets 中，并提供 GitHub 的 `sha256:` digest。缺少匹配包或校验信息时应用停止更新，不尝试运行其他附件。
- 下载仅允许 GitHub 和指定 GitHub release CDN 的 HTTPS 地址；不采用网盘或第三方代理。

Release 正文直接作为更新说明显示，不执行 HTML 或脚本。发布前请写清用户能看到的变化。

## MSI 制作要求

目前只定义交接协议，没有安装或下载制作 MSI 的工具，也没有生成安装包。

| 项目 | 约定 |
|---|---|
| 安装范围 | 当前 Windows 用户；无需管理员权限；不得加入提权自定义动作 |
| 默认程序目录 | 可采用当前用户的 Programs 目录；允许安装到 D 盘的专用目录 |
| 目录结构 | 保留根目录脚本、`bin/group-assistant.exe` 和 `bin/desktop/tongxu.exe` 及相邻依赖 |
| UpgradeCode | `{A15AB6E2-CE98-48BD-B8E4-8D11F58A24D1}`，后续版本保持不变 |
| 协议属性 | MSI Property 表中的 `TONGXU_UPDATE_PROTOCOL=1` |
| 更新参数 | `/passive /norestart ALLUSERS=2 MSIINSTALLPERUSER=1 TONGXU_UPDATE=1 INSTALLFOLDER="原目录"` |
| 安装标记 | 程序根目录安装 `installed-layout.json`，内容为 `{"Version":1}` |
| 回滚 | 使用 MSI 事务；旧版移除必须放在可回滚事务内，不得关闭 rollback |
| 启动 | 更新模式不由 MSI 启动桌面，交接程序确认完成后启动，避免重复启动 |

MSI 附带 `Runtime-Paths.ps1` 与 `Install-Update.ps1`。更新交接程序复制到用户数据区运行，复核 MSI 的版本、UpgradeCode、协议属性和文件哈希，等待旧桌面退出，取得桌面、启动器、配置和后台服务锁后才运行系统 `msiexec.exe`。执行前再核对已安装版本，拒绝重复安装或降级。不会绕过 PowerShell 策略、请求管理员权限或重启 Windows。

不要把真实 `config.group-assistant.json`、`runtime`、登录凭据、WORK、源码缓存或测试证据打进安装包。根目录配置应使用不含机主资料的发布模板。安装与卸载不得删除用户数据目录或自定义工作目录。

## 数据目录与旧版兼容

没有安装标记时，开发版继续使用原来的 `runtime` 和已保存路径。加入安装标记后，安装版的配置和桌面偏好位于 `%LOCALAPPDATA%\Tongxu\runtime`；新安装默认将文档、临时任务、软件与状态分别放在 `%LOCALAPPDATA%\Tongxu` 下的 `files/tmp/software/state`。已有 `Paths` 设置优先，不覆盖用户选择。

LocalApplicationData 取 Windows 当前用户目录，是为了与安装目录分离，并非要求所有工作数据放到 C 盘。工作目录和专用登录目录仍可在配置中选择 D 盘。

旧部署迁移前先关闭桌面和服务，在同一 Windows 账户下手动运行：

```powershell
.\Prepare-InstalledData.ps1 -SourceRoot 'D:\MY_SHARE\group-assistant'
```

脚本只准备安装版的配置和桌面偏好副本，将原来生效的自定义路径完整写入新配置，不搬移文件，也不复制或删除 Codex 登录目录。目标已经存在时拒绝覆盖。**原来的工作目录和专用登录目录仍会被使用，迁移后不要删除它们，也不要同时启动新旧部署。** 本次开发没有执行迁移。

## 打包阶段必须补做的验证

使用两个真实版本测试首次安装、原位升级、安装失败回滚、安装取消、磁盘空间不足、文件占用以及升级后的版本与重启。确认配置、DPAPI 凭据、用户文件、自定义路径和专用登录均保留。用普通用户账户验证无需提权；核对 .NET 运行时的打包策略。

现有离线测试使用合成网络响应与假后台，能验证版本、下载校验、界面和任务排空行为，不能代替上述安装包验收，也不能证明真实网络环境能访问 GitHub。

接口依据：[GitHub Releases API](https://docs.github.com/en/rest/releases/releases)、[Windows Installer 参数](https://learn.microsoft.com/en-us/windows/win32/msi/standard-installer-command-line-options)、[用户安装范围](https://learn.microsoft.com/en-us/windows/win32/msi/installation-context)、[安装回滚](https://learn.microsoft.com/en-us/windows/win32/msi/rollback-installation)。
