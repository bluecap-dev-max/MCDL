# mcdl — Minecraft 全版本下载器

一个**单文件、无依赖**的 Windows 终端工具（TUI + 命令行），
从 Mojang 官方接口拉取 Minecraft Java 版**全部历史版本列表**（截至撰写时 926 个：正式版 103 / 快照 762 / Beta 26 / Alpha 35，数量随官方更新），
勾选需要的版本批量下载，直接落成启动器认得的 `.minecraft` 目录结构。

> ### ⚠️ 免责声明（请先阅读）
>
> - 本项目是**非官方第三方工具**，与 **Mojang Studios、Microsoft、Minecraft 官方**没有任何关系，
>   未获得其授权、认可或背书。
> - **Minecraft** 及相关名称、图标、游戏文件、资源、代码的版权与商标均归 **Mojang Studios** 所有。
> - 本工具**不包含、不托管、不分发、不修改**任何 Minecraft 游戏文件。它只是一个下载客户端，
>   把 **Mojang 官方 CDN 上公开可下载的文件**（以及第三方镜像提供的同一批文件）保存到本地。
> - 使用本工具下载的资源，其使用须遵守 [Minecraft 最终用户许可协议 (EULA)](https://www.minecraft.net/eula)
>   及所在地区的法律法规。**游玩请通过官方渠道购买正版**。
> - 本工具**不提供**登录、账号、破解、绕过正版验证等任何功能。
> - 第三方镜像（如 BMCLAPI）由第三方独立运营，与本项目无关；其可用性、内容与安全性由该第三方负责。
> - 本项目仅供**学习与个人使用**，按“现状”提供，不附带任何担保，使用风险自负。

---

## 目录

- [它是什么](#它是什么)
- [界面示意](#界面示意)
- [特性](#特性)
- [快速开始](#快速开始)
- [快捷键](#快捷键)
- [适配第三方启动器](#适配第三方启动器)
- [下载源与国内加速](#下载源与国内加速)
- [输出结构](#输出结构)
- [命令行参数](#命令行参数)
- [配置文件](#配置文件)
- [从源码编译](#从源码编译)
- [常见问题](#常见问题)
- [项目结构](#项目结构)
- [数据来源](#数据来源)
- [免责声明](#免责声明)
- [致谢](#致谢)

---

## 它是什么

Minecraft Java 版的启动器（官方 / PCL / HMCL / Prism 等）都需要一套固定的本地目录结构：
`versions/`、`assets/`、`libraries/`。

手动凑齐这些文件有点小难（bushi）：自从小孩哥的浮木举报pcl之后各大启动器全都下架了下载功能，因而为了舒服一点，我vibe出了`mcdl` ：

- 拉取**官方版本清单**，列出全部版本（正式版 / 快照 / Beta / Alpha）
- 让你在终端里**搜索、筛选、多选**
- 并发下载并校验，输出成标准目录，**启动器打开就能看到、能直接启动**

它**只是下载器**，不启动游戏。

## 界面示意

```
 Minecraft 全版本下载器  mcdl           启动器: PCL 2/3  输出: D:\mc\26.2  并发: 8
 筛选: [全部]  正式版   快照   Beta   Alpha    搜索:    已选 2 / 926

┌ 版本列表 ───────────────────────────────────┐┌ 下载任务 ─────────────────────────────────┐
│ >[ ] 26.4-snapshot-7   快照 2026-11-03       ││                                            │
│  [ ] 26.4-snapshot-6   快照 2026-10-27       ││ 26.3                62%   158.4 MB         │
│  [*] 26.3             正式 2026-09-15        ││ 1.21.4             100%    94.1 MB         │
│  [ ] 1.21.4           正式 2024-12-03        ││                                            │
└──────────────────────────────────────────────┘└────────────────────────────────────────────┘
 进度 [#######################-----------]  62.14%  98.4 MB / 158.4 MB  3.3 MB/s  剩余 00:42
 状态: 下载中   已选 2 个版本   文件 128   失败 0
 移动: ↑↓/jk  PgUp/PgDn 翻页  Home/End 首尾  空格 勾选  a 全选  A 清空
 操作: Enter 开始  p 暂停  c 取消  r 补齐  o 打开  x 打包  s 设置  / 搜索  ? 帮助  q 退出
 · 已获取 926 个版本（正式版 103 / 快照 762 / Beta 26 / Alpha 35）
 · 检测到的启动器：PCL 2/3(D:\mc\26.2)
```

## 特性

**下载**

- 并发下载（默认 8，可调 1–32）+ 指数退避重试
- 断点续传（`.part` + HTTP Range），中断后接着下
- SHA-1 校验（Mojang 提供），失败自动重下
- 已存在且完整的文件自动跳过
- 按路径 / SHA-1 去重：多版本共享的依赖库、资源只下一次

**版本与内容**

- 全部历史版本，支持按类型筛选、按版本号搜索
- 客户端 / 服务端 / 官方映射 / 资源(assets) / 依赖库 分别可选
- 按 Mojang 规则正确过滤依赖库（系统 + CPU 架构 + 版本条件）
- 可选解包原生库（`lwjgl64.dll`、`OpenAL64.dll` …）到 `natives/`

**启动器适配**

- 自动探测 PCL / HMCL / Prism / MultiMC / 官方启动器的**真实游戏目录**
- 三种输出布局：标准 `.minecraft` / PCL 友好 / Prism·MultiMC 实例
- 一键打包整合包 zip（PCL、Prism 可直接导入）

**网络**

- 官方源 + BMCLAPI + BMCLAPI 备用，**自动测速选最快**，失败自动切源
- 支持自定义镜像前缀（主文件 / 资源 / 依赖库 / 元数据分别可配）

## 快速开始

下载 `mcdl.exe`，双击运行（Windows 10/11 的 Windows Terminal 或 CMD 均可）。

首次运行会自动探测你机器上的启动器并把输出目录对上去；
也可以在设置里手动指定。

### 方式一：交互界面（推荐）

```
mcdl.exe
```

1. `/` 搜索版本号（例如 `1.21.4`），或 `f` 切换类型筛选
2. `空格` 勾选，`a` 全选当前筛选结果
3. `Enter` 开始下载
4. 下完直接打开启动器，版本已经出现在启动列表里

### 方式二：命令行

```bash
# 下载全部版本的客户端
mcdl --cli --all --client

# 只下正式版和快照
mcdl --cli --all --type release --type snapshot

# 指定版本
mcdl --cli --version 1.21.4 --version 1.20.1 --client --server

# 只预览计划，不实际下载
mcdl --cli --version 1.21.4 --libraries --dry-run

# 自动测速选源（国内加速）
mcdl --cli --all --source auto

# 下载到 PCL 的游戏目录，并解包原生库
mcdl --cli --version 1.21.4 --launcher pcl --natives

# 看看本机装了哪些启动器、目录在哪
mcdl --launchers
```

## 快捷键

| 按键 | 作用 |
|---|---|
| `↑` `↓` / `j` `k` | 移动光标 |
| `PgUp` `PgDn` / `u` `d` | 上下翻页 |
| `Home` `End` / `g` `G` | 跳到首 / 尾 |
| `空格` | 勾选 / 取消勾选 |
| `a` / `A` | 全选当前筛选结果 / 清空选择 |
| `Enter` | 开始下载选中的版本 |
| `p` / `c` | 暂停继续 / 取消 |
| `r` | 重新检查并补齐缺失文件 |
| `o` | 打开输出目录 |
| `x` | 打包成整合包 zip |
| `l` | 导出「已下载版本清单」 |
| `f` / `Tab` | 切换类型筛选 |
| `/` | 搜索版本号 |
| `v` | 切换排序（最新 / 最旧） |
| `s` | 设置（目录 / 启动器 / 布局 / 下载源 / 并发 / 内容 / 镜像） |
| `?` | 帮助 |
| `q` | 退出 |

设置面板内：`↑↓` 选择项、`←→` 调整值、`空格`/`回车` 切换、`s` 保存、`Esc` 关闭。

## 适配第三方启动器

启动器的「版本文件夹」本质就是 `.minecraft/versions/<id>/`，需要 `<id>.json` + `<id>.jar`。
`mcdl` 会读出各启动器**自己配置文件里记录的游戏目录**，而不是猜默认路径：

| 启动器 | 探测方式 |
|---|---|
| **PCL 2/3** | 找 `Plain Craft Launcher 2.exe` → 读 `PCL\Setup.ini` 的 `LaunchFolderSelect` |
| **HMCL** | 找 `HMCL*.jar/exe` → 读同目录 `hmcl.json` / `.hmcl.json` 的 `gameDir`（兼容 `gameDirectories[0].path`） |
| **Prism** | 读 `%APPDATA%\PrismLauncher\prismlauncher.cfg` 的 `InstanceDir` |
| **MultiMC** | 读 `%APPDATA%\MultiMC\multimc.cfg` 的 `InstanceDir` |
| 官方启动器 | `%APPDATA%\.minecraft` |

三种输出布局：

| 布局 | 目录结构 | 适配 |
|---|---|---|
| **标准 .minecraft** | `<目录>/versions/` `assets/` `libraries/` | 官方启动器、HMCL、PCL、ATLauncher |
| **PCL 友好** | 同上 + `launcher_profiles.json` | PCL 3 / 官方启动器直接识别为游戏目录 |
| **Prism / MultiMC 实例** | `<目录>/<版本>/minecraft/…` + `instance.cfg` | Prism、MultiMC（把 `<目录>` 当 instances 目录） |

Prism 实例的 `instance.cfg` 会自动生成：

```ini
name=1.21.4
InstanceType=OneSix
IntendedVersion=1.21.4
```

### 打包整合包

按 `x` 把当前输出目录打包成 zip（内含 `.minecraft/`，PCL / Prism 可直接导入）：

- 输出位置：`<游戏目录>-整合包.zip`（**在游戏目录旁边**），例如 `D:\mc\26.2-整合包.zip`
- 打包时显示实时进度；被占用 / 无权读取的文件会跳过并提示，不会中断
- 源目录为空或不存在时明确报错，不会产出空 zip

## 下载源与国内加速

设置面板 `s` →「下载源」，或用 `--source`：

| 选项 | 地址 | 说明 |
|---|---|---|
| **自动（测速）** | 全部源 | 并发实测各源 256 KB 下载速度，选最快的 |
| 官方源 | Mojang 官方 CDN | `piston-data` / `libraries` / `resources` |
| BMCLAPI | `bmclapi2.bangbang93.com` | 国内常用镜像 |
| BMCLAPI 备用 | `bmclapi.bangbang93.com` | 备用域名，国内常常比主域名更快 |
| 自定义镜像 | 设置里的 4 个前缀 | 主文件 / 资源 / 依赖库 / 元数据分别可配 |

细节：

- 镜像覆盖**版本 JSON、客户端、服务端、依赖库、资源**；资源索引和官方映射没有可靠镜像，走官方。
- BMCLAPI 的 `/maven/` 只镜像 `libraries.minecraft.net`，Maven Central 上的库会自动走原始地址。
- 每个文件都记录**主源 + 备用源**，主源重试全部失败后自动切到官方源再试一轮。
- 标题栏实时显示当前实际使用的源。

## 输出结构

```
<输出目录>/
├─ versions/<id>/<id>.jar       客户端
├─ versions/<id>/<id>.json      版本元数据（启动器靠它识别版本）
├─ versions/<id>/server.jar     服务端（1.7.10+ 有）
├─ versions/<id>/client.txt     官方映射（可选）
├─ assets/indexes/<idx>.json    资源索引（可选）
├─ assets/objects/<xx>/<sha1>   资源文件（可选）
├─ libraries/<maven 路径>       依赖库（可选）
├─ natives/                     解包后的原生库（可选）
├─ launcher_profiles.json       启动器档案（PCL / 官方）
└─ version_manifest.local.json  已下载版本清单
```

## 命令行参数

| 参数 | 说明 |
|---|---|
| `-o`, `-out` | 输出目录（默认 `%USERPROFILE%\.minecraft`） |
| `-all` | 选择全部版本 |
| `-version` | 指定版本号，可重复 |
| `-type` | 限定类型 `release` / `snapshot` / `old_beta` / `old_alpha`，可重复 |
| `-client` / `-server` | 只下载客户端 / 服务端 |
| `-libraries` | 下载依赖库 |
| `-assets` | 下载资源文件 |
| `-mappings` | 下载官方反混淆映射 |
| `-natives` | 解包原生库到 `natives/` |
| `-layout` | 输出布局 `standard` / `pcl` / `prism` |
| `-launcher` | 目标启动器 `pcl` / `hmcl` / `mojang` / `prism` / `multimc` |
| `-source` | 下载源 `auto` / `official` / `bmclapi` / `bmclapi-alt` / `custom` |
| `-concurrency` | 并发下载数 |
| `-export-zip` | 下载完成后打包整合包 zip |
| `-dry-run` | 只显示下载计划，不实际下载 |
| `-launchers` | 列出检测到的启动器及其游戏目录 |
| `-cli` | 非交互命令行模式 |
| `-ascii` | 纯 ASCII 界面（老终端兼容） |
| `-help` | 显示帮助 |

> `-version` 与 `-type` 可重复使用；未指定 `-version`/`-type`/`-all` 时默认只下正式版。

## 配置文件

`%APPDATA%\mcdl\config.json`（TUI 内按 `s` 也可修改）。主要字段：

```json
{
  "outputDir": "D:\\mc\\26.2",
  "launcher": "pcl",
  "layout": "pcl",
  "source": "auto",
  "concurrency": 8,
  "downloadClient": true,
  "downloadServer": false,
  "downloadLibs": false,
  "downloadAssets": false,
  "extractNatives": false
}
```

> 用记事本编辑也没问题（会自动忽略 UTF-8 BOM）。

## 从源码编译

```bash
git clone <repo>
cd mcdl
go build -trimpath -ldflags "-s -w" -o mcdl.exe .
```

- 只依赖 **Go 标准库**（无任何第三方包，`go.mod` 里没有 require）
- Go 1.24+
- 主要面向 Windows（用到 Win32 控制台 API）；其他平台可编译，但终端交互未做适配

## 常见问题

**Q：下载完启动器里没有？**
先确认输出目录是不是启动器实际用的那个目录。运行 `mcdl --launchers` 看探测结果，
或在启动器里把「游戏目录」指到 mcdl 的输出目录。

**Q：下载到一半中断了？**
断点续传，重新运行会自动接着下（`.part` 文件）。按 `r` 可重新检查并补齐。

**Q：某个文件一直失败？**
多半是镜像缺文件或网络抖动。换下载源（`s` → 下载源 → 官方源）再试；工具本身也会自动切备用源。

**Q：需要 Java 吗？**
不需要。mcdl 只是下载器，不运行游戏。启动游戏需要对应版本的 Java，由启动器负责。

**Q：会和正版验证冲突吗？**
不会。它只下载公开文件，不接触账号、不修改游戏、不绕过任何验证。

**Q：占多大空间？**
只下客户端时，单个现代版本约 20–30 MB；全量 900+ 个版本客户端大约十几 GB。
勾选资源（assets）会显著增加占用。

## 项目结构

```
mcdl/
├─ main.go              入口、命令行参数、CLI 模式
├─ config.go            配置读写
├─ mojang.go            官方 API、版本元数据、依赖库规则
├─ source.go            下载源（官方 / BMCLAPI / 自定义）与测速
├─ engine.go            下载引擎（队列、并发、续传、校验、重试）
├─ launcher_compat.go   启动器目录探测、布局、natives 解包、打包
├─ ui.go                 TUI 主循环与状态
├─ ui_render.go          界面渲染
├─ ui_settings.go        设置面板
├─ console_windows.go    Windows 控制台（原始输入、UTF-8、VT）
├─ console_stream.go     管道/文件模式的按键解析
├─ console_other.go      非 Windows 兜底
└─ width.go              终端显示宽度（CJK 对齐）
```

## 数据来源

| 内容 | 地址 |
|---|---|
| 版本清单 | `launchermeta.mojang.com/mc/game/version_manifest_v2.json` |
| 版本元数据 | `piston-meta.mojang.com/v1/packages/<sha>/<id>.json` |
| 客户端 / 服务端 | `piston-data.mojang.com/v1/objects/<sha1>/client.jar` |
| 远古版本 | `launcher.mojang.com/v1/objects/<sha1>/client.jar` |
| 资源文件 | `resources.download.minecraft.net/<xx>/<sha1>` |
| 依赖库 | `libraries.minecraft.net/<maven 路径>` |

以上文件均由 **Mojang Studios** 提供，本项目仅做下载与整理。

## 免责声明

1. **非官方**：本项目是独立的第三方开源工具，与 **Mojang Studios、Microsoft、Minecraft 官方团队**
   无任何隶属、合作、赞助或授权关系。文中出现的 “Minecraft”“我的世界” 等名称仅用于说明用途。
2. **不提供游戏本体**：本仓库**不包含**任何 Minecraft 客户端/服务端/资源/代码，
   也不搭建任何文件服务器。所有文件均由用户从 **Mojang 官方 CDN** 或其第三方镜像实时下载。
3. **版权归属**：Minecraft 的商标、美术、音频、代码及其他一切内容，版权均归 **Mojang Studios** 所有。
   本项目的 GPL-3.0 许可**仅覆盖本仓库自身的源代码**，不覆盖任何 Minecraft 相关内容。
4. **合规使用**：请遵守 [Minecraft EULA](https://www.minecraft.net/eula) 及当地法律法规。
   游玩请通过**官方渠道购买正版**，本项目不提供账号、登录、破解或绕过验证的能力。
5. **第三方镜像**：BMCLAPI 等镜像由第三方运营，本项目不对其内容、可用性与安全性做任何担保，
   也不对其行为负责。
6. **无担保**：本软件按“现状”提供，不附带任何明示或暗示的担保。因使用本软件造成的任何后果
   （包括但不限于数据丢失、账号问题、网络流量消耗），由使用者自行承担。

## 致谢

- [BMCLAPI](https://bmclapi.bangbang93.com/) — 国内镜像
- 各启动器作者（PCL / HMCL / Prism / MultiMC）—— 目录结构与配置格式参考自其公开源码

## 许可

本项目**源代码**采用 **GNU General Public License v3.0 (GPL-3.0)**，完整条款见仓库根目录的 [`LICENSE`](LICENSE)。

- 你可以自由使用、修改、分发本项目的代码，但**衍生作品同样必须以 GPL-3.0 发布并公开源代码**。
- 分发二进制时需附带许可证文本与版权声明。
- **Minecraft 相关的任何内容（名称、商标、游戏文件、资源、代码）不在此许可范围内**，
  其版权归 Mojang Studios 所有。

> 本项目与 Mojang Studios / Microsoft 无关，未获其授权或背书。Minecraft 是 Mojang Studios 的商标。
