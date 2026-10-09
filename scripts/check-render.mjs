// 客户端页渲染自检
//
// 用途：不开浏览器、不开安卓，直接把 web/client.html 放进一个真实 DOM 实现里跑一遍，
// 验证脚本能正常执行、列表能正常渲染、HTML 转义和失效态都对。
//
// 这是为了拦住「语法没问题但运行时抛错」这类会导致老安卓上白屏的问题。
//
// 用法：
//   node scripts/check-render.mjs              使用内置的边界用例数据
//   node scripts/check-render.mjs --live       连本机正在跑的服务取真实数据
//   node scripts/check-render.mjs --live http://127.0.0.1:5421
//
// 依赖 jsdom。

import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, '..');
const require = createRequire(import.meta.url);

function loadJsdom() {
  const candidates = ['jsdom'];
  if (process.env.NODE_PATH) {
    for (const dir of process.env.NODE_PATH.split(path.delimiter)) {
      if (dir) {
        candidates.push(path.join(dir, 'jsdom'));
      }
    }
  }
  for (const candidate of candidates) {
    try {
      return require(candidate);
    } catch (error) {
      // 换下一个候选位置
    }
  }
  return null;
}

const jsdomModule = loadJsdom();
if (!jsdomModule) {
  console.error('缺少 jsdom 依赖，请先执行：');
  console.error('  npm install');
  process.exit(2);
}
const { JSDOM } = jsdomModule;

// 故意混入边界数据：HTML 注入、失效文件、超长中文名、空预览文本
const FIXTURE = {
  hostname: 'TEST-PC',
  items: [
    { id: 'f1', kind: 'file', name: '正常文件.pdf', size: 1536, source: 'admin', exists: true, createdAt: 1791500000 },
    { id: 'f2', kind: 'file', name: '已被删除的文件.txt', size: 12, source: 'admin', exists: false, createdAt: 1791500001 },
    { id: 'f3', kind: 'file', name: '<img src=x onerror=alert(1)>.txt', size: 100, source: 'client', exists: true, createdAt: 1791500002 },
    { id: 'f4', kind: 'file', name: '一个非常长的中文文件名用来测试在窄屏幕上的换行表现.doc', size: 4 * 1024 * 1024 * 1024, source: 'client', exists: true, createdAt: 1791500003 },
    { id: 't1', kind: 'text', name: '这是一条文本预览', size: 33, source: 'client', text: '这是一条文本预览，用来验证字符数统计', exists: true, createdAt: 1791500004 },
    { id: 't2', kind: 'text', name: '带"引号"和\'单引号\'的文本', size: 20, source: 'admin', text: '带"引号"和\'单引号\'的文本', exists: true, createdAt: 1791500005 },
  ],
};

const args = process.argv.slice(2);
const liveIndex = args.indexOf('--live');
const liveBase = liveIndex >= 0 ? (args[liveIndex + 1] || 'http://127.0.0.1:5421') : null;

async function loadPayload() {
  if (!liveBase) {
    return FIXTURE;
  }
  const response = await fetch(liveBase + '/api/items');
  if (!response.ok) {
    throw new Error(`取 /api/items 失败：HTTP ${response.status}`);
  }
  return await response.json();
}

const payload = await loadPayload();

const html = fs.readFileSync(path.join(root, 'web/client.html'), 'utf8');
const adminHtml = fs
  .readFileSync(path.join(root, 'web/admin.html'), 'utf8')
  .replace(/__FS_TOKEN__/g, 'test-token');

const failures = [];
const pageErrors = [];

const dom = new JSDOM(html, {
  runScripts: 'dangerously',
  pretendToBeVisual: true,
  url: 'http://127.0.0.1:15421/client.html',
  beforeParse(window) {
    // 用真实的服务端返回替掉网络层，这样页面脚本走的是完整逻辑，只是不真的发请求
    function FakeXHR() {
      this.readyState = 0;
      this.status = 0;
      this.responseText = '';
      this.onreadystatechange = null;
    }
    FakeXHR.prototype.open = function (method, url) {
      this._method = method;
      this._url = url;
      this.readyState = 1;
    };
    FakeXHR.prototype.setRequestHeader = function () {};
    FakeXHR.prototype.send = function () {
      const self = this;
      const isItems = String(self._url).indexOf('/api/items') === 0;
      Promise.resolve().then(() => {
        self.readyState = 4;
        self.status = isItems ? 200 : 200;
        self.responseText = isItems ? JSON.stringify(payload) : '{"ok":true}';
        if (self.onreadystatechange) {
          self.onreadystatechange();
        }
      });
    };
    window.XMLHttpRequest = FakeXHR;

    window.onerror = function (message) {
      pageErrors.push(String(message));
    };
    const originalError = window.console.error;
    window.console.error = function () {
      pageErrors.push(Array.prototype.slice.call(arguments).join(' '));
      if (originalError) {
        originalError.apply(window.console, arguments);
      }
    };
  },
});

// 等页面里的轮询和渲染回调跑完
await new Promise((resolve) => setTimeout(resolve, 300));

const document = dom.window.document;
const listBox = document.getElementById('listBox');
const rendered = listBox ? listBox.innerHTML : '';

if (pageErrors.length > 0) {
  failures.push(`页面脚本抛出错误：${pageErrors.join(' | ')}`);
}

if (!rendered) {
  failures.push('listBox 是空的，列表没有渲染出来');
}

const rowCount = (rendered.match(/<tr/g) || []).length;
const expectedRows = payload.items.length + 1; // 含表头
if (rendered && rowCount !== expectedRows) {
  failures.push(`渲染出 ${rowCount} 个 tr，期望 ${expectedRows} 个（${payload.items.length} 条数据 + 1 行表头）`);
}

// 失效文件必须没有下载链接
if (rendered.indexOf('/api/download/f2') >= 0) {
  failures.push('失效文件 f2 不应该有下载链接');
}
if (rendered.indexOf('文件已失效') < 0) {
  failures.push('失效文件没有显示「文件已失效」提示');
}

// 可用文件必须带下载链接
for (const id of ['f1', 'f3', 'f4']) {
  if (rendered.indexOf('/api/download/' + id) < 0) {
    failures.push(`文件 ${id} 缺少下载链接`);
  }
}

// 文件名里的 HTML 必须被转义，不能变成真实标签
if (rendered.indexOf('<img src=x') >= 0) {
  failures.push('文件名里的 HTML 没有被转义（存在注入风险）');
}
if (rendered.indexOf('&lt;img src=x') < 0) {
  failures.push('文件名里的尖括号没有被转义成实体');
}

// 文本条目要有复制按钮，且大小列显示字数而不是字节
if (rendered.toLowerCase().indexOf('cop ytext') >= 0) {
  failures.push('复制按钮的函数名被写错了');
}
if (rendered.indexOf('FS.copyText(') < 0) {
  failures.push('文本条目缺少复制按钮');
}
if (rendered.indexOf('字</td>') < 0) {
  failures.push('文本条目的大小列没有显示字数');
}

// 主机名要显示出来
const hostNode = document.getElementById('hostName');
if (!hostNode || hostNode.innerHTML.indexOf(payload.hostname) < 0) {
  failures.push(`主机名没有渲染出来，期望包含 ${payload.hostname}`);
}

console.log('--- 渲染出来的列表（前 900 字符）---');
console.log(rendered.slice(0, 900));
console.log('');

if (failures.length > 0) {
  for (const failure of failures) {
    console.log(`[失败] ${failure}`);
  }
  dom.window.close();
  process.exit(1);
}

console.log(`[通过] client.html 渲染正常，共 ${payload.items.length} 条数据`);
dom.window.close();

// ---------------------------------------------------------------- 管理页

const adminFailures = [];
const adminErrors = [];

const adminDom = new JSDOM(adminHtml, {
  runScripts: 'dangerously',
  pretendToBeVisual: true,
  url: 'http://127.0.0.1:15421/admin',
  beforeParse(window) {
    function FakeXHR() {
      this.readyState = 0;
      this.status = 0;
      this.responseText = '';
    }
    FakeXHR.prototype.open = function (method, url) {
      this._method = method;
      this._url = url;
      this.readyState = 1;
    };
    FakeXHR.prototype.setRequestHeader = function (name, value) {
      if (name === 'X-FS-Token' && value !== 'test-token') {
        adminErrors.push('管理页没有带上正确令牌');
      }
    };
    FakeXHR.prototype.send = function () {
      const self = this;
      const url = String(self._url);
      Promise.resolve().then(() => {
        self.readyState = 4;
        self.status = 200;
        if (url.indexOf('/api/admin/info') === 0) {
          self.responseText = JSON.stringify({
            hostname: 'TEST-PC',
            version: '1.0.0',
            port: 5421,
            uploadDir: 'C:\\apps\\uploads',
            dataFile: 'C:\\apps\\data\\items.json',
            exeDir: 'C:\\apps',
            maxUploadGB: 4,
            urls: ['http://192.168.1.23:5421/client.html'],
            items: payload.items.map((item) => Object.assign({ path: 'C:\\apps\\file.txt' }, item)),
          });
        } else {
          self.responseText = JSON.stringify({ items: [] });
        }
        if (self.onreadystatechange) {
          self.onreadystatechange();
        }
      });
    };
    window.XMLHttpRequest = FakeXHR;
    window.onerror = function (message) {
      adminErrors.push(String(message));
    };
    window.confirm = function () { return true; };
    window.prompt = function () { return null; };
  },
});

await new Promise((resolve) => setTimeout(resolve, 300));

const adminDocument = adminDom.window.document;
const adminRendered = adminDocument.getElementById('listBox')
  ? adminDocument.getElementById('listBox').innerHTML
  : '';

if (adminErrors.length > 0) {
  adminFailures.push(`管理页脚本抛出错误：${adminErrors.join(' | ')}`);
}
if (!adminRendered) {
  adminFailures.push('管理页 listBox 是空的，列表没有渲染出来');
}
if (adminRendered && adminRendered.indexOf('已失效') < 0) {
  adminFailures.push('管理页没有标出失效条目');
}
if (adminRendered && adminRendered.indexOf('ADMIN.remove(') < 0) {
  adminFailures.push('管理页缺少移除按钮');
}

const bigUrlNode = adminDocument.getElementById('bigUrl');
if (!bigUrlNode || bigUrlNode.value !== 'http://192.168.1.23:5421/client.html') {
  adminFailures.push(`管理页访问链接不正确：${bigUrlNode ? bigUrlNode.value : '(元素缺失)'}`);
}
const addrSelect = adminDocument.getElementById('addrSelect');
if (!addrSelect || addrSelect.innerHTML.indexOf('192.168.1.23') < 0) {
  adminFailures.push('管理页地址下拉没有填充');
}
if (adminRendered && adminRendered.indexOf('C:\\apps\\file.txt') < 0) {
  adminFailures.push('管理页没有显示本机磁盘路径');
}

if (adminFailures.length > 0) {
  for (const failure of adminFailures) {
    console.log(`[失败] ${failure}`);
  }
  adminDom.window.close();
  process.exit(1);
}

console.log('[通过] admin.html 渲染正常');
adminDom.window.close();
process.exit(0);
