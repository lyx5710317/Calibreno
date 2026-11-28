# 日文假名标注

Calibreno 自研 EPUB 工具。选中已有 EPUB 的日文书籍，在右键菜单打开「日文假名标注…」。生成结果会写回同一本书的 EPUB 格式，可以用原 EPUB 备份或撤销恢复。PDF、TXT、MOBI 等可先用「转换书籍」生成 EPUB。

| 注音范围 | 规则 |
| --- | --- |
| 全量注音（N5 参考） | 标注能确认读音的含汉字词。 |
| 标准注音（N2 参考） | 标注含小学汉字表以外汉字的词。默认选项。 |
| 生僻注音（N1 参考） | 标注含常用汉字表以外汉字的词。 |

插件离线按词确定读音，例如大人 → おとな，食べる → 食（た）べる。切换范围时只重算插件写入的注音，保留书里原有的注音。与正文标签交叉或没有词典读音的词可能跳过。首版处理可重排、UTF-8 编码的日文 EPUB 正文。

## 兼容性与构建

需要带插件资源读取接口的 Calibreno 0.8.2 或更新版本。从仓库的 `marketplace` 目录执行 `npm ci` 和 `node build.mjs japanese-furigana`，再用对应版本的 `dalibre-plugin check plugins/japanese-furigana` 检查。词典随插件包提供，编译时取自锁定的 npm 依赖。

## 数据来源

- 分词和读音来自 [kuromoji.js](https://github.com/takuyaa/kuromoji.js) 和 IPADIC。Apache 2.0 许可证、IPADIC 声明及 Double-Array Trie 的 MIT 许可证随插件包分发。
- 小学汉字来自[文部科学省《学年别汉字配当表》](https://www.mext.go.jp/content/20230120-mxt_kyoiku02-100002604_01.pdf)别表的 1026 字，使用[表格转录数据](https://github.com/Koichi-Kobayashi/KanjiVariants/blob/53eca025c740028722c54b90608b86a400265b16/data/Education/EducationKanjiGradeTable.txt)提取，按学年核对字数。
- 2136 个常用汉字来自 [Unihan 17.0.0](https://www.unicode.org/Public/17.0.0/ucd/Unihan.zip) 的 2010 年 `kJoyoKanji` 项，Unicode 数据许可见 `UNICODE-LICENSE.txt`。
