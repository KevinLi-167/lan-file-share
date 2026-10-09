# 第三方声明 / Third-Party Notices

本项目（LANFileShare）在**功能划分、页面布局与交互方式**上参考了下列开源项目。
参考项目自身的源代码未被复制到本项目中，但依据其许可条款，此处保留原始版权声明全文。

---

## tri5m/file-share

- 仓库：https://github.com/tri5m/file-share
- 用途：本项目前端页面的功能划分、页面布局与交互方式参考来源
- 许可：MIT License

```
MIT License

Copyright (c) 2026 Trifolium Wang <trifolium.wang@gmail.com>

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

---

## 开发期工具依赖（不随产物分发）

以下依赖仅用于**本地静态校验**，不参与编译，也不会出现在分发的 exe 中：

| 依赖 | 许可 | 用途 |
|---|---|---|
| [acorn](https://github.com/acornjs/acorn) | MIT | 在 `scripts/check-es5.mjs` 中按 ECMAScript 5 解析内联脚本 |
| [jsdom](https://github.com/jsdom/jsdom) | MIT | 在 `scripts/check-render.mjs` 中离线跑一遍页面渲染自检 |
