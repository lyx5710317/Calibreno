# 电子书转换

Windows x64 的转换引擎支持 TXT 与 EPUB 互转、普通 FB2 转 EPUB、无 DRM 的 MOBI 和 AZW3 转 EPUB、EPUB 转 AZW3、可重排 EPUB 横竖排互转，以及 EPUB 和 PDF 双向转换。EPUB 转 PDF 使用横排 A4，章节按原书顺序排印。PDF 转 EPUB 可提取文字层或使用离线 OCR，生成可重排的横排 EPUB。

转换入口可接收本地文件，也可以指定 Calibre 书库里的书籍 ID 和源格式。书库转换可把新格式挂到同一条书目。目标格式已存在或需要横竖排副本时，选择文件路径导出。

## 桌面应用

在书库中右键一本书，或打开书籍详情里的更多菜单，选择「转换书籍…」。在详情页对某个格式打开右键菜单，可以预选该源格式。转换本地文件可从侧栏「工具」或欢迎页进入。

PDF 可在转换对话框中选择原始阅读方向、识别语言和试转页码。试转结束后，逐页结果同时显示可缩放的 PDF 原页和可复制的识别文字，并标出待复核页。在该视图可跳到指定页或重跑一页或多页，返回转换对话框后可沿用记录完成整本转换。重新打开同一份原件也能访问已保存的逐页结果。

## 命令行

把 `book-conversion` 放在 Calibreno 可执行文件旁，或传入 `-runtime` 指定运行时位置。EPUB 转 AZW3 复用 Kindle USB 发送的组件，首次会联网下载和校验。

```powershell
.\Calibreno.exe convert ".\原书.txt" ".\原书.epub"
.\Calibreno.exe convert -layout vertical-rtl ".\横排.epub" ".\竖排.epub"
.\Calibreno.exe convert -library "D:\书库" -book 42 -from MOBI -to EPUB
.\Calibreno.exe convert -library "D:\书库" -book 42 -from EPUB -layout vertical-rtl ".\竖排副本.epub"
```

PDF 选页试转会保留原页图、识别文字与待复核记录。用相同工作区可以重跑所选页，或继续识别全书。续跑会校验源 PDF，并使用已保存的页面。页码按 PDF 文件的物理顺序计算。原页图保存在工作区的 `pdf-pages/document/previews` 目录。

```powershell
.\Calibreno.exe convert -workspace ".\识别记录" -pages "1,3-5" -pdf-layout vertical-rtl -pdf-language zh-Hant ".\扫描.pdf"
.\Calibreno.exe convert -workspace ".\识别记录" -pages "3" -ocr -pdf-layout vertical-rtl -pdf-language zh-Hant ".\扫描.pdf"
.\Calibreno.exe convert -workspace ".\识别记录" -pdf-layout vertical-rtl -pdf-language zh-Hant ".\扫描.pdf" ".\扫描.epub"
```

## 构建 Windows x64 运行时

在 Windows x64 上安装 Node.js 及 64 位 CPython 3.11。可用 `CALIBRENO_BUILD_PYTHON` 指定 CPython 解释器路径。将运行时准备到 MyGo 的 Windows x64 资源目录之后，再构建 Calibreno。

```powershell
Set-Location conversion-runtime
npm ci
node scripts/stage.mjs
Set-Location ..
go tool mygo build -platform windows/amd64 ./cmd/dalibre
```

转换代码复用 ZlibraryNo 的 MIT 实现。运行时保留该项目的许可证、Chromium 与 Electron 的许可声明、Noto 字体许可、libmobi 源码和 PDF OCR 依赖声明。 [转换运行时许可证](../conversion-runtime/LICENSE)
